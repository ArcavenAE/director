package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The production shape of director#66: the shim on a leaf, its durable on the
// hub, the durable lost. Across the leaf link the pull on the missing durable
// is not answered at all, so no error reaches the shim to react to.
func TestLeafLostGlobalDurableIsRecreated(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	was := durableCheckEvery
	durableCheckEvery = time.Second
	defer func() { durableCheckEvery = was }()
	rig := startLeafRig(t)
	_, gjs := rig.provision(ctx)
	self := Sender{AgentID: "michael", Workspace: "aae-orc", Team: "ops"}
	bus, err := connect(ctx, rig.leafURL, self, &globalConfig{Domain: "global", Cluster: "kinu", Role: roleDirector})
	if err != nil {
		t.Fatal(err)
	}
	defer bus.close()
	pubEnv(t, ctx, gjs, "global.director.inbox", "g0", "INFORM", "before the loss")
	first, err := bus.receiveTiered(ctx, 8*time.Second)
	if err != nil || first.Env == nil {
		t.Fatalf("setup read: %+v %v", first, err)
	}
	if err := gjs.DeleteConsumer(ctx, globalDirectorStream, globalDurable("michael", bus.instance)); err != nil {
		t.Fatal(err)
	}
	pubEnv(t, ctx, gjs, "global.director.inbox", "g1", "INFORM", "after the loss")
	res, err := bus.receiveTiered(ctx, 20*time.Second)
	t.Logf("after loss: env=%v warn=%q err=%v", res.Env != nil, res.GlobalWarn, err)
	if res.Env == nil || res.Env.MessageID != "g1" {
		t.Fatalf("want g1 after the loss, got %+v", res)
	}
	if !strings.Contains(res.GlobalWarn, "recreated") {
		t.Errorf("the recreate must be reported, got %q", res.GlobalWarn)
	}
}

// A hub restart with its store intact keeps the durable; the shim must carry
// on receiving across it without a restart of its own.
func TestLeafHubRestartKeepsReceiving(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	rig := startLeafRig(t)
	_, gjs := rig.provision(ctx)
	self := Sender{AgentID: "michael", Workspace: "aae-orc", Team: "ops"}
	bus, err := connect(ctx, rig.leafURL, self, &globalConfig{Domain: "global", Cluster: "kinu", Role: roleDirector})
	if err != nil {
		t.Fatal(err)
	}
	defer bus.close()
	if _, err := bus.globalReady(ctx); err != nil {
		t.Fatal(err)
	}
	rig.restartHub()
	pubEnv(t, ctx, gjs, "global.director.inbox", "g1", "INFORM", "after the restart")
	res, err := bus.receiveTiered(ctx, 20*time.Second)
	t.Logf("after restart: env=%v warn=%q err=%v", res.Env != nil, res.GlobalWarn, err)
	if res.Env == nil || res.Env.MessageID != "g1" {
		t.Fatalf("want g1 after the hub restart, got %+v", res)
	}
}
