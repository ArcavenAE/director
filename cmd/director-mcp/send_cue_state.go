package main

// The recipient's cue state in a send result (director#719). A send says
// "accepted for delivery" whatever state the recipient's cue is in, so a seat
// whose cue is off, unverified or stalled misses an ask and the sender never
// learns it. The state is already published: each seat writes cue, cue_reason
// and oldest_pending_s into its presence row (bus.go, presenceView.fields).
// This reads that row once, after the send is accepted, and adds what it says
// to the result. It is a diagnostic: it never changes whether or where a send
// goes, and a row it cannot read adds nothing.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// cueStatesThatDoNotWake are the cue states in which the notice will not reach
// the seat as a turn: the shim sends none (off), sends one no model has echoed
// (unverified), or sent one the seat has not drained (stalled).
var cueStatesThatDoNotWake = map[string]bool{"off": true, "unverified": true, "stalled": true}

// recipientCueFields reads the presence row of a local agent:// recipient and
// returns the result fields it supports: recipient_cue, recipient_cue_reason,
// recipient_oldest_pending_s and, for a state that does not wake the seat,
// recipient_warning, one line on its own key (the relay-log warning owns
// "warning"). It returns nil for any other address, for a recipient with no
// row, and for a row that carries no cue field (the cue is off in its own
// config, or its shim predates the field): the result then says nothing about
// the cue rather than guessing. When a recipient has several live rows, the
// newest by ts speaks.
func (b *Bus) recipientCueFields(ctx context.Context, addr string) map[string]any {
	rest, ok := strings.CutPrefix(addr, "agent://")
	if !ok || b.kv == nil {
		return nil
	}
	team, id, ok := strings.Cut(rest, "/")
	if !ok {
		return nil
	}
	keys, err := b.kv.Keys(ctx)
	if err != nil { // no keys, or the read failed: either way there is nothing to report
		return nil
	}
	prefix := "presence." + team + "." + id + "."
	var row map[string]any
	newest := ""
	for _, k := range keys {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		entry, err := b.kv.Get(ctx, k)
		if err != nil {
			continue
		}
		var rec map[string]any
		if json.Unmarshal(entry.Value(), &rec) != nil {
			continue
		}
		ts, _ := rec["ts"].(string)
		if row == nil || ts > newest {
			row, newest = rec, ts
		}
	}
	state, _ := row["cue"].(string)
	if state == "" {
		return nil
	}
	out := map[string]any{"recipient_cue": state}
	if reason, _ := row["cue_reason"].(string); reason != "" {
		out["recipient_cue_reason"] = reason
	}
	if age, ok := ageSeconds(row["oldest_pending_s"]); ok {
		out["recipient_oldest_pending_s"] = int64(age)
	}
	if cueStatesThatDoNotWake[state] {
		out["recipient_warning"] = fmt.Sprintf("the recipient's cue is %s, so this message may sit unread until the seat polls; list_roster shows its cue state and pending age", state)
	}
	return out
}
