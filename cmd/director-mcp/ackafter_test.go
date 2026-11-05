package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// aae-orc-nanyb: acked after the reply is written; a late cancel gives the
// mail back in process.

// The probe from the aae-orc-jnxus premise check: w1 is cancelled after the
// bus fetched m1. Before this change m1 was acked and answered to w1, which the
// client had dropped, and w2 saw nothing. Now m1 is not acked and w1 gets no
// reply.
func TestACancelledWaitDoesNotTakeTheMessageAWaiterNeverReads(t *testing.T) {
	r := newPipeRig(t, false)
	var once sync.Once
	r.bus.testAfterAck = func(ctx context.Context) {
		fired := false
		once.Do(func() { fired = true })
		if !fired {
			return
		}
		r.cancel(`1`)
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
			t.Error("the cancel did not reach the call")
		}
	}
	openFirstWait(t, r)
	r.wait(`2`, 3, 1) // refused: one wait at a time
	if m := r.next(2 * time.Second); idOf(m) != `2` || !isErr(m) {
		t.Fatalf("the second wait must be refused: %v", m)
	}
	r.publish("m1")
	if !r.untilInFlight(0, 3*time.Second) {
		t.Fatalf("the cancelled wait is still in flight: %d", r.s.inFlight())
	}
	r.send(`{"jsonrpc":"2.0","id":"p","method":"ping"}`)
	if got := idOf(r.next(2 * time.Second)); got != `"p"` {
		t.Fatalf("the cancelled wait was answered (%s)", got)
	}
	r.wait(`3`, 5, 1)
	m := r.next(6 * time.Second)
	if idOf(m) != `3` || !strings.Contains(toolText(t, m), "m1") {
		t.Fatalf("m1 must come back to the next wait: %v", m)
	}
}

// A reply that was written is read by the client, which then cancels the call
// it had already answered. The envelope goes to the returned queue, the summary
// counts it, and the next wait returns it first.
func TestALateCancelReturnsTheAnsweredMailToTheNextWait(t *testing.T) {
	r := newPipeRig(t, false)
	var logs lockedLog
	r.s.log = logs.add
	r.wait(`1`, 10, 1)
	if !r.untilInFlight(1, 2*time.Second) {
		t.Fatal("the wait is not in flight")
	}
	r.publish("m1")
	if m := r.next(5 * time.Second); idOf(m) != `1` || !strings.Contains(toolText(t, m), "m1") {
		t.Fatalf("first wait: %v", m)
	}
	if !r.untilInFlight(0, 2*time.Second) {
		t.Fatal("the call is still in flight")
	}
	r.cancel(`1`)
	time.Sleep(200 * time.Millisecond)
	r.send(`{"jsonrpc":"2.0","id":"s","method":"tools/call","params":{"name":"inbox_summary","arguments":{}}}`)
	if txt := toolText(t, r.next(3*time.Second)); !strings.Contains(txt, `"returned": 1`) {
		t.Fatalf("the summary must show returned: 1: %s", txt)
	}
	r.publish("m2")
	r.wait(`2`, 5, 1)
	m := r.next(6 * time.Second)
	if txt := toolText(t, m); idOf(m) != `2` || !strings.Contains(txt, "m1") || !strings.Contains(txt, `"returned": true`) {
		t.Fatalf("the returned message must come first, marked: %v", m)
	}
	r.wait(`3`, 5, 1)
	if txt := toolText(t, r.next(6*time.Second)); !strings.Contains(txt, "m2") {
		t.Fatalf("m2 must follow: %s", txt)
	}
	if !logs.has("returned to the next wait") || !logs.has("message_id m1") {
		t.Fatalf("the move must be logged with the message_id: %v", logs.all())
	}
}

