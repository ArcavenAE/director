package main

// The cue's FOR-filter peek (director#572) reads the global messages a seat has
// not acked without touching the seat's durable consumer. It does that with an
// ephemeral AckNone pull consumer that starts just above the durable's ack
// floor, and it deletes the consumer afterwards. This test proves a leaf
// credential, with the real hubclasses/leaf.conf grants, may do exactly that on
// its own GLOBAL_TO_<cluster> stream across a leafnode link, and that the
// durable's counts do not move.

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func TestLeafKeyMayPeekItsOwnInboxWithAnAckNoneEphemeralConsumer(t *testing.T) {
	_, url, _ := leafnodeFixture(t, "leaf")
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := jetstream.NewWithDomain(nc, "global")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st, err := js.Stream(ctx, "GLOBAL_TO_probe")
	if err != nil {
		t.Fatalf("stream info on its own GLOBAL_TO_probe: %v", err)
	}
	durable, err := st.Consumer(ctx, "mcp_global_probe_01AAA")
	if err != nil {
		t.Fatalf("consumer info on the seat's durable: %v", err)
	}
	before := durable.CachedInfo()
	// The seat's own consumer filter is the peek's filter, and the peek starts
	// one above the durable's ack floor.
	peek, err := st.CreateConsumer(ctx, jetstream.ConsumerConfig{
		AckPolicy:     jetstream.AckNonePolicy,
		DeliverPolicy: jetstream.DeliverByStartSequencePolicy,
		OptStartSeq:   before.AckFloor.Stream + 1,
		FilterSubject: before.Config.FilterSubject,
	})
	if err != nil {
		t.Fatalf("create an AckNone start-sequence ephemeral consumer on GLOBAL_TO_probe: %v", err)
	}
	name := peek.CachedInfo().Name
	if name == "" {
		t.Fatal("the ephemeral consumer has no name")
	}
	batch, err := peek.FetchNoWait(10)
	if err != nil {
		t.Fatalf("fetch on the ephemeral consumer: %v", err)
	}
	n := 0
	for range batch.Messages() {
		n++
	}
	if err := batch.Error(); err != nil {
		t.Fatalf("fetch on the ephemeral consumer ended with: %v", err)
	}
	if n != 3 {
		t.Errorf("the peek read %d messages, want the 3 the durable has not acked", n)
	}
	// The durable did not move: no delivery, no ack pending.
	after, err := durable.Info(ctx)
	if err != nil {
		t.Fatalf("consumer info on the durable after the peek: %v", err)
	}
	if after.NumPending != before.NumPending || after.NumAckPending != before.NumAckPending ||
		after.Delivered.Stream != before.Delivered.Stream || after.AckFloor.Stream != before.AckFloor.Stream {
		t.Errorf("the durable moved: pending %d->%d, ack pending %d->%d, delivered %d->%d, ack floor %d->%d",
			before.NumPending, after.NumPending, before.NumAckPending, after.NumAckPending,
			before.Delivered.Stream, after.Delivered.Stream, before.AckFloor.Stream, after.AckFloor.Stream)
	}
	if err := st.DeleteConsumer(ctx, name); err != nil {
		t.Fatalf("delete the ephemeral consumer: %v", err)
	}
	if _, err := st.Consumer(ctx, name); err == nil {
		t.Errorf("consumer %s is still answered after the delete", name)
	}
	// Control: the credential is the restricted leaf class and not an open one.
	// A read of a message by sequence, which the peek does not use, is refused.
	if !refused(nc, "$JS.global.API.STREAM.MSG.GET.GLOBAL_TO_probe", `{"seq":1}`) {
		t.Errorf("STREAM.MSG.GET on GLOBAL_TO_probe was answered for a plain leaf")
	}
}
