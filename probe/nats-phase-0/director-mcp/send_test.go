package main

import (
	"encoding/json"
	"testing"
)

// R-88 leans on a reply carrying in_reply_to equal to the nonce it answers, and
// the shim copies in_reply_to into CorrelationID. The send_message schema names
// the argument "in_reply_to" (with an underscore), so the struct field needs a
// json tag: Go's case-insensitive match does not bridge the underscore, and
// without the tag in_reply_to and reply_by silently drop and CorrelationID
// never sets. This pins that they bind, so a receipt can be correlated.
func TestSendArgsBindInReplyToForReceiptCorrelation(t *testing.T) {
	self := Sender{AgentID: "builder", Team: "fleet", Workspace: "ops2"}
	raw := json.RawMessage(`{
		"to": "agent://fleet/director",
		"performative": "AGREE",
		"text": "ack",
		"in_reply_to": "nonce-123",
		"reply_by": "2026-09-14T00:00:00Z"
	}`)

	e, wsHint, err := buildSendEnvelope(self, raw)
	if err != nil {
		t.Fatalf("buildSendEnvelope: %v", err)
	}
	if e.InReplyTo != "nonce-123" {
		t.Fatalf("in_reply_to did not bind: InReplyTo = %q, want nonce-123 (missing json tag?)", e.InReplyTo)
	}
	if e.CorrelationID != "nonce-123" {
		t.Fatalf("CorrelationID not set from in_reply_to: %q; R-88 receipt correlation would break", e.CorrelationID)
	}
	if e.ReplyBy != "2026-09-14T00:00:00Z" {
		t.Fatalf("reply_by did not bind: %q", e.ReplyBy)
	}
	if wsHint != "" {
		t.Fatalf("no workspace argument given, wsHint should be empty, got %q", wsHint)
	}
}

// With no in_reply_to, CorrelationID stays empty rather than picking up a stray
// value, so a fresh (non-reply) message is not spuriously correlated.
func TestSendArgsNoInReplyToLeavesCorrelationEmpty(t *testing.T) {
	self := Sender{AgentID: "builder", Team: "fleet", Workspace: "ops2"}
	raw := json.RawMessage(`{"to":"agent://fleet/director","performative":"INFORM","text":"hello"}`)

	e, _, err := buildSendEnvelope(self, raw)
	if err != nil {
		t.Fatalf("buildSendEnvelope: %v", err)
	}
	if e.InReplyTo != "" || e.CorrelationID != "" {
		t.Fatalf("a non-reply set correlation fields: in_reply_to=%q correlation_id=%q", e.InReplyTo, e.CorrelationID)
	}
}
