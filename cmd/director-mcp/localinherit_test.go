package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// director#220, plan T-A2 (asks-die-unread D1, section 7 tests 1 to 3e). A
// successor reads the unread local mail its predecessor left, marked
// inherited_from, once the predecessor is gone. These run on a scratch
// nats-server. They set both DIRECTOR_PREDECESSOR and MARVEL_PREDECESSOR so
// they hold whichever variable a tier reads; the one helper is tested apart.

const (
	lineWS   = "aae-orc"
	lineTeam = "ops"
)

func lineInbox(id string) string { return "agent." + lineWS + "." + lineTeam + "." + id + ".inbox" }
func lineRole(role string) string {
	return "agent." + lineWS + "." + lineTeam + ".role." + role + ".inbox"
}

// lineConnect attaches a session. pred, when set, is the lineage input.
func lineConnect(t *testing.T, ctx context.Context, url, id, role, pred string) *Bus {
	t.Helper()
	t.Setenv("DIRECTOR_STATE", t.TempDir())
	t.Setenv("DIRECTOR_HANDLED", "")
	t.Setenv("DIRECTOR_PREDECESSOR", "")
	t.Setenv("MARVEL_PREDECESSOR", "")
	if pred != "" {
		t.Setenv("DIRECTOR_PREDECESSOR", pred)
		t.Setenv("MARVEL_PREDECESSOR", lineWS+"/"+pred)
	}
	b, err := connect(ctx, url, Sender{AgentID: id, Role: role, Workspace: lineWS, Team: lineTeam}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.close)
	if err := b.writePresence(ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	b.takeResumed()
	return b
}

// lineExit is a session leaving: its presence row goes and its connection
// closes. Its durable stays, as it does when a shim exits.
func lineExit(t *testing.T, ctx context.Context, b *Bus) {
	t.Helper()
	if err := b.kv.Delete(ctx, "presence."+b.self.Team+"."+b.self.AgentID+"."+b.instance); err != nil {
		t.Fatal(err)
	}
	b.close()
}

// lineReadAck reads and acks n messages the session's durable holds.
func lineReadAck(t *testing.T, ctx context.Context, b *Bus, n int) {
	t.Helper()
	for got := 0; got < n; {
		res, err := b.receiveBatch(ctx, 5*time.Second, n-got)
		if err != nil || len(res.Items) == 0 {
			t.Fatalf("setup read: got %d of %d: %v", got, n, err)
		}
		got += len(res.Items)
	}
}

// lineItem is one received message as the tool result carries it.
type lineItem struct {
	ID            string
	InheritedFrom string
}

func lineItems(t *testing.T, res batchResult) []lineItem {
	t.Helper()
	var out []lineItem
	for _, it := range res.Items {
		raw, err := json.Marshal(it)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		from, _ := m["inherited_from"].(string)
		out = append(out, lineItem{ID: it.Env.MessageID, InheritedFrom: from})
	}
	return out
}

func lineIDs(items []lineItem) string {
	var s []string
	for _, i := range items {
		s = append(s, i.ID)
	}
	return strings.Join(s, ",")
}

// inheritDurables lists the inherited durables a session holds on the inbox.
func inheritDurables(t *testing.T, ctx context.Context, js jetstream.JetStream, id string) []string {
	t.Helper()
	st, err := js.Stream(ctx, "AGENT_INBOX")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	l := st.ListConsumers(ctx)
	for ci := range l.Info() {
		if strings.HasPrefix(ci.Name, "mcp_"+id+"_") && strings.HasSuffix(ci.Name, "-inherit") {
			out = append(out, ci.Name)
		}
	}
	if err := l.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

// publishN publishes ids <prefix>1..<prefix>n to a subject.
func publishN(t *testing.T, ctx context.Context, js jetstream.JetStream, subject, prefix string, from, to int) {
	t.Helper()
	for i := from; i <= to; i++ {
		pubEnv(t, ctx, js, subject, prefix+strconv.Itoa(i), "INFORM", "mail "+strconv.Itoa(i))
	}
}

// Test 1. The predecessor read 2 of 5 and left 3 unread; it is gone; the
// successor receives exactly the 3, each marked inherited_from, and none of the
// 2 the predecessor acked.
func TestASuccessorReceivesItsPredecessorsUnreadLocalMail(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	pred := lineConnect(t, ctx, url, "pred", "", "")
	publishN(t, ctx, js, lineInbox("pred"), "p", 1, 5)
	lineReadAck(t, ctx, pred, 2)
	lineExit(t, ctx, pred)

	succ := lineConnect(t, ctx, url, "succ", "", "pred")
	runHeartbeat(t, ctx, succ, 40*time.Millisecond)
	if !waitFor(5*time.Second, func() bool { return len(inheritDurables(t, ctx, js, "succ")) == 1 }) {
		t.Errorf("no inherited durable within 5s of the predecessor's exit")
	}
	res, err := succ.receiveBatch(ctx, 3*time.Second, 10)
	if err != nil {
		t.Fatal(err)
	}
	items := lineItems(t, res)
	if got := lineIDs(items); got != "p3,p4,p5" {
		t.Fatalf("successor received %q, want p3,p4,p5 (the 3 unread, not the 2 acked)", got)
	}
	for _, it := range items {
		if it.InheritedFrom != "pred" {
			t.Errorf("%s carries inherited_from %q, want pred", it.ID, it.InheritedFrom)
		}
	}
}

// Test 2, a guard: without the lineage input there is no inherited durable and
// the predecessor's mail is not read. It holds today and must keep holding.
func TestWithoutALineageInputNothingIsInherited(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	pred := lineConnect(t, ctx, url, "pred", "", "")
	publishN(t, ctx, js, lineInbox("pred"), "p", 1, 3)
	lineExit(t, ctx, pred)

	succ := lineConnect(t, ctx, url, "succ", "", "")
	runHeartbeat(t, ctx, succ, 40*time.Millisecond)
	time.Sleep(600 * time.Millisecond)
	if d := inheritDurables(t, ctx, js, "succ"); len(d) != 0 {
		t.Errorf("an inherited durable exists without a lineage input: %v", d)
	}
	res, err := succ.receiveBatch(ctx, time.Second, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 0 {
		t.Errorf("a seat with no lineage received %v", lineIDs(lineItems(t, res)))
	}
}

// Test 3, the shift order. The successor connects while the predecessor's row is
// live: no inherited durable, nothing read. The predecessor acks 2 more and
// exits: the durable appears after the row is gone, at the new floor, and
// delivers only what is past it.
func TestNoInheritedDurableWhileThePredecessorRowIsLive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	pred := lineConnect(t, ctx, url, "pred", "", "")
	publishN(t, ctx, js, lineInbox("pred"), "p", 1, 5)

	succ := lineConnect(t, ctx, url, "succ", "", "pred")
	runHeartbeat(t, ctx, succ, 40*time.Millisecond)
	time.Sleep(800 * time.Millisecond) // many ticks with the row live
	if d := inheritDurables(t, ctx, js, "succ"); len(d) != 0 {
		t.Fatalf("an inherited durable exists while the predecessor's row is live: %v", d)
	}
	if res, err := succ.receiveBatch(ctx, 500*time.Millisecond, 10); err != nil || len(res.Items) != 0 {
		t.Fatalf("successor read %v (err %v) while the predecessor was live", lineIDs(lineItems(t, res)), err)
	}

	lineReadAck(t, ctx, pred, 2)
	lineExit(t, ctx, pred)
	if !waitFor(5*time.Second, func() bool { return len(inheritDurables(t, ctx, js, "succ")) == 1 }) {
		t.Fatal("no inherited durable within 5s of the predecessor's row going")
	}
	res, err := succ.receiveBatch(ctx, 3*time.Second, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := lineIDs(lineItems(t, res)); got != "p3,p4,p5" {
		t.Errorf("successor received %q, want p3,p4,p5 (past the floor the predecessor reached)", got)
	}
}

// failingReadKV is the presence bucket with every read failing.
type failingReadKV struct{ jetstream.KeyValue }

var errKVRead = errors.New("presence read failed")

func (failingReadKV) Get(context.Context, string) (jetstream.KeyValueEntry, error) {
	return nil, errKVRead
}
func (failingReadKV) Keys(context.Context, ...jetstream.WatchOpt) ([]string, error) {
	return nil, errKVRead
}
func (failingReadKV) ListKeys(context.Context, ...jetstream.WatchOpt) (jetstream.KeyLister, error) {
	return nil, errKVRead
}
func (failingReadKV) Watch(context.Context, string, ...jetstream.WatchOpt) (jetstream.KeyWatcher, error) {
	return nil, errKVRead
}
func (failingReadKV) WatchAll(context.Context, ...jetstream.WatchOpt) (jetstream.KeyWatcher, error) {
	return nil, errKVRead
}
func (failingReadKV) WatchFiltered(context.Context, []string, ...jetstream.WatchOpt) (jetstream.KeyWatcher, error) {
	return nil, errKVRead
}

// Test 3b. A read that fails is unknown, never "the predecessor is gone": with
// every presence read failing, nothing is ever inherited. The control first
// shows the same fixture does inherit when the reads work.
func TestFailedPresenceReadsNeverTriggerInheritance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)

	// control: the predecessor is gone and the reads work.
	pa := lineConnect(t, ctx, url, "pa", "", "")
	publishN(t, ctx, js, lineInbox("pa"), "a", 1, 2)
	lineExit(t, ctx, pa)
	sa := lineConnect(t, ctx, url, "sa", "", "pa")
	runHeartbeat(t, ctx, sa, 40*time.Millisecond)
	if !waitFor(5*time.Second, func() bool { return len(inheritDurables(t, ctx, js, "sa")) == 1 }) {
		t.Fatal("CONTROL FAILED: with working reads and the predecessor gone, nothing was inherited within 5s")
	}

	// the same, with every read failing.
	pb := lineConnect(t, ctx, url, "pb", "", "")
	publishN(t, ctx, js, lineInbox("pb"), "b", 1, 2)
	lineExit(t, ctx, pb)
	sb := lineConnect(t, ctx, url, "sb", "", "pb")
	sb.kv = failingReadKV{KeyValue: sb.kv}
	runHeartbeat(t, ctx, sb, 40*time.Millisecond)
	time.Sleep(1500 * time.Millisecond) // about 35 ticks, every read failed
	if d := inheritDurables(t, ctx, js, "sb"); len(d) != 0 {
		t.Errorf("failed presence reads triggered inheritance: %v", d)
	}
}

// Test 3c, an in-place restart. The predecessor's shim restarts: the old
// instance row goes, a new instance row is written soon after. That is not a
// departure. The control predecessor, whose row never returns, is inherited.
func TestAPredecessorThatRestartsInPlaceIsNotInherited(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)

	// control: the row goes and does not come back.
	pa := lineConnect(t, ctx, url, "pa", "", "")
	publishN(t, ctx, js, lineInbox("pa"), "a", 1, 2)
	lineExit(t, ctx, pa)
	sa := lineConnect(t, ctx, url, "sa", "", "pa")
	runHeartbeat(t, ctx, sa, 40*time.Millisecond)
	if !waitFor(5*time.Second, func() bool { return len(inheritDurables(t, ctx, js, "sa")) == 1 }) {
		t.Fatal("CONTROL FAILED: a predecessor that left was not inherited within 5s")
	}

	// the restart: the old row goes and the new instance's row is written at
	// once, before three empty reads can pass.
	pb := lineConnect(t, ctx, url, "pb", "", "")
	publishN(t, ctx, js, lineInbox("pb"), "b", 1, 2)
	sb := lineConnect(t, ctx, url, "sb", "", "pb")
	runHeartbeat(t, ctx, sb, 40*time.Millisecond)
	time.Sleep(300 * time.Millisecond)
	if err := pb.kv.Delete(ctx, "presence."+pb.self.Team+"."+pb.self.AgentID+"."+pb.instance); err != nil {
		t.Fatal(err)
	}
	pb2 := lineConnect(t, ctx, url, "pb", "", "")
	_ = pb2 // writes its row at connect, as a restarted shim does
	time.Sleep(1500 * time.Millisecond)
	if d := inheritDurables(t, ctx, js, "sb"); len(d) != 0 {
		t.Errorf("a predecessor that restarted in place was inherited: %v", d)
	}
}

