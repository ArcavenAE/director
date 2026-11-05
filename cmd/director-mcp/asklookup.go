package main

import (
	"fmt"
	"time"
)

// inboxMaxAge is how long the inbox keeps mail, as the setup instructions
// declare it (docs/getting-started.md; TestLocalInactiveOutlastsInboxMaxAge
// pins that value). The send records are kept this long plus a day
// (delivery.go). staleAfter in askledger.go is a different figure for a
// different thing, the ledger's own staleness and Seen-pruning window.
const inboxMaxAge = 168 * time.Hour

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
