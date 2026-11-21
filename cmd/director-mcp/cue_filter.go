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
// does not decode, a roster read that fails, an ambiguous FOR line, and a peek
// that returns its whole limit, since that peek may have stopped short of what
// waits and cannot say which listed messages are still unacked.
//
// Cost on the 2 s poll: nothing is read with the filter off, or while the
// global tier has nothing unacked. While it does, one peek is made, and its
// result is reused while the durable's position (waiting count, ack floor and
// delivered sequence) is unchanged, for at most cueFilterReuse. An uncached
// peek also reads the roster once for each listed message with a well-formed FOR
// line that names no seat here, up to cueFilterPeekMax (1000) reads. A message with
// no FOR line, an ambiguous one, or one that names this seat, reads no roster.

// cueFilterPeekMax is how many waiting messages the cue's peek reads. It is above
// any inbox a seat holds in practice, so a dropped-only inbox of a few hundred
// is counted in full; past it the count falls back to the durable's own number.
// A var so a test can set the bound small.
var cueFilterPeekMax = 1000

// cueFilterReuse bounds how long one peek's result is reused for an unchanged
// durable position. The roster the filter reads can change with no change to
// the durable, so the reuse ends on its own.
const cueFilterReuse = 30 * time.Second

// cueFilterNow is the clock the peek's reuse reads. Production keeps time.Now;
// a test sets it to its own clock so it can move past a poll without waiting.
// It is a package variable, so a test that swaps it must not run in parallel.
var cueFilterNow = time.Now

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
	now := cueFilterNow()
	drops, ok := g.cueDrop.get(key, now)
	if !ok {
		pctx, cancel := context.WithTimeout(ctx, streamReadTimeout)
		defer cancel()
		items, _, maybeConsumed, err := peekWaiting(pctx, g.js, g.cons(), "global", cueFilterPeekMax)
		if err != nil {
			return c.global // a doubt: count it all, and read again next poll
		}
		if len(items) >= cueFilterPeekMax {
			// The peek returned its whole limit (cueFilterPeekMax, well above any
			// inbox a seat holds), so it may have stopped before the end of what
			// waits. That is a doubt, and a doubt cues: count the durable's own
			// number and cache it as no drops.
			g.cueDrop.put(key, now, 0)
			return c.global
		}
		// A message above the consumer's delivered sequence has not been handed out,
		// so it is certainly still waiting: a drop there counts for sure. Only a
		// message at or below it may have been acked above the floor, and
		// maybeConsumed, the listed messages the durable no longer counts, can only
		// be among those. Take the worst case over that uncertain set alone, so
		// the count is never too low and a dropped message that is plainly still
		// waiting is no longer cancelled by an unrelated acked one.
		var certain, uncertain uint64
		doubted := false // a message was delivered only because the roster could not place it
		for _, it := range items {
			if it.Env == nil {
				continue // does not decode: delivered, so counted
			}
			drop, _, doubt := g.foreignDoubt(pctx, it.Env)
			if doubt {
				doubted = true
			}
			if drop {
				if it.Seq > c.ginfo.Delivered.Stream {
					certain++
				} else {
					uncertain++
				}
			}
		}
		if uncertain > uint64(maybeConsumed) {
			uncertain -= uint64(maybeConsumed)
		} else {
			uncertain = 0
		}
		drops = certain + uncertain
		if !doubted {
			// A count that met a doubt is used for this poll only: the roster may
			// place the message by the next one, and the drain then drops it, so
			// keeping the count would hold the cue up for mail it will not hand
			// over (director#731).
			g.cueDrop.put(key, now, drops)
		}
	}
	if drops >= c.global {
		return 0
	}
	return c.global - drops
}

// leaveOutForeign returns the listed global messages without the ones the filter
// will drop, and how many it left out. Every list of the global tier's waiting
// mail that a seat reads (inbox_summary, the class-aware cue) goes through it,
// so none shows or wakes for mail the next drain drops. With the filter off, or
// for a message that does not decode, nothing is left out. The decision is
// g.foreign, the one dropForeign and the cue's count act on.
func (g *globalTier) leaveOutForeign(ctx context.Context, items []summaryItem) ([]summaryItem, int) {
	if g == nil || g.forFilter == nil {
		return items, 0
	}
	kept := make([]summaryItem, 0, len(items))
	for _, it := range items {
		if it.Env != nil {
			if drop, _ := g.foreign(ctx, it.Env); drop {
				continue
			}
		}
		kept = append(kept, it)
	}
	return kept, len(items) - len(kept)
}