// Test 3d, the rollout case. A role-holding predecessor on today's shim has one
// combined [instance, role] durable acked through 120. The successor's
// instance-only inherited durable starts at 121, not 1.
func TestInheritanceStartsAtTheFloorOfACombinedRoleDurable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	pred := lineConnect(t, ctx, url, "pred", "supervisor", "")
	publishN(t, ctx, js, lineInbox("pred"), "p", 1, 125)
	lineReadAck(t, ctx, pred, 120)
	lineExit(t, ctx, pred)

	succ := lineConnect(t, ctx, url, "succ", "", "pred")
	runHeartbeat(t, ctx, succ, 40*time.Millisecond)
	if !waitFor(5*time.Second, func() bool { return len(inheritDurables(t, ctx, js, "succ")) == 1 }) {
		t.Fatal("no inherited durable within 5s of the predecessor's exit")
	}
	res, err := succ.receiveBatch(ctx, 3*time.Second, 200)
	if err != nil {
		t.Fatal(err)
	}
	if got := lineIDs(lineItems(t, res)); got != "p121,p122,p123,p124,p125" {
		t.Errorf("successor received %d messages (%q), want exactly p121 to p125", len(res.Items), got)
	}
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
	st, err := js.Stream(ctx, "AGENT_INBOX")
	if err != nil {
		t.Fatal(err)
	}
	starts := map[string]uint64{}
	l := st.ListConsumers(ctx)
	for ci := range l.Info() {
		if strings.HasPrefix(ci.Name, "mcp_sup_"+fresh.instance) {
			starts[ci.Name] = ci.Config.OptStartSeq
		}
	}
	if err := l.Err(); err != nil {
		t.Fatal(err)
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

// The one lineage helper. Director ruled (c): DIRECTOR_PREDECESSOR first, then
// MARVEL_PREDECESSOR, one function for both tiers and the dossier. This tests
// predecessorID, the function the global tier calls today.
func TestLocalAndGlobalTiersReadTheSameLineageInput(t *testing.T) {
	cases := []struct {
		name, director, marvel, self, want string
	}{
		{"DIRECTOR_PREDECESSOR alone", "dpred", "", "me", "dpred"},
		{"MARVEL_PREDECESSOR alone is the fallback", "", "aae-orc/mpred", "me", "mpred"},
		{"DIRECTOR_PREDECESSOR wins over MARVEL_PREDECESSOR", "dpred", "aae-orc/mpred", "me", "dpred"},
		{"neither set", "", "", "me", ""},
		{"DIRECTOR_PREDECESSOR naming the seat itself is a restart", "me", "", "me", ""},
		{"a DIRECTOR_PREDECESSOR with a workspace prefix keeps the name", "aae-orc/dpred", "", "me", "dpred"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("DIRECTOR_PREDECESSOR", c.director)
			t.Setenv("MARVEL_PREDECESSOR", c.marvel)
			if got := predecessorID(c.self); got != c.want {
				t.Errorf("predecessorID(%q) with DIRECTOR_PREDECESSOR=%q MARVEL_PREDECESSOR=%q = %q, want %q",
					c.self, c.director, c.marvel, got, c.want)
			}
		})
	}
}

