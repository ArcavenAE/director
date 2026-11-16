package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
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

// The peek is bounded on the 2 s poll: with the durable's position unchanged,
// a second count reuses the first one's decision and reads the roster no more.
func TestCueCountReusesAPeekWhileTheDurableDoesNotMove(t *testing.T) {
	r, _ := forPathRig(t)
	reads := 0
	inner := r.bus.global.forFilter.roster
	r.bus.global.forFilter.roster = func(ctxArg context.Context) ([]map[string]any, error) {
		reads++
		return inner(ctxArg)
	}
	pubEnv(t, r.ctx, r.gjs, r.bus.global.cfg.inboxSubject(), "drop1", "INFORM", "FOR alpha-supervisor\nnot for you")
	if n := cueCountOf(t, r); n != 0 {
		t.Fatalf("first cue count = %d, want 0", n)
	}
	first := reads
	if first == 0 {
		t.Fatal("the first count did not read the roster, so it made no decision")
	}
	if n := cueCountOf(t, r); n != 0 {
		t.Fatalf("second cue count = %d, want 0", n)
	}
	if reads != first {
		t.Fatalf("the second count read the roster %d more times, want it to reuse the first decision", reads-first)
	}
}

// A message the filter already dropped is acked and is not counted; mail that
// arrives after it still is.
func TestCueCountAfterAPullDroppedAMessageCountsOnlyNewMail(t *testing.T) {
	r, _ := forPathRig(t)
	pubEnv(t, r.ctx, r.gjs, r.bus.global.cfg.inboxSubject(), "drop1", "INFORM", "FOR alpha-supervisor\nnot for you")
	if _, err := r.bus.receiveBatch(r.ctx, 500*time.Millisecond, 50); err != nil {
		t.Fatal(err)
	}
	if n := cueCountOf(t, r); n != 0 {
		t.Fatalf("cue count after the drop = %d, want 0", n)
	}
	pubEnv(t, r.ctx, r.gjs, r.bus.global.cfg.inboxSubject(), "plain1", "INFORM", "no FOR line")
	if n := cueCountOf(t, r); n != 1 {
		t.Fatalf("cue count after new mail = %d, want 1", n)
	}
}

// A message the filter dropped is acked, and it can sit above the ack floor
// behind a message the seat has not acked. The peek lists both, but the durable
// counts only the unacked one, so the cue's count stays at 1: the kept message.
func TestCueCountKeepsAnUnackedMessageBehindWhichAnAckedDropSits(t *testing.T) {
	r, _ := forPathRig(t)
	sub := r.bus.global.cfg.inboxSubject()
	pubEnv(t, r.ctx, r.gjs, sub, "plain0", "INFORM", "no FOR line")
	pubEnv(t, r.ctx, r.gjs, sub, "drop1", "INFORM", "FOR alpha-supervisor\nnot for you")
	batch, err := r.bus.global.cons().FetchNoWait(2)
	if err != nil {
		t.Fatal(err)
	}
	for m := range batch.Messages() {
		var e Envelope
		if err := json.Unmarshal(m.Data(), &e); err != nil {
			t.Fatal(err)
		}
		if e.MessageID == "drop1" {
			if err := m.DoubleAck(r.ctx); err != nil { // acked out of order, plain0 stays unacked
				t.Fatal(err)
			}
		}
	}
	if err := batch.Error(); err != nil {
		t.Fatal(err)
	}
	if n := cueCountOf(t, r); n != 1 {
		t.Fatalf("cue count = %d, want 1 (plain0 is unacked; drop1 is acked above the floor)", n)
	}
}

// A peek that returns its whole limit may have stopped before the end of what
// waits, so it cannot tell which listed messages are still waiting. That is a
// doubt, and a doubt cues: the count is the durable's own, with nothing left out.
func TestCueCountIsTheDurablesCountWhenThePeekReturnsItsFullLimit(t *testing.T) {
	r, _ := forPathRig(t)
	sub := r.bus.global.cfg.inboxSubject()
	for i := 0; i < summaryDefault; i++ {
		pubEnv(t, r.ctx, r.gjs, sub, fmt.Sprintf("drop%d", i), "INFORM", "FOR alpha-supervisor\nnot for you")
	}
	if n := cueCountOf(t, r); n != summaryDefault {
		t.Fatalf("cue count = %d, want %d: a full-limit peek is a doubt, so nothing is left out", n, summaryDefault)
	}
}

// The same shape as above with enough acked drops that the peek stops at its
// limit before it reaches the newest unacked message. Counting what the peek
// listed would take the acked drops off a count that includes mail past the
// limit, and the cue would read one too few.
func TestCueCountKeepsUnackedMailWhenAckedDropsFillThePeek(t *testing.T) {
	r, _ := forPathRig(t)
	sub := r.bus.global.cfg.inboxSubject()
	pubEnv(t, r.ctx, r.gjs, sub, "plain0", "INFORM", "no FOR line")
	for i := 0; i < summaryDefault; i++ {
		pubEnv(t, r.ctx, r.gjs, sub, fmt.Sprintf("drop%d", i), "INFORM", "FOR alpha-supervisor\nnot for you")
	}
	pubEnv(t, r.ctx, r.gjs, sub, "plainLast", "INFORM", "no FOR line")
	batch, err := r.bus.global.cons().FetchNoWait(summaryDefault + 2)
	if err != nil {
		t.Fatal(err)
	}
	for m := range batch.Messages() {
		var e Envelope
		if err := json.Unmarshal(m.Data(), &e); err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(e.MessageID, "drop") {
			if err := m.DoubleAck(r.ctx); err != nil { // acked out of order above the floor
				t.Fatal(err)
			}
		}
	}
	if err := batch.Error(); err != nil {
		t.Fatal(err)
	}
	if n := cueCountOf(t, r); n != 2 {
		t.Fatalf("cue count = %d, want 2 (plain0 and plainLast are unacked; the drops are acked)", n)
	}
}
