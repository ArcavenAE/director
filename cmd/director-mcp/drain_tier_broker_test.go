package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The drain's proposed tier through wait_for_message with max > 1 against a
// real nats-server (skipped when it is not on PATH), read from the JSON the
// seat receives.

func drainJSON(t *testing.T, ctx context.Context, bus *Bus, args string) map[string]any {
	t.Helper()
	out, err := toolWait(ctx, bus, json.RawMessage(args))
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func drainMessages(t *testing.T, m map[string]any) []map[string]any {
	t.Helper()
	raw, ok := m["messages"].([]any)
	if !ok {
		t.Fatalf("result has no messages list: %v", m)
	}
	var out []map[string]any
	for _, r := range raw {
		out = append(out, r.(map[string]any))
	}
	return out
}

func TestBrokerDrainThreeStatusInformsCarryTheLine(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	t.Setenv("DIRECTOR_STATE", t.TempDir())
	t.Setenv("RELAY_LOG", filepath.Join(t.TempDir(), "none.md"))
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	bus, err := connect(ctx, url, Sender{AgentID: "operator", Workspace: "aae-orc", Team: "ops"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bus.close()
	mine := "agent.aae-orc.ops.operator.inbox"
	pubEnv(t, ctx, js, mine, "s1", "INFORM", "status: working on the digest step")
	pubEnv(t, ctx, js, mine, "s2", "INFORM", "status: tests are running")
	pubEnv(t, ctx, js, mine, "s3", "INFORM", "status: pushed; waiting on CI")

	m := drainJSON(t, ctx, bus, `{"timeout_seconds":2,"max":10}`)
	msgs := drainMessages(t, m)
	if len(msgs) != 3 {
		t.Fatalf("drained %d messages, want 3: %v", len(msgs), m)
	}
	for _, x := range msgs {
		if x["proposed_tier"] != "routine" || x["reason"] == nil || x["reason"] == "" {
			t.Errorf("message %v: proposed_tier %v reason %v, want routine with a reason", x["sequence"], x["proposed_tier"], x["reason"])
		}
	}
	if m["fact_line"] != "no board change; no reply owed" {
		t.Errorf("fact_line = %v, want %q", m["fact_line"], "no board change; no reply owed")
	}
}

func TestBrokerDrainOneCancelHasNoLine(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	t.Setenv("DIRECTOR_STATE", t.TempDir())
	t.Setenv("RELAY_LOG", filepath.Join(t.TempDir(), "none.md"))
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
	pubEnv(t, ctx, js, mine, "c1", "CANCEL", "stop")

	m := drainJSON(t, ctx, bus, `{"timeout_seconds":2,"max":10}`)
	msgs := drainMessages(t, m)
	if len(msgs) != 3 {
		t.Fatalf("drained %d messages, want 3: %v", len(msgs), m)
	}
	if _, has := m["fact_line"]; has {
		t.Errorf("fact_line present with a CANCEL in the batch: %v", m["fact_line"])
	}
	last := msgs[2]
	if last["proposed_tier"] != "act" || last["reason"] != "performative CANCEL" {
		t.Errorf("CANCEL: proposed_tier %v reason %v, want act and %q", last["proposed_tier"], last["reason"], "performative CANCEL")
	}
}

func TestBrokerDrainLaneItemIsNeverRoutine(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	state := t.TempDir()
	t.Setenv("DIRECTOR_STATE", state)
	rl := filepath.Join(t.TempDir(), "relay-log.md")
	if err := os.WriteFile(rl, []byte(laneRelayLog), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RELAY_LOG", rl)
	ask := `{"ask_id":"I-1","received_at":"` + time.Now().UTC().Add(-time.Hour).Format(time.RFC3339) + `","text":"x","source":"operator"}` + "\n"
	if err := os.WriteFile(filepath.Join(state, "asks.jsonl"), []byte(ask), 0o600); err != nil {
		t.Fatal(err)
	}
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	bus, err := connect(ctx, url, Sender{AgentID: "operator", Workspace: "aae-orc", Team: "ops"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bus.close()
	mine := "agent.aae-orc.ops.operator.inbox"
	pubEnv(t, ctx, js, mine, "s1", "INFORM", "status: working")
	reply := testEnv("the-reply", "sup", "INFORM", "done")
	reply.InReplyTo = "01MSENT1"
	reply.Recipient.Address = "agent://ops/operator"
	b, _ := json.Marshal(reply)
	if _, err := js.Publish(ctx, mine, b); err != nil {
		t.Fatal(err)
	}

	m := drainJSON(t, ctx, bus, `{"timeout_seconds":2,"max":10}`)
	msgs := drainMessages(t, m)
	if len(msgs) != 2 {
		t.Fatalf("drained %d messages, want 2: %v", len(msgs), m)
	}
	got := msgs[1]["proposed_tier"]
	why, _ := msgs[1]["reason"].(string)
	if got != "act" || len(why) < 4 || why[:4] != "lane" {
		t.Errorf("lane item: proposed_tier %v reason %q, want act and a reason that starts with lane", got, why)
	}
	if _, has := m["fact_line"]; has {
		t.Errorf("fact_line present with a lane item in the batch: %v", m["fact_line"])
	}
}
