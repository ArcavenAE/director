package main

// The tool catalog and dispatch. Six tools: the five from the spbc ticket
// (send_message, wait_for_message, list_roster, set_presence, broadcast) and
// inbox_summary. Each is request/response; wait_for_message is the long-poll
// that answers the push-vs-poll question, and its max argument drains a
// backlog in one call (drain.go).

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/oklog/ulid/v2"
)

// toolCatalog describes the tools this session actually has. The global forms
// appear only when the launcher turned the global tier on, so a session with
// no hub is never told about addresses it cannot route (gcfg nil = off).
func toolCatalog(gcfg *globalConfig) []toolDef {
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	toDesc := "recipient address: agent://{team}/{id}, role://{team}/{role}, or broadcast://{workspace}[/{team}]"
	sendDesc := "Send a director envelope to another session or role. Returns accepted-for-delivery with a message_id; this is a send acknowledgement, not a delivery or read receipt."
	waitDesc := "Block up to timeout_seconds for the next message addressed to this session, then return it. This is a poll: the model must call it. The server cannot push a message into context on its own. With max greater than 1 it returns every waiting message up to max in one call, oldest first, and consumes them as the single form does; run inbox_summary first to see what is waiting without consuming it."
	summaryDesc := "Summarize the messages waiting for this session without consuming any: counts by sender and by performative, the REQUEST, FAILURE and QUERY messages and any that set reply_by, say they hold custody, or await a reply, and the waiting sequence numbers oldest first. Acks nothing; drain in order afterwards with wait_for_message max=N."
	rosterDesc := "List the sessions currently present, from the presence store. Absence means silence, not a negative report."
	if gcfg != nil {
		toDesc += ", or across hosts global://director and global://{cluster}/supervisor. A global send is refused before publish when nobody is live at that address."
		sendDesc += " This session is " + gcfg.selfAddress() + " at the global tier: reply to a global message with global://director, and name a cluster (list_roster shows them) to reach its supervisor."
		waitDesc += " It polls the local inbox and this session's global inbox, and the result names the tier the message came from. A batch lists local messages first, then global; sequence numbers order messages within a tier only."
		summaryDesc += " Covers both the local inbox and this session's global inbox."
		rosterDesc += " Rows from both tiers are merged and carry a tier column; global rows carry the cluster and role that address them."
	}
	return []toolDef{
		{
			Name:        "send_message",
			Description: sendDesc,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"to":           str(toDesc),
					"performative": str("one of INFORM REQUEST AGREE REFUSE FAILURE CFP PROPOSE ACCEPT-PROPOSAL REJECT-PROPOSAL CANCEL QUERY NOT-UNDERSTOOD"),
					"text":         str("the message body"),
					"refs":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "pointers: bd:, finding:, file:, url:, pr:"},
					"in_reply_to":  str("optional message_id being answered"),
					"reply_by":     str("optional RFC3339 deadline"),
					"workspace":    str("optional recipient workspace. Omit and it is resolved from the recipient's live presence; a send to a recipient with no live presence is then refused, not silently misdelivered. Set it to address a known cold mailbox (no live presence) verbatim. Ignored for a global:// address, whose subject carries no workspace."),
				},
				"required": []string{"to", "performative", "text"},
			},
		},
		{
			Name:        "wait_for_message",
			Description: waitDesc,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"timeout_seconds": map[string]any{"type": "integer", "description": "how long to wait when nothing is already waiting (default 30, max 120)"},
					"max":             map[string]any{"type": "integer", "description": "most messages to return in one call (default 1, max 50). Above 1, waiting messages return at once, oldest first."},
				},
			},
		},
		{
			Name:        "inbox_summary",
			Description: summaryDesc,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit": map[string]any{"type": "integer", "description": "most waiting messages to read per tier (default 200, max 500); totals past it still come from consumer state"},
				},
			},
		},
		{
			Name:        "list_roster",
			Description: rosterDesc,
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
					"team":      str("optional team; omit for whole workspace"),
					"text":      str("the message body"),
					"workspace": str("optional target workspace; omit to broadcast to your own"),
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
	case "inbox_summary":
		return toolSummary(ctx, bus, rawArgs)
	case "list_roster":
		return toolRoster(ctx, bus)
	case "set_presence":
		return toolPresence(ctx, bus, rawArgs)
	case "broadcast":
		return toolBroadcast(ctx, bus, rawArgs)
	}
	return nil, errors.New("unknown tool: " + name)
}

