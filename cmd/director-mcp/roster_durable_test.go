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
// unknown, never missing. The elapsed bound is a small multiple of the
// deadline, so a regression to a deadline per row or per lookup (40 rows over
// 8 workers is five deadlines end to end) fails it.
func TestRosterLookupsShareOneDeadline(t *testing.T) {
	old := rosterLookupDeadline
	rosterLookupDeadline = 200 * time.Millisecond
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
	if d, limit := time.Since(start), 3*rosterLookupDeadline; d > limit {
		t.Errorf("flagRows took %v for %d stalled rows, want within %v (the %v deadline)", d, n, limit, rosterLookupDeadline)
	}
	for i, r := range rows {
		if r["durable"] != "unknown" || flagsOf(r) != flagDurableUnchecked {
			t.Errorf("row %d = %v, want unknown and %s", i, r, flagDurableUnchecked)
		}
	}
}

// The local half of the aae-orc-t77rr invariant: a local durable outlives the
// mail AGENT_INBOX keeps. The setup instructions declare the stream's max age,
// so the test reads it from there and from the README, which must agree.
func TestLocalInactiveOutlastsInboxMaxAge(t *testing.T) {
	re := regexp.MustCompile(`(?s)stream add AGENT_INBOX.*?--max-age (\d+)h`)
	var declared []time.Duration
	for _, p := range []string{"../../docs/getting-started.md", "../../README.md"} {
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		m := re.FindSubmatch(src)
		if m == nil {
			t.Fatalf("%s declares no AGENT_INBOX --max-age in hours", p)
		}
		hours, _ := strconv.Atoi(string(m[1]))
		declared = append(declared, time.Duration(hours)*time.Hour)
	}
	if declared[0] != declared[1] {
		t.Fatalf("getting-started.md declares %v and README.md %v for AGENT_INBOX", declared[0], declared[1])
	}
	if localConsumerInactive <= declared[0] {
		t.Errorf("localConsumerInactive = %v, not above the inbox's declared max age %v: a durable would be cleaned up while the inbox still holds its unread mail", localConsumerInactive, declared[0])
	}
	if declared[0] != 168*time.Hour {
		t.Errorf("the setup instructions declare %v for AGENT_INBOX, want 168h; update this test with the declaration", declared[0])
	}
}

