package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// #669 review. A global send's audit copy now names the hub stream, subject and
// sequence it was stored under. The delivery check reads only AGENT_INBOX, so a
// hub sequence judged against the local inbox's first sequence would read a
// global REQUEST as expired unread when nothing expired. A copy that names a
// stream other than AGENT_INBOX must stay out of that check.
// publishGlobalAuditCopy writes the audit copy a global REQUEST leaves: the hub
// stream, subject and sequence 1.
func publishGlobalAuditCopy(t *testing.T, r *deliveryRig, id string) {
	t.Helper()
	g := envFor(id, "REQUEST", "agent://ops/sender", "global://director", "hello")
	body, _ := json.Marshal(g)
	m := &nats.Msg{Subject: "agent.audit", Data: body, Header: nats.Header{}}
	m.Header.Set(jetstream.MsgIDHeader, g.MessageID)
	m.Header.Set("Director-Inbox-Stream", "GLOBAL_TO_DIRECTOR")
	m.Header.Set("Director-Inbox-Subject", "global.director.inbox")
	m.Header.Set("Director-Inbox-Seq", "1")
	if _, err := r.js.PublishMsg(r.ctx, m); err != nil {
		t.Fatal(err)
	}
}

func TestAGlobalSendIsNotJudgedAgainstTheLocalInbox(t *testing.T) {
	r := newDeliveryRig(t)
	r.covering(t)
	r.send(t, "m-local", "REQUEST") // local inbox sequence 1, which expires below

	publishGlobalAuditCopy(t, r, "m-global")

	if err := r.reader.Pass(r.ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Second) // the local inbox drops sequence 1
	late := time.Now().Add(alarmAfter + time.Minute)
	if err := r.reader.Pass(r.ctx, late); err != nil {
		t.Fatal(err)
	}

	local := r.reader.ledger.Rows["m-local"]
	if local == nil || local.ExpiredUnread == "" {
		t.Fatalf("control: the local send was not marked expired unread: %+v", local)
	}
	global := r.reader.ledger.Rows["m-global"]
	if global == nil {
		t.Fatalf("the global REQUEST opened no row; rows = %v", r.reader.ledger.Rows)
	}
	if global.ExpiredUnread != "" {
		t.Errorf("the global send was marked expired unread at %s by comparing hub sequence 1 with the local inbox", global.ExpiredUnread)
	}
	if len(r.reader.NotRead) != 0 {
		t.Errorf("the pass reported unread items for a send it cannot judge: %v", r.reader.NotRead)
	}
	// asks --id reads the same check live: it must not say a hub send expired
	// because of the local inbox's first sequence.
	if d := r.lookup(t, "m-global").Delivery; d == nil || d.State != deliveryUnknown {
		t.Errorf("asks --id delivery for the global send = %+v, want %s (the check reads AGENT_INBOX only)", d, deliveryUnknown)
	}
}

// The text form of asks --id is what a person reads. For a send stored in a hub
// stream it must say why delivery is unknown, not repeat the line for a local
// send ("the inbox's own read, not a reply"), which is wrong for a hub send.
func TestAsksIDTextNamesWhyAGlobalSendsDeliveryIsUnknown(t *testing.T) {
	r := newDeliveryRig(t)
	publishGlobalAuditCopy(t, r, "m-global-text")
	if err := r.reader.Pass(r.ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := runAsksCmd(r.ctx, r.url, cliArgs{mode: "asks", askID: "m-global-text"}, &out, &out); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	got := out.String()
	if !strings.Contains(got, "delivery: unknown") {
		t.Fatalf("no unknown delivery line for a global send:\n%s", got)
	}
	if !strings.Contains(got, "GLOBAL_TO_DIRECTOR") {
		t.Errorf("the delivery line does not say which stream holds the send:\n%s", got)
	}
	if strings.Contains(got, "the inbox's own read") {
		t.Errorf("a hub-stored send is described as the inbox's own read:\n%s", got)
	}
}
