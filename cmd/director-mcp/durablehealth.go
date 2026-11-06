package main

// Durable health (I-100 P6, aae-orc-tchxl). A shim rebuilds a durable the broker
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
	durableMissing = "missing" // lost, and the last rebuild failed
)

type durableHealth struct {
	mu      sync.Mutex
	state   string
	ok      int64
	failed  int64
	lastErr string
}

func (h *durableHealth) rebuilt() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ok++
	h.state, h.lastErr = durableRebuilt, ""
}

func (h *durableHealth) rebuildFailed(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.failed++
	h.state, h.lastErr = durableMissing, err.Error()
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
	return s
}

// mergeDurable folds tiers into one summary: the worst state wins (missing over
// rebuilt over present), counts add, and reasons join.
func mergeDurable(parts ...durableSummary) durableSummary {
	out := durableSummary{State: durablePresent}
	rank := map[string]int{durablePresent: 0, durableRebuilt: 1, durableMissing: 2}
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
