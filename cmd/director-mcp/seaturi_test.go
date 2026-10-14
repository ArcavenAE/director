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
		{"marvel://fleet1/c1/ws1/team1/builder-0", "builder", 0},
		{"marvel://fleet1/c1/ws1/team1/widget-maker-0", "widget-maker", 0},
		{"marvel://fleet1/c1/ws1/team1/builder-10", "builder", 10},
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
		{"empty role, leading hyphen", "marvel://fleet1/c1/ws1/team1/-0", "valid segment"},
		{"doubled hyphen", "marvel://fleet1/c1/ws1/team1/x--0", "valid segment"},
		{"segment of one hyphen", "marvel://fleet1/c1/ws1/-/builder-0", "valid segment"},
		{"segment of two hyphens", "marvel://fleet1/c1/--/team1/builder-0", "valid segment"},
		{"leading zero in index", "marvel://fleet1/c1/ws1/team1/builder-00", "leading zero"},
		{"old generation shape", "marvel://fleet1/c1/ws1/team1/widget-g4-3", "old generation"},
		{"uppercase fleet", "marvel://Fleet1/c1/ws1/team1/builder-0", "valid segment"},
		{"percent escape", "marvel://fleet1/c1/ws1/team1/builder%2D0", "valid segment"},
		{"trailing slash", "marvel://fleet1/c1/ws1/team1/builder-0/", "trailing slash"},
		{"four segments", "marvel://fleet1/c1/team1/builder-0", "4 segments"},
		{"six segments", "marvel://fleet1/c1/ws1/team1/x/builder-0", "6 segments"},
		{"query", "marvel://fleet1/c1/ws1/team1/builder-0?x=1", "valid segment"},
		{"fragment", "marvel://fleet1/c1/ws1/team1/builder-0#x", "valid segment"},
		{"no index", "marvel://fleet1/c1/ws1/team1/builder", "no -<index>"},
		{"non-digit index", "marvel://fleet1/c1/ws1/team1/builder-x", "not decimal"},
		{"wrong scheme", "agent://fleet1/c1/ws1/team1/builder-0", "does not start"},
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
	raw := "marvel://fleet1/c1/ws1/team1/builder%2D0"
	p, err := url.Parse(raw)
	if err != nil || p.Path != "/c1/ws1/team1/builder-0" {
		t.Fatalf("premise: url.Parse decodes the escape, got %q %v", p.Path, err)
	}
	if _, err := ParseSeatURI(raw); err == nil {
		t.Fatal("the raw string with %2D must be rejected")
	}
}

func TestNewSeatURIBuildsAndRoundTrips(t *testing.T) {
	u, err := NewSeatURI("fleet1", "c1", "ws1", "team1", "widget-maker", 3)
	if err != nil {
		t.Fatal(err)
	}
	if got := u.String(); got != "marvel://fleet1/c1/ws1/team1/widget-maker-3" {
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
		name                           string
		fleet, cluster, ws, team, role string
		idx                            int
	}{
		{"uppercase", "Fleet1", "c1", "ws1", "team1", "builder", 0},
		{"empty cluster", "fleet1", "", "ws1", "team1", "builder", 0},
		{"underscore", "fleet1", "c_1", "ws1", "team1", "builder", 0},
		{"slash in team", "fleet1", "c1", "ws1", "a/b", "builder", 0},
		{"old generation role", "fleet1", "c1", "ws1", "team1", "widget-g4", 3},
		{"empty role", "fleet1", "c1", "ws1", "team1", "", 0},
		{"negative index", "fleet1", "c1", "ws1", "team1", "builder", -1},
	}
	for _, c := range cases {
		if _, err := NewSeatURI(c.fleet, c.cluster, c.ws, c.team, c.role, c.idx); err == nil {
			t.Errorf("%s: accepted", c.name)
		}
	}
}
