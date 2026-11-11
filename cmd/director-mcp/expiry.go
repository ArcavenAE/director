package main

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// The inbox stream drops mail past its max age whether or not the seat has
// read it, and a poll that finds nothing says "silence, not failure"
// (aae-orc-t77rr). expiryWatch lets that poll tell "no mail" from "the stream
// aged past what this seat last read".
//
// It compares the durable's read position (the stream sequence it last
// delivered) with the stream's first sequence. Mail for this seat inside the
// gap is gone and cannot be counted, and the gap also holds other seats' mail,
// so the signal says "may have" and never gives a count. It is reported once
// per gap: a later, wider gap reports again.
type expiryWatch struct {
	mu       sync.Mutex
	reported uint64 // the first sequence the last report was made against
}

// expiredUnread returns the note for a local poll that found nothing, or ""
// when the stream has not aged past the seat's read position, the gap was
// already reported, or either read failed (a failed read says nothing).
func (b *Bus) expiredUnread(ctx context.Context) string {
	cons := b.cons()
	if cons == nil {
		return ""
	}
	ci, err := cons.Info(ctx)
	if err != nil {
		return ""
	}
	st, err := b.js.Stream(ctx, "AGENT_INBOX")
	if err != nil {
		return ""
	}
	si, err := st.Info(ctx)
	if err != nil {
		return ""
	}
	return b.expiry.note("AGENT_INBOX", si.State.FirstSeq, ci.Delivered.Stream, si.Config.MaxAge)
}

// note is the shared comparison: the stream's first sequence against the
// durable's last delivered one, reported once per gap. The caller's own
// expiryWatch holds the once-per-gap state, so each tier keeps its own.
func (w *expiryWatch) note(stream string, first, read uint64, maxAge time.Duration) string {
	if first <= read+1 {
		return ""
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if first <= w.reported {
		return ""
	}
	w.reported = first
	return fmt.Sprintf("mail addressed to this seat may have expired unread: stream sequences %d to %d are gone from %s (its max age is %s), past this seat's last read at sequence %d. The gap also holds other seats' mail, so this is a window and not a count; a sender whose ask went unanswered should resend",
		read+1, first-1, stream, maxAge, read)
}

// expiredUnread is the global tier's check, the same comparison against the
// seat's hub durable and the hub stream it reads. It uses only the seat's own
// consumer info and the stream's info: a cluster credential cannot list
// consumers (finding-006), and a failed read says nothing.
func (g *globalTier) expiredUnread(ctx context.Context) string {
	cons := g.cons()
	if cons == nil {
		return ""
	}
	ci, err := cons.Info(ctx)
	if err != nil {
		return ""
	}
	name := g.cfg.streamName()
	st, err := g.js.Stream(ctx, name)
	if err != nil {
		return ""
	}
	si, err := st.Info(ctx)
	if err != nil {
		return ""
	}
	return g.expiry.note(name, si.State.FirstSeq, ci.Delivered.Stream, si.Config.MaxAge)
}

// globalExpiredUnread is the note for a poll that found nothing on the global
// tier. It is "" when the tier is off or the attach fails; with the tier on it
// attaches on first use through globalReady, the same attach the poll makes.
func (b *Bus) globalExpiredUnread(ctx context.Context) string {
	if b.globalCfg == nil {
		return ""
	}
	g, err := b.globalReady(ctx)
	if err != nil {
		return ""
	}
	return g.expiredUnread(ctx)
}
