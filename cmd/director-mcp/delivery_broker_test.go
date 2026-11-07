package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// These run the sender's delivery signal end to end on a scratch nats-server:
// a real send through Bus.publish (stamped by the audit mirror), a reader pass
// into the ledger, and asks --id reading the inbox stream live. The inbox keeps
// mail for 2 seconds so an expiry is observed, not simulated.

const seatSubj = "agent.aae-orc.ops.seat.inbox"

type deliveryRig struct {
	ctx    context.Context
	url    string
	js     jetstream.JetStream
	bus    *Bus
	reader *askReader
}

func newDeliveryRig(t *testing.T) *deliveryRig {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	url := startScratchServer(t)
	_, js := provisionAudit(t, ctx, url)
	st, err := js.Stream(ctx, "AGENT_INBOX")
	if err != nil {
		t.Fatal(err)
	}
	cfg := st.CachedInfo().Config
	cfg.MaxAge, cfg.Duplicates = 2*time.Second, time.Second
	if _, err := js.UpdateStream(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	bus, err := connect(ctx, url, Sender{AgentID: "sender", Workspace: "aae-orc", Team: "ops"}, &globalConfig{Domain: "global", Cluster: "kinu", Role: roleDirector})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bus.close)
	return &deliveryRig{ctx: ctx, url: url, js: js, bus: bus,
		reader: newAskReader(js, askReaderCfg{Broker: "local", File: filepath.Join(t.TempDir(), "a.json")})}
}

// send publishes one envelope to the seat through the real send path.
func (r *deliveryRig) send(t *testing.T, id, perf string) uint64 {
	t.Helper()
	e := envFor(id, perf, "agent://ops/sender", "agent://ops/seat", "hello")
	// one test sends before any durable exists, which a hinted send refuses unless cold
	res, err := r.bus.publish(context.WithValue(r.ctx, coldSendKey{}, true), &e, "aae-orc")
	if err != nil {
		t.Fatal(err)
	}
	return res.Sequence
}

// covering makes a durable reader of exactly the seat's subject.
func (r *deliveryRig) covering(t *testing.T) jetstream.Consumer {
	t.Helper()
	c, err := r.js.CreateOrUpdateConsumer(r.ctx, "AGENT_INBOX", jetstream.ConsumerConfig{
		Durable: "mcp_seat_1", FilterSubject: seatSubj, AckPolicy: jetstream.AckExplicitPolicy})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func readAndAck(t *testing.T, c jetstream.Consumer, n int) {
	t.Helper()
	b, err := c.Fetch(n, jetstream.FetchMaxWait(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	got := 0
	for m := range b.Messages() {
		if err := m.DoubleAck(context.Background()); err != nil {
			t.Fatal(err)
		}
		got++
	}
	if got != n {
		t.Fatalf("read %d messages, want %d", got, n)
	}
}

func (r *deliveryRig) lookup(t *testing.T, id string) askLookup {
	t.Helper()
	if err := r.reader.Pass(r.ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := runAsksCmd(r.ctx, r.url, cliArgs{mode: "asks", askID: id, json: true}, &out, &out); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	var res askLookup
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("asks --id output: %v\n%s", err, out.String())
	}
	return res
}

func wantDelivery(t *testing.T, res askLookup, state string) {
	t.Helper()
	if res.Delivery == nil || res.Delivery.State != state {
		t.Fatalf("delivery = %+v, want %s (%+v)", res.Delivery, state, res)
	}
}

func TestTheSendIsStampedWithWhereTheInboxStoredIt(t *testing.T) {
	r := newDeliveryRig(t)
	seq := r.send(t, "m-stamp", "INFORM")
	st, _ := r.js.Stream(r.ctx, "AGENT_AUDIT")
	msg, err := st.GetMsg(r.ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := msg.Header.Get("Director-Inbox-Subject"); got != seatSubj {
		t.Errorf("Director-Inbox-Subject = %q, want %q", got, seatSubj)
	}
	if got := msg.Header.Get("Director-Inbox-Seq"); got != "1" || seq != 1 {
		t.Errorf("Director-Inbox-Seq = %q (ack %d), want 1", got, seq)
	}
}

func TestAnInformIsReadByACoveringDurable(t *testing.T) {
	r := newDeliveryRig(t)
	c := r.covering(t)
	r.send(t, "m-inform", "INFORM")
	wantDelivery(t, r.lookup(t, "m-inform"), deliveryNotYetRead)
	readAndAck(t, c, 1)
	res := r.lookup(t, "m-inform")
	wantDelivery(t, res, deliveryRead)
	if res.Found {
		t.Errorf("an INFORM opened an ask row: %+v", res)
	}
}

// The case the reply-based lookup could not tell from a message never
// delivered: read, not answered.
func TestARequestReadButNotRepliedToReadsAsRead(t *testing.T) {
	r := newDeliveryRig(t)
	c := r.covering(t)
	r.send(t, "m-req", "REQUEST")
	readAndAck(t, c, 1)
	res := r.lookup(t, "m-req")
	wantDelivery(t, res, deliveryRead)
	if !res.Found || !strings.Contains(res.Verdict, "no acknowledgement yet") {
		t.Errorf("the reply verdict changed: %q", res.Verdict)
	}
}

func TestASendNobodyReadsExpires(t *testing.T) {
	r := newDeliveryRig(t)
	r.covering(t)
	r.send(t, "m-lost", "INFORM")
	wantDelivery(t, r.lookup(t, "m-lost"), deliveryNotYetRead)
	time.Sleep(3 * time.Second)
	wantDelivery(t, r.lookup(t, "m-lost"), deliveryExpiredUnrd)
}

func TestThePassMarksAnExpiredRequestOnce(t *testing.T) {
	r := newDeliveryRig(t)
	r.covering(t)
	r.send(t, "m-ask", "REQUEST")
	if err := r.reader.Pass(r.ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Second)
	late := time.Now().Add(alarmAfter + time.Minute)
	if err := r.reader.Pass(r.ctx, late); err != nil {
		t.Fatal(err)
	}
	row := r.reader.ledger.Rows["m-ask"]
	if row == nil || row.ExpiredUnread == "" {
		t.Fatalf("the pass did not mark the expired ask: %+v", row)
	}
	first := row.ExpiredUnread
	if err := r.reader.Pass(r.ctx, late.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if again := r.reader.ledger.Rows["m-ask"].ExpiredUnread; again != first {
		t.Errorf("the mark moved from %s to %s: it must be raised once", first, again)
	}
	// The existing alarm path carries it: the report, read at the later time
	// the pass ran, lists the ask with the expiry in its text.
	var texts []string
	for _, a := range r.reader.ledger.Report(late.Add(time.Minute), nil, false).Alarms {
		texts = append(texts, a.Text)
	}
	if len(texts) != 1 || !strings.Contains(texts[0], "expired unread") {
		t.Errorf("alarms = %q, want one saying expired unread", texts)
	}
}
