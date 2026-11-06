package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// aae-orc-mjckm (P5), test 4: a message marked read with no reply owed
// leaves the pending count and fires no cue.unanswered. TestNoReplyAckClearsPending
// pins the count; this pins the warning, through a real cue over a real bus.

type noReplyRig struct {
	now   time.Time
	warns []string
	cues  int
	c     *cue
}

func newNoReplyRig(t *testing.T, bus *Bus) *noReplyRig {
	r := &noReplyRig{now: time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)}
	cfg := defaultCueConfig()
	cfg.Enabled = true
	n := 0
	r.c = newCue(cfg, busCueSource{bus},
		func(p map[string]any) {
			if m, ok := p["meta"].(map[string]any); !ok || m["kind"] != "self-test" {
				r.cues++
			}
		},
		func(ids []string, state string) { r.warns = append(r.warns, strings.Join(ids, ",")+" "+state) },
		func() time.Time { return r.now },
		func() string { n++; return "nonce" },
	)
	if v, ch := r.c.handshake("2025-11-25"); v != "2025-11-25" || !ch {
		t.Fatalf("handshake: %q channel=%v", v, ch)
	}
	r.c.initialized()
	return r
}

func (r *noReplyRig) step(d time.Duration) { r.now = r.now.Add(d); r.c.tick(context.Background()) }

// The seat reads a message and the cue goes out for it; the seat then marks it
// no_reply, and two receipt windows later no cue.unanswered has been sent and no
// re-cue is due.
func TestNoReplyAckFiresNoCueUnanswered(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, js, _, _ := handledBus(t, ctx, true, "")
	r := newNoReplyRig(t, bus)
	W := r.c.cfg.Window

	pubEnv(t, ctx, js, mineSubject, "nr1", "INFORM", "for another seat; take no action")
	res, err := bus.receiveTiered(ctx, 2*time.Second)
	if err != nil || res.Env == nil || res.Env.MessageID != "nr1" {
		t.Fatalf("receive = %+v, %v", res, err)
	}
	r.step(2 * time.Second)
	if r.cues != 1 {
		t.Fatalf("setup: the cue for the unmarked message went out %d times, want 1", r.cues)
	}
	if out, err := bus.markHandled(ctx, "local", "nr1", "no_reply", ""); err != nil || out["acked"] != true {
		t.Fatalf("mark no_reply = %v, %v", out, err)
	}
	r.step(W + time.Second)
	r.step(W + time.Second)
	if len(r.warns) != 0 {
		t.Fatalf("a no_reply ack was followed by cue.unanswered warnings: %v", r.warns)
	}
	if r.cues != 1 {
		t.Fatalf("a no_reply ack was followed by a re-cue: %d cues, want 1", r.cues)
	}
}

// Control: the same flow with the message left unmarked does warn, so the test
// above is able to see a warning at all.
func TestUnmarkedReadMessageDoesFireCueUnanswered(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, js, _, _ := handledBus(t, ctx, true, "")
	r := newNoReplyRig(t, bus)
	W := r.c.cfg.Window

	pubEnv(t, ctx, js, mineSubject, "nr2", "INFORM", "read and left alone")
	if res, err := bus.receiveTiered(ctx, 2*time.Second); err != nil || res.Env == nil {
		t.Fatalf("receive = %+v, %v", res, err)
	}
	r.step(2 * time.Second)
	r.step(W + time.Second)
	if len(r.warns) == 0 {
		t.Fatal("CONTROL FAILED: an unmarked read message drew no cue.unanswered after the window")
	}
}
