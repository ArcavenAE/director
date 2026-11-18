package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// director#220, plan T-A2b (asks-die-unread D1, design 3 and 3a). A seat that
// holds a role reads two local durables, `mcp_<self>_<instance>` for its own
// instance subject and `mcp_<self>_<instance>-role` for the role subject, as one
// source. The two subjects get different starts: the own subject resumes after
// the seat's own departed floor, the role subject after the highest floor any
// holder of the role has acked. These run on a scratch nats-server. The cases
// marked "pin" hold today with one combined durable and must keep holding on two:
// each is a site that reads the local consumer.

// seatDurables lists a session's own durables by name with their filter set and
// start sequence.
func seatDurables(t *testing.T, ctx context.Context, js jetstream.JetStream, id, instance string) map[string]*jetstream.ConsumerInfo {
	t.Helper()
	st, err := js.Stream(ctx, "AGENT_INBOX")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*jetstream.ConsumerInfo{}
	l := st.ListConsumers(ctx)
	for ci := range l.Info() {
		if strings.HasPrefix(ci.Name, "mcp_"+id+"_"+instance) {
			out[ci.Name] = ci
		}
	}
	if err := l.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// Test 3e, the first restart onto split durables. A seat whose departed durable
// is a combined [instance, role] acked through 200 restarts: its own durable and
// its -role durable both start at 201, and nothing at or below 200 returns.
func TestARestartOntoSplitDurablesStartsBothAfterTheCombinedFloor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	old := lineConnect(t, ctx, url, "sup", "supervisor", "")
	publishN(t, ctx, js, lineInbox("sup"), "o", 1, 150)
	publishN(t, ctx, js, lineRole("supervisor"), "r", 1, 50)
	lineReadAck(t, ctx, old, 200)
	publishN(t, ctx, js, lineInbox("sup"), "o", 151, 152)
	publishN(t, ctx, js, lineRole("supervisor"), "r", 51, 51)
	lineExit(t, ctx, old)

	fresh := lineConnect(t, ctx, url, "sup", "supervisor", "")
	starts := map[string]uint64{}
	for name, ci := range seatDurables(t, ctx, js, "sup", fresh.instance) {
		starts[name] = ci.Config.OptStartSeq
	}
	if len(starts) != 2 {
		t.Fatalf("the restarted seat holds %d durables %v, want its own and its -role durable", len(starts), starts)
	}
	for name, s := range starts {
		if s != 201 {
			t.Errorf("%s starts at %d, want 201", name, s)
		}
	}
	res, err := fresh.receiveBatch(ctx, 3*time.Second, 50)
	if err != nil {
		t.Fatal(err)
	}
	got := lineIDs(lineItems(t, res))
	for _, want := range []string{"o151", "o152", "r51"} {
		if !strings.Contains(got, want) {
			t.Errorf("restarted seat did not receive %s: got %q", want, got)
		}
	}
	if len(res.Items) != 3 {
		t.Errorf("restarted seat received %d messages (%q), want exactly 3 past the floor", len(res.Items), got)
	}
}

// A role holder reads two durables, one per subject; a seat with no role reads
// one. Each carries the inactive threshold, so an abandoned one is cleaned up.
func TestARoleHolderHoldsOneDurablePerSubject(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	holder := lineConnect(t, ctx, url, "sup", "supervisor", "")
	plain := lineConnect(t, ctx, url, "plain", "", "")

	// control: a seat with no role holds exactly one durable, on its own subject.
	pd := seatDurables(t, ctx, js, "plain", plain.instance)
	if len(pd) != 1 {
		t.Fatalf("CONTROL FAILED: a seat with no role holds %d durables, want 1", len(pd))
	}

	hd := seatDurables(t, ctx, js, "sup", holder.instance)
	if len(hd) != 2 {
		t.Fatalf("a role holder holds %d durables %v, want 2", len(hd), keys(hd))
	}
	for name, ci := range hd {
		subs := filterSubjects(ci.Config)
		switch {
		case strings.HasSuffix(name, "-role"):
			if len(subs) != 1 || subs[0] != lineRole("supervisor") {
				t.Errorf("%s filters %v, want only the role subject", name, subs)
			}
		default:
			if len(subs) != 1 || subs[0] != lineInbox("sup") {
				t.Errorf("%s filters %v, want only the seat's own subject", name, subs)
			}
		}
		if ci.Config.InactiveThreshold != localConsumerInactive {
			t.Errorf("%s inactive threshold = %v, want %v", name, ci.Config.InactiveThreshold, localConsumerInactive)
		}
	}
}

