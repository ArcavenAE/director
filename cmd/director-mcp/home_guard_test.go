package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// underDir reports whether path sits inside dir, once both are resolved
// through symlinks (on macOS the temp directory is under /var, a link to
// /private/var). A path whose last levels do not exist yet still resolves: the
// longest existing prefix is resolved and the rest is joined back on.
func underDir(path, dir string) bool {
	p := resolveExisting(path)
	d, err := filepath.EvalSymlinks(dir)
	if err != nil {
		d = dir
	}
	return p == d || strings.HasPrefix(p, d+string(filepath.Separator))
}

// resolveExisting resolves the longest prefix of path that exists through
// symlinks and joins the levels that do not exist onto it.
func resolveExisting(path string) string {
	path = filepath.Clean(path)
	rest := ""
	for {
		if r, err := filepath.EvalSymlinks(path); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(path)
		if parent == path {
			return filepath.Join(path, rest)
		}
		rest = filepath.Join(filepath.Base(path), rest)
		path = parent
	}
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

// On macOS the default TMPDIR is under /var, a link to /private/var, and the
// scratch home the guard makes has no .director directory until a test writes
// one. A state path two levels below a directory reached through a link must
// still read as inside it, or the guard exits 1 before the first test runs.
func TestUnderDirReadsAPathBelowUncreatedLevelsThroughASymlinkAsInside(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(link, ".director", "state")
	if !underDir(path, link) {
		t.Errorf("underDir(%q, %q) = false, want true: the path sits two uncreated levels below the link", path, link)
	}
	if !underDir(path, real) {
		t.Errorf("underDir(%q, %q) = false, want true: the link points at that directory", path, real)
	}
}

// The check after the run names the leaked path whatever the tests returned,
// so a failing run that also leaked HOME shows both causes.
func TestFinishRunReportsALeakedHomeWhenTheTestsFailed(t *testing.T) {
	scratch := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	var stderr bytes.Buffer
	if code := finishRun(1, scratch, &stderr); code != 1 {
		t.Errorf("finishRun(1, ...) = %d, want 1: a failed run stays failed", code)
	}
	if !strings.Contains(stderr.String(), "after the run") {
		t.Errorf("stderr = %q, want the after-the-run guard error even though the tests failed", stderr.String())
	}
}

// A scratch home that cannot be removed leaves files behind on the
// developer's disk, so the run says so on stderr.
func TestRemoveScratchReportsAFailureToRemove(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root removes a read-only directory's files, so nothing fails")
	}
	scratch := filepath.Join(t.TempDir(), "scratch")
	locked := filepath.Join(scratch, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	var stderr bytes.Buffer
	removeScratch(scratch, &stderr)
	if !strings.Contains(stderr.String(), scratch) {
		t.Errorf("stderr = %q, want it to name the scratch directory %q that was not removed", stderr.String(), scratch)
	}
}

// go keeps its config file and its GOPATH under HOME by default, so the guard
// moving HOME would send both to the scratch directory: a go env or go install
// run from a test would read an empty config and write into a directory the
// guard cannot remove.
func TestHomeGuardKeepsGoenvAndGopathOffTheScratchHome(t *testing.T) {
	out, err := exec.Command("go", "env", "-json", "GOENV", "GOPATH").Output()
	if err != nil {
		t.Fatalf("go env: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("go env -json printed %q: %v", out, err)
	}
	home := os.Getenv("HOME")
	for _, name := range []string{"GOENV", "GOPATH"} {
		for _, p := range filepath.SplitList(got[name]) {
			if underDir(p, home) {
				t.Errorf("%s = %q is under the scratch HOME %q", name, p, home)
			}
		}
	}
}
