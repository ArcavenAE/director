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

// A misspelled send_message argument used to be dropped, so the caller saw
// "content.data is empty for type text" and looked at an envelope field it
// never set. An unknown argument now fails at the parse and is named.
func TestSendArgsRefuseUnknownArgumentByName(t *testing.T) {
	self := Sender{AgentID: "builder", Team: "fleet", Workspace: "ops2"}
	raw := json.RawMessage(`{"to":"agent://fleet/director","performative":"INFORM","content":"hello"}`)

	e, _, err := buildSendEnvelope(self, raw)
	if err == nil {
		t.Fatalf("args carrying content and no text were accepted: envelope %+v", e)
	}
	if e != nil {
		t.Fatalf("an unknown argument is refused before any envelope exists, got %+v", e)
	}
	want := `send_message: unknown argument "content" (want text)`
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

func TestSendArgsRefuseUnknownArgumentListsValidNames(t *testing.T) {
	self := Sender{AgentID: "builder", Team: "fleet", Workspace: "ops2"}
	raw := json.RawMessage(`{"to":"agent://fleet/director","performative":"INFORM","text":"hi","priority":"high"}`)

	_, _, err := buildSendEnvelope(self, raw)
	if err == nil {
		t.Fatal("an unknown argument alongside valid ones was accepted")
	}
	want := `send_message: unknown argument "priority" (valid: to, performative, text, refs, in_reply_to, reply_by, workspace)`
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

// Every argument the schema names still binds, so the refusal narrows nothing
// a valid caller sends.
func TestSendArgsAllSchemaArgumentsStillPass(t *testing.T) {
	self := Sender{AgentID: "builder", Team: "fleet", Workspace: "ops2"}
	raw := json.RawMessage(`{"to":"agent://fleet/director","performative":"INFORM","text":"hi","refs":["pr:1"],"in_reply_to":"n1","reply_by":"2026-09-14T00:00:00Z","workspace":"ops2"}`)

	e, ws, err := buildSendEnvelope(self, raw)
	if err != nil {
		t.Fatalf("a valid send was refused: %v", err)
	}
	if e.Content.Data != "hi" || ws != "ops2" || e.InReplyTo != "n1" {
		t.Fatalf("a valid send did not bind: %+v ws=%q", e, ws)
	}
}

// fail_fast is added to the schema by the presence watch and read by toolSend,
// so the strict decode must accept it.
func TestSendArgsAcceptFailFast(t *testing.T) {
	self := Sender{AgentID: "builder", Team: "fleet", Workspace: "ops2"}
	raw := json.RawMessage(`{"to":"global://c1/supervisor","performative":"INFORM","text":"hi","fail_fast":true}`)

	if _, _, err := buildSendEnvelope(self, raw); err != nil {
		t.Fatalf("a send carrying fail_fast was refused: %v", err)
	}
}
