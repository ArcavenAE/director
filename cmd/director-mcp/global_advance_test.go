package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// global-advance moves a seat's own global durable forward by acking through
// its backlog (director#596). It touches no other consumer and no message.

func advanceRig(t *testing.T, ctx context.Context, url string, agent string) (*Bus, *globalConfig) {
	t.Helper()
	gcfg := &globalConfig{Domain: "global", Cluster: "c1", Role: roleDirector}
	t.Setenv("MARVEL_PREDECESSOR", "")
	b, err := connect(ctx, url, freshSeat(agent), gcfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.close)
	attach(t, ctx, b)
	if warn := b.writeGlobalPresence(ctx, "idle"); warn != "" {
		t.Fatal(warn)
	}
	return b, gcfg
}

func TestAdvanceSkipsABacklogAndDeliversOnlyLaterMail(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, _ := provision(t, ctx, url)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	// The suite's probe makes a seat replay, the shape of a seat started before
	// director#595: its durable holds the whole backlog.
	for i := 0; i < 5; i++ {
		pubEnv(t, ctx, gjs, "global.director.inbox", fmt.Sprintf("g%d", i), "INFORM", "old")
	}
	b, _ := advanceRig(t, ctx, url, "adv-g1-0")
	res, err := advanceGlobalDurable(ctx, b.global, b.self.AgentID, 0)
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	if res.Acked != 5 || res.AfterFloor != 5 || res.BeforeFloor != 0 {
		t.Errorf("result %+v, want 5 acked, floor 0 to 5", res)
	}
	out := formatAdvance(res)
	for _, s := range []string{globalDirectorStream, "sequences 1 to 5", "5 message(s)"} {
		if !strings.Contains(out, s) {
			t.Errorf("printout %q lacks %q", out, s)
		}
	}
	pubEnv(t, ctx, gjs, "global.director.inbox", "g5", "INFORM", "sent after the advance")
	if got := readIDs(t, ctx, b); fmt.Sprint(got) != "[global:g5]" {
		t.Errorf("seat read %v, want only [global:g5]", got)
	}
}

