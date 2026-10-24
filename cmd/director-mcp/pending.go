package main

import (
	"context"
	"sync"
	"time"
)

// pendingTracker keeps how long this seat's oldest waiting message has waited,
// on the shim's own clock, from pending counts sampled over time.
type pendingTracker struct {
	mu sync.Mutex
}

// mark applies one count sample for a tier, stamped before the read.
func (p *pendingTracker) mark(tier string, n uint64, at time.Time) {}

// age is how long the oldest waiting message has waited at now; 0 when none.
func (p *pendingTracker) age(now time.Time) time.Duration { return 0 }

// oldestPending is the age of this seat's oldest waiting message.
func (b *Bus) oldestPending() time.Duration { return b.pending.age(time.Now()) }

// samplePending reads each tier's pending count and marks it.
func (b *Bus) samplePending(ctx context.Context) {}
