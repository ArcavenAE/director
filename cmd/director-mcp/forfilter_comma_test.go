package main

import (
	"context"
	"testing"
	"time"
)

// A FOR line that names a seat and then carries a ticket id or the sender after
// a comma ("FOR e98-ent-supervisor-g1-0, I-578: ...") reached seats on other
// teams (I-579, aae-orc-c93bj). These are the leak lines as they arrived on
// corporate: each names a seat of another team, so this seat, ops/operator,
// must drop it. The guards below keep every message the parser cannot read
// delivered, since a dropped real message is the worse error.
func commaRows() []map[string]any {
	return append(forTestRows(),
		forRow("e98-supervisor-g8-0", "e98", "supervisor", "c1", time.Second),
		forRow("e98-ent-supervisor-g1-0", "e98-ent", "supervisor", "c1", time.Second),
		forRow("cue-ent-supervisor-g1-0", "cue-ent", "supervisor", "c1", time.Second),
		forRow("beta-supervisor-g1-0", "beta", "supervisor", "c1", time.Second))
}

func TestForFilterDropsALeakedCommaLine(t *testing.T) {
	cases := []struct{ name, data string }{
		{"sender and ticket after the comma, colon ends it", "FOR e98-supervisor-g8-0, from product-supervisor-g2-0: I-577, please read the plan\nbody"},
		{"ticket id after the comma, colon ends it", "FOR e98-ent-supervisor-g1-0, I-578: the route record is written\nbody"},
		{"ticket id after the comma, full stop ends it", "FOR cue-ent-supervisor-g1-0, I-578. The route record is written\nbody"},
		{"ticket id after the comma, no end mark", "FOR e98-ent-supervisor-g1-0, I-578\nbody"},
		{"lowercase ticket id after the comma", "FOR e98-ent-supervisor-g1-0, i-578: the route record is written\nbody"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := forTestFilter(commaRows(), nil)
			if drop, _ := f.drops(context.Background(), &Envelope{Content: Content{Data: c.data}}); !drop {
				t.Fatalf("%q was delivered to a seat on another team", c.data)
			}
		})
	}
}

func TestForTokensStopsBeforeATicketIdOrFrom(t *testing.T) {
	cases := []struct{ name, text, want string }{
		{"ticket id", "e98-ent-supervisor-g1-0, I-578", "e98-ent-supervisor-g1-0"},
		{"lowercase ticket id", "e98-ent-supervisor-g1-0, i-578", "e98-ent-supervisor-g1-0"},
		{"seat before the ticket id in the same element is kept", "e98-supervisor-g8-0, beta-supervisor and I-578", "e98-supervisor-g8-0|beta-supervisor"},
		{"from and the sender", "e98-supervisor-g8-0, from product-supervisor-g2-0", "e98-supervisor-g8-0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := join(forTokens(c.text)); got != c.want {
				t.Fatalf("forTokens(%q) = %q, want %q", c.text, got, c.want)
			}
		})
	}
}

func join(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += "|"
		}
		out += v
	}
	return out
}

// Guards: these pass today and must keep passing after the fix.
func TestForFilterCommaGuardsStillDeliverWhatItCannotRead(t *testing.T) {
	cases := []struct {
		name, data string
		drop       bool
	}{
		{"multi-seat list of other teams still drops", "FOR e98-supervisor-g8-0, beta-supervisor: I-578\nbody", true},
		{"multi-seat with and still drops", "FOR e98-supervisor-g8-0 and beta-supervisor, I-578: body", true},
		{"this seat in the list delivers", "FOR e98-supervisor-g8-0, operator-g3-0, I-578: body", false},
		{"this seat named first delivers", "FOR operator-g3-0, I-578: body", false},
		{"a sibling seat of this team is another seat and drops", "FOR e98-supervisor-g8-0, ops-builder-g2-0: body", true},
		{"an unknown seat delivers", "FOR nobody-here-g1-0, I-578: body", false},
		{"a line with no seat name delivers", "FOR : I-578 body", false},
		{"a symbol-only first element delivers", "FOR !!!, I-578: body", false},
		{"a lone non-seat word after a seat stays ambiguous and delivers", "FOR alpha-supervisor, Bob\nbody", false},
		{"a lone non-seat word after a comma, colon ends it, delivers", "FOR alpha-supervisor, Bob: body", false},
		{"this seat named in the prose delivers", "FOR e98-supervisor-g8-0, from operator-g3-0: I-578", false},
		{"no FOR line delivers", "I-578 is routed\nbody", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := forTestFilter(commaRows(), nil)
			if got, _ := f.drops(context.Background(), &Envelope{Content: Content{Data: c.data}}); got != c.drop {
				t.Fatalf("%q: drop = %v, want %v", c.data, got, c.drop)
			}
		})
	}
}
