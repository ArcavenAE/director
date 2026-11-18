package main

// Durable health (aae-orc-tchxl, P6). A shim rebuilds a durable the broker
// lost (localrecover.go, and global.go restore) and used to say so once, in a
// notice. This keeps the count: how many rebuilds worked, how many failed, and
// whether the durable is back. It rides in the presence record and in
// `director-mcp unread`, adds no network path, and gates nothing (SOUL 8).

import (
	"fmt"
	"sync"
)

const (
	durablePresent = "present" // never lost, or no rebuild attempted
	durableRebuilt = "rebuilt" // lost, and recreated
	durableUnknown = "unknown" // the broker did not answer the last lookup
	durableMissing = "missing" // lost, and the last rebuild failed
)

type durableHealth struct {
	mu      sync.Mutex
	state   string
	ok      int64
	failed  int64
	lastErr string
	// unkErr is why the last lookup went unanswered. It is set only while the
	// state is unknown, and a rebuild or an answered lookup clears it.
	unkErr string
}

func (h *durableHealth) rebuilt() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ok++
	h.state, h.lastErr, h.unkErr = durableRebuilt, "", ""
}

func (h *durableHealth) rebuildFailed(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.failed++
	h.state, h.lastErr = durableMissing, err.Error()
}

// lookupUnanswered records a lookup the broker did not answer: neither present
// nor not-found. Nothing was attempted, so no rebuild is counted. A failed
// rebuild stays the worse state and is not overwritten.
func (h *durableHealth) lookupUnanswered(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state == durableMissing {
		return
	}
	h.state, h.unkErr = durableUnknown, err.Error()
}

// lookupAnswered clears an unknown state: the broker answered, so the durable is
// present again (or rebuilt, when an earlier rebuild succeeded).
func (h *durableHealth) lookupAnswered() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state != durableUnknown {
		return
	}
	h.unkErr = ""
	if h.ok > 0 {
		h.state = durableRebuilt
	} else {
		h.state = ""
	}
}

// durableSummary is the view of one or more tiers' health that a presence record
// and a report carry.
type durableSummary struct {
	State  string
	OK     int64
	Failed int64
	Reason string
}

func (h *durableHealth) summary(tier string) durableSummary {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := durableSummary{State: h.state, OK: h.ok, Failed: h.failed}
	if s.State == "" {
		s.State = durablePresent
	}
	if h.lastErr != "" {
		s.Reason = fmt.Sprintf("durable rebuild failed on the %s tier: %s", tier, h.lastErr)
	}
	if h.state == durableUnknown && h.unkErr != "" {
		s.Reason = fmt.Sprintf("durable lookup unanswered on the %s tier: %s", tier, h.unkErr)
	}
	return s
}

// mergeDurable folds tiers into one summary: the worst state wins (missing over
// unknown over rebuilt over present), counts add, and reasons join.
func mergeDurable(parts ...durableSummary) durableSummary {
	out := durableSummary{State: durablePresent}
	rank := map[string]int{durablePresent: 0, durableRebuilt: 1, durableUnknown: 2, durableMissing: 3}
	for _, p := range parts {
		if rank[p.State] > rank[out.State] {
			out.State = p.State
		}
		out.OK += p.OK
		out.Failed += p.Failed
		out.Reason = joinReason(out.Reason, p.Reason)
	}
	return out
}
