package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// Red fixtures for the report that a message the FOR filter drops still raises a
// cue, so the seat wakes and finds nothing (aae-orc-inv9u). The count path and
// the class-aware path already leave dropped mail out (cuefilter_count_test.go,
// cuefilter_paths_test.go), so each fixture below is one candidate for a path
// that does not. A fixture that fails on its assertion at origin/main
// reproduces a wake; one that passes rules its candidate out.

// Candidate 1: the cue's oldest-message read. When the count says a message is
// waiting, the cue describes the oldest one. With a dropped message ahead of a
// kept one, the description must be the kept message, which is the one the seat
// will be handed.
func TestCueOldestMetaSkipsAGlobalMessageTheFilterDrops(t *testing.T) {
	r, _ := forPathRig(t)
	publishForPair(t, r) // drop1 first, then plain1
	m, err := busCueSource{r.bus}.oldestMeta(r.ctx)
	if err != nil || m == nil {
		t.Fatalf("oldestMeta = %v, %v; want the kept message", m, err)
	}
	if m.MessageID != "plain1" {
		t.Errorf("oldestMeta names %q, want plain1 (drop1 is dropped on the next drain)", m.MessageID)
	}
}

// Candidate 2: the doubt rule at the peek limit. A peek that returns its whole
// limit (summaryDefault) cannot say which listed messages are still unacked, so
// the count falls back to the durable's own number. An inbox holding only
// dropped mail, at or over that limit, then raises a cue.
func TestCueCountIsZeroForAnInboxOfOnlyDroppedMailPastThePeekLimit(t *testing.T) {
	r, _ := forPathRig(t)
	sub := r.bus.global.cfg.inboxSubject()
	for i := 0; i < summaryDefault+5; i++ {
		pubEnv(t, r.ctx, r.gjs, sub, fmt.Sprintf("drop%d", i), "INFORM", "FOR alpha-supervisor\nnot for you")
	}
	if n := cueCountOf(t, r); n != 0 {
		t.Errorf("cue count = %d for %d dropped messages and nothing else, want 0", n, summaryDefault+5)
	}
}

// Candidate 3: the worst-case subtraction. The cue's peek lists messages above
// the ack floor, including ones already acked. It takes the number the durable
// no longer counts (maybeConsumed) off the drop count, assuming the worst. With
// one kept message acked above the floor and one dropped message still waiting,
// that subtraction cancels the dropped message's drop, so the count is 1 for an
// inbox the seat has nothing to read in.
func TestCueCountIsZeroWhenAKeptMessageIsAckedAboveTheFloorAndOnlyDroppedMailWaits(t *testing.T) {
	r, _ := forPathRig(t)
	sub := r.bus.global.cfg.inboxSubject()
	pubEnv(t, r.ctx, r.gjs, sub, "held1", "INFORM", "no FOR line") // fetched, left unacked: holds the floor
	pubEnv(t, r.ctx, r.gjs, sub, "kept2", "INFORM", "no FOR line") // fetched and acked above the floor
	pubEnv(t, r.ctx, r.gjs, sub, "drop3", "INFORM", "FOR alpha-supervisor\nnot for you")
	batch, err := r.bus.global.cons().Fetch(2, jetstream.FetchMaxWait(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for m := range batch.Messages() {
		n++
		if n == 2 {
			if err := m.DoubleAck(r.ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	if n != 2 {
		t.Fatalf("fetched %d messages, want 2", n)
	}
	// held1 is ack-pending (the seat holds it and it counts), kept2 is acked above
	// the floor, drop3 is dropped on the next drain and must not count.
	if got := cueCountOf(t, r); got != 1 {
		t.Errorf("cue count = %d, want 1 (held1; drop3 is dropped)", got)
	}
}
