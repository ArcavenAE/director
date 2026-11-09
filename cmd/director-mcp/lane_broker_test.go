package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// inbox_summary carries the lane: the reply to an open ask is in "lane", whatever
// else is waiting, and nothing is acked.
func TestBrokerSummaryCarriesTheLane(t *testing.T) {
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
	self := Sender{AgentID: "operator", Workspace: "aae-orc", Team: "ops"}
	bus, err := connect(ctx, url, self, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bus.close()
	mine := "agent.aae-orc.ops.operator.inbox"
	pubEnv(t, ctx, js, mine, "gate1", "INFORM", "GATE one")
	pubEnv(t, ctx, js, mine, "gate2", "INFORM", "GATE two")
	reply := testEnv("the-reply", "sup", "INFORM", "done")
	reply.InReplyTo = "01MSENT1"
	reply.Recipient.Address = "agent://ops/operator"
	b, _ := json.Marshal(reply)
	if _, err := js.Publish(ctx, mine, b); err != nil {
		t.Fatal(err)
	}

	out, err := toolSummary(ctx, bus, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	lane, ok := out.(map[string]any)["lane"].(inboxLane)
	if !ok {
		t.Fatalf("inbox_summary has no lane: %v", out)
	}
	if len(lane.Items) != 1 || lane.Items[0].MessageID != "the-reply" || lane.Items[0].AskID != "I-1" {
		t.Fatalf("lane = %+v, want only the reply, for I-1", lane)
	}
	if got := out.(map[string]any)["waiting"].(map[string]uint64)["local"]; got != 3 {
		t.Errorf("waiting = %d, want 3: the summary acks nothing", got)
	}
}
