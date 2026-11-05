package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// aae-orc-jnxus: a wait acks what it reads, so a second open wait on one
// session is refused before any fetch and reads nothing.

func isErr(m map[string]any) bool {
	res, _ := m["result"].(map[string]any)
	e, _ := res["isError"].(bool)
	return e
}

// openFirstWait leaves wait id 1 blocked on an empty inbox.
func openFirstWait(t *testing.T, r *pipeRig) {
	t.Helper()
	r.wait(`1`, 20, 1)
	if !r.untilInFlight(1, 2*time.Second) {
		t.Fatal("the first wait is not in flight")
	}
}

func TestASecondWaitIsRefusedAndTheFirstKeepsTheMessages(t *testing.T) {
	for _, max := range []int{1, 5} {
		t.Run(fmt.Sprintf("max=%d", max), func(t *testing.T) {
			r := newPipeRig(t, false)
			openFirstWait(t, r)
			r.wait(`2`, 5, max)
			m := r.next(2 * time.Second)
			if idOf(m) != `2` || !isErr(m) {
				t.Fatalf("the second wait must be refused at once: %v", m)
			}
			txt := toolText(t, m)
			if !strings.Contains(txt, "already open") || !strings.Contains(txt, "nothing was read") {
				t.Fatalf("refusal text = %q", txt)
			}
			r.publish("m1")
			r.publish("m2")
			m = r.next(8 * time.Second)
			if idOf(m) != `1` || isErr(m) || !strings.Contains(toolText(t, m), "m1") || strings.Contains(toolText(t, m), "m2") {
				t.Fatalf("the first wait must return m1 only: %v", m)
			}
			// m2 is still on the durable.
			r.send(`{"jsonrpc":"2.0","id":"s","method":"tools/call","params":{"name":"inbox_summary","arguments":{}}}`)
			m = r.next(3 * time.Second)
			if !strings.Contains(toolText(t, m), `"local": 1`) {
				t.Fatalf("m2 should still be waiting: %s", toolText(t, m))
			}
		})
	}
}

func TestAWaitSucceedsAfterTheFirstIsCancelled(t *testing.T) {
	r := newPipeRig(t, false)
	openFirstWait(t, r)
	r.cancel(`1`)
	if !r.untilInFlight(0, time.Second) {
		t.Fatal("the cancelled wait is still in flight")
	}
	r.publish("after")
	r.wait(`3`, 5, 1)
	m := r.next(6 * time.Second)
	if idOf(m) != `3` || isErr(m) || !strings.Contains(toolText(t, m), "after") {
		t.Fatalf("a wait after a cancel must run: %v", m)
	}
}

func TestAWaitSucceedsAfterTheFirstTimesOut(t *testing.T) {
	r := newPipeRig(t, false)
	r.wait(`1`, 1, 1)
	if m := r.next(4 * time.Second); idOf(m) != `1` || isErr(m) {
		t.Fatalf("first wait: %v", m)
	}
	r.publish("late")
	r.wait(`2`, 5, 1)
	m := r.next(6 * time.Second)
	if idOf(m) != `2` || isErr(m) || !strings.Contains(toolText(t, m), "late") {
		t.Fatalf("a wait after a timeout must run: %v", m)
	}
}

func TestAnOpenLocalWaitRefusesASecondOnTheGlobalTierToo(t *testing.T) {
	r := newPipeRig(t, true)
	openFirstWait(t, r)
	r.wait(`2`, 5, 1)
	m := r.next(2 * time.Second)
	if idOf(m) != `2` || !isErr(m) {
		t.Fatalf("the second wait must be refused: %v", m)
	}
	r.publishGlobal("g1")
	// The first wait reaches the global tier after the local slice.
	m = r.next(12 * time.Second)
	if idOf(m) != `1` || !strings.Contains(toolText(t, m), "g1") {
		t.Fatalf("the global message must go to the first wait: %v", m)
	}
}

func TestInboxSummaryAnswersDuringAnOpenWait(t *testing.T) {
	r := newPipeRig(t, false)
	openFirstWait(t, r)
	r.send(`{"jsonrpc":"2.0","id":"s","method":"tools/call","params":{"name":"inbox_summary","arguments":{}}}`)
	m := r.next(3 * time.Second)
	if idOf(m) != `"s"` || isErr(m) {
		t.Fatalf("inbox_summary beside an open wait: %v", m)
	}
}

func TestARefusedWaitDoesNotRaiseTheCuesOpenWaits(t *testing.T) {
	r := newPipeRig(t, false)
	r.s.cue = newCueRig(t).c
	openFirstWait(t, r)
	r.wait(`2`, 5, 1)
	if m := r.next(2 * time.Second); idOf(m) != `2` || !isErr(m) {
		t.Fatalf("the second wait must be refused: %v", m)
	}
	r.s.cue.mu.Lock()
	n := r.s.cue.openWaits
	r.s.cue.mu.Unlock()
	if n != 1 {
		t.Fatalf("openWaits = %d, want 1 (the first wait only)", n)
	}
}
