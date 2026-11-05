package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// What became of a send, read from the inbox stream itself (aae-orc-t77rr,
// item 3). The states and their order are the architect's spec:
// read, then expired_unread, then no_reader, then not_yet_read, and unknown
// whenever the stream or its consumers could not be read.

func ci(cfg jetstream.ConsumerConfig, ackFloor uint64) *jetstream.ConsumerInfo {
	return &jetstream.ConsumerInfo{Config: cfg, AckFloor: jetstream.SequenceInfo{Stream: ackFloor}}
}

const subjA = "agent.aae-orc.ops.a.inbox"

func TestDeliveryOfReadsTheStreamAndTheCoveringDurables(t *testing.T) {
	durable := func(floor uint64) *jetstream.ConsumerInfo {
		return ci(jetstream.ConsumerConfig{Durable: "mcp_a_1", FilterSubject: subjA}, floor)
	}
	cases := []struct {
		name string
		send sendRec
		view inboxView
		want string
	}{
		{"read: a covering durable's floor reached it", sendRec{Seq: 5, Subject: subjA}, inboxView{First: 1, Consumers: []*jetstream.ConsumerInfo{durable(5)}}, "read"},
		{"read: and still read after the stream aged past it", sendRec{Seq: 5, Subject: subjA}, inboxView{First: 9, Consumers: []*jetstream.ConsumerInfo{durable(7)}}, "read"},
		{"not yet read: in the stream, floor behind", sendRec{Seq: 5, Subject: subjA}, inboxView{First: 1, Consumers: []*jetstream.ConsumerInfo{durable(4)}}, "not_yet_read"},
		{"expired unread: the stream aged past it, floor behind", sendRec{Seq: 5, Subject: subjA}, inboxView{First: 6, Consumers: []*jetstream.ConsumerInfo{durable(4)}}, "expired_unread"},
		{"expired unread: aged out and no durable ever covered it", sendRec{Seq: 5, Subject: subjA}, inboxView{First: 6}, "expired_unread"},
		{"no reader: still in the stream and nothing covers it", sendRec{Seq: 5, Subject: subjA}, inboxView{First: 1}, "no_reader"},
		{"an ephemeral consumer's floor is not a read", sendRec{Seq: 5, Subject: subjA}, inboxView{First: 1, Consumers: []*jetstream.ConsumerInfo{
			ci(jetstream.ConsumerConfig{Name: "eph", FilterSubject: subjA}, 9)}}, "no_reader"},
		{"a wildcard durable's floor is not a read", sendRec{Seq: 5, Subject: subjA}, inboxView{First: 1, Consumers: []*jetstream.ConsumerInfo{
			ci(jetstream.ConsumerConfig{Durable: "wild", FilterSubject: "agent.aae-orc.ops.>"}, 9)}}, "no_reader"},
		{"another seat's durable is not a read", sendRec{Seq: 5, Subject: subjA}, inboxView{First: 1, Consumers: []*jetstream.ConsumerInfo{
			ci(jetstream.ConsumerConfig{Durable: "other", FilterSubject: "agent.aae-orc.ops.b.inbox"}, 9)}}, "no_reader"},
		{"unknown: the stream could not be read", sendRec{Seq: 5, Subject: subjA}, inboxView{Err: errors.New("stream info: timeout")}, "unknown"},
		{"unknown: the consumers could not be listed and none reached it", sendRec{Seq: 5, Subject: subjA}, inboxView{First: 1, ConsumersErr: errors.New("list: timeout")}, "unknown"},
	}
	for _, c := range cases {
		got := deliveryOf(c.send, c.view)
		if got.State != c.want {
			t.Errorf("%s: state = %q, want %q", c.name, got.State, c.want)
		}
	}
	if got := deliveryOf(sendRec{Seq: 5, Subject: subjA}, inboxView{Err: errors.New("stream info: timeout")}); !strings.Contains(got.Error, "timeout") {
		t.Errorf("unknown carries no error: %+v", got)
	}
}

func TestSendRecordsPruneAtTheInboxMaxAgePlusADay(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	l := newAskLedger()
	l.Sends["keep"] = sendRec{Seq: 1, At: now.Add(-inboxMaxAge - 23*time.Hour)}
	l.Sends["drop"] = sendRec{Seq: 2, At: now.Add(-inboxMaxAge - 25*time.Hour)}
	l.Sweep(now)
	if _, ok := l.Sends["keep"]; !ok {
		t.Error("a send inside inboxMaxAge plus a day was pruned")
	}
	if _, ok := l.Sends["drop"]; ok {
		t.Error("a send past inboxMaxAge plus a day was kept")
	}
}
