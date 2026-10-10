package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// The regression this pins: the send path once put envelopes on the bus with
// an empty content.data, and each one came back "accepted for delivery" with a
// message_id. An accepted empty body is silent loss, so validate now refuses it
// at the send boundary. See the 2026-09-13 empty-body regression.
func TestValidateRejectsEmptyBodyBearingContent(t *testing.T) {
	base := func() *Envelope {
		e := newEnvelope(Sender{AgentID: "planner", Workspace: "aae-orc", Team: "ops"})
		e.Recipient.Address = "agent://ops/builder"
		e.Performative = "INFORM"
		return e
	}

	for _, typ := range []string{"text", "task", "result"} {
		e := base()
		e.Content = Content{Type: typ, Data: ""}
		if err := e.validate(); err == nil {
			t.Errorf("type %q with empty data was accepted; want rejection", typ)
		} else if !strings.Contains(err.Error(), "content.data is empty") {
			t.Errorf("type %q: error does not name the empty body: %v", typ, err)
		}
		e.Content.Data = "a real body"
		if err := e.validate(); err != nil {
			t.Errorf("type %q with data was rejected: %v", typ, err)
		}
	}
}

// A signal is a bare notification and may carry no data; a pointer must carry
// at least one ref instead of a body.
func TestValidateContentTypeShapes(t *testing.T) {
	base := func() *Envelope {
		e := newEnvelope(Sender{AgentID: "planner", Workspace: "aae-orc", Team: "ops"})
		e.Recipient.Address = "agent://ops/builder"
		e.Performative = "INFORM"
		return e
	}

	e := base()
	e.Content = Content{Type: "signal"}
	if err := e.validate(); err != nil {
		t.Errorf("empty signal was rejected: %v", err)
	}

	e = base()
	e.Content = Content{Type: "pointer"}
	if err := e.validate(); err == nil {
		t.Error("pointer with no refs was accepted; want rejection")
	}
	e.Content.Refs = []string{"bd:aae-orc-1"}
	if err := e.validate(); err != nil {
		t.Errorf("pointer with a ref was rejected: %v", err)
	}

	e = base()
	e.Content = Content{Type: ""}
	if err := e.validate(); err == nil {
		t.Error("empty content.type was accepted; want rejection")
	}
}

// The full send path, decode to marshal, keeps the body on the wire; this is
// the exact shape that regressed to data_len=0.
func TestSendPathKeepsBodyOnTheWire(t *testing.T) {
	raw := json.RawMessage(`{"to":"agent://ops/planner","performative":"INFORM","text":"a real body of some length"}`)
	var a struct {
		To, Performative, Text, InReplyTo, ReplyBy string
		Refs                                       []string
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatal(err)
	}
	e := newEnvelope(Sender{AgentID: "planner", Workspace: "aae-orc", Team: "ops"})
	e.Recipient.Address = a.To
	e.Performative = a.Performative
	e.Content = Content{Type: "text", Data: a.Text, Refs: a.Refs}
	if err := e.validate(); err != nil {
		t.Fatalf("valid envelope rejected: %v", err)
	}
	body, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"data":"a real body of some length"`) {
		t.Fatalf("content.data missing on the wire:\n%s", body)
	}
}
