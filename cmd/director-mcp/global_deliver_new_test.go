package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// A seat's first global durable starts at the stream head (director#594).
// A seat that has an earlier durable, its own, its predecessor's or an earlier
// generation's, keeps resuming from that position.

func freshSeat(name string) Sender { return Sender{AgentID: name, Workspace: "ws1", Team: "ops"} }

// attach creates the seat's global durable now. The tier attaches on first
// use, so a test that publishes "after the seat started" attaches first.
func attach(t *testing.T, ctx context.Context, b *Bus) {
	t.Helper()
	if _, err := b.globalReady(ctx); err != nil {
		t.Fatal(err)
	}
}

func readIDs(t *testing.T, ctx context.Context, b *Bus) []string {
	t.Helper()
	res, err := b.receiveBatch(ctx, 3*time.Second, 10)
	if err != nil {
		t.Fatal(err)
	}
	return ids(res.Items)
}

func TestBrokerFreshSeatGlobalDurableStartsAtTheHead(t *testing.T) {
	useRealProbe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, _ := provision(t, ctx, url)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	gcfg := &globalConfig{Domain: "global", Cluster: "c1", Role: roleDirector}
	t.Setenv("MARVEL_PREDECESSOR", "")
	pubEnv(t, ctx, gjs, "global.director.inbox", "g0", "INFORM", "old, for another seat")
	pubEnv(t, ctx, gjs, "global.director.inbox", "g1", "INFORM", "old, for another seat")
	b, err := connect(ctx, url, freshSeat("fresh-g1-0"), gcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	attach(t, ctx, b)
	gst, err := gjs.Stream(ctx, globalDirectorStream)
	if err != nil {
		t.Fatal(err)
	}
	gsi, err := gst.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	head := gsi.State.LastSeq
	if ci, err := b.global.cons().Info(ctx); err != nil || ci.AckFloor.Stream != head {
		t.Fatalf("durable floor = %v (%v), want the head %d the note reports", ci, err, head)
	}
	pa, err := gjs.Publish(ctx, "global.director.inbox", mustEnv("g2"))
	if err != nil {
		t.Fatal(err)
	}
	if pa.Sequence <= head {
		t.Errorf("g2 landed at sequence %d, not above the head %d", pa.Sequence, head)
	}
	if got := readIDs(t, ctx, b); fmt.Sprint(got) != "[global:g2]" {
		t.Errorf("fresh seat read %v, want only [global:g2]", got)
	}
	note := strings.Join(b.takeResumed(), "\n")
	for _, s := range []string{"2 earlier message(s)", "not delivered", globalDirectorStream, "sequences 1 to 2"} {
		if !strings.Contains(note, s) {
			t.Errorf("start note %q lacks %q", note, s)
		}
	}
	if strings.Contains(note, "audit_lookup") {
		t.Errorf("start note points at audit_lookup, which cannot read the hub stream: %q", note)
	}
	if strings.Contains(note, "global inbox has no earlier durable for this seat, so it reads everything") {
		t.Errorf("start note still says the seat reads everything: %q", note)
	}
}

func TestBrokerSuccessorStillResumesFromThePredecessorFloor(t *testing.T) {
	useRealProbe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, _ := provision(t, ctx, url)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	gcfg := &globalConfig{Domain: "global", Cluster: "c1", Role: roleDirector}
	pred, succ := freshSeat("worker-g1-0"), freshSeat("worker-g2-0")
	t.Setenv("MARVEL_PREDECESSOR", "")
	b1, err := connect(ctx, url, pred, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	attach(t, ctx, b1)
	pubEnv(t, ctx, gjs, "global.director.inbox", "g0", "INFORM", "read by the predecessor")
	if r, err := b1.receiveTiered(ctx, 8*time.Second); err != nil || r.Env == nil || r.Env.MessageID != "g0" {
		t.Fatalf("predecessor read %+v %v", r, err)
	}
	b1.close()
	pubEnv(t, ctx, gjs, "global.director.inbox", "g1", "INFORM", "left for the successor")
	t.Setenv("MARVEL_PREDECESSOR", "ws1/"+pred.AgentID)
	b2, err := connect(ctx, url, succ, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b2.close()
	if got := readIDs(t, ctx, b2); fmt.Sprint(got) != "[global:g1]" {
		t.Errorf("successor read %v, want [global:g1]: the predecessor's floor, not the head", got)
	}
}

// A predecessor that left a durable which acked nothing still leaves unread
// mail for its successor.
func TestBrokerSuccessorOfADurableThatAckedNothingReplays(t *testing.T) {
	useRealProbe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, _ := provision(t, ctx, url)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	gcfg := &globalConfig{Domain: "global", Cluster: "c1", Role: roleDirector}
	pred, succ := freshSeat("worker-g1-0"), freshSeat("worker-g2-0")
	t.Setenv("MARVEL_PREDECESSOR", "")
	b1, err := connect(ctx, url, pred, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	attach(t, ctx, b1)
	pubEnv(t, ctx, gjs, "global.director.inbox", "g0", "INFORM", "never read by the predecessor")
	b1.close()
	t.Setenv("MARVEL_PREDECESSOR", "ws1/"+pred.AgentID)
	b2, err := connect(ctx, url, succ, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b2.close()
	if got := readIDs(t, ctx, b2); fmt.Sprint(got) != "[global:g0]" {
		t.Errorf("successor read %v, want [global:g0]: a durable with no ack is not a fresh seat", got)
	}
}

// When the seat's earlier durables cannot be looked up at all, the seat keeps
// the replay: a head start could drop a predecessor's unread mail.
func TestBrokerFailedLookupKeepsTheReplay(t *testing.T) {
	prev := earlierDurableProbe
	earlierDurableProbe = func(context.Context, *globalTier, string, string) (bool, error) {
		return false, fmt.Errorf("listing local durables failed (no responders)")
	}
	t.Cleanup(func() { earlierDurableProbe = prev })
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, _ := provision(t, ctx, url)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	gcfg := &globalConfig{Domain: "global", Cluster: "c1", Role: roleDirector}
	t.Setenv("MARVEL_PREDECESSOR", "")
	pubEnv(t, ctx, gjs, "global.director.inbox", "g0", "INFORM", "may be a predecessor's unread mail")
	b, err := connect(ctx, url, freshSeat("lookup-g1-0"), gcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	if got := readIDs(t, ctx, b); fmt.Sprint(got) != "[global:g0]" {
		t.Errorf("seat with a failed lookup read %v, want the replay [global:g0]", got)
	}
	if note := strings.Join(b.takeResumed(), "\n"); !strings.Contains(note, "could not read the seat's earlier position") {
		t.Errorf("start note %q does not name the failed lookup", note)
	}
}

// A credential that cannot count the earlier messages says UNKNOWN, not 0.
func TestBrokerFreshSeatNoteSaysUnknownWhenTheCountIsUnreadable(t *testing.T) {
	useRealProbe(t)
	shortFloorList(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, seat := startScratchServerWithSeat(t, "STREAM.INFO")
	nc, _ := provision(t, ctx, admin)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	gcfg := &globalConfig{Domain: "global", Cluster: "c1", Role: roleDirector}
	t.Setenv("MARVEL_PREDECESSOR", "")
	pubEnv(t, ctx, gjs, "global.director.inbox", "g0", "INFORM", "old")
	b, err := connect(ctx, seat, freshSeat("unknown-g1-0"), gcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	attach(t, ctx, b)
	pubEnv(t, ctx, gjs, "global.director.inbox", "g1", "INFORM", "new")
	if got := readIDs(t, ctx, b); fmt.Sprint(got) != "[global:g1]" {
		t.Errorf("read %v, want [global:g1]", got)
	}
	note := strings.Join(b.takeResumed(), "\n")
	if !strings.Contains(note, "UNKNOWN") || !strings.Contains(note, globalDirectorStream) || strings.Contains(note, "audit_lookup") || strings.Contains(note, " 0 earlier") || strings.Contains(note, "1 earlier") {
		t.Errorf("start note %q: want UNKNOWN and no count", note)
	}
}

// With the class-aware cue on, a fresh seat has no backlog from before it
// started, and mail sent after it started is beyond the cue's boundary.
func TestBrokerClassAwareCueOnAFreshSeatSeesNoOldBacklog(t *testing.T) {
	useRealProbe(t)
	t.Setenv("DIRECTOR_CUE_CLASS", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, _ := provision(t, ctx, url)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	gcfg := &globalConfig{Domain: "global", Cluster: "c1", Role: roleDirector}
	t.Setenv("MARVEL_PREDECESSOR", "")
	pubEnv(t, ctx, gjs, "global.director.inbox", "g0", "INFORM", "old")
	pubEnv(t, ctx, gjs, "global.director.inbox", "g1", "INFORM", "old")
	b, err := connect(ctx, url, freshSeat("cue-g1-0"), gcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	src := busCueSource{b: b}
	floors, err := src.floors(ctx)
	if err != nil {
		t.Fatal(err)
	}
	g, err := b.globalReady(ctx)
	if err != nil {
		t.Fatal(err)
	}
	items, _, _, err := peekWaiting(ctx, g.js, g.cons(), "global", cueWaitingLimit)
	if err != nil || len(items) != 0 {
		t.Fatalf("a fresh seat peeks %d waiting (%v), want none", len(items), err)
	}
	pubEnv(t, ctx, gjs, "global.director.inbox", "g2", "INFORM", "new")
	items, _, _, err = peekWaiting(ctx, g.js, g.cons(), "global", cueWaitingLimit)
	if err != nil || len(items) != 1 {
		t.Fatalf("after one send the seat peeks %d (%v), want 1", len(items), err)
	}
	if floors["global"] != 2 {
		t.Errorf("cue floor for global = %d, want at least the 2 old messages", floors["global"])
	}
}

// A fresh seat whose hub durable is lost and recreated resumes at its own head,
// not from the start of the stream: it had acked nothing, so the recreate
// would otherwise replay the history the head start skipped.
func TestBrokerFreshSeatRecreatedDurableStaysAtTheHead(t *testing.T) {
	useRealProbe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, _ := provision(t, ctx, url)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	gcfg := &globalConfig{Domain: "global", Cluster: "c1", Role: roleDirector}
	t.Setenv("MARVEL_PREDECESSOR", "")
	pubEnv(t, ctx, gjs, "global.director.inbox", "g0", "INFORM", "old")
	b, err := connect(ctx, url, freshSeat("lost-g1-0"), gcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	attach(t, ctx, b)
	g := b.global
	if err := gjs.DeleteConsumer(ctx, globalDirectorStream, globalDurable(b.self.AgentID, b.instance)); err != nil {
		t.Fatal(err)
	}
	pubEnv(t, ctx, gjs, "global.director.inbox", "g1", "INFORM", "sent while the durable was missing")
	if rebuilt, err := g.restore(ctx, b.self.AgentID, b.instance); err != nil || !rebuilt {
		t.Fatalf("restore = %v, %v; want a rebuild", rebuilt, err)
	}
	if got := readIDs(t, ctx, b); fmt.Sprint(got) != "[global:g1]" {
		t.Errorf("after the rebuild the seat read %v, want only [global:g1]", got)
	}
}

// mustEnv is the body pubEnv publishes for an INFORM with id.
func mustEnv(id string) []byte {
	e := testEnv(id, "sender-"+id, "INFORM", "sent after the seat started")
	e.Recipient.Address = "agent://ops/operator"
	b, _ := json.Marshal(e)
	return b
}

// A message that lands between the seat's count of earlier mail and the
// creation of its durable is delivered, not skipped: the count and the start
// describe the same point.
func TestBrokerMailBetweenTheCountAndTheStartIsDelivered(t *testing.T) {
	useRealProbe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, _ := provision(t, ctx, url)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	gcfg := &globalConfig{Domain: "global", Cluster: "c1", Role: roleDirector}
	t.Setenv("MARVEL_PREDECESSOR", "")
	pubEnv(t, ctx, gjs, "global.director.inbox", "g0", "INFORM", "old")
	prev := afterHeadRead
	afterHeadRead = func() {
		pubEnv(t, ctx, gjs, "global.director.inbox", "gmid", "INFORM", "published between the count and the start")
	}
	t.Cleanup(func() { afterHeadRead = prev })
	b, err := connect(ctx, url, freshSeat("race-g1-0"), gcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	attach(t, ctx, b)
	if got := readIDs(t, ctx, b); fmt.Sprint(got) != "[global:gmid]" {
		t.Errorf("seat read %v, want [global:gmid]: a message between the count and the start is not skipped", got)
	}
	note := strings.Join(b.takeResumed(), "\n")
	if !strings.Contains(note, "1 earlier message(s)") || !strings.Contains(note, "sequences 1 to 1") {
		t.Errorf("start note %q does not describe the same point the durable started at", note)
	}
}

// The note's range starts at the stream's first retained sequence, not at 1.
func TestBrokerFreshSeatNoteRangeStartsAtTheFirstRetainedSequence(t *testing.T) {
	useRealProbe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, _ := provision(t, ctx, url)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	gcfg := &globalConfig{Domain: "global", Cluster: "c1", Role: roleDirector}
	t.Setenv("MARVEL_PREDECESSOR", "")
	for i := 0; i < 3; i++ {
		pubEnv(t, ctx, gjs, "global.director.inbox", fmt.Sprintf("g%d", i), "INFORM", "old")
	}
	gst, err := gjs.Stream(ctx, globalDirectorStream)
	if err != nil {
		t.Fatal(err)
	}
	if err := gst.Purge(ctx, jetstream.WithPurgeSequence(3)); err != nil { // drops sequences 1 and 2
		t.Fatal(err)
	}
	b, err := connect(ctx, url, freshSeat("first-g1-0"), gcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	attach(t, ctx, b)
	note := strings.Join(b.takeResumed(), "\n")
	if !strings.Contains(note, "1 earlier message(s)") || !strings.Contains(note, "sequences 3 to 3") {
		t.Errorf("start note %q: want 1 message in sequences 3 to 3", note)
	}
}

// A fresh seat on an empty stream is told the stream held nothing, not that
// sequences 1 to 0 were skipped.
func TestBrokerFreshSeatNoteOnAnEmptyStream(t *testing.T) {
	useRealProbe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	provision(t, ctx, url)
	gcfg := &globalConfig{Domain: "global", Cluster: "c1", Role: roleDirector}
	t.Setenv("MARVEL_PREDECESSOR", "")
	b, err := connect(ctx, url, freshSeat("empty-g1-0"), gcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	attach(t, ctx, b)
	note := strings.Join(b.takeResumed(), "\n")
	if strings.Contains(note, "1 to 0") || !strings.Contains(note, "held no earlier messages") {
		t.Errorf("start note %q: want the stream described as holding no earlier messages", note)
	}
}
