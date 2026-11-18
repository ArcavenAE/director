package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// #248, second slice. The ask reader finds where a send was stored from the
// Director-Inbox-* headers on its audit copy. A local copy named a subject and a
// seq and no stream, and a global copy named nothing, although the hub's
// publish ack had all three in hand. These run against a scratch broker.

// lastAudit reads the newest record on the local audit stream.
func lastAudit(t *testing.T, ctx context.Context, audit jetstream.Stream) *jetstream.RawStreamMsg {
	t.Helper()
	info, err := audit.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.State.Msgs == 0 {
		t.Fatal("the audit stream holds no record")
	}
	rec, err := audit.GetMsg(ctx, info.State.LastSeq)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

// globalSendRig attaches the scratch broker's hub domain to a Bus, with a live
// director row, and returns the Bus and the local audit stream.
func globalSendRig(t *testing.T, ctx context.Context) (*Bus, jetstream.Stream) {
	t.Helper()
	url := startScratchServer(t)
	nc, js := provision(t, ctx, url)
	audit := createAudit(t, ctx, js)
	gjs, err := jetstream.NewWithDomain(nc, "global")
	if err != nil {
		t.Fatal(err)
	}
	kv, err := gjs.KeyValue(ctx, globalPresenceBucket)
	if err != nil {
		t.Fatal(err)
	}
	putRow(t, ctx, kv, "presence.director.01AAAAAAAAAAAAAAAAAAAAAAAA", directorRow("director", "01AAAAAAAAAAAAAAAAAAAAAAAA"))
	cfg := globalConfig{Domain: "global", Cluster: "c1", Role: roleSupervisor}
	bus, err := connect(ctx, url, Sender{AgentID: "sup", Workspace: "w", Team: "t"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bus.close)
	bus.globalCfg = &cfg
	bus.global = &globalTier{cfg: cfg, js: gjs, kv: kv}
	return bus, audit
}

func TestGlobalSendAuditRecordCarriesInboxStreamAndSeq(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, audit := globalSendRig(t, ctx)

	e := &Envelope{MessageID: "m-global-hdr", Recipient: Recipient{Address: "global://director"}}
	res, err := bus.publishGlobalOnce(ctx, e, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	rec := lastAudit(t, ctx, audit)
	if got := rec.Header.Get("Director-Inbox-Stream"); got != "GLOBAL_TO_DIRECTOR" {
		t.Errorf("Director-Inbox-Stream = %q, want GLOBAL_TO_DIRECTOR", got)
	}
	if got, want := rec.Header.Get("Director-Inbox-Seq"), strconv.FormatUint(res.Sequence, 10); got != want {
		t.Errorf("Director-Inbox-Seq = %q, want the hub's sequence %s", got, want)
	}
}

// An addition beyond the plan's two named headers: the subject the hub stored
// the send under, as the local copy already names its subject.
func TestGlobalSendAuditRecordCarriesTheHubSubject(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, audit := globalSendRig(t, ctx)

	e := &Envelope{MessageID: "m-global-subj", Recipient: Recipient{Address: "global://director"}}
	if _, err := bus.publishGlobalOnce(ctx, e, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	rec := lastAudit(t, ctx, audit)
	if got := rec.Header.Get("Director-Inbox-Subject"); got != "global.director.inbox" {
		t.Errorf("Director-Inbox-Subject = %q, want global.director.inbox", got)
	}
}

func TestLocalSendAuditRecordNamesTheInboxStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	audit := createAudit(t, ctx, js)
	sender := liveSeatNoCleanup(t, ctx, url, Sender{AgentID: "sup", Workspace: "w", Team: "t"})
	liveSeatNoCleanup(t, ctx, url, Sender{AgentID: "a", Workspace: "w", Team: "t"})

	raw, _ := json.Marshal(map[string]any{"to": "agent://t/a", "performative": "INFORM", "text": "hi"})
	out, err := toolSend(ctx, sender, raw)
	if err != nil {
		t.Fatal(err)
	}
	m := asMap(t, out)
	rec := lastAudit(t, ctx, audit)
	if got := rec.Header.Get("Director-Inbox-Stream"); got != "AGENT_INBOX" {
		t.Errorf("Director-Inbox-Stream = %q, want AGENT_INBOX", got)
	}
	if got, want := rec.Header.Get("Director-Inbox-Seq"), strconv.FormatUint(m["sequence"].(uint64), 10); got != want {
		t.Errorf("Director-Inbox-Seq = %q, want %s (unchanged)", got, want)
	}
}

// The plan's second test for this slice: the ask reader must still ingest an
// audit record that carries Director-Inbox-Stream, and one that does not (the
// old shape). It reads only -Seq and -Subject, so this holds at b97d1ec already
// and is a pin, not a red test; the unknown header is what this slice adds.
func TestAskReaderIgnoresAnUnknownInboxStreamHeader(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provisionAudit(t, ctx, url)

	pub := func(e Envelope, headers map[string]string) {
		t.Helper()
		body, _ := json.Marshal(e)
		m := &nats.Msg{Subject: "agent.audit", Data: body, Header: nats.Header{}}
		m.Header.Set(jetstream.MsgIDHeader, e.MessageID)
		for k, v := range headers {
			m.Header.Set(k, v)
		}
		if _, err := js.PublishMsg(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	pub(envFor("m-new", "REQUEST", "sup-1", "agent://ops/builder-1", "x"), map[string]string{
		"Director-Inbox-Stream":  "GLOBAL_TO_DIRECTOR",
		"Director-Inbox-Subject": "global.director.inbox",
		"Director-Inbox-Seq":     "7",
	})
	pub(envFor("m-old", "REQUEST", "sup-1", "agent://ops/builder-1", "y"), map[string]string{
		"Director-Inbox-Subject": "agent.ops.builder-1.inbox",
		"Director-Inbox-Seq":     "3",
	})

	r := newAskReader(js, askReaderCfg{Broker: "local", File: filepath.Join(t.TempDir(), "a.json")})
	if err := r.Pass(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"m-new", "m-old"} {
		if r.ledger.Rows[id] == nil {
			t.Errorf("row %s was not ingested; rows = %v", id, r.ledger.Rows)
		}
	}
	if len(r.NotRead) != 0 {
		t.Errorf("the pass left records unread: %v", r.NotRead)
	}
}
