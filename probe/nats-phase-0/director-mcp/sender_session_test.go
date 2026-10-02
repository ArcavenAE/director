package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// Part E1 of unread slice M (sim/design/unread-slice-m.md, director#126): every
// send carries sender.session, the sending session's instance. The unread
// reader counts a reply as evidence that a seat reads outside its durable only
// when sender.session names that durable's instance, so two live sessions of
// one agent must never share it, and a send that leaves it empty can never be
// mistaken for one that names a session.

func TestSendCarriesTheSendingSessionInstance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	auditStream(t, ctx, js)

	one := liveSeat(t, ctx, url, Sender{AgentID: "seat", Workspace: "w", Team: "t"})
	two := liveSeat(t, ctx, url, Sender{AgentID: "seat", Workspace: "w", Team: "t"})
	rcpt := liveSeat(t, ctx, url, Sender{AgentID: "rcpt", Workspace: "w", Team: "t"})
	if one.instance == two.instance {
		t.Fatalf("two sessions of one agent share instance %q", one.instance)
	}

	for _, s := range []*Bus{one, two} {
		raw, _ := json.Marshal(map[string]any{"to": "agent://t/rcpt", "performative": "INFORM", "text": "from " + s.instance})
		if _, err := toolSend(ctx, s, raw); err != nil {
			t.Fatalf("send from %s: %v", s.instance, err)
		}
		e, _, err := rcpt.receive(ctx, 2*time.Second)
		if err != nil || e == nil {
			t.Fatalf("rcpt received nothing from %s: %v", s.instance, err)
		}
		if e.Sender.Session != s.instance {
			t.Errorf("sender.session = %q, want the sending session's instance %q", e.Sender.Session, s.instance)
		}
		if e.Sender.AgentID != "seat" {
			t.Errorf("sender.agent_id = %q, want seat", e.Sender.AgentID)
		}
	}
}

func TestBroadcastCarriesTheSendingSessionInstance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	auditStream(t, ctx, js)

	sender := liveSeat(t, ctx, url, Sender{AgentID: "sup", Workspace: "w", Team: "t"})
	a := liveSeat(t, ctx, url, Sender{AgentID: "a", Workspace: "w", Team: "t"})

	raw, _ := json.Marshal(map[string]any{"to": "broadcast://w/t", "performative": "INFORM", "text": "all"})
	if _, err := toolSend(ctx, sender, raw); err != nil {
		t.Fatalf("send to broadcast://w/t: %v", err)
	}
	e, _, err := a.receive(ctx, 2*time.Second)
	if err != nil || e == nil {
		t.Fatalf("a received nothing: %v", err)
	}
	if e.Sender.Session != sender.instance {
		t.Errorf("sender.session = %q, want %q", e.Sender.Session, sender.instance)
	}
}

// The field is on the wire under its envelope name, so a reader that decodes
// the raw message (the unread scan does, by sequence) sees it.
func TestSenderSessionSerializesAsSession(t *testing.T) {
	e := newEnvelope(Sender{AgentID: "a", Workspace: "w", Team: "t", Session: "01ABC"})
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var back struct {
		Sender map[string]any `json:"sender"`
	}
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Sender["session"] != "01ABC" {
		t.Errorf("sender = %v, want session 01ABC", back.Sender)
	}
}
