package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// Cases added after the mutant run over the T-A1 change (director#269). Each
// pins a branch the first four tests left uncovered.

// Two callers that both find the durable gone must both end up on the rebuilt
// one: the one that rebuilds and the one that waited for it. A waiter that
// reported "nothing to do" would surface the original error to its caller.
func TestTwoRestoresOfOneLossBothReportTheRebuild(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, js := localLossBus(t, ctx)
	if err := js.DeleteConsumer(ctx, "AGENT_INBOX", localDurableName(bus)); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	asked := 0
	both := make(chan struct{})
	bus.lookup = func(ctx context.Context, durable string) error {
		mu.Lock()
		asked++
		if asked == 2 {
			close(both)
		}
		mu.Unlock()
		select {
		case <-both:
		case <-time.After(time.Second):
		}
		_, err := bus.js.Consumer(ctx, "AGENT_INBOX", durable)
		return err
	}
	type res struct {
		ok  bool
		err error
	}
	out := make(chan res, 2)
	for i := 0; i < 2; i++ {
		go func() { ok, err := bus.restoreLocal(ctx); out <- res{ok, err} }()
	}
	for i := 0; i < 2; i++ {
		if r := <-out; r.err != nil || !r.ok {
			t.Errorf("restore %d = %v, %v; want true, nil (the durable was rebuilt, retry on it)", i, r.ok, r.err)
		}
	}
	if rec := presenceRecord(t, ctx, bus); rec["rebuilds_ok"] != int64(1) {
		t.Errorf("rebuilds_ok = %v, want 1: %v", rec["rebuilds_ok"], rec)
	}
}

// A lookup that fails because the caller left is not a broker that is silent.
func TestACanceledLookupDoesNotMarkTheDurableUnknown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, _ := localLossBus(t, ctx)
	counted(t, bus, context.Canceled)
	cctx, ccancel := context.WithCancel(ctx)
	ccancel()
	if ok, err := bus.restoreLocal(cctx); ok || err != nil {
		t.Fatalf("restoreLocal with a canceled context = %v, %v; want false, nil", ok, err)
	}
	if rec := presenceRecord(t, ctx, bus); rec["durable_state"] != nil {
		t.Errorf("a canceled lookup left durable_state %v: %v", rec["durable_state"], rec)
	}
}

// A rebuild that failed is the worst state; a later silent broker does not
// soften it to unknown.
func TestAnUnansweredLookupDoesNotHideAFailedRebuild(t *testing.T) {
	var h durableHealth
	h.rebuildFailed(errors.New("create refused"))
	h.lookupUnanswered(errors.New("no responders"))
	s := h.summary("local")
	if s.State != durableMissing || s.Failed != 1 {
		t.Fatalf("summary = %+v, want missing with 1 failed rebuild", s)
	}
}

// An answered lookup after a successful rebuild goes back to rebuilt, not to
// present: the count of rebuilds is still true.
func TestAnAnsweredLookupAfterARebuildReadsRebuiltNotPresent(t *testing.T) {
	var h durableHealth
	h.rebuilt()
	h.lookupUnanswered(errors.New("no responders"))
	if s := h.summary("local"); s.State != durableUnknown {
		t.Fatalf("after an unanswered lookup state = %q, want unknown", s.State)
	}
	h.lookupAnswered()
	if s := h.summary("local"); s.State != durableRebuilt || s.OK != 1 || s.Reason != "" {
		t.Fatalf("after an answered lookup summary = %+v, want rebuilt, 1 rebuild, no reason", s)
	}
}
