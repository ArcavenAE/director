package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Build item 6 (R-210, #88): with the watch on, a refusal for absence can say
// when the cluster was last seen, from the watch's state. A cluster the watch
// has never seen keeps today's wording.
func TestRefusalNamesLastSeenWhenWatchKnowsIt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	fx := newWatchFx(t, ctx, 2*time.Second, true)
	fx.otherCluster()
	// control: a cluster the watch has never seen keeps the wording on main, with
	// no last-seen claim it cannot back.
	_, err := fx.send(map[string]any{"to": "global://c9/supervisor", "fail_fast": true})
	if err == nil || !strings.Contains(err.Error(), "nothing is registered under") || strings.Contains(err.Error(), "last seen") {
		t.Fatalf("CONTROL FAILED: an unknown cluster should keep today's wording, got %v", err)
	}

	fx.put("c1", "s1", "working")
	fx.tick()
	seen := fx.clk.wall.Format(time.RFC3339)
	fx.waitGone("presence.c1.supervisor.s1")
	fx.tick()
	fx.tick() // absent
	_, err = fx.send(map[string]any{"to": "global://c1/supervisor", "fail_fast": true})
	if err == nil || !strings.Contains(err.Error(), "last seen "+seen) {
		t.Fatalf("a cluster the watch saw leave should be refused with its last-seen time %s, got %v", seen, err)
	}
	if !strings.Contains(err.Error(), "no session would consume this send") {
		t.Fatalf("the refusal must still say it refused and why: %v", err)
	}
	// the held reason carries the same words.
	out, err := fx.send(map[string]any{"to": "global://c1/supervisor"})
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := asMap(t, out)["reason"].(string); !strings.Contains(r, "last seen "+seen) {
		t.Fatalf("the hold reason should say when it was last seen, got %q", r)
	}
}
