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
// exactly. On the token side, forSeat allows lower-case clusters only, so an
// upper-case cluster written in a FOR token cannot reach it through drops();
// the pin calls it directly.
func TestForFilterNamesSelfClusterCompareIsCaseSensitive(t *testing.T) {
	f := forTestFilter(forTestRows(), nil)
	if !f.namesSelf("operator", "c1") {
		t.Fatal("control: operator@c1 should name this seat")
	}
	if f.namesSelf("operator", "C1") {
		t.Fatal("operator@C1 names a seat on another cluster, not this seat on c1")
	}
}

// In the roster lookup, otherTeamSeat matches a presence row only when the
// cluster written in the token equals the row's cluster exactly. The pin asks
// for alpha-supervisor on C1 while its only presence row is on c1: the token's
// cluster is upper case and the row's is lower case, and that is another
// cluster, so no row matches. On the token side, forSeat allows lower-case
// clusters only, so an upper-case cluster written in a FOR token cannot reach
// otherTeamSeat through drops(); the pin calls it directly. This says nothing
// about the row side, where a row's cluster may carry upper case.
func TestForFilterOtherTeamSeatClusterCompareIsCaseSensitive(t *testing.T) {
	f := forTestFilter(forTestRows(), nil)
	rows, _ := f.roster(context.Background())
	if !f.otherTeamSeat("alpha-supervisor", "c1", rows) {
		t.Fatal("control: alpha-supervisor@c1 should match its presence row on c1")
	}
	if f.otherTeamSeat("alpha-supervisor", "C1", rows) {
		t.Fatal("alpha-supervisor@C1 names another cluster, so the row on c1 must not match")
	}
}