// A cancel past the window changes nothing and is logged.
func TestALateCancelPastTheWindowChangesNothing(t *testing.T) {
	r := newPipeRig(t, false)
	var logs lockedLog
	r.s.log = logs.add
	r.s.lateWindow = 150 * time.Millisecond
	r.wait(`1`, 10, 1)
	if !r.untilInFlight(1, 2*time.Second) {
		t.Fatal("the wait is not in flight")
	}
	r.publish("m1")
	if m := r.next(5 * time.Second); idOf(m) != `1` {
		t.Fatalf("first wait: %v", m)
	}
	if !r.untilInFlight(0, 2*time.Second) {
		t.Fatal("the call is still in flight")
	}
	time.Sleep(400 * time.Millisecond)
	r.cancel(`1`)
	time.Sleep(200 * time.Millisecond)
	if n := r.bus.returnedCount(); n != 0 {
		t.Fatalf("returned = %d past the window, want 0", n)
	}
	if !logs.has("past the") || !logs.has("m1") {
		t.Fatalf("the late cancel must be logged with the message_id: %v", logs.all())
	}
	r.wait(`2`, 1, 1)
	if txt := toolText(t, r.next(3*time.Second)); strings.Contains(txt, "m1") {
		t.Fatalf("m1 must not come back past the window: %s", txt)
	}
}

// A cancel on a batch wait after the fetch: all five are Nak'd, none answered,
// and the next batch wait gets all five.
func TestABatchCancelledBeforeItsReplyGivesAllFiveBack(t *testing.T) {
	r := newPipeRig(t, false)
	for i := 1; i <= 5; i++ {
		r.publish(fmt.Sprintf("b%d", i))
	}
	reached := make(chan struct{})
	r.s.testBeforeReply = func(ctx context.Context) {
		defer close(reached)
		r.cancel(`1`)
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
			t.Error("the cancel did not reach the call")
		}
	}
	r.wait(`1`, 5, 5)
	select {
	case <-reached:
	case <-time.After(4 * time.Second):
		t.Fatal("the batch wait never reached its reply")
	}
	if !r.untilInFlight(0, 4*time.Second) {
		t.Fatalf("the cancelled batch is still in flight: %d", r.s.inFlight())
	}
	r.s.testBeforeReply = nil
	r.send(`{"jsonrpc":"2.0","id":"p","method":"ping"}`)
	if got := idOf(r.next(2 * time.Second)); got != `"p"` {
		t.Fatalf("the cancelled batch was answered (%s)", got)
	}
	r.wait(`2`, 5, 5)
	txt := toolText(t, r.next(6*time.Second))
	for i := 1; i <= 5; i++ {
		if !strings.Contains(txt, fmt.Sprintf("b%d", i)) {
			t.Fatalf("b%d did not come back: %s", i, txt)
		}
	}
}

type lockedLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *lockedLog) add(f string, a ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(f, a...))
	l.mu.Unlock()
}

func (l *lockedLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

func (l *lockedLog) has(sub string) bool {
	for _, x := range l.all() {
		if strings.Contains(x, sub) {
			return true
		}
	}
	return false
}

func TestTheLateCancelWindowIsAThresholdRow(t *testing.T) {
	dir := t.TempDir()
	tab, _, err := loadThresholdsNotes(dir)
	if err != nil || tab[thresholdLateCancelWindow] != 30*time.Second {
		t.Fatalf("default = %v, %v; want 30s", tab[thresholdLateCancelWindow], err)
	}
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, "thresholds.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"late_cancel_window": "45s"}`)
	if tab, _, err = loadThresholdsNotes(dir); err != nil || tab[thresholdLateCancelWindow] != 45*time.Second {
		t.Fatalf("set = %v, %v; want 45s", tab[thresholdLateCancelWindow], err)
	}
	write(`{"late_cancel_window": "soon"}`)
	tab, notes, err := loadThresholdsNotes(dir)
	if err != nil || tab[thresholdLateCancelWindow] != 30*time.Second || len(notes) != 1 {
		t.Fatalf("bad value = %v, %v, notes %v; want the default and one note", tab[thresholdLateCancelWindow], err, notes)
	}
}
