package main

// The receiver classifier (aae-orc-tk8ge, T1 of the director-cue plan). It
// sorts each message the seat receives into act or routine, once, so a later
// cue can wake at once for act and hold routine mail. It only classifies; the
// cue does not read it yet.
//
// Inputs only raise. A message is act when it is a CANCEL, FAILURE, REFUSE,
// REQUEST, QUERY, PROPOSE, CFP or NOT-UNDERSTOOD; the first reply to a
// REQUEST, QUERY, PROPOSE or CFP this seat sent; a body with a GATE, Pre-GATE
// or WORD line (the same test look.go uses, gateLine); one whose
// reply_by has passed; or anything the classifier cannot read, which fails
// open. Everything else is routine. A body never lowers a class, so a
// REQUEST that says no wake is needed stays act.
//
// A message is classified once, keyed by tier and stream sequence (sequences
// order messages within one tier only). The one thing that can change after
// that is a reply_by passing, which is a comparison against the remembered
// time and no second decode.

import (
	"encoding/json"
	"sort"
	"sync"
	"time"
)

type msgClass string

const (
	classAct     msgClass = "act"
	classRoutine msgClass = "routine"
)

const (
	// classifyMemoCap bounds the per-message memo. Past it, the lowest half by
	// sequence is dropped, so a seat that falls that far behind decodes its
	// oldest messages again. The class comes out the same: the ask record
	// remembers which message was the first reply, and a reply_by is read
	// from the message.
	classifyMemoCap = 16384
	// classifyAskCap bounds the record of sent ids. Past it the oldest are
	// dropped, and a reply to a dropped id is unmatched, so it is act: an
	// eviction costs a turn and never loses a wake.
	classifyAskCap = 4096
)

// askPerformatives are the sent messages that ask for a turn or a word back;
// the first reply to one is act.
var askPerformatives = map[string]bool{"REQUEST": true, "QUERY": true, "PROPOSE": true, "CFP": true}

type classKey struct {
	tier string
	seq  uint64
}

type classEntry struct {
	class   msgClass
	replyBy time.Time // zero when the message carries none
}

// askState records the message that was the first reply to a sent ask, so
// the same message, decoded again after a memo eviction, is still that reply.
type askState struct {
	ask      bool // the sent message was a REQUEST or QUERY
	consumed bool
	by       classKey
}

type classifier struct {
	mu      sync.Mutex
	memo    map[classKey]classEntry
	decodes int // messages decoded, for the once-per-message check
	floor   map[string]uint64
	asks    map[string]*askState // every sent message id -> whether it was an ask, and who answered it
	askList []string             // insertion order, for eviction
}

func newClassifier() *classifier {
	return &classifier{
		memo:  map[classKey]classEntry{},
		floor: map[string]uint64{},
		asks:  map[string]*askState{},
	}
}

// noteSent records every message this seat sends. The first reply to a
// REQUEST, QUERY, PROPOSE or CFP is act; a reply to any other recorded message falls through
// to the ordinary rules, and a reply to an id never recorded is act.
func (c *classifier) noteSent(id, perf string) {
	if id == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.asks[id]; ok {
		return
	}
	c.asks[id] = &askState{ask: askPerformatives[perf]}
	c.askList = append(c.askList, id)
	if len(c.askList) > classifyAskCap {
		drop := len(c.askList) - classifyAskCap/2
		for _, old := range c.askList[:drop] {
			delete(c.asks, old)
		}
		c.askList = append([]string(nil), c.askList[drop:]...)
	}
}

// setBacklogFloor sets the stream sequence of the tier's durable at creation.
// Sequences are compared within the tier; clocks are never compared.
func (c *classifier) setBacklogFloor(tier string, seq uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.floor[tier] = seq
}

// isBacklog reports a message at or below the tier's floor: it was already
// waiting when the durable was created, so it is never cued one by one.
func (c *classifier) isBacklog(tier string, seq uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	f, ok := c.floor[tier]
	return ok && seq != 0 && seq <= f
}

func (c *classifier) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.memo)
}

// classify returns the message's class. A message with no sequence, or one
// that does not decode into an envelope with a message id, is act.
func (c *classifier) classify(tier string, seq uint64, data []byte, now time.Time) msgClass {
	if seq == 0 {
		return classAct
	}
	k := classKey{tier, seq}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.memo[k]; ok {
		if e.class == classRoutine && !e.replyBy.IsZero() && e.replyBy.Before(now) {
			e.class = classAct
			c.memo[k] = e
		}
		return e.class
	}
	c.decodes++
	e := c.decide(k, data, now)
	if len(c.memo) >= classifyMemoCap {
		c.evict()
	}
	c.memo[k] = e
	return e.class
}

func (c *classifier) decide(k classKey, data []byte, now time.Time) classEntry {
	var env Envelope
	if json.Unmarshal(data, &env) != nil || env.MessageID == "" {
		return classEntry{class: classAct}
	}
	// The ask check runs on every path below, so a first reply that is act for
	// another reason (a passed reply_by, an unknown performative) still
	// consumes its ask.
	signal := env.Content.Type == "signal"
	act := c.replyToOpenAsk(k, env.InReplyTo, signal)
	if !performatives[env.Performative] {
		return classEntry{class: classAct}
	}
	var replyBy time.Time
	if env.ReplyBy != "" {
		t, err := time.Parse(time.RFC3339, env.ReplyBy)
		if err != nil {
			return classEntry{class: classAct}
		}
		replyBy = t
		if t.Before(now) {
			return classEntry{class: classAct, replyBy: t}
		}
	}
	switch env.Performative {
	case "CANCEL", "FAILURE", "REFUSE", "REQUEST", "QUERY", "PROPOSE", "CFP", "NOT-UNDERSTOOD":
		act = true
	}
	if gateLine.MatchString(env.Content.Data) {
		act = true
	}
	// A status signal changes nothing and owes no reply when it says
	// "working"; any other signal value, blocked-on included, is act.
	if signal && env.Content.Data != "working" {
		act = true
	}
	if act {
		return classEntry{class: classAct, replyBy: replyBy}
	}
	return classEntry{class: classRoutine, replyBy: replyBy}
}

// replyToOpenAsk reports whether a message with this in_reply_to is act on
// that account: the first reply to a sent ask, the same message again, or a
// reply to an id the seat never recorded. The caller holds mu.
//
// A signal (the status a seat reports while it works) never consumes the ask,
// so the answer that follows it is still the first reply.
func (c *classifier) replyToOpenAsk(k classKey, inReplyTo string, signal bool) bool {
	if inReplyTo == "" {
		return false
	}
	a, known := c.asks[inReplyTo]
	if !known {
		return true
	}
	if !a.ask || signal {
		return false
	}
	if !a.consumed {
		a.consumed, a.by = true, k
		return true
	}
	return a.by == k
}

// evict drops the lowest half of the memo by sequence; the caller holds mu.
func (c *classifier) evict() {
	keys := make([]classKey, 0, len(c.memo))
	for k := range c.memo {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].seq < keys[j].seq })
	for _, k := range keys[:len(keys)/2] {
		delete(c.memo, k)
	}
}
