package main

// The drain's proposed tier (aae-orc-b0uw2, T6 of the director-cue plan). The
// batch form of wait_for_message says, for each message, the tier the receiver
// classifier proposes (act or routine) and why, and adds one fact line when
// every message in the batch is routine, so a seat with no cue (codex,
// opencode, crush) can skip a batch that needs no turn.

import "time"

// allRoutineLine is the fact line a batch carries when nothing in it is act.
const allRoutineLine = "no board change; no reply owed"

// tierBatch sets ProposedTier and Reason on each item and returns the fact
// line: allRoutineLine when the batch is not empty and every item is routine,
// else "". A lane item (a reply to an open operator ask) is never routine.
// Not built yet: this stub changes nothing.
func tierBatch(cls *classifier, items []drained, lane inboxLane, now time.Time) string {
	return ""
}
