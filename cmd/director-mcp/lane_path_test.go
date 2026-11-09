package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The lane reads the relay log where the writer puts it. relay-log (which the
// shim runs as $DIRECTOR_HOME/bin/relay-log) uses $RELAY_LOG, else
// <checkout>/sim/notes/relay-log.md when the script is a symlink into a
// checkout. The lane resolves it the same way, so a default symlink install
// builds the lane with no RELAY_LOG set.

// symlinkInstall builds a checkout with the relay-log script in it and an
// install home whose bin/relay-log is a symlink to that script. It returns the
// home and the relay log path the writer would use.
func symlinkInstall(t *testing.T, withNotesDir bool) (home, logPath string) {
	t.Helper()
	root := t.TempDir()
	scripts := filepath.Join(root, "skills", "director", "scripts")
	if err := os.MkdirAll(scripts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scripts, "relay-log"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(root, "sim", "notes")
	if withNotesDir {
		if err := os.MkdirAll(notes, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	home = t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(scripts, "relay-log"), filepath.Join(home, "bin", "relay-log")); err != nil {
		t.Fatal(err)
	}
	return home, filepath.Join(notes, "relay-log.md")
}

func TestLaneFindsTheRelayLogThroughTheSymlinkInstall(t *testing.T) {
	home, logPath := symlinkInstall(t, true)
	if err := os.WriteFile(logPath, []byte(laneRelayLog), 0o600); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	t.Setenv("DIRECTOR_STATE", state)
	t.Setenv("DIRECTOR_HOME", home)
	t.Setenv("RELAY_LOG", "")
	if err := os.WriteFile(filepath.Join(state, "asks.jsonl"), []byte(`{"ask_id":"I-1","received_at":"2026-11-09T04:59:00Z","text":"x","source":"operator"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l := loadLane([]summaryItem{laneMsg("local", 9, "r", "INFORM", "01MSENT1")}, laneNow)
	if l.Warning != "" || len(l.Items) != 1 || l.Items[0].AskID != "I-1" {
		t.Fatalf("lane = %+v, want the reply for I-1 and no warning: the log is at %s", l, logPath)
	}
}

func TestRelayLogEnvOverridesTheCheckoutFallback(t *testing.T) {
	home, logPath := symlinkInstall(t, true)
	if err := os.WriteFile(logPath, []byte(laneRelayLog), 0o600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "other.md")
	if err := os.WriteFile(other, []byte("# empty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DIRECTOR_STATE", t.TempDir())
	t.Setenv("DIRECTOR_HOME", home)
	t.Setenv("RELAY_LOG", other)
	path, ok := relayLogPath()
	if !ok || path != other {
		t.Fatalf("relayLogPath = %q, %v; want the RELAY_LOG override %q", path, ok, other)
	}
}

func TestNoRelayLogResolvedKeepsTheWarning(t *testing.T) {
	t.Setenv("DIRECTOR_STATE", t.TempDir())
	t.Setenv("RELAY_LOG", "")
	// A checkout with no sim/notes directory, as relay-log refuses to guess.
	home, _ := symlinkInstall(t, false)
	t.Setenv("DIRECTOR_HOME", home)
	l := loadLane([]summaryItem{laneMsg("local", 1, "r", "INFORM", "01MSENT1")}, laneNow)
	if len(l.Items) != 0 || !strings.Contains(l.Warning, "RELAY_LOG") {
		t.Fatalf("lane = %+v, want no items and a warning that names RELAY_LOG", l)
	}
	// And a plain install home with no relay-log at all.
	t.Setenv("DIRECTOR_HOME", t.TempDir())
	l = loadLane(nil, laneNow)
	if !strings.Contains(l.Warning, "RELAY_LOG") {
		t.Fatalf("lane = %+v, want the warning with no relay-log in the home", l)
	}
}
