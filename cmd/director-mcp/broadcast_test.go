package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// LR-1 (director#121). A broadcast used to publish core NATS to a subject no
// stream captures and no session subscribes, and still answer "broadcast
// sent". These run against a scratch broker and skip without nats-server.

func auditStream(t *testing.T, ctx context.Context, js jetstream.JetStream) {
	t.Helper()
	if _, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: "AGENT_AUDIT", Subjects: []string{"agent.audit"}}); err != nil {
		t.Fatal(err)
	}
}

func streamMsgs(t *testing.T, ctx context.Context, js jetstream.JetStream, name string) uint64 {
	t.Helper()
	s, err := js.Stream(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return info.State.Msgs
}

func liveSeat(t *testing.T, ctx context.Context, url string, self Sender) *Bus {
	t.Helper()
	b, err := connect(ctx, url, self, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.close)
	if err := b.writePresence(ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBroadcastFansOutToLivePresence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	auditStream(t, ctx, js)

	sender := liveSeat(t, ctx, url, Sender{AgentID: "sup", Workspace: "w", Team: "t"})
	a := liveSeat(t, ctx, url, Sender{AgentID: "a", Workspace: "w", Team: "t"})
	b := liveSeat(t, ctx, url, Sender{AgentID: "b", Workspace: "w", Team: "t"})
	// A second session of seat a: one recipient, not two sends to one inbox.
	liveSeat(t, ctx, url, Sender{AgentID: "a", Workspace: "w", Team: "t"})
	// Out of scope: another team in w, and team t in another workspace.
	other := liveSeat(t, ctx, url, Sender{AgentID: "c", Workspace: "w", Team: "u"})
	elsewhere := liveSeat(t, ctx, url, Sender{AgentID: "d", Workspace: "w2", Team: "t"})

	auditBefore := streamMsgs(t, ctx, js, "AGENT_AUDIT")
	raw, _ := json.Marshal(map[string]string{"team": "t", "text": "roll call"})
	out, err := toolBroadcast(ctx, sender, raw)
	if err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	res, _ := out.(map[string]any)
	if n, _ := res["recipients"].(int); n != 2 {
		t.Fatalf("recipients = %v, want 2 (result %v)", res["recipients"], res)
	}
	if f, _ := res["failed"].([]map[string]string); len(f) != 0 {
		t.Errorf("failed = %v, want none", f)
	}
	if got := streamMsgs(t, ctx, js, "AGENT_AUDIT") - auditBefore; got != 2 {
		t.Errorf("audit records added = %d, want one per recipient (2)", got)
	}

	conv := ""
	for _, seat := range []*Bus{a, b} {
		e, _, err := seat.receive(ctx, 2*time.Second)
		if err != nil || e == nil {
			t.Fatalf("%s received nothing: %v", seat.self.AgentID, err)
		}
		if e.Content.Data != "roll call" {
			t.Errorf("%s got %q", seat.self.AgentID, e.Content.Data)
		}
		if conv == "" {
			conv = e.ConversationID
		} else if e.ConversationID != conv {
			t.Errorf("conversation ids differ: %q vs %q", conv, e.ConversationID)
		}
	}
	for _, seat := range []*Bus{sender, other, elsewhere} {
		if e, _, _ := seat.receive(ctx, 500*time.Millisecond); e != nil {
			t.Errorf("%s/%s received the broadcast, want nothing", seat.self.Workspace, seat.self.AgentID)
		}
	}
}

func TestBroadcastRefusesAtZeroRecipientsBeforePublish(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	auditStream(t, ctx, js)

	sender := liveSeat(t, ctx, url, Sender{AgentID: "sup", Workspace: "w", Team: "t"})
	liveSeat(t, ctx, url, Sender{AgentID: "c", Workspace: "w2", Team: "t"})

	inbox, audit := streamMsgs(t, ctx, js, "AGENT_INBOX"), streamMsgs(t, ctx, js, "AGENT_AUDIT")
	raw, _ := json.Marshal(map[string]string{"team": "t", "text": "anyone?"})
	out, err := toolBroadcast(ctx, sender, raw)
	if err == nil {
		t.Fatalf("broadcast with no one live returned %v, want a refusal", out)
	}
	msg := err.Error()
	for _, want := range []string{`"w"`, `"t"`, "R-92"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal %q does not name %s", msg, want)
		}
	}
	if got := streamMsgs(t, ctx, js, "AGENT_INBOX"); got != inbox {
		t.Errorf("AGENT_INBOX moved %d -> %d on a refusal", inbox, got)
	}
	// With LR-6, the refusal itself is recorded: exactly one audit record,
	// marked refused at the resolve stage, and nothing on any inbox.
	if got := streamMsgs(t, ctx, js, "AGENT_AUDIT"); got != audit+1 {
		t.Fatalf("AGENT_AUDIT %d -> %d on a refusal, want exactly one refusal record", audit, got)
	}
	if !strings.Contains(msg, "audit: recorded agent.audit seq") {
		t.Errorf("refusal does not report its audit record: %q", msg)
	}
	s, _ := js.Stream(ctx, "AGENT_AUDIT")
	info, _ := s.Info(ctx)
	rec, err := s.GetMsg(ctx, info.State.LastSeq)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Header.Get("Director-Outcome") != "refused" || rec.Header.Get("Director-Stage") != "resolve" {
		t.Errorf("refusal record headers: outcome %q stage %q", rec.Header.Get("Director-Outcome"), rec.Header.Get("Director-Stage"))
	}
	if !strings.Contains(string(rec.Data), `"address":"broadcast://w/t"`) {
		t.Errorf("refusal record is not the broadcast as asked: %s", rec.Data)
	}
}
