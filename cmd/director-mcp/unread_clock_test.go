package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// The oldest unread message is aged on the broker's clock (the consumer info
// TimeStamp), not on the clock of the host that runs the reader, so a skewed
// reader host does not misreport how old the mail is.

func TestUnreadAgesOnTheBrokerClockNotTheReaderClock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	kv, _ := js.KeyValue(ctx, "AGENT_STATE")
	bin := buildShim(t, t.TempDir(), false)
	startShim(t, ctx, bin, url, Sender{AgentID: "a", Workspace: "w", Team: "t"}, kv)
	pubEnv(t, ctx, js, "agent.w.t.a.inbox", "mclock", "INFORM", "one")
	time.Sleep(2 * time.Second)

	// control: a reader whose clock agrees with the broker sees an age of about 2s.
	r, err := readUnread(ctx, js, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	d := findDurable(t, r, "a")
	if d.OldestAgeSeconds < 1 || d.OldestAgeSeconds > 30 {
		t.Fatalf("CONTROL FAILED: age with an agreeing clock = %.1fs, want about 2", d.OldestAgeSeconds)
	}

	// a reader host two hours ahead of the broker must not read the mail as two hours old.
	r, err = readUnread(ctx, js, time.Now().Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	d = findDurable(t, r, "a")
	if d.OldestAgeSeconds > 60 {
		t.Fatalf("age on a reader clock 2h ahead = %.0fs, want the broker-clock age (about 2s)", d.OldestAgeSeconds)
	}
	if strings.Contains(d.Note, "reader clock") {
		t.Fatalf("the broker supplied a time, yet the row says the reader clock was used: %q", d.Note)
	}
}

// When the server leaves the consumer info time zero, the age falls back to the
// reader clock and the row says so.
func TestBrokerClockFallsBackToReaderClockAndSaysSo(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	msg := now.Add(-90 * time.Second)

	secs, note := ageOnBrokerClock(time.Time{}, now, msg)
	if secs != 90 {
		t.Fatalf("fallback age = %v, want 90", secs)
	}
	if !strings.Contains(note, "reader clock") {
		t.Fatalf("fallback note = %q, want it to say the reader clock was used", note)
	}

	broker := now.Add(-30 * time.Second)
	secs, note = ageOnBrokerClock(broker, now, msg)
	if secs != 60 || note != "" {
		t.Fatalf("broker age = %v note %q, want 60 and no note", secs, note)
	}
}

// simulateAgreeingBrokerClock is for tests that stand in for elapsed time by
// handing the reader a clock in the future: the broker's clock is then taken to
// agree with that reader clock, which is what these tests were written against.
func simulateAgreeingBrokerClock(t *testing.T) {
	t.Helper()
	old := brokerClock
	brokerClock = func(_ *jetstream.ConsumerInfo, readerNow time.Time) time.Time { return readerNow }
	t.Cleanup(func() { brokerClock = old })
}
