package main

import (
	"context"
	"os"
	"testing"
)

// realEarlierDurableProbe is the shipped lookup that decides whether a seat's
// first global durable starts at the stream head (director#594).
var realEarlierDurableProbe = earlierDurableProbe

// The broker tests written before director#594 publish mail before the seat
// attaches and expect to read it, which is the replay a seat with an earlier
// durable still gets. They keep it through a probe that always finds one. The
// head-start tests call useRealProbe to run the shipped lookup.
func TestMain(m *testing.M) {
	earlierDurableProbe = func(context.Context, *globalTier, string, string) (bool, error) { return true, nil }
	os.Exit(m.Run())
}

func useRealProbe(t *testing.T) {
	t.Helper()
	prev := earlierDurableProbe
	earlierDurableProbe = realEarlierDurableProbe
	t.Cleanup(func() { earlierDurableProbe = prev })
}
