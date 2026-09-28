package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// When every per-seat send of a broadcast fails, nothing was accepted for
// delivery, so the broadcast is a refusal, not "accepted for delivery" with
// an empty sent list (director#121 follow-up, review G427). Each refused copy
// is still audited on its own (LR-6).
func TestBroadcastRefusesWhenEverySendFails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	auditStream(t, ctx, js)

	sender := liveSeat(t, ctx, url, Sender{AgentID: "sup", Workspace: "w", Team: "t"})
	liveSeat(t, ctx, url, Sender{AgentID: "a", Workspace: "w", Team: "t"})
	liveSeat(t, ctx, url, Sender{AgentID: "b", Workspace: "w", Team: "t"})
	// Presence says a and b are live, but no inbox stream will take their
	// mail, so every per-seat publish fails.
	if err := js.DeleteStream(ctx, "AGENT_INBOX"); err != nil {
		t.Fatal(err)
	}
	audit := streamMsgs(t, ctx, js, "AGENT_AUDIT")

	for _, c := range []struct {
		name string
		call func() (any, error)
	}{
		{"broadcast tool", func() (any, error) {
			raw, _ := json.Marshal(map[string]string{"team": "t", "text": "roll call"})
			return toolBroadcast(ctx, sender, raw)
		}},
		{"send_message to broadcast://", func() (any, error) {
			raw, _ := json.Marshal(map[string]any{"to": "broadcast://w/t", "performative": "INFORM", "text": "roll call"})
			return toolSend(ctx, sender, raw)
		}},
	} {
		out, err := c.call()
		if err == nil {
			t.Fatalf("%s: every send failed and it returned %v, want a refusal", c.name, out)
		}
		msg := err.Error()
		for _, want := range []string{"0 of 2", "agent://t/a", "agent://t/b", "R-92"} {
			if !strings.Contains(msg, want) {
				t.Errorf("%s: refusal %q does not name %s", c.name, msg, want)
			}
		}
	}
	// Two copies per call, each audited once as its own refused send.
	if got := streamMsgs(t, ctx, js, "AGENT_AUDIT") - audit; got != 4 {
		t.Errorf("audit records added = %d, want 4 (one per refused copy)", got)
	}
}
