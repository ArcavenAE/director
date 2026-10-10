package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// K4 (director#278): the handled ledger and ack-after-handled. The ledger
// tests are pure; the broker tests follow drain_broker_test.go's scratch
// nats-server and skip without it.

func ev(tier string, seq uint64, id, disp string) handledEvent {
	stream := "AGENT_INBOX"
	if tier == "global" {
		stream = globalDirectorStream
	}
	return handledEvent{At: "2026-10-10T14:00:00Z", Tier: tier, Stream: stream, Seq: seq, MessageID: id, Disposition: disp}
}

func ledgerLines(t *testing.T, dir string) []string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(dir, "handled.jsonl"))
	s := strings.TrimSpace(string(b))
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func TestHandledLedgerFoldKeysOnMessageIDLastEventWins(t *testing.T) {
	l := newHandledLedger(t.TempDir())
	parked := ev("local", 5, "idA", "parked")
	parked.Reason = "waiting on a PR"
	for _, e := range []handledEvent{parked, ev("local", 6, "idB", "handled"), ev("global", 5, "idC", "handled"), ev("local", 5, "idA", "handled")} {
		if _, err := l.record(e); err != nil {
			t.Fatal(err)
		}
	}
	f, err := l.fold()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.ByID) != 3 {
		t.Fatalf("keys = %d, want 3 (idA, idB, idC): %v", len(f.ByID), f.ByID)
	}
	if d := f.ByID["idA"].Disposition; d != "handled" {
		t.Errorf("idA = %q, want the later handled to win", d)
	}
	if e := f.ByID["idC"]; e.Tier != "global" || e.Seq != 5 || e.Stream != globalDirectorStream {
		t.Errorf("tier, stream and sequence are recorded for the reader: %+v", e)
	}
}

func TestHandledLedgerReusedSequenceWithANewMessageIDIsAnotherKey(t *testing.T) {
	l := newHandledLedger(t.TempDir())
	if _, err := l.record(ev("local", 1, "before-reset", "handled")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.record(ev("local", 1, "after-reset", "handled")); err != nil {
		t.Fatal(err)
	}
	f, _ := l.fold()
	if len(f.ByID) != 2 {
		t.Fatalf("keys = %d, want 2: the sequence is not the key", len(f.ByID))
	}
}

func TestHandledLedgerSameDispositionIsNotRecordedTwice(t *testing.T) {
	dir := t.TempDir()
	l := newHandledLedger(dir)
	first, err := l.record(ev("local", 9, "idA", "handled"))
	if err != nil || !first {
		t.Fatalf("first record = %v, %v; want true", first, err)
	}
	second, err := l.record(ev("local", 9, "idA", "handled"))
	if err != nil || second {
		t.Fatalf("second record = %v, %v; want false (already in the ledger)", second, err)
	}
	if n := len(ledgerLines(t, dir)); n != 1 {
		t.Errorf("ledger has %d lines, want 1", n)
	}
}

func TestHandledLedgerRejectedLinesKeyByPositionNotByMessageID(t *testing.T) {
	dir := t.TempDir()
	l := newHandledLedger(dir)
	rej := ev("local", 4, "", "rejected")
	rej.Reason = "no message_id"
	if _, err := l.record(rej); err != nil {
		t.Fatal(err)
	}
	reuse := ev("local", 8, "idA", "rejected")
	reuse.Reason = "message_id reused with different bytes"
	if _, err := l.record(ev("local", 2, "idA", "handled")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.record(reuse); err != nil {
		t.Fatal(err)
	}
	f, _ := l.fold()
	if len(f.Rejected) != 2 {
		t.Fatalf("rejected = %d, want 2: %v", len(f.Rejected), f.Rejected)
	}
	if d := f.ByID["idA"].Disposition; d != "handled" {
		t.Errorf("a reuse must leave the original's disposition as recorded, got %q", d)
	}
}

func TestHandledLedgerTornLastLineIsLeftAndNextEventStartsOwnLine(t *testing.T) {
	dir := t.TempDir()
	good, _ := json.Marshal(ev("local", 1, "idA", "handled"))
	if err := os.WriteFile(filepath.Join(dir, "handled.jsonl"), append(append([]byte{}, good...), []byte("\n{\"at\":\"2026-10-10T1")...), 0o600); err != nil {
		t.Fatal(err)
	}
	l := newHandledLedger(dir)
	if _, err := l.record(ev("local", 2, "idB", "handled")); err != nil {
		t.Fatal(err)
	}
	f, err := l.fold()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.ByID) != 2 {
		t.Fatalf("keys = %d, want 2 (the torn line is skipped, not fatal): %v", len(f.ByID), f.ByID)
	}
}

func TestHandledLedgerFlockSerializesConcurrentWriters(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	for i := 1; i <= 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if _, err := newHandledLedger(dir).record(ev("local", uint64(n), fmt.Sprintf("id%d", n), "handled")); err != nil {
				t.Errorf("record %d: %v", n, err)
			}
		}(i)
	}
	wg.Wait()
	lines := ledgerLines(t, dir)
	if len(lines) != 20 {
		t.Fatalf("lines = %d, want 20", len(lines))
	}
	for _, ln := range lines {
		var e handledEvent
		if err := json.Unmarshal([]byte(ln), &e); err != nil {
			t.Errorf("line is not whole JSON: %q", ln)
		}
	}
}

