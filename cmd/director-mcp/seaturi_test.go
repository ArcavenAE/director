package main

import (
	"net/url"
	"strings"
	"testing"
)

// The accepted and rejected rows are the worked examples of
// docs/design/seat-identity-grammar.md section 3.1, one row each.
func TestParseSeatURIWorkedExamples(t *testing.T) {
	ok := []struct {
		raw, role string
		idx       int
	}{
		{"marvel://c1/ws1/team1/builder-0", "builder", 0},
		{"marvel://c1/ws1/team1/widget-maker-0", "widget-maker", 0},
		{"marvel://c1/ws1/team1/builder-10", "builder", 10},
	}
	for _, c := range ok {
		u, err := ParseSeatURI(c.raw)
		if err != nil {
			t.Fatalf("%s: %v", c.raw, err)
		}
		if u.Role != c.role || u.Index != c.idx || u.String() != c.raw {
			t.Errorf("%s: got role %q index %d string %q", c.raw, u.Role, u.Index, u.String())
		}
	}
	bad := []struct{ name, raw, why string }{
		{"empty role, leading hyphen", "marvel://c1/ws1/team1/-0", "valid segment"},
		{"doubled hyphen", "marvel://c1/ws1/team1/x--0", "valid segment"},
		{"segment of one hyphen", "marvel://c1/ws1/-/builder-0", "valid segment"},
		{"segment of two hyphens", "marvel://c1/--/team1/builder-0", "valid segment"},
		{"leading zero in index", "marvel://c1/ws1/team1/builder-00", "leading zero"},
		{"old generation shape", "marvel://c1/ws1/team1/widget-g4-3", "old generation"},
		{"uppercase cluster", "marvel://C1/ws1/team1/builder-0", "valid segment"},
		{"percent escape, uppercase hex", "marvel://c1/ws1/team1/builder%2D0", "valid segment"},
		{"percent escape, lowercase hex", "marvel://c1/ws1/team1/builder%2d0", "valid segment"},
		{"percent escape in team", "marvel://c1/ws1/team%2d1/builder-0", "valid segment"},
		{"percent escape, digits only", "marvel://c1/ws1/team%30/builder-0", "valid segment"},
		{"leading hyphen on team", "marvel://c1/ws1/-team/builder-0", "valid segment"},
		{"leading hyphen on slot", "marvel://c1/ws1/team1/-builder-0", "valid segment"},
		{"doubled hyphen inside a segment", "marvel://c1/ws1/a--b/builder-0", "valid segment"},
		{"doubled hyphen inside a slot role", "marvel://c1/ws1/team1/x--y-0", "valid segment"},
		{"trailing hyphen on workspace", "marvel://c1/ws1-/team1/builder-0", "valid segment"},
		{"old generation shape, two digits", "marvel://c1/ws1/team1/widget-g12-3", "old generation"},
		{"trailing slash", "marvel://c1/ws1/team1/builder-0/", "trailing slash"},
		{"three segments", "marvel://c1/team1/builder-0", "3 segments"},
		{"five segments, the old fleet form", "marvel://fleet1/c1/ws1/team1/builder-0", "5 segments"},
		{"six segments", "marvel://c1/ws1/team1/x/y/builder-0", "6 segments"},
		{"query", "marvel://c1/ws1/team1/builder-0?x=1", "valid segment"},
		{"fragment", "marvel://c1/ws1/team1/builder-0#x", "valid segment"},
		{"no index", "marvel://c1/ws1/team1/builder", "no -<index>"},
		{"non-digit index", "marvel://c1/ws1/team1/builder-x", "not decimal"},
		{"wrong scheme", "agent://c1/ws1/team1/builder-0", "does not start"},
		{"empty", "", "does not start"},
	}
	for _, c := range bad {
		_, err := ParseSeatURI(c.raw)
		if err == nil {
			t.Errorf("%s: %q was accepted", c.name, c.raw)
			continue
		}
		if !strings.Contains(err.Error(), c.why) {
			t.Errorf("%s: error %q does not name %q", c.name, err, c.why)
		}
	}
}

// url.Parse decodes escapes in Path, so the percent row must be rejected on
// the raw string even though the parsed form would read builder-0 (CAND-ID-2).
func TestSeatURIValidatesTheRawStringNotTheParsedForm(t *testing.T) {
	// Lowercase and digit-only escapes, so the percent rule is pinned apart
	// from the uppercase rule.
	for _, c := range []struct{ raw, decoded string }{
		{"marvel://c1/ws1/team1/builder%2d0", "/ws1/team1/builder-0"},
		{"marvel://c1/ws1/team%2d1/builder-0", "/ws1/team-1/builder-0"},
		{"marvel://c1/ws1/team%30/builder-0", "/ws1/team0/builder-0"},
	} {
		p, err := url.Parse(c.raw)
		if err != nil || p.Path != c.decoded {
			t.Fatalf("premise: url.Parse decodes the escape in %s, got %q %v", c.raw, p.Path, err)
		}
		if _, err := ParseSeatURI(c.raw); err == nil {
			t.Fatalf("the raw string %s must be rejected", c.raw)
		}
	}
}

func TestNewSeatURIBuildsAndRoundTrips(t *testing.T) {
	u, err := NewSeatURI("c1", "ws1", "team1", "widget-maker", 3)
	if err != nil {
		t.Fatal(err)
	}
	if got := u.String(); got != "marvel://c1/ws1/team1/widget-maker-3" {
		t.Fatalf("string: %s", got)
	}
	if u.Slot() != "widget-maker-3" {
		t.Fatalf("slot: %s", u.Slot())
	}
	back, err := ParseSeatURI(u.String())
	if err != nil || back != u {
		t.Fatalf("round trip: %v %v", back, err)
	}
}

func TestNewSeatURIRefusesWhatTheParserRefuses(t *testing.T) {
	cases := []struct {
		name                    string
		cluster, ws, team, role string
		idx                     int
	}{
		{"uppercase", "C1", "ws1", "team1", "builder", 0},
		{"empty cluster", "", "ws1", "team1", "builder", 0},
		{"underscore", "c_1", "ws1", "team1", "builder", 0},
		{"slash in team", "c1", "ws1", "a/b", "builder", 0},
		{"old generation role", "c1", "ws1", "team1", "widget-g4", 3},
		{"old generation role, two digits", "c1", "ws1", "team1", "widget-g12", 3},
		{"leading hyphen", "c1", "ws1", "-team", "builder", 0},
		{"doubled hyphen", "c1", "ws1", "a--b", "builder", 0},
		{"trailing hyphen", "c1", "ws1-", "team1", "builder", 0},
		{"empty role", "c1", "ws1", "team1", "", 0},
		{"negative index", "c1", "ws1", "team1", "builder", -1},
	}
	for _, c := range cases {
		if _, err := NewSeatURI(c.cluster, c.ws, c.team, c.role, c.idx); err == nil {
			t.Errorf("%s: accepted", c.name)
		}
	}
}
