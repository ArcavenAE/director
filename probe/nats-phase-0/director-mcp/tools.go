package main

// The tool catalog and dispatch. Five tools, matching the spbc ticket:
// send_message, wait_for_message, list_roster, set_presence, broadcast.
// Each is request/response; wait_for_message is the long-poll that answers
// the push-vs-poll question.

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/arcavenae/marvel/contracts/go/envelope"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/oklog/ulid/v2"
)

func toolCatalog() []toolDef {
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	return []toolDef{
		{
			Name:        "send_message",
			Description: "Send a director envelope to another session or role. Returns accepted-for-delivery with a message_id; this is a send acknowledgement, not a delivery or read receipt.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"to":           str("recipient address: agent://{team}/{id}, role://{team}/{role}, or broadcast://{workspace}[/{team}]"),
					"performative": str("one of INFORM REQUEST AGREE REFUSE FAILURE CFP PROPOSE ACCEPT-PROPOSAL REJECT-PROPOSAL CANCEL QUERY NOT-UNDERSTOOD"),
					"text":         str("the message body"),
					"refs":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "pointers: bd:, finding:, file:, url:, pr:"},
					"in_reply_to":  str("optional message_id being answered"),
					"reply_by":     str("optional RFC3339 deadline"),
				},
				"required": []string{"to", "performative", "text"},
			},
		},
		{
			Name:        "wait_for_message",
			Description: "Block up to timeout_seconds for the next message addressed to this session, then return it. This is a poll: the model must call it. The server cannot push a message into context on its own.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"timeout_seconds": map[string]any{"type": "integer", "description": "how long to wait (default 30, max 120)"},
				},
			},
		},
		{
			Name:        "list_roster",
			Description: "List the sessions currently present, from the presence store. Absence means silence, not a negative report.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			Name:        "set_presence",
			Description: "Record this session's presence heartbeat (state, e.g. idle or busy). Must be repeated within the presence window or the entry expires.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"state": str("idle | busy | away")},
				"required":   []string{"state"},
			},
		},
		{
			Name:        "broadcast",
			Description: "Send an INFORM to every session in the workspace or a team. No durable queue: late joiners do not replay it.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"team": str("optional team; omit for whole workspace"),
					"text": str("the message body"),
				},
				"required": []string{"text"},
			},
		},
	}
}

func dispatchTool(ctx context.Context, bus *Bus, name string, rawArgs json.RawMessage) (any, error) {
	switch name {
	case "send_message":
		return toolSend(ctx, bus, rawArgs)
	case "wait_for_message":
		return toolWait(ctx, bus, rawArgs)
	case "list_roster":
		return toolRoster(ctx, bus)
	case "set_presence":
		return toolPresence(ctx, bus, rawArgs)
	case "broadcast":
		return toolBroadcast(ctx, bus, rawArgs)
	}
	return nil, errors.New("unknown tool: " + name)
}

func newEnvelope(self Sender) *envelope.Envelope {
	id := ulid.Make().String()
	cid := "cid-" + id
	s := envelope.Sender{AgentID: self.AgentID, Workspace: self.Workspace}
	if self.Role != "" {
		role := self.Role
		s.Role = &role
	}
	// Principal stays nil (reserved, R-53) and authority states none with a null
	// seat: an agent speaking on its own behalf through the phase-0 shim holds no
	// seat, so it carries no authority and says so rather than inferring one
	// (R-02, R-03). direct and relayed are seat-bearing and arrive with the
	// identity-lane seat work; the shim never emits them yet.
	return &envelope.Envelope{
		SchemaVersion:  1,
		MessageID:      id,
		ConversationID: &cid,
		Sender:         s,
		Authority:      envelope.Authority{Strength: envelope.StrengthNone, Seat: nil},
		SentAt:         time.Now().UTC(),
	}
}

