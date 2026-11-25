package main

import (
	"fmt"
	"testing"
)

// The cue's oldest-message read steps over mail the FOR filter drops, but it
// reads only summaryDefault (200) messages to do it, while the count reads
// cueFilterPeekMax (1000). With more dropped messages than the first read holds
// ahead of the one kept message, the read sees only dropped mail and finds
// nothing to describe, although the count says one message is waiting
// (aae-orc-9u224, item 1).
func TestCueOldestMetaStepsOverMoreDroppedMailThanTheSummaryPeekHolds(t *testing.T) {
	r, _ := forPathRig(t)
	sub := r.bus.global.cfg.inboxSubject()
	for i := 0; i < summaryMax+5; i++ {
		pubEnv(t, r.ctx, r.gjs, sub, fmt.Sprintf("drop%d", i), "INFORM", "FOR alpha-supervisor\nnot for you")
	}
	pubEnv(t, r.ctx, r.gjs, sub, "kept1", "INFORM", "no FOR line")
	if n := cueCountOf(t, r); n != 1 {
		t.Fatalf("cue count = %d, want 1: the kept message is the only one the seat will be handed", n)
	}
	m, err := busCueSource{r.bus}.oldestMeta(r.ctx)
	if err != nil || m == nil {
		t.Fatalf("oldestMeta = %v, %v; want the kept message", m, err)
	}
	if m.MessageID != "kept1" {
		t.Errorf("oldestMeta names %q, want kept1 (the %d dropped messages ahead of it are not described)", m.MessageID, summaryMax+5)
	}
}

// The same read on the one tier the pending tracker found oldest. It has its own
// peek bound, so it gets its own case; with the oldest-message read fixed, a
// bound left too narrow here would fall back to oldestMeta, which reads the local
// tier first, so a local message sits beside the global ones to make the fallback
// name the wrong message.
func TestCueOldestMetaOnGlobalStepsOverMoreDroppedMailThanTheSummaryPeekHolds(t *testing.T) {
	r, _ := forPathRig(t)
	sub := r.bus.global.cfg.inboxSubject()
	for i := 0; i < summaryMax+5; i++ {
		pubEnv(t, r.ctx, r.gjs, sub, fmt.Sprintf("drop%d", i), "INFORM", "FOR alpha-supervisor\nnot for you")
	}
	pubEnv(t, r.ctx, r.gjs, sub, "kept1", "INFORM", "no FOR line")
	// A local message too: oldestMeta reads the local tier first, so an
	// oldestMetaOn bound that is too narrow falls back to it and names this one.
	pubEnv(t, r.ctx, r.js, r.inbox, "local1", "INFORM", "local mail")
	m, err := busCueSource{r.bus}.oldestMetaOn(r.ctx, "global")
	if err != nil || m == nil {
		t.Fatalf("oldestMetaOn(global) = %v, %v; want the kept message", m, err)
	}
	if m.MessageID != "kept1" {
		t.Errorf("oldestMetaOn(global) names %q, want kept1", m.MessageID)
	}
}
