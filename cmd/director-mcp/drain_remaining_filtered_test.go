package main

import "testing"

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
