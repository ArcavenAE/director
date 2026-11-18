package main

import (
	"context"
	"sync"
	"time"
)

// A catching-up presence (director#222 and #220, asks-die-unread D2). At connect
// the shim writes presence state `catching-up`, not `idle`, and keeps writing it
// on its heartbeat until both hold:
//
//  1. every source (local, inherited, global) reports zero waiting, and an armed
//     inheritance has started and ended or drained, and
//  2. the seat has called wait_for_message at least once since connect.
//
// Then it writes the state the seat asked for (idle if it asked for none) and
// accepts the seat's own set_presence from then on. Until then a set_presence is
// recorded but presence stays `catching-up`, and the tool result says why and how
// many are waiting. Sends still resolve: the state is free text and nothing
// compares it, so a catching-up row counts as live as any other.
const stateCatchingUp = "catching-up"

type catchupState struct {
	mu        sync.Mutex
	done      bool
	waited    bool
	polledAt  time.Time // the first wait_for_message since connect; zero before it
	requested string    // what the seat asked for while catching up
}

// catchingUp reports whether the session is still catching up.
func (b *Bus) catchingUp() bool {
	b.catchup.mu.Lock()
	defer b.catchup.mu.Unlock()
	return !b.catchup.done
}

// noteWaited records a wait_for_message call.
func (b *Bus) noteWaited() {
	b.catchup.mu.Lock()
	b.catchup.waited = true
	if b.catchup.polledAt.IsZero() {
		b.catchup.polledAt = time.Now()
	}
	b.catchup.mu.Unlock()
}

// polledAtText is when the seat first called wait_for_message, for the presence
// row, and "" before it. A seat whose predecessor is still live stays
// catching-up with nothing waiting while it polls, so the row carries this fact
// for a sweep to tell it from a seat that never polled.
func (b *Bus) polledAtText() string {
	b.catchup.mu.Lock()
	defer b.catchup.mu.Unlock()
	if b.catchup.polledAt.IsZero() {
		return ""
	}
	return b.catchup.polledAt.UTC().Format(time.RFC3339)
}

// holdRequestedState records the state a seat asked for while it is catching up
// and reports whether it was held back. A request for catching-up itself is not
// a request.
func (b *Bus) holdRequestedState(state string) bool {
	b.catchup.mu.Lock()
	defer b.catchup.mu.Unlock()
	if b.catchup.done || state == stateCatchingUp {
		return false
	}
	b.catchup.requested = state
	return true
}

// waitingNow is the count waiting across every source this session reads, read
// under streamReadTimeout. ok is
// false when a source could not be read, so a failed read never reads as zero.
func (b *Bus) waitingNow(ctx context.Context) (n uint64, ok bool) {
	// Bounded like the tracker's reads, so a consumer whose Info never answers
	// cannot stall the heartbeat's check, the held set_presence or a presence write.
	ctx, cancel := context.WithTimeout(ctx, streamReadTimeout)
	defer cancel()
	rem, failed := b.remainingRead(ctx)
	if len(failed) > 0 {
		return 0, false
	}
	for _, v := range rem {
		n += v
	}
	return n, true
}

// inheritanceSettled is true with no lineage, or once an armed inheritance has
// started and then drained or ended.
func (b *Bus) inheritanceSettled() bool {
	b.inh.mu.Lock()
	pred, started, ended := b.inh.pred, b.inh.cons != nil, b.inh.ended != ""
	b.inh.mu.Unlock()
	if pred == "" || ended {
		return true
	}
	return started
}

// evalCatchup ends the catch-up when its conditions hold and writes the state
// the seat asked for. It is called on the heartbeat and when a wait returns.
func (b *Bus) evalCatchup(ctx context.Context) {
	b.catchup.mu.Lock()
	if b.catchup.done || !b.catchup.waited {
		b.catchup.mu.Unlock()
		return
	}
	b.catchup.mu.Unlock()
	if !b.inheritanceSettled() {
		return
	}
	if n, ok := b.waitingNow(ctx); !ok || n != 0 {
		return
	}
	b.catchup.mu.Lock()
	if b.catchup.done {
		b.catchup.mu.Unlock()
		return
	}
	b.catchup.done = true
	state := b.catchup.requested
	b.catchup.mu.Unlock()
	if state == "" {
		state = "idle"
	}
	_, _ = b.setPresence(ctx, state)
}

// connectedAtText is when this session connected, for the presence row.
func (b *Bus) connectedAtText() string { return b.connectedAt.UTC().Format(time.RFC3339) }
