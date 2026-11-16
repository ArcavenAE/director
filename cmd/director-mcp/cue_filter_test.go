package main

import (
	"testing"
	"time"
)

// The reuse of one peek's result ends cueFilterReuse after it was stored: a get
// just inside the bound hits, and a get at the bound misses. The roster the
// filter reads can change with no change to the durable, so the reuse must end
// on its own.
func TestCueFilterCacheReuseEndsAtTheBound(t *testing.T) {
	var c cueFilterCache
	k := cueFilterKey{waiting: 3, ackFloor: 7, delivered: 9}
	t0 := time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC)
	if _, ok := c.get(k, t0); ok {
		t.Fatal("an empty cache hit")
	}
	c.put(k, t0, 2)
	if n, ok := c.get(k, t0.Add(cueFilterReuse-time.Nanosecond)); !ok || n != 2 {
		t.Fatalf("get just inside the bound = %d, %v; want 2, true", n, ok)
	}
	if _, ok := c.get(k, t0.Add(cueFilterReuse)); ok {
		t.Fatal("a get at put's time plus the reuse bound hit; it must miss")
	}
	if _, ok := c.get(cueFilterKey{waiting: 4, ackFloor: 7, delivered: 9}, t0); ok {
		t.Fatal("a get for a different durable position hit")
	}
}
