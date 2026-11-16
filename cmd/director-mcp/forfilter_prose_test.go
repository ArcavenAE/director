package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A FOR line that names one seat and then continues in prose after a comma
// ("FOR alpha-supervisor-g2-0, on repo#1008 at 65eb9433:") reached seats on
// other teams. The prose words are read as names, and a token such as
// "repo#1008" fails the seat pattern, so the whole line reads as ambiguous and
// the filter delivers.
func TestForFilterDropsNamedSeatFollowedByCommaProse(t *testing.T) {
	rows := append(forTestRows(),
		forRow("alpha-supervisor-g2-0", "alpha", "supervisor", "c1", time.Second))
	f := forTestFilter(rows, nil)
	cases := []string{
		"FOR alpha-supervisor-g2-0, on repo#1008 at 65eb9433:\nbody",
		"FOR alpha-supervisor-g2-0, on the PR at 65eb9433:\nbody",
		"FOR alpha-supervisor-g2-0, see Org/repo#12:\nbody",
	}
	for _, data := range cases {
		drop, _ := f.drops(context.Background(), &Envelope{Content: Content{Data: data}})
		if !drop {
			t.Errorf("FOR line %q was delivered to a seat on another team", data)
		}
	}
}

func TestForTokensEndsTheListAtProse(t *testing.T) {
	cases := []struct {
		name, text string
		want       []string
	}{
		{"names then prose", "alpha-supervisor-g2-0, on repo#1008 at 65eb9433", []string{"alpha-supervisor-g2-0"}},
		{"joiners keep an element as names", "alpha-supervisor, beta-supervisor and gamma-supervisor", []string{"alpha-supervisor", "beta-supervisor", "gamma-supervisor"}},
		{"first element is always names", "alpha-supervisor beta-supervisor, on repo#1 at abc1234", []string{"alpha-supervisor", "beta-supervisor"}},
		{"prose ends everything after it", "alpha-supervisor, on the PR, beta-supervisor", []string{"alpha-supervisor"}},
		{"one token is a name", "alpha-supervisor, alpha-supervisr", []string{"alpha-supervisor", "alpha-supervisr"}},
		{"space separated list is unchanged", "alpha-supervisor beta-supervisor", []string{"alpha-supervisor", "beta-supervisor"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := forTokens(c.text)
			if strings.Join(got, "|") != strings.Join(c.want, "|") {
				t.Fatalf("forTokens(%q) = %q, want %q", c.text, got, c.want)
			}
		})
	}
}

func TestForFilterCommaListDecisions(t *testing.T) {
	rows := append(forTestRows(),
		forRow("beta-supervisor-g1-0", "beta", "supervisor", "c1", time.Second),
		forRow("gamma-supervisor-g1-0", "gamma", "supervisor", "c1", time.Second))
	cases := []struct {
		name, data string
		drop       bool
	}{
		{"three names with a joiner in the last element", "FOR alpha-supervisor, beta-supervisor and gamma-supervisor:\nbody", true},
		{"prose ends the list after two names", "FOR alpha-supervisor beta-supervisor, on repo#1 at abc1234:\nbody", true},
		{"a mistyped seat-shaped name delivers", "FOR alpha-supervisor, alpha-supervisr:\nbody", false},
		{"space separated names keep today's rule", "FOR alpha-supervisor beta-supervisor", true},
		{"space separated with an unknown name delivers", "FOR alpha-supervisor nobody-here", false},
		{"this seat before the prose delivers", "FOR alpha-supervisor, operator, on repo#1:\nbody", false},
		{"this seat only inside the prose does not deliver", "FOR alpha-supervisor, see operator and ops:\nbody", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := forTestFilter(rows, nil)
			if got, _ := f.drops(context.Background(), &Envelope{Content: Content{Data: c.data}}); got != c.drop {
				t.Fatalf("%q: drop = %v, want %v", c.data, got, c.drop)
			}
		})
	}
}
