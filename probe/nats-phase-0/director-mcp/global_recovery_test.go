package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// A pull on a durable the hub no longer has fails with no responders (or
// consumer deleted mid-pull), never with ErrConsumerNotFound, so a rebuild
// keyed on ErrConsumerNotFound alone never runs. These cases must all be
// treated as a possible loss worth checking.
func TestPossiblyLostCoversWhatAPullReturns(t *testing.T) {
	for _, err := range []error{nats.ErrNoResponders, jetstream.ErrConsumerDeleted, jetstream.ErrConsumerNotFound} {
		if !possiblyLost(err) {
			t.Errorf("possiblyLost(%v) = false", err)
		}
	}
	for _, err := range []error{nil, jetstream.ErrNoMessages, nats.ErrTimeout, errors.New("other")} {
		if possiblyLost(err) {
			t.Errorf("possiblyLost(%v) = true", err)
		}
	}
}

// globalLossFixture attaches a director session, reads and acks one global
// message, then deletes the session's hub durable under it (what the live hub
// showed on 2026-09-25: no durable for live sessions) and publishes one more.
func globalLossFixture(t *testing.T, ctx context.Context) (*Bus, jetstream.JetStream) {
	t.Helper()
	url := startScratchServer(t)
	nc, _ := provision(t, ctx, url)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	self := Sender{AgentID: "michael", Workspace: "aae-orc", Team: "ops"}
	bus, err := connect(ctx, url, self, &globalConfig{Domain: "global", Cluster: "kinu", Role: roleDirector})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bus.close)
	pubEnv(t, ctx, gjs, "global.director.inbox", "g0", "INFORM", "before the loss")
	first, err := bus.receiveTiered(ctx, 8*time.Second)
	if err != nil || first.Env == nil || first.Env.MessageID != "g0" {
		t.Fatalf("setup read: %+v %v", first, err)
	}
	if err := gjs.DeleteConsumer(ctx, globalDirectorStream, globalDurable("michael", bus.instance)); err != nil {
		t.Fatal(err)
	}
	pubEnv(t, ctx, gjs, "global.director.inbox", "g1", "INFORM", "after the loss")
	return bus, gjs
}

func TestBrokerLostGlobalDurableIsRecreatedForThePoll(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, gjs := globalLossFixture(t, ctx)

	res, err := bus.receiveTiered(ctx, 12*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Env == nil || res.Env.MessageID != "g1" {
		t.Fatalf("want g1 after recreate (and no replay of acked g0), got %+v", res)
	}
	if !strings.Contains(res.GlobalWarn, "recreated") {
		t.Errorf("the recreate must be reported, got warning %q", res.GlobalWarn)
	}
	if _, err := gjs.Consumer(ctx, globalDirectorStream, globalDurable("michael", bus.instance)); err != nil {
		t.Errorf("durable not back on the hub: %v", err)
	}
	// The notice is reported once, not on every later poll.
	again, _ := bus.receiveTiered(ctx, 2*time.Second)
	if strings.Contains(again.GlobalWarn, "recreated") {
		t.Errorf("notice repeated: %q", again.GlobalWarn)
	}
}

func TestBrokerLostGlobalDurableIsRecreatedForTheBatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, _ := globalLossFixture(t, ctx)

	res, err := bus.receiveBatch(ctx, 8*time.Second, 5)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(res.Items); len(got) != 1 || got[0] != "global:g1" {
		t.Fatalf("batch after loss = %v, want [global:g1]", got)
	}
	if !strings.Contains(res.GlobalWarn, "recreated") {
		t.Errorf("the recreate must be reported, got warning %q", res.GlobalWarn)
	}
}

func TestBrokerLostGlobalDurableIsRecreatedForTheSummary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, _ := globalLossFixture(t, ctx)

	res, err := bus.summarizeInbox(ctx, summaryDefault)
	if err != nil {
		t.Fatal(err)
	}
	if res.Read["global"] != 1 {
		t.Errorf("summary read %d global, want 1 (g1 only)", res.Read["global"])
	}
	if !strings.Contains(res.GlobalWarn, "recreated") {
		t.Errorf("the recreate must be reported, got warning %q", res.GlobalWarn)
	}
}
