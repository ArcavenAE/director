package main

import (
	"context"
	"encoding/json"
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

func ev(tier string, seq uint64, disp string) handledEvent {
	stream := "AGENT_INBOX"
	if tier == "global" {
		stream = globalDirectorStream
	}
	return handledEvent{At: "2026-10-10T14:00:00Z", Tier: tier, Stream: stream, Seq: seq, MessageID: "m", Disposition: disp}
}

func TestHandledLedgerFoldLastEventWinsPerKey(t *testing.T) {
	l := newHandledLedger(t.TempDir())
	for _, e := range []handledEvent{
		ev("local", 5, "parked"), ev("local", 6, "handled"), ev("global", 5, "handled"), ev("local", 5, "handled"),
	} {
		if e.Disposition == "parked" {
			e.Reason = "waiting on a PR"
		}
		if _, err := l.record(e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := l.fold()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("keys = %d, want 3 (local 5, local 6, global 5): %v", len(got), got)
	}
	if d := got[handledKey{"local", "AGENT_INBOX", 5}].Disposition; d != "handled" {
		t.Errorf("local 5 = %q, want the later handled to win", d)
	}
	if _, ok := got[handledKey{"global", globalDirectorStream, 5}]; !ok {
		t.Error("tier is part of the key: global 5 must be its own entry")
	}
}

func TestHandledLedgerSameDispositionIsNotRecordedTwice(t *testing.T) {
	dir := t.TempDir()
	l := newHandledLedger(dir)
	first, err := l.record(ev("local", 9, "handled"))
	if err != nil || !first {
		t.Fatalf("first record = %v, %v; want true", first, err)
	}
	second, err := l.record(ev("local", 9, "handled"))
	if err != nil || second {
		t.Fatalf("second record = %v, %v; want false (already in the ledger)", second, err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "handled.jsonl"))
	if n := strings.Count(string(b), "\n"); n != 1 {
		t.Errorf("ledger has %d lines, want 1", n)
	}
}

func TestHandledLedgerTornLastLineIsLeftAndNextEventStartsOwnLine(t *testing.T) {
	dir := t.TempDir()
	good, _ := json.Marshal(ev("local", 1, "handled"))
	if err := os.WriteFile(filepath.Join(dir, "handled.jsonl"), append(append([]byte{}, good...), []byte("\n{\"at\":\"2026-10-10T1")...), 0o600); err != nil {
		t.Fatal(err)
	}
	l := newHandledLedger(dir)
	if _, err := l.record(ev("local", 2, "handled")); err != nil {
		t.Fatal(err)
	}
	got, err := l.fold()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("keys = %d, want 2 (the torn line is skipped, not fatal): %v", len(got), got)
	}
}

func TestHandledLedgerFlockSerializesConcurrentWriters(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	for i := 1; i <= 20; i++ {
		wg.Add(1)
		go func(seq uint64) {
			defer wg.Done()
			if _, err := newHandledLedger(dir).record(ev("local", seq, "handled")); err != nil {
				t.Errorf("record %d: %v", seq, err)
			}
		}(uint64(i))
	}
	wg.Wait()
	b, _ := os.ReadFile(filepath.Join(dir, "handled.jsonl"))
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
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
		{`{"tier":"local","sequence":3,"disposition":"handled"}`, ""},
		{`{"tier":"local","sequence":3,"disposition":"forwarded","forward_id":"01X"}`, ""},
		{`{"tier":"global","sequence":3,"disposition":"parked","reason":"waiting on a PR"}`, ""},
		{`{"tier":"local","sequence":3,"disposition":"forwarded"}`, "forward_id"},
		{`{"tier":"local","sequence":3,"disposition":"parked"}`, "reason"},
		{`{"tier":"local","sequence":3,"disposition":"done"}`, "disposition"},
		{`{"tier":"mars","sequence":3,"disposition":"handled"}`, "tier"},
		{`{"tier":"local","disposition":"handled"}`, "sequence"},
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
	if b, _ := os.ReadFile(filepath.Join(dir, "handled.jsonl")); len(b) != 0 {
		t.Fatalf("ledger written on read: %q", b)
	}
	out, err := bus.markHandled(ctx, "local", res.Seq, "handled", "")
	if err != nil {
		t.Fatal(err)
	}
	if out["status"] != "recorded" {
		t.Errorf("mark result = %v", out)
	}
	if n := ackPending(t, ctx, bus.consumer); n != 0 {
		t.Errorf("ack-pending after mark = %d, want 0", n)
	}
	got, _ := newHandledLedger(dir).fold()
	if e, ok := got[handledKey{"local", "AGENT_INBOX", res.Seq}]; !ok || e.Disposition != "handled" || e.MessageID != "h1" {
		t.Errorf("ledger entry = %+v, %v", e, ok)
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
		if _, err := bus.markHandled(ctx, it.Tier, it.Seq, "handled", ""); err != nil {
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
	res, _ := bus.receiveTiered(ctx, 2*time.Second)
	if _, err := bus.markHandled(ctx, "local", res.Seq, "parked", "waiting on a PR"); err != nil {
		t.Fatal(err)
	}
	if n := ackPending(t, ctx, bus.consumer); n != 0 {
		t.Errorf("a parked message must ack, ack-pending = %d", n)
	}
	got, _ := newHandledLedger(dir).fold()
	if e := got[handledKey{"local", "AGENT_INBOX", res.Seq}]; e.Disposition != "parked" || e.Reason != "waiting on a PR" {
		t.Errorf("entry = %+v", e)
	}
}

func TestBrokerCrashBetweenLedgerAndAckRedeliversAcksAndDoesNotReturnIt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, js, _, dir := handledBus(t, ctx, true, `{"handled_ack_wait":"1s"}`)
	pubEnv(t, ctx, js, mineSubject, "c1", "REQUEST", "crash me")
	pubEnv(t, ctx, js, mineSubject, "c2", "INFORM", "next")
	res, _ := bus.receiveTiered(ctx, 2*time.Second)
	// The session wrote the ledger and died before the ack.
	if _, err := newHandledLedger(dir).record(handledEvent{At: "2026-10-10T14:00:00Z", Tier: "local", Stream: "AGENT_INBOX", Seq: res.Seq, MessageID: "c1", Disposition: "handled"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond) // the ack wait passes; c1 is redelivered
	next, err := bus.receiveBatch(ctx, 3*time.Second, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range next.Items {
		if it.Env.MessageID == "c1" {
			t.Fatal("c1 is in the ledger and must not be returned again")
		}
	}
	if len(next.Items) != 1 || next.Items[0].Env.MessageID != "c2" {
		t.Fatalf("batch = %v, want only c2", ids(next.Items))
	}
	if _, err := bus.markHandled(ctx, "local", next.Items[0].Seq, "handled", ""); err != nil {
		t.Fatal(err)
	}
	if n := ackPending(t, ctx, bus.consumer); n != 0 {
		t.Errorf("c1 must be acked by the drain that found it in the ledger; ack-pending = %d", n)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "handled.jsonl"))
	if n := strings.Count(string(b), `"message_id":"c1"`); n != 1 {
		t.Errorf("c1 recorded %d times, want 1", n)
	}
}

func TestBrokerRedeliveredButUnhandledIsNotReturnedAgainAndStaysListed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, js, _, _ := handledBus(t, ctx, true, `{"handled_ack_wait":"1s"}`)
	pubEnv(t, ctx, js, mineSubject, "r1", "REQUEST", "slow work")
	if res, _ := bus.receiveTiered(ctx, 2*time.Second); res.Env == nil {
		t.Fatal("no first delivery")
	}
	time.Sleep(1500 * time.Millisecond)
	again, err := bus.receiveBatch(ctx, 2*time.Second, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Items) != 0 {
		t.Errorf("a redelivery of a message this session already holds must not be returned again: %v", ids(again.Items))
	}
	sum, err := bus.summarizeInbox(ctx, summaryDefault)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.ReadUnhandled) != 1 || sum.ReadUnhandled[0].MessageID != "r1" {
		t.Errorf("read-unhandled = %+v, want r1", sum.ReadUnhandled)
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
	if _, err := bus.markHandled(ctx, "local", sum.ReadUnhandled[0].Seq, "handled", ""); err != nil {
		t.Fatal(err)
	}
	sum, _ = bus.summarizeInbox(ctx, summaryDefault)
	if len(sum.ReadUnhandled) != 1 || sum.ReadUnhandled[0].MessageID != "s2" {
		t.Errorf("after marking s1: %+v", sum.ReadUnhandled)
	}
}

func TestBrokerMarkingAMessageThisSessionNeverHeldStillRecords(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, _, _, dir := handledBus(t, ctx, true, "")
	out, err := bus.markHandled(ctx, "local", 777, "handled", "")
	if err != nil {
		t.Fatal(err)
	}
	if out["acked"] != false {
		t.Errorf("result = %v; nothing was pending, so acked must be false and say so", out)
	}
	if got, _ := newHandledLedger(dir).fold(); len(got) != 1 {
		t.Errorf("ledger = %v, want the one entry", got)
	}
}

func TestBrokerMarkHandledRefusedWithTheModeOff(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, _, _, _ := handledBus(t, ctx, false, "")
	if _, err := bus.markHandled(ctx, "local", 1, "handled", ""); err == nil {
		t.Error("mark_handled with the mode off must refuse")
	}
}
