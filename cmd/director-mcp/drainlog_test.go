package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The drain log records each drain the shim stamps, so the standing hook can
// tell a broken beat from an idle director (design slice 20,
// sim/design/director-standing-loop.md section 4). A line is
// "<time> ended=<batch|silence> stamp=<written|failed>", appended where
// last-drain is written. These run on the scratch broker of drain_broker_test.go
// with a scratch DIRECTOR_STATE.

var drainLogLine = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ ended=(batch|silence) stamp=(written|failed)$`)

func drainLogLines(t *testing.T, state string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(state, "drain.log"))
	if err != nil {
		t.Fatalf("no drain.log: %v", err)
	}
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		t.Fatalf("drain.log = %q, want newline ended lines", raw)
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
}

func TestABatchDrainAppendsOneDrainLogLineWithTheStampsTime(t *testing.T) {
	ctx, bus, pub, path := lastDrainFixture(t)
	state := filepath.Dir(path)
	pub("a")
	pub("b")
	if _, err := toolWait(ctx, bus, json.RawMessage(`{"timeout_seconds":2,"max":5}`)); err != nil {
		t.Fatal(err)
	}
	lines := drainLogLines(t, state)
	if len(lines) != 1 || !drainLogLine.MatchString(lines[0]) || !strings.HasSuffix(lines[0], " ended=batch stamp=written") {
		t.Fatalf("drain.log = %q, want one line ending ended=batch stamp=written", lines)
	}
	stamp, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("setup: no last-drain: %v", err)
	}
	if got, _, _ := strings.Cut(lines[0], " "); got+"\n" != string(stamp) {
		t.Errorf("drain.log time = %q, last-drain = %q, want the same reading", got, stamp)
	}
}

func TestASilentDrainAppendsAnEndedSilenceLine(t *testing.T) {
	ctx, bus, _, path := lastDrainFixture(t)
	if _, err := toolWait(ctx, bus, json.RawMessage(`{"timeout_seconds":1,"max":5}`)); err != nil {
		t.Fatal(err)
	}
	lines := drainLogLines(t, filepath.Dir(path))
	if len(lines) != 1 || !drainLogLine.MatchString(lines[0]) || !strings.HasSuffix(lines[0], " ended=silence stamp=written") {
		t.Fatalf("drain.log = %q, want one line ending ended=silence stamp=written", lines)
	}
}

func TestAFailedStampStillAppendsALineSayingSo(t *testing.T) {
	ctx, bus, _, path := lastDrainFixture(t)
	// A directory where last-drain belongs makes the rename fail.
	if err := os.MkdirAll(filepath.Join(path, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := toolWait(ctx, bus, json.RawMessage(`{"timeout_seconds":1,"max":5}`)); err != nil {
		t.Fatalf("a failed stamp must not fail the wait: %v", err)
	}
	lines := drainLogLines(t, filepath.Dir(path))
	if len(lines) != 1 || !drainLogLine.MatchString(lines[0]) || !strings.HasSuffix(lines[0], " ended=silence stamp=failed") {
		t.Fatalf("drain.log = %q, want one line ending ended=silence stamp=failed", lines)
	}
}

func TestTwoDrainsAppendTwoLinesAndNeverRewriteTheFirst(t *testing.T) {
	ctx, bus, _, path := lastDrainFixture(t)
	state := filepath.Dir(path)
	var firstLine string
	for i := 0; i < 2; i++ {
		if _, err := toolWait(ctx, bus, json.RawMessage(`{"timeout_seconds":1,"max":5}`)); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first := drainLogLines(t, state)
			if len(first) != 1 {
				t.Fatalf("after one drain drain.log = %q, want one line", first)
			}
			firstLine = first[0]
		}
	}
	lines := drainLogLines(t, state)
	if len(lines) != 2 || lines[0] != firstLine {
		t.Errorf("drain.log = %q, want two lines with the first unchanged (%q)", lines, firstLine)
	}
}

// Guard, passes before the change: a wait that returns mail with some left
// waiting writes no stamp, so it must write no line either; otherwise every
// busy drain would read as a broken beat.
func TestAWaitThatLeavesMailWaitingAppendsNoDrainLogLine(t *testing.T) {
	ctx, bus, pub, path := lastDrainFixture(t)
	for _, id := range []string{"a", "b", "c", "d"} {
		pub(id)
	}
	if _, err := toolWait(ctx, bus, json.RawMessage(`{"timeout_seconds":2,"max":2}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("setup: last-drain was written with mail left waiting")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "drain.log")); err == nil {
		t.Errorf("a wait with mail left waiting appended a drain.log line")
	}
}
