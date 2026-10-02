package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// A cluster credential on the hub may create and read its own consumers but
// not list a stream's consumers: CONSUMER.NAMES and CONSUMER.LIST are outside
// the leaf's allow-list and answer "no responders" (finding-006). The global
// resume floor must not depend on listing (aae-orc-2ro3e: a /mcp reconnect
// replayed 212 already-acked global messages while the local tier resumed).

// startScratchServerWithSeat starts a scratch server like startScratchServer,
// with an admin user that provisions and a seat user denied consumer listing
// on the hub stream, as a cluster credential is. One process serves both
// tiers, and the server checks a domain request against its mapped $JS.API
// subject, so the deny names the hub stream in both forms: listing the local
// AGENT_INBOX stays allowed, as it is through a leaf. deny adds API verbs to
// deny on the hub stream (for example "CONSUMER.INFO").
func startScratchServerWithSeat(t *testing.T, deny ...string) (adminURL, seatURL string) {
	t.Helper()
	bin, err := exec.LookPath("nats-server")
	if err != nil {
		t.Skip("nats-server not on PATH")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	dir := t.TempDir()
	var quoted []string
	for _, verb := range append([]string{"CONSUMER.LIST", "CONSUMER.NAMES"}, deny...) {
		for _, api := range []string{"$JS.API.", "$JS.global.API."} {
			if strings.Contains(verb, "."+globalDirectorStream+".") {
				quoted = append(quoted, fmt.Sprintf("%q", api+verb)) // one exact subject
				continue
			}
			quoted = append(quoted, fmt.Sprintf("%q", api+verb+"."+globalDirectorStream), fmt.Sprintf("%q", api+verb+"."+globalDirectorStream+".>"))
		}
	}
	body := fmt.Sprintf(`listen: 127.0.0.1:%d
jetstream { store_dir: %q, domain: global }
authorization {
  users = [
    {user: admin, password: admin}
    {user: seat, password: seat, permissions: {publish: {allow: [">"], deny: [%s]}, subscribe: {allow: [">"]}}}
  ]
}
`, port, filepath.Join(dir, "js"), strings.Join(quoted, ", "))
	conf := filepath.Join(dir, "s.conf")
	if err := os.WriteFile(conf, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-c", conf)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	adminURL = fmt.Sprintf("nats://admin:admin@127.0.0.1:%d", port)
	seatURL = fmt.Sprintf("nats://seat:seat@127.0.0.1:%d", port)
	for i := 0; i < 50; i++ {
		if nc, err := nats.Connect(adminURL); err == nil {
			nc.Close()
			return adminURL, seatURL
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("scratch nats-server did not come up")
	return "", ""
}

// The seat user really cannot list hub consumers, or the tests below prove
// nothing.
// A denied request on the scratch server times out rather than failing fast,
// so the tests shorten the shim's bound on the hub listing.
func shortFloorList(t *testing.T) {
	t.Helper()
	prev := floorListTimeout
	floorListTimeout = 300 * time.Millisecond
	t.Cleanup(func() { floorListTimeout = prev })
}

func TestBrokerSeatUserCannotListHubConsumers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, seat := startScratchServerWithSeat(t)
	provision(t, ctx, admin)
	nc, err := nats.Connect(seat)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	st, err := gjs.Stream(ctx, globalDirectorStream)
	if err != nil {
		t.Fatalf("stream info is allowed: %v", err)
	}
	// A denied request gets no reply here, so bound it; the hub answers "no
	// responders" at once instead.
	lctx, lcancel := context.WithTimeout(ctx, 2*time.Second)
	lister := st.ListConsumers(lctx)
	for range lister.Info() {
	}
	lcancel()
	if lister.Err() == nil {
		t.Fatal("listing hub consumers must fail for the seat user")
	}
	ljs, _ := jetstream.New(nc)
	lst, err := ljs.Stream(ctx, "AGENT_INBOX")
	if err != nil {
		t.Fatal(err)
	}
	local := lst.ListConsumers(ctx)
	for range local.Info() {
	}
	if err := local.Err(); err != nil {
		t.Fatalf("listing local consumers must stay allowed: %v", err)
	}
}

// A reconnect resumes after the seat's last ack on the hub even though the
// seat cannot list hub consumers.
func TestBrokerGlobalReconnectResumesWithoutListingHubConsumers(t *testing.T) {
	shortFloorList(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, seat := startScratchServerWithSeat(t)
	nc, _ := provision(t, ctx, admin)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	gcfg := &globalConfig{Domain: "global", Cluster: "c1", Role: roleDirector}
	pubEnv(t, ctx, gjs, "global.director.inbox", "g0", "INFORM", "read by the first")
	b1, err := connect(ctx, seat, resumeSelf, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := b1.receiveTiered(ctx, 8*time.Second); err != nil || r.Env == nil || r.Env.MessageID != "g0" {
		t.Fatalf("first read %+v %v", r, err)
	}
	b1.close()
	pubEnv(t, ctx, gjs, "global.director.inbox", "g1", "INFORM", "while reconnecting")
	b2, err := connect(ctx, seat, resumeSelf, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b2.close()
	res, err := b2.receiveBatch(ctx, 3*time.Second, 5)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(res.Items); fmt.Sprint(got) != "[global:g1]" {
		t.Errorf("second instance read %v, want only [global:g1]", got)
	}
	notes := strings.Join(b2.takeResumed(), " | ")
	if !strings.Contains(notes, "global inbox resumed after stream sequence 1") {
		t.Errorf("resume note: %q", notes)
	}
}

// A seat with no earlier instance still reads the mail waiting on the hub.
func TestBrokerGlobalNewSeatReadsUnackedMailWithoutListing(t *testing.T) {
	shortFloorList(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, seat := startScratchServerWithSeat(t)
	nc, _ := provision(t, ctx, admin)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	gcfg := &globalConfig{Domain: "global", Cluster: "c1", Role: roleDirector}
	pubEnv(t, ctx, gjs, "global.director.inbox", "g0", "INFORM", "waiting")
	pubEnv(t, ctx, gjs, "global.director.inbox", "g1", "INFORM", "waiting too")
	b, err := connect(ctx, seat, resumeSelf, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	res, err := b.receiveBatch(ctx, 3*time.Second, 5)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(res.Items); fmt.Sprint(got) != "[global:g0 global:g1]" {
		t.Errorf("new seat read %v, want [global:g0 global:g1]", got)
	}
}

// A live sibling's position is still not taken: a session joining a live seat
// gets its own copy, listing or not.
func TestBrokerGlobalJoiningALiveSeatWithoutListingGetsItsOwnCopy(t *testing.T) {
	shortFloorList(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, seat := startScratchServerWithSeat(t)
	nc, _ := provision(t, ctx, admin)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	gcfg := &globalConfig{Domain: "global", Cluster: "c1", Role: roleDirector}
	pubEnv(t, ctx, gjs, "global.director.inbox", "g0", "INFORM", "read by the first")
	b1, err := connect(ctx, seat, resumeSelf, gcfg)
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
	b2, err := connect(ctx, seat, resumeSelf, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b2.close()
	res, err := b2.receiveBatch(ctx, 3*time.Second, 5)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(res.Items); fmt.Sprint(got) != "[global:g0]" {
		t.Errorf("joining session read %v, want [global:g0]", got)
	}
}

// When the seat's earlier position cannot be read at all, the note says so
// and why; it does not claim there was no earlier durable.
func TestAnUnreadableFloorIsNamedNotDenied(t *testing.T) {
	got := resumeNote("global", 0, 212, "the hub refused the listing; no departed instance was found by name")
	if strings.Contains(got, "no earlier durable") || !strings.Contains(got, "global inbox could not read the seat's earlier position") ||
		!strings.Contains(got, "the hub refused the listing") || !strings.Contains(got, "212 message(s) waiting") {
		t.Errorf("note: %q", got)
	}
	if got := resumeNote("global", 0, 2, ""); !strings.Contains(got, "no earlier durable") {
		t.Errorf("with no reason, the note is unchanged: %q", got)
	}
	if got := resumeNote("global", 219, 0, "ignored"); !strings.Contains(got, "resumed after stream sequence 219") {
		t.Errorf("a found floor wins: %q", got)
	}
}

// departedInstance builds, as the admin user, what an earlier instance of a
// seat leaves behind: a local durable mcp_<agent>_<inst> and a hub durable
// mcp_global_<agent>_<inst> filtering hubFilter, with every message on that
// subject acked, and no presence row.
func departedInstance(t *testing.T, ctx context.Context, nc *nats.Conn, agent, inst, hubFilter string) {
	t.Helper()
	js, _ := jetstream.New(nc)
	if _, err := js.CreateOrUpdateConsumer(ctx, "AGENT_INBOX", jetstream.ConsumerConfig{
		Durable: "mcp_" + agent + "_" + inst, FilterSubject: "agent.aae-orc.ops." + agent + ".inbox", AckPolicy: jetstream.AckExplicitPolicy,
	}); err != nil {
		t.Fatal(err)
	}
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	cons, err := gjs.CreateOrUpdateConsumer(ctx, globalDirectorStream, jetstream.ConsumerConfig{
		Durable: "mcp_global_" + agent + "_" + inst, FilterSubject: hubFilter, AckPolicy: jetstream.AckExplicitPolicy,
	})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := cons.Fetch(10, jetstream.FetchMaxWait(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for m := range batch.Messages() {
		if err := m.DoubleAck(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

// A departed instance's hub durable on a different subject is not this
// seat's position on its own inbox.
func TestBrokerGlobalFloorByNameIgnoresADurableOnAnotherSubject(t *testing.T) {
	shortFloorList(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, seat := startScratchServerWithSeat(t)
	nc, _ := provision(t, ctx, admin)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	gcfg := &globalConfig{Domain: "global", Cluster: "c1", Role: roleDirector}
	pubEnv(t, ctx, gjs, "global.director.inbox", "g0", "INFORM", "unread on this seat's subject")
	pubEnv(t, ctx, gjs, "global.director.other", "x1", "INFORM", "acked on another subject")
	departedInstance(t, ctx, nc, "operator", "01OLDOTHERSUBJECT", "global.director.other")
	b, err := connect(ctx, seat, resumeSelf, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	res, err := b.receiveBatch(ctx, 3*time.Second, 5)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(res.Items); fmt.Sprint(got) != "[global:g0]" {
		t.Errorf("read %v, want [global:g0]: an ack on another subject is not this inbox's floor", got)
	}
}

// A departed seat whose id extends this one (sup_T1 for sup) is not this
// seat's predecessor, though its local durable starts mcp_sup_.
func TestBrokerGlobalFloorByNameIgnoresASeatWhoseIDExtendsThisOne(t *testing.T) {
	shortFloorList(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, seat := startScratchServerWithSeat(t)
	nc, _ := provision(t, ctx, admin)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	gcfg := &globalConfig{Domain: "global", Cluster: "c1", Role: roleDirector}
	pubEnv(t, ctx, gjs, "global.director.inbox", "g0", "INFORM", "read by sup_T1")
	departedInstance(t, ctx, nc, "sup_T1", "01OLDEXTENDEDSEAT", "global.director.inbox")
	sup, err := connect(ctx, seat, Sender{AgentID: "sup", Workspace: "aae-orc", Team: "ops"}, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer sup.close()
	res, err := sup.receiveBatch(ctx, 3*time.Second, 5)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(res.Items); fmt.Sprint(got) != "[global:g0]" {
		t.Errorf("sup read %v, want [global:g0]: sup_T1's position is not sup's", got)
	}
}

// When the departed instance cannot be read by name either, the note names
// the failure rather than claiming there was no earlier durable.
func TestBrokerGlobalFloorUnreadableByNameIsReported(t *testing.T) {
	shortFloorList(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, seat := startScratchServerWithSeat(t, "CONSUMER.INFO."+globalDirectorStream+".mcp_global_operator_01OLDUNREADABLE")
	nc, _ := provision(t, ctx, admin)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	gcfg := &globalConfig{Domain: "global", Cluster: "c1", Role: roleDirector}
	pubEnv(t, ctx, gjs, "global.director.inbox", "g0", "INFORM", "acked by the departed instance")
	departedInstance(t, ctx, nc, "operator", "01OLDUNREADABLE", "global.director.inbox")
	b, err := connect(ctx, seat, resumeSelf, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	if _, err := b.globalReady(ctx); err != nil {
		t.Fatal(err)
	}
	notes := strings.Join(b.takeResumed(), " | ")
	if strings.Contains(notes, "global inbox has no earlier durable") || !strings.Contains(notes, "global inbox could not read the seat's earlier position") {
		t.Errorf("resume note: %q", notes)
	}
}
