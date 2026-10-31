package main

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// forPathRig is a global-tier rig whose filter drops mail FOR "alpha-supervisor"
// (a seat of another team in forTestRows) and records each drop log line.
func forPathRig(t *testing.T) (*pipeRig, func() []string) {
	t.Helper()
	r := newPipeRig(t, true)
	var mu sync.Mutex
	var logs []string
	f := forTestFilter(forTestRows(), nil)
	f.logf = func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		logs = append(logs, strings.TrimSpace(fmt.Sprintf(format, args...)))
	}
	r.bus.global.forFilter = f
	return r, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), logs...)
	}
}

func publishForPair(t *testing.T, r *pipeRig) {
	t.Helper()
	sub := r.bus.global.cfg.inboxSubject()
	pubEnv(t, r.ctx, r.gjs, sub, "drop1", "INFORM", "FOR alpha-supervisor\nnot for you")
	pubEnv(t, r.ctx, r.gjs, sub, "plain1", "INFORM", "no FOR line")
}

func wantOneDrop(t *testing.T, logs []string) {
	t.Helper()
	if len(logs) != 1 || !strings.Contains(logs[0], "global ack drop1 FOR alpha-supervisor") {
		t.Fatalf("drop log = %q, want one line naming drop1", logs)
	}
}

// Mail that arrives while the seat is blocked in its poll goes through fetchOne.
// Without the filter call there, drop1 is the message the poll returns.
func TestForFilterDropsMailArrivingDuringABlockedPoll(t *testing.T) {
	r, logs := forPathRig(t)
	g := r.bus.global
	got := make(chan *Envelope, 1)
	go func() {
		e, _, _, _ := g.fetchOne(r.ctx, 10*time.Second)
		got <- e
	}()
	time.Sleep(500 * time.Millisecond) // the poll is blocked on an empty durable
	publishForPair(t, r)
	select {
	case e := <-got:
		if e == nil || e.MessageID != "plain1" {
			t.Fatalf("the blocked poll returned %v, want plain1", e)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the blocked poll returned nothing")
	}
	wantOneDrop(t, logs())
}

// Mail already waiting when the seat polls goes through pullNoWait, behind
// takeWaiting for one message and behind receiveBatch for many. Without the
// filter call there, drop1 is delivered.
func TestForFilterDropsAlreadyWaitingMail(t *testing.T) {
	t.Run("max 1", func(t *testing.T) {
		r, logs := forPathRig(t)
		publishForPair(t, r)
		res, err := r.bus.receiveTiered(r.ctx, 2*time.Second)
		if err != nil || res.Env == nil || res.Env.MessageID != "plain1" {
			t.Fatalf("receiveTiered = %v, %v; want plain1", res.Env, err)
		}
		wantOneDrop(t, logs())
	})
	t.Run("max 50", func(t *testing.T) {
		r, logs := forPathRig(t)
		publishForPair(t, r)
		res, err := r.bus.receiveBatch(r.ctx, 2*time.Second, 50)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, it := range res.Items {
			got = append(got, it.Env.MessageID)
		}
		if strings.Join(got, ",") != "plain1" {
			t.Fatalf("receiveBatch delivered %v, want [plain1]", got)
		}
		wantOneDrop(t, logs())
	})
}
