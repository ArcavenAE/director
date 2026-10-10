package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// The ask reader against a scratch broker (design section 9, last paragraph):
// it creates no consumer, and a second pass over the same sequences changes
// no row.

func TestParseArgsAcceptsTheAskModesAndRefusesTheRest(t *testing.T) {
	for _, c := range []struct {
		args []string
		mode string
		bad  bool
	}{
		{[]string{"asks"}, "asks", false},
		{[]string{"asks", "--json", "--all"}, "asks", false},
		{[]string{"asks", "--help"}, "asks", false},
		{[]string{"asks", "--bogus"}, "", true},
		{[]string{"ask-reader"}, "ask-reader", false},
		{[]string{"ask-reader", "--once", "--file", "/x/y.json", "--interval", "10s"}, "ask-reader", false},
		{[]string{"ask-reader", "--interval", "30s"}, "ask-reader", false}, // the edge itself is accepted
		{[]string{"ask-reader", "--interval", "30.001s"}, "", true},
		{[]string{"ask-reader", "--interval", "31s"}, "", true},
		{[]string{"ask-reader", "--interval", "45s"}, "", true}, // slower than the 30s the design requires
		{[]string{"ask-reader", "--interval", "0s"}, "", true},
		{[]string{"ask-reader", "--bogus"}, "", true},
	} {
		got, err := parseArgs(c.args)
		if c.bad {
			if err == nil {
				t.Errorf("%v: accepted as %+v, want a refusal", c.args, got)
			}
			continue
		}
		if err != nil || got.mode != c.mode {
			t.Errorf("%v: got %+v, %v; want mode %s", c.args, got, err, c.mode)
		}
	}
	got, _ := parseArgs([]string{"ask-reader", "--once", "--file", "/x/y.json", "--interval", "10s"})
	if !got.once || got.file != "/x/y.json" || got.interval != 10*time.Second {
		t.Errorf("ask-reader flags = %+v", got)
	}
}

func provisionAudit(t *testing.T, ctx context.Context, url string) (*nats.Conn, jetstream.JetStream) {
	t.Helper()
	nc, js := provision(t, ctx, url)
	if _, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: "AGENT_AUDIT", Subjects: []string{"agent.audit"}, MaxAge: 720 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	return nc, js
}

func auditPublish(t *testing.T, ctx context.Context, js jetstream.JetStream, e Envelope, refused bool) {
	t.Helper()
	body, _ := json.Marshal(e)
	m := &nats.Msg{Subject: "agent.audit", Data: body, Header: nats.Header{}}
	m.Header.Set(jetstream.MsgIDHeader, e.MessageID)
	if refused {
		m.Header.Set("Director-Outcome", "refused")
	}
	if _, err := js.PublishMsg(ctx, m); err != nil {
		t.Fatal(err)
	}
}

func envFor(id, perf, from, to, data string, opts ...envOpt) Envelope {
	return rec(time.Now(), id, perf, from, to, data, opts...).Env
}