func newEnvelope(self Sender) *Envelope {
	id := ulid.Make().String()
	e := &Envelope{
		SchemaVersion:  1,
		MessageID:      id,
		ConversationID: "cid-" + id,
		Sender:         self,
		SentAt:         time.Now().UTC().Format(time.RFC3339),
	}
	e.Sender.Principal = nil // Phase 0: envelope validates with principal null
	return e
}

// sendArgs are the send_message tool arguments. in_reply_to and reply_by carry
// json tags because Go's case-insensitive field match does not bridge the
// underscore in the schema key to the CamelCase field: without the tag both
// would silently drop, and a reply would lose the in_reply_to that R-88
// receipt correlation reads (in_reply_to -> CorrelationID below). to,
// performative, text and refs bind without a tag, having no underscore.
type sendArgs struct {
	To           string
	Performative string
	Text         string
	InReplyTo    string `json:"in_reply_to"`
	ReplyBy      string `json:"reply_by"`
	Workspace    string `json:"workspace"`
	Refs         []string
}

// buildSendEnvelope parses and validates send_message args into an envelope,
// returning the optional workspace hint separately. Split from toolSend so the
// arg binding, notably in_reply_to -> CorrelationID (R-88 receipt
// correlation), is testable without a broker.
func buildSendEnvelope(self Sender, raw json.RawMessage) (*Envelope, string, error) {
	var a sendArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, "", err
	}
	e := newEnvelope(self)
	e.Recipient.Address = a.To
	e.Performative = a.Performative
	e.Content = Content{Type: "text", Data: a.Text, Refs: a.Refs}
	e.InReplyTo = a.InReplyTo
	e.ReplyBy = a.ReplyBy
	if a.InReplyTo != "" {
		e.CorrelationID = a.InReplyTo
	}
	if err := e.validate(); err != nil {
		return nil, "", err
	}
	return e, a.Workspace, nil
}

func toolSend(ctx context.Context, bus *Bus, raw json.RawMessage) (any, error) {
	e, wsHint, err := buildSendEnvelope(bus.self, raw)
	if err != nil {
		return nil, err
	}
	res, err := bus.publish(ctx, e, wsHint)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"status":     "accepted for delivery",
		"message_id": e.MessageID,
		"tier":       res.Tier,
		"note":       "accepted is not delivered or read; the recipient reports those (R-08)",
	}
	// The stream and sequence are the hub's (or the local broker's) own word
	// that the bytes are stored. It matters most at the global tier, where a
	// leaf-side sender is not permitted to read the director's stream back.
	if res.Stream != "" {
		out["stream"] = res.Stream
		out["sequence"] = res.Sequence
	}
	return out, nil
}

// waitArgs are the wait_for_message arguments after defaults and caps.
type waitArgs struct {
	TimeoutSeconds int `json:"timeout_seconds"`
	Max            int `json:"max"`
}

func parseWaitArgs(raw json.RawMessage) waitArgs {
	var a waitArgs
	_ = json.Unmarshal(raw, &a)
	if a.TimeoutSeconds <= 0 {
		a.TimeoutSeconds = 30
	}
	if a.TimeoutSeconds > 120 {
		a.TimeoutSeconds = 120
	}
	if a.Max <= 0 {
		a.Max = 1
	}
	if a.Max > maxDrain {
		a.Max = maxDrain
	}
	return a
}

