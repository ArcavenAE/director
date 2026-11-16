package main

import (
	"context"
	"testing"
)

// Cluster names are config identifiers, so the FOR filter compares them
// exactly and with case. The test seat is operator-g3-0 on cluster c1. A
// cluster that differs from it only in case is another cluster, and a seat
// written on it is not this seat.

// In cut prose, operator@C1 names a seat on cluster C1, not this seat on c1,
// so the line still drops. operator@c1 delivers as the control.
func TestForFilterProseClusterCompareIsCaseSensitive(t *testing.T) {
	cases := []struct {
		name, data string
		drop       bool
	}{
		{"control: this cluster in lower case delivers", "FOR alpha-supervisor, see operator@c1 on repo#1:\nbody", false},
		{"a cluster that differs only in case drops", "FOR alpha-supervisor, see operator@C1 on repo#1:\nbody", true},
		{"the full id on a cluster that differs only in case drops", "FOR alpha-supervisor, see operator-g3-0@C1 on repo#1:\nbody", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := forTestFilter(forTestRows(), nil)
			if got, _ := f.drops(context.Background(), &Envelope{Content: Content{Data: c.data}}); got != c.drop {
				t.Fatalf("%q: drop = %v, want %v", c.data, got, c.drop)
			}
		})
	}
}

// In the name path, namesSelf takes the cluster as written and compares it
// exactly. The FOR line pattern keeps an upper-case cluster from reaching it
// through drops(), so the pin calls it directly.
func TestForFilterNamesSelfClusterCompareIsCaseSensitive(t *testing.T) {
	f := forTestFilter(forTestRows(), nil)
	if !f.namesSelf("operator", "c1") {
		t.Fatal("control: operator@c1 should name this seat")
	}
	if f.namesSelf("operator", "C1") {
		t.Fatal("operator@C1 names a seat on another cluster, not this seat on c1")
	}
}
