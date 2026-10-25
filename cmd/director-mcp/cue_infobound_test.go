package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// silentInfoConsumer is a tier consumer whose Info never answers, the way a
// denied CONSUMER.INFO or a hub past the bound looks to the caller: it returns
// only when its context ends.
type silentInfoConsumer struct{ jetstream.Consumer }

func (silentInfoConsumer) Info(ctx context.Context) (*jetstream.ConsumerInfo, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// countWithin runs one cue tick's read and reports how long it took, giving up
// at give so a read that hangs fails the test instead of the test's deadline.
func countWithin(t *testing.T, ctx context.Context, bus *Bus, give time.Duration) time.Duration {
	t.Helper()
	done := make(chan struct{})
	start := time.Now()
	go func() {
		_, _ = (busCueSource{bus}).count(ctx)
		close(done)
	}()
	select {
	case <-done:
		return time.Since(start)
	case <-time.After(give):
		t.Fatalf("the tier read still had not returned after %s", give)
		return 0
	}
}

// The 500 ms bound covers the whole tier read, the consumer Info call included
// (operator ruling 2026-10-24, option 1). A consumer Info that never answers
// must end at the bound and mark the tier unverified, keep its age, and the
// row must read unverified, never stalled. Local tier.
func TestASilentLocalConsumerInfoIsBoundedAndUnverified(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	bus := roleBus(t, ctx, url, "info-seat", "")
	r := newCueRig(t)
	r.started()
	r.c.ack("nonce1")
	bus.attachCue(r.c)
	bus.pending.mark("local", pendingSample{at: time.Now().Add(-800 * time.Second)})
	pubEnv(t, ctx, js, "agent.aae-orc.ops.info-seat.inbox", "m-l", "INFORM", "l")
	if _, err := (busCueSource{bus}).count(ctx); err != nil { // a good sample first
		t.Fatal(err)
	}
	bus.localMu.Lock()
	bus.consumer = silentInfoConsumer{bus.consumer}
	bus.localMu.Unlock()
	if el := countWithin(t, ctx, bus, 3*time.Second); el > 1500*time.Millisecond {
		t.Fatalf("a silent consumer Info cost the sample %s", el)
	}
	assertUnverifiedKept(t, ctx, bus, "local")
}

// The same on the global tier.
func TestASilentGlobalConsumerInfoIsBoundedAndUnverified(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, _ := startScratchServerWithSeat(t)
	nc, _ := provision(t, ctx, admin)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	bus, err := connect(ctx, admin, Sender{AgentID: "operator", Workspace: "aae-orc", Team: "ops"}, &globalConfig{Domain: "global", Cluster: "kinu", Role: roleDirector})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bus.close)
	g, err := bus.globalReady(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r := newCueRig(t)
	r.started()
	r.c.ack("nonce1")
	bus.attachCue(r.c)
	bus.pending.mark("global", pendingSample{at: time.Now().Add(-800 * time.Second)})
	pubEnv(t, ctx, gjs, g.cfg.inboxSubject(), "m-g", "INFORM", "g")
	if _, err := (busCueSource{bus}).count(ctx); err != nil { // a good sample first
		t.Fatal(err)
	}
	g.setConsumer(silentInfoConsumer{g.cons()})
	if el := countWithin(t, ctx, bus, 3*time.Second); el > 1500*time.Millisecond {
		t.Fatalf("a silent consumer Info cost the sample %s", el)
	}
	assertUnverifiedKept(t, ctx, bus, "global")
}

func assertUnverifiedKept(t *testing.T, ctx context.Context, bus *Bus, tier string) {
	t.Helper()
	why := bus.pending.unverified()
	if !strings.HasPrefix(why, "pending age unverified on the "+tier+" tier: ") {
		t.Fatalf("want the %s tier unverified, got %q", tier, why)
	}
	if got := bus.oldestPending(); got < 800*time.Second {
		t.Fatalf("the age is kept, never 0: got %s", got)
	}
	if err := bus.writePresence(ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	row := presenceRow(t, ctx, bus)
	reason, _ := row["cue_reason"].(string)
	if row["cue"] != cueStateUnver || !strings.Contains(reason, why) || strings.Contains(reason, "stalled") {
		t.Fatalf("the row reads unverified with the reason, never stalled: %v", row)
	}
}
