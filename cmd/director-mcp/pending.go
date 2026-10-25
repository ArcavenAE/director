package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// pendingTracker keeps how long this seat's oldest unacked message has waited,
// on the shim's own clock, from consumer and stream state sampled over time
// (director#297). No broker timestamp is read, so there is no clock skew
// between hosts.
//
// The count is NumPending plus NumAckPending, the same count the cue uses, as
// the marvel watcher design proposes (section 11.5, in the marvel#1056 draft,
// not yet merged): every message not yet acked.
// A message read and held ack-pending (handled mode) keeps aging, and so does
// one Nak'd after a cancel while it waits to be delivered again; only the ack
// takes it off. The clock stays this sample-inferred age. The reference for
// it is the watcher's age, which reads the broker's own clock; the two can
// differ by the sample interval and by the start-of-sampling stamp below.
//
// Each sample carries the tier's count and, while anything is unacked, T:
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
// were known not to be there. Each batch also keeps the stream's last
// sequence at the sample that counted it, an upper bound on its messages. The
// inbox streams are shared, so that bound is normally above this seat's own
// mail; on a sample with nothing undelivered it drops to Delivered.Stream.
// While some mail is undelivered the bound stays where it was, and an acked
// batch then pops once the ack floor passes it, which on nats-server happens
// at the ack, since the broker moves the floor past other seats' sequences.
// Then the batches reconcile to the count. With nothing held ack-pending,
// every counted message is undelivered, and those leave in stream order, so
// the front is popped while the total is above the count. With mail held, a
// fall in the count can be an ack out of stream order (mark_handled acks the
// message the seat names, and a Nak'd message stays while later ones are
// acked), so only batches the consumer's AckFloor.Stream has passed are
// popped. Either way any shortfall is pushed at the previous sample. A
// message that leaves while another arrives inside one sample therefore
// cannot pass its age on, and a redelivery moves neither T nor the count. A
// message aging out by the stream's limits while another arrives makes T
// undercount arrivals; the shortfall push stamps the newcomer at the previous
// sample, so the age does not read low. A drop with no delivery (an age-out or
// a delete) keeps older batches, so the age reads high.
//
// From the second sample on the age does not read low: an ack out of order
// pops nothing the ack floor has not passed. It reads high by at most one
// sample (2 s while the cue watcher samples, 30 s on the heartbeat when it
// does not) while nothing is held. While mail is held it can read high for
// longer: a batch stays whole until the floor passes all of it, so a message
// acked first can leave a later one in its batch with the batch's older
// stamp, and an age-out of undelivered mail pops nothing. A tier's first
// sample has nothing before it, so a backlog present at start is stamped at
// that sample's own time and reads young. With a count of 0 on the sample
// before, every message unacked now arrived after it, so T is not read on an
// empty sample.
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
	unread     uint64    // its NumPending plus NumAckPending
	ackPending uint64    // its NumAckPending, so the reason can say unacked
	total      uint64    // its T, when it was read
	haveTotal  bool
	batches    []pendingBatch
	unverified string // why the last sample failed; empty when it did not
}

type pendingBatch struct {
	n    uint64
	seen time.Time
	// hi is the stream's last sequence at the sample that counted the batch,
	// so every message in it is at or below hi; 0 when it was not read.
	hi uint64
}

// pendingSample is one tier's state, read after at. failed, when set, is why
// the stream read failed, and the rest is not used.
type pendingSample struct {
	unread     uint64 // NumPending plus NumAckPending
	ackPending uint64 // NumAckPending
	total      uint64 // T, valid when haveTotal
	haveTotal  bool
	lastSeq    uint64 // the stream's last sequence, read with T; 0 when not read
	ackFloor   uint64 // the consumer's AckFloor.Stream
	delivered  uint64 // the consumer's Delivered.Stream
	failed     string
	at         time.Time
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
	// hi is the stream-wide last sequence, and the inbox streams are shared, so
	// it is normally above this seat's own mail. With nothing undelivered every
	// counted message is at or below Delivered.Stream, so hi drops to it. The
	// broker also moves the ack floor past other seats' sequences, so this
	// guards the pop rather than being the only thing that makes it work.
	allDelivered := s.unread > 0 && s.unread == s.ackPending && s.delivered > 0
	hi := s.lastSeq
	if allDelivered && (hi == 0 || hi > s.delivered) {
		hi = s.delivered
	}
	push := func(n uint64) {
		if k := len(t.batches); k > 0 && t.batches[k-1].seen.Equal(prev) {
			t.batches[k-1].n += n
			t.batches[k-1].hi = max(t.batches[k-1].hi, hi)
			return
		}
		t.batches = append(t.batches, pendingBatch{n: n, seen: prev, hi: hi})
	}
	if t.haveTotal && s.haveTotal && s.total > t.total {
		push(s.total - t.total)
	}
	var sum uint64
	for _, b := range t.batches {
		sum += b.n
	}
	// With mail held ack-pending, a fall in the count can be an ack out of
	// stream order, which says nothing about the front. Only batches the ack
	// floor has passed are gone then, and the rest stay, so the age reads high
	// rather than low. With nothing held, every counted message is undelivered,
	// and those leave in stream order, so the front is popped by count.
	if allDelivered {
		for i := range t.batches {
			if t.batches[i].hi == 0 || t.batches[i].hi > s.delivered {
				t.batches[i].hi = s.delivered
			}
		}
	}
	for s.ackPending > 0 && len(t.batches) > 0 && t.batches[0].hi > 0 && t.batches[0].hi <= s.ackFloor {
		sum -= t.batches[0].n
		t.batches = t.batches[1:]
	}
	for s.ackPending == 0 && sum > s.unread && len(t.batches) > 0 {
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
	t.at, t.unread, t.ackPending, t.total, t.haveTotal = s.at, s.unread, s.ackPending, s.total, s.haveTotal
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
	return p.oldestLocked(now)
}

// oldestUnacked reports whether the oldest waiting message is ack-pending:
// its tier's last good sample held ack-pending mail, which a consumer
// delivered before anything still undelivered.
func (p *pendingTracker) oldestUnacked(now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, tier := p.oldestLocked(now)
	t := p.tiers[tier]
	return t != nil && t.ackPending > 0
}

func (p *pendingTracker) oldestLocked(now time.Time) (time.Duration, string) {
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
// unacked, as one STREAM.INFO with a subjects filter per filter subject, under
// the caller's bound. A sample with nothing unacked needs no T: the next one's
// unacked mail all arrived after it. A failed or timed-out read marks the
// sample failed.
func sampleOf(ctx context.Context, js jetstream.JetStream, stream string, info *jetstream.ConsumerInfo, at time.Time) pendingSample {
	s := pendingSample{unread: info.NumPending + uint64(info.NumAckPending), ackPending: uint64(info.NumAckPending), ackFloor: info.AckFloor.Stream, delivered: info.Delivered.Stream, at: at}
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
		s.lastSeq = max(s.lastSeq, si.State.LastSeq)
	}
	s.haveTotal = true
	return s
}

// samplePending is the heartbeat's sample, taken when the cue watcher is not
// sampling every poll. A failed read leaves the tracker as it was.
func (b *Bus) samplePending(ctx context.Context) { _, _ = b.pendingByTier(ctx) }
