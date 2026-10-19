package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// director#269: a local durable deleted under a running session was never
// rebuilt, so inbox_summary kept returning 404 consumer not found and the
// long poll kept returning no responders. The global tier already recreates
// its durable (global.go restore); these tests hold the local tier to the same
// behavior against a scratch nats-server, never a live bus.

const localLossSubject = "agent.aae-orc.ops.operator.inbox"

func localDurableName(b *Bus) string { return "mcp_operator_" + b.instance }

// localLossBus attaches a local-only session and returns it with the stream
// context. It clears the attach-time resume note, so a note seen later is the
// recreate's.
func localLossBus(t *testing.T, ctx context.Context) (*Bus, jetstream.JetStream) {
	t.Helper()
	t.Setenv("DIRECTOR_STATE", t.TempDir())
	t.Setenv("DIRECTOR_HANDLED", "")
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	bus, err := connect(ctx, url, Sender{AgentID: "operator", Workspace: "aae-orc", Team: "ops"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bus.close)
	bus.takeResumed()
	return bus, js
}

// lossFixture reads and acks a0, leaves a1 unread, deletes the durable under
// the session, and publishes a2. A rebuilt durable must deliver a1 and a2 and
// must not replay a0.
func lossFixture(t *testing.T, ctx context.Context) (*Bus, jetstream.JetStream) {
	t.Helper()
	bus, js := localLossBus(t, ctx)
	pubEnv(t, ctx, js, localLossSubject, "a0", "INFORM", "before the loss, read")
	pubEnv(t, ctx, js, localLossSubject, "a1", "INFORM", "before the loss, unread")
	first, err := bus.receiveTiered(ctx, 5*time.Second)
	if err != nil || first.Env == nil || first.Env.MessageID != "a0" {
		t.Fatalf("setup read: %+v %v", first, err)
	}
	// control: a healthy durable reports no recreate, so a note seen below is real.
	for _, n := range bus.takeResumed() {
		if strings.Contains(n, "recreated") {
			t.Fatalf("CONTROL FAILED: a healthy durable reported a recreate: %q", n)
		}
	}
	if err := js.DeleteConsumer(ctx, "AGENT_INBOX", localDurableName(bus)); err != nil {
		t.Fatal(err)
	}
	pubEnv(t, ctx, js, localLossSubject, "a2", "INFORM", "after the loss")
	return bus, js
}

func recreateNote(t *testing.T, bus *Bus) string {
	t.Helper()
	for _, n := range bus.takeResumed() {
		if strings.Contains(n, "recreated") {
			return n
		}
	}
	return ""
}

func TestBrokerLostLocalDurableIsRecreatedForThePoll(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, js := lossFixture(t, ctx)

	var got []string
	for i := 0; i < 2; i++ {
		res, err := bus.receiveTiered(ctx, 8*time.Second)
		if err != nil {
			t.Fatalf("poll %d after the loss: %v", i, err)
		}
		if res.Env == nil {
			t.Fatalf("poll %d after the loss returned nothing; got so far %v", i, got)
		}
		got = append(got, res.Env.MessageID)
	}
	if strings.Join(got, ",") != "a1,a2" {
		t.Fatalf("after the loss got %v, want [a1 a2]: the unread a1 delivered, the acked a0 not replayed", got)
	}
	if recreateNote(t, bus) == "" {
		t.Error("the recreate must be reported in the resume notes")
	}
	if recreateNote(t, bus) != "" {
		t.Error("the notice repeated")
	}
	if _, err := js.Consumer(ctx, "AGENT_INBOX", localDurableName(bus)); err != nil {
		t.Errorf("durable not back: %v", err)
	}
}

func TestBrokerLostLocalDurableIsRecreatedForTheBatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, _ := lossFixture(t, ctx)

	res, err := bus.receiveBatch(ctx, 8*time.Second, 5)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(res.Items); strings.Join(got, ",") != "local:a1,local:a2" {
		t.Fatalf("batch after the loss = %v, want [local:a1 local:a2]", got)
	}
	if recreateNote(t, bus) == "" {
		t.Error("the recreate must be reported in the resume notes")
	}
}

