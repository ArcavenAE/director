package main

// Tests for the cue counters (aae-orc-nqler): cues by sender and by performative, and
// re-cues apart, the before number for the coalescing work. Counts are in memory, show in
// inbox_summary as cue_counts beside cue, never reset, and say when they began.

import (
	"encoding/json"
	"testing"
	"time"
)

func TestCueCountsStartAtZeroWithASinceTime(t *testing.T) {
	r := newCueRig(t)
	got := r.c.counts()
	if got.Cues != 0 || got.Recues != 0 || len(got.BySender) != 0 || len(got.ByPerformative) != 0 {
		t.Fatalf("a new cue has counted nothing: %+v", got)
	}
	if got.Since != r.now.UTC().Format(time.RFC3339) {
		t.Fatalf("since must be the time the counting began, %s: %q", r.now.UTC().Format(time.RFC3339), got.Since)
	}
}

func TestCueCountsCountMailCuesBySenderAndPerformative(t *testing.T) {
	r := newCueRig(t)
	r.started()
	r.unanswered() // the self-test notices are not mail cues
	r.cues = nil
	r.src.oldest = &cueMeta{Sender: "sup@ws", Performative: "REQUEST", MessageID: "m1"}
	r.src.n = 1
	r.step(2 * time.Second)
	r.src.oldest = &cueMeta{Sender: "dev@ws", Performative: "INFORM", MessageID: "m2"}
	r.src.n++
	r.step(6 * time.Second)
	r.src.oldest = &cueMeta{Sender: "dev@ws", Performative: "INFORM", MessageID: "m3"}
	r.src.n++
	r.step(6 * time.Second)
	if len(r.mailCues()) != 3 {
		t.Fatalf("the rig must have sent three mail cues: %d", len(r.mailCues()))
	}
	got := r.c.counts()
	if got.Cues != 3 {
		t.Fatalf("three mail cues were sent, the self-test notices are not counted: %d", got.Cues)
	}
	if got.BySender["sup@ws"] != 1 || got.BySender["dev@ws"] != 2 || len(got.BySender) != 2 {
		t.Fatalf("by sender: %v", got.BySender)
	}
	if got.ByPerformative["REQUEST"] != 1 || got.ByPerformative["INFORM"] != 2 || len(got.ByPerformative) != 2 {
		t.Fatalf("by performative: %v", got.ByPerformative)
	}
}

func TestCueCountsKeepARecueApartFromTheCues(t *testing.T) {
	r := newCueRig(t)
	r.started()
	r.unanswered()
	r.cues = nil
	r.src.n = 1
	r.step(2 * time.Second)
	r.step(r.c.cfg.Window) // unanswered for a window: the first re-cue
	if len(r.mailCues()) != 2 {
		t.Fatalf("the rig must have sent a cue and a re-cue: %d", len(r.mailCues()))
	}
	got := r.c.counts()
	if got.Cues != 1 || got.Recues != 1 {
		t.Fatalf("one cue and one re-cue, counted apart: cues %d recues %d", got.Cues, got.Recues)
	}
	if got.BySender["sup@ws"] != 1 {
		t.Fatalf("a re-cue is not a cue by its sender: %v", got.BySender)
	}
}

func TestInboxSummaryCarriesCueCountsBesideCue(t *testing.T) {
	r := newCueRig(t)
	r.started()
	r.unanswered()
	r.src.n = 1
	r.step(2 * time.Second)
	s := &Server{cue: r.c, log: func(string, ...any) {}}
	res := map[string]any{}
	s.cueBefore("inbox_summary", json.RawMessage(`{}`))(res)
	if _, ok := res["cue"]; !ok {
		t.Fatalf("the cue key stays as it was: %v", res)
	}
	raw, err := json.Marshal(res["cue_counts"])
	if err != nil || string(raw) == "null" {
		t.Fatalf("inbox_summary must carry cue_counts: %v %v", res, err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"since", "cues", "recues", "by_sender", "by_performative"} {
		if _, ok := got[k]; !ok {
			t.Fatalf("cue_counts lacks %q: %s", k, raw)
		}
	}
	if got["cues"] != float64(1) {
		t.Fatalf("one mail cue was sent: %s", raw)
	}
	if _, ok := got["by_class"]; ok {
		t.Fatalf("by_class waits for the classifier: %s", raw)
	}
}

func TestCueCountsAreNotResetByAnInboxSummary(t *testing.T) {
	r := newCueRig(t)
	r.started()
	r.unanswered()
	r.src.n = 1
	r.step(2 * time.Second)
	s := &Server{cue: r.c, log: func(string, ...any) {}}
	s.cueBefore("inbox_summary", json.RawMessage(`{}`))(map[string]any{})
	s.cueBefore("inbox_summary", json.RawMessage(`{}`))(map[string]any{})
	if got := r.c.counts(); got.Cues != 1 {
		t.Fatalf("reading the counts must not reset them: %d", got.Cues)
	}
}