func TestAdvanceRefusals(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, _ := provision(t, ctx, url)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	pubEnv(t, ctx, gjs, "global.director.inbox", "g0", "INFORM", "old")
	pubEnv(t, ctx, gjs, "global.director.inbox", "g1", "INFORM", "old")
	b, _ := advanceRig(t, ctx, url, "refuse-g1-0")

	// No durable for that agent id on the stream.
	if _, err := advanceGlobalDurable(ctx, b.global, "nobody-g1-0", 0); err == nil || !strings.Contains(err.Error(), "no global durable") {
		t.Errorf("a seat with no durable: err = %v, want a refusal naming no global durable", err)
	}

	// A message delivered and not acked is a delivery in flight.
	cons := b.global.cons()
	batch, err := cons.Fetch(1, jetstream.FetchMaxWait(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for range batch.Messages() {
	}
	before, err := cons.Info(ctx)
	if err != nil || before.NumAckPending == 0 {
		t.Fatalf("setup: ack pending = %v (%v), want a delivery in flight", before, err)
	}
	if _, err := advanceGlobalDurable(ctx, b.global, b.self.AgentID, 0); err == nil || !strings.Contains(err.Error(), "in flight") {
		t.Errorf("with a delivery in flight: err = %v, want a refusal naming in flight", err)
	}
	after, _ := cons.Info(ctx)
	if after.AckFloor.Stream != before.AckFloor.Stream || after.NumPending != before.NumPending {
		t.Errorf("a refused advance changed the durable: before %+v after %+v", before.AckFloor, after.AckFloor)
	}
}

func TestAdvanceTouchesNoOtherConsumerAndNoMessage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, _ := provision(t, ctx, url)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	for i := 0; i < 4; i++ {
		pubEnv(t, ctx, gjs, "global.director.inbox", fmt.Sprintf("g%d", i), "INFORM", "old")
	}
	mine, _ := advanceRig(t, ctx, url, "mine-g1-0")
	other, _ := advanceRig(t, ctx, url, "other-g1-0")
	oc := other.global.cons()
	otherBefore, err := oc.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := gjs.Stream(ctx, globalDirectorStream)
	streamBefore, _ := st.Info(ctx)
	if _, err := advanceGlobalDurable(ctx, mine.global, mine.self.AgentID, 0); err != nil {
		t.Fatalf("advance: %v", err)
	}
	otherAfter, _ := oc.Info(ctx)
	if otherAfter.AckFloor.Stream != otherBefore.AckFloor.Stream || otherAfter.NumPending != otherBefore.NumPending || otherAfter.NumAckPending != otherBefore.NumAckPending {
		t.Errorf("another seat's durable moved: before %+v/%d after %+v/%d", otherBefore.AckFloor, otherBefore.NumPending, otherAfter.AckFloor, otherAfter.NumPending)
	}
	streamAfter, _ := st.Info(ctx)
	if streamAfter.State.Msgs != streamBefore.State.Msgs || streamAfter.State.FirstSeq != streamBefore.State.FirstSeq || streamAfter.State.LastSeq != streamBefore.State.LastSeq {
		t.Errorf("the stream changed: before %+v after %+v", streamBefore.State, streamAfter.State)
	}
}

func TestAdvanceToASequenceStopsThere(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, _ := provision(t, ctx, url)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	for i := 0; i < 5; i++ {
		pubEnv(t, ctx, gjs, "global.director.inbox", fmt.Sprintf("g%d", i), "INFORM", "old")
	}
	b, _ := advanceRig(t, ctx, url, "to-g1-0")
	res, err := advanceGlobalDurable(ctx, b.global, b.self.AgentID, 3)
	if err != nil {
		t.Fatalf("advance --to 3: %v", err)
	}
	if res.AfterFloor != 3 || res.Acked != 3 {
		t.Errorf("result %+v, want floor 3 and 3 acked", res)
	}
	if got := readIDs(t, ctx, b); fmt.Sprint(got) != "[global:g3 global:g4]" {
		t.Errorf("seat read %v, want [global:g3 global:g4]", got)
	}
}

// A credential that cannot read the stream's state still advances, and says
// UNKNOWN for the head, never 0.
func TestAdvancePrintsUnknownWhenTheHeadIsUnreadable(t *testing.T) {
	shortFloorList(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, seat := startScratchServerWithSeat(t, "STREAM.INFO")
	nc, _ := provision(t, ctx, admin)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	for i := 0; i < 3; i++ {
		pubEnv(t, ctx, gjs, "global.director.inbox", fmt.Sprintf("g%d", i), "INFORM", "old")
	}
	b, _ := advanceRig(t, ctx, seat, "unk-g1-0")
	res, err := advanceGlobalDurable(ctx, b.global, b.self.AgentID, 0)
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	if res.HeadKnown {
		t.Errorf("the head was read though stream info is denied: %+v", res)
	}
	if out := formatAdvance(res); !strings.Contains(out, "UNKNOWN") || !strings.Contains(out, "3 message(s)") {
		t.Errorf("printout %q: want UNKNOWN for the head and 3 messages acked", out)
	}
}

// The CLI refuses a --seat that is not the caller's own agent id.
func TestGlobalAdvanceRefusesAnotherSeat(t *testing.T) {
	cli, err := parseArgs([]string{"global-advance", "--seat", "someone-else-g1-0"})
	if err != nil {
		t.Fatal(err)
	}
	var out, errw strings.Builder
	t.Setenv("DIRECTOR_AGENT_ID", "mine-g1-0")
	if code := runGlobalAdvanceCmd(context.Background(), cli, &out, &errw); code == 0 || !strings.Contains(errw.String(), "not your own") {
		t.Errorf("code %d, stderr %q: want a refusal saying the seat is not your own", code, errw.String())
	}
}
