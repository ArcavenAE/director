package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The drain's remaining figure is what the seat is still to be handed. With the
// FOR filter on, global mail the filter will drop is not that, and the cue's
// count and the pending tracker already leave it out (director#731). Before
// this change remainingRead read the raw consumer counts, so a dropped message
// still in the durable read as remaining global 1 while the drain returned
// nothing.

// A message for a live seat of another team is dropped, so nothing remains.
func TestRemainingGlobalLeavesOutMailTheFilterDrops(t *testing.T) {
	r, _ := forPathRig(t)
	pubEnv(t, r.ctx, r.gjs, r.bus.global.cfg.inboxSubject(), "drop1", "INFORM", "FOR alpha-supervisor\nnot for you")
	rem, failed := r.bus.remainingRead(r.ctx)
	if len(failed) != 0 {
		t.Fatalf("a tier could not be read: %v", failed)
	}
	if got := rem["global"]; got != 0 {
		t.Fatalf("remaining global = %d, want 0 for a message the filter will drop", got)
	}
}

// Guard: a message with no FOR line is handed over, so it still remains.
func TestRemainingGlobalKeepsMailTheFilterKeeps(t *testing.T) {
	r, _ := forPathRig(t)
	pubEnv(t, r.ctx, r.gjs, r.bus.global.cfg.inboxSubject(), "plain1", "INFORM", "no FOR line")
	rem, failed := r.bus.remainingRead(r.ctx)
	if len(failed) != 0 {
		t.Fatalf("a tier could not be read: %v", failed)
	}
	if got := rem["global"]; got != 1 {
		t.Fatalf("remaining global = %d, want 1 for a message the filter keeps", got)
	}
}

// holdOne fetches one message through the global consumer on a call that holds
// what it fetches, as a wait does (ackafter.go), and returns that call's context.
func holdOne(t *testing.T, r *pipeRig) context.Context {
	t.Helper()
	wctx, al := withAckLater(r.ctx)
	env, _, _, err := r.bus.global.fetchOne(wctx, 2*time.Second)
	if err != nil || env == nil {
		t.Fatalf("setup: the fetch returned %v, %v; want a held message", env, err)
	}
	if got := al.heldByTier()["global"]; got != 1 {
		t.Fatalf("setup: the call holds %d global messages, want 1", got)
	}
	return wctx
}

// A message the call holds is in its reply, so the hold takes it off once. It
// must not also count as a drop. Here the roster could not place the held
// message at the fetch, so it was kept as a doubt, and it can place it by the
// time of the count. With one kept message still waiting, remaining global must
// read 1; reading 0 would let last-drain record a clean drain over waiting mail.
func TestRemainingGlobalTakesAHeldMessageOffOnce(t *testing.T) {
	r, _ := forPathRig(t)
	failing := true
	r.bus.global.forFilter.roster = func(context.Context) ([]map[string]any, error) {
		if failing {
			return nil, errors.New("roster unreadable")
		}
		return forTestRows(), nil
	}
	sub := r.bus.global.cfg.inboxSubject()
	pubEnv(t, r.ctx, r.gjs, sub, "doubt1", "INFORM", "FOR alpha-supervisor\nnot for you")
	wctx := holdOne(t, r)
	pubEnv(t, r.ctx, r.gjs, sub, "plain1", "INFORM", "no FOR line")
	failing = false
	rem, failedTiers := r.bus.remainingRead(wctx)
	if len(failedTiers) != 0 {
		t.Fatalf("a tier could not be read: %v", failedTiers)
	}
	if got := rem["global"]; got != 1 {
		t.Fatalf("remaining global = %d, want 1 (plain1 waits; doubt1 is held and comes off once)", got)
	}
}

// The cue and the drain read the filter through one stored count, and the keys
// they use mean different things (the raw count, and the count after the hold).
// After the cue has counted, one dropped arrival must not let the drain reuse
// the count from before that arrival.
func TestRemainingGlobalDoesNotReuseTheCuesCount(t *testing.T) {
	r, _ := forPathRig(t)
	sub := r.bus.global.cfg.inboxSubject()
	pubEnv(t, r.ctx, r.gjs, sub, "plain1", "INFORM", "no FOR line")
	wctx := holdOne(t, r)
	if n := cueCountOf(t, r); n != 1 {
		t.Fatalf("setup: the cue's count = %d, want 1 (the held message is still ack-pending)", n)
	}
	pubEnv(t, r.ctx, r.gjs, sub, "drop1", "INFORM", "FOR alpha-supervisor\nnot for you")
	rem, failedTiers := r.bus.remainingRead(wctx)
	if len(failedTiers) != 0 {
		t.Fatalf("a tier could not be read: %v", failedTiers)
	}
	if got := rem["global"]; got != 0 {
		t.Fatalf("remaining global = %d, want 0 (plain1 is held, drop1 is dropped)", got)
	}
}

// Control for the test above: the same state with no poll by the cue reads 0, so
// the cue's stored count is what the drain was reusing.
func TestRemainingGlobalWithNoCuePollLeavesOutTheDroppedArrival(t *testing.T) {
	r, _ := forPathRig(t)
	sub := r.bus.global.cfg.inboxSubject()
	pubEnv(t, r.ctx, r.gjs, sub, "plain1", "INFORM", "no FOR line")
	wctx := holdOne(t, r)
	pubEnv(t, r.ctx, r.gjs, sub, "drop1", "INFORM", "FOR alpha-supervisor\nnot for you")
	rem, failedTiers := r.bus.remainingRead(wctx)
	if len(failedTiers) != 0 {
		t.Fatalf("a tier could not be read: %v", failedTiers)
	}
	if got := rem["global"]; got != 0 {
		t.Fatalf("remaining global = %d, want 0 (plain1 is held, drop1 is dropped)", got)
	}
}

// The other direction: what the drain stores must not be what the cue reuses.
// The drain reads first with a hold; a dropped message then arrives, and the cue's
// count must see it.
func TestCueCountDoesNotReuseTheDrainsCount(t *testing.T) {
	r, _ := forPathRig(t)
	sub := r.bus.global.cfg.inboxSubject()
	pubEnv(t, r.ctx, r.gjs, sub, "plain1", "INFORM", "no FOR line")
	wctx := holdOne(t, r)
	if rem, _ := r.bus.remainingRead(wctx); rem["global"] != 0 {
		t.Fatalf("setup: remaining global = %d, want 0 (plain1 is held)", rem["global"])
	}
	pubEnv(t, r.ctx, r.gjs, sub, "drop1", "INFORM", "FOR alpha-supervisor\nnot for you")
	if n := cueCountOf(t, r); n != 1 {
		t.Fatalf("cue count = %d, want 1 (plain1 is ack-pending and counted; drop1 is dropped)", n)
	}
}
