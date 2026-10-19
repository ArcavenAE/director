package main

import (
	"testing"
	"time"
)

// The tests drive the core with an injected clock and recorded scans. None of
// them touches a bus. Each refusal-shaped assertion (no absent event) is
// preceded by a control in the same shape that does produce the event, because
// "nothing was reported" passes for a core that never reports.

type wclock struct {
	wall time.Time
	mono time.Duration
}

func newWclock() *wclock {
	return &wclock{wall: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
}

func (c *wclock) tick() {
	c.wall = c.wall.Add(30 * time.Second)
	c.mono += 30 * time.Second
}

// pass advances the clock one beat and observes the rows.
func pass(w *PresenceWatch, c *wclock, rows ...watchRow) []watchEvent {
	c.tick()
	return w.Observe(c.wall, c.mono, watchScan{Rows: rows})
}

func supRow(cluster, inst string) watchRow {
	return watchRow{Key: "presence." + cluster + ".supervisor." + inst, Rec: &watchRec{State: "working", TS: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}}
}

func stateRow(cluster, inst, state string) watchRow {
	r := supRow(cluster, inst)
	r.Rec.State = state
	return r
}

// kinds lists the event kinds for one unit, in order.
func kinds(evs []watchEvent, unit string) []string {
	var out []string
	for _, e := range evs {
		if e.Unit == unit {
			out = append(out, e.Kind)
		}
	}
	return out
}

func has(evs []watchEvent, unit, kind string) bool {
	for _, e := range evs {
		if e.Unit == unit && e.Kind == kind {
			return true
		}
	}
	return false
}

func count(evs []watchEvent, kind string) int {
	n := 0
	for _, e := range evs {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

// established returns a watch that has seen c1/a for two passes.
func established(t *testing.T) (*PresenceWatch, *wclock) {
	t.Helper()
	w, c := newPresenceWatch(defaultWatchThresholds()), newWclock()
	if ev := pass(w, c, supRow("c1", "a")); !has(ev, "c1/a", "appeared") {
		t.Fatalf("CONTROL FAILED, the assertions below prove nothing: a first sighting should report appeared, got %+v", ev)
	}
	pass(w, c, supRow("c1", "a"))
	if u := w.Unit("c1/a"); u == nil || u.Status != unitPresent {
		t.Fatalf("CONTROL FAILED: unit should be present, got %+v", u)
	}
	return w, c
}

func TestWatchTwoMissedPassesIsAbsent(t *testing.T) {
	w, c := established(t)
	first := pass(w, c)
	second := pass(w, c)
	if has(first, "c1/a", "absent") {
		t.Fatalf("one missed pass reported absent: %+v", first)
	}
	if !has(second, "c1/a", "absent") || !has(second, "c1", "absent") {
		t.Fatalf("two missed passes should report the supervisor and its cluster absent, got %+v", second)
	}
	if u := w.Unit("c1/a"); u.Status != unitAbsent {
		t.Fatalf("status = %q, want absent", u.Status)
	}
}

func TestWatchOneMissedPassIsNotAbsent(t *testing.T) {
	// control: two misses do report, in the same shape.
	w, c := established(t)
	pass(w, c)
	if !has(pass(w, c), "c1/a", "absent") {
		t.Fatal("CONTROL FAILED: two misses must report absent")
	}

	w, c = established(t)
	var all []watchEvent
	all = append(all, pass(w, c)...)
	all = append(all, pass(w, c, supRow("c1", "a"))...)
	all = append(all, pass(w, c)...)
	all = append(all, pass(w, c, supRow("c1", "a"))...)
	if count(all, "absent") != 0 || count(all, "back") != 0 {
		t.Fatalf("single missed passes between sightings must report nothing, got %+v", all)
	}
	if u := w.Unit("c1/a"); u.Status != unitPresent {
		t.Fatalf("status = %q, want present", u.Status)
	}
}

func TestWatchUnreadableIsUnknownNotAbsent(t *testing.T) {
	// control: the row simply missing for two passes reports absent.
	w, c := established(t)
	pass(w, c)
	if !has(pass(w, c), "c1/a", "absent") {
		t.Fatal("CONTROL FAILED: a missing row must report absent")
	}

	w, c = established(t)
	unread := watchRow{Key: "presence.c1.supervisor.a", Unfetched: true}
	var all []watchEvent
	for i := 0; i < 4; i++ {
		all = append(all, pass(w, c, unread)...)
	}
	if count(all, "absent") != 0 {
		t.Fatalf("an unreadable row reported absent: %+v", all)
	}
	if n := count(all, "unknown"); n != 1 {
		t.Fatalf("unknown reported %d times, want once: %+v", n, all)
	}
	if u := w.Unit("c1/a"); !u.Unknown || u.Status != unitPresent {
		t.Fatalf("want present and unknown, got %+v", u)
	}
	// the row reads again: unknown clears, nothing is reported as back.
	ev := pass(w, c, supRow("c1", "a"))
	if w.Unit("c1/a").Unknown || has(ev, "c1/a", "back") {
		t.Fatalf("a row that reads again should clear unknown quietly, got %+v", ev)
	}
}

func TestWatchKeysErrorMarksAllUnknownOnce(t *testing.T) {
	w, c := established(t)
	pass(w, c, supRow("c1", "a"), supRow("c2", "b"))
	blind := func() []watchEvent {
		c.tick()
		return w.Observe(c.wall, c.mono, watchScan{KeysErr: true})
	}
	first := blind()
	if count(first, "blind") != 1 || len(first) != 1 {
		t.Fatalf("a failed keys call should give exactly one blind event, got %+v", first)
	}
	if e := first[0]; e.Text == "" {
		t.Fatalf("the blind event should say why: %+v", e)
	}
	var more []watchEvent
	for i := 0; i < 5; i++ {
		more = append(more, blind()...)
	}
	if len(more) != 0 {
		t.Fatalf("further blind passes should be silent, got %+v", more)
	}
	for _, name := range []string{"c1/a", "c2/b", "c1", "c2"} {
		u := w.Unit(name)
		if u == nil || !u.Unknown || u.Status != unitPresent {
			t.Fatalf("%s should read present and unknown, got %+v", name, u)
		}
	}
	// the hub reads again: one sighted event, no absent and no back.
	ev := pass(w, c, supRow("c1", "a"), supRow("c2", "b"))
	if count(ev, "sighted") != 1 || count(ev, "absent")+count(ev, "back") != 0 {
		t.Fatalf("recovery should say sighted once and nothing else, got %+v", ev)
	}
}

// absentUnit returns a watch whose c1/a has gone absent.
func absentUnit(t *testing.T) (*PresenceWatch, *wclock) {
	t.Helper()
	w, c := established(t)
	pass(w, c)
	if !has(pass(w, c), "c1/a", "absent") {
		t.Fatal("setup: the unit should have gone absent")
	}
	return w, c
}

func TestWatchBackNeedsTwoPasses(t *testing.T) {
	// control: two consecutive sightings report back.
	w, c := absentUnit(t)
	if has(pass(w, c, supRow("c1", "a")), "c1/a", "back") {
		t.Fatal("one sighting reported back")
	}
	if !has(pass(w, c, supRow("c1", "a")), "c1/a", "back") {
		t.Fatal("CONTROL FAILED: two consecutive sightings must report back")
	}

	// a sighting, a miss, a sighting is not two in a row.
	w, c = absentUnit(t)
	var all []watchEvent
	all = append(all, pass(w, c, supRow("c1", "a"))...)
	all = append(all, pass(w, c)...)
	all = append(all, pass(w, c, supRow("c1", "a"))...)
	if count(all, "back") != 0 {
		t.Fatalf("non-consecutive sightings reported back: %+v", all)
	}
	if u := w.Unit("c1/a"); u.Status != unitAbsent {
		t.Fatalf("status = %q, want absent", u.Status)
	}
}

// flapCycle takes the unit absent (two misses) and back (two sightings).
func flapCycle(w *PresenceWatch, c *wclock) []watchEvent {
	var all []watchEvent
	all = append(all, pass(w, c)...)
	all = append(all, pass(w, c)...)
	all = append(all, pass(w, c, supRow("c1", "a"))...)
	all = append(all, pass(w, c, supRow("c1", "a"))...)
	return all
}

func TestWatchFourTransitionsInAnHourIsFlapping(t *testing.T) {
	// control: three transitions are not flapping.
	w, c := established(t)
	pass(w, c)
	pass(w, c) // absent (1)
	pass(w, c, supRow("c1", "a"))
	pass(w, c, supRow("c1", "a")) // back (2)
	pass(w, c)
	if ev := pass(w, c); !has(ev, "c1/a", "absent") || has(ev, "c1/a", "flapping") { // absent (3)
		t.Fatalf("CONTROL FAILED: three transitions should report absent and not flapping, got %+v", ev)
	}

	w, c = established(t)
	cycle1 := flapCycle(w, c) // absent, back
	cycle2 := flapCycle(w, c) // absent, back: the fourth transition
	if count(cycle1, "flapping") != 0 {
		t.Fatalf("two transitions reported flapping: %+v", cycle1)
	}
	if n := count(cycle2, "flapping"); n != 1 {
		t.Fatalf("flapping reported %d times in the second cycle, want once: %+v", n, cycle2)
	}
	if !w.Unit("c1/a").Flapping {
		t.Fatal("unit should be flapping")
	}
	// further transitions are recorded, collapsed, and add no second flapping event.
	cycle3 := flapCycle(w, c)
	if count(cycle3, "flapping") != 0 {
		t.Fatalf("flapping reported twice: %+v", cycle3)
	}
	for _, e := range cycle3 {
		if (e.Kind == "absent" || e.Kind == "back") && !e.Collapsed {
			t.Fatalf("a transition inside a flapping spell should be collapsed: %+v", e)
		}
	}
	// 15 minutes without a transition ends the spell.
	for i := 0; i < 31; i++ {
		pass(w, c, supRow("c1", "a"))
	}
	if w.Unit("c1/a").Flapping {
		t.Fatal("flapping should end after 15 minutes without a transition")
	}
}

func TestWatchAsleepReadsAsleepUntilPastUntil(t *testing.T) {
	w, c := newPresenceWatch(defaultWatchThresholds()), newWclock()
	until := c.wall.Add(20 * time.Minute)
	sleepState := "asleep until " + until.Format(time.RFC3339)
	pass(w, c, stateRow("c1", "a", sleepState))
	pass(w, c, stateRow("c1", "a", sleepState))
	pass(w, c)
	ev := pass(w, c)
	if !has(ev, "c1/a", "asleep") || has(ev, "c1/a", "absent") {
		t.Fatalf("a unit whose last row said asleep should read asleep, not absent: %+v", ev)
	}
	if u := w.Unit("c1/a"); u.Status != unitAsleep {
		t.Fatalf("status = %q, want asleep", u.Status)
	}
	// until plus 15 minutes has not passed: no loss is reported.
	var quiet []watchEvent
	for c.wall.Before(until.Add(14 * time.Minute)) {
		quiet = append(quiet, pass(w, c)...)
	}
	if count(quiet, "absent") != 0 {
		t.Fatalf("absent reported before until plus 15 minutes: %+v", quiet)
	}
	// past it, the loss is reported once.
	var late []watchEvent
	for c.wall.Before(until.Add(17 * time.Minute)) {
		late = append(late, pass(w, c)...)
	}
	if n := count(late, "absent"); n != 2 { // the supervisor and its cluster
		t.Fatalf("want absent for the supervisor and the cluster after until plus 15 minutes, got %d: %+v", n, late)
	}
	// control: the same shape with a plain "working" last row goes absent at once.
	w2, c2 := newPresenceWatch(defaultWatchThresholds()), newWclock()
	pass(w2, c2, supRow("c1", "a"))
	pass(w2, c2, supRow("c1", "a"))
	pass(w2, c2)
	if !has(pass(w2, c2), "c1/a", "absent") {
		t.Fatal("CONTROL FAILED: a working unit should go absent after two misses")
	}
}
