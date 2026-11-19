package main

import (
	"context"
	"testing"
	"time"
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
	found, perr := earlierDurableProbe(ctx, g, self.AgentID, b.instance)
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
	found, perr := earlierDurableProbe(ctx, g, self.AgentID, b.instance)
	if perr != nil || !found {
		t.Errorf("probe = %v, %v; an earlier instance's durable must count", found, perr)
	}
}
