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
	for i := 0; i < summaryDefault+5; i++ {
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
		t.Errorf("oldestMeta names %q, want kept1 (the %d dropped messages ahead of it are not described)", m.MessageID, summaryDefault+5)
	}
}
