package main

import (
	"context"
	"fmt"
	"strings"
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

// A session joining a LIVE seat does not take its sibling's ack floor: under
// R-50 as implemented every live session of a seat gets its own copy, so the
// joiner reads the history the sibling already read, and both get new mail.
// Only a departed instance's floor is a resume point.
func TestBrokerJoiningALiveSeatGetsItsOwnCopy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	pubEnv(t, ctx, js, resumeInbox, "old1", "INFORM", "read by the first")
	b1, err := connect(ctx, url, resumeSelf, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b1.close()
	if err := b1.writePresence(ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	if e, _, err := b1.receive(ctx, 2*time.Second); err != nil || e == nil {
		t.Fatalf("first read %v %v", e, err)
	}
	b2, err := connect(ctx, url, resumeSelf, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b2.close()
	pubEnv(t, ctx, js, resumeInbox, "new1", "INFORM", "for both")
	res, err := b2.receiveBatch(ctx, 2*time.Second, 5)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(res.Items); fmt.Sprint(got) != fmt.Sprint([]string{"local:old1", "local:new1"}) {
		t.Errorf("joining session read %v, want [local:old1 local:new1]", got)
	}
	if e, _, err := b1.receive(ctx, 2*time.Second); err != nil || e == nil || e.MessageID != "new1" {
		t.Errorf("live sibling got %v, %v; want new1", e, err)
	}
}

// The same rule on the global tier: a live sibling's position is not taken.
func TestBrokerJoiningALiveGlobalSeatGetsItsOwnCopy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, _ := provision(t, ctx, url)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	gcfg := &globalConfig{Domain: "global", Cluster: "kinu", Role: roleDirector}
	pubEnv(t, ctx, gjs, "global.director.inbox", "g0", "INFORM", "read by the first")
	b1, err := connect(ctx, url, resumeSelf, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b1.close()
	if r, err := b1.receiveTiered(ctx, 8*time.Second); err != nil || r.Env == nil || r.Env.MessageID != "g0" {
		t.Fatalf("first read %+v %v", r, err)
	}
	if warn := b1.writeGlobalPresence(ctx, "idle"); warn != "" {
		t.Fatal(warn)
	}
	b2, err := connect(ctx, url, resumeSelf, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b2.close()
	res, err := b2.receiveBatch(ctx, 3*time.Second, 5)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(res.Items); fmt.Sprint(got) != fmt.Sprint([]string{"global:g0"}) {
		t.Errorf("joining session read %v, want [global:g0]", got)
	}
}

// The global durable resumes the same way on a reconnect.
func TestBrokerGlobalReconnectResumesAfterTheSeatsLastAck(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, _ := provision(t, ctx, url)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	gcfg := &globalConfig{Domain: "global", Cluster: "kinu", Role: roleDirector}
	pubEnv(t, ctx, gjs, "global.director.inbox", "g0", "INFORM", "read by the first")
	b1, err := connect(ctx, url, resumeSelf, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := b1.receiveTiered(ctx, 8*time.Second); err != nil || r.Env == nil || r.Env.MessageID != "g0" {
		t.Fatalf("first read %+v %v", r, err)
	}
	b1.close()
	pubEnv(t, ctx, gjs, "global.director.inbox", "g1", "INFORM", "while reconnecting")
	b2, err := connect(ctx, url, resumeSelf, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b2.close()
	res, err := b2.receiveBatch(ctx, 3*time.Second, 5)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(res.Items); len(got) != 1 || got[0] != "global:g1" {
		t.Errorf("second instance read %v, want only [global:g1]", got)
	}
}

// A session that resumed from the seat floor and has read nothing itself
// still knows that floor when the hub loses its durable: the recreate resumes
// after the seat's last ack, not from the start of the stream (director#82
// recovery and the #84 resume rule together).
func TestBrokerRecreatedGlobalDurableKeepsTheSeatFloor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, _ := provision(t, ctx, url)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	gcfg := &globalConfig{Domain: "global", Cluster: "kinu", Role: roleDirector}
	pubEnv(t, ctx, gjs, "global.director.inbox", "g0", "INFORM", "read by the first")
	b1, err := connect(ctx, url, resumeSelf, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := b1.receiveTiered(ctx, 8*time.Second); err != nil || r.Env == nil || r.Env.MessageID != "g0" {
		t.Fatalf("first read %+v %v", r, err)
	}
	b1.close()
	b2, err := connect(ctx, url, resumeSelf, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b2.close()
	if _, err := b2.globalReady(ctx); err != nil {
		t.Fatal(err)
	}
	if err := gjs.DeleteConsumer(ctx, globalDirectorStream, globalDurable(resumeSelf.AgentID, b2.instance)); err != nil {
		t.Fatal(err)
	}
	pubEnv(t, ctx, gjs, "global.director.inbox", "g1", "INFORM", "after the loss")
	res, err := b2.receiveBatch(ctx, 8*time.Second, 5)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(res.Items); len(got) != 1 || got[0] != "global:g1" {
		t.Errorf("recreated durable read %v, want only [global:g1] (g0 was acked by the seat)", got)
	}
	if !strings.Contains(res.GlobalWarn, "recreated") {
		t.Errorf("the recreate must be reported, got warning %q", res.GlobalWarn)
	}
}

// The first wait after a start says where the inbox resumed and what waits,
// once.
func TestBrokerResumeIsReportedOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	pubEnv(t, ctx, js, resumeInbox, "old1", "INFORM", "read")
	b1, err := connect(ctx, url, resumeSelf, nil)
	if err != nil {
		t.Fatal(err)
	}
	if e, _, _ := b1.receive(ctx, 2*time.Second); e == nil {
		t.Fatal("setup read")
	}
	b1.close()
	pubEnv(t, ctx, js, resumeInbox, "new1", "INFORM", "waiting")
	b2, err := connect(ctx, url, resumeSelf, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b2.close()
	first, err := toolWait(ctx, b2, []byte(`{"timeout_seconds":2}`))
	if err != nil {
		t.Fatal(err)
	}
	notes, _ := first.(map[string]any)["resumed"].([]string)
	if len(notes) != 1 || !strings.Contains(notes[0], "after stream sequence 1") || !strings.Contains(notes[0], "1 message(s) waiting") {
		t.Errorf("first wait resumed = %v", notes)
	}
	second, _ := toolWait(ctx, b2, []byte(`{"timeout_seconds":1}`))
	if _, ok := second.(map[string]any)["resumed"]; ok {
		t.Errorf("resume reported twice")
	}
}
