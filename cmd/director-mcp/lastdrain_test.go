package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// last-drain is the beat the director skill reads to know the inbox was
// drained. The shim writes it when a batch wait returns with nothing left
// waiting, so the beat no longer depends on a person running standing-hook.
// These run against the scratch broker of drain_broker_test.go, with a scratch
// DIRECTOR_STATE: the real ~/.director is never read or written.

func lastDrainFixture(t *testing.T) (context.Context, *Bus, func(id string), string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	state := t.TempDir()
	t.Setenv("DIRECTOR_STATE", state)
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	self := Sender{AgentID: "operator", Workspace: "aae-orc", Team: "ops"}
	bus, err := connect(ctx, url, self, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bus.close)
	pub := func(id string) {
		pubEnv(t, ctx, js, "agent.aae-orc.ops.operator.inbox", id, "INFORM", "m "+id)
	}
	return ctx, bus, pub, filepath.Join(state, "last-drain")
}

func TestWaitRemainingZeroWritesLastDrain(t *testing.T) {
	ctx, bus, pub, path := lastDrainFixture(t)
	pub("a")
	pub("b")
	before := time.Now().UTC().Truncate(time.Second)
	res, err := toolWait(ctx, bus, json.RawMessage(`{"timeout_seconds":2,"max":5}`))
	if err != nil {
		t.Fatal(err)
	}
	after := time.Now().UTC().Add(time.Second)
	rem := res.(map[string]any)["remaining"].(map[string]uint64)
	if rem["local"] != 0 {
		t.Fatalf("setup: remaining = %v, want local 0", rem)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("a wait that returned remaining 0 wrote no last-drain: %v", err)
	}
	ts, err := time.Parse("2006-01-02T15:04:05Z", string(raw[:len(raw)-1]))
	if err != nil || raw[len(raw)-1] != '\n' {
		t.Fatalf("last-drain = %q, want one RFC 3339 UTC line", raw)
	}
	if ts.Before(before) || ts.After(after) {
		t.Errorf("last-drain %v is outside the return window %v to %v", ts, before, after)
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".*"))
	if len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

func TestWaitRemainingNonzeroLeavesLastDrain(t *testing.T) {
	ctx, bus, pub, path := lastDrainFixture(t)
	const sentinel = "2020-01-01T00:00:00Z\n"
	if err := os.WriteFile(path, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c", "d"} {
		pub(id)
	}
	res, err := toolWait(ctx, bus, json.RawMessage(`{"timeout_seconds":2,"max":2}`))
	if err != nil {
		t.Fatal(err)
	}
	rem := res.(map[string]any)["remaining"].(map[string]uint64)
	if rem["local"] != 2 {
		t.Fatalf("setup: remaining = %v, want local 2", rem)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != sentinel {
		t.Errorf("last-drain = %q after a return with 2 waiting, want it unchanged", raw)
	}
}

func TestWaitSilenceLeavesLastDrain(t *testing.T) {
	ctx, bus, _, path := lastDrainFixture(t)
	if _, err := toolWait(ctx, bus, json.RawMessage(`{"timeout_seconds":1,"max":5}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a wait that returned nothing created last-drain (stat err = %v)", err)
	}
}
