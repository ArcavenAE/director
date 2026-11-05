package main

import (
	"context"
	"fmt"
	"sync"
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
	first, read := si.State.FirstSeq, ci.Delivered.Stream
	if first <= read+1 {
		return ""
	}
	b.expiry.mu.Lock()
	defer b.expiry.mu.Unlock()
	if first <= b.expiry.reported {
		return ""
	}
	b.expiry.reported = first
	return fmt.Sprintf("mail addressed to this seat may have expired unread: stream sequences %d to %d are gone from AGENT_INBOX (its max age is %s), past this seat's last read at sequence %d. The gap also holds other seats' mail, so this is a window and not a count; a sender whose ask went unanswered should resend",
		read+1, first-1, si.Config.MaxAge, read)
}

// expiredUnread is the global tier's check; see expiryWatch.
func (g *globalTier) expiredUnread(ctx context.Context) string { return "" }