func TestHandledLedgerFsyncIsCalledAndItsFailureIsReturned(t *testing.T) {
	dir := t.TempDir()
	l := newHandledLedger(dir)
	synced := 0
	l.syncFile = func(*os.File) error { synced++; return errors.New("disk full") }
	if _, err := l.record(ev("local", 1, "idA", "handled")); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("a failed fsync must be returned, got %v", err)
	}
	if synced != 1 {
		t.Errorf("fsync called %d times, want 1", synced)
	}
	// The line is in the file but was never synced: a retry must not take it
	// for recorded. It syncs again, and only then reports done.
	l.syncFile = func(*os.File) error { synced++; return nil }
	wrote, err := l.record(ev("local", 1, "idA", "handled"))
	if err != nil {
		t.Fatal(err)
	}
	if wrote {
		t.Error("the retry must not write a second line")
	}
	if synced != 2 {
		t.Errorf("the retry must sync the existing line before it is called recorded: synced %d times, want 2", synced)
	}
	if n := len(ledgerLines(t, dir)); n != 1 {
		t.Errorf("ledger has %d lines, want 1", n)
	}
}

func TestHandledModeIsOffByDefault(t *testing.T) {
	t.Setenv("DIRECTOR_HANDLED", "")
	h, err := handledFromEnv()
	if err != nil || h != nil {
		t.Fatalf("handledFromEnv = %v, %v; want nil, nil with the mode unset", h, err)
	}
}

