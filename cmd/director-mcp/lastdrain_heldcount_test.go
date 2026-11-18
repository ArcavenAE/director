package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A wait in the default mode holds what it fetched and acks it only after its
// reply is written (ackafter.go). The consumer therefore reports those
// messages as ack-pending while remaining is read, so a batch that returned
// mail never read 0 and last-drain was never written (design slice 1,
// sim/design/director-standing-loop.md section 8). These run on the scratch
// broker of drain_broker_test.go with a scratch DIRECTOR_STATE.

// remaining counts what is still waiting for the next call. The messages this
// call fetched and holds are in its reply, not waiting, so they are left out;
// mail the call did not fetch still counts.
func TestRemainingExcludesTheCallsOwnHeldMessages(t *testing.T) {
	ctx, bus, pub, _ := lastDrainFixture(t)
	for _, id := range []string{"a", "b", "c", "d"} {
		pub(id)
	}
	wctx, al := withAckLater(ctx)
	res, err := bus.receiveBatch(wctx, 2*time.Second, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 2 || len(al.items()) != 2 {
		t.Fatalf("setup: the batch returned %d items and holds %d, want 2 and 2", len(res.Items), len(al.items()))
	}
	rem := bus.remaining(wctx)
	if rem["local"] != 2 {
		t.Errorf("remaining local = %d with 2 held and 2 unfetched, want 2 (the held pair is in this reply)", rem["local"])
	}
	al.ack(bus, t.Logf)
	if got := bus.remaining(ctx)["local"]; got != 2 {
		t.Errorf("after the ack, remaining local = %d, want 2", got)
	}
}

// The same wait through the real call path writes last-drain: two messages
// fetched, none left, so the beat records the drain.
func TestABatchWaitThroughCallToolWritesLastDrain(t *testing.T) {
	state := t.TempDir()
	t.Setenv("DIRECTOR_STATE", state)
	r := newPipeRig(t, false)
	r.publish("m1")
	r.publish("m2")
	before := time.Now().UTC().Truncate(time.Second)
	r.wait(`1`, 3, 5)
	m := r.next(8 * time.Second)
	if idOf(m) != `1` {
		t.Fatalf("the wait was not answered first: %v", m)
	}
	var got struct {
		Count     int               `json:"count"`
		Remaining map[string]uint64 `json:"remaining"`
	}
	if err := json.Unmarshal([]byte(toolText(t, m)), &got); err != nil {
		t.Fatal(err)
	}
	if got.Count != 2 {
		t.Fatalf("setup: the batch carried %d messages, want 2", got.Count)
	}
	if got.Remaining["local"] != 0 {
		t.Errorf("remaining = %v, want local 0 (nothing is left behind this reply)", got.Remaining)
	}
	raw, err := os.ReadFile(filepath.Join(state, "last-drain"))
	if err != nil {
		t.Fatalf("a batch wait that drained the inbox through the call path wrote no last-drain: %v", err)
	}
	ts, err := time.Parse("2006-01-02T15:04:05Z", string(raw[:len(raw)-1]))
	if err != nil {
		t.Fatalf("last-drain = %q, want one RFC 3339 UTC line", raw)
	}
	if ts.Before(before) || ts.After(time.Now().UTC().Add(time.Second)) {
		t.Errorf("last-drain %v is outside the call's window starting %v", ts, before)
	}
}
