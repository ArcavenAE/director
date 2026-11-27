package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Review findings of director#763 (aae-orc-b0uw2-r1-fixes). F1: the drain never
// says "no reply owed" about a message inbox_summary flags. F2: a returned batch
// keeps the discard count of the call that drained it.

func TestTierBatchFlaggedRoutineMessagesRaiseTheBatchToAct(t *testing.T) {
	future := classifyNow.Add(time.Hour).Format(time.RFC3339)
	cases := []struct {
		name string
		edit func(*Envelope)
		body string
		flag string
	}{
		{"future reply_by", func(e *Envelope) { e.ReplyBy = future }, "status: working", "reply_by"},
		{"custody", nil, "status: I hold custody of the findings", "holds custody"},
		{"awaits a reply", nil, "status: awaiting your reply on the digest", "awaits a reply"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			items := append(statusInforms(), tierItem(4, "INFORM", c.body, c.edit))
			if len(flagReasons(items[3].Env)) == 0 {
				t.Fatalf("setup: inbox_summary does not flag this message")
			}
			line := tierBatch(newClassifier(), items, inboxLane{}, classifyNow)
			if line != "" {
				t.Errorf("fact line = %q, want none: inbox_summary flags this message", line)
			}
			got := items[3]
			if got.ProposedTier != "act" || !strings.Contains(got.Reason, c.flag) {
				t.Errorf("flagged message: proposed_tier %q reason %q, want act and a reason naming %q", got.ProposedTier, got.Reason, c.flag)
			}
			for _, it := range items[:3] {
				if it.ProposedTier != "routine" {
					t.Errorf("seq %d: proposed_tier %q, want routine", it.Seq, it.ProposedTier)
				}
			}
		})
	}
}

func TestTierBatchAnUnflaggedBatchStillGetsTheLine(t *testing.T) {
	if line := tierBatch(newClassifier(), statusInforms(), inboxLane{}, classifyNow); line != allRoutineLine {
		t.Errorf("fact line = %q, want %q", line, allRoutineLine)
	}
}

// The mail a cancelled call handed back came from a call that discarded an
// unreadable message, which may have been a REQUEST, so the returned batch is
// withheld its line as the fresh batch was.
func TestBrokerReturnedBatchKeepsTheDiscardOfItsCall(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	t.Setenv("DIRECTOR_STATE", t.TempDir())
	t.Setenv("RELAY_LOG", t.TempDir()+"/none.md")
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	bus, err := connect(ctx, url, Sender{AgentID: "operator", Workspace: "aae-orc", Team: "ops"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bus.close()
	mine := "agent.aae-orc.ops.operator.inbox"
	pubEnv(t, ctx, js, mine, "s1", "INFORM", "status: working")
	pubEnv(t, ctx, js, mine, "s2", "INFORM", "status: still working")
	if _, err := js.Publish(ctx, mine, []byte("not an envelope")); err != nil {
		t.Fatal(err)
	}
	wctx, al := withAckLater(ctx)
	first := drainJSON(t, wctx, bus, `{"timeout_seconds":2,"max":10}`)
	if d, _ := first["discarded"].(float64); d != 1 {
		t.Fatalf("setup: discarded = %v, want 1: %v", first["discarded"], first)
	}
	bus.returnMail(al.items(), "test cancel", func(string, ...any) {})

	m := drainJSON(t, ctx, bus, `{"timeout_seconds":2,"max":10}`)
	if _, ok := m["returned"]; !ok {
		t.Fatalf("not the returned path: %v", m)
	}
	if _, has := m["fact_line"]; has {
		t.Errorf("returned batch carries fact_line %v although its call discarded a message", m["fact_line"])
	}
}
