package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// The pending-age bound (I-190:1): list_roster flags a live row over it, the
// unread reader marks a live recipient over it, and neither changes anything
// but the report.

func setBound(t *testing.T, d time.Duration) {
	t.Helper()
	old := pendingAgeBound
	pendingAgeBound = d
	t.Cleanup(func() { pendingAgeBound = old })
}

func TestDefaultPendingAgeBoundIsTwiceTheCueWindow(t *testing.T) {
	if got, want := defaultPendingAgeBound(), 2*defaultCueConfig().Window; got != want || got != 600*time.Second {
		t.Errorf("default bound = %v, want 2 x the cue window = %v (600s)", got, want)
	}
}

func TestLoadPendingAgeBound(t *testing.T) {
	def := defaultPendingAgeBound()
	for _, c := range []struct {
		name, val string
		want      time.Duration
		warns     bool
	}{
		{"unset", "", def, false},
		{"minutes", "15m", 15 * time.Minute, false},
		{"seconds", "90s", 90 * time.Second, false},
		{"unparsable", "soon", def, true},
		{"no unit", "600", def, true},
		{"zero", "0s", def, true},
		{"negative", "-5m", def, true},
	} {
		var warn bytes.Buffer
		got := loadPendingAgeBound(func(k string) string {
			if k != envPendingAgeBound {
				t.Errorf("%s: read %q, want only %s", c.name, k, envPendingAgeBound)
			}
			return c.val
		}, &warn)
		if got != c.want {
			t.Errorf("%s: bound = %v, want %v", c.name, got, c.want)
		}
		if c.warns != (warn.Len() > 0) {
			t.Errorf("%s: warning = %q, want a warning: %v", c.name, warn.String(), c.warns)
		}
		if c.warns && !strings.Contains(warn.String(), envPendingAgeBound) {
			t.Errorf("%s: the warning does not name %s: %q", c.name, envPendingAgeBound, warn.String())
		}
	}
}

