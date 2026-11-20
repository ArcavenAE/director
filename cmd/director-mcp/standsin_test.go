package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A mirror seat (team cue-ent) that stands in for a swapped-out team (cue)
// answers mail named for that team's seats (runbook ticket A, director#696).
// The fixtures are the seat cue-ent-supervisor-g1-0 on cluster c1 and a fresh
// presence row of the seat it stands in for.

func standInFilter(standsIn string, rows []map[string]any) *forFilter {
	f := forTestFilter(rows, nil)
	f.agentID, f.team, f.role, f.standsIn = "cue-ent-supervisor-g1-0", "cue-ent", "supervisor", standsIn
	return f
}

func ultraRows() []map[string]any {
	return []map[string]any{
		forRow("cue-supervisor-g1-0", "cue", "supervisor", "c1", time.Second),
		forRow("cue-builder-g1-0", "cue", "builder", "c1", time.Second),
	}
}

func TestForFilterStandInDeliversUltraSeatMail(t *testing.T) {
	f := standInFilter("cue", ultraRows())
	for _, data := range []string{
		"FOR cue-supervisor-g1-0",
		"FOR cue-supervisor",
		"FOR role://cue/supervisor",
	} {
		if forDrop(t, f, data) {
			t.Errorf("%q was dropped; a seat standing in for cue should deliver mail for cue's supervisor", data)
		}
	}
}

func TestForFilterStandInUnsetDropsUltraSeatMail(t *testing.T) {
	f := standInFilter("", ultraRows())
	if !forDrop(t, f, "FOR cue-supervisor-g1-0") {
		t.Fatal("with no stand-in set, mail for another team's supervisor was delivered")
	}
}

func TestForFilterStandInOtherRoleDrops(t *testing.T) {
	f := standInFilter("cue", ultraRows())
	if !forDrop(t, f, "FOR cue-builder-g1-0") {
		t.Fatal("a stand-in for cue delivered mail for cue's builder to the supervisor")
	}
}

func TestForFilterStandInBadValueIgnored(t *testing.T) {
	for _, tc := range []struct{ raw, want, reason string }{
		{"cue", "cue", ""},
		{"cue.ent", "", "cue.ent"},
		{"cue/ent-supervisor-g1-0", "", "cue/ent-supervisor-g1-0"},
		{"*", "", "*"},
		{"cue-ent", "", "cue-ent"}, // the seat's own team: standing in for itself
	} {
		got, why := parseStandsInFor(tc.raw, "cue-ent")
		if got != tc.want {
			t.Errorf("parseStandsInFor(%q) team = %q, want %q", tc.raw, got, tc.want)
		}
		if tc.reason == "" && why != "" {
			t.Errorf("parseStandsInFor(%q) reason = %q, want none", tc.raw, why)
		}
		if tc.reason != "" && !strings.Contains(why, tc.reason) {
			t.Errorf("parseStandsInFor(%q) reason = %q, want a line that names the value", tc.raw, why)
		}
	}
}

// The presence row says which team the seat stands in for, and carries no such
// field when none is set.
func TestPresenceRowCarriesStandsInFor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	provision(t, ctx, url)
	for _, tc := range []struct{ id, standsIn string }{{"mirror", "cue"}, {"plain", ""}} {
		b, err := connect(ctx, url, Sender{AgentID: tc.id, Workspace: "aae-orc", Team: "ops", StandsIn: tc.standsIn}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(b.close)
		if err := b.writePresence(ctx, "idle"); err != nil {
			t.Fatal(err)
		}
		entry, err := b.kv.Get(ctx, "presence.ops."+tc.id+"."+b.instance)
		if err != nil {
			t.Fatal(err)
		}
		var rec map[string]any
		if err := json.Unmarshal(entry.Value(), &rec); err != nil {
			t.Fatal(err)
		}
		got, has := rec["stands_in_for"]
		if tc.standsIn != "" && got != tc.standsIn {
			t.Errorf("%s: stands_in_for = %v, want %q", tc.id, got, tc.standsIn)
		}
		if tc.standsIn == "" && has {
			t.Errorf("%s: stands_in_for = %v, want no field when unset", tc.id, got)
		}
	}
}
