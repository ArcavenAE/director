package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A send_message to broadcast:// took the same silent path the broadcast tool
// did: core NATS to a subject nothing stores, answered "accepted for
// delivery". The ruling covers every path to a broadcast (director#121), so
// this entry point gets the same fan-out and the same refusal at zero.

func TestSendToBroadcastAddressFansOut(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	auditStream(t, ctx, js)

	sender := liveSeat(t, ctx, url, Sender{AgentID: "sup", Workspace: "w", Team: "t"})
	a := liveSeat(t, ctx, url, Sender{AgentID: "a", Workspace: "w", Team: "t"})
	b := liveSeat(t, ctx, url, Sender{AgentID: "b", Workspace: "w", Team: "t"})
	other := liveSeat(t, ctx, url, Sender{AgentID: "c", Workspace: "w", Team: "u"})

	raw, _ := json.Marshal(map[string]any{"to": "broadcast://w/t", "performative": "REQUEST", "text": "status please"})
	out, err := toolSend(ctx, sender, raw)
	if err != nil {
		t.Fatalf("send to broadcast://w/t: %v", err)
	}
	res, _ := out.(map[string]any)
	if n, _ := res["recipients"].(int); n != 2 {
		t.Fatalf("recipients = %v, want 2 (result %v)", res["recipients"], res)
	}
	for _, seat := range []*Bus{a, b} {
		e, _, err := seat.receive(ctx, 2*time.Second)
		if err != nil || e == nil {
			t.Fatalf("%s received nothing: %v", seat.self.AgentID, err)
		}
		if e.Content.Data != "status please" || e.Performative != "REQUEST" {
			t.Errorf("%s got %q %q, want the caller's text and performative", seat.self.AgentID, e.Performative, e.Content.Data)
		}
	}
	for _, seat := range []*Bus{sender, other} {
		if e, _, _ := seat.receive(ctx, 500*time.Millisecond); e != nil {
			t.Errorf("%s received the broadcast, want nothing", seat.self.AgentID)
		}
	}
}

func TestSendToBroadcastAddressRefusesAtZero(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	auditStream(t, ctx, js)
	sender := liveSeat(t, ctx, url, Sender{AgentID: "sup", Workspace: "w", Team: "t"})

	inbox, audit := streamMsgs(t, ctx, js, "AGENT_INBOX"), streamMsgs(t, ctx, js, "AGENT_AUDIT")
	raw, _ := json.Marshal(map[string]any{"to": "broadcast://w/t", "performative": "INFORM", "text": "anyone?"})
	out, err := toolSend(ctx, sender, raw)
	if err == nil {
		t.Fatalf("send to an empty broadcast scope returned %v, want a refusal", out)
	}
	for _, want := range []string{`"w"`, `"t"`, "R-92"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %s", err, want)
		}
	}
	if got := streamMsgs(t, ctx, js, "AGENT_INBOX"); got != inbox {
		t.Errorf("AGENT_INBOX moved %d -> %d", inbox, got)
	}
	if got := streamMsgs(t, ctx, js, "AGENT_AUDIT"); got != audit {
		t.Errorf("AGENT_AUDIT moved %d -> %d", audit, got)
	}
}
