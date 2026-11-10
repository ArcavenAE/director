package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// shapeRow is a global presence row with the keys list_roster shows for a live
// seat: the fields the filter reads (agent_id, team, role, cluster, ts) and the
// ones it must ignore. Shape only; every value is a placeholder.
func shapeRow(agent, team, role, cluster string, age time.Duration) map[string]any {
	return map[string]any{
		"agent_id": agent, "cluster": cluster, "cue": "on", "durable": "unknown",
		"instance": "01ZZZZZZZZZZZZZZZZZZZZZZZZ", "oldest_pending_s": float64(0), "pid": float64(4242),
		"pidspace": "p", "role": role, "state": "idle", "team": team, "tier": "global",
		"ts": forNow.Add(-age).Format(time.RFC3339), "workspace": "w",
	}
}

// shapeFilter is a seat on team "alpha" on cluster "c-a". Its roster holds a
// supervisor on team "beta" on another cluster, as a remote seat shows up in
// the shared global presence bucket, and a team-mate.
func shapeFilter() *forFilter {
	rows := []map[string]any{
		shapeRow("beta-supervisor-g4-1", "beta", "supervisor", "c-b", 3*time.Second),
		shapeRow("beta-builder-g2-0", "beta", "scoped-builder", "c-b", 3*time.Second),
		shapeRow("alpha-supervisor-g9-0", "alpha", "supervisor", "c-a", 3*time.Second),
	}
	return &forFilter{
		agentID: "alpha-supervisor-g9-0", team: "alpha", role: "supervisor", cluster: "c-a",
		roster: func(context.Context) ([]map[string]any, error) { return rows, nil },
		now:    func() time.Time { return forNow },
		logf:   func(string, ...any) {},
	}
}

// The FOR lines as they arrive in the fan-out: a seat or role name followed by
// a period, by a colon, and director's older "only (others ignore)" form.
func TestForFilterOnRealShapes(t *testing.T) {
	cases := []struct {
		name string
		data string
		drop bool
	}{
		{"seat name then a period", "FOR beta-supervisor-g4-1. From alpha-supervisor: please look", true},
		{"bare role name then a period", "FOR beta-supervisor. From alpha-supervisor: please look", true},
		{"seat name then a colon", "FOR beta-supervisor-g4-1: from alpha-supervisor, please look", true},
		{"bare role name then a colon", "FOR beta-supervisor: from alpha-supervisor", true},
		{"role uri then a period", "FOR role://beta/supervisor. From alpha-supervisor", true},
		{"the older only form is not read as names", "For beta-supervisor only (others ignore): please look", false},
		{"own bare role delivers", "FOR alpha-supervisor. From beta-supervisor: please look", false},
		{"own seat delivers", "FOR alpha-supervisor-g9-0. From beta-supervisor", false},
		{"a name with no row delivers", "FOR gamma-supervisor. From beta-supervisor", false},
		{"one own-team name among others delivers", "FOR beta-supervisor, alpha-supervisor. From beta-builder", false},
		{"no FOR line delivers", "From beta-supervisor: hello", false},
	}
	for _, c := range cases {
		e := &Envelope{}
		e.Content.Data = c.data
		got, _ := shapeFilter().drops(context.Background(), e)
		if got != c.drop {
			t.Errorf("%s: drops = %v, want %v for %q", c.name, got, c.drop, c.data)
		}
	}
}

// A remote seat's row on a different cluster still resolves a name that
// carries no @cluster suffix, and a row with no usable ts does not.
func TestForFilterRowFields(t *testing.T) {
	f := shapeFilter()
	e := &Envelope{}
	e.Content.Data = "FOR beta-supervisor. From alpha-supervisor"
	if d, _ := f.drops(context.Background(), e); !d {
		t.Fatal("a fresh row on another cluster must resolve an unsuffixed name")
	}
	old := shapeRow("beta-supervisor-g4-1", "beta", "supervisor", "c-b", 10*time.Minute)
	f.roster = func(context.Context) ([]map[string]any, error) { return []map[string]any{old}, nil }
	if d, _ := f.drops(context.Background(), e); d {
		t.Fatal("a stale row must not prove another team")
	}
	noTeam := shapeRow("beta-supervisor-g4-1", "", "supervisor", "c-b", time.Second)
	f.roster = func(context.Context) ([]map[string]any, error) { return []map[string]any{noTeam}, nil }
	if d, _ := f.drops(context.Background(), e); d {
		t.Fatal("a row with no team must not prove another team")
	}
}

// The attach line says on, off, or set but not 1, names the team the filter
// uses, and never prints the variable's value.
func TestForFilterStatusLine(t *testing.T) {
	on := forFilterStatus("1", "alpha", "c-a", "supervisor")
	if !strings.Contains(on, "for-filter on") || !strings.Contains(on, "team=alpha") || !strings.Contains(on, "cluster=c-a") {
		t.Errorf("on line = %q", on)
	}
	off := forFilterStatus("", "alpha", "c-a", "supervisor")
	if !strings.Contains(off, "for-filter off") || strings.Contains(off, "not 1") {
		t.Errorf("off line = %q", off)
	}
	odd := forFilterStatus("sekrit-true", "alpha", "c-a", "supervisor")
	if !strings.Contains(odd, "for-filter off") || !strings.Contains(odd, "set but not 1") || strings.Contains(odd, "sekrit-true") {
		t.Errorf("set-but-not-1 line = %q", odd)
	}
}

// The first decision logs how many roster rows it read, once.
func TestForFilterFirstDecisionLogsRosterRows(t *testing.T) {
	f := shapeFilter()
	var lines []string
	f.logf = func(format string, args ...any) { lines = append(lines, format) }
	e := &Envelope{}
	e.Content.Data = "FOR beta-supervisor. From alpha-supervisor"
	f.drops(context.Background(), e)
	f.drops(context.Background(), e)
	n := 0
	for _, l := range lines {
		if strings.Contains(l, "roster rows") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("roster-row lines = %d, want 1; lines %q", n, lines)
	}
}
