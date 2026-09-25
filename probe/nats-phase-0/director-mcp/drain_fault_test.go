package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// Fault-path tests for the batch drain (director#79 review, findings 1 and 3).
// A scripted consumer stands in for the durable so an ack failure or a failed
// fetch can land at an exact point in a batch. The rule under test: a message
// the shim has already acked, or may have acked, is returned to the caller,
// never dropped because something after it failed.

type fakeMsg struct {
	jetstream.Msg // unused methods panic if reached
	data          []byte
	seq           uint64
	ackErr        error
	acked, termed bool
}

func (m *fakeMsg) Data() []byte { return m.data }
func (m *fakeMsg) Metadata() (*jetstream.MsgMetadata, error) {
	return &jetstream.MsgMetadata{Sequence: jetstream.SequencePair{Stream: m.seq}}, nil
}
func (m *fakeMsg) DoubleAck(context.Context) error { m.acked = m.ackErr == nil; return m.ackErr }
func (m *fakeMsg) Ack() error                      { m.acked = true; return nil }
func (m *fakeMsg) Term() error                     { m.termed = true; return nil }

type fakeBatch struct {
	ch  chan jetstream.Msg
	err error
}

func (b *fakeBatch) Messages() <-chan jetstream.Msg { return b.ch }
func (b *fakeBatch) Error() error                   { return b.err }

func batchOf(err error, msgs ...*fakeMsg) *fakeBatch {
	ch := make(chan jetstream.Msg, len(msgs))
	for _, m := range msgs {
		ch <- m
	}
	close(ch)
	return &fakeBatch{ch: ch, err: err}
}

// step is one scripted fetch result: a batch, or an error from the call itself.
type step struct {
	batch *fakeBatch
	err   error
}

type fakeConsumer struct {
	jetstream.Consumer
	noWait []step // FetchNoWait results, in order; empty batches once exhausted
	wait   []step // Fetch results, in order
}

func (c *fakeConsumer) next(q *[]step) (jetstream.MessageBatch, error) {
	if len(*q) == 0 {
		return batchOf(nil), nil
	}
	s := (*q)[0]
	*q = (*q)[1:]
	if s.err != nil {
		return nil, s.err
	}
	return s.batch, nil
}
func (c *fakeConsumer) FetchNoWait(int) (jetstream.MessageBatch, error) { return c.next(&c.noWait) }
func (c *fakeConsumer) Fetch(int, ...jetstream.FetchOpt) (jetstream.MessageBatch, error) {
	return c.next(&c.wait)
}

func fmsg(t *testing.T, id string, seq uint64) *fakeMsg {
	t.Helper()
	b, err := json.Marshal(testEnv(id, "s", "INFORM", "x"))
	if err != nil {
		t.Fatal(err)
	}
	return &fakeMsg{data: b, seq: seq}
}

func busWith(c *fakeConsumer) *Bus {
	return &Bus{consumer: c, self: Sender{AgentID: "michael", Workspace: "aae-orc", Team: "ops"}}
}

// An ack that fails on the second of three messages must not drop the first,
// and must not strand the third ack-pending behind newer mail: all three come
// back, the second flagged as unconfirmed.
func TestBatchAckFailureMidBatchReturnsEverything(t *testing.T) {
	m1, m2, m3 := fmsg(t, "a", 1), fmsg(t, "b", 2), fmsg(t, "c", 3)
	m2.ackErr = errors.New("ack timeout")
	bus := busWith(&fakeConsumer{noWait: []step{{batch: batchOf(nil, m1, m2, m3)}}})
	res, err := bus.receiveBatch(context.Background(), time.Second, 10)
	if err != nil {
		t.Fatalf("an ack failure after consuming messages must not fail the call: %v", err)
	}
	if got := ids(res.Items); len(got) != 3 {
		t.Fatalf("items = %v, want all three", got)
	}
	if !res.Items[1].AckUnconfirmed || res.Items[0].AckUnconfirmed || res.Items[2].AckUnconfirmed {
		t.Errorf("only the second item should be flagged ack_unconfirmed: %+v", res.Items)
	}
	if !m3.acked {
		t.Error("the message after a failed ack was never acked; it will redeliver behind newer mail")
	}
	if res.LocalWarn == "" {
		t.Error("the ack failure should surface as a local warning")
	}
}

// A second fetch that fails after the first one returned acked messages keeps
// those messages and reports the failure as a warning.
func TestBatchSecondFetchFailureKeepsAckedMessages(t *testing.T) {
	m1, m2 := fmsg(t, "a", 1), fmsg(t, "b", 2)
	bus := busWith(&fakeConsumer{noWait: []step{
		{batch: batchOf(nil, m1, m2)},
		{err: errors.New("connection reset")},
	}})
	res, err := bus.receiveBatch(context.Background(), time.Second, 10)
	if err != nil {
		t.Fatalf("messages were already acked; the call must return them, got error %v", err)
	}
	if got := ids(res.Items); len(got) != 2 {
		t.Fatalf("items = %v, want the two acked before the failure", got)
	}
	if res.LocalWarn == "" {
		t.Error("the failed fetch should surface as a local warning")
	}
}

// In the blocking path the first message is acked by the long poll; a failed
// top-up afterwards must not drop it.
func TestBatchTopUpFailureKeepsBlockingMessage(t *testing.T) {
	first := fmsg(t, "first", 7)
	bus := busWith(&fakeConsumer{
		noWait: []step{{batch: batchOf(nil)}, {err: errors.New("connection reset")}},
		wait:   []step{{batch: batchOf(nil, first)}},
	})
	res, err := bus.receiveBatch(context.Background(), time.Second, 10)
	if err != nil {
		t.Fatalf("the first message was acked; the call must return it, got error %v", err)
	}
	if got := ids(res.Items); len(got) != 1 || res.Items[0].Seq != 7 {
		t.Fatalf("items = %v, want the one blocking message", got)
	}
	if res.LocalWarn == "" {
		t.Error("the failed top-up should surface as a local warning")
	}
}

// With nothing consumed, a fetch failure is still an error: there is nothing
// to return and the caller must hear it.
func TestBatchFailureWithNothingConsumedIsAnError(t *testing.T) {
	bus := busWith(&fakeConsumer{noWait: []step{{err: errors.New("connection reset")}}})
	if _, err := bus.receiveBatch(context.Background(), time.Second, 10); err == nil {
		t.Fatal("a failure with nothing consumed should be an error")
	}
}