func toolWait(ctx context.Context, bus *Bus, raw json.RawMessage) (any, error) {
	a := parseWaitArgs(raw)
	if a.Max > 1 {
		return toolWaitBatch(ctx, bus, a)
	}
	res, err := bus.receiveTiered(ctx, time.Duration(a.TimeoutSeconds)*time.Second)
	if err != nil && !errors.Is(err, jetstream.ErrNoMessages) {
		return nil, err
	}
	out := map[string]any{"message": res.Env}
	if res.Env == nil {
		out["note"] = "no message within the window; this is silence, not failure"
	} else {
		out["tier"] = res.Tier
	}
	// A global-tier failure is reported beside the answer rather than instead
	// of it: the local tier keeps working through a hub outage, so the poll
	// does too, and the caller still hears that the hub did not answer.
	if res.GlobalWarn != "" {
		out["global_warning"] = res.GlobalWarn
	}
	return out, nil
}

// toolWaitBatch is wait_for_message with max > 1. The single form keeps its
// result shape exactly, so existing callers see no change.
func toolWaitBatch(ctx context.Context, bus *Bus, a waitArgs) (any, error) {
	res, err := bus.receiveBatch(ctx, time.Duration(a.TimeoutSeconds)*time.Second, a.Max)
	if err != nil {
		return nil, err
	}
	items := res.Items
	if items == nil {
		items = []drained{}
	}
	out := map[string]any{
		"messages": items,
		"count":    len(items),
		"order":    "oldest first within each tier; local before global",
	}
	if len(items) == 0 {
		out["note"] = "no message within the window; this is silence, not failure"
	} else {
		out["remaining"] = bus.remaining(ctx)
	}
	if res.Discarded > 0 {
		out["discarded"] = res.Discarded
		out["discarded_note"] = "undecodable messages were terminated and skipped so the rest of the batch could return"
	}
	if res.GlobalWarn != "" {
		out["global_warning"] = res.GlobalWarn
	}
	return out, nil
}

func toolSummary(ctx context.Context, bus *Bus, raw json.RawMessage) (any, error) {
	var a struct {
		Limit int `json:"limit"`
	}
	_ = json.Unmarshal(raw, &a)
	if a.Limit <= 0 {
		a.Limit = summaryDefault
	}
	if a.Limit > summaryMax {
		a.Limit = summaryMax
	}
	res, err := bus.summarizeInbox(ctx, a.Limit)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"summary": res.Summary,
		"waiting": res.Expected,
		"read":    res.Read,
		"note":    "nothing was acked; drain in order with wait_for_message max=N",
	}
	for tier, exp := range res.Expected {
		if uint64(res.Read[tier]) < exp {
			out["partial"] = "fewer messages were read than the consumer reports waiting (limit reached, or read raced a drain); the counts cover what was read"
		}
	}
	if res.GlobalWarn != "" {
		out["global_warning"] = res.GlobalWarn
	}
	return out, nil
}

func toolRoster(ctx context.Context, bus *Bus) (any, error) {
	rows, globalWarn, err := bus.rosterMerged(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"present": rows, "count": len(rows)}
	if globalWarn != "" {
		out["global_warning"] = globalWarn
	}
	return out, nil
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
	globalWarn, err := bus.setPresence(ctx, a.State)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"status": "presence recorded", "state": a.State}
	if globalWarn != "" {
		out["global_warning"] = globalWarn
	}
	return out, nil
}

func toolBroadcast(ctx context.Context, bus *Bus, raw json.RawMessage) (any, error) {
	var a struct {
		Team, Text string
		Workspace  string `json:"workspace"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	// The broadcast workspace goes into the address; omit it to broadcast to
	// your own workspace, set it to reach another. resolveSubject builds the
	// subject from the address's workspace, so no roster hint is needed here.
	ws := a.Workspace
	if ws == "" {
		ws = bus.self.Workspace
	}
	e := newEnvelope(bus.self)
	if a.Team == "" {
		e.Recipient.Address = "broadcast://" + ws
	} else {
		e.Recipient.Address = "broadcast://" + ws + "/" + a.Team
	}
	e.Recipient.Team = a.Team
	e.Performative = "INFORM"
	e.Content = Content{Type: "text", Data: a.Text}
	if err := e.validate(); err != nil {
		return nil, err
	}
	if _, err := bus.publish(ctx, e, ""); err != nil {
		return nil, err
	}
	return map[string]any{"status": "broadcast sent", "message_id": e.MessageID}, nil
}
