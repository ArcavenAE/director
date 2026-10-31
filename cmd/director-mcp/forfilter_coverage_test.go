package main

import (
	"context"
	"testing"
	"time"
)

// The -gN-M suffix strip is the only way a token that names a seat by its base
// name resolves. The row's role is "supervisor" and its agent id carries
// "research", so the <team>-<role> form cannot match "alpha-research-supervisor"
// and only the strip can. Without the strip the message delivers.
func TestForFilterBaseNameResolvesOnlyThroughTheSuffixStrip(t *testing.T) {
	rows := []map[string]any{forRow("alpha-research-supervisor-g1-0", "alpha", "supervisor", "c1", time.Second)}
	f := forTestFilter(rows, nil)
	e := &Envelope{Content: Content{Data: "FOR alpha-research-supervisor"}}
	if got, _ := f.drops(context.Background(), e); !got {
		t.Fatal("a base name did not resolve to the other team's seat")
	}
}

// A token equal to this seat's team name delivers even when a seat of another
// team answers to that same name. The other team's row has agent id ops-g1-0,
// so its base name is "ops", this seat's team. Only the name == team half of the
// self check keeps the message; without it the row proves another team's seat
// and the message drops.
func TestForFilterOwnTeamNameDeliversWhenAnotherTeamsSeatShareIt(t *testing.T) {
	rows := []map[string]any{forRow("ops-g1-0", "alpha", "supervisor", "c1", time.Second)}
	f := forTestFilter(rows, nil)
	e := &Envelope{Content: Content{Data: "FOR ops"}}
	if got, _ := f.drops(context.Background(), e); got {
		t.Fatal("this seat's team name dropped because another team's seat shares the name")
	}
}
