package main

// The drain's proposed tier (aae-orc-b0uw2, T6 of the director-cue plan). The
// batch form of wait_for_message says, for each message, the tier the receiver
// classifier proposes (act or routine) and why, and adds one fact line when
// every message in the batch is routine, so a seat with no cue (codex,
// opencode, crush) can skip a batch that needs no turn.

import (
	"encoding/json"
	"strings"
	"time"
)

// allRoutineLine is the fact line a batch carries when nothing in it is act.
const allRoutineLine = "no reply owed in this batch"

// tierBatch sets ProposedTier and Reason on each item and returns the fact
// line: allRoutineLine when the batch is not empty and every item is routine,
// else "". The line states what is computed: the classifier proposes routine
// and inbox_summary flags nothing, for every message in the batch. A lane item (a reply to an open operator ask) is never routine: the
// lane wins over the classifier, which is still asked first so each message is
// classified once, by tier and sequence.
func tierBatch(cls *classifier, items []drained, lane inboxLane, now time.Time) string {
	inLane := map[classKey]string{}
	for _, l := range lane.Items {
		inLane[classKey{l.Tier, l.Sequence}] = l.AskID
	}
	routine := 0
	for i := range items {
		it := &items[i]
		data, _ := json.Marshal(it.Env)
		class, why := cls.classifyWhy(it.Tier, it.Seq, data, now)
		if ask, ok := inLane[classKey{it.Tier, it.Seq}]; ok {
			class, why = classAct, "lane: reply to open ask "+ask
		}
		// inbox_summary flags a message that carries a reply_by, custody or an
		// awaited answer; the drain never calls such a message routine, so it
		// never says "no reply owed" while the summary says one is.
		if class == classRoutine && it.Env != nil {
			if flags := flagReasons(it.Env); len(flags) > 0 {
				class, why = classAct, "flagged: "+strings.Join(flags, ", ")
			}
		}
		it.ProposedTier, it.Reason = string(class), why
		if class == classRoutine {
			routine++
		}
	}
	if len(items) > 0 && routine == len(items) {
		return allRoutineLine
	}
	return ""
}

// tierDrain tiers a batch about to be returned and returns its fact line. The
// line is withheld when the same call discarded an unreadable message, since
// that message may have been a REQUEST: "no reply owed" is a claim about
// everything that arrived.
func (b *Bus) tierDrain(items []drained, discarded int) string {
	if len(items) == 0 {
		return ""
	}
	sum := make([]summaryItem, 0, len(items))
	for _, it := range items {
		sum = append(sum, summaryItem{Tier: it.Tier, Seq: it.Seq, Env: it.Env})
	}
	now := time.Now()
	line := tierBatch(b.classifier(), items, loadLane(sum, now), now)
	if discarded > 0 {
		return ""
	}
	return line
}

// classifier is the seat's receiver classifier: the cue's when the class-aware
// cue is on, so a message is classified once whichever reads it first, and the
// drain's own otherwise (a seat with no cue still gets tiers).
func (b *Bus) classifier() *classifier {
	if b.sentCls != nil {
		return b.sentCls
	}
	b.ownClsOnce.Do(func() { b.ownCls = newClassifier() })
	return b.ownCls
}
