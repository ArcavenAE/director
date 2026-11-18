package main

import (
	"os"
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
