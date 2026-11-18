package main

import "testing"

// RT6: the cue's count leaves out mail the filter will drop (leaveOutDropped),
// but two other paths that list the global tier's waiting mail did not, so a
// seat with the filter on was told of, and woken for, mail it would never be
// handed. inbox_summary counted it as waiting, and the class-aware cue's peek
// classified it as a message to wake for.

// inbox_summary lists and counts what the seat will be handed, not what the
// filter will drop on the next drain.
func TestInboxSummaryLeavesOutGlobalMailTheFilterDrops(t *testing.T) {
	r, _ := forPathRig(t)
	publishForPair(t, r)
	res, err := r.bus.summarizeInbox(r.ctx, summaryDefault)
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.Total != 1 {
		t.Errorf("summary total = %d, want 1 (plain1; drop1 is dropped on the next drain)", res.Summary.Total)
	}
	if res.Expected["global"] != 1 || res.Read["global"] != 1 {
		t.Errorf("global expected %d, read %d; want 1 and 1", res.Expected["global"], res.Read["global"])
	}
}

// The class-aware cue reads the waiting set to decide what to wake for; mail
// the filter will drop is not in it.
func TestCueWaitingLeavesOutGlobalMailTheFilterDrops(t *testing.T) {
	r, _ := forPathRig(t)
	publishForPair(t, r)
	items, err := busCueSource{r.bus}.waiting(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	var global []string
	for _, it := range items {
		if it.Tier == "global" && it.Env != nil {
			global = append(global, it.Env.MessageID)
		}
	}
	if len(global) != 1 || global[0] != "plain1" {
		t.Errorf("waiting global items = %v, want [plain1]", global)
	}
}

// Control: with the filter off both paths show everything.
func TestInboxSummaryAndCueWaitingKeepEverythingWithTheFilterOff(t *testing.T) {
	r := newPipeRig(t, true)
	publishForPair(t, r)
	res, err := r.bus.summarizeInbox(r.ctx, summaryDefault)
	if err != nil || res.Summary.Total != 2 {
		t.Fatalf("summary total = %d (%v), want 2 with the filter off", res.Summary.Total, err)
	}
	items, err := busCueSource{r.bus}.waiting(r.ctx)
	if err != nil || len(items) != 2 {
		t.Fatalf("waiting = %d items (%v), want 2 with the filter off", len(items), err)
	}
}
