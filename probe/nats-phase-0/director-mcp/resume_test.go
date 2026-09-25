package main

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

var resumeSelf = Sender{AgentID: "michael", Workspace: "aae-orc", Team: "ops"}

const resumeInbox = "agent.aae-orc.ops.michael.inbox"

// A reconnect (a new instance of the same seat) resumes after the last message
// the seat acked, instead of replaying the whole inbox under DeliverAll.
func TestBrokerNewInstanceResumesAfterTheSeatsLastAck(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	pubEnv(t, ctx, js, resumeInbox, "old1", "INFORM", "yesterday")
	pubEnv(t, ctx, js, resumeInbox, "old2", "INFORM", "yesterday too")

	b1, err := connect(ctx, url, resumeSelf, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := b1.receiveBatch(ctx, 2*time.Second, 5)
	if err != nil || len(res.Items) != 2 {
		t.Fatalf("first instance read %v, %v", ids(res.Items), err)
	}
	b1.close()
	pubEnv(t, ctx, js, resumeInbox, "new1", "INFORM", "while reconnecting")

	b2, err := connect(ctx, url, resumeSelf, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b2.close()
	res, err = b2.receiveBatch(ctx, 2*time.Second, 5)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(res.Items); len(got) != 1 || got[0] != "local:new1" {
		t.Errorf("second instance read %v, want only [local:new1]", got)
	}
}

// A seat with no earlier durable still gets mail sent to its cold mailbox.
func TestBrokerFirstInstanceStillReadsItsColdMailbox(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	pubEnv(t, ctx, js, resumeInbox, "cold1", "INFORM", "sent before the seat started")
	b, err := connect(ctx, url, resumeSelf, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	e, _, err := b.receive(ctx, 2*time.Second)
	if err != nil || e == nil || e.MessageID != "cold1" {
		t.Fatalf("cold mailbox read %v, %v", e, err)
	}
}

// Another seat's durable, even with a similar name, sets no resume point.
func TestBrokerResumeIgnoresOtherSeats(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	other := Sender{AgentID: "michael-2", Workspace: "aae-orc", Team: "ops"}
	pubEnv(t, ctx, js, "agent.aae-orc.ops.michael-2.inbox", "o1", "INFORM", "for the other seat")
	pubEnv(t, ctx, js, resumeInbox, "m1", "INFORM", "mine, unread")
	ob, err := connect(ctx, url, other, nil)
	if err != nil {
		t.Fatal(err)
	}
	if e, _, err := ob.receive(ctx, 2*time.Second); err != nil || e == nil {
		t.Fatalf("other seat read %v, %v", e, err)
	}
	ob.close()
	b, err := connect(ctx, url, resumeSelf, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	e, _, err := b.receive(ctx, 2*time.Second)
	if err != nil || e == nil || e.MessageID != "m1" {
		t.Fatalf("want m1, got %v, %v", e, err)
	}
}

// Local durables carry an inactive threshold, so a superseded instance's
// durable is cleaned up instead of accumulating.
func TestBrokerLocalDurableHasAnInactiveThreshold(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	b, err := connect(ctx, url, resumeSelf, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	info, err := b.consumer.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Config.InactiveThreshold < 72*time.Hour {
		t.Errorf("local durable inactive threshold = %v, want above the inbox's 72h max age", info.Config.InactiveThreshold)
	}
	_ = js
}

// A local backlog must not starve the global tier: with both tiers holding
// mail, successive single polls alternate rather than draining local first.
func TestBrokerLocalBacklogDoesNotStarveGlobalPoll(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, js := provision(t, ctx, url)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	for _, id := range []string{"l1", "l2", "l3", "l4"} {
		pubEnv(t, ctx, js, resumeInbox, id, "INFORM", "backlog")
	}
	pubEnv(t, ctx, gjs, "global.director.inbox", "g1", "INFORM", "fresh")
	b, err := connect(ctx, url, resumeSelf, &globalConfig{Domain: "global", Cluster: "kinu", Role: roleDirector})
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	var got []string
	for i := 0; i < 2; i++ {
		res, err := b.receiveTiered(ctx, 3*time.Second)
		if err != nil || res.Env == nil {
			t.Fatalf("poll %d: %+v %v", i, res, err)
		}
		got = append(got, res.Tier+":"+res.Env.MessageID)
	}
	if got[0] != "global:g1" && got[1] != "global:g1" {
		t.Errorf("first two polls = %v; the global message waited behind the local backlog", got)
	}
}

// The same for a batch smaller than the local backlog.
func TestBrokerLocalBacklogDoesNotStarveGlobalBatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, js := provision(t, ctx, url)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	for _, id := range []string{"l1", "l2", "l3", "l4", "l5"} {
		pubEnv(t, ctx, js, resumeInbox, id, "INFORM", "backlog")
	}
	pubEnv(t, ctx, gjs, "global.director.inbox", "g1", "INFORM", "fresh")
	b, err := connect(ctx, url, resumeSelf, &globalConfig{Domain: "global", Cluster: "kinu", Role: roleDirector})
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	res, err := b.receiveBatch(ctx, 3*time.Second, 3)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range res.Items {
		if it.Tier == "global" {
			found = true
		}
	}
	if len(res.Items) != 3 || !found {
		t.Errorf("batch of 3 = %v; want 3 items including the global one", ids(res.Items))
	}
}
