package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// pendingTracker keeps how long this seat's oldest unread message has waited,
// on the shim's own clock, from consumer and stream state sampled over time
// (director#297). No broker timestamp is read, so there is no clock skew
// between hosts.
//
// Unread is NumPending: messages not yet delivered to this seat. Ack-pending
// mail was delivered, so a message read and held (handled mode) does not age
// toward stalled; stalled means mail nobody has drained. A message Nak'd after
// a cancel was delivered once, so it is not counted while it waits to be
// delivered again.
//
// Each sample carries the tier's NumPending and, while anything is unread, T:
// the number of messages the stream holds on this consumer's own filter
// subjects, from STREAM.INFO with a subjects filter (a verb the hub grants a
// leaf on its inbox stream). The inbox streams use limits retention, so a
// message stays on its subject after it is read, and T rises by exactly the
// arrivals between two samples. A stream moved to work-queue or interest
// retention would drop read messages from T, and the counts would stop meaning
// arrivals.
//
// Each tier keeps a FIFO of (how many, first seen). Arrivals, max(0, T -
// T_prev), are pushed stamped at the PREVIOUS sample, the latest moment they
// were known not to be there. Then the batches reconcile to NumPending: the
// front is popped while the total is above it, since a consumer delivers in
// stream order, and any shortfall is pushed at the previous sample. A message
// that leaves while another arrives inside one sample therefore cannot pass its
// age on, and a redelivery moves neither T nor NumPending. A message aging out
// by the stream's limits while another arrives makes T undercount arrivals;
// the shortfall push stamps the newcomer at the previous sample, so the age
// does not read low. A drop with no delivery (an age-out or a delete) keeps
// older batches, so the age reads high.
//
// From the second sample on the age never reads low, and it reads high by at
// most one sample: 2 s while the cue watcher samples, 30 s on the heartbeat
// when it does not. A tier's first sample has nothing before it, so a backlog
// present at start is stamped at that sample's own time and reads young. With
// NumPending 0 on the sample before, every message unread now arrived after
// it, so T is not read on an empty sample.
//
// A sample stamped before the last one applied is dropped. A stream read that
// fails or times out marks the tier unverified until its next good sample: its
// batches are kept, so its age keeps growing and is never reset to 0, and the
// cue reads unverified, never stalled.
type pendingTracker struct {
	mu    sync.Mutex
	tiers map[string]*tierPending
}

type tierPending struct {
	at         time.Time // the last good sample's stamp
	unread     uint64    // its NumPending
	total      uint64    // its T, when it was read
	haveTotal  bool
	batches    []pendingBatch
	unverified string // why the last sample failed; empty when it did not
}

type pendingBatch struct {
	n    uint64
	seen time.Time
}

// pendingSample is one tier's state, read after at. failed, when set, is why
// the stream read failed, and the rest is not used.
type pendingSample struct {
	unread    uint64 // NumPending
	total     uint64 // T, valid when haveTotal
	haveTotal bool
	failed    string
	at        time.Time
}

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
	if s.failed != "" {
		t.unverified = s.failed
		return
	}
	t.unverified = ""
	prev := t.at
	if prev.IsZero() {
		prev = s.at // a first sample is stamped at its own time
	}
	push := func(n uint64) {
		if k := len(t.batches); k > 0 && t.batches[k-1].seen.Equal(prev) {
			t.batches[k-1].n += n
			return
		}
		t.batches = append(t.batches, pendingBatch{n: n, seen: prev})
	}
	if t.haveTotal && s.haveTotal && s.total > t.total {
		push(s.total - t.total)
	}
	var sum uint64
	for _, b := range t.batches {
		sum += b.n
	}
	for sum > s.unread && len(t.batches) > 0 {
		drop := min(sum-s.unread, t.batches[0].n)
		t.batches[0].n -= drop
		sum -= drop
		if t.batches[0].n == 0 {
			t.batches = t.batches[1:]
		}
	}
	if sum < s.unread {
		push(s.unread - sum)
	}
	t.at, t.unread, t.total, t.haveTotal = s.at, s.unread, s.total, s.haveTotal
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
		if len(t.batches) == 0 {
			continue
		}
		if s := t.batches[0].seen; at.IsZero() || s.Before(at) {
			at, tier = s, name
		}
	}
	if at.IsZero() || now.Before(at) {
		return 0, tier
	}
	return now.Sub(at), tier
}