// bodyBearing content types exist to carry a body in content.data. The schema
// makes data optional (a signal carries none, a pointer carries refs), so an
// empty text/task/result body is schema-valid yet worthless on the wire: an
// envelope accepted for delivery with no body is silent loss, the class this
// layer exists to prevent. This policy is enforced on emit, beside schema
// validation, not by it (2026-09-13 empty-body regression).
var bodyBearing = map[envelope.Type]bool{
	envelope.TypeText:   true,
	envelope.TypeTask:   true,
	envelope.TypeResult: true,
}

func checkEmitPolicy(e *envelope.Envelope) error {
	if bodyBearing[e.Content.Type] {
		body, _ := e.Content.Data.(string)
		if body == "" {
			return errors.New("content.data is empty for type " + string(e.Content.Type) + "; a body-bearing message must not be accepted for delivery empty (silent loss)")
		}
	}
	if e.Content.Type == envelope.TypePointer && len(e.Content.Refs) == 0 {
		return errors.New("content.type is pointer but content.refs is empty; a pointer message must carry at least one ref")
	}
	return nil
}

func toolSend(ctx context.Context, bus *Bus, raw json.RawMessage) (any, error) {
	var a struct {
		To, Performative, Text, InReplyTo, ReplyBy string
		Refs                                       []string
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	e := newEnvelope(bus.self)
	e.Recipient.Address = a.To
	e.Performative = envelope.Performative(a.Performative)
	e.Content = envelope.Content{Type: envelope.TypeText, Data: a.Text, Refs: a.Refs}
	if a.InReplyTo != "" {
		e.InReplyTo = &a.InReplyTo
		corr := a.InReplyTo
		e.CorrelationID = &corr
	}
	if a.ReplyBy != "" {
		t, err := time.Parse(time.RFC3339, a.ReplyBy)
		if err != nil {
			return nil, errors.New("reply_by must be RFC3339: " + err.Error())
		}
		e.ReplyBy = &t
	}
	if err := bus.publish(ctx, e); err != nil {
		return nil, err
	}
	return map[string]any{
		"status":     "accepted for delivery",
		"message_id": e.MessageID,
		"note":       "accepted is not delivered or read; the recipient reports those (R-08)",
	}, nil
}

func toolWait(ctx context.Context, bus *Bus, raw json.RawMessage) (any, error) {
	var a struct {
		TimeoutSeconds int `json:"timeout_seconds"`
	}
	_ = json.Unmarshal(raw, &a)
	if a.TimeoutSeconds <= 0 {
		a.TimeoutSeconds = 30
	}
	if a.TimeoutSeconds > 120 {
		a.TimeoutSeconds = 120
	}
	e, err := bus.receive(ctx, time.Duration(a.TimeoutSeconds)*time.Second)
	if err != nil && !errors.Is(err, jetstream.ErrNoMessages) {
		return nil, err
	}
	if e == nil {
		return map[string]any{"message": nil, "note": "no message within the window; this is silence, not failure"}, nil
	}
	return map[string]any{"message": e}, nil
}

func toolRoster(ctx context.Context, bus *Bus) (any, error) {
	r, err := bus.roster(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"present": r, "count": len(r)}, nil
}

func toolPresence(ctx context.Context, bus *Bus, raw json.RawMessage) (any, error) {
	var a struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if a.State == "" {
		a.State = "idle"
	}
	if err := bus.setPresence(ctx, a.State); err != nil {
		return nil, err
	}
	return map[string]any{"status": "presence recorded", "state": a.State}, nil
}

func toolBroadcast(ctx context.Context, bus *Bus, raw json.RawMessage) (any, error) {
	var a struct {
		Team, Text string
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	e := newEnvelope(bus.self)
	if a.Team == "" {
		e.Recipient.Address = "broadcast://" + bus.self.Workspace
	} else {
		e.Recipient.Address = "broadcast://" + bus.self.Workspace + "/" + a.Team
	}
	if a.Team != "" {
		team := a.Team
		e.Recipient.Team = &team
	}
	e.Performative = envelope.PerformativeINFORM
	e.Content = envelope.Content{Type: envelope.TypeText, Data: a.Text}
	if err := bus.publish(ctx, e); err != nil {
		return nil, err
	}
	return map[string]any{"status": "broadcast sent", "message_id": e.MessageID}, nil
}
