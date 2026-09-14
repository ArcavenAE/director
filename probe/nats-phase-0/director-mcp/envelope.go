package main

// Director envelope v1. Matches
// aae-orc/docs/design/director-envelope-and-adapter-events.md section 2.
// This is the probe cut: the fields the shim reads or writes, validated with
// principal null (the identity plane attaches later, aae-orc-mqwk).

import (
	"errors"
	"strings"
)

type Sender struct {
	AgentID   string `json:"agent_id"`
	Role      string `json:"role,omitempty"`
	Workspace string `json:"workspace"`
	Session   string `json:"session,omitempty"`
	Principal any    `json:"principal"` // RESERVED, null in Phase 0
	Team      string `json:"-"`         // in-process routing only; not an envelope field (team lives on recipient)
}

type Recipient struct {
	Address string `json:"address"` // agent://{team}/{id} | role://{team}/{role} | broadcast://{ws}[/{team}]
	Team    string `json:"team,omitempty"`
}

type Content struct {
	Type string   `json:"type"` // text | task | result | signal | pointer
	Data string   `json:"data"`
	Refs []string `json:"refs,omitempty"`
}

type Envelope struct {
	SchemaVersion  int       `json:"schema_version"`
	MessageID      string    `json:"message_id"`
	CorrelationID  string    `json:"correlation_id,omitempty"`
	ConversationID string    `json:"conversation_id,omitempty"`
	InReplyTo      string    `json:"in_reply_to,omitempty"`
	Sender         Sender    `json:"sender"`
	Recipient      Recipient `json:"recipient"`
	Performative   string    `json:"performative"`
	Content        Content   `json:"content"`
	ReplyBy        string    `json:"reply_by,omitempty"`
	ExpiresAt      string    `json:"expires_at,omitempty"`
	SentAt         string    `json:"sent_at"`
	Trace          struct {
		OtelTraceparent any `json:"otel_traceparent"`
	} `json:"trace"`
}

// The v1 performative subset (envelope doc section 2.2). Kept as a set so the
// shim rejects a typo rather than putting an unroutable verb on the bus.
var performatives = map[string]bool{
	"INFORM": true, "REQUEST": true, "AGREE": true, "REFUSE": true,
	"FAILURE": true, "CFP": true, "PROPOSE": true, "ACCEPT-PROPOSAL": true,
	"REJECT-PROPOSAL": true, "CANCEL": true, "QUERY": true, "NOT-UNDERSTOOD": true,
}

const maxEnvelopeBytes = 65536 // section 2.4: 64 KiB, larger goes by pointer

func (e *Envelope) validate() error {
	if e.SchemaVersion != 1 {
		return errors.New("schema_version must be 1")
	}
	if e.MessageID == "" {
		return errors.New("message_id required")
	}
	if !performatives[e.Performative] {
		return errors.New("unknown performative: " + e.Performative)
	}
	if e.Recipient.Address == "" {
		return errors.New("recipient.address required")
	}
	if !strings.HasPrefix(e.Recipient.Address, "agent://") &&
		!strings.HasPrefix(e.Recipient.Address, "role://") &&
		!strings.HasPrefix(e.Recipient.Address, "broadcast://") {
		return errors.New("recipient.address must be agent:// role:// or broadcast://")
	}
	if err := e.Content.validate(); err != nil {
		return err
	}
	return nil
}

// bodyBearing is the set of content types whose whole purpose is to carry a
// body in content.data. A pointer carries refs instead; a signal is a bare
// notification and may carry neither.
var bodyBearing = map[string]bool{"text": true, "task": true, "result": true}

// validate rejects a content block that would go on the bus empty. An envelope
// accepted for delivery with no body is silent loss, the failure class the
// director layer exists to prevent, so a body-bearing type with empty data is
// refused at the send boundary and surfaces to the caller as an error rather
// than a message_id. See the empty-body regression, 2026-09-13.
func (c *Content) validate() error {
	if c.Type == "" {
		return errors.New("content.type required")
	}
	if bodyBearing[c.Type] && c.Data == "" {
		return errors.New("content.data is empty for type " + c.Type + "; a body-bearing message must not be accepted for delivery empty (silent loss)")
	}
	if c.Type == "pointer" && len(c.Refs) == 0 {
		return errors.New("content.type is pointer but content.refs is empty; a pointer message must carry at least one ref")
	}
	return nil
}
