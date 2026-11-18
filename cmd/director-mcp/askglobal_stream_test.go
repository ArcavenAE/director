package main

import (
	"encoding/json"
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
func TestAGlobalSendIsNotJudgedAgainstTheLocalInbox(t *testing.T) {
	r := newDeliveryRig(t)
	r.covering(t)
	r.send(t, "m-local", "REQUEST") // local inbox sequence 1, which expires below

	g := envFor("m-global", "REQUEST", "agent://ops/sender", "global://director", "hello")
	body, _ := json.Marshal(g)
	m := &nats.Msg{Subject: "agent.audit", Data: body, Header: nats.Header{}}
	m.Header.Set(jetstream.MsgIDHeader, g.MessageID)
	m.Header.Set("Director-Inbox-Stream", "GLOBAL_TO_DIRECTOR")
	m.Header.Set("Director-Inbox-Subject", "global.director.inbox")
	m.Header.Set("Director-Inbox-Seq", "1")
	if _, err := r.js.PublishMsg(r.ctx, m); err != nil {
		t.Fatal(err)
	}

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
	if _, ok := r.reader.ledger.Sends["m-global"]; ok {
		t.Errorf("a send stored in GLOBAL_TO_DIRECTOR was recorded as a local inbox send")
	}
}