// unverified is the reason, for the cue, that a tier's last sample failed;
// empty when every tier's last sample was good. Local is named first.
func (p *pendingTracker) unverified() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	reason := ""
	for _, tier := range []string{"local", "global"} {
		if t := p.tiers[tier]; t != nil && t.unverified != "" {
			reason = joinReason(reason, fmt.Sprintf("pending age unverified on the %s tier: %s", tier, t.unverified))
		}
	}
	return reason
}

// oldestPending is the age of this seat's oldest waiting message.
func (b *Bus) oldestPending() time.Duration { return b.pending.age(time.Now()) }

// pendingByTier reads each tier's waiting count from consumer state for the
// cue (NumPending plus NumAckPending, as before) and marks the pending tracker
// with each tier it read. A tier whose read fails or passes the bound is
// marked unverified, and the error is the first such tier's. A hub not yet
// attached is left out, as in remaining().
func (b *Bus) pendingByTier(ctx context.Context) (uint64, error) {
	info, err := b.readTier(ctx, "local", b.js, "AGENT_INBOX", b.cons())
	if err != nil {
		return 0, err
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
	gi, err := b.readTier(ctx, "global", g.js, g.cfg.streamName(), g.cons())
	if err != nil {
		return 0, err
	}
	return n + gi.NumPending + uint64(gi.NumAckPending), nil
}

// streamReadTimeout bounds one tier's whole read for a sample: the consumer
// Info call and the stream reads after it (operator ruling 2026-10-24, option
// 1). A denied request gets no answer and would otherwise wait out the whole
// tick; loopback answers in about 1 ms and a leaf to hub in tens of ms, so
// 500 ms leaves wide room while both tiers still fit inside the 2 s cue poll.
var streamReadTimeout = 500 * time.Millisecond

// readTier reads one tier's consumer state and marks the tracker with it,
// all under streamReadTimeout. A consumer Info that fails or passes the bound
// marks the tier unverified and is returned as the error.
func (b *Bus) readTier(ctx context.Context, tier string, js jetstream.JetStream, stream string, cons jetstream.Consumer) (*jetstream.ConsumerInfo, error) {
	at := time.Now()
	rctx, cancel := context.WithTimeout(ctx, streamReadTimeout)
	defer cancel()
	info, err := cons.Info(rctx)
	if err != nil {
		b.pending.mark(tier, pendingSample{at: at, failed: err.Error()})
		return nil, err
	}
	b.pending.mark(tier, sampleOf(rctx, js, stream, info, at))
	return info, nil
}

// sampleOf is one tier's tracker sample. T is read only when something is
// unread, as one STREAM.INFO with a subjects filter per filter subject, under
// the caller's bound. A sample with nothing unread needs no T: the next one's
// unread mail all arrived after it. A failed or timed-out read marks the
// sample failed.
func sampleOf(ctx context.Context, js jetstream.JetStream, stream string, info *jetstream.ConsumerInfo, at time.Time) pendingSample {
	s := pendingSample{unread: info.NumPending, at: at}
	if s.unread == 0 {
		return s
	}
	subjects := info.Config.FilterSubjects
	if info.Config.FilterSubject != "" {
		subjects = append([]string{info.Config.FilterSubject}, subjects...)
	}
	st, err := js.Stream(ctx, stream)
	if err != nil {
		s.failed = err.Error()
		return s
	}
	for _, subj := range subjects {
		si, err := st.Info(ctx, jetstream.WithSubjectFilter(subj))
		if err != nil {
			s.failed = err.Error()
			return s
		}
		s.total += si.State.Subjects[subj]
	}
	s.haveTotal = true
	return s
}

// samplePending is the heartbeat's sample, taken when the cue watcher is not
// sampling every poll. A failed read leaves the tracker as it was.
func (b *Bus) samplePending(ctx context.Context) { _, _ = b.pendingByTier(ctx) }
