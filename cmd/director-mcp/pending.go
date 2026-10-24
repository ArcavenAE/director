package main

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// pendingTracker keeps how long this seat's oldest unread message has waited,
// on the shim's own clock, from consumer state sampled over time (director#297).
// No broker timestamp is read, so there is no clock skew between hosts.
//
// Unread is NumPending: messages not yet delivered to this seat. Ack-pending
// mail was delivered, so a message read and held (handled mode) does not age
// toward stalled; stalled means mail nobody has drained. A message Nak'd after
// a cancel was delivered once, so it is not counted while it waits to be
// delivered again.
//
// Each sample carries the tier's NumPending, Delivered.Stream (the highest
// stream sequence delivered to this consumer) and a tag: the highest sequence
// on this consumer's own filter subjects at that sample. A tier keeps one mark
// per sample at which its tag moved, holding the tag and the previous sample's
// stamp. A consumer delivers in stream order, so the oldest unread message has
// a sequence above Delivered.Stream and at or below the first mark's tag past
// it, and above the mark before: it arrived after that previous sample and was
// there by the mark's own sample. So the age, read from the first mark whose tag
// is above Delivered.Stream, never reads low and reads high by at most one
// sample: 2 s while the cue watcher samples, 30 s on the heartbeat when it does
// not. It counts no arrivals or departures, so a message that leaves while
// another arrives inside one sample, or a redelivery, cannot shift the age.
//
// That holds from the second sample on. A tier's first sample has nothing
// before it, so a backlog already waiting at start is stamped at that sample's
// own time and reads young. A sample stamped before the last one applied (a
// slow read landing after a newer one) is dropped. With NumPending 0 every mark
// is cleared; a mark whose tag Delivered.Stream has reached is popped.
type pendingTracker struct {
	mu    sync.Mutex
	tiers map[string]*tierPending
}

type tierPending struct {
	at    time.Time // the last applied sample's stamp
	tag   uint64    // its tag
	marks []pendingMark
}

// pendingMark is a sample at which the tier's tag moved: every message with a
// sequence at or below tag and above the mark before was not there at seen.
type pendingMark struct {
	tag  uint64
	seen time.Time
}

// pendingSample is one tier's consumer state, read after at.
type pendingSample struct {
	unread    uint64 // NumPending
	delivered uint64 // Delivered.Stream
	tag       uint64 // the highest sequence on the consumer's filter subjects
	at        time.Time
	// Stubs for the red tests (round 2); not yet read.
	total     uint64
	haveTotal bool
	failed    string
}

// unverified is a stub for the red tests: it reports no failed tier yet.
func (p *pendingTracker) unverified() string { return "" }

// mark applies one sample for a tier.
func (p *pendingTracker) mark(tier string, s pendingSample) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.tiers == nil {
		p.tiers = map[string]*tierPending{}
	}
	t := p.tiers[tier]
	if t == nil {
		t = &tierPending{}
		p.tiers[tier] = t
	}
	if !t.at.IsZero() && s.at.Before(t.at) {
		return
	}
	prev := t.at
	if prev.IsZero() {
		prev = s.at // a first sample is stamped at its own time
	}
	t.at = s.at
	if s.unread == 0 {
		// The reconcile: nothing is unread, so no mark stands. The tag is not
		// read with nothing unread, so the last one is kept.
		t.marks = nil
		return
	}
	if s.tag < t.tag {
		t.marks = nil // the stream went backwards (replaced): start over
	}
	for len(t.marks) > 0 && t.marks[0].tag <= s.delivered {
		t.marks = t.marks[1:]
	}
	// New sequences since the last sample, or unread mail no mark covers (a
	// first sample, or a redelivery after every mark was passed).
	if s.tag > t.tag || len(t.marks) == 0 {
		t.marks = append(t.marks, pendingMark{tag: s.tag, seen: prev})
	}
	t.tag = s.tag
}

// age is how long the oldest waiting message has waited at now; 0 when none.
func (p *pendingTracker) age(now time.Time) time.Duration {
	d, _ := p.oldest(now)
	return d
}

// oldest is age with the tier whose front is the oldest, empty when nothing
// waits.
func (p *pendingTracker) oldest(now time.Time) (time.Duration, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var at time.Time
	tier := ""
	for name, t := range p.tiers {
		if len(t.marks) == 0 {
			continue
		}
		if s := t.marks[0].seen; at.IsZero() || s.Before(at) {
			at, tier = s, name
		}
	}
	if at.IsZero() || now.Before(at) {
		return 0, tier
	}
	return now.Sub(at), tier
}

// oldestPending is the age of this seat's oldest waiting message.
func (b *Bus) oldestPending() time.Duration { return b.pending.age(time.Now()) }

// pendingByTier reads each tier's waiting count from consumer state for the
// cue (NumPending plus NumAckPending, as before) and marks the pending tracker
// with each tier it could read. The error is the first tier that could not be
// read. A hub not yet attached is left out, as in remaining().
func (b *Bus) pendingByTier(ctx context.Context) (uint64, error) {
	at := time.Now()
	info, err := b.cons().Info(ctx)
	if err != nil {
		return 0, err
	}
	if s, ok := sampleOf(ctx, b.js, "AGENT_INBOX", info, at); ok {
		b.pending.mark("local", s)
	}
	n := info.NumPending + uint64(info.NumAckPending)
	if b.globalCfg == nil {
		return n, nil
	}
	b.globalMu.Lock()
	g := b.global
	b.globalMu.Unlock()
	if g == nil {
		return n, nil
	}
	at = time.Now()
	gi, err := g.cons().Info(ctx)
	if err != nil {
		return 0, err
	}
	if s, ok := sampleOf(ctx, g.js, g.cfg.streamName(), gi, at); ok {
		b.pending.mark("global", s)
	}
	return n + gi.NumPending + uint64(gi.NumAckPending), nil
}

// sampleOf is one tier's tracker sample. The tag is read only when something
// is unread: the highest sequence on the consumer's own filter subjects, one
// read per subject. The stream's own last sequence would not do, since a
// stream like AGENT_INBOX carries every seat's mail and its last sequence moves
// with theirs. A subject with no message counts as 0. Any other failed read
// returns ok false and the tier's sample is skipped, as a failed count read
// is: a tag read low would pop a mark early and the age could read low.
func sampleOf(ctx context.Context, js jetstream.JetStream, stream string, info *jetstream.ConsumerInfo, at time.Time) (pendingSample, bool) {
	s := pendingSample{unread: info.NumPending, delivered: info.Delivered.Stream, at: at}
	if s.unread == 0 {
		return s, true
	}
	subjects := info.Config.FilterSubjects
	if info.Config.FilterSubject != "" {
		subjects = append([]string{info.Config.FilterSubject}, subjects...)
	}
	st, err := js.Stream(ctx, stream)
	if err != nil {
		return s, false
	}
	for _, subj := range subjects {
		m, err := st.GetLastMsgForSubject(ctx, subj)
		if errors.Is(err, jetstream.ErrMsgNotFound) {
			continue
		}
		if err != nil {
			return s, false
		}
		if m.Sequence > s.tag {
			s.tag = m.Sequence
		}
	}
	return s, true
}

// samplePending is the heartbeat's sample, taken when the cue watcher is not
// sampling every poll. A failed read leaves the tracker as it was.
func (b *Bus) samplePending(ctx context.Context) { _, _ = b.pendingByTier(ctx) }
