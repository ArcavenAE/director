package main

import (
	"context"
	"sync"
	"time"
)

// The cue wakes a seat for mail it will read. With the FOR filter on, a global
// message the filter will drop is not mail the seat is handed, so the cue's
// count leaves it out (director#572). The count is read from consumer state and
// carries no message bodies, so the cue looks at what waits the way
// peekWaiting does for inbox_summary: an ephemeral AckNone consumer that
// starts above the durable's ack floor and is deleted afterwards. The durable's
// acks, redelivery and counts do not move, and the leaf class's grants already
// allow the sequence (cuefilter_peek_grant_test.go).
//
// The decision is the filter's own, globalTier.foreign, the one dropForeign
// acts on, so the cue cannot count a message the filter would deliver as
// dropped. Every doubt counts the message: a peek that fails, a message that
// does not decode, a roster read that fails, an ambiguous FOR line.
//
// Cost on the 2 s poll: nothing is read with the filter off, or while the
// global tier has nothing unacked. While it does, one peek is made, and its
// result is reused while the durable's position (waiting count, ack floor and
// delivered sequence) is unchanged, for at most cueFilterReuse.

// cueFilterReuse bounds how long one peek's result is reused for an unchanged
// durable position. The roster the filter reads can change with no change to
// the durable, so the reuse ends on its own.
const cueFilterReuse = 30 * time.Second

type cueFilterKey struct{ waiting, ackFloor, delivered uint64 }

// cueFilterCache is the last peek's result for a durable position.
type cueFilterCache struct {
	mu    sync.Mutex
	ok    bool
	key   cueFilterKey
	at    time.Time
	drops uint64
}

func (c *cueFilterCache) get(k cueFilterKey, now time.Time) (uint64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ok && c.key == k && now.Sub(c.at) < cueFilterReuse {
		return c.drops, true
	}
	return 0, false
}

func (c *cueFilterCache) put(k cueFilterKey, now time.Time, drops uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ok, c.key, c.at, c.drops = true, k, now, drops
}

// leaveOutDropped is the global tier's count with the messages the filter will
// drop taken out. It never returns more than the count it was given.
func leaveOutDropped(ctx context.Context, c tierCounts) uint64 {
	g := c.g
	if g == nil || g.forFilter == nil || c.global == 0 || c.ginfo == nil {
		return c.global
	}
	key := cueFilterKey{waiting: c.global, ackFloor: c.ginfo.AckFloor.Stream, delivered: c.ginfo.Delivered.Stream}
	now := time.Now()
	drops, ok := g.cueDrop.get(key, now)
	if !ok {
		pctx, cancel := context.WithTimeout(ctx, streamReadTimeout)
		defer cancel()
		items, _, maybeConsumed, err := peekWaiting(pctx, g.js, g.cons(), "global", summaryDefault)
		if err != nil {
			return c.global // a doubt: count it all, and read again next poll
		}
		for _, it := range items {
			if it.Env == nil {
				continue // does not decode: delivered, so counted
			}
			if drop, _ := g.foreign(pctx, it.Env); drop {
				drops++
			}
		}
		// A message the filter dropped earlier is acked, but it can still sit
		// above the ack floor and be listed. maybeConsumed is how many listed
		// messages the durable no longer counts; take the worst case, that they
		// are the droppable ones, so this never counts too few.
		if drops > uint64(maybeConsumed) {
			drops -= uint64(maybeConsumed)
		} else {
			drops = 0
		}
		g.cueDrop.put(key, now, drops)
	}
	if drops >= c.global {
		return 0
	}
	return c.global - drops
}
