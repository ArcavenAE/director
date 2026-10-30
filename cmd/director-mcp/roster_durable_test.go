package main

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

func rosterRowFor(t *testing.T, rows []map[string]any, agent string) map[string]any {
	t.Helper()
	for _, r := range rows {
		if r["agent_id"] == agent {
			return r
		}
	}
	t.Fatalf("no roster row for %s in %v", agent, rows)
	return nil
}

func flagsOf(r map[string]any) string {
	f, _ := r["flags"].([]string)
	return strings.Join(f, ",")
}

func putPresence(t *testing.T, ctx context.Context, bus *Bus, key string, rec map[string]any) {
	t.Helper()
	body, _ := json.Marshal(rec)
	if _, err := bus.kv.Put(ctx, key, body); err != nil {
		t.Fatal(err)
	}
}

// A live seat with no durable has a presence row and no consumer, so the
// roster must say so; a seat that has its durable shows how much waits on it,
// and a row the lookup cannot place is unknown, never missing.
func TestBrokerRosterFlagsLiveSeatsByTheirDurable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)

	self := Sender{AgentID: "operator", Workspace: "aae-orc", Team: "ops"}
	bus, err := connect(ctx, url, self, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bus.close()
	if _, err := bus.setPresence(ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	pubEnv(t, ctx, js, "agent.aae-orc.ops.operator.inbox", "r1", "INFORM", "one")
	pubEnv(t, ctx, js, "agent.aae-orc.ops.operator.inbox", "r2", "INFORM", "two")

	// Present in the roster, durable never created.
	putPresence(t, ctx, bus, "presence.ops.ghost.01GHOSTINSTANCE", map[string]any{
		"agent_id": "ghost", "team": "ops", "workspace": "aae-orc", "instance": "01GHOSTINSTANCE", "state": "idle", "oldest_pending_s": 0,
	})
	// A row from a shim that predates oldest_pending_s, and a row that names no instance.
	putPresence(t, ctx, bus, "presence.ops.old.01OLDINSTANCE", map[string]any{
		"agent_id": "old", "team": "ops", "workspace": "aae-orc", "instance": "01OLDINSTANCE", "state": "idle",
	})
	putPresence(t, ctx, bus, "presence.ops.noinst.x", map[string]any{
		"agent_id": "noinst", "team": "ops", "workspace": "aae-orc", "state": "idle", "oldest_pending_s": 0,
	})

	rows, warn, err := bus.rosterMerged(ctx)
	if err != nil || warn != "" {
		t.Fatalf("roster: %v %q", err, warn)
	}

	me := rosterRowFor(t, rows, "operator")
	if me["durable"] != "present" || me["unread"] != uint64(2) || flagsOf(me) != "" {
		t.Errorf("own row = durable %v unread %v flags %q, want present, 2, none", me["durable"], me["unread"], flagsOf(me))
	}
	ghost := rosterRowFor(t, rows, "ghost")
	if ghost["durable"] != "missing" || flagsOf(ghost) != flagNoDurable {
		t.Errorf("ghost row = durable %v flags %q, want missing and %s", ghost["durable"], flagsOf(ghost), flagNoDurable)
	}
	old := rosterRowFor(t, rows, "old")
	if old["durable"] != "missing" || flagsOf(old) != flagPendingAgeUnreported+","+flagNoDurable {
		t.Errorf("old row = durable %v flags %q", old["durable"], flagsOf(old))
	}
	noinst := rosterRowFor(t, rows, "noinst")
	if noinst["durable"] != "unknown" || flagsOf(noinst) != flagDurableUnchecked {
		t.Errorf("no-instance row = durable %v flags %q, want unknown and %s", noinst["durable"], flagsOf(noinst), flagDurableUnchecked)
	}
	if _, has := noinst["unread"]; has {
		t.Errorf("an unchecked row must not carry an unread count: %v", noinst)
	}
}

// A lookup the hub never answers marks the row unknown. It does not say the
// durable is missing, and it does not hold the roster for longer than the bound.
func TestBrokerRosterLookupFailureIsUnknownNotMissing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	provision(t, ctx, url)
	self := Sender{AgentID: "operator", Workspace: "aae-orc", Team: "ops"}
	bus, err := connect(ctx, url, self, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bus.close()

	dead, cancelDead := context.WithCancel(ctx)
	cancelDead()
	row := map[string]any{"agent_id": "operator", "instance": bus.instance, "oldest_pending_s": 0}
	bus.flagLocalRows(dead, []map[string]any{row})
	if row["durable"] != "unknown" || flagsOf(row) != flagDurableUnchecked {
		t.Errorf("row after a failed lookup = durable %v flags %q, want unknown and %s", row["durable"], flagsOf(row), flagDurableUnchecked)
	}
}

// The invariant behind aae-orc-t77rr: a global durable outlives the mail its
// stream keeps. The hub provisioner is where the stream's max age is
// declared, so the test reads it from there.
func TestGlobalInactiveOutlastsHubStreamMaxAge(t *testing.T) {
	src, err := os.ReadFile("../../probe/nats-global-tier/hub/provision.sh")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`--max-age (\d+)h`).FindSubmatch(src)
	if m == nil {
		t.Fatal("provision.sh declares no --max-age in hours")
	}
	hours, _ := strconv.Atoi(string(m[1]))
	maxAge := time.Duration(hours) * time.Hour
	if globalConsumerInactive <= maxAge {
		t.Errorf("globalConsumerInactive = %v, not above the hub streams' declared max age %v: a durable would be cleaned up while its stream still holds unread mail", globalConsumerInactive, maxAge)
	}
	if want := 8 * 24 * time.Hour; globalConsumerInactive != want {
		t.Errorf("globalConsumerInactive = %v, want %v (7-day retention plus a day)", globalConsumerInactive, want)
	}
	if maxAge != 168*time.Hour {
		t.Errorf("provision.sh declares %v for the hub streams, want 168h; update this test with the declaration", maxAge)
	}
}

