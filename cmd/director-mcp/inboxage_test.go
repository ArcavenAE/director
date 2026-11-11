package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// aae-orc-t77rr: director-mcp reads AGENT_INBOX's max_age from the live stream
// at startup instead of assuming a constant. These use the scratch broker of
// drain_broker_test.go and build the stream themselves, since provision() makes
// one with no max age.

// resetInboxMaxAge puts the process back on the fallback when a test ends, so
// one test's adopted value never reaches another's.
func resetInboxMaxAge(t *testing.T) {
	t.Helper()
	liveInboxMaxAge.Store(0)
	t.Cleanup(func() { liveInboxMaxAge.Store(0) })
}

func inboxStream(t *testing.T, maxAge time.Duration) (context.Context, jetstream.JetStream) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	nc, err := nats.Connect(startScratchServer(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: "AGENT_INBOX", Subjects: []string{"agent.*.*.*.inbox"}, MaxAge: maxAge}); err != nil {
		t.Fatal(err)
	}
	return ctx, js
}

type logCapture struct{ lines []string }

func (c *logCapture) logf(format string, a ...any) {
	c.lines = append(c.lines, fmt.Sprintf(format, a...))
}
func (c *logCapture) joined() string { return strings.Join(c.lines, "\n") }

func TestAFreshProcessAssumesTheFallback(t *testing.T) {
	resetInboxMaxAge(t)
	if got := inboxMaxAge(); got != inboxMaxAgeFallback {
		t.Errorf("inboxMaxAge() = %v before any read, want the fallback %v", got, inboxMaxAgeFallback)
	}
}

func TestA72hStreamGives72hBehaviour(t *testing.T) {
	resetInboxMaxAge(t)
	ctx, js := inboxStream(t, 72*time.Hour)
	var lc logCapture
	adoptInboxMaxAge(ctx, js, lc.logf)
	if got := inboxMaxAge(); got != 72*time.Hour {
		t.Fatalf("inboxMaxAge() = %v, want 72h read from the live stream", got)
	}
	if got := sendKeep(); got != 96*time.Hour {
		t.Errorf("sendKeep() = %v, want 72h plus a day", got)
	}
	// the ledger prunes by that value, not by the fallback
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	l := newAskLedger()
	l.Sends["keep"] = sendRec{Seq: 1, At: now.Add(-95 * time.Hour)}
	l.Sends["drop"] = sendRec{Seq: 2, At: now.Add(-97 * time.Hour)}
	l.Sweep(now)
	if _, ok := l.Sends["keep"]; !ok {
		t.Error("a send 95h old was pruned on a 72h stream (kept for 96h)")
	}
	if _, ok := l.Sends["drop"]; ok {
		t.Error("a send 97h old was kept on a 72h stream; the 168h fallback was used")
	}
	if !strings.Contains(lc.joined(), "72h0m0s") {
		t.Errorf("startup log = %q, want the adopted value named", lc.joined())
	}
}

func TestAStreamLongerThanTheFallbackIsAdoptedToo(t *testing.T) {
	resetInboxMaxAge(t)
	ctx, js := inboxStream(t, 240*time.Hour)
	adoptInboxMaxAge(ctx, js, func(string, ...any) {})
	if got := inboxMaxAge(); got != 240*time.Hour {
		t.Errorf("inboxMaxAge() = %v, want 240h", got)
	}
}

func TestAMissingStreamFallsBackWithAWarning(t *testing.T) {
	resetInboxMaxAge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	nc, err := nats.Connect(startScratchServer(t))
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, _ := jetstream.New(nc) // no AGENT_INBOX was created
	var lc logCapture
	adoptInboxMaxAge(ctx, js, lc.logf)
	if got := inboxMaxAge(); got != inboxMaxAgeFallback {
		t.Errorf("inboxMaxAge() = %v after a failed read, want the fallback %v", got, inboxMaxAgeFallback)
	}
	if out := lc.joined(); !strings.Contains(out, "WARNING") || !strings.Contains(out, "fallback") || !strings.Contains(out, "168h0m0s") {
		t.Errorf("log = %q, want a warning that names the fallback and its value", out)
	}
}

func TestAStreamWithNoMaxAgeFallsBackWithAWarning(t *testing.T) {
	resetInboxMaxAge(t)
	ctx, js := inboxStream(t, 0) // unlimited
	var lc logCapture
	adoptInboxMaxAge(ctx, js, lc.logf)
	if got := inboxMaxAge(); got != inboxMaxAgeFallback {
		t.Errorf("inboxMaxAge() = %v for a stream with no max age, want the fallback %v", got, inboxMaxAgeFallback)
	}
	if out := lc.joined(); !strings.Contains(out, "WARNING") || !strings.Contains(out, "no max age") {
		t.Errorf("log = %q, want a warning that the stream has no max age", out)
	}
}

func TestAReadFailureNeverBlocksOrPanics(t *testing.T) {
	resetInboxMaxAge(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // a cancelled context makes the read fail
	nc, err := nats.Connect(startScratchServer(t))
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, _ := jetstream.New(nc)
	done := make(chan struct{})
	go func() { adoptInboxMaxAge(ctx, js, func(string, ...any) {}); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("adoptInboxMaxAge did not return after a failed read")
	}
	if got := inboxMaxAge(); got != inboxMaxAgeFallback {
		t.Errorf("inboxMaxAge() = %v, want the fallback", got)
	}
}
