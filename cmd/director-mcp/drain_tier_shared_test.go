package main

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

// Three cases the architects' mutants found missing (aae-orc-b0uw2, green check
// of 0b58a2d9): the drain shares the cue's classifier, noteSent feeds the
// drain's own classifier when the cue is off, and the reply_by flip updates the
// reason with the class.

func drainScratchEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DIRECTOR_STATE", t.TempDir())
	t.Setenv("RELAY_LOG", filepath.Join(t.TempDir(), "none.md"))
}

// With the class-aware cue on, the cue classifies a message first and the drain
// returns that same classification: the same reason, and no second decode.
func TestDrainUsesTheCuesClassifierOnce(t *testing.T) {
	drainScratchEnv(t)
	cls := newClassifier()
	b := &Bus{sentCls: cls}
	items := statusInforms()
	cueReason := map[uint64]string{}
	for _, it := range items {
		data, _ := json.Marshal(it.Env)
		_, why := cls.classifyWhy(it.Tier, it.Seq, data, time.Now())
		cueReason[it.Seq] = why
	}
	if cls.decodes != len(items) {
		t.Fatalf("the cue decoded %d messages, want %d", cls.decodes, len(items))
	}
	line := b.tierDrain(items, 0)
	if cls.decodes != len(items) {
		t.Errorf("decodes rose to %d after the drain, want %d: the drain must read the cue's memo", cls.decodes, len(items))
	}
	if line != allRoutineLine {
		t.Errorf("fact line = %q, want %q", line, allRoutineLine)
	}
	for _, it := range items {
		if it.Reason != cueReason[it.Seq] || it.ProposedTier != "routine" {
			t.Errorf("seq %d: proposed_tier %q reason %q, want routine and the cue's %q", it.Seq, it.ProposedTier, it.Reason, cueReason[it.Seq])
		}
	}
	if b.ownCls != nil {
		t.Errorf("the drain made its own classifier although the cue's is set")
	}
}

// With the cue off, a send is still recorded, so the first reply to this seat's
// own REQUEST is act and a reply to its own INFORM is routine. A seat that
// never recorded the send would read both as act (an unknown id).
func TestNoteSentWithTheCueOffFeedsTheDrain(t *testing.T) {
	drainScratchEnv(t)
	b := &Bus{}
	b.noteSent(&Envelope{MessageID: "01MYASK", Performative: "REQUEST"})
	b.noteSent(&Envelope{MessageID: "01MYINFORM", Performative: "INFORM"})
	items := []drained{
		tierItem(1, "AGREE", "ok", func(e *Envelope) { e.InReplyTo = "01MYASK" }),
		tierItem(2, "AGREE", "ok", func(e *Envelope) { e.InReplyTo = "01MYINFORM" }),
	}
	if line := b.tierDrain(items, 0); line != "" {
		t.Errorf("fact line = %q, want none: one reply answers an ask", line)
	}
	if got := items[0]; got.ProposedTier != "act" || got.Reason != "reply to an open ask" {
		t.Errorf("reply to own REQUEST: proposed_tier %q reason %q, want act and %q", got.ProposedTier, got.Reason, "reply to an open ask")
	}
	if got := items[1]; got.ProposedTier != "routine" || got.Reason != "nothing asks for a turn" {
		t.Errorf("reply to own INFORM: proposed_tier %q reason %q, want routine and %q", got.ProposedTier, got.Reason, "nothing asks for a turn")
	}
}

// A routine message with a future reply_by turns act once the time passes, and
// its reason says so; the memo flip must carry the reason with the class.
func TestReplyByFlipCarriesItsReason(t *testing.T) {
	c := newClassifier()
	rb := classifyNow.Add(time.Minute).Format(time.RFC3339)
	data := classifyEnv(t, "INFORM", "fyi", rb)
	class, why := c.classifyWhy("local", 9, data, classifyNow)
	if class != classRoutine || why != "nothing asks for a turn" {
		t.Fatalf("before reply_by: %q %q, want routine and %q", class, why, "nothing asks for a turn")
	}
	class, why = c.classifyWhy("local", 9, data, classifyNow.Add(2*time.Minute))
	if class != classAct || why != "reply_by passed" {
		t.Errorf("after reply_by: %q %q, want act and %q", class, why, "reply_by passed")
	}
}