func TestBrokerLostLocalDurableIsRecreatedForTheSummary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, _ := lossFixture(t, ctx)

	res, err := bus.summarizeInbox(ctx, summaryDefault)
	if err != nil {
		t.Fatalf("the summary after the loss failed, the 404 of director#269: %v", err)
	}
	if res.Read["local"] != 2 {
		t.Errorf("summary read %d local, want 2 (a1 and a2)", res.Read["local"])
	}
	if recreateNote(t, bus) == "" {
		t.Error("the recreate must be reported in the resume notes")
	}
}

// An empty pull on a durable the broker no longer has looks like an empty
// inbox. It is checked at most once per durableCheckEvery (global.go recheck).
func TestBrokerLostLocalDurableIsFoundByAnEmptyPoll(t *testing.T) {
	old := durableCheckEvery
	durableCheckEvery = 0
	defer func() { durableCheckEvery = old }()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, js := localLossBus(t, ctx)

	// control: with the durable intact an empty poll rebuilds nothing.
	if _, err := bus.receiveTiered(ctx, time.Second); err != nil {
		t.Fatal(err)
	}
	if recreateNote(t, bus) != "" {
		t.Fatal("CONTROL FAILED: an empty poll on a healthy durable reported a recreate")
	}

	if err := js.DeleteConsumer(ctx, "AGENT_INBOX", localDurableName(bus)); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.receiveTiered(ctx, time.Second); err != nil {
		t.Fatalf("the empty poll after the loss failed: %v", err)
	}
	if _, err := js.Consumer(ctx, "AGENT_INBOX", localDurableName(bus)); err != nil {
		t.Fatalf("an empty poll did not find the lost durable: %v", err)
	}
	pubEnv(t, ctx, js, localLossSubject, "b1", "INFORM", "after the empty poll")
	res, err := bus.receiveTiered(ctx, 8*time.Second)
	if err != nil || res.Env == nil || res.Env.MessageID != "b1" {
		t.Fatalf("mail sent after the rebuild must arrive, got %+v %v", res, err)
	}
	if recreateNote(t, bus) == "" {
		t.Error("the recreate must be reported")
	}
}

// Handled mode acks only on mark_handled, so a held message can sit below a
// message acked later. A rebuild that starts after the highest ack would skip
// the held one and leave its ack handle dead.
func TestBrokerLostLocalDurableKeepsAHeldMessageMarkable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, js, _, _ := handledBus(t, ctx, true, "")
	bus.takeResumed()
	pubEnv(t, ctx, js, mineSubject, "h1", "REQUEST", "held, never marked before the loss")
	pubEnv(t, ctx, js, mineSubject, "h2", "REQUEST", "marked before the loss")
	for _, want := range []string{"h1", "h2"} {
		if res, err := bus.receiveTiered(ctx, 5*time.Second); err != nil || res.Env == nil || res.Env.MessageID != want {
			t.Fatalf("setup read %s: %+v %v", want, res, err)
		}
	}
	if out, err := bus.markHandled(ctx, "local", "h2", "handled", ""); err != nil || out["acked"] != true {
		t.Fatalf("setup mark h2: %v %v", out, err)
	}
	if err := js.DeleteConsumer(ctx, "AGENT_INBOX", "mcp_operator_"+bus.instance); err != nil {
		t.Fatal(err)
	}
	pubEnv(t, ctx, js, mineSubject, "h3", "REQUEST", "after the loss")

	res, err := bus.receiveTiered(ctx, 8*time.Second)
	if err != nil || res.Env == nil || res.Env.MessageID != "h3" {
		t.Fatalf("after the loss want h3 (h1 is already held, h2 is handled), got %+v %v", res, err)
	}
	// control: h3 is markable on the rebuilt durable, so a failure below is h1's.
	if out, err := bus.markHandled(ctx, "local", "h3", "handled", ""); err != nil || out["acked"] != true {
		t.Fatalf("CONTROL FAILED: h3 should mark and ack on the rebuilt durable: %v %v", out, err)
	}
	// The new durable redelivered h1 (the handled state swapped in a live ack
	// handle), so the broker holds it ack-pending; a rebuild that started after
	// h2 would leave 0 here and h1's old handle dead.
	if n := ackPending(t, ctx, bus.consumer); n != 1 {
		t.Fatalf("ack-pending on the rebuilt durable = %d, want 1 (h1 redelivered)", n)
	}
	out, err := bus.markHandled(ctx, "local", "h1", "handled", "")
	if err != nil || out["acked"] != true {
		t.Fatalf("the held h1 must still ack after the rebuild (the rebuild must redeliver it): %v %v", out, err)
	}
	if n := ackPending(t, ctx, bus.consumer); n != 0 {
		t.Fatalf("ack-pending after marking h1 = %d, want 0", n)
	}
}

