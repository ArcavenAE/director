package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// underDir reports whether path sits inside dir, once both are resolved
// through symlinks (on macOS the temp directory is under /var, a link to
// /private/var).
func underDir(path, dir string) bool {
	p, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		p = filepath.Dir(path)
	}
	d, err := filepath.EvalSymlinks(dir)
	if err != nil {
		d = dir
	}
	p = filepath.Join(p, filepath.Base(path))
	return p == d || strings.HasPrefix(p, d+string(filepath.Separator))
}

// A test run must not resolve a state path under the developer's real home:
// director#682, where four tests left a fresh ~/.director/state/last-drain.
// TestMain points HOME and DIRECTOR_STATE at a scratch directory for every
// test, so the paths a test resolves sit under the temp directory.
func TestHomeGuardKeepsTheStatePathOffTheRealHome(t *testing.T) {
	tmp := os.TempDir()
	if home := os.Getenv("HOME"); !underDir(home, tmp) {
		t.Errorf("HOME = %q is outside the temp directory %q", home, tmp)
	}
	dir, err := stateDir()
	if err != nil {
		t.Fatalf("stateDir: %v", err)
	}
	if !underDir(dir, tmp) {
		t.Errorf("stateDir() = %q is outside the temp directory %q", dir, tmp)
	}
}

// The guard's own check names the offending path when HOME or the state
// directory resolves outside the directory it was given.
func TestHomeGuardHoldsReportsAPathOutsideScratch(t *testing.T) {
	other := t.TempDir()
	err := homeGuardHolds(other)
	if err == nil {
		t.Fatalf("homeGuardHolds(%q) = nil, want an error: HOME is the guard's scratch directory, not this one", other)
	}
	if !strings.Contains(err.Error(), "outside the scratch home") {
		t.Errorf("error = %q, want it to say the path is outside the scratch home", err)
	}
}

// A test that runs the go tool (version_test.go builds the binary) must reach
// the developer's module and build caches, not a cold copy under the scratch
// HOME: with HOME moved, go would default both under it, fetch every module
// again, and leave a read-only module cache behind that the guard cannot
// remove.
func TestHomeGuardKeepsTheGoCachesOffTheScratchHome(t *testing.T) {
	out, err := exec.Command("go", "env", "GOMODCACHE", "GOCACHE").Output()
	if err != nil {
		t.Fatalf("go env: %v", err)
	}
	lines := strings.Fields(string(out))
	if len(lines) != 2 {
		t.Fatalf("go env printed %q, want GOMODCACHE and GOCACHE", out)
	}
	home := os.Getenv("HOME")
	for i, name := range []string{"GOMODCACHE", "GOCACHE"} {
		if underDir(lines[i], home) {
			t.Errorf("%s = %q is under the scratch HOME %q", name, lines[i], home)
		}
	}
}
