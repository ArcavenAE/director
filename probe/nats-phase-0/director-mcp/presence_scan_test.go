package main

import (
	"strings"
	"testing"
)

// The defect (aae-orc-7xrdo, finding-188's first specimen in shipped code):
// the resolver collapsed "this row could not be read" into "this row is not
// there", then refused with a sentence asserting the second.
//
// The refusal is correct and must stay. What is under test is what it CLAIMS.
//
// Every refusal assertion below is preceded by a positive control in the same
// shape, because "an error was returned" passes for a resolver that refuses
// everything, which is the defect these tests exist to prevent. And the four
// zero cases are asserted as four distinct outcomes rather than as "an error",
// because a test that accepts any refusal for all four reproduces the very
// conflation being fixed.

const liveAddr = "agent://fleet/builder"
const livePrefix = "presence.fleet.builder."

// positive control, reused: a readable record in the same shape resolves.
func mustResolve(t *testing.T) {
	t.Helper()
	ws, err := pickWorkspace(liveAddr, livePrefix, presenceScan{
		Keys: 1, TeamMatched: 1, Matched: 1, Workspaces: []string{"ops2"},
	})
	if err != nil {
		t.Fatalf("CONTROL FAILED, the refusal assertions below prove nothing: a live record should resolve, got %v", err)
	}
	if ws != "ops2" {
		t.Fatalf("CONTROL FAILED: resolved %q, want ops2", ws)
	}
}

func TestPresenceRefusalDistinguishesTheFourZeroCases(t *testing.T) {
	mustResolve(t)

	tests := []struct {
		name string
		scan presenceScan
		// want is text unique to this outcome.
		want string
		// absent must NOT appear.
		absent string
	}{
		{
			name: "empty bucket",
			scan: presenceScan{},
			want: "the presence bucket is empty",
		},
		{
			name: "team name wrong",
			scan: presenceScan{Keys: 7},
			want: "the team name is likely wrong",
		},
		{
			name: "team live, id wrong or seat down",
			scan: presenceScan{Keys: 7, TeamMatched: 3},
			want: "the recipient id is wrong, or that seat is down",
		},
		{
			name: "rows matched and none could be read",
			scan: presenceScan{Keys: 7, TeamMatched: 3, Matched: 2, Unreadable: 2},
			want: "was NOT established",
			// The whole point: this case must not claim absence.
			absent: "no session would consume",
		},
	}

	seen := map[string]string{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := pickWorkspace(liveAddr, livePrefix, tt.scan)
			if err == nil {
				t.Fatal("every zero case must still refuse; a fix that lets a send through is the wrong fix")
			}
			got := err.Error()
			if !strings.Contains(got, tt.want) {
				t.Errorf("want text %q, got:\n%s", tt.want, got)
			}
			if tt.absent != "" && strings.Contains(got, tt.absent) {
				t.Errorf("this outcome must not claim %q, because it was not established; got:\n%s", tt.absent, got)
			}
			if !strings.Contains(got, "R-92") {
				t.Errorf("every refusal should still name R-92; got:\n%s", got)
			}
		})
		if _, err := pickWorkspace(liveAddr, livePrefix, tt.scan); err != nil {
			if prev, dup := seen[err.Error()]; dup {
				t.Errorf("%q and %q produce the IDENTICAL sentence, which is the defect: %s", tt.name, prev, err)
			}
			seen[err.Error()] = tt.name
		}
	}
}

// The incomplete-record path is the third silent skip named in the ticket: the
// value parsed but carried no workspace. It is a read that succeeded, so it is
// reported separately from an unreadable one, and it still must not claim the
// recipient is absent.
func TestPresenceRefusalReportsIncompleteRecordsSeparately(t *testing.T) {
	mustResolve(t)

	_, err := pickWorkspace(liveAddr, livePrefix, presenceScan{
		Keys: 4, TeamMatched: 2, Matched: 1, Incomplete: 1,
	})
	if err == nil {
		t.Fatal("an incomplete record must still refuse")
	}
	got := err.Error()
	for _, want := range []string{"1 missing a workspace", "was NOT established"} {
		if !strings.Contains(got, want) {
			t.Errorf("want %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "no session would consume") {
		t.Errorf("an unusable record is not evidence of absence; got:\n%s", got)
	}
}

// The three established cases keep the original claim, because in each of them
// it is true and was shown. Only the unestablished case loses it.
func TestEstablishedAbsenceStillSaysSo(t *testing.T) {
	mustResolve(t)

	for _, scan := range []presenceScan{
		{},
		{Keys: 7},
		{Keys: 7, TeamMatched: 3},
	} {
		_, err := pickWorkspace(liveAddr, livePrefix, scan)
		if err == nil {
			t.Fatalf("scan %+v must refuse", scan)
		}
		if !strings.Contains(err.Error(), "no session would consume") {
			t.Errorf("an established absence should still say so; scan %+v gave:\n%s", scan, err)
		}
	}
}

func TestTeamScopeNarrowsToTheTeamSegment(t *testing.T) {
	tests := []struct{ prefix, want string }{
		{"presence.fleet.builder.", "presence.fleet."},
		{"presence.fleet.", "presence.fleet."},
		{"presence.director.", "presence.director."},
		{"presence.mokuzai.supervisor.", "presence.mokuzai."},
		{"presence.", "presence."},
		{"", ""},
	}
	for _, tt := range tests {
		if got := teamScope(tt.prefix); got != tt.want {
			t.Errorf("teamScope(%q) = %q, want %q", tt.prefix, got, tt.want)
		}
	}
}

// The global tier carries the same defect and the same fix, so it gets the
// same assertions rather than a weaker set.
func TestGlobalPresenceRefusalDistinguishesItsCases(t *testing.T) {
	const addr = "global://mokuzai/supervisor"
	const prefix = "presence.mokuzai.supervisor."

	tests := []struct {
		name          string
		scan          presenceScan
		want          string
		mustNotAssert bool
	}{
		{"empty bucket", presenceScan{}, "the bucket is empty", false},
		{"cluster name wrong", presenceScan{Keys: 5}, "the cluster name is likely wrong", false},
		{"cluster live, role wrong or seat down", presenceScan{Keys: 5, TeamMatched: 2}, "the role is wrong, or that seat is down", false},
		{"rows unreadable", presenceScan{Keys: 5, TeamMatched: 2, Matched: 2, Unreadable: 2}, "was NOT established", true},
	}
	seen := map[string]string{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := noGlobalPresenceErr(addr, prefix, tt.scan)
			if err == nil {
				t.Fatal("every case must refuse")
			}
			got := err.Error()
			if !strings.Contains(got, tt.want) {
				t.Errorf("want %q, got:\n%s", tt.want, got)
			}
			if tt.mustNotAssert && strings.Contains(got, "no session would consume") {
				t.Errorf("unreadable rows are not evidence of absence; got:\n%s", got)
			}
			if !strings.Contains(got, "R-92") {
				t.Errorf("want R-92 named, got:\n%s", got)
			}
		})
		err := noGlobalPresenceErr(addr, prefix, tt.scan)
		if prev, dup := seen[err.Error()]; dup {
			t.Errorf("%q and %q produce the IDENTICAL sentence: %s", tt.name, prev, err)
		}
		seen[err.Error()] = tt.name
	}
}
