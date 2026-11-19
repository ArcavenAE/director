package main

import (
	"encoding/json"
	"testing"
	"time"
)

// A seat with the FOR filter on is handed less mail than its durable holds: the
// filter acks the mail for another seat on the next drain. The roster read the
// durable's own count and the seat's pending age counted that mail, so an idle
// seat showed unread and pending-age-over (aae-orc-koaqv). The seat knows what
// it will be handed, so it publishes that in its presence row (unread_deliverable),
// its pending tracker leaves the dropped mail out, and the roster prefers the
// seat's figure and labels the broker's count raw.

func presenceRecordOf(t *testing.T, r *pipeRig) map[string]any {
	t.Helper()
	e, err := r.bus.kv.Get(r.ctx, "presence."+r.bus.self.Team+"."+r.bus.self.AgentID+"."+r.bus.instance)
	if err != nil {
		t.Fatal(err)
	}
	rec := map[string]any{}
	if err := json.Unmarshal(e.Value(), &rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

// The tracker's age is the age of the oldest message the seat will be handed.
// A dropped-only inbox has none.
func TestPendingAgeLeavesOutGlobalMailTheFilterDrops(t *testing.T) {
	r, _ := forPathRig(t)
	pubEnv(t, r.ctx, r.gjs, r.bus.global.cfg.inboxSubject(), "drop1", "INFORM", "FOR alpha-supervisor\nnot for you")
	if _, err := r.bus.pendingTiers(r.ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if age := r.bus.pending.age(time.Now()); age != 0 {
		t.Errorf("pending age = %v for an inbox of only dropped mail, want 0", age)
	}
}

// Control: mail the filter keeps still ages.
func TestPendingAgeKeepsGlobalMailTheFilterKeeps(t *testing.T) {
	r, _ := forPathRig(t)
	pubEnv(t, r.ctx, r.gjs, r.bus.global.cfg.inboxSubject(), "plain1", "INFORM", "no FOR line")
	if _, err := r.bus.pendingTiers(r.ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if age := r.bus.pending.age(time.Now()); age == 0 {
		t.Error("pending age = 0 for a kept message, want it to age")
	}
}

// The seat's presence row says how much it will be handed.
func TestPresenceRowCarriesUnreadDeliverable(t *testing.T) {
	r, _ := forPathRig(t)
	publishForPair(t, r) // drop1 is dropped, plain1 is kept
	if err := r.bus.writePresence(r.ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	got, ok := presenceRecordOf(t, r)["unread_deliverable"]
	if !ok {
		t.Fatal("presence row has no unread_deliverable")
	}
	if got != float64(1) {
		t.Errorf("unread_deliverable = %v, want 1 (plain1; drop1 is dropped)", got)
	}
	rec := presenceRecordOf(t, r)
	if rec["unread_deliverable_global"] != float64(1) || rec["unread_deliverable_local"] != float64(0) {
		t.Errorf("per tier = global %v, local %v, want 1 and 0", rec["unread_deliverable_global"], rec["unread_deliverable_local"])
	}
}

// A seat with no global filter has nothing to leave out, so the field is absent
// and a reader keeps the broker's count.
func TestPresenceRowOmitsUnreadDeliverableWithoutTheFilter(t *testing.T) {
	r := newPipeRig(t, false)
	if err := r.bus.writePresence(r.ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	if v, ok := presenceRecordOf(t, r)["unread_deliverable"]; ok {
		t.Errorf("unread_deliverable = %v on a seat with no global filter, want it absent", v)
	}
}

func rosterRowOf(t *testing.T, r *pipeRig, agent string) map[string]any {
	t.Helper()
	rows, _, err := r.bus.rosterMerged(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return rosterRowFor(t, rows, agent)
}

func publishLocal(t *testing.T, r *pipeRig, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		pubEnv(t, r.ctx, r.js, r.inbox, "m"+string(rune('a'+i)), "INFORM", "hello")
	}
}

// The roster shows the seat's own deliverable count as unread, and the broker's
// count under its own name.
func TestRosterUnreadPrefersTheSeatsDeliverableCount(t *testing.T) {
	r := newPipeRig(t, false)
	publishLocal(t, r, 3)
	if err := r.bus.writePresence(r.ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	rec := presenceRecordOf(t, r)
	rec["unread_deliverable_local"] = 0
	putPresence(t, r.ctx, r.bus, "presence."+r.bus.self.Team+"."+r.bus.self.AgentID+"."+r.bus.instance, rec)
	row := rosterRowOf(t, r, "operator")
	if row["unread"] != uint64(0) {
		t.Errorf("unread = %v, want 0 (the seat will be handed none of the 3 waiting)", row["unread"])
	}
	if row["unread_raw"] != uint64(3) {
		t.Errorf("unread_raw = %v, want 3 (the broker's count)", row["unread_raw"])
	}
	if row["unread_source"] != "seat" {
		t.Errorf("unread_source = %v, want seat", row["unread_source"])
	}
}

// A row from a shim that predates the field keeps the broker's count, and says so.
func TestRosterUnreadKeepsTheBrokerCountForAnOldShim(t *testing.T) {
	r := newPipeRig(t, false)
	publishLocal(t, r, 3)
	if err := r.bus.writePresence(r.ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	row := rosterRowOf(t, r, "operator")
	if row["unread"] != uint64(3) {
		t.Errorf("unread = %v, want 3 with no seat figure", row["unread"])
	}
	if row["unread_raw"] != uint64(3) {
		t.Errorf("unread_raw = %v, want 3", row["unread_raw"])
	}
	if row["unread_source"] != "broker-raw" {
		t.Errorf("unread_source = %v, want broker-raw", row["unread_source"])
	}
}

// The roster takes the seat's figure for the row's own tier, less what the seat
// holds, never above the broker's count; without a figure it keeps the broker's.
func TestUnreadForTakesTheSeatsFigureForTheRowsTier(t *testing.T) {
	cases := []struct {
		name   string
		row    map[string]any
		stream string
		raw    uint64
		held   uint64
		want   uint64
		src    string
	}{
		{"local row, seat says 0", map[string]any{"unread_deliverable_local": float64(0)}, "AGENT_INBOX", 3, 0, 0, "seat"},
		{"global row, seat says 1 of 5", map[string]any{"unread_deliverable_global": float64(1)}, "GLOBAL_TO_C1", 5, 0, 1, "seat"},
		{"the other tier's figure is not used", map[string]any{"unread_deliverable_local": float64(0)}, "GLOBAL_TO_C1", 5, 0, 5, "broker-raw"},
		{"held mail comes off the seat's figure", map[string]any{"unread_deliverable_global": float64(3)}, "GLOBAL_TO_C1", 5, 2, 1, "seat"},
		{"never above the broker's count", map[string]any{"unread_deliverable_local": float64(9)}, "AGENT_INBOX", 2, 0, 2, "seat"},
		{"no figure", map[string]any{}, "AGENT_INBOX", 4, 1, 4, "broker-raw"},
	}
	for _, c := range cases {
		got, src := unreadFor(c.row, c.stream, c.raw, c.held)
		if got != c.want || src != c.src {
			t.Errorf("%s: unreadFor = %d, %q; want %d, %q", c.name, got, src, c.want, c.src)
		}
	}
}
