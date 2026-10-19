package main

import (
	"testing"
	"time"
)

// Build item 2 (sim/design/presence-watch.md, D3): the watcher's own downtime
// and its host's sleep are gaps it records, never transitions it infers.

// skip moves the wall clock by wall and the monotonic reading by mono, then
// observes. A host sleep is a wall jump with a small monotonic one.
func (c *wclock) skip(w *PresenceWatch, wall, mono time.Duration, rows ...watchRow) []watchEvent {
	c.wall = c.wall.Add(wall)
	c.mono += mono
	return w.Observe(c.wall, c.mono, watchScan{Rows: rows})
}

func transitionsIn(evs []watchEvent) int { return count(evs, "absent") + count(evs, "back") }

func TestWatchGapIsNotATransition(t *testing.T) {
	// control: a late pass is recorded as a down gap, so the assertions below
	// are about a core that does see gaps.
	w, c := established(t)
	before := c.wall
	ev := c.skip(w, 10*time.Minute, 10*time.Minute, supRow("c1", "a"))
	if count(ev, "watch-down") != 1 || len(w.Downs) != 1 || !w.Downs[0].From.Equal(before) {
		t.Fatalf("CONTROL FAILED: a pass 10 minutes late should record one down gap from the last pass, got %+v, downs %+v", ev, w.Downs)
	}
	if !has(ev, "c1/a", "present-after-gap") || transitionsIn(ev) != 0 || count(ev, "back") != 0 {
		t.Fatalf("a unit still present after a gap should read present-after-gap and no transition, got %+v", ev)
	}

	// absent before the gap, seen once after it: present after the gap, never back.
	w, c = absentUnit(t)
	ev = c.skip(w, 10*time.Minute, 10*time.Minute, supRow("c1", "a"))
	if !has(ev, "c1/a", "present-after-gap") || !has(ev, "c1", "present-after-gap") || count(ev, "back") != 0 {
		t.Fatalf("a unit absent before a gap and present after it should not report back, got %+v", ev)
	}
	if w.Unit("c1/a").Status != unitPresent {
		t.Fatalf("status = %q, want present", w.Unit("c1/a").Status)
	}

	// a new instance first sighted on the gap pass, with its cluster absent before
	// the gap, reads the same as the old instance returning: the cluster is
	// present after the gap, and neither the instance nor the cluster reports
	// back, now or on the next pass (design D3, "never infers that a cluster left
	// and returned inside a gap"). The control is the old instance, above.
	w, c = absentUnit(t)
	ev = c.skip(w, 10*time.Minute, 10*time.Minute, supRow("c1", "i2"))
	next := pass(w, c, supRow("c1", "i2"))
	if !has(ev, "c1/i2", "present-after-gap") || !has(ev, "c1", "present-after-gap") {
		t.Fatalf("a new instance on the gap pass should read present-after-gap for itself and its cluster, got %+v", ev)
	}
	if count(ev, "back")+count(next, "back") != 0 || len(next) != 0 {
		t.Fatalf("no back may follow, got gap pass %+v then %+v", ev, next)
	}
	if n := len(w.Unit("c1").Transitions); n != 1 {
		t.Fatalf("the cluster has %d transitions, want only its one absent", n)
	}

	// absent before the gap, still unseen after it: reported as absent before the gap, once.
	w, c = absentUnit(t)
	ev = c.skip(w, 10*time.Minute, 10*time.Minute)
	if !has(ev, "c1/a", "absent-before-gap") || transitionsIn(ev) != 0 {
		t.Fatalf("a unit absent before and after a gap should read absent-before-gap, got %+v", ev)
	}

	// present before the gap, unseen after it: one miss is not absence, the
	// second consecutive miss after the gap is.
	w, c = established(t)
	ev = c.skip(w, 10*time.Minute, 10*time.Minute)
	if transitionsIn(ev) != 0 {
		t.Fatalf("the first pass after a gap must not infer absence, got %+v", ev)
	}
	if !has(pass(w, c), "c1/a", "absent") {
		t.Fatal("the second consecutive miss after the gap should report absent")
	}
}

func TestWatchWallAheadOfMonotonicRecordsHostSleep(t *testing.T) {
	// control: the same wall jump with a matching monotonic jump is a down gap.
	w, c := established(t)
	if ev := c.skip(w, 10*time.Minute, 10*time.Minute, supRow("c1", "a")); count(ev, "host-sleep") != 0 || count(ev, "watch-down") != 1 {
		t.Fatalf("CONTROL FAILED: a wall jump the monotonic clock also made is not a host sleep, got %+v", ev)
	}

	w, c = established(t)
	from := c.wall
	ev := c.skip(w, 10*time.Minute, 30*time.Second, supRow("c1", "a"))
	if count(ev, "host-sleep") != 1 || count(ev, "watch-down") != 0 {
		t.Fatalf("wall time 9.5 minutes ahead of the monotonic clock should record one host sleep and no down gap, got %+v", ev)
	}
	if len(w.Sleeps) != 1 || !w.Sleeps[0].From.Equal(from) || !w.Sleeps[0].To.Equal(c.wall) {
		t.Fatalf("sleep gap = %+v, want %v to %v", w.Sleeps, from, c.wall)
	}
	if transitionsIn(ev) != 0 || w.Unit("c1/a").Status != unitPresent {
		t.Fatalf("a host sleep must not cause a transition, got %+v", ev)
	}
	// a 20-second wall lead is inside the tolerance and records nothing.
	w, c = established(t)
	ev = c.skip(w, 50*time.Second, 30*time.Second, supRow("c1", "a"))
	if count(ev, "host-sleep") != 0 || len(w.Sleeps) != 0 {
		t.Fatalf("a wall lead under 30 seconds is not a sleep, got %+v", ev)
	}
}

func TestWatchAbsenceSubtractsHostSleep(t *testing.T) {
	w, c := established(t)
	lastSeen := c.wall
	pass(w, c)
	if !has(pass(w, c), "c1/a", "absent") {
		t.Fatal("setup: the unit should have gone absent")
	}
	// control: with no sleep, the duration is the wall time since it was last seen.
	if d := w.AbsenceDuration("c1/a", c.wall); d != c.wall.Sub(lastSeen) {
		t.Fatalf("CONTROL FAILED: duration without sleep = %v, want %v", d, c.wall.Sub(lastSeen))
	}
	c.skip(w, 10*time.Minute, 30*time.Second) // the host sleeps about 9.5 minutes
	pass(w, c)
	now := c.wall
	want := now.Sub(lastSeen) - w.Sleeps[0].To.Sub(w.Sleeps[0].From)
	if got := w.AbsenceDuration("c1/a", now); got != want {
		t.Fatalf("duration = %v, want %v (wall %v less the sleep %v)", got, want, now.Sub(lastSeen), w.Sleeps[0].To.Sub(w.Sleeps[0].From))
	}
	if got := w.AbsenceDuration("nope", now); got != 0 {
		t.Fatalf("an unknown unit has no absence, got %v", got)
	}
	if w.Unit("c1/a").Status != unitAbsent {
		t.Fatal("the unit stays absent through the sleep")
	}
}
