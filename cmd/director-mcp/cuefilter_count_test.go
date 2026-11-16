package main

import (
	"context"
	"errors"
	"testing"
)

// The cue wakes a seat for mail it will read (director#572). With the FOR
// filter on, a global message the filter will drop is not mail the seat is
// handed, so the cue's count leaves it out. Every doubt still counts: a roster
// read error, or a line the filter cannot place, is delivered by dropForeign,
// so the cue counts it too. The count is the cue's own source, busCueSource.

func cueCountOf(t *testing.T, r *pipeRig) uint64 {
	t.Helper()
	n, err := busCueSource{r.bus}.count(r.ctx)
	if err != nil {
		t.Fatalf("cue count: %v", err)
	}
	return n
}

// Control: mail the filter keeps is counted, with the filter on.
func TestCueCountKeepsGlobalMailTheFilterKeeps(t *testing.T) {
	r, _ := forPathRig(t)
	pubEnv(t, r.ctx, r.gjs, r.bus.global.cfg.inboxSubject(), "plain1", "INFORM", "no FOR line")
	if n := cueCountOf(t, r); n != 1 {
		t.Fatalf("cue count = %d, want 1 for a message the filter keeps", n)
	}
}

// A message the filter drops raises no cue count.
func TestCueCountLeavesOutGlobalMailTheFilterDrops(t *testing.T) {
	r, _ := forPathRig(t)
	pubEnv(t, r.ctx, r.gjs, r.bus.global.cfg.inboxSubject(), "drop1", "INFORM", "FOR alpha-supervisor\nnot for you")
	if n := cueCountOf(t, r); n != 0 {
		t.Fatalf("cue count = %d, want 0 for a message the filter drops", n)
	}
}

// Dropped and kept mail together: only the kept message counts.
func TestCueCountOfAMixedPairIsTheKeptMessageOnly(t *testing.T) {
	r, _ := forPathRig(t)
	publishForPair(t, r)
	if n := cueCountOf(t, r); n != 1 {
		t.Fatalf("cue count = %d, want 1 (plain1; drop1 is dropped)", n)
	}
}

// Every doubt goes to delivery: with the roster unreadable the filter delivers
// the message, so the cue counts it.
func TestCueCountKeepsAMessageWhenTheRosterReadFails(t *testing.T) {
	r, _ := forPathRig(t)
	r.bus.global.forFilter.roster = func(ctxArg context.Context) ([]map[string]any, error) {
		return nil, errors.New("roster unreadable")
	}
	pubEnv(t, r.ctx, r.gjs, r.bus.global.cfg.inboxSubject(), "drop1", "INFORM", "FOR alpha-supervisor\nnot for you")
	if n := cueCountOf(t, r); n != 1 {
		t.Fatalf("cue count = %d, want 1 when the roster cannot be read", n)
	}
}

// With the filter off nothing is subtracted.
func TestCueCountIsUnchangedWithTheFilterOff(t *testing.T) {
	r := newPipeRig(t, true)
	pubEnv(t, r.ctx, r.gjs, r.bus.global.cfg.inboxSubject(), "drop1", "INFORM", "FOR alpha-supervisor\nnot for you")
	if n := cueCountOf(t, r); n != 1 {
		t.Fatalf("cue count = %d, want 1 with the filter off", n)
	}
}
