package main

import (
	"fmt"
	"time"
)

// inboxMaxAge is how long the inbox keeps mail, as the setup instructions
// declare it (docs/getting-started.md; TestLocalInactiveOutlastsInboxMaxAge
// pins that value). A REQUEST unanswered for longer than this may have
// expired unread. staleAfter in askledger.go is a 72h figure that predates the
// 168h declaration and also bounds the ledger's own retention, so it is left
// alone here.
const inboxMaxAge = 168 * time.Hour

// askLookup is the answer to "what became of message <id>?".
type askLookup struct {
	ID      string  `json:"id"`
	Found   bool    `json:"found"`
	Verdict string  `json:"verdict"`
	Row     *askRow `json:"row,omitempty"`
}

// FindByID looks a message id up in the ledger, by the ask's own id or any id
// in its thread. Only a REQUEST opens a row, so another kind of message is
// reported as not found, and the verdict says so.
func (l *askLedger) FindByID(id string, now time.Time) askLookup {
	out := askLookup{ID: id}
	ask, ok := l.threads[id]
	if !ok {
		out.Verdict = "no row for this id: the ledger opens a row only for a REQUEST it saw, and drops closed rows after 30 days"
		return out
	}
	r := l.Rows[ask]
	out.Found, out.Row = true, r
	age := now.Sub(r.LastMsgAt)
	switch {
	case r.State == askSent && age > inboxMaxAge:
		out.Verdict = fmt.Sprintf("sent and not acknowledged for %s, longer than the inbox keeps mail (%s): it may have expired unread; resend it", humanAge(age), humanAge(inboxMaxAge))
	case r.State == askSent:
		out.Verdict = fmt.Sprintf("sent %s ago, no acknowledgement yet", humanAge(age))
	case r.State == askCancelled:
		out.Verdict = fmt.Sprintf("cancelled by the asker at %s", r.StateAt)
	default:
		out.Verdict = fmt.Sprintf("%s at %s (the recipient's reply, not a read receipt)", r.State, r.StateAt)
	}
	return out
}
