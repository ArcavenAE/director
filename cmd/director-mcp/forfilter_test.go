package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

var forNow = time.Date(2026, 10, 31, 2, 0, 0, 0, time.UTC)

func forRow(agent, team, role, cluster string, age time.Duration) map[string]any {
	return map[string]any{
		"agent_id": agent, "team": team, "role": role, "cluster": cluster,
		"ts": forNow.Add(-age).Format(time.RFC3339),
	}
}

// forTestFilter is the seat ops/operator on cluster c1, with a roster of one
// seat on another team, one team-mate and one stale row.
func forTestFilter(rows []map[string]any, err error) *forFilter {
	return &forFilter{
		agentID: "operator-g3-0", team: "ops", role: "supervisor", cluster: "c1",
		roster: func(context.Context) ([]map[string]any, error) { return rows, err },
		now:    func() time.Time { return forNow },
		logf:   func(string, ...any) {},
	}
}

func forTestRows() []map[string]any {
	return []map[string]any{
		forRow("alpha-supervisor-g1-0", "alpha", "supervisor", "c1", time.Second),
		forRow("alpha-research-supervisor-g1-0", "alpha", "research-supervisor", "c1", time.Second),
		forRow("ops-builder-g2-0", "ops", "builder", "c1", time.Second),
		forRow("old-seat-g1-0", "old", "supervisor", "c1", 10*time.Minute),
	}
}

func TestForFilterDecisions(t *testing.T) {
	cases := []struct {
		name string
		data string
		drop bool
	}{
		{"another team's seat drops", "FOR alpha-supervisor\nplease look", true},
		{"full agent id drops", "FOR alpha-supervisor-g1-0", true},
		{"team-role form drops", "FOR alpha-research-supervisor", true},
		{"role uri drops", "FOR role://alpha/supervisor", true},
		{"several other-team seats drop", "FOR alpha-supervisor, alpha-research-supervisor.", true},
		{"and between other-team seats drops", "FOR alpha-supervisor and alpha-research-supervisor", true},
		{"first non-blank line is read", "\n  \nFOR alpha-supervisor", true},
		{"this seat delivers", "FOR operator", false},
		{"this seat's agent id delivers", "FOR operator-g3-0", false},
		{"this seat's team-role delivers", "FOR ops-supervisor", false},
		{"this seat's role uri delivers", "FOR role://ops/supervisor", false},
		{"this seat on this cluster delivers", "FOR operator@c1", false},
		{"this team delivers", "FOR ops", false},
		{"a team-mate delivers", "FOR ops-builder", false},
		{"no FOR delivers", "hello there", false},
		{"empty delivers", "", false},
		{"FOR on line 2 delivers", "hello\nFOR alpha-supervisor", false},
		{"lower case for delivers", "for alpha-supervisor", false},
		{"indented FOR delivers", " FOR alpha-supervisor", false},
		{"FOR with no target delivers", "FOR", false},
		{"free text after FOR delivers", "FOR alpha-supervisor: please look at Friday's list!", false},
		{"one bad token spoils the line", "FOR alpha-supervisor Hello!", false},
		{"other seat and this seat delivers", "FOR alpha-supervisor and operator", false},
		{"other seat and this team delivers", "FOR alpha-supervisor, ops", false},
		{"unknown name delivers", "FOR nobody-here", false},
		{"one unknown name among others delivers", "FOR alpha-supervisor and nobody-here", false},
		{"another team's name is not a seat", "FOR alpha", false},
		{"seat on another cluster than the row delivers", "FOR alpha-supervisor@corporate", false},
		{"stale row delivers", "FOR old-seat", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := forTestFilter(forTestRows(), nil)
			e := &Envelope{Content: Content{Data: c.data}}
			if got, _ := f.drops(context.Background(), e); got != c.drop {
				t.Fatalf("%q: drop = %v, want %v", c.data, got, c.drop)
			}
		})
	}
}

// A row on this team that answers to the same name keeps the mail.
func TestForFilterSameNameOnThisTeamDelivers(t *testing.T) {
	rows := append(forTestRows(), forRow("shared-g1-0", "ops", "shared", "c1", time.Second),
		forRow("shared-g2-0", "alpha", "shared", "c1", time.Second))
	f := forTestFilter(rows, nil)
	if got, _ := f.drops(context.Background(), &Envelope{Content: Content{Data: "FOR shared"}}); got {
		t.Fatal("a name held by a seat of this team dropped")
	}
}

