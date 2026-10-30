package main

// Roster durable flags (aae-orc-t77rr). A presence row says a session is
// alive; it does not say the session has a durable to read its mail from. A
// seat whose durable was cleaned up, or never created, stays on the roster
// looking healthy while mail piles up in the stream with no consumer. The
// unread report walks durables and the roster walks presence rows, so a live
// seat with no durable had a row in neither. Each roster row now names
// what its durable says, and flags what could not be established.
//
// Diagnostic, not a gate: a flag informs the reader, nothing waits on it.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

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

// rosterLookupDeadline bounds the whole durable check of one roster call, and
// rosterLookupWorkers is how many rows are looked up at once under it. Rows are
// looked up concurrently so a stalled broker holds list_roster for at most the
// deadline and not for the rows times streamReadTimeout. A row whose lookup is
// not answered by the deadline, or not started before it, reads as unknown. A
// variable so tests can shorten the deadline.
var (
	rosterLookupDeadline = 2 * time.Second
	rosterLookupWorkers  = 8
)

// flagRows runs check on every row under one deadline, rosterLookupWorkers at a
// time. check returns the row's flags and writes durable, durable_note and
// unread on the row it is given; each call touches only its own row. A row the
// deadline reached before its check started is marked unknown here.
func flagRows(ctx context.Context, rows []map[string]any, check func(ctx context.Context, row map[string]any) []string) {
	dctx, cancel := context.WithTimeout(ctx, rosterLookupDeadline)
	defer cancel()
	flags := make([][]string, len(rows))
	sem := make(chan struct{}, rosterLookupWorkers)
	var wg sync.WaitGroup
	for i, r := range rows {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-dctx.Done():
				r["durable"] = "unknown"
				r["durable_note"] = "the roster call reached its lookup deadline before this row's durable was looked up"
				flags[i] = append(flagsOfAge(r), flagDurableUnchecked)
				return
			}
			flags[i] = check(dctx, r)
		}()
	}
	wg.Wait()
	for i, r := range rows {
		if len(flags[i]) > 0 {
			r["flags"] = flags[i]
		}
	}
}

// flagsOfAge is the age flag alone, for a row that was not looked up.
func flagsOfAge(row map[string]any) []string {
	if _, ok := row["oldest_pending_s"]; !ok {
		return []string{flagPendingAgeUnreported}
	}
	return nil
}

// flagLocalRows adds "durable" (present, missing or unknown), "unread" (the
// durable's own count, when present) and "flags" to each local roster row. The
// lookups run under one deadline (flagRows), so a hub that does not answer
// marks the rows unknown instead of holding the roster.
func (b *Bus) flagLocalRows(ctx context.Context, rows []map[string]any) {
	flagRows(ctx, rows, func(ctx context.Context, r map[string]any) []string {
		flags := flagsOfAge(r)
		agent, _ := r["agent_id"].(string)
		inst, _ := r["instance"].(string)
		if agent == "" || inst == "" {
			r["durable"] = "unknown"
			r["durable_note"] = "the presence row names no agent id or instance, so its durable cannot be looked up"
			return append(flags, flagDurableUnchecked)
		}
		b.checkLocalDurable(ctx, r, inboxDurableName(agent, inst), &flags)
		return flags
	})
}

func (b *Bus) checkLocalDurable(ctx context.Context, row map[string]any, durable string, flags *[]string) {
	checkDurable(ctx, b.js, "AGENT_INBOX", durable, row, flags)
}

// flagGlobalRows is flagLocalRows for the rows of GLOBAL_PRESENCE. A seat's
// global durable sits on its cluster's hub stream (GLOBAL_TO_<cluster>), or on
// GLOBAL_TO_DIRECTOR for a director, under the name globalDurable gives it. A
// cluster credential may not be allowed to read another cluster's consumers,
// and a leaf link that does not answer looks the same, so a lookup that fails
// for any reason but "no such durable" marks the row unknown, never missing.
func (g *globalTier) flagGlobalRows(ctx context.Context, rows []map[string]any) {
	flagRows(ctx, rows, func(ctx context.Context, r map[string]any) []string {
		flags := flagsOfAge(r)
		agent, _ := r["agent_id"].(string)
		inst, _ := r["instance"].(string)
		role, _ := r["role"].(string)
		cluster, _ := r["cluster"].(string)
		stream := ""
		switch {
		case role == roleDirector:
			stream = globalDirectorStream
		case cluster != "":
			stream = globalStreamPrefix + cluster
		}
		if agent == "" || inst == "" || stream == "" {
			r["durable"] = "unknown"
			r["durable_note"] = "the presence row names no agent id, instance or cluster, so its durable cannot be looked up"
			return append(flags, flagDurableUnchecked)
		}
		checkDurable(ctx, g.js, stream, globalDurable(agent, inst), r, &flags)
		return flags
	})
}

// checkDurable looks one durable up under streamReadTimeout and records what the
// broker said on the row: present with its unread count, missing (the broker
// answered not found), or unknown (anything else).
func checkDurable(ctx context.Context, js jetstream.JetStream, stream, durable string, row map[string]any, flags *[]string) {
	rctx, cancel := context.WithTimeout(ctx, streamReadTimeout)
	defer cancel()
	cons, err := js.Consumer(rctx, stream, durable)
	switch {
	case errors.Is(err, jetstream.ErrConsumerNotFound):
		row["durable"] = "missing"
		row["durable_note"] = fmt.Sprintf("%s has no durable %s for this live session", stream, durable)
		*flags = append(*flags, flagNoDurable)
	case err != nil:
		row["durable"] = "unknown"
		row["durable_note"] = fmt.Sprintf("looking up durable %s on %s failed: %v", durable, stream, err)
		*flags = append(*flags, flagDurableUnchecked)
	default:
		row["durable"] = "present"
		if info := cons.CachedInfo(); info != nil {
			row["unread"] = info.NumPending + uint64(info.NumAckPending)
		}
	}
}
