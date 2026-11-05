package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// An empty poll must not read as "no mail" when the inbox stream has aged past
// this seat's last read position (aae-orc-t77rr). Mail for the seat inside
// that window is gone and cannot be counted, so the signal says "may have".
// The stream drops it by max_age, which leaves the seat's position where it
// was and moves the stream's first sequence past it (a purge would move the
// position too, so the test does not use one).
func TestEmptyPollReportsMailThatMayHaveExpired(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	ast, err := js.Stream(ctx, "AGENT_INBOX")
	if err != nil {
		t.Fatal(err)
	}
	cfg := ast.CachedInfo().Config
	cfg.MaxAge, cfg.Duplicates = 2*time.Second, time.Second
	if _, err := js.UpdateStream(ctx, cfg); err != nil {
		t.Fatal(err)
	}

	self := Sender{AgentID: "operator", Workspace: "aae-orc", Team: "ops"}
	bus, err := connect(ctx, url, self, &globalConfig{Domain: "global", Cluster: "kinu", Role: roleDirector})
	if err != nil {
		t.Fatal(err)
	}
	defer bus.close()

	mine := "agent.aae-orc.ops.operator.inbox"
	other := "agent.aae-orc.ops.someone-else.inbox"
	pubEnv(t, ctx, js, mine, "l1", "INFORM", "read before the gap")
	if _, err := toolWait(ctx, bus, json.RawMessage(`{"timeout_seconds":2}`)); err != nil {
		t.Fatal(err)
	}

	// Control: an idle seat with nothing aged out gets the plain silence note
	// and no expiry field.
	out := waitOut(t, ctx, bus)
	if _, ok := out["expired_unread"]; ok {
		t.Fatalf("expired_unread present with nothing aged out: %v", out)
	}

	// Mail for other seats arrives while this one is away, and ages out with
	// the seat's own read at sequence 1. The seat polls nothing meanwhile.
	for _, id := range []string{"x1", "x2", "x3"} {
		pubEnv(t, ctx, js, other, id, "INFORM", "someone else's mail")
	}
	time.Sleep(3 * time.Second)

	out = waitOut(t, ctx, bus)
	note, _ := out["expired_unread"].(string)
	if !strings.Contains(note, "may have") || !strings.Contains(note, "sequences 2 to 4") {
		t.Fatalf("expired_unread = %q, want a may-have note naming the window of sequences 2 to 4 (%v)", note, out)
	}
	if n, _ := out["note"].(string); !strings.Contains(n, "silence, not failure") {
		t.Errorf("the plain note changed: %v", out)
	}

	// The same gap is not reported twice, and a wider one is.
	out = waitOut(t, ctx, bus)
	if _, ok := out["expired_unread"]; ok {
		t.Errorf("the same gap was reported again: %v", out)
	}
	pubEnv(t, ctx, js, other, "x4", "INFORM", "someone else's mail")
	time.Sleep(3 * time.Second)

	// The batch form reports it too.
	b := waitBatchOut(t, ctx, bus)
	if _, ok := b["expired_unread"]; !ok {
		t.Errorf("batch wait carries no expired_unread: %v", b)
	}
}

func waitOut(t *testing.T, ctx context.Context, bus *Bus) map[string]any {
	t.Helper()
	res, err := toolWait(ctx, bus, json.RawMessage(`{"timeout_seconds":1}`))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("toolWait returned %T", res)
	}
	return m
}

func waitBatchOut(t *testing.T, ctx context.Context, bus *Bus) map[string]any {
	t.Helper()
	res, err := toolWait(ctx, bus, json.RawMessage(`{"timeout_seconds":1,"max":5}`))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("toolWait returned %T", res)
	}
	return m
}
