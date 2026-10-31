package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// ackStub is a message whose acks are recorded and whose DoubleAck can fail.
// The embedded nil Msg means any other method call panics, so a test fails
// loudly if dropForeign reaches for one.
type ackStub struct {
	jetstream.Msg
	acks, doubleAcks int
	doubleAckErr     error
}

func (s *ackStub) Ack() error                      { s.acks++; return nil }
func (s *ackStub) DoubleAck(context.Context) error { s.doubleAcks++; return s.doubleAckErr }

func foreignEnvelope() *Envelope {
	return &Envelope{MessageID: "drop1", Content: Content{Data: "FOR alpha-supervisor\nnot for you"}}
}

func ackTestTier(logs *[]string) *globalTier {
	f := forTestFilter(forTestRows(), nil)
	f.logf = func(format string, args ...any) {
		*logs = append(*logs, strings.TrimSpace(fmt.Sprintf(format, args...)))
	}
	return &globalTier{forFilter: f}
}

// A drop is acked with a confirmed ack (DoubleAck), never the plain Ack.
func TestDropForeignUsesAConfirmedAck(t *testing.T) {
	var logs []string
	g := ackTestTier(&logs)
	m := &ackStub{}
	if !g.dropForeign(context.Background(), m, foreignEnvelope(), 7) {
		t.Fatal("a foreign message was not dropped")
	}
	if m.doubleAcks != 1 || m.acks != 0 {
		t.Fatalf("acks: DoubleAck %d, Ack %d; want 1 and 0", m.doubleAcks, m.acks)
	}
	if g.forDrops.Load() != 1 || g.acked != 7 {
		t.Fatalf("forDrops %d, acked %d; want 1 and 7", g.forDrops.Load(), g.acked)
	}
}

// When the ack is not confirmed the message is still withheld (the broker
// redelivers it to be dropped again), nothing is noted as acked or counted, and
// the failure is logged.
func TestDropForeignWithholdsTheMessageOnAFailedAck(t *testing.T) {
	var logs []string
	g := ackTestTier(&logs)
	m := &ackStub{doubleAckErr: errors.New("ack timed out")}
	if !g.dropForeign(context.Background(), m, foreignEnvelope(), 7) {
		t.Fatal("a foreign message was delivered because its ack failed")
	}
	if g.forDrops.Load() != 0 || g.acked != 0 {
		t.Fatalf("a failed ack was counted: forDrops %d, acked %d", g.forDrops.Load(), g.acked)
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "global ack drop1 FOR alpha-supervisor not confirmed") {
		t.Fatalf("log = %q, want one 'not confirmed' line naming drop1", logs)
	}
}

// A pull that drops every message it fetched goes on to the mail behind them.
// drainNoWait is called directly, with one message per pull, so the blocking
// fetch cannot rescue it: without the loop over dropped pulls the first pull
// returns nothing and the drain ends with the plain message still waiting.
func TestDrainNoWaitGoesPastAPullThatOnlyDropped(t *testing.T) {
	r, logs := forPathRig(t)
	sub := r.bus.global.cfg.inboxSubject()
	pubEnv(t, r.ctx, r.gjs, sub, "drop1", "INFORM", "FOR alpha-supervisor\nnot for you")
	pubEnv(t, r.ctx, r.gjs, sub, "drop2", "INFORM", "FOR alpha-supervisor\nnot for you either")
	pubEnv(t, r.ctx, r.gjs, sub, "plain1", "INFORM", "no FOR line")
	time.Sleep(200 * time.Millisecond)
	items, _, err := r.bus.global.drainNoWait(r.ctx, 1, r.bus.self.AgentID, r.bus.instance)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Env.MessageID != "plain1" {
		var got []string
		for _, it := range items {
			got = append(got, it.Env.MessageID)
		}
		t.Fatalf("drainNoWait(1) returned %v, want [plain1]", got)
	}
	if n := len(logs()); n != 2 {
		t.Fatalf("drop log lines = %d, want 2", n)
	}
}