func keys(m map[string]*jetstream.ConsumerInfo) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// The role durable is rebuilt like the own one when the broker loses it, and
// resumes after what it acked, so role mail is neither lost nor replayed.
func TestADeletedRoleDurableIsRebuiltAndResumesAfterItsAcks(t *testing.T) {
	old := durableCheckEvery
	durableCheckEvery = 0
	defer func() { durableCheckEvery = old }()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	holder := lineConnect(t, ctx, url, "sup", "supervisor", "")
	publishN(t, ctx, js, lineRole("supervisor"), "r", 1, 2)
	lineReadAck(t, ctx, holder, 2)
	roleName := "mcp_sup_" + holder.instance + "-role"
	if _, ok := seatDurables(t, ctx, js, "sup", holder.instance)[roleName]; !ok {
		t.Fatalf("the holder has no %s durable to lose", roleName)
	}
	if err := js.DeleteConsumer(ctx, "AGENT_INBOX", roleName); err != nil {
		t.Fatal(err)
	}
	publishN(t, ctx, js, lineRole("supervisor"), "r", 3, 3)
	res, err := holder.receiveBatch(ctx, 5*time.Second, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := lineIDs(lineItems(t, res)); got != "r3" {
		t.Errorf("after the role durable was rebuilt the holder received %q, want only r3", got)
	}
	if _, ok := seatDurables(t, ctx, js, "sup", holder.instance)[roleName]; !ok {
		t.Error("the role durable was not rebuilt")
	}
}

// Pin: role mail waiting counts as waiting in every read that sums the local
// consumers. Each of these passes on one combined durable and has to pass on two.
func TestRoleMailWaitingIsCountedByEveryLocalRead(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	holder := lineConnect(t, ctx, url, "sup", "supervisor", "")
	publishN(t, ctx, js, lineInbox("sup"), "o", 1, 2)
	publishN(t, ctx, js, lineRole("supervisor"), "r", 1, 3)

	if rem := holder.remaining(ctx); rem["local"] != 5 {
		t.Errorf("remaining local = %d, want 5 (2 own and 3 role): %v", rem["local"], rem)
	}
	if n, err := holder.pendingByTier(ctx); err != nil || n != 5 {
		t.Errorf("pendingByTier = %d (err %v), want 5", n, err)
	}
	sum, err := holder.summarizeInbox(ctx, summaryDefault)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Read["local"] != 5 || sum.Expected["local"] != 5 {
		t.Errorf("summary read %v expected %v, want local 5", sum.Read, sum.Expected)
	}
	res, err := holder.receiveBatch(ctx, 3*time.Second, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 5 {
		t.Errorf("batch received %d (%q), want 5", len(res.Items), lineIDs(lineItems(t, res)))
	}
	if rem := holder.remaining(ctx); rem["local"] != 0 {
		t.Errorf("remaining local after the drain = %d, want 0: %v", rem["local"], rem)
	}
}

// roleStartFixture publishes 40 role messages, has a holder ack through the 37th,
// and publishes 2 messages to the new seat's own subject. It returns the holder.
func roleStartFixture(t *testing.T, ctx context.Context, url string, js jetstream.JetStream) *Bus {
	t.Helper()
	holder := lineConnect(t, ctx, url, "sup-a", "supervisor", "")
	publishN(t, ctx, js, lineRole("supervisor"), "r", 1, 40)
	lineReadAck(t, ctx, holder, 37)
	publishN(t, ctx, js, lineInbox("sup-b"), "s", 1, 2)
	return holder
}

func idSet(items []lineItem) map[string]bool {
	out := map[string]bool{}
	for _, i := range items {
		out[i.ID] = true
	}
	return out
}

// Design test 4a: a new id with no departed durable and no lineage connects
// holding a role. The stream holds 40 role messages, a live holder acked through
// the 37th, and 2 messages sit on the new id's own subject. It receives its 2 and
// role messages 38 to 40, not 1 to 37: mail some holder acked is not replayed.
func TestANewRoleHolderStartsItsRoleSubjectAfterTheHoldersFloor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	roleStartFixture(t, ctx, url, js)

	fresh := lineConnect(t, ctx, url, "sup-b", "supervisor", "")
	res, err := fresh.receiveBatch(ctx, 3*time.Second, 100)
	if err != nil {
		t.Fatal(err)
	}
	got := idSet(lineItems(t, res))
	for _, want := range []string{"s1", "s2", "r38", "r39", "r40"} {
		if !got[want] {
			t.Errorf("the new holder did not receive %s", want)
		}
	}
	if len(res.Items) != 5 {
		t.Errorf("the new holder received %d messages, want exactly 5 (its 2 and role 38 to 40): replayed role mail below the floor", len(res.Items))
	}
}

// Design test 4a, repeated with the holder departed and its durable still stored.
func TestANewRoleHolderStartsAfterADepartedHoldersFloor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	holder := roleStartFixture(t, ctx, url, js)
	lineExit(t, ctx, holder)

	fresh := lineConnect(t, ctx, url, "sup-b", "supervisor", "")
	res, err := fresh.receiveBatch(ctx, 3*time.Second, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 5 {
		t.Errorf("the new holder received %d messages (%q), want exactly 5 (its 2 and role 38 to 40)", len(res.Items), lineIDs(lineItems(t, res)))
	}
}

// Design test 4b, a pin: with no durable that ever filtered the role subject, the
// first holder reads every role message the stream holds.
func TestTheFirstRoleHolderReadsAllRoleMail(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	publishN(t, ctx, js, lineRole("supervisor"), "r", 1, 40)
	publishN(t, ctx, js, lineInbox("sup-b"), "s", 1, 2)

	first := lineConnect(t, ctx, url, "sup-b", "supervisor", "")
	res, err := first.receiveBatch(ctx, 3*time.Second, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 42 {
		t.Errorf("the first holder received %d messages, want all 42", len(res.Items))
	}
}

// A pin: the cue's count and oldest-message reads see role-subject mail on a role
// holder. They read the local consumer today and must read both of a split
// holder's. The cue's lane (laneOpen) lists replies to asks the seat has open, so a
// bare role REQUEST does not show in it and it is not pinned here.
func TestTheCuesReadsSeeRoleSubjectMail(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	holder := lineConnect(t, ctx, url, "sup", "supervisor", "")
	pubEnv(t, ctx, js, lineRole("supervisor"), "rq1", "REQUEST", "to the role")

	src := busCueSource{b: holder}
	if n, err := src.count(ctx); err != nil || n != 1 {
		t.Errorf("the cue's count = %d (err %v), want 1 for one role message waiting", n, err)
	}
	meta, err := src.oldestMeta(ctx)
	if err != nil || meta == nil || meta.MessageID != "rq1" {
		t.Errorf("the cue's oldest = %+v (err %v), want the role message rq1", meta, err)
	}
}

// director#677 limit, closed by the one consumer list: inherited mail is counted
// by the cue's pending count (pendingTiers), as it is by remaining and the
// summary, so an idle successor is rung for it.
func TestTheCuesPendingCountIncludesInheritedMail(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	pred := lineConnect(t, ctx, url, "pred", "", "")
	publishN(t, ctx, js, lineInbox("pred"), "p", 1, 3)
	lineExit(t, ctx, pred)

	succ := lineConnect(t, ctx, url, "succ", "", "pred")
	// control: before the inheritance starts nothing of the predecessor's counts.
	if n, err := (busCueSource{b: succ}).count(ctx); err != nil || n != 0 {
		t.Fatalf("CONTROL FAILED: the cue's count before the inheritance = %d (err %v), want 0", n, err)
	}
	runHeartbeat(t, ctx, succ, 40*time.Millisecond)
	if !waitFor(5*time.Second, func() bool { return len(inheritDurables(t, ctx, js, "succ")) == 1 }) {
		t.Fatal("nothing inherited")
	}
	if rem := succ.remaining(ctx); rem["inherited"] != 3 {
		t.Fatalf("CONTROL FAILED: remaining counts %v, want inherited 3", rem)
	}
	if n, err := (busCueSource{b: succ}).count(ctx); err != nil || n != 3 {
		t.Errorf("the cue's count = %d (err %v), want 3 inherited messages waiting", n, err)
	}
	if n, err := succ.pendingByTier(ctx); err != nil || n != 3 {
		t.Errorf("pendingByTier = %d (err %v), want 3", n, err)
	}
	meta, err := (busCueSource{b: succ}).oldestMeta(ctx)
	if err != nil || meta == nil {
		t.Errorf("the cue's oldest = %+v (err %v), want the oldest inherited message", meta, err)
	}
}
