package main

import (
	"fmt"
	"sync/atomic"
	"time"
)

// inboxMaxAgeFallback is the inbox lifetime director-mcp assumes until it has
// read the live stream, and keeps assuming when that read fails (README.md and
// docs/getting-started.md show a 168h stream as their example). The running
// value is inboxMaxAge(), which adoptInboxMaxAge sets from AGENT_INBOX's own
// max_age at startup. The send records are kept that long plus a day
// (delivery.go). staleAfter in askledger.go is a different figure for a
// different thing, the ledger's own staleness and Seen-pruning window.
const inboxMaxAgeFallback = 168 * time.Hour

// liveInboxMaxAge is the max_age read from the live stream; 0 means not read.
var liveInboxMaxAge atomic.Int64

// inboxMaxAge is how long the inbox keeps mail: the live stream's max_age once
// adopted, else inboxMaxAgeFallback.
func inboxMaxAge() time.Duration {
	if d := time.Duration(liveInboxMaxAge.Load()); d > 0 {
		return d
	}
	return inboxMaxAgeFallback
}

// askLookup is the answer to "what became of message <id>?".
type askLookup struct {
	ID      string  `json:"id"`
	Found   bool    `json:"found"`
	Verdict string  `json:"verdict"`
	Row     *askRow `json:"row,omitempty"`
	// Send is the recorded local send, when the ledger holds one for the id;
	// Delivery is what the inbox stream says became of it, read live by the
	// caller. Delivery is a read by a covering durable, never a reply.
	Send     *sendRec  `json:"send,omitempty"`
	Delivery *delivery `json:"delivery,omitempty"`
}

// FindByID looks a message id up in the ledger, by the ask's own id or any id
// in its thread. Only a REQUEST opens a row, so another kind of message is
// reported as not found, and the verdict says so.
func (l *askLedger) FindByID(id string, now time.Time) askLookup {
	out := askLookup{ID: id}
	if sr, ok := l.Sends[id]; ok {
		out.Send = &sr
	}
	ask, ok := l.threads[id]
	if !ok {
		out.Verdict = "no row for this id: the ledger opens a row only for a REQUEST it saw, and drops closed rows after 30 days"
		if out.Send != nil {
			out.Verdict = fmt.Sprintf("no ask row (only a REQUEST opens one); the ledger holds this %s send", out.Send.Perf)
		}
		return out
	}
	r := l.Rows[ask]
	out.Found, out.Row = true, r
	age := now.Sub(r.LastMsgAt)
	switch {
	case r.State == askSent:
		out.Verdict = fmt.Sprintf("sent %s ago, no acknowledgement yet", humanAge(age))
	case r.State == askCancelled:
		out.Verdict = fmt.Sprintf("cancelled by the asker at %s", r.StateAt)
	default:
		out.Verdict = fmt.Sprintf("%s at %s (the recipient's reply, not a read receipt)", r.State, r.StateAt)
	}
	return out
}
