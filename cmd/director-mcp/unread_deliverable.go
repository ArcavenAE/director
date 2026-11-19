package main

import (
	"context"
	"sync"
	"time"
)

// What the seat will be handed, published in its presence row.
//
// A seat with the global FOR filter on is handed less mail than its durable
// holds: the filter acks mail for another seat on the next drain. A reader of
// the roster has only the broker's count, and the filter is the seat's own (it
// is built from the seat's identity), so only the seat can say how much it will
// be handed. The seat writes that in its presence row, per tier, from the same
// leaveOutDropped the cue uses, and the roster prefers it (aae-orc-koaqv).
//
//	unread_deliverable_local   the local tier's waiting count, held mail included
//	unread_deliverable_global  the global tier's, with the mail the filter will drop left out
//	unread_deliverable         the two added, which is the cue's own count
//
// The fields are written only by a seat with the filter attached. An older shim
// writes none, and the roster keeps the broker's count for it, labelled raw.

// deliverableFreshness is how old the stored figure may be before a presence
// write reads the tiers again. The heartbeat samples just before it writes the
// row, so this only matters for a write with no sample before it.
const deliverableFreshness = 10 * time.Second

type deliverableCounts struct{ local, global uint64 }

type deliverableState struct {
	mu sync.Mutex
	at time.Time
	c  deliverableCounts
}

func (d *deliverableState) set(local, global uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.at, d.c = time.Now(), deliverableCounts{local: local, global: global}
}

func (d *deliverableState) fresh() (deliverableCounts, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.c, !d.at.IsZero() && time.Since(d.at) < deliverableFreshness
}

// deliverableNow is the figure for the presence row: nil when no FOR filter is
// attached, so the row carries no field, and nil when the tiers cannot be read.
func (b *Bus) deliverableNow(ctx context.Context) *deliverableCounts {
	if b.globalCfg == nil {
		return nil
	}
	b.globalMu.Lock()
	g := b.global
	b.globalMu.Unlock()
	if g == nil || g.forFilter == nil {
		return nil
	}
	if c, ok := b.deliverable.fresh(); ok {
		return &c
	}
	if _, err := b.pendingTiers(ctx); err != nil {
		return nil
	}
	c, _ := b.deliverable.fresh()
	return &c
}

// fields writes the figures into a presence record; a nil figure writes nothing.
func (c *deliverableCounts) fields(rec map[string]any) {
	if c == nil {
		return
	}
	rec["unread_deliverable_local"] = c.local
	rec["unread_deliverable_global"] = c.global
	rec["unread_deliverable"] = c.local + c.global
}

// unreadFor is the roster's unread for one durable row. raw is the broker's
// NumPending and held its NumAckPending. A row that carries the seat's figure for
// this tier gives the figure less the mail the seat holds, never above the
// broker's count; a row without one keeps the broker's count. The second value is
// where the number came from: "seat" or "broker-raw".
func unreadFor(row map[string]any, stream string, raw, held uint64) (uint64, string) {
	field := "unread_deliverable_global"
	if stream == "AGENT_INBOX" {
		field = "unread_deliverable_local"
	}
	v, ok := row[field].(float64)
	if !ok || v < 0 {
		return raw, "broker-raw"
	}
	d := uint64(v)
	if d > held {
		d -= held
	} else {
		d = 0
	}
	return min(raw, d), "seat"
}