// A live row over the bound gets pending-age-over with its age and the bound in
// force; a row at the bound does not; a row with no age keeps unreported; a
// departed row (its presence key gone) is not on the roster to be flagged.
func TestBrokerRosterFlagsAPendingAgeOverTheBound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	provision(t, ctx, url)
	bus, err := connect(ctx, url, Sender{AgentID: "operator", Workspace: "aae-orc", Team: "ops"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bus.close()
	row := func(id string, extra map[string]any) {
		rec := map[string]any{"agent_id": id, "team": "ops", "workspace": "aae-orc", "instance": "01" + strings.ToUpper(id), "state": "idle"}
		for k, v := range extra {
			rec[k] = v
		}
		putPresence(t, ctx, bus, "presence.ops."+id+".01"+strings.ToUpper(id), rec)
	}
	row("over", map[string]any{"oldest_pending_s": 1234})
	row("atbound", map[string]any{"oldest_pending_s": 600})
	row("under", map[string]any{"oldest_pending_s": 30})
	row("noage", nil)
	row("gone", map[string]any{"oldest_pending_s": 99999})
	if err := bus.kv.Delete(ctx, "presence.ops.gone.01GONE"); err != nil {
		t.Fatal(err)
	}

	rows, warn, err := bus.rosterMerged(ctx)
	if err != nil || warn != "" {
		t.Fatalf("roster: %v %q", err, warn)
	}
	over := rosterRowFor(t, rows, "over")
	if !strings.Contains(flagsOf(over), flagPendingAgeOver) {
		t.Errorf("a row at 1234 s: flags %q, want %s", flagsOf(over), flagPendingAgeOver)
	}
	if note, _ := over["pending_age_note"].(string); note != "oldest unread message has waited 1234 s, over the 600 s bound" {
		t.Errorf("pending_age_note = %q", note)
	}
	for _, id := range []string{"atbound", "under"} {
		r := rosterRowFor(t, rows, id)
		if strings.Contains(flagsOf(r), flagPendingAgeOver) || r["pending_age_note"] != nil {
			t.Errorf("%s: flagged %q note %v, want neither", id, flagsOf(r), r["pending_age_note"])
		}
	}
	if f := flagsOf(rosterRowFor(t, rows, "noage")); !strings.Contains(f, flagPendingAgeUnreported) || strings.Contains(f, flagPendingAgeOver) {
		t.Errorf("a row with no age: flags %q, want %s only", f, flagPendingAgeUnreported)
	}
	for _, r := range rows {
		if r["agent_id"] == "gone" {
			t.Errorf("a departed row is on the roster: %v", r)
		}
	}

	// An override moves the line, and the note names the bound in force.
	setBound(t, 20*time.Second)
	rows, _, err = bus.rosterMerged(ctx)
	if err != nil {
		t.Fatal(err)
	}
	under := rosterRowFor(t, rows, "under")
	if !strings.Contains(flagsOf(under), flagPendingAgeOver) {
		t.Errorf("30 s under a 20 s bound: flags %q", flagsOf(under))
	}
	if note, _ := under["pending_age_note"].(string); !strings.Contains(note, "over the 20 s bound") {
		t.Errorf("note %q does not name the bound in force", note)
	}
}

func boundRow(t *testing.T, r unreadReport, instance string) unreadDurable {
	t.Helper()
	return rowFor(t, r, instance)
}

// unread, local tier: a live recipient whose oldest unread message is over the
// bound gets over_bound, a drained one and a recipient with no presence do not,
// a reads-outside-durable one (its mail was answered) does not, and
// --older-than neither hides nor sets the flag.
func TestUnreadMarksALiveRecipientOverTheBound(t *testing.T) {
	simulateAgreeingBrokerClock(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	slow := liveSeat(t, ctx, url, Sender{AgentID: "slow", Workspace: "w", Team: "t"})
	deaf := liveSeat(t, ctx, url, Sender{AgentID: "deaf", Workspace: "w", Team: "t"})
	outside := liveSeat(t, ctx, url, Sender{AgentID: "out", Workspace: "w", Team: "t"})
	pubEnv(t, ctx, js, "agent.w.t.slow.inbox", "s1", "INFORM", "one")
	pubEnv(t, ctx, js, "agent.w.t.slow.inbox", "s2", "INFORM", "two")
	if e, _, err := slow.receive(ctx, 2*time.Second); err != nil || e == nil {
		t.Fatalf("slow read nothing: %v", err)
	}
	pubEnv(t, ctx, js, "agent.w.t.deaf.inbox", "d1", "INFORM", "one")
	pubEnv(t, ctx, js, "agent.w.t.out.inbox", "o1", "INFORM", "one")
	replyFrom(t, ctx, js, "out", outside.instance, "o1", "r1")

	later := time.Now().Add(time.Hour)
	r := mustRead(t, ctx, js, later)
	applyBound(&r, 10*time.Minute)
	for inst, want := range map[string]bool{slow.instance: true, deaf.instance: true, outside.instance: false} {
		if d := boundRow(t, r, inst); d.OverBound != want {
			t.Errorf("%s (%s) over_bound = %v, want %v", d.AgentID, d.State, d.OverBound, want)
		}
	}

	// Not over the bound, not flagged.
	r = mustRead(t, ctx, js, time.Now())
	applyBound(&r, 10*time.Minute)
	if d := boundRow(t, r, slow.instance); d.OverBound {
		t.Errorf("a minutes-old message over a 10 minute bound: %+v", d)
	}

	// --older-than neither hides the flag nor sets it.
	r = mustRead(t, ctx, js, later)
	applyThreshold(&r, 24*time.Hour)
	applyBound(&r, 10*time.Minute)
	d := boundRow(t, r, slow.instance)
	if d.OverThreshold || !d.OverBound {
		t.Errorf("with --older-than 24h: over_threshold %v over_bound %v, want false and true", d.OverThreshold, d.OverBound)
	}

	// A recipient with no presence (its session ended) is never flagged.
	deaf.close()
	if err := slow.kv.Delete(ctx, "presence.t.deaf."+deaf.instance); err != nil {
		t.Fatal(err)
	}
	r = mustRead(t, ctx, js, later)
	applyBound(&r, 10*time.Minute)
	if gone := boundRow(t, r, deaf.instance); gone.Live || gone.OverBound {
		t.Errorf("a recipient with no presence: %+v, want not live and not over_bound", gone)
	}

	var out bytes.Buffer
	setBound(t, 10*time.Minute)
	printUnread(&out, r, unreadOpts{})
	if !strings.Contains(out.String(), "over the 600 s pending-age bound: mcp_slow_") {
		t.Errorf("text view does not mark the over-bound row:\n%s", out.String())
	}
}

// The command path: over_bound appears in --json whatever --older-than is, and
// the exit status is 0 with and without flags.
func TestUnreadCommandOverBoundJSONAndExit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	liveSeat(t, ctx, url, Sender{AgentID: "slow", Workspace: "w", Team: "t"})
	pubEnv(t, ctx, js, "agent.w.t.slow.inbox", "s1", "INFORM", "one")
	time.Sleep(1100 * time.Millisecond)

	run := func(cli cliArgs) (unreadReport, int) {
		var out, errw bytes.Buffer
		cli.mode, cli.json = "unread", true
		code := runUnreadCmd(ctx, url, cli, "global", &out, &errw)
		var r unreadReport
		if err := json.Unmarshal(out.Bytes(), &r); err != nil {
			t.Fatalf("unread --json: %v\n%s", err, out.String())
		}
		return r, code
	}
	overBound := func(r unreadReport) bool {
		for _, d := range r.Durables {
			if d.AgentID == "slow" && d.OverBound {
				return true
			}
		}
		return false
	}

	setBound(t, 500*time.Millisecond)
	flagged, codeFlagged := run(cliArgs{})
	hidden, codeHidden := run(cliArgs{olderThan: 24 * time.Hour})
	if !overBound(flagged) || !overBound(hidden) {
		t.Errorf("over_bound: plain %v, with --older-than 24h %v, want true both", overBound(flagged), overBound(hidden))
	}
	setBound(t, time.Hour)
	clear, codeClear := run(cliArgs{})
	if overBound(clear) {
		t.Error("over_bound set under a one hour bound")
	}
	if codeFlagged != 0 || codeHidden != 0 || codeClear != 0 {
		t.Errorf("exit codes %d %d %d, want 0 with and without flags", codeFlagged, codeHidden, codeClear)
	}
}

// unread, global tier: a live hub recipient over the bound gets over_bound, and
// one with no presence does not.
func TestUnreadMarksAHubRecipientOverTheBound(t *testing.T) {
	simulateAgreeingBrokerClock(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, _ := provision(t, ctx, url)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	if _, err := gjs.CreateStream(ctx, jetstream.StreamConfig{Name: "GLOBAL_TO_edge1", Subjects: []string{"global.edge1.>"}}); err != nil {
		t.Fatal(err)
	}
	sup, err := connect(ctx, url, Sender{AgentID: "sup", Workspace: "w", Team: "t"}, &globalConfig{Domain: "global", Cluster: "edge1", Role: roleSupervisor})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sup.close)
	g, err := sup.globalReady(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.writePresence(ctx, sup.self, sup.instance, sup.pid, "idle"); err != nil {
		t.Fatal(err)
	}
	pubEnv(t, ctx, gjs, "global.edge1.supervisor.inbox", "g1", "REQUEST", "from director")

	reports, err := readUnreadGlobal(ctx, nc, "global", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for i := range reports {
		applyBound(&reports[i], 10*time.Minute)
		for _, d := range reports[i].Durables {
			if reports[i].Stream == "GLOBAL_TO_edge1" && d.Instance == sup.instance {
				found = true
				if !d.Live || !d.OverBound {
					t.Errorf("hub row %+v, want live and over_bound", d)
				}
			}
			if !d.Live && d.OverBound {
				t.Errorf("a hub row with no presence is over_bound: %+v", d)
			}
		}
	}
	if !found {
		t.Fatalf("no hub row for the supervisor in %+v", reports)
	}
}
