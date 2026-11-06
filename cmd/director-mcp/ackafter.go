package main

// aae-orc-nanyb: a message is acked only after the reply carrying it has been
// written, and a call cancelled before then gives its mail back.
//
// A wait that acked at the fetch lost the message whenever the client had
// already given up on the call: the server answered a call nobody read. So in
// the default mode a wait holds what it fetched (ackLater), and callTool acks
// after the reply is written to stdout, or Naks if the call ended first. The
// handled mode (handled.go) never acks on read and does not use this.
//
// A notifications/cancelled can also cross a reply already written and acked:
// the client sent it before it read the reply. The server keeps the answered
// call's envelopes for a window and a cancel naming that call puts them on an
// in-process returned queue, which the next wait returns first. A process that
// dies holding returned mail loses it from the bus; the log names each
// message_id so it can be recovered by hand.

import (
	"context"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

const thresholdLateCancelWindow = "late_cancel_window"

type ackLaterKey struct{}

// fetchedMsg is one fetched message awaiting its ack.
type fetchedMsg struct {
	m    jetstream.Msg
	item drained
}

// ackLater collects what one call fetched. It is attached to the call's
// context; a call without one (every caller but a wait_for_message tool call)
// acks at the fetch as before.
type ackLater struct {
	mu      sync.Mutex
	held    []fetchedMsg
	carried []drained // returned-queue mail this call's reply also carries
}

func withAckLater(ctx context.Context) (context.Context, *ackLater) {
	a := &ackLater{}
	return context.WithValue(ctx, ackLaterKey{}, a), a
}

func ackLaterFrom(ctx context.Context) *ackLater {
	a, _ := ctx.Value(ackLaterKey{}).(*ackLater)
	return a
}

func (a *ackLater) hold(m jetstream.Msg, e *Envelope, tier string, seq uint64) {
	a.mu.Lock()
	a.held = append(a.held, fetchedMsg{m: m, item: drained{Env: e, Tier: tier, Seq: seq}})
	a.mu.Unlock()
}

func (a *ackLater) carry(items []drained) {
	a.mu.Lock()
	a.carried = append(a.carried, items...)
	a.mu.Unlock()
}

// items is everything the reply carries: what was held and what came back from
// the returned queue.
func (a *ackLater) items() []drained {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := append([]drained(nil), a.carried...)
	for _, h := range a.held {
		out = append(out, h.item)
	}
	return out
}

// nak gives every held message back to the broker unread.
func (a *ackLater) nak() int {
	a.mu.Lock()
	held := a.held
	a.held = nil
	a.mu.Unlock()
	for _, h := range held {
		_ = h.m.Nak()
	}
	return len(held)
}

// ack confirms every held message after the reply was written. A failed ack
// leaves the message to redeliver, a duplicate rather than a loss; each is
// logged by message_id.
func (a *ackLater) ack(b *Bus, logf func(string, ...any)) {
	a.mu.Lock()
	held := a.held
	a.held = nil
	a.mu.Unlock()
	for _, h := range held {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := h.m.DoubleAck(ctx)
		cancel()
		if err != nil {
			logf("ack after reply failed for %s sequence %d (message_id %s): %v; it may be delivered once more", h.item.Tier, h.item.Seq, messageIDOf(h.item.Env), err)
			continue
		}
		b.noteTierAcked(h.item.Tier, h.item.Seq)
	}
}

func messageIDOf(e *Envelope) string {
	if e == nil {
		return ""
	}
	return e.MessageID
}

// returnMail puts mail whose reply nobody read back at the front of the line:
// the next wait returns it before it fetches. Each message_id is logged.
func (b *Bus) returnMail(items []drained, why string, logf func(string, ...any)) {
	if len(items) == 0 {
		return
	}
	for _, it := range items {
		logf("returned to the next wait (%s): %s sequence %d, message_id %s", why, it.Tier, it.Seq, messageIDOf(it.Env))
	}
	b.retMu.Lock()
	b.returned = append(append([]drained(nil), items...), b.returned...)
	b.retMu.Unlock()
}

// takeReturned removes up to n returned messages, oldest first.
func (b *Bus) takeReturned(n int) []drained {
	b.retMu.Lock()
	defer b.retMu.Unlock()
	if n > len(b.returned) {
		n = len(b.returned)
	}
	out := append([]drained(nil), b.returned[:n]...)
	b.returned = b.returned[n:]
	return out
}

func (b *Bus) returnedCount() int {
	b.retMu.Lock()
	defer b.retMu.Unlock()
	return len(b.returned)
}
