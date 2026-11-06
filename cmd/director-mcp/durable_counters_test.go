package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// I-100 P6 (aae-orc-tchxl): the shim counts its durable rebuilds and says when
// one failed; the counts ride in its presence record, and `director-mcp unread`
// copies them onto the seat's row and marks a live seat whose durable the broker
// no longer has as missing. A report is a diagnostic: it always exits 0.

func presenceRecord(t *testing.T, ctx context.Context, b *Bus) map[string]any {
	t.Helper()
	rec := map[string]any{}
	b.presenceView(ctx).fields(rec)
	return rec
}

func deleteLocalDurable(t *testing.T, ctx context.Context, b *Bus) {
	t.Helper()
	name := "mcp_" + b.self.AgentID + "_" + b.instance
	if err := b.js.DeleteConsumer(ctx, "AGENT_INBOX", name); err != nil {
		t.Fatalf("delete durable %s: %v", name, err)
	}
}

// A healthy seat's presence row and unread row are unchanged: the counters appear
// only once a durable has been lost.
func TestHealthySeatRowsCarryNoDurableFields(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	bus := roleBus(t, ctx, url, "steady", "")

	rec := presenceRecord(t, ctx, bus)
	for _, k := range []string{"durable_state", "rebuilds_ok", "rebuilds_failed", "durable_reason"} {
		if _, ok := rec[k]; ok {
			t.Fatalf("a healthy seat's presence carries %s: %v", k, rec)
		}
	}
	d := findDurable(t, mustRead(t, ctx, js, time.Now()), "steady")
	if d.DurableState != "" || d.RebuildsOK != 0 || d.RebuildsFailed != 0 {
		t.Fatalf("unread row = state %q ok %d failed %d, want none", d.DurableState, d.RebuildsOK, d.RebuildsFailed)
	}
}

// #269 specimen: the local durable is deleted while the shim runs, the shim
// rebuilds it, and the rebuild is counted in presence and in unread.
func TestDeletedDurableRebuildIsCounted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	bus := roleBus(t, ctx, url, "rebuilder", "")
	deleteLocalDurable(t, ctx, bus)

	ok, err := bus.restoreLocal(ctx)
	if err != nil || !ok {
		t.Fatalf("restoreLocal = %v, %v; want a rebuild", ok, err)
	}
	if err := bus.writePresence(ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	rec := presenceRecord(t, ctx, bus)
	if rec["durable_state"] != "rebuilt" || rec["rebuilds_ok"] != int64(1) {
		t.Fatalf("presence fields = %v, want rebuilt with 1 rebuild", rec)
	}
	d := findDurable(t, mustRead(t, ctx, js, time.Now()), "rebuilder")
	if d.DurableState != "rebuilt" || d.RebuildsOK != 1 {
		t.Fatalf("unread row = state %q ok %d, want rebuilt 1", d.DurableState, d.RebuildsOK)
	}
}

// A live seat whose durable is gone and not yet rebuilt reads missing in unread.
func TestLiveSeatWithNoDurableReadsMissing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	bus := roleBus(t, ctx, url, "gone", "")
	deleteLocalDurable(t, ctx, bus)

	d := findDurable(t, mustRead(t, ctx, js, time.Now()), "gone")
	if d.DurableState != "missing" || !d.Live {
		t.Fatalf("row = state %q live %v, want missing and live", d.DurableState, d.Live)
	}
}

// A failed rebuild is loud: the tool path returns the failure, and presence
// carries state missing, the failure count and a cue_reason that names it.
func TestFailedRebuildIsLoudInResultAndPresence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	bus := roleBus(t, ctx, url, "stuck", "")
	deleteLocalDurable(t, ctx, bus)
	bus.localMu.Lock()
	bus.local.cfg.Name = "not-the-durable" // the broker refuses a name that differs from the durable
	bus.localMu.Unlock()

	ok, err := bus.restoreLocal(ctx)
	if ok || err == nil || !strings.Contains(err.Error(), "recreating it failed") {
		t.Fatalf("restoreLocal = %v, %v; want the failure returned", ok, err)
	}
	rec := presenceRecord(t, ctx, bus)
	if rec["durable_state"] != "missing" || rec["rebuilds_failed"] != int64(1) {
		t.Fatalf("presence fields = %v, want missing with 1 failed rebuild", rec)
	}
	if why, _ := rec["durable_reason"].(string); !strings.Contains(why, "durable rebuild failed") {
		t.Fatalf("durable_reason = %q, want it to name the failed rebuild", why)
	}
	// With the cue on, the same failure is in cue_reason.
	bus.attachCue(newNoReplyRig(t, bus).c)
	rec = presenceRecord(t, ctx, bus)
	if why, _ := rec["cue_reason"].(string); !strings.Contains(why, "durable rebuild failed") {
		t.Fatalf("cue_reason = %q, want it to name the failed rebuild", why)
	}
	if err := bus.writePresence(ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	d := findDurable(t, mustRead(t, ctx, js, time.Now()), "stuck")
	if d.DurableState != "missing" || d.RebuildsFailed != 1 {
		t.Fatalf("unread row = state %q failed %d, want missing 1", d.DurableState, d.RebuildsFailed)
	}
}

// The report is a diagnostic: the command exits 0 with a missing durable in it.
func TestUnreadExitsZeroWithAMissingDurable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	kv, _ := js.KeyValue(ctx, "AGENT_STATE")
	bin := buildShim(t, t.TempDir(), false)
	startShim(t, ctx, bin, url, Sender{AgentID: "vanish", Workspace: "w", Team: "t"}, kv)
	rows := mustRead(t, ctx, js, time.Now())
	if err := js.DeleteConsumer(ctx, "AGENT_INBOX", findDurable(t, rows, "vanish").Durable); err != nil {
		t.Fatal(err)
	}
	r := runUnread(t, bin, url) // runUnread fails the test on a nonzero exit
	if d := findDurable(t, r, "vanish"); d.DurableState != "missing" {
		t.Fatalf("row state = %q, want missing", d.DurableState)
	}
}

// The counters add no network path: they ride in the presence record and the
// report, and the shim opens no connection beyond the bus.
func TestCountersAddNoNetworkPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url, mon := startMonitoredServer(t)
	provision(t, ctx, url)
	bus := roleBus(t, ctx, url, "offline", "")
	before := connz(t, mon)
	deleteLocalDurable(t, ctx, bus)
	if _, err := bus.restoreLocal(ctx); err != nil {
		t.Fatal(err)
	}
	if err := bus.writePresence(ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	after := connz(t, mon)
	if len(after) != len(before) {
		t.Fatalf("connections went from %d to %d across a rebuild and a presence write", len(before), len(after))
	}
	for _, ip := range after {
		if ip != "127.0.0.1" && ip != "::1" {
			t.Fatalf("a client connected from %q, want loopback only", ip)
		}
	}
}