func TestHandledAckWaitComesFromTheThresholdTable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DIRECTOR_STATE", dir)
	t.Setenv("DIRECTOR_HANDLED", "1")
	h, err := handledFromEnv()
	if err != nil || h == nil {
		t.Fatalf("handledFromEnv = %v, %v", h, err)
	}
	if h.ackWait <= 30*time.Second {
		t.Errorf("default ack wait %v must exceed the 30s default so a read message is not redelivered mid-work", h.ackWait)
	}
	if err := os.WriteFile(filepath.Join(dir, "thresholds.json"), []byte(`{"handled_ack_wait":"7m"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h, err = handledFromEnv()
	if err != nil || h.ackWait != 7*time.Minute {
		t.Fatalf("ack wait = %v, %v; want 7m from thresholds.json", h.ackWait, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "thresholds.json"), []byte(`{"handled_ack_wait":"soon"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := handledFromEnv(); err == nil {
		t.Error("a bad table value must be loud, not silently defaulted")
	}
	if err := os.WriteFile(filepath.Join(dir, "thresholds.json"), []byte(`{"handled_ack_wiat":"5m"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := handledFromEnv(); err == nil || !strings.Contains(err.Error(), "handled_ack_wiat") {
		t.Errorf("a misspelled key must be refused by name, not silently ignored: %v", err)
	}
}

func TestMarkHandledToolIsOfferedOnlyWithTheModeOn(t *testing.T) {
	has := func(on bool) bool {
		for _, d := range toolCatalogHandled(nil, false, on) {
			if d.Name == "mark_handled" {
				return true
			}
		}
		return false
	}
	if has(false) {
		t.Error("mark_handled offered with the mode off")
	}
	if !has(true) {
		t.Error("mark_handled missing with the mode on")
	}
}

func TestMarkHandledArgsAreValidated(t *testing.T) {
	cases := []struct{ raw, want string }{
		{`{"message_id":"A","tier":"local","disposition":"handled"}`, ""},
		{`{"message_id":"A","tier":"local","disposition":"forwarded","forward_id":"01X"}`, ""},
		{`{"message_id":"A","tier":"global","disposition":"parked","reason":"waiting on a PR"}`, ""},
		{`{"tier":"local","disposition":"handled"}`, "message_id"},
		{`{"message_id":"A","tier":"local","disposition":"forwarded"}`, "forward_id"},
		{`{"message_id":"A","tier":"local","disposition":"parked"}`, "reason"},
		{`{"message_id":"A","tier":"local","disposition":"done"}`, "disposition"},
		{`{"message_id":"A","tier":"mars","disposition":"handled"}`, "tier"},
	}
	for _, c := range cases {
		_, err := parseMarkArgs(json.RawMessage(c.raw))
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", c.raw, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: err = %v, want it to name %q", c.raw, err, c.want)
		}
	}
}

// ---- broker ----

func handledBus(t *testing.T, ctx context.Context, on bool, table string) (*Bus, jetstream.JetStream, jetstream.JetStream, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DIRECTOR_STATE", dir)
	if on {
		t.Setenv("DIRECTOR_HANDLED", "1")
	} else {
		t.Setenv("DIRECTOR_HANDLED", "")
	}
	if table != "" {
		if err := os.WriteFile(filepath.Join(dir, "thresholds.json"), []byte(table), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	url := startScratchServer(t)
	nc, js := provision(t, ctx, url)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	self := Sender{AgentID: "operator", Workspace: "aae-orc", Team: "ops"}
	bus, err := connect(ctx, url, self, &globalConfig{Domain: "global", Cluster: "kinu", Role: roleDirector})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bus.close)
	return bus, js, gjs, dir
}

const mineSubject = "agent.aae-orc.ops.operator.inbox"

func ackPending(t *testing.T, ctx context.Context, c jetstream.Consumer) int {
	t.Helper()
	info, err := c.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return info.NumAckPending
}

func TestBrokerHandledOffStillAcksOnRead(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, js, _, _ := handledBus(t, ctx, false, "")
	pubEnv(t, ctx, js, mineSubject, "a1", "INFORM", "one")
	pubEnv(t, ctx, js, mineSubject, "a2", "INFORM", "two")
	if _, err := bus.receiveTiered(ctx, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.receiveBatch(ctx, 2*time.Second, 5); err != nil {
		t.Fatal(err)
	}
	if n := ackPending(t, ctx, bus.consumer); n != 0 {
		t.Errorf("ack-pending = %d with the mode off, want 0 (ack on read, bus.go as today)", n)
	}
	if bus.handled != nil {
		t.Error("the mode is off but a handled state exists")
	}
}

func TestBrokerHandledOnReadLeavesMessageAckPendingUntilMarked(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, js, _, dir := handledBus(t, ctx, true, "")
	pubEnv(t, ctx, js, mineSubject, "h1", "REQUEST", "please review")
	res, err := bus.receiveTiered(ctx, 2*time.Second)
	if err != nil || res.Env == nil || res.Env.MessageID != "h1" {
		t.Fatalf("receive = %+v, %v", res, err)
	}
	if n := ackPending(t, ctx, bus.consumer); n != 1 {
		t.Fatalf("ack-pending after read = %d, want 1 (delivered, not acked)", n)
	}
	if len(ledgerLines(t, dir)) != 0 {
		t.Fatal("ledger written on read")
	}
	out, err := bus.markHandled(ctx, "local", "h1", "handled", "")
	if err != nil {
		t.Fatal(err)
	}
	if out["status"] != "recorded" || out["acked"] != true {
		t.Errorf("mark result = %v", out)
	}
	if n := ackPending(t, ctx, bus.consumer); n != 0 {
		t.Errorf("ack-pending after mark = %d, want 0", n)
	}
	f, _ := newHandledLedger(dir).fold()
	e, ok := f.ByID["h1"]
	if !ok || e.Disposition != "handled" || e.Seq != res.Seq || e.Tier != "local" || e.Stream != "AGENT_INBOX" {
		t.Errorf("ledger entry = %+v, %v", e, ok)
	}
	if e.Hash == "" {
		t.Error("every ledger event records the envelope hash")
	}
}

func TestBrokerBatchDrainWithModeOnDoesNotAck(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, js, gjs, _ := handledBus(t, ctx, true, "")
	pubEnv(t, ctx, js, mineSubject, "b1", "INFORM", "one")
	pubEnv(t, ctx, js, mineSubject, "b2", "INFORM", "two")
	pubEnv(t, ctx, gjs, "global.director.inbox", "g1", "QUERY", "which?")
	res, err := bus.receiveBatch(ctx, 2*time.Second, 10)
	if err != nil || len(res.Items) != 3 {
		t.Fatalf("batch = %v, %v", ids(res.Items), err)
	}
	if n := ackPending(t, ctx, bus.consumer); n != 2 {
		t.Errorf("local ack-pending = %d, want 2", n)
	}
	g, err := bus.globalReady(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n := ackPending(t, ctx, g.consumer); n != 1 {
		t.Errorf("global ack-pending = %d, want 1", n)
	}
	for _, it := range res.Items {
		if _, err := bus.markHandled(ctx, it.Tier, it.Env.MessageID, "handled", ""); err != nil {
			t.Fatal(err)
		}
	}
	if a, b := ackPending(t, ctx, bus.consumer), ackPending(t, ctx, g.consumer); a != 0 || b != 0 {
		t.Errorf("ack-pending after marks = %d, %d, want 0, 0", a, b)
	}
}

func TestBrokerParkedAcksAndStaysVisibleInTheLedger(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, js, _, dir := handledBus(t, ctx, true, "")
	pubEnv(t, ctx, js, mineSubject, "p1", "REQUEST", "merge when ready")
	if _, err := bus.receiveTiered(ctx, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.markHandled(ctx, "local", "p1", "parked", "waiting on a PR"); err != nil {
		t.Fatal(err)
	}
	if n := ackPending(t, ctx, bus.consumer); n != 0 {
		t.Errorf("a parked message must ack, ack-pending = %d", n)
	}
	f, _ := newHandledLedger(dir).fold()
	if e := f.ByID["p1"]; e.Disposition != "parked" || e.Reason != "waiting on a PR" {
		t.Errorf("entry = %+v", e)
	}
}

func TestBrokerCrashBetweenLedgerAndAckRedeliversAcksAndDoesNotReturnIt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// The keepalive would hold c1 and stop the redelivery this test needs, so
	// the session is simulated dead: its held set and keepalive are dropped.
	bus, js, _, dir := handledBus(t, ctx, true, `{"handled_ack_wait":"1s"}`)
	pubEnv(t, ctx, js, mineSubject, "c1", "REQUEST", "crash me")
	pubEnv(t, ctx, js, mineSubject, "c2", "INFORM", "next")
	res, _ := bus.receiveTiered(ctx, 2*time.Second)
	bus.handled.simulateCrash()
	if _, err := newHandledLedger(dir).record(handledEvent{At: "2026-10-10T14:00:00Z", Tier: "local", Stream: "AGENT_INBOX", Seq: res.Seq, MessageID: "c1", Disposition: "handled"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond) // the ack wait passes; c1 is redelivered
	next, err := bus.receiveBatch(ctx, 3*time.Second, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Items) != 1 || next.Items[0].Env.MessageID != "c2" {
		t.Fatalf("batch = %v, want only c2 (c1 is in the ledger and must not be returned)", ids(next.Items))
	}
	if _, err := bus.markHandled(ctx, "local", "c2", "handled", ""); err != nil {
		t.Fatal(err)
	}
	if n := ackPending(t, ctx, bus.consumer); n != 0 {
		t.Errorf("c1 must be acked by the drain that found it in the ledger; ack-pending = %d", n)
	}
	if n := strings.Count(strings.Join(ledgerLines(t, dir), "\n"), `"message_id":"c1"`); n != 1 {
		t.Errorf("c1 recorded %d times, want 1", n)
	}
}

func TestBrokerStreamResetReusedSequenceWithNewMessageIDIsReturnedNotAcked(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, js, _, dir := handledBus(t, ctx, true, "")
	// The ledger holds a message from before the stream was recreated. Its
	// sequence is the one the first new message is about to take.
	if _, err := newHandledLedger(dir).record(handledEvent{At: "2026-10-10T14:00:00Z", Tier: "local", Stream: "AGENT_INBOX", Seq: 1, MessageID: "from-the-old-stream", Disposition: "handled"}); err != nil {
		t.Fatal(err)
	}
	pubEnv(t, ctx, js, mineSubject, "from-the-new-stream", "REQUEST", "a different message at sequence 1")
	res, err := bus.receiveTiered(ctx, 2*time.Second)
	if err != nil || res.Env == nil || res.Env.MessageID != "from-the-new-stream" {
		t.Fatalf("receive = %+v, %v; a reused sequence with a new message_id must be returned", res, err)
	}
	if res.Seq != 1 {
		t.Fatalf("test premise: the new message must be at sequence 1, got %d", res.Seq)
	}
	if n := ackPending(t, ctx, bus.consumer); n != 1 {
		t.Errorf("ack-pending = %d, want 1: it was returned, not acked as handled", n)
	}
}

func TestBrokerFailedFsyncNeverAcksAndTheMessageStaysPending(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, js, _, dir := handledBus(t, ctx, true, "")
	pubEnv(t, ctx, js, mineSubject, "f1", "REQUEST", "sync me")
	if _, err := bus.receiveTiered(ctx, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	bus.handled.ledger.syncFile = func(*os.File) error { return errors.New("disk full") }
	if _, err := bus.markHandled(ctx, "local", "f1", "handled", ""); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("the tool must return the fsync error, got %v", err)
	}
	if n := ackPending(t, ctx, bus.consumer); n != 1 {
		t.Fatalf("ack-pending = %d, want 1: a failed fsync never acks", n)
	}
	if rows := bus.handled.unhandled(); len(rows) != 1 {
		t.Errorf("the message must stay held and visible: %+v", rows)
	}
	bus.handled.ledger.syncFile = func(f *os.File) error { return f.Sync() }
	out, err := bus.markHandled(ctx, "local", "f1", "handled", "")
	if err != nil || out["acked"] != true {
		t.Fatalf("retry = %v, %v", out, err)
	}
	if n := len(ledgerLines(t, dir)); n != 1 {
		t.Errorf("ledger has %d lines after the retry, want 1", n)
	}
}

func TestBrokerInProgressKeepsAHeldMessageFromBeingRedelivered(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, js, _, _ := handledBus(t, ctx, true, `{"handled_ack_wait":"1s"}`)
	pubEnv(t, ctx, js, mineSubject, "w1", "REQUEST", "long work")
	if res, _ := bus.receiveTiered(ctx, 2*time.Second); res.Env == nil {
		t.Fatal("no first delivery")
	}
	before, _ := bus.consumer.Info(ctx)
	time.Sleep(3 * time.Second) // three ack waits with no mark
	// A pull consumer redelivers only when asked for more, so ask.
	if again, err := bus.receiveBatch(ctx, 500*time.Millisecond, 5); err != nil || len(again.Items) != 0 {
		t.Fatalf("second drain = %v, %v; want nothing", ids(again.Items), err)
	}
	after, _ := bus.consumer.Info(ctx)
	if after.NumRedelivered != 0 || after.Delivered.Consumer != before.Delivered.Consumer {
		t.Errorf("the held message was redelivered (redelivered %d, delivered %d to %d): InProgress did not run", after.NumRedelivered, before.Delivered.Consumer, after.Delivered.Consumer)
	}
	if after.NumAckPending != 1 {
		t.Errorf("ack-pending = %d, want 1", after.NumAckPending)
	}
	if rows := bus.handled.unhandled(); len(rows) != 1 || rows[0].MessageID != "w1" {
		t.Errorf("read-unhandled = %+v, want w1", rows)
	}
	if _, err := bus.markHandled(ctx, "local", "w1", "handled", ""); err != nil {
		t.Fatal(err)
	}
}

func TestBrokerEnvelopeWithNoMessageIDIsRejectedTermedAndCounted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, js, _, dir := handledBus(t, ctx, true, `{"handled_ack_wait":"1s"}`)
	e := testEnv("", "outsider", "INFORM", "no id here")
	b, _ := json.Marshal(e)
	if _, err := js.Publish(ctx, mineSubject, b); err != nil {
		t.Fatal(err)
	}
	pubEnv(t, ctx, js, mineSubject, "ok1", "INFORM", "fine")
	res, err := bus.receiveBatch(ctx, 2*time.Second, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 1 || res.Items[0].Env.MessageID != "ok1" {
		t.Fatalf("batch = %v, want only ok1: an envelope with no message_id is not returned", ids(res.Items))
	}
	f, _ := newHandledLedger(dir).fold()
	if len(f.Rejected) != 1 {
		t.Fatalf("rejected lines = %d, want 1: %v", len(f.Rejected), f.Rejected)
	}
	time.Sleep(1500 * time.Millisecond)
	if n := ackPending(t, ctx, bus.consumer); n != 1 {
		t.Errorf("ack-pending = %d, want 1 (ok1 only): the rejected one is Termed, not held to redeliver forever", n)
	}
	sum, err := bus.summarizeInbox(ctx, summaryDefault)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Rejected) != 1 || sum.Rejected[0].Reason == "" {
		t.Errorf("inbox_summary must count the rejected line with its reason: %+v", sum.Rejected)
	}
}

func TestBrokerLedgeredMessageIDWithDifferentBytesIsRejectedTermedOriginalKept(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// To adjust if the reuse path changes (director#280 re-review): the
	// expectation is the no-id path, reject and Term, with the original's
	// disposition left as recorded.
	bus, js, _, dir := handledBus(t, ctx, true, "")
	if _, err := newHandledLedger(dir).record(handledEvent{At: "2026-10-10T14:00:00Z", Tier: "local", Stream: "AGENT_INBOX", Seq: 40, MessageID: "r1", Hash: "hash-of-the-original-bytes", Disposition: "handled"}); err != nil {
		t.Fatal(err)
	}
	pubEnv(t, ctx, js, mineSubject, "r1", "REQUEST", "same id, other bytes")
	pubEnv(t, ctx, js, mineSubject, "after", "INFORM", "next")
	res, err := bus.receiveBatch(ctx, 2*time.Second, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 1 || res.Items[0].Env.MessageID != "after" {
		t.Fatalf("batch = %v, want only after", ids(res.Items))
	}
	f, _ := newHandledLedger(dir).fold()
	if len(f.Rejected) != 1 {
		t.Errorf("rejected = %v, want one line for the reuse", f.Rejected)
	}
	if e := f.ByID["r1"]; e.Disposition != "handled" || e.Hash != "hash-of-the-original-bytes" {
		t.Errorf("the original's record must stand: %+v", e)
	}
	if n := ackPending(t, ctx, bus.consumer); n != 1 {
		t.Errorf("ack-pending = %d, want 1 (after only; the reuse is Termed)", n)
	}
	if _, err := bus.markHandled(ctx, "local", "r1", "handled", ""); err != nil {
		t.Logf("mark of a rejected reuse: %v", err) // not asserted: reuse never goes through mark_handled
	}
}

func TestBrokerSummaryListsReadUnhandledOldestFirstApartFromUnread(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, js, _, _ := handledBus(t, ctx, true, "")
	pubEnv(t, ctx, js, mineSubject, "s1", "INFORM", "one")
	pubEnv(t, ctx, js, mineSubject, "s2", "INFORM", "two")
	pubEnv(t, ctx, js, mineSubject, "s3", "INFORM", "three")
	if _, err := bus.receiveBatch(ctx, 2*time.Second, 2); err != nil {
		t.Fatal(err)
	}
	sum, err := bus.summarizeInbox(ctx, summaryDefault)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.ReadUnhandled) != 2 || sum.ReadUnhandled[0].MessageID != "s1" || sum.ReadUnhandled[1].MessageID != "s2" {
		t.Fatalf("read-unhandled = %+v, want s1 then s2", sum.ReadUnhandled)
	}
	if sum.Summary.Total != 1 {
		t.Errorf("unread total = %d, want 1 (s3); read messages are not double counted", sum.Summary.Total)
	}
	if _, err := bus.markHandled(ctx, "local", "s1", "handled", ""); err != nil {
		t.Fatal(err)
	}
	sum, _ = bus.summarizeInbox(ctx, summaryDefault)
	if len(sum.ReadUnhandled) != 1 || sum.ReadUnhandled[0].MessageID != "s2" {
		t.Errorf("after marking s1: %+v", sum.ReadUnhandled)
	}
}

func TestBrokerMarkOfAnIdThisSessionDoesNotHoldIsRefusedAndWritesNothing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, _, _, dir := handledBus(t, ctx, true, "")
	_, err := bus.markHandled(ctx, "local", "ghost", "handled", "")
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("a mark for an id the session does not hold must be refused with an error naming it, got %v", err)
	}
	if n := len(ledgerLines(t, dir)); n != 0 {
		t.Errorf("ledger has %d lines, want 0: a refused mark writes nothing", n)
	}
}

func TestBrokerMessageMarkedBeforeDeliveryIsStillReturnedWhenItArrives(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, js, _, _ := handledBus(t, ctx, true, "")
	// A stale or copied id, marked before its message exists.
	if _, err := bus.markHandled(ctx, "local", "u1", "handled", ""); err == nil {
		t.Fatal("the mark must be refused")
	}
	pubEnv(t, ctx, js, mineSubject, "u1", "REQUEST", "the real message")
	res, err := bus.receiveBatch(ctx, 2*time.Second, 5)
	if err != nil || len(res.Items) != 1 || res.Items[0].Env.MessageID != "u1" {
		t.Fatalf("batch = %v, %v; u1 must be returned, not acked unread", ids(res.Items), err)
	}
	if n := ackPending(t, ctx, bus.consumer); n != 1 {
		t.Errorf("ack-pending = %d, want 1: delivered, not acked", n)
	}
}

func TestBrokerReuseAfterARefusedUnheldMarkIsStillRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, js, _, dir := handledBus(t, ctx, true, "")
	if _, err := bus.markHandled(ctx, "local", "u2", "handled", ""); err == nil {
		t.Fatal("the unheld mark must be refused")
	}
	pubEnv(t, ctx, js, mineSubject, "u2", "REQUEST", "original bytes")
	if res, _ := bus.receiveTiered(ctx, 2*time.Second); res.Env == nil || res.Env.MessageID != "u2" {
		t.Fatalf("the original must be returned: %+v", res)
	}
	if _, err := bus.markHandled(ctx, "local", "u2", "handled", ""); err != nil {
		t.Fatal(err)
	}
	pubEnv(t, ctx, js, mineSubject, "u2", "REQUEST", "other bytes under the same id")
	pubEnv(t, ctx, js, mineSubject, "after", "INFORM", "next")
	res, err := bus.receiveBatch(ctx, 2*time.Second, 5)
	if err != nil || len(res.Items) != 1 || res.Items[0].Env.MessageID != "after" {
		t.Fatalf("batch = %v, %v; the reuse must be rejected, not acked as handled", ids(res.Items), err)
	}
	if f, _ := newHandledLedger(dir).fold(); len(f.Rejected) != 1 {
		t.Errorf("rejected = %v, want the reuse", f.Rejected)
	}
}

func TestBrokerFailedRejectedLineWriteNeverTermsAndTheMessageStaysPending(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, js, _, dir := handledBus(t, ctx, true, `{"handled_ack_wait":"1s"}`)
	e := testEnv("", "outsider", "INFORM", "no id here")
	b, _ := json.Marshal(e)
	if _, err := js.Publish(ctx, mineSubject, b); err != nil {
		t.Fatal(err)
	}
	bus.handled.ledger.syncFile = func(*os.File) error { return errors.New("disk full") }
	_, _ = bus.receiveBatch(ctx, 1*time.Second, 5) // the rejection fails; the error rides beside the result
	// m.Term() is not confirmed by the server, so a Term sent by a broken guard
	// can land after the first read. Poll for a second: ack-pending must hold at
	// 1 the whole time, and any read of 0 is the Term.
	for until := time.Now().Add(time.Second); time.Now().Before(until); time.Sleep(25 * time.Millisecond) {
		if n := ackPending(t, ctx, bus.consumer); n != 1 {
			t.Fatalf("ack-pending = %d, want 1: a rejected line that was not written must not be Termed", n)
		}
	}
	// With the disk back, the redelivery is rejected properly and Termed.
	bus.handled.ledger.syncFile = func(f *os.File) error { return f.Sync() }
	time.Sleep(1500 * time.Millisecond)
	if _, err := bus.receiveBatch(ctx, 1*time.Second, 5); err != nil {
		t.Fatal(err)
	}
	if n := ackPending(t, ctx, bus.consumer); n != 0 {
		t.Errorf("ack-pending = %d, want 0 after the redelivery was rejected and Termed", n)
	}
	if f, _ := newHandledLedger(dir).fold(); len(f.Rejected) != 1 {
		t.Errorf("rejected = %v, want 1", f.Rejected)
	}
}

func TestBrokerAckWaitIsSetOnBothTiersFromTheTable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, _, _, _ := handledBus(t, ctx, true, `{"handled_ack_wait":"7m"}`)
	g, err := bus.globalReady(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]jetstream.Consumer{"local": bus.consumer, "global": g.consumer} {
		info, err := c.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if info.Config.AckWait != 7*time.Minute {
			t.Errorf("%s ack wait = %v, want 7m from the table", name, info.Config.AckWait)
		}
	}
}

func TestBrokerAckWaitIsLeftAloneWithTheModeOff(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, _, _, _ := handledBus(t, ctx, false, `{"handled_ack_wait":"7m"}`)
	g, err := bus.globalReady(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]jetstream.Consumer{"local": bus.consumer, "global": g.consumer} {
		info, _ := c.Info(ctx)
		if info.Config.AckWait == 7*time.Minute {
			t.Errorf("%s ack wait was set with the mode off", name)
		}
	}
}

func TestBrokerMarkHandledRefusedWithTheModeOff(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, _, _, _ := handledBus(t, ctx, false, "")
	if _, err := bus.markHandled(ctx, "local", "x", "handled", ""); err == nil {
		t.Error("mark_handled with the mode off must refuse")
	}
}