// A global roster row gets the same flags, with the durable looked up on the
// hub stream of the row's cluster (or the director stream for a director).
func TestBrokerRosterFlagsGlobalRowsByTheirDurable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, _ := provision(t, ctx, url)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	if _, err := gjs.CreateStream(ctx, jetstream.StreamConfig{Name: globalStreamPrefix + "c2", Subjects: []string{"global.c2.>"}}); err != nil {
		t.Fatal(err)
	}
	mk := func(stream, durable, filter string) {
		t.Helper()
		_, err := gjs.CreateOrUpdateConsumer(ctx, stream, jetstream.ConsumerConfig{Durable: durable, FilterSubject: filter, AckPolicy: jetstream.AckExplicitPolicy})
		if err != nil {
			t.Fatal(err)
		}
	}
	mk(globalStreamPrefix+"c2", globalDurable("sup", "01HAS"), "global.c2.supervisor.inbox")
	if _, err := gjs.Publish(ctx, "global.c2.supervisor.inbox", []byte("x")); err != nil {
		t.Fatal(err)
	}

	self := Sender{AgentID: "operator", Workspace: "aae-orc", Team: "ops"}
	bus, err := connect(ctx, url, self, &globalConfig{Domain: "global", Cluster: "c1", Role: roleDirector})
	if err != nil {
		t.Fatal(err)
	}
	defer bus.close()
	if _, err := bus.setPresence(ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	kv, err := gjs.KeyValue(ctx, globalPresenceBucket)
	if err != nil {
		t.Fatal(err)
	}
	put := func(key string, rec map[string]any) {
		t.Helper()
		body, _ := json.Marshal(rec)
		if _, err := kv.Put(ctx, key, body); err != nil {
			t.Fatal(err)
		}
	}
	put("presence.c2.supervisor.01HAS", map[string]any{"agent_id": "sup", "cluster": "c2", "role": "supervisor", "instance": "01HAS", "oldest_pending_s": 0})
	put("presence.c2.supervisor.01LOST", map[string]any{"agent_id": "gone", "cluster": "c2", "role": "supervisor", "instance": "01LOST", "oldest_pending_s": 0})
	put("presence.c9.supervisor.01NOSTREAM", map[string]any{"agent_id": "far", "cluster": "c9", "role": "supervisor", "instance": "01NOSTREAM", "oldest_pending_s": 0})
	put("presence.c2.supervisor.01NOCLUSTER", map[string]any{"agent_id": "nowhere", "role": "supervisor", "instance": "01NOCLUSTER"})

	rows, warn, err := bus.rosterMerged(ctx)
	if err != nil || warn != "" {
		t.Fatalf("roster: %v %q", err, warn)
	}
	byAgent := map[string]map[string]any{}
	for _, r := range rows {
		if r["tier"] == "global" {
			a, _ := r["agent_id"].(string)
			byAgent[a] = r
		}
	}
	if r := byAgent["sup"]; r["durable"] != "present" || r["unread"] != uint64(1) || flagsOf(r) != "" {
		t.Errorf("seat with a durable = %v", r)
	}
	if r := byAgent["gone"]; r["durable"] != "missing" || flagsOf(r) != flagNoDurable {
		t.Errorf("seat without a durable = %v", r)
	}
	if r := byAgent["far"]; r["durable"] != "unknown" || flagsOf(r) != flagDurableUnchecked {
		t.Errorf("seat on a stream the hub does not have = %v, want unknown, not missing", r)
	}
	if r := byAgent["nowhere"]; r["durable"] != "unknown" || flagsOf(r) != flagPendingAgeUnreported+","+flagDurableUnchecked {
		t.Errorf("row naming no cluster = %v", r)
	}
	if r := byAgent["operator"]; r["durable"] != "present" {
		t.Errorf("this session's own director row = %v, want present", r)
	}

	// One deadline covers both tiers: with none left, every row on either tier
	// reads unknown and none reads missing.
	old := rosterLookupDeadline
	rosterLookupDeadline = time.Nanosecond
	defer func() { rosterLookupDeadline = old }()
	rows, _, err = bus.rosterMerged(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r["durable"] != "unknown" || !strings.Contains(flagsOf(r), flagDurableUnchecked) {
			t.Errorf("row after the deadline = %v, want unknown and %s", r, flagDurableUnchecked)
		}
	}
}

// The roster call has one deadline for both tiers, not one per tier: with
// every lookup stalled, the call returns after about one deadline and not two.
// The 1ns phase of the global rows test cannot tell the two apart.
func TestBrokerRosterOneDeadlineCoversBothTiers(t *testing.T) {
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
	kv, err := gjs.KeyValue(ctx, globalPresenceBucket)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		id := strconv.Itoa(i)
		putPresence(t, ctx, bus, "presence.ops.l"+id+".01L"+id, map[string]any{"agent_id": "l" + id, "team": "ops", "instance": "01L" + id, "oldest_pending_s": 0})
		body, _ := json.Marshal(map[string]any{"agent_id": "g" + id, "cluster": "c2", "role": "supervisor", "instance": "01G" + id, "oldest_pending_s": 0})
		if _, err := kv.Put(ctx, "presence.c2.supervisor.01G"+id, body); err != nil {
			t.Fatal(err)
		}
	}

	oldDeadline, oldCheck := rosterLookupDeadline, durableCheck
	rosterLookupDeadline = 400 * time.Millisecond
	durableCheck = func(ctx context.Context, _ jetstream.JetStream, _, _ string, row map[string]any, flags *[]string) {
		<-ctx.Done() // a broker that never answers
		row["durable"] = "unknown"
		*flags = append(*flags, flagDurableUnchecked)
	}
	defer func() { rosterLookupDeadline, durableCheck = oldDeadline, oldCheck }()

	start := time.Now()
	rows, _, err := bus.rosterMerged(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if d, limit := time.Since(start), rosterLookupDeadline*3/2; d > limit {
		t.Errorf("rosterMerged took %v with every lookup stalled, want within %v: one deadline for both tiers", d, limit)
	}
	var local, global int
	for _, r := range rows {
		if r["durable"] != "unknown" {
			t.Errorf("row = %v, want unknown", r)
		}
		if r["tier"] == "global" {
			global++
		} else {
			local++
		}
	}
	if local < 12 || global < 12 {
		t.Errorf("rows = %d local, %d global, want at least 12 of each", local, global)
	}
}
