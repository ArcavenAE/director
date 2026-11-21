package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// director#731: a seat with the FOR filter on is cued, and warned
// cue.unanswered, for global mail the filter drops, and each drain comes back
// empty. These drive the real cue watcher over the real bus source, with the
// filter built as in forPathRig (it drops mail FOR a live seat of another team).

type foreignCueRig struct {
	*pipeRig
	now   time.Time
	cues  []map[string]any
	warns []string
	c     *cue
}

func newForeignCueRig(t *testing.T) *foreignCueRig {
	t.Helper()
	pr, _ := forPathRig(t)
	r := &foreignCueRig{pipeRig: pr, now: time.Now()}
	cfg := defaultCueConfig()
	cfg.Enabled = true
	r.c = newCue(cfg, busCueSource{r.bus},
		func(p map[string]any) { r.cues = append(r.cues, p) },
		func(ids []string, state string) { r.warns = append(r.warns, strings.Join(ids, ",")+" "+state) },
		func() time.Time { return r.now },
		func() string { return fmt.Sprintf("nonce-%d", len(r.cues)) },
	)
	if v, ch := r.c.handshake("2025-11-25"); v != "2025-11-25" || !ch {
		t.Fatalf("handshake: got %q channel=%v", v, ch)
	}
	r.c.initialized()
	return r
}

func (r *foreignCueRig) step(d time.Duration) {
	r.now = r.now.Add(d)
	r.c.tick(r.ctx)
}

// mailCues are the cues sent for mail, without self-test notices.
func (r *foreignCueRig) mailCues() []map[string]any {
	var out []map[string]any
	for _, p := range r.cues {
		if m, ok := p["meta"].(map[string]any); !ok || m["kind"] != "self-test" {
			out = append(out, p)
		}
	}
	return out
}

// First trial, factor: none. Mail FOR a live seat of another team is mail the
// filter drops, so the cue stays quiet and no warning goes out.
func TestCueStaysQuietForGlobalMailTheFilterDrops(t *testing.T) {
	r := newForeignCueRig(t)
	pubEnv(t, r.ctx, r.gjs, r.bus.global.cfg.inboxSubject(), "drop1", "INFORM", "FOR alpha-supervisor\nnot for you")
	// Past the warning window (300 s) at the poll interval, so a late cue or a
	// cue.unanswered would show.
	for i := 0; i < 170; i++ {
		r.step(2 * time.Second)
	}
	if got := r.mailCues(); len(got) != 0 {
		t.Fatalf("a cue went out for mail the filter drops: %v", got)
	}
	if len(r.warns) != 0 {
		t.Fatalf("a warning went out for mail the filter drops: %v", r.warns)
	}
}

// A message the filter cannot place (the roster cannot be read, or no fresh row
// names the seat it is for) is delivered by the drain, so the cue counts it for
// that poll. That count must not be kept: when the doubt clears the next poll
// leaves the message out, as the drain will, and the cue must not stay up for
// mail it will not hand over (director#731).

// setCueFilterClock sets the cue's reuse clock for one test.
func setCueFilterClock(t *testing.T, now func() time.Time) {
	t.Helper()
	prev := cueFilterNow
	cueFilterNow = now
	t.Cleanup(func() { cueFilterNow = prev })
}

// Factor: the roster read fails on the first poll and recovers before the next.
func TestCueCountLeavesOutDroppedMailOnceTheRosterReadRecovers(t *testing.T) {
	r, _ := forPathRig(t)
	clock := time.Now()
	setCueFilterClock(t, func() time.Time { return clock })
	failing := true
	r.bus.global.forFilter.roster = func(context.Context) ([]map[string]any, error) {
		if failing {
			return nil, errors.New("roster unreadable")
		}
		return forTestRows(), nil
	}
	pubEnv(t, r.ctx, r.gjs, r.bus.global.cfg.inboxSubject(), "drop1", "INFORM", "FOR alpha-supervisor\nnot for you")
	if n := cueCountOf(t, r); n != 1 {
		t.Fatalf("cue count with the roster unreadable = %d, want 1 (a doubt is delivered, so counted)", n)
	}
	failing = false
	clock = clock.Add(2 * time.Second)
	if n := cueCountOf(t, r); n != 0 {
		t.Fatalf("cue count after the roster read recovered = %d, want 0 (the filter now drops the message, and the drain will)", n)
	}
}

// Factor: every row of the seat the mail is for is stale on the first poll and
// fresh on the next, so the same doubt clears from the other side.
func TestCueCountLeavesOutDroppedMailOnceTheRowsTurnFresh(t *testing.T) {
	r, _ := forPathRig(t)
	clock := time.Now()
	setCueFilterClock(t, func() time.Time { return clock })
	rows := []map[string]any{forRow("alpha-supervisor-g1-0", "alpha", "supervisor", "c1", 10*time.Minute)}
	r.bus.global.forFilter.roster = func(context.Context) ([]map[string]any, error) { return rows, nil }
	pubEnv(t, r.ctx, r.gjs, r.bus.global.cfg.inboxSubject(), "drop1", "INFORM", "FOR alpha-supervisor\nnot for you")
	if n := cueCountOf(t, r); n != 1 {
		t.Fatalf("cue count with only a stale row = %d, want 1 (no fresh row names the seat, so the message is delivered)", n)
	}
	rows = []map[string]any{forRow("alpha-supervisor-g1-0", "alpha", "supervisor", "c1", time.Second)}
	clock = clock.Add(2 * time.Second)
	if n := cueCountOf(t, r); n != 0 {
		t.Fatalf("cue count once the row is fresh = %d, want 0 (the filter now drops the message, and the drain will)", n)
	}
}
