package main

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// I-685 item 8, director#731: on a shared global supervisor address, a burst of
// mail that names another seat in its FOR line must draw no cue for the seat it
// does not name. A cue for it is an empty cue: the drain drops the mail, so the
// seat's wait returns nothing, and every empty cue is a full-context turn.

// publishForeignBurst publishes n messages FOR the other supervisor, back to
// back (the one-second burst of the field evidence).
func publishForeignBurst(t *testing.T, r *foreignCueRig, n int, prefix string) {
	t.Helper()
	sub := r.bus.global.cfg.inboxSubject()
	for i := 0; i < n; i++ {
		pubEnv(t, r.ctx, r.gjs, sub, fmt.Sprintf("%s%d", prefix, i), "INFORM", "FOR alpha-supervisor\nnot for you")
	}
}

// A burst of five FOR-other messages, then the poll every two seconds for a
// window past the warning window: no cue, no warning.
func TestNoCueForABurstOfForeignMailOnASharedAddress(t *testing.T) {
	r := newForeignCueRig(t)
	publishForeignBurst(t, r, 5, "burst")
	for i := 0; i < 170; i++ {
		r.step(2 * time.Second)
	}
	if got := r.mailCues(); len(got) != 0 {
		t.Fatalf("a burst of 5 FOR-other messages drew %d cue(s): %v", len(got), got)
	}
	if len(r.warns) != 0 {
		t.Fatalf("a burst of 5 FOR-other messages drew warnings: %v", r.warns)
	}
}

// Bursts that arrive between polls, each one a second apart, still draw none.
func TestNoCueForRepeatedForeignBursts(t *testing.T) {
	r := newForeignCueRig(t)
	for b := 0; b < 6; b++ {
		publishForeignBurst(t, r, 5, fmt.Sprintf("b%d-", b))
		r.step(time.Second)
		r.step(time.Second)
	}
	for i := 0; i < 100; i++ {
		r.step(2 * time.Second)
	}
	if got := r.mailCues(); len(got) != 0 {
		t.Fatalf("six bursts of FOR-other mail drew %d cue(s): %v", len(got), got)
	}
}

// slowRoster reads the roster as a loaded host does: each read takes d, and a
// read that the caller's deadline cuts short fails, as the KV read behind it
// does when the context ends.
func slowRoster(d time.Duration, reads *int) func(context.Context) ([]map[string]any, error) {
	return func(ctx context.Context) ([]map[string]any, error) {
		*reads++
		select {
		case <-time.After(d):
			return forTestRows(), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// The field shape: a burst on a loaded host. The cue's count reads the roster
// once for each waiting FOR-other message, all inside one read deadline
// (streamReadTimeout). With 5 messages at 150 ms a read, the reads after the
// third run out of deadline, fail, and the filter delivers those messages "on a
// doubt", so the count rises and the cue fires. The next poll reads the roster
// in time, drops the mail, and the count falls: an empty cue.
func TestNoCueForAForeignBurstWhileTheRosterIsSlow(t *testing.T) {
	r := newForeignCueRig(t)
	reads := 0
	r.bus.global.forFilter.roster = slowRoster(150*time.Millisecond, &reads)
	publishForeignBurst(t, r, 5, "slow")
	r.step(2 * time.Second)
	if got := r.mailCues(); len(got) != 0 {
		t.Fatalf("a burst of 5 FOR-other messages drew %d cue(s) on the first poll, with %d roster reads: %v", len(got), reads, got)
	}
	for i := 0; i < 20; i++ {
		r.step(2 * time.Second)
	}
	if got := r.mailCues(); len(got) != 0 {
		t.Fatalf("a burst of 5 FOR-other messages drew %d cue(s): %v", len(got), got)
	}
	if len(r.warns) != 0 {
		t.Fatalf("warnings for mail the filter drops: %v", r.warns)
	}
}
