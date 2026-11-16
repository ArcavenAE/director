package main

import (
	"context"
	"testing"
)

// A seat written as name@cluster inside the cut prose counts as this seat when
// the name part matches by the existing rule and the cluster is this seat's
// cluster. A name@other-cluster does not match. The test seat is operator-g3-0
// on cluster c1.
func TestForFilterProseNameAtClusterDelivers(t *testing.T) {
	cases := []struct {
		name, data string
		drop       bool
	}{
		{"short id at this cluster delivers", "FOR alpha-supervisor, see operator@c1 on repo#1:\nbody", false},
		{"full id at this cluster delivers", "FOR alpha-supervisor, see operator-g3-0@c1 on repo#1:\nbody", false},
		{"case is ignored on the name part", "FOR alpha-supervisor, see Operator@c1 on repo#1:\nbody", false},
		{"short id at another cluster drops", "FOR alpha-supervisor, see operator@c2 on repo#1:\nbody", true},
		{"full id at another cluster drops", "FOR alpha-supervisor, see operator-g3-0@c2 on repo#1:\nbody", true},
		{"a prefix of this seat at this cluster drops", "FOR alpha-supervisor, see operator-extra@c1 on repo#1:\nbody", true},
		{"a role word at this cluster drops", "FOR alpha-supervisor, see supervisor@c1 on repo#1:\nbody", true},
		{"bare short id still delivers", "FOR alpha-supervisor, see operator on repo#1:\nbody", false},
		{"another seat at this cluster still drops", "FOR alpha-supervisor, see ops-builder@c1 on repo#1:\nbody", true},
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
