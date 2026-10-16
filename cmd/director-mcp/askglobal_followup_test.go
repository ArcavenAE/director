package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// A party read from the global tier and a local party with the same team and
// role must not share a rollup row. validToken accepts "global" as a team, and
// a cluster can be named like a local team, so team/role alone is ambiguous.
func TestRollupKeySeparatesGlobalFromLocalParties(t *testing.T) {
	globalDirector := askParty{Address: "global://director", Team: globalTeam, Role: roleDirector}
	localLookalike := askParty{Address: "agent://global/x", AgentID: "x", Team: "global", Role: roleDirector}
	if rollupKey(globalDirector) == rollupKey(localLookalike) {
		t.Fatalf("both roll up as %q", rollupKey(globalDirector))
	}
	clusterSup := askParty{Address: "global://ops/supervisor", Team: "ops", Role: "supervisor"}
	localSup := askParty{Address: "role://ops/supervisor", Team: "ops", Role: "supervisor"}
	if rollupKey(clusterSup) == rollupKey(localSup) {
		t.Fatalf("a cluster named like a local team merges: %q", rollupKey(clusterSup))
	}
	// Local parties keep their key, so existing rollups do not move.
	if got := rollupKey(localSup); got != "ops/supervisor" {
		t.Fatalf("local key = %q, want ops/supervisor", got)
	}
}

// The unread view reads the same global subject and must agree on the team
// token the ledger uses for the director.
func TestUnreadAndLedgerAgreeOnTheDirectorsTeamToken(t *testing.T) {
	team, role := teamAndRole("director", []string{"global.director.inbox"})
	if team != globalTeam || role != roleDirector {
		t.Fatalf("teamAndRole = %q/%q, want %q/%q", team, role, globalTeam, roleDirector)
	}
}

// A hub that cannot be read leaves the global table alone. If gatherGlobal
// reported ok on a failed read, the empty observation would mark every entry
// not live.
func TestAnUnreadableHubLeavesTheGlobalTableAlone(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, js := provisionAudit(t, ctx, url)
	gjs, err := jetstream.NewWithDomain(nc, "global")
	if err != nil {
		t.Fatal(err)
	}
	gkv, _ := gjs.KeyValue(ctx, globalPresenceBucket)
	_, _ = gkv.Put(ctx, "presence.cluster-b.supervisor.01ABC", []byte(`{"cluster":"cluster-b","role":"supervisor","agent_id":"sup-b","state":"idle"}`))

	r := newAskReader(js, askReaderCfg{Broker: "local", File: filepath.Join(t.TempDir(), "a.json"), Hub: gjs})
	if err := r.Pass(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	before := append([]globalEntry(nil), r.ledger.GlobalID...)
	if len(before) != 1 || !before[0].Live {
		t.Fatalf("table before = %+v, want one live entry", before)
	}

	bad, _ := jetstream.NewWithDomain(nc, "nohub")
	r.cfg.Hub = bad
	short, c2 := context.WithTimeout(ctx, 5*time.Second)
	defer c2()
	if err := r.Pass(short, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	after := r.ledger.GlobalID
	if len(after) != 1 || !after[0].Live || !after[0].LastSeen.Equal(before[0].LastSeen) {
		t.Fatalf("table after an unreadable hub = %+v, want unchanged %+v", after, before)
	}
}

// The hub principals the reader has seen are the only record that a cluster
// was once present, so a restart must not lose them (pmxxq). The second
// reader has no hub, so anything it holds came back from the store.
func TestGlobalIDsSurviveAnAskReaderRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, js := provisionAudit(t, ctx, url)
	gjs, err := jetstream.NewWithDomain(nc, "global")
	if err != nil {
		t.Fatal(err)
	}
	gkv, _ := gjs.KeyValue(ctx, globalPresenceBucket)
	_, _ = gkv.Put(ctx, "presence.cluster-b.supervisor.01ABC", []byte(`{"cluster":"cluster-b","role":"supervisor","agent_id":"sup-b","state":"idle"}`))

	r1 := newAskReader(js, askReaderCfg{Broker: "local", File: filepath.Join(t.TempDir(), "a.json"), Hub: gjs})
	if err := r1.Pass(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	want := append([]globalEntry(nil), r1.ledger.GlobalID...)
	if len(want) != 1 || want[0].Cluster != "cluster-b" {
		t.Fatalf("table before the restart = %+v, want one cluster-b entry", want)
	}

	r2 := newAskReader(js, askReaderCfg{Broker: "local", File: filepath.Join(t.TempDir(), "b.json")})
	if err := r2.Pass(ctx, time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	got := r2.ledger.GlobalID
	if len(got) != 1 || got[0].Cluster != want[0].Cluster || got[0].Role != want[0].Role ||
		!got[0].FirstSeen.Equal(want[0].FirstSeen) || !got[0].LastSeen.Equal(want[0].LastSeen) {
		t.Fatalf("table after the restart = %+v, want %+v", got, want)
	}
}
