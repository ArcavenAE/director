package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// director#269, plan T-A1. The local durable is rebuilt when a pull or a summary
// fails, or when an empty poll asks (localrecover.go). A seat that has stopped
// polling does neither, and a lookup the broker never answers is dropped without
// a trace. These tests hold the heartbeat to the same check, and an unanswered
// lookup to a state a reader can see. All of it runs on a scratch nats-server.

// runHeartbeat ticks the shim's heartbeat until the returned stop is called.
func runHeartbeat(t *testing.T, ctx context.Context, b *Bus, every time.Duration) (stop func()) {
	t.Helper()
	hctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { b.heartbeat(hctx, every, func(string, ...any) {}); close(done) }()
	var once sync.Once
	stop = func() { once.Do(func() { cancel(); <-done }) }
	t.Cleanup(stop)
	return stop
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(d time.Duration, cond func() bool) bool {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}

func TestAHeartbeatRebuildsALocalDurableDeletedWhileTheSeatIsNotPolling(t *testing.T) {
	old := durableCheckEvery
	durableCheckEvery = 0
	defer func() { durableCheckEvery = old }()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, js := localLossBus(t, ctx)

	// control: with the durable intact the heartbeat rebuilds nothing.
	stop := runHeartbeat(t, ctx, bus, 40*time.Millisecond)
	time.Sleep(300 * time.Millisecond)
	if rec := presenceRecord(t, ctx, bus); rec["rebuilds_ok"] != nil {
		t.Fatalf("CONTROL FAILED: a healthy durable reported a rebuild: %v", rec)
	}
	stop()

	pubEnv(t, ctx, js, localLossSubject, "h1", "INFORM", "sent while the seat is not polling")
	if err := js.DeleteConsumer(ctx, "AGENT_INBOX", localDurableName(bus)); err != nil {
		t.Fatal(err)
	}
	// No receive, summary or wait from here: only the heartbeat runs.
	runHeartbeat(t, ctx, bus, 40*time.Millisecond)
	back := waitFor(5*time.Second, func() bool {
		_, err := js.Consumer(ctx, "AGENT_INBOX", localDurableName(bus))
		return err == nil
	})
	if !back {
		t.Fatal("the heartbeat did not rebuild the deleted local durable within 5s")
	}
	c, err := js.Consumer(ctx, "AGENT_INBOX", localDurableName(bus))
	if err != nil {
		t.Fatal(err)
	}
	if n := c.CachedInfo().NumPending; n != 1 {
		t.Errorf("rebuilt durable has %d pending, want 1 (h1 delivered, not skipped)", n)
	}
	if rec := presenceRecord(t, ctx, bus); rec["rebuilds_ok"] != int64(1) {
		t.Errorf("presence rebuilds_ok = %v, want 1: %v", rec["rebuilds_ok"], rec)
	}
}

func TestRestoreLocalUnansweredLookupMarksDurable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, _ := localLossBus(t, ctx)
	if rec := presenceRecord(t, ctx, bus); rec["durable_state"] != nil {
		t.Fatalf("CONTROL FAILED: a healthy seat carries durable_state: %v", rec)
	}

	asks := counted(t, bus, nats.ErrNoResponders)
	ok, err := bus.restoreLocal(ctx)
	if ok || err != nil {
		t.Fatalf("restoreLocal on an unanswered lookup = %v, %v; want false, nil (nothing rebuilt, no failure)", ok, err)
	}
	if *asks != 1 {
		t.Fatalf("CONTROL FAILED: the lookup hook ran %d times, want 1", *asks)
	}
	rec := presenceRecord(t, ctx, bus)
	if rec["durable_state"] != "unknown" {
		t.Errorf("durable_state = %v after an unanswered lookup, want unknown: %v", rec["durable_state"], rec)
	}
	if r, _ := rec["durable_reason"].(string); !strings.Contains(r, "no responders") {
		t.Errorf("durable_reason = %q, want it to carry the lookup error (no responders)", r)
	}
	if n, _ := rec["rebuilds_failed"].(int64); n != 0 {
		t.Errorf("an unanswered lookup counted %d failed rebuilds, want 0: nothing was attempted", n)
	}

	// The next answered lookup clears it.
	counted(t, bus, nil)
	if ok, err := bus.restoreLocal(ctx); ok || err != nil {
		t.Fatalf("restoreLocal on an answered, present lookup = %v, %v; want false, nil", ok, err)
	}
	if rec := presenceRecord(t, ctx, bus); rec["durable_state"] != nil && rec["durable_state"] != durablePresent {
		t.Errorf("durable_state = %v after an answered lookup, want it cleared: %v", rec["durable_state"], rec)
	}
}

