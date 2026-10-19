package main

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// healthyGlobalBus attaches a director session to a scratch hub and leaf with
// the global tier on and nothing queued.
func healthyGlobalBus(t *testing.T, ctx context.Context) (*Bus, jetstream.JetStream) {
	t.Helper()
	url := startScratchServer(t)
	nc, _ := provision(t, ctx, url)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	self := Sender{AgentID: "operator", Workspace: "aae-orc", Team: "ops"}
	bus, err := connect(ctx, url, self, &globalConfig{Domain: "global", Cluster: "kinu", Role: roleDirector})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bus.close)
	return bus, gjs
}

// countedGlobal counts the hub lookups of the session's durable. With fail
// nil it asks the real hub; otherwise it answers fail.
func countedGlobal(t *testing.T, ctx context.Context, bus *Bus, fail error) *int {
	t.Helper()
	g, err := bus.globalReady(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	g.lookup = func(ctx context.Context, durable string) error {
		n++
		if fail != nil {
			return fail
		}
		_, err := g.js.Consumer(ctx, g.cfg.streamName(), durable)
		return err
	}
	return &n
}

func ageGlobalCheck(bus *Bus, by time.Duration) {
	bus.global.mu.Lock()
	bus.global.checked = time.Now().Add(-by)
	bus.global.mu.Unlock()
}

// A healthy empty poll asks the hub whether the durable exists at most once per
// durableCheckEvery, and not at all inside the window.
func TestGlobalEmptyPollAsksTheHubOncePerWindow(t *testing.T) {
	old := durableCheckEvery
	durableCheckEvery = time.Hour
	defer func() { durableCheckEvery = old }()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, _ := healthyGlobalBus(t, ctx)
	asks := countedGlobal(t, ctx, bus, nil)

	if _, err := bus.receiveTiered(ctx, time.Second); err != nil {
		t.Fatal(err)
	}
	if *asks != 0 {
		t.Fatalf("inside the window the hub was asked %d times, want 0", *asks)
	}
	ageGlobalCheck(bus, 2*time.Hour)
	if _, err := bus.receiveTiered(ctx, time.Second); err != nil {
		t.Fatal(err)
	}
	if *asks != 1 {
		t.Fatalf("when due the hub was asked %d times, want 1", *asks)
	}
	for i := 0; i < 3; i++ {
		if _, err := bus.receiveTiered(ctx, time.Second); err != nil {
			t.Fatal(err)
		}
	}
	if *asks != 1 {
		t.Fatalf("after the check the window should hold: %d asks, want 1", *asks)
	}
}

// Only a hub that says the durable is not found rebuilds it. Any other answer,
// such as a timeout, changes nothing.
func TestGlobalOnlyNotFoundRebuilds(t *testing.T) {
	old := durableCheckEvery
	durableCheckEvery = 0
	defer func() { durableCheckEvery = old }()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, gjs := healthyGlobalBus(t, ctx)
	durable := globalDurable("operator", bus.instance)
	if _, err := bus.globalReady(ctx); err != nil { // the durable exists once attached
		t.Fatal(err)
	}
	if err := gjs.DeleteConsumer(ctx, globalDirectorStream, durable); err != nil {
		t.Fatal(err)
	}
	asks := countedGlobal(t, ctx, bus, context.DeadlineExceeded)
	_, _ = bus.receiveTiered(ctx, time.Second)
	if *asks == 0 {
		t.Fatal("CONTROL FAILED: the lookup was never made, so the answer below proves nothing")
	}
	if _, err := gjs.Consumer(ctx, globalDirectorStream, durable); err == nil {
		t.Fatal("a lookup that failed with a timeout rebuilt the durable")
	}
	// control: the same state with an honest answer from the hub does rebuild.
	countedGlobal(t, ctx, bus, nil)
	if _, err := bus.receiveTiered(ctx, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := gjs.Consumer(ctx, globalDirectorStream, durable); err != nil {
		t.Fatalf("CONTROL FAILED: a not-found answer should rebuild: %v", err)
	}
}

// A lookup that fails still starts the window, so a hub that cannot answer is
// not asked again on every empty poll (the global twin of director#328 round 1).
func TestGlobalFailedLookupStartsTheWindow(t *testing.T) {
	old := durableCheckEvery
	durableCheckEvery = time.Hour
	defer func() { durableCheckEvery = old }()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, _ := healthyGlobalBus(t, ctx)
	asks := countedGlobal(t, ctx, bus, context.DeadlineExceeded)
	ageGlobalCheck(bus, 2*time.Hour)
	for i := 0; i < 3; i++ {
		if _, err := bus.receiveTiered(ctx, time.Second); err != nil {
			t.Fatal(err)
		}
	}
	if *asks != 1 {
		t.Fatalf("a failing lookup was made %d times over three empty polls, want 1 inside the window", *asks)
	}
}
