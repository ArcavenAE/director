package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Build item 3: the state and event files. Every test uses a temp dir.

func stateJSON(t *testing.T, w *PresenceWatch) string {
	t.Helper()
	b, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestWatchStateSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	st := newWatchStore(dir)
	w, c := absentUnit(t)
	if err := st.Save(w); err != nil {
		t.Fatal(err)
	}
	// control: the file exists and is private, so a load that equals the saved
	// state is not a load from nothing.
	info, err := os.Stat(filepath.Join(dir, watchStateFile))
	if err != nil {
		t.Fatalf("CONTROL FAILED: Save wrote no state file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state file mode = %v, want 0600", info.Mode().Perm())
	}
	got, err := st.Load(defaultWatchThresholds())
	if err != nil {
		t.Fatal(err)
	}
	if stateJSON(t, got) != stateJSON(t, w) {
		t.Fatalf("reloaded state differs:\n got %s\nwant %s", stateJSON(t, got), stateJSON(t, w))
	}
	// the reloaded watch carries on: the absent unit comes back after two sightings.
	ev := c.skip(got, 40*time.Second, 5*time.Second, supRow("c1", "a"))
	ev = append(ev, pass(got, c, supRow("c1", "a"))...)
	// the first pass after a restart is a gap: present after it, never back.
	if got.Unit("c1/a").Status != unitPresent || !has(ev, "c1/a", "present-after-gap") || count(ev, "back") != 0 {
		t.Fatalf("a reloaded absent unit should read present-after-gap and not back, got %+v", ev)
	}
	// a fresh dir is a fresh watch.
	fresh, err := newWatchStore(t.TempDir()).Load(defaultWatchThresholds())
	if err != nil || len(fresh.Units) != 0 || !fresh.LastPass.IsZero() {
		t.Fatalf("a missing state file should load as a fresh watch, got %+v, %v", fresh, err)
	}
	// no temp files are left beside the state file.
	left, _ := filepath.Glob(filepath.Join(dir, "*.tmp*"))
	if len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

func TestWatchRestartRecordsDownGap(t *testing.T) {
	dir := t.TempDir()
	st := newWatchStore(dir)
	// the saved unit is absent, so a return was possible across the restart.
	w, c := absentUnit(t)
	last := c.wall
	if err := st.Save(w); err != nil {
		t.Fatal(err)
	}
	// control: the same absent unit in the same process, a pass on time later,
	// records no gap, and two sightings do report back. The restart below must
	// report neither a gap-free pass nor a back.
	if ev := pass(w, c, supRow("c1", "a")); count(ev, "watch-down") != 0 {
		t.Fatalf("CONTROL FAILED: a pass on time should record no gap, got %+v", ev)
	}
	if ev := pass(w, c, supRow("c1", "a")); !has(ev, "c1/a", "back") {
		t.Fatalf("CONTROL FAILED: two sightings of the absent unit must report back, got %+v", ev)
	}

	// a new process loads the state and passes 40 seconds after the saved pass.
	re, err := st.Load(defaultWatchThresholds())
	if err != nil {
		t.Fatal(err)
	}
	now := last.Add(40 * time.Second)
	ev := re.Observe(now, 2*time.Second, watchScan{Rows: []watchRow{supRow("c1", "a")}})
	if count(ev, "watch-down") != 1 || len(re.Downs) != 1 || !re.Downs[0].From.Equal(last) || !re.Downs[0].To.Equal(now) {
		t.Fatalf("a restart should record one down gap from the saved pass to now, got %+v, downs %+v", ev, re.Downs)
	}
	if count(ev, "back") != 0 || count(ev, "absent") != 0 {
		t.Fatalf("a restart must not infer a transition, got %+v", ev)
	}
	// the gap reaches the event file and reads back.
	if err := st.Append(ev); err != nil {
		t.Fatal(err)
	}
	read, err := st.Events()
	if err != nil {
		t.Fatal(err)
	}
	if count(read, "watch-down") != 1 {
		t.Fatalf("the event file should hold the down gap, got %+v", read)
	}
}

func TestWatchEventsAppendInOrderAndSurviveATornLine(t *testing.T) {
	dir := t.TempDir()
	st := newWatchStore(dir)
	t0 := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	if err := st.Append([]watchEvent{{At: t0, Kind: "absent", Unit: "c1"}, {At: t0.Add(time.Minute), Kind: "back", Unit: "c1"}}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, watchEventsFile))
	if err != nil {
		t.Fatalf("CONTROL FAILED: Append wrote no event file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("event file mode = %v, want 0600", info.Mode().Perm())
	}
	// a crash mid-write leaves a torn last line; it is skipped, and the next
	// append starts on its own line.
	f, err := os.OpenFile(filepath.Join(dir, watchEventsFile), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"at":"2026-10-19T12:02:00Z","kind":"abs`); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := st.Append([]watchEvent{{At: t0.Add(3 * time.Minute), Kind: "absent", Unit: "c2"}}); err != nil {
		t.Fatal(err)
	}
	read, err := st.Events()
	if err != nil {
		t.Fatal(err)
	}
	if len(read) != 3 || read[0].Kind != "absent" || read[1].Kind != "back" || read[2].Unit != "c2" {
		t.Fatalf("events = %+v, want absent, back, then the c2 absent in order", read)
	}
}

func TestWatchFailedFsyncIsAnError(t *testing.T) {
	dir := t.TempDir()
	st := newWatchStore(dir)
	w, _ := established(t)
	if err := st.Save(w); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, watchStateFile))
	if len(before) == 0 {
		t.Fatal("CONTROL FAILED: Save wrote nothing")
	}
	st.syncFile = func(*os.File) error { return errors.New("disk full") }
	w2, _ := absentUnit(t)
	if err := st.Save(w2); err == nil {
		t.Fatal("a failed fsync on the state file should be an error")
	}
	if after, _ := os.ReadFile(filepath.Join(dir, watchStateFile)); string(after) != string(before) {
		t.Fatal("a failed save must leave the old state file as it was")
	}
	if err := st.Append([]watchEvent{{Kind: "absent", Unit: "c1"}}); err == nil {
		t.Fatal("a failed fsync on the event file should be an error")
	}
}
