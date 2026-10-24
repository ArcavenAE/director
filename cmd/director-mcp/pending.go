package main

import (
	"context"
	"sync"
	"time"
)

// pendingTracker keeps how long this seat's oldest waiting message has waited,
// on the shim's own clock, from pending counts sampled over time (director#297).
// No broker timestamp is read, so there is no clock skew between hosts.
//
// Each tier keeps its own FIFO of (how many, first seen). A wait drains local
// before global, so the two tiers do not leave in arrival order, but within one
// consumer JetStream delivers in order, so each tier pops from its front. The
// oldest age is the older of the two fronts.
//
// The approximation: a rise in the count is stamped at the PREVIOUS sample's
// time, the latest moment the new messages were known not to be there yet, so
// the age is never below the true age. It can read high by up to one sample
// interval: 2 s while the cue watcher samples, 30 s on the heartbeat when it
// does not. The first sample of a tier has no previous one and is stamped at
// its own time, so a backlog that was already waiting at start reads young. A
// sample older than the last one applied (a slow read landing after a newer
// one) is dropped.
type pendingTracker struct {
	mu    sync.Mutex
	tiers map[string]*tierPending
}

type tierPending struct {
	n       uint64    // the count at the last applied sample
	at      time.Time // that sample's stamp
	batches []pendingBatch
}

type pendingBatch struct {
	n    uint64
	seen time.Time
}

// mark applies one count sample for a tier, stamped before the read.
func (p *pendingTracker) mark(tier string, n uint64, at time.Time) {
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
	if !t.at.IsZero() && at.Before(t.at) {
		return
	}
	switch {
	case n > t.n:
		seen := t.at
		if seen.IsZero() {
			seen = at
		}
		t.batches = append(t.batches, pendingBatch{n: n - t.n, seen: seen})
	case n < t.n:
		drop := t.n - n
		for drop > 0 && len(t.batches) > 0 {
			if t.batches[0].n <= drop {
				drop -= t.batches[0].n
				t.batches = t.batches[1:]
				continue
			}
			t.batches[0].n -= drop
			drop = 0
		}
	}
	t.n, t.at = n, at
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

// oldestPending is the age of this seat's oldest waiting message.
func (b *Bus) oldestPending() time.Duration { return b.pending.age(time.Now()) }

// pendingByTier reads each tier's waiting count from consumer state, marking
// the tracker with every tier it could read. The error is the first tier that
// could not be read. A hub not yet attached is left out, as in remaining().
func (b *Bus) pendingByTier(ctx context.Context) (uint64, error) {
	at := time.Now()
	info, err := b.cons().Info(ctx)
	if err != nil {
		return 0, err
	}
	local := info.NumPending + uint64(info.NumAckPending)
	b.pending.mark("local", local, at)
	if b.globalCfg == nil {
		return local, nil
	}
	b.globalMu.Lock()
	g := b.global
	b.globalMu.Unlock()
	if g == nil {
		return local, nil
	}
	at = time.Now()
	gi, err := g.cons().Info(ctx)
	if err != nil {
		return 0, err
	}
	global := gi.NumPending + uint64(gi.NumAckPending)
	b.pending.mark("global", global, at)
	return local + global, nil
}

// samplePending is the heartbeat's sample, taken when the cue watcher is not
// sampling every poll. A failed read leaves the tracker as it was.
func (b *Bus) samplePending(ctx context.Context) { _, _ = b.pendingByTier(ctx) }
