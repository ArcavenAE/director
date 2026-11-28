package main

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// On a cluster whose credential cannot list the hub stream, the earlier-durable
// probe reads the local durable names instead (global_fresh.go). A seat holding a
// role has two local durables of its own, mcp_<agent>_<instance> and
// mcp_<agent>_<instance>-role, and both are created before the global probe runs.
// The probe compared the text after the last "_" with the instance, so the role
// durable's "<instance>-role" did not match, and the seat's own durable counted as
// an earlier one. A found durable means the new global durable does not start at
// the head, so every role-holding seat replayed the retained stream on its first
// start.
//
// The suite stubs earlierDurableProbe in main_test.go, so these tests call the real
// probe (realEarlierDurableProbe).

func TestProbeOwnRoleDurableIsNotEarlier(t *testing.T) {
	shortFloorList(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, seat := startScratchServerWithSeat(t)
	provision(t, ctx, admin)
	self := Sender{AgentID: "alpha-builder-g1-0", Workspace: "aae-orc", Team: "ops", Role: "builder"}
	gcfg := &globalConfig{Domain: "global", Cluster: "c1", Role: roleDirector}
	b, err := connect(ctx, seat, self, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	g, err := b.globalReady(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The seat's own local durables exist: SELF and SELF-role.
	names := jsConsumerNames(t, ctx, b.js, "AGENT_INBOX")
	for _, want := range []string{b.durableOf(false), b.durableOf(true)} {
		if _, ok := names[want]; !ok {
			t.Fatalf("local durable %s is missing from %v", want, names)
		}
	}
	found, perr := realEarlierDurableProbe(ctx, g, self.AgentID, b.instance)
	if perr != nil {
		t.Fatalf("probe error: %v", perr)
	}
	if found {
		t.Error("the probe counted this seat's own local durables as an earlier durable, so its start would not be at the head")
	}
}

// Control: an earlier instance's local durable still counts.
func TestProbeEarlierInstanceDurableIsStillEarlier(t *testing.T) {
	shortFloorList(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, seat := startScratchServerWithSeat(t)
	nc, _ := provision(t, ctx, admin)
	departedInstance(t, ctx, nc, "alpha-builder-g1-0", "01EARLIERINSTANCE", "global.director.inbox")
	self := Sender{AgentID: "alpha-builder-g1-0", Workspace: "aae-orc", Team: "ops", Role: "builder"}
	gcfg := &globalConfig{Domain: "global", Cluster: "c1", Role: roleDirector}
	b, err := connect(ctx, seat, self, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	g, err := b.globalReady(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found, perr := realEarlierDurableProbe(ctx, g, self.AgentID, b.instance)
	if perr != nil || !found {
		t.Errorf("probe = %v, %v; an earlier instance's durable must count", found, perr)
	}
}

// A seat with a predecessor also holds mcp_<agent>_<instance>-inherit while it
// reads the predecessor's mail. That durable is the seat's own too. The probe
// sees it on the local fallback, which runs because the seat user cannot list
// the hub stream.
func TestProbeOwnInheritDurableIsNotEarlier(t *testing.T) {
	shortFloorList(t)
	const pred = "alpha-builder-g1-0"
	t.Setenv("DIRECTOR_PREDECESSOR", pred)
	t.Setenv("MARVEL_PREDECESSOR", "")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, seat := startScratchServerWithSeat(t)
	provision(t, ctx, admin)
	self := Sender{AgentID: "alpha-builder-g2-0", Workspace: "aae-orc", Team: "ops", Role: "builder"}
	gcfg := &globalConfig{Domain: "global", Cluster: "c1", Role: roleDirector}
	b, err := connect(ctx, seat, self, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	g, err := b.globalReady(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.startInherit(ctx, pred); err != nil {
		t.Fatal(err)
	}
	names := jsConsumerNames(t, ctx, b.js, "AGENT_INBOX")
	if want := b.durableOf(false) + "-inherit"; !hasName(names, want) {
		t.Fatalf("local durable %s is missing from %v", want, names)
	}
	found, perr := realEarlierDurableProbe(ctx, g, self.AgentID, b.instance)
	if perr != nil {
		t.Fatalf("probe error: %v", perr)
	}
	if found {
		t.Error("the probe counted this seat's own -inherit durable as an earlier durable, so its start would not be at the head")
	}
}

func hasName(names map[string]struct{}, want string) bool {
	_, ok := names[want]
	return ok
}

// A role-holding seat's first global durable starts one past the stream head.
func TestRoleSeatFirstGlobalDurableStartsAtHeadPlusOne(t *testing.T) {
	useRealProbe(t)
	shortFloorList(t)
	t.Setenv("DIRECTOR_PREDECESSOR", "")
	t.Setenv("MARVEL_PREDECESSOR", "")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, seat := startScratchServerWithSeat(t)
	nc, _ := provision(t, ctx, admin)
	gjs, err := jetstream.NewWithDomain(nc, "global")
	if err != nil {
		t.Fatal(err)
	}
	pubEnv(t, ctx, gjs, "global.director.inbox", "g0", "INFORM", "old")
	pubEnv(t, ctx, gjs, "global.director.inbox", "g1", "INFORM", "old")
	self := Sender{AgentID: "alpha-builder-g1-0", Workspace: "aae-orc", Team: "ops", Role: "builder"}
	gcfg := &globalConfig{Domain: "global", Cluster: "c1", Role: roleDirector}
	b, err := connect(ctx, seat, self, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	g, err := b.globalReady(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cfg := g.cons().CachedInfo().Config
	if cfg.DeliverPolicy != jetstream.DeliverByStartSequencePolicy || cfg.OptStartSeq != 3 {
		t.Errorf("consumer starts %v at %d, want by start sequence at 3 (head 2 plus 1)", cfg.DeliverPolicy, cfg.OptStartSeq)
	}
}