// The fourth state ranks between rebuilt and missing: a failed rebuild is still
// the worst thing a seat can report. The literal is deliberate; this runs before
// the state has a name.
func TestMergeDurableRanksUnknownBetweenRebuiltAndMissing(t *testing.T) {
	unknown := durableSummary{State: "unknown", Reason: "lookup failed"}
	if got := mergeDurable(unknown, durableSummary{State: durableRebuilt, OK: 1}); got.State != "unknown" {
		t.Errorf("unknown over rebuilt: state %q, want unknown", got.State)
	}
	if got := mergeDurable(durableSummary{State: durablePresent}, unknown); got.State != "unknown" {
		t.Errorf("unknown over present: state %q, want unknown", got.State)
	}
	if got := mergeDurable(unknown, durableSummary{State: durableMissing, Failed: 1}); got.State != durableMissing {
		t.Errorf("missing over unknown: state %q, want missing", got.State)
	}
}

// Poll and heartbeat can both find the durable gone at the same moment. A hook
// holds each lookup until the other has asked, so both see "not found". The seat
// must end with one consumer and one rebuild counted for the one loss.
func TestPollAndHeartbeatRebuildingTogetherLeaveOneConsumerAndOneCount(t *testing.T) {
	old := durableCheckEvery
	durableCheckEvery = 0
	defer func() { durableCheckEvery = old }()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, js := localLossBus(t, ctx)
	if err := js.DeleteConsumer(ctx, "AGENT_INBOX", localDurableName(bus)); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	asked := 0
	both := make(chan struct{})
	bus.lookup = func(ctx context.Context, durable string) error {
		mu.Lock()
		asked++
		if asked == 2 {
			close(both)
		}
		mu.Unlock()
		select {
		case <-both:
		case <-time.After(time.Second):
		}
		_, err := bus.js.Consumer(ctx, "AGENT_INBOX", durable)
		return err
	}

	runHeartbeat(t, ctx, bus, 40*time.Millisecond)
	polled := make(chan error, 1)
	go func() { _, err := bus.receiveTiered(ctx, 3*time.Second); polled <- err }()
	if err := <-polled; err != nil {
		t.Fatalf("the poll after the loss failed: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	n := asked
	mu.Unlock()
	if n < 2 {
		t.Fatalf("the lookup was made %d times: the heartbeat never asked, so the race below did not happen", n)
	}
	var names []string
	for ci := range jsConsumerNames(t, ctx, js, "AGENT_INBOX") {
		if ci == localDurableName(bus) {
			names = append(names, ci)
		}
	}
	if len(names) != 1 {
		t.Errorf("consumers named %s = %d, want 1", localDurableName(bus), len(names))
	}
	if rec := presenceRecord(t, ctx, bus); rec["rebuilds_ok"] != int64(1) {
		t.Errorf("rebuilds_ok = %v after one loss found by two paths, want 1: %v", rec["rebuilds_ok"], rec)
	}
}

// jsConsumerNames lists the stream's consumer names as a set.
func jsConsumerNames(t *testing.T, ctx context.Context, js jetstream.JetStream, stream string) map[string]struct{} {
	t.Helper()
	st, err := js.Stream(ctx, stream)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]struct{}{}
	l := st.ListConsumers(ctx)
	for ci := range l.Info() {
		out[ci.Name] = struct{}{}
	}
	if err := l.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
