package main

import (
	"testing"
	"time"
)

// holdsUnreadMail is the one selector behind --older-than (applyThreshold) and
// the pending-age bound (applyBound). This pins what it picks today, and checks
// it against the two expressions it replaced on every row the reader can build:
// State is set only on a live row, and a reading row has nothing pending, so a
// reading row has no OldestSeq.
func TestHoldsUnreadMailPicksWhatTheTwoOldExpressionsPicked(t *testing.T) {
	states := []string{"", stateReading, stateBehind, stateDurableIdle, stateReadsOutside}
	for _, live := range []bool{false, true} {
		for _, st := range states {
			for _, seq := range []uint64{0, 7} {
				if st != "" && !live {
					continue // State is only ever set on a live row
				}
				if st == "" && live {
					continue // a live row always has a state
				}
				if st == stateReading && seq != 0 {
					continue // reading means nothing pending, so no oldest sequence
				}
				row := unreadDurable{Live: live, State: st, OldestSeq: seq}
				oldThreshold := (st == stateBehind || st == stateDurableIdle) && seq != 0
				oldBound := live && st != stateReadsOutside && seq != 0
				got := holdsUnreadMail(&row)
				if got != oldThreshold || got != oldBound {
					t.Errorf("live=%v state=%q seq=%d: holdsUnreadMail=%v, old threshold %v, old bound %v", live, st, seq, got, oldThreshold, oldBound)
				}
			}
		}
	}
}

// What it picks: live rows behind or durable-idle with mail waiting, and no
// other row.
func TestHoldsUnreadMailTable(t *testing.T) {
	cases := []struct {
		name string
		row  unreadDurable
		want bool
	}{
		{"behind with mail", unreadDurable{Live: true, State: stateBehind, OldestSeq: 3}, true},
		{"durable-idle with mail", unreadDurable{Live: true, State: stateDurableIdle, OldestSeq: 3}, true},
		{"reads outside the durable", unreadDurable{Live: true, State: stateReadsOutside, OldestSeq: 3}, false},
		{"reading", unreadDurable{Live: true, State: stateReading}, false},
		{"behind with no oldest sequence", unreadDurable{Live: true, State: stateBehind}, false},
		{"not live", unreadDurable{State: stateBehind, OldestSeq: 3}, false},
	}
	for _, c := range cases {
		row := c.row
		if got := holdsUnreadMail(&row); got != c.want {
			t.Errorf("%s: holdsUnreadMail = %v, want %v", c.name, got, c.want)
		}
	}
}

// Both checks mark the same rows once the age passes both limits.
func TestThresholdAndBoundMarkTheSameRows(t *testing.T) {
	r := unreadReport{Durables: []unreadDurable{
		{Durable: "a", Live: true, State: stateBehind, OldestSeq: 1, OldestAgeSeconds: 7200},
		{Durable: "b", Live: true, State: stateDurableIdle, OldestSeq: 2, OldestAgeSeconds: 7200},
		{Durable: "c", Live: true, State: stateReadsOutside, OldestSeq: 3, OldestAgeSeconds: 7200},
		{Durable: "d", Live: true, State: stateReading},
		{Durable: "e", Live: true, State: stateBehind, OldestSeq: 4, OldestAgeSeconds: 5},
	}}
	applyThreshold(&r, time.Hour)
	applyBound(&r, time.Hour)
	for _, d := range r.Durables {
		if d.OverThreshold != d.OverBound {
			t.Errorf("%s: over_threshold %v, over_bound %v, want equal", d.Durable, d.OverThreshold, d.OverBound)
		}
	}
	if !r.Durables[0].OverBound || !r.Durables[1].OverBound || r.Durables[2].OverBound || r.Durables[3].OverBound || r.Durables[4].OverBound {
		t.Errorf("marked rows wrong: %+v", r.Durables)
	}
}