// The dossier takes its predecessor from the same input.
func TestDossierReadsTheSameLineageInput(t *testing.T) {
	state := dossierScratch(t)
	ctx, url, js := scratchBus(t)
	now := time.Now()
	auditAt(t, ctx, js, now.Add(-time.Hour), "a1", "REQUEST", "ops-builder-2", "agent://ops/x", "b", senderRole("builder"))
	t.Setenv("DIRECTOR_STATE", state)
	t.Setenv("DIRECTOR_AGENT_ID", "builder-new")
	t.Setenv("DIRECTOR_DWS", "/nonexistent/dws")
	run := func() string {
		cli, err := parseArgs([]string{"dossier", "--role", "ops/builder"})
		if err != nil {
			t.Fatal(err)
		}
		var so, se bytes.Buffer
		if code := runDossierCmd(ctx, url, cli, &so, &se); code != 0 {
			t.Fatalf("exit %d", code)
		}
		for _, l := range strings.Split(so.String(), "\n") {
			if strings.HasPrefix(l, "- predecessor:") {
				return l
			}
		}
		return "(no predecessor line)"
	}
	t.Setenv("MARVEL_PREDECESSOR", "")
	t.Setenv("DIRECTOR_PREDECESSOR", "ops-builder-2")
	if line := run(); !strings.HasPrefix(line, "- predecessor: ops-builder-2") {
		t.Errorf("DIRECTOR_PREDECESSOR was not used for the dossier's predecessor: %q", line)
	}
	t.Setenv("DIRECTOR_PREDECESSOR", "")
	t.Setenv("MARVEL_PREDECESSOR", "aae-orc/ops-builder-2")
	if line := run(); !strings.HasPrefix(line, "- predecessor: ops-builder-2") {
		t.Errorf("a MARVEL_PREDECESSOR of <workspace>/<name> was not read as that name: %q", line)
	}
}
