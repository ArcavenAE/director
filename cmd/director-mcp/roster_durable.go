package main

// Roster durable flags (aae-orc-t77rr). A presence row says a session is
// alive; it does not say the session has a durable to read its mail from. A
// seat whose durable was cleaned up, or never created, stays on the roster
// looking healthy while mail piles up in the stream with no consumer. The
// unread report walks durables and the roster walks presence rows, so a live
// seat with no durable had a row in neither. Each local roster row now names
// what its durable says, and flags what could not be established.
//
// Diagnostic, not a gate: a flag informs the reader, nothing waits on it.

import (
	"context"
	"errors"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"
)

// The flag words a roster row can carry in "flags".
const (
	// flagNoDurable: the broker answered that the seat's durable does
	// not exist, so mail sent to it has nobody to read it.
	flagNoDurable = "no-durable"
	// flagDurableUnchecked: the durable could not be looked up (the lookup
	// failed or timed out, or the row names no instance), so its state is
	// unknown, not missing.
	flagDurableUnchecked = "durable-unchecked"
	// flagPendingAgeUnreported: the row carries no oldest_pending_s, as from a
	// shim older than the field, so how long mail has waited is not known.
	flagPendingAgeUnreported = "pending-age-unreported"
)

// inboxDurableName is the name of a local session's durable on AGENT_INBOX.
func inboxDurableName(agentID, instance string) string { return "mcp_" + agentID + "_" + instance }

// flagLocalRows adds "durable" (present, missing or unknown), "unread" (the
// durable's own count, when present) and "flags" to each local roster row. A
// lookup is bounded by streamReadTimeout, so a hub that does not answer costs
// one bound per row and marks the row unknown.
func (b *Bus) flagLocalRows(ctx context.Context, rows []map[string]any) {
	for _, r := range rows {
		var flags []string
		if _, ok := r["oldest_pending_s"]; !ok {
			flags = append(flags, flagPendingAgeUnreported)
		}
		agent, _ := r["agent_id"].(string)
		inst, _ := r["instance"].(string)
		if agent == "" || inst == "" {
			r["durable"] = "unknown"
			r["durable_note"] = "the presence row names no agent id or instance, so its durable cannot be looked up"
			flags = append(flags, flagDurableUnchecked)
		} else {
			b.checkLocalDurable(ctx, r, inboxDurableName(agent, inst), &flags)
		}
		if len(flags) > 0 {
			r["flags"] = flags
		}
	}
}

func (b *Bus) checkLocalDurable(ctx context.Context, row map[string]any, durable string, flags *[]string) {
	rctx, cancel := context.WithTimeout(ctx, streamReadTimeout)
	defer cancel()
	cons, err := b.js.Consumer(rctx, "AGENT_INBOX", durable)
	switch {
	case errors.Is(err, jetstream.ErrConsumerNotFound):
		row["durable"] = "missing"
		row["durable_note"] = fmt.Sprintf("AGENT_INBOX has no durable %s for this live session", durable)
		*flags = append(*flags, flagNoDurable)
	case err != nil:
		row["durable"] = "unknown"
		row["durable_note"] = fmt.Sprintf("looking up durable %s failed: %v", durable, err)
		*flags = append(*flags, flagDurableUnchecked)
	default:
		row["durable"] = "present"
		if info := cons.CachedInfo(); info != nil {
			row["unread"] = info.NumPending + uint64(info.NumAckPending)
		}
	}
}
