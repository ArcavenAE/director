package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// globalTier.expiredUnread makes three reads: the seat's consumer info, the
// stream, and the stream's info. Each must say nothing when it fails, so each
// gets a test where only that read fails and a control where none does.
// The fakes embed the interfaces and answer only the calls the check makes.

type fakeCons struct {
	jetstream.Consumer
	read uint64
	err  error
}

func (c fakeCons) Info(context.Context) (*jetstream.ConsumerInfo, error) {
	if c.err != nil {
		return nil, c.err
	}
	return &jetstream.ConsumerInfo{Delivered: jetstream.SequenceInfo{Stream: c.read}}, nil
}

type fakeStream struct {
	jetstream.Stream
	first uint64
	err   error
}

func (s fakeStream) Info(context.Context, ...jetstream.StreamInfoOpt) (*jetstream.StreamInfo, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &jetstream.StreamInfo{State: jetstream.StreamState{FirstSeq: s.first}, Config: jetstream.StreamConfig{MaxAge: 2 * time.Second}}, nil
}

type fakeJS struct {
	jetstream.JetStream
	st  fakeStream
	err error
}

func (j fakeJS) Stream(context.Context, string) (jetstream.Stream, error) {
	if j.err != nil {
		return nil, j.err
	}
	return j.st, nil
}

func TestGlobalExpiredUnreadStaysQuietWhenAnyOneReadFails(t *testing.T) {
	boom := errors.New("refused")
	gap := func() *globalTier {
		return &globalTier{
			cfg:      globalConfig{Domain: "global", Cluster: "c1", Role: roleSupervisor},
			js:       fakeJS{st: fakeStream{first: 5}},
			consumer: fakeCons{read: 1},
		}
	}
	// Control: with every read answering, the same fixture reports the gap, so
	// the cases below are quiet because of the failing read and not the fixture.
	if note := gap().expiredUnread(context.Background()); !strings.Contains(note, "sequences 2 to 4") || !strings.Contains(note, "GLOBAL_TO_c1") {
		t.Fatalf("control: note = %q, want the window 2 to 4 on GLOBAL_TO_c1", note)
	}
	cases := []struct {
		name string
		mod  func(*globalTier)
	}{
		{"the consumer info read fails", func(g *globalTier) { g.consumer = fakeCons{read: 1, err: boom} }},
		{"the stream read fails", func(g *globalTier) { g.js = fakeJS{st: fakeStream{first: 5}, err: boom} }},
		{"the stream info read fails", func(g *globalTier) { g.js = fakeJS{st: fakeStream{first: 5, err: boom}} }},
	}
	for _, c := range cases {
		g := gap()
		c.mod(g)
		if note := g.expiredUnread(context.Background()); note != "" {
			t.Errorf("%s: note = %q, want none", c.name, note)
		}
		// A failed read must not use up the once-per-gap report.
		g2 := gap()
		c.mod(g2)
		_ = g2.expiredUnread(context.Background())
		g2.js, g2.consumer = gap().js, gap().consumer
		if note := g2.expiredUnread(context.Background()); note == "" {
			t.Errorf("%s: a later successful read was silenced by the failed one", c.name)
		}
	}
}