// A roster that cannot be read, or a row with no team or no readable ts,
// proves nothing.
func TestForFilterUnprovenRosterDelivers(t *testing.T) {
	data := &Envelope{Content: Content{Data: "FOR alpha-supervisor"}}
	if got, _ := forTestFilter(nil, errors.New("hub down")).drops(context.Background(), data); got {
		t.Fatal("a roster error dropped")
	}
	noTeam := forRow("alpha-supervisor-g1-0", "", "supervisor", "c1", time.Second)
	if got, _ := forTestFilter([]map[string]any{noTeam}, nil).drops(context.Background(), data); got {
		t.Fatal("a row with no team dropped")
	}
	noTS := forRow("alpha-supervisor-g1-0", "alpha", "supervisor", "c1", time.Second)
	delete(noTS, "ts")
	if got, _ := forTestFilter([]map[string]any{noTS}, nil).drops(context.Background(), data); got {
		t.Fatal("a row with no ts dropped")
	}
}

func TestForFilterReturnsTheForText(t *testing.T) {
	f := forTestFilter(forTestRows(), nil)
	drop, text := f.drops(context.Background(), &Envelope{Content: Content{Data: "FOR alpha-supervisor, alpha-research-supervisor\nbody"}})
	if !drop || text != "alpha-supervisor, alpha-research-supervisor" {
		t.Fatalf("drop = %v, text = %q", drop, text)
	}
}

// The filter runs in the global fetch: a message for another team's seat is
// acked and logged with its id, the next message is delivered, and a message
// for a seat whose roster row is stale is delivered rather than dropped.
func TestGlobalFetchDropsMailForAnotherTeam(t *testing.T) {
	r := newPipeRig(t, true)
	g := r.bus.global
	var mu sync.Mutex
	var logs []string
	f := forTestFilter(forTestRows(), nil)
	f.logf = func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		logs = append(logs, strings.TrimSpace(fmt.Sprintf(format, args...)))
	}
	g.forFilter = f

	sub := g.cfg.inboxSubject()
	pubEnv(t, r.ctx, r.gjs, sub, "drop1", "INFORM", "FOR alpha-supervisor\nnot for you")
	pubEnv(t, r.ctx, r.gjs, sub, "stale1", "INFORM", "FOR old-seat\nstale roster row")
	pubEnv(t, r.ctx, r.gjs, sub, "plain1", "INFORM", "no FOR line")

	var got []string
	for i := 0; i < 3; i++ {
		e, _, _, err := g.fetchOne(r.ctx, 2*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if e == nil {
			break
		}
		got = append(got, e.MessageID)
	}
	if strings.Join(got, ",") != "stale1,plain1" {
		t.Fatalf("delivered %v, want [stale1 plain1]", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(logs) != 1 || !strings.Contains(logs[0], "global ack drop1 FOR alpha-supervisor") {
		t.Fatalf("drop log = %q, want one line naming drop1", logs)
	}
	// The dropped message was acked: a fresh pull finds nothing left.
	if e, _, _, err := g.fetchOne(r.ctx, 300*time.Millisecond); err != nil || e != nil {
		t.Fatalf("a message is still waiting after the fetches: %v, %v", e, err)
	}
}

// With the filter off the same message is delivered.
func TestGlobalFetchDeliversForLinesWhenFilterOff(t *testing.T) {
	r := newPipeRig(t, true)
	g := r.bus.global
	if g.forFilter != nil {
		t.Fatal("the filter is on without DIRECTOR_FOR_FILTER")
	}
	pubEnv(t, r.ctx, r.gjs, g.cfg.inboxSubject(), "m1", "INFORM", "FOR alpha-supervisor\nbody")
	e, _, _, err := g.fetchOne(r.ctx, 2*time.Second)
	if err != nil || e == nil || e.MessageID != "m1" {
		t.Fatalf("fetch = %v, %v; want m1", e, err)
	}
}

// A name this seat answers to delivers even when the roster holds no fresh row
// of this seat and a seat of another team shares that base name. Only the
// namesSelf check keeps the mail: without it the other team's row proves the
// name belongs elsewhere and the message drops.
func TestForFilterBaseNameSharedWithAnotherTeamDelivers(t *testing.T) {
	rows := []map[string]any{forRow("operator-g1-0", "alpha", "supervisor", "c1", time.Second)}
	f := forTestFilter(rows, nil)
	if got, _ := f.drops(context.Background(), &Envelope{Content: Content{Data: "FOR operator"}}); got {
		t.Fatal("the base name of this seat dropped because another team's seat shares it")
	}
	if got, _ := f.drops(context.Background(), &Envelope{Content: Content{Data: "FOR operator-g1-0"}}); !got {
		t.Fatal("the other team's full agent id did not drop, so the fixture proves nothing")
	}
}
