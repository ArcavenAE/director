package main

import (
	"context"
	"testing"
	"time"
)

// Seat-granular drop (D1), local role (D2) and every leading FOR line (D3).
// The fixtures use alpha and beta for teams, as the other filter tests do.

func forDrop(t *testing.T, f *forFilter, data string) bool {
	t.Helper()
	got, _ := f.drops(context.Background(), &Envelope{Content: Content{Data: data}})
	return got
}

// seatFilter is the seat alpha-supervisor-g1-0 on cluster c1.
func seatFilter(rows []map[string]any) *forFilter {
	f := forTestFilter(rows, nil)
	f.agentID, f.team, f.role = "alpha-supervisor-g1-0", "alpha", "supervisor"
	return f
}

// RT1: a sibling seat of this seat's own team is another seat, so mail for it drops.
func TestForFilterSameTeamSiblingDrops(t *testing.T) {
	f := seatFilter([]map[string]any{
		forRow("alpha-research-supervisor-g1-0", "alpha", "research-supervisor", "c1", time.Second),
	})
	if !forDrop(t, f, "FOR alpha-research-supervisor-g1-0") {
		t.Fatal("mail for a sibling seat of the same team was delivered")
	}
}

// RT2: a same-team row on another cluster is another seat, so mail addressed
// to it with its cluster drops here.
func TestForFilterOtherClusterSameTeamDrops(t *testing.T) {
	f := seatFilter([]map[string]any{
		forRow("alpha-research-supervisor-g1-0", "alpha", "research-supervisor", "c2", time.Second),
	})
	if !forDrop(t, f, "FOR alpha-research-supervisor-g1-0@c2") {
		t.Fatal("mail for a same-team seat on another cluster was delivered")
	}
}

// Guard: mail for this seat's predecessor reaches its successor during a shift.
func TestForFilterOwnPredecessorDelivers(t *testing.T) {
	f := seatFilter([]map[string]any{
		forRow("alpha-supervisor-g1-0", "alpha", "supervisor", "c1", time.Second),
	})
	f.agentID = "alpha-supervisor-g2-0"
	if forDrop(t, f, "FOR alpha-supervisor-g1-0") {
		t.Fatal("mail for this seat's predecessor was dropped")
	}
}

// Guard: the bare team name and this seat's own names deliver.
func TestForFilterBareTeamDelivers(t *testing.T) {
	f := seatFilter([]map[string]any{
		forRow("alpha-research-supervisor-g1-0", "alpha", "research-supervisor", "c1", time.Second),
	})
	for _, data := range []string{"FOR alpha", "FOR alpha-supervisor", "FOR alpha-supervisor-g1-0", "FOR role://alpha/supervisor"} {
		if forDrop(t, f, data) {
			t.Fatalf("%q was dropped", data)
		}
	}
}

// RT3: a filter built by the production constructor for a research seat
// answers to <team>-research-supervisor, not to the team supervisor's alias,
// even though its global role is supervisor.
func TestForFilterResearchSeatIgnoresTeamSupervisorAlias(t *testing.T) {
	self := Sender{AgentID: "alpha-research-g1-0", Team: "alpha", Role: "research-supervisor"}
	rows := []map[string]any{
		forRow("alpha-supervisor-g1-0", "alpha", "supervisor", "c1", time.Second),
	}
	roster := func(context.Context) ([]map[string]any, error) { return rows, nil }
	f := newForFilter(self, globalConfig{Cluster: "c1", Role: "supervisor"}, roster)
	f.now = func() time.Time { return forNow }
	f.logf = func(string, ...any) {}
	if f.namesSelf("alpha-supervisor", "") {
		t.Fatal("a research seat answers to the team supervisor's alias")
	}
	if !f.namesSelf("alpha-research-supervisor", "") {
		t.Fatal("a research seat does not answer to its own team-role name")
	}
	if !forDrop(t, f, "FOR alpha-supervisor") {
		t.Fatal("mail for the team supervisor was delivered to its research seat")
	}
}

// RT4: every leading FOR line is read, so a seat named on line 2 delivers.
func TestForFilterReadsEveryLeadingForLine(t *testing.T) {
	f := forTestFilter(forTestRows(), nil)
	f.agentID, f.team, f.role = "beta-supervisor-g1-0", "beta", "supervisor"
	if forDrop(t, f, "FOR alpha-supervisor\nFOR beta-supervisor\nbody") {
		t.Fatal("a seat named on the second FOR line was dropped")
	}
	if !forDrop(t, f, "FOR alpha-supervisor\nFOR alpha-research-supervisor\nbody") {
		t.Fatal("two FOR lines naming other seats did not drop")
	}
}

// Guard: a FOR line after a non-FOR line is still not read.
func TestForFilterForLineAfterTextIsNotRead(t *testing.T) {
	f := forTestFilter(forTestRows(), nil)
	f.agentID, f.team, f.role = "beta-supervisor-g1-0", "beta", "supervisor"
	if !forDrop(t, f, "FOR alpha-supervisor\nhello\nFOR beta-supervisor") {
		t.Fatal("a FOR line after other text was read")
	}
}
