package main

import (
	"testing"
	"time"
)

// cashp C1 (sim/design/cluster-gone-alert.md section 4): a pass is sighted only
// when the director's own current row is in it. A pass that is not sighted is
// self-unseen: every unit unknown, no absence inferred, one event.

func selfPass(w *PresenceWatch, c *wclock, self bool, rows ...watchRow) []watchEvent {
	c.tick()
	sc := watchScan{Rows: rows, SelfCheck: true, SelfSeen: self, SelfWant: c.wall, SelfTS: c.wall}
	return w.Observe(c.wall, c.mono, sc)
}

func establishedSelf(t *testing.T) (*PresenceWatch, *wclock) {
	t.Helper()
	w, c := newPresenceWatch(defaultWatchThresholds()), newWclock()
	selfPass(w, c, true, supRow("c1", "a"))
	selfPass(w, c, true, supRow("c1", "a"))
	if u := w.Unit("c1/a"); u == nil || u.Status != unitPresent {
		t.Fatalf("CONTROL FAILED: c1/a should be present, got %+v", u)
	}
	return w, c
}

func TestSelfSightedPassStillReportsAbsence(t *testing.T) {
	w, c := establishedSelf(t)
	selfPass(w, c, true)
	if !has(selfPass(w, c, true), "c1/a", "absent") {
		t.Fatal("CONTROL FAILED: two sighted passes without the row must report absent")
	}
}

func TestSelfUnseenPassRaisesNoAbsence(t *testing.T) {
	w, c := establishedSelf(t)
	var all []watchEvent
	for i := 0; i < 4; i++ {
		all = append(all, selfPass(w, c, false)...)
	}
	if count(all, "absent") != 0 || count(all, "lost") != 0 {
		t.Fatalf("a pass without the director's own row must not call a cluster gone, got %+v", all)
	}
	if count(all, "self-unseen") != 1 {
		t.Fatalf("self-unseen should be recorded once, got %+v", all)
	}
	if u := w.Unit("c1/a"); !u.Unknown || u.Status != unitPresent {
		t.Fatalf("units should read unknown, still present: %+v", u)
	}
}

func TestSelfUnseenWhenOwnRowIsOlderThanTheLastWrite(t *testing.T) {
	w, c := establishedSelf(t)
	c.tick()
	sc := watchScan{SelfCheck: true, SelfSeen: true, SelfWant: c.wall, SelfTS: c.wall.Add(-time.Minute)}
	if ev := w.Observe(c.wall, c.mono, sc); count(ev, "self-unseen") != 1 {
		t.Fatalf("a row older than the one this session wrote is another hub's copy, got %+v", ev)
	}
}

func TestSelfSeenAgainIsRecordedOnceAndAbsenceCountsAgain(t *testing.T) {
	w, c := establishedSelf(t)
	selfPass(w, c, false)
	back := selfPass(w, c, true, supRow("c1", "a"))
	if count(back, "self-seen") != 1 {
		t.Fatalf("the return of the own row should be recorded once, got %+v", back)
	}
	selfPass(w, c, true)
	if !has(selfPass(w, c, true), "c1/a", "absent") {
		t.Fatal("after a sighted return, two missed passes must report absent again")
	}
}

func TestSelfUnseenIsNotWrittenByAScanThatMakesNoClaim(t *testing.T) {
	w, c := established(t)
	pass(w, c)
	if !has(pass(w, c), "c1/a", "absent") {
		t.Fatal("a scan with SelfCheck false makes no claim and behaves as before")
	}
}
