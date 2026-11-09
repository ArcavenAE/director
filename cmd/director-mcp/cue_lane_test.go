package main

import (
	"context"
	"testing"
	"time"
)

// C10: the lane re-cue. A cue that holds an open lane item (a reply to an open
// operator ask) does not back off: while one is open the re-cue interval stays
// W (W, 2W, 3W, ...). With no open item the schedule is the old one: W, 4W, 8W,
// then hourly. A closed item changes nothing. The re-cue names the oldest open
// item in its meta, never its text.

func (f *fakeSource) laneOpen(context.Context) (int, *laneItem) {
	if len(f.lane) == 0 {
		return 0, nil
	}
	return len(f.lane), &f.lane[0]
}

func laneRig(t *testing.T, items ...laneItem) (*cueRig, func(d time.Duration), time.Duration) {
	t.Helper()
	r := newCueRig(t)
	r.started()
	r.cues = nil
	r.src.lane = items
	r.src.n = 1
	r.step(2 * time.Second)
	t0 := r.now
	return r, func(d time.Duration) { r.now = t0.Add(d); r.c.tick(context.Background()) }, r.c.cfg.Window
}

func TestAnOpenLaneItemReCuesAtWAndAgainAtTwoW(t *testing.T) {
	r, at, W := laneRig(t, laneItem{MessageID: "reply1", AskID: "I-1"})
	at(W + time.Second)
	if got := len(r.mailCues()); got != 2 {
		t.Fatalf("a re-cue at W, got %d cues", got)
	}
	at(2*W - time.Second)
	if got := len(r.mailCues()); got != 2 {
		t.Fatalf("no re-cue before 2W, got %d cues", got)
	}
	at(2*W + time.Second)
	if got := len(r.mailCues()); got != 3 {
		t.Fatalf("a second re-cue at 2W with a lane item open (today it waits for 4W), got %d cues", got)
	}
	at(3*W + time.Second)
	if got := len(r.mailCues()); got != 4 {
		t.Fatalf("and at 3W, got %d cues", got)
	}
}

func TestAnEmptyLaneKeepsTheOldSchedule(t *testing.T) {
	r, at, W := laneRig(t)
	at(W + time.Second)
	at(2*W + 2*time.Second)
	at(3*W + time.Second)
	if got := len(r.mailCues()); got != 2 {
		t.Fatalf("no lane item: one re-cue at W and none at 2W or 3W, got %d cues", got)
	}
	at(4*W + time.Second)
	if got := len(r.mailCues()); got != 3 {
		t.Fatalf("a re-cue at 4W, got %d cues", got)
	}
	at(8*W + time.Second)
	if got := len(r.mailCues()); got != 4 {
		t.Fatalf("a re-cue at 8W, got %d cues", got)
	}
}

func TestAClosedLaneItemChangesNothing(t *testing.T) {
	r, at, W := laneRig(t, laneItem{MessageID: "reply1", AskID: "I-1"})
	at(W + time.Second) // re-cue 1 at W, lane open
	r.src.lane = nil    // the ask is closed
	at(2*W + time.Second)
	if got := len(r.mailCues()); got != 2 {
		t.Fatalf("lane closed: no re-cue at 2W, got %d cues", got)
	}
	at(4*W + time.Second)
	if got := len(r.mailCues()); got != 3 {
		t.Fatalf("lane closed: the normal 4W re-cue, got %d cues", got)
	}
}

func TestTheReCueNamesTheOldestOpenLaneItem(t *testing.T) {
	r, at, W := laneRig(t, laneItem{MessageID: "reply1", AskID: "I-1", Sequence: 4}, laneItem{MessageID: "reply2", AskID: "I-2", Sequence: 9})
	at(W + time.Second)
	cues := r.mailCues()
	if len(cues) != 2 {
		t.Fatalf("setup: want the re-cue, got %d cues", len(cues))
	}
	m := meta(t, cues[1])
	if m["lane_oldest"] != "reply1" || m["lane_ask"] != "I-1" || m["lane_open"] != "2" {
		t.Errorf("re-cue meta = %v, want lane_oldest reply1, lane_ask I-1, lane_open 2", m)
	}
	if first := meta(t, cues[0]); first["lane_oldest"] != nil {
		t.Errorf("the first cue carries lane meta: %v", first)
	}
}