// streamState is the stream's message count, sequence range and byte size, so a
// publish, a delete or a purge by the reader shows.
func streamState(t *testing.T, ctx context.Context, js jetstream.JetStream, name string) string {
	t.Helper()
	s, err := js.Stream(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	st := info.State
	return fmt.Sprintf("msgs=%d bytes=%d first=%d last=%d deleted=%d", st.Msgs, st.Bytes, st.FirstSeq, st.LastSeq, st.NumDeleted)
}

func consumerCount(t *testing.T, ctx context.Context, js jetstream.JetStream, stream string) int {
	t.Helper()
	s, err := js.Stream(ctx, stream)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	l := s.ListConsumers(ctx)
	for range l.Info() {
		n++
	}
	return n
}

func TestTheReaderCreatesNoConsumerAndASecondPassChangesNoRow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provisionAudit(t, ctx, url)
	kv, _ := js.KeyValue(ctx, "AGENT_STATE")
	_, _ = kv.Put(ctx, "presence.ops.builder-1.01ABC", []byte(`{"agent_id":"builder-1","team":"ops","role":"builder","state":"idle"}`))
	auditPublish(t, ctx, js, envFor("m1", "REQUEST", "sup-1", "agent://ops/builder-1", "please build x", senderRole("supervisor")), false)
	auditPublish(t, ctx, js, envFor("m2", "AGREE", "builder-1", "agent://ops/sup-1", "ok", inReplyTo("m1"), senderRole("builder")), false)
	auditPublish(t, ctx, js, envFor("m3", "REQUEST", "sup-1", "agent://ops/builder-1", "refused one"), true)

	file := filepath.Join(t.TempDir(), "ask-ledger.json")
	r := newAskReader(js, askReaderCfg{Broker: "local", File: file})
	before := consumerCount(t, ctx, js, "AGENT_AUDIT") + consumerCount(t, ctx, js, "AGENT_INBOX")
	stateBefore := streamState(t, ctx, js, "AGENT_AUDIT")
	if err := r.Pass(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := streamState(t, ctx, js, "AGENT_AUDIT"); got != stateBefore {
		t.Fatalf("AGENT_AUDIT state changed across a pass:\n%s\n%s", stateBefore, got)
	}
	if got := consumerCount(t, ctx, js, "AGENT_AUDIT") + consumerCount(t, ctx, js, "AGENT_INBOX"); got != before {
		t.Fatalf("consumers %d -> %d: the reader created one", before, got)
	}
	if len(r.ledger.Rows) != 1 || r.ledger.Rows["m1"] == nil || r.ledger.Rows["m1"].State != askAcked {
		t.Fatalf("rows = %+v", r.ledger.Rows)
	}
	if got := r.ledger.Rows["m1"].Owner.Role; got != "builder" {
		t.Fatalf("owner role = %q, want builder from presence", got)
	}
	first, _ := json.Marshal(r.ledger.Rows)
	if err := r.Pass(ctx, time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	second, _ := json.Marshal(r.ledger.Rows)
	if !bytes.Equal(first, second) {
		t.Fatalf("a second pass changed a row:\n%s\n%s", first, second)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var doc askReport
	if err := json.Unmarshal(b, &doc); err != nil || len(doc.Rows) != 1 {
		t.Fatalf("json file: %v, %d rows", err, len(doc.Rows))
	}
	if left, _ := filepath.Glob(file + ".tmp*"); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

func TestAReaderStartedLaterResumesFromTheStoreAndReadsNothingTwice(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provisionAudit(t, ctx, url)
	auditPublish(t, ctx, js, envFor("m1", "REQUEST", "sup-1", "agent://ops/builder-1", "x"), false)
	r1 := newAskReader(js, askReaderCfg{Broker: "local", File: filepath.Join(t.TempDir(), "a.json")})
	if err := r1.Pass(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	auditPublish(t, ctx, js, envFor("m2", "AGREE", "builder-1", "agent://ops/sup-1", "ok", inReplyTo("m1")), false)
	r2 := newAskReader(js, askReaderCfg{Broker: "local", File: filepath.Join(t.TempDir(), "b.json")})
	if err := r2.Pass(ctx, time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := r2.ledger.LastSeq["local"]; got != 2 {
		t.Fatalf("last seq = %d, want 2", got)
	}
	if rows := r2.ledger.Rows; rows["m1"] == nil || rows["m1"].State != askAcked {
		t.Fatalf("rows after resume = %+v", rows)
	}
}

func TestAMissingAuditStreamIsNamedAndTheReportIsPartial(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url) // no AGENT_AUDIT
	r := newAskReader(js, askReaderCfg{Broker: "local", File: filepath.Join(t.TempDir(), "a.json")})
	if err := r.Pass(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(r.NotRead) == 0 || !strings.Contains(r.NotRead[0], "AGENT_AUDIT") {
		t.Fatalf("not read = %v", r.NotRead)
	}
	rep, err := readAskReport(ctx, js, time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Partial || len(rep.Gaps.StreamsNotRead) == 0 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestAsksReadsTheStoreAndRunsNoPassOfItsOwn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provisionAudit(t, ctx, url)
	auditPublish(t, ctx, js, envFor("m1", "REQUEST", "sup-1", "agent://ops/builder-1", "please build x"), false)
	// Nothing has run a pass: the store is empty, and asks says the reader is down.
	var out bytes.Buffer
	if code := runAsksCmd(ctx, url, cliArgs{mode: "asks", json: true}, &out, &out); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	var empty askReport
	if err := json.Unmarshal(out.Bytes(), &empty); err != nil || len(empty.Rows) != 0 {
		t.Fatalf("asks before any pass: %v %s", err, out.String())
	}
	r := newAskReader(js, askReaderCfg{Broker: "local", File: filepath.Join(t.TempDir(), "a.json")})
	if err := r.Pass(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := runAsksCmd(ctx, url, cliArgs{mode: "asks", json: true}, &out, &out); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var rep askReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil || len(rep.Rows) != 1 || rep.Rows[0].Ask != "m1" {
		t.Fatalf("asks after a pass: %v %s", err, out.String())
	}
	out.Reset()
	_ = runAsksCmd(ctx, url, cliArgs{mode: "asks"}, &out, &out)
	for _, w := range []string{"1 rows", "1. alarms", "2. blocked on whom", "3. rollup per owner role", "unacked 1", "4. gaps"} {
		if !strings.Contains(out.String(), w) {
			t.Fatalf("text output lacks %q:\n%s", w, out.String())
		}
	}
}

func TestAsksHelpPrintsTheOrphanNoteAndExitsZero(t *testing.T) {
	var out bytes.Buffer
	if code := runAsksCmd(context.Background(), "nats://127.0.0.1:1", cliArgs{mode: "asks", help: true}, &out, &out); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "ack timeout") {
		t.Fatalf("help = %q", out.String())
	}
}

func TestTheReaderResolvesAGlobalOwnerFromTheHubsPresenceAndNamesAnUnreadableHub(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, js := provisionAudit(t, ctx, url)
	gjs, err := jetstream.NewWithDomain(nc, "global")
	if err != nil {
		t.Fatal(err)
	}
	gkv, _ := gjs.KeyValue(ctx, globalPresenceBucket)
	_, _ = gkv.Put(ctx, "presence.cluster-b.supervisor.01ABC", []byte(`{"cluster":"cluster-b","role":"supervisor","agent_id":"sup-b","state":"idle"}`))
	auditPublish(t, ctx, js, envFor("m1", "REQUEST", "sup-1", "global://cluster-b/supervisor", "x"), false)
	auditPublish(t, ctx, js, envFor("m2", "REQUEST", "sup-1", "global://cluster-c/supervisor", "y"), false)

	r := newAskReader(js, askReaderCfg{Broker: "local", File: filepath.Join(t.TempDir(), "a.json"), Hub: gjs})
	if err := r.Pass(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if o := r.ledger.Rows["m1"].Owner; o.Team != "cluster-b" || o.Role != "supervisor" {
		t.Fatalf("m1 owner = %+v, want cluster-b/supervisor", o)
	}
	if o := r.ledger.Rows["m2"].Owner; o.Role != roleUnresolved {
		t.Fatalf("m2 owner = %+v, want unresolved (no presence for cluster-c)", o)
	}
	if len(r.NotRead) != 0 {
		t.Fatalf("not read = %v, want none", r.NotRead)
	}

	// A hub that cannot be read is named, and does not mark every global entry expired.
	bad, _ := jetstream.NewWithDomain(nc, "nohub")
	r2 := newAskReader(js, askReaderCfg{Broker: "local", File: filepath.Join(t.TempDir(), "b.json"), Hub: bad})
	short, c2 := context.WithTimeout(ctx, 5*time.Second)
	defer c2()
	if err := r2.Pass(short, time.Now()); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range r2.NotRead {
		if strings.Contains(n, globalPresenceBucket) {
			found = true
		}
	}
	if !found {
		t.Fatalf("not read = %v, want %s named", r2.NotRead, globalPresenceBucket)
	}
}