// counted wraps the broker lookup so a test can see how often it is made.
func counted(t *testing.T, bus *Bus, fail error) *int {
	t.Helper()
	n := 0
	bus.lookup = func(ctx context.Context, durable string) error {
		n++
		if fail != nil {
			return fail
		}
		_, err := bus.js.Consumer(ctx, "AGENT_INBOX", durable)
		return err
	}
	return &n
}

// A healthy empty poll asks the broker whether the durable exists at most once
// per durableCheckEvery, and not at all inside the window (director#328 round 1:
// a mutant that makes recheckLocal return false must fail here).
func TestBrokerEmptyPollAsksTheBrokerOncePerWindow(t *testing.T) {
	old := durableCheckEvery
	durableCheckEvery = time.Hour
	defer func() { durableCheckEvery = old }()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, _ := localLossBus(t, ctx)
	asks := counted(t, bus, nil)

	// inside the window (attach just set it): an empty poll, a batch and a
	// non-blocking take ask nothing.
	if _, err := bus.receiveTiered(ctx, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.receiveBatch(ctx, time.Second, 5); err != nil {
		t.Fatal(err)
	}
	if *asks != 0 {
		t.Fatalf("inside the window the broker was asked %d times, want 0", *asks)
	}

	// due: the next empty poll asks once, finds the durable, rebuilds nothing, and
	// the window starts again.
	bus.localMu.Lock()
	bus.local.checked = time.Now().Add(-2 * time.Hour)
	bus.localMu.Unlock()
	if _, err := bus.receiveTiered(ctx, time.Second); err != nil {
		t.Fatal(err)
	}
	if *asks != 1 {
		t.Fatalf("when due the broker was asked %d times, want 1", *asks)
	}
	if recreateNote(t, bus) != "" {
		t.Fatal("a healthy durable was rebuilt")
	}
	for i := 0; i < 3; i++ {
		if _, err := bus.receiveBatch(ctx, time.Second, 5); err != nil {
			t.Fatal(err)
		}
	}
	if *asks != 1 {
		t.Fatalf("after the check the window should hold: %d asks, want 1", *asks)
	}
}

// Only a broker that says the durable is not found rebuilds it. Any other
// answer, such as a timeout, changes nothing (director#328 round 1: a mutant
// that rebuilds on any error must fail here).
func TestBrokerOnlyNotFoundRebuilds(t *testing.T) {
	old := durableCheckEvery
	durableCheckEvery = 0
	defer func() { durableCheckEvery = old }()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, js := localLossBus(t, ctx)
	if err := js.DeleteConsumer(ctx, "AGENT_INBOX", localDurableName(bus)); err != nil {
		t.Fatal(err)
	}
	asks := counted(t, bus, context.DeadlineExceeded)
	_, _ = bus.receiveTiered(ctx, time.Second) // the pull error is reported as it was
	if *asks == 0 {
		t.Fatal("CONTROL FAILED: the lookup was never made, so the answer below proves nothing")
	}
	if _, err := js.Consumer(ctx, "AGENT_INBOX", localDurableName(bus)); err == nil {
		t.Fatal("a lookup that failed with a timeout rebuilt the durable")
	}
	if recreateNote(t, bus) != "" {
		t.Fatal("a recreate was reported for a lookup that failed")
	}
	// control: the same state with an honest not-found answer does rebuild.
	counted(t, bus, nil)
	if _, err := bus.receiveTiered(ctx, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := js.Consumer(ctx, "AGENT_INBOX", localDurableName(bus)); err != nil {
		t.Fatalf("CONTROL FAILED: a not-found answer should rebuild: %v", err)
	}
}
