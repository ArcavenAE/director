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

// The durable-idle window runs from the durable's Created, a broker time, to
// the broker's clock, so a reader host with a skewed clock neither raises the
// state early nor hides it.

func TestUnreadReaderClockAheadDoesNotNameAYoungDurableIdle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	seat := liveSeat(t, ctx, url, Sender{AgentID: "ahead", Workspace: "w", Team: "t"})
	pubEnv(t, ctx, js, "agent.w.t.ahead.inbox", "ahead1", "REQUEST", "hello")

	// control: with agreeing clocks the young durable with mail waiting is behind.
	if d := rowFor(t, mustRead(t, ctx, js, time.Now()), seat.instance); d.State != stateBehind {
		t.Fatalf("CONTROL FAILED: state with an agreeing clock = %q, want %q", d.State, stateBehind)
	}

	d := rowFor(t, mustRead(t, ctx, js, time.Now().Add(2*time.Hour)), seat.instance)
	if d.State != stateBehind {
		t.Fatalf("reader clock 2h ahead: state = %q, want %q: the broker clock says the durable is younger than the idle window", d.State, stateBehind)
	}
}

func TestUnreadReaderClockBehindStillNamesAnOldDurableIdle(t *testing.T) {
	old := unreadIdleWindow
	unreadIdleWindow = time.Second
	t.Cleanup(func() { unreadIdleWindow = old })
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	seat := liveSeat(t, ctx, url, Sender{AgentID: "slow", Workspace: "w", Team: "t"})
	pubEnv(t, ctx, js, "agent.w.t.slow.inbox", "slow1", "REQUEST", "hello")
	time.Sleep(2 * time.Second)

	// control: with agreeing clocks the durable is past the window.
	if d := rowFor(t, mustRead(t, ctx, js, time.Now()), seat.instance); d.State != stateDurableIdle {
		t.Fatalf("CONTROL FAILED: state with an agreeing clock = %q, want %q", d.State, stateDurableIdle)
	}

	d := rowFor(t, mustRead(t, ctx, js, time.Now().Add(-2*time.Hour)), seat.instance)
	if d.State != stateDurableIdle {
		t.Fatalf("reader clock 2h behind: state = %q, want %q: the broker clock says the durable is past the idle window", d.State, stateDurableIdle)
	}
}

// When the server leaves the consumer info time zero, the idle window falls
// back to the reader clock and the row says so. A row whose state needs no
// clock says nothing about the window.
func TestUnreadIdleWindowOnTheReaderClockSaysSo(t *testing.T) {
	real := brokerClock
	brokerClock = func(*jetstream.ConsumerInfo, time.Time) time.Time { return time.Time{} }
	t.Cleanup(func() { brokerClock = real })
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	waiting := liveSeat(t, ctx, url, Sender{AgentID: "waiting", Workspace: "w", Team: "t"})
	empty := liveSeat(t, ctx, url, Sender{AgentID: "empty", Workspace: "w", Team: "t"})
	pubEnv(t, ctx, js, "agent.w.t.waiting.inbox", "wait1", "REQUEST", "hello")

	r := mustRead(t, ctx, js, time.Now().Add(2*time.Hour))
	d := rowFor(t, r, waiting.instance)
	if d.State != stateDurableIdle {
		t.Fatalf("no broker time, reader clock 2h ahead: state = %q, want %q from the reader clock", d.State, stateDurableIdle)
	}
	const want = "idle window uses the reader clock"
	if !strings.Contains(d.Note, want) {
		t.Fatalf("row note = %q, want it to say %q", d.Note, want)
	}
	if d := rowFor(t, r, empty.instance); d.State != stateReading || strings.Contains(d.Note, want) {
		t.Fatalf("row with nothing waiting = state %q note %q, want %q and no idle-window note", d.State, d.Note, stateReading)
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