// A shim mints a fresh instance, so every global durable it attaches is a new
// one; it must be created at the declared threshold, and so must the durable a
// recovery recreates after the hub lost it. A durable another process created
// earlier keeps the threshold it was created with: nothing here reaches it.
func TestBrokerNewGlobalDurablesCarryTheDeclaredThreshold(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, _ := provision(t, ctx, url)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	self := Sender{AgentID: "operator", Workspace: "aae-orc", Team: "ops"}
	bus, err := connect(ctx, url, self, &globalConfig{Domain: "global", Cluster: "c1", Role: roleDirector})
	if err != nil {
		t.Fatal(err)
	}
	defer bus.close()
	durable := globalDurable("operator", bus.instance)
	threshold := func(when string) {
		t.Helper()
		cons, err := gjs.Consumer(ctx, globalDirectorStream, durable)
		if err != nil {
			t.Fatalf("%s: %v", when, err)
		}
		if got := cons.CachedInfo().Config.InactiveThreshold; got != globalConsumerInactive {
			t.Errorf("%s: durable threshold = %v, want %v", when, got, globalConsumerInactive)
		}
	}
	if _, err := bus.globalReady(ctx); err != nil {
		t.Fatal(err)
	}
	threshold("at attach")

	if err := gjs.DeleteConsumer(ctx, globalDirectorStream, durable); err != nil {
		t.Fatal(err)
	}
	pubEnv(t, ctx, gjs, "global.director.inbox", "g1", "INFORM", "after the loss")
	if res, err := bus.receiveTiered(ctx, 12*time.Second); err != nil || res.Env == nil {
		t.Fatalf("poll after the loss: %+v %v", res, err)
	}
	threshold("after the hub lost the durable and the poll recreated it")
}

// A stalled broker must not hold list_roster for rows times the per-lookup
// bound: the whole call has one deadline, and rows it did not reach read as
// unknown, never missing.
func TestRosterLookupsShareOneDeadline(t *testing.T) {
	old := rosterLookupDeadline
	rosterLookupDeadline = 150 * time.Millisecond
	defer func() { rosterLookupDeadline = old }()

	const n = 40
	rows := make([]map[string]any, n)
	for i := range rows {
		rows[i] = map[string]any{"oldest_pending_s": 0}
	}
	start := time.Now()
	flagRows(context.Background(), rows, func(ctx context.Context, r map[string]any) []string {
		<-ctx.Done() // a broker that never answers
		r["durable"] = "unknown"
		return []string{flagDurableUnchecked}
	})
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("flagRows took %v for %d stalled rows, want about the %v deadline", d, n, rosterLookupDeadline)
	}
	for i, r := range rows {
		if r["durable"] != "unknown" || flagsOf(r) != flagDurableUnchecked {
			t.Errorf("row %d = %v, want unknown and %s", i, r, flagDurableUnchecked)
		}
	}
}
