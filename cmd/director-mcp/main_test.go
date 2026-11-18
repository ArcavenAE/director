package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// realEarlierDurableProbe is the shipped lookup that decides whether a seat's
// first global durable starts at the stream head (director#594).
var realEarlierDurableProbe = earlierDurableProbe

// The broker tests written before director#594 publish mail before the seat
// attaches and expect to read it, which is the replay a seat with an earlier
// durable still gets. They keep it through a probe that always finds one. The
// head-start tests call useRealProbe to run the shipped lookup.
func TestMain(m *testing.M) {
	earlierDurableProbe = func(context.Context, *globalTier, string, string) (bool, error) { return true, nil }
	os.Exit(runGuarded(m))
}

// runGuarded runs the package's tests with HOME and DIRECTOR_STATE pointed at a
// scratch directory, so no test reaches the developer's real home through the
// state directory or any other path under HOME (director#682, where four tests
// left a fresh ~/.director/state/last-drain). A test that needs another value
// sets it with t.Setenv, which restores the guard's value afterwards.
// TestHomeGuardKeepsTheStatePathOffTheRealHome fails if the paths resolve
// outside the temp directory.
func runGuarded(m *testing.M) int {
	scratch, err := os.MkdirTemp("", "director-mcp-test-home-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "director-mcp tests: no scratch home: %v\n", err)
		return 1
	}
	defer removeScratch(scratch, os.Stderr)
	pinGoCaches()
	os.Setenv("HOME", scratch)
	os.Setenv("DIRECTOR_STATE", filepath.Join(scratch, ".director", "state"))
	if err := homeGuardHolds(scratch); err != nil {
		fmt.Fprintf(os.Stderr, "director-mcp tests: %v\n", err)
		return 1
	}
	return finishRun(m.Run(), scratch, os.Stderr)
}

// finishRun checks the guard after the tests ran and returns the exit code. A
// test that set HOME or DIRECTOR_STATE with os.Setenv and did not restore it
// would let later tests reach the real home, so the run fails. The leak is
// reported whatever the tests returned, so a failing run that also leaked shows
// both causes.
func finishRun(code int, scratch string, stderr io.Writer) int {
	if err := homeGuardHolds(scratch); err != nil {
		fmt.Fprintf(stderr, "director-mcp tests: after the run, %v\n", err)
		if code == 0 {
			return 1
		}
	}
	return code
}

// removeScratch removes the scratch home the run made, and says so on stderr
// when it cannot, since what stays behind is on the developer's disk.
func removeScratch(scratch string, stderr io.Writer) {
	if err := os.RemoveAll(scratch); err != nil {
		fmt.Fprintf(stderr, "director-mcp tests: could not remove the scratch home %q: %v\n", scratch, err)
	}
}

// pinGoCaches sets the module cache, the build cache, the go config file and
// GOPATH to where go finds them now, before HOME moves, unless the caller set
// them. Without it a test that runs the go tool (version_test.go builds the
// binary) gets cold caches under the scratch HOME, fetches every module again
// and leaves a read-only module cache behind, and go reads an empty config
// from the scratch HOME. go env -json carries the values whole, since GOENV
// holds a space on macOS. When go cannot be asked, they stay unset and the test
// that needs go fails on its own.
func pinGoCaches() {
	names := []string{"GOMODCACHE", "GOCACHE", "GOENV", "GOPATH"}
	out, err := exec.Command("go", append([]string{"env", "-json"}, names...)...).Output()
	if err != nil {
		return
	}
	var got map[string]string
	if err := json.Unmarshal(out, &got); err != nil {
		return
	}
	for _, name := range names {
		if os.Getenv(name) == "" && got[name] != "" {
			os.Setenv(name, got[name])
		}
	}
}

// homeGuardHolds returns an error if HOME or the state directory resolves
// outside the scratch directory.
func homeGuardHolds(scratch string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("no home directory: %w", err)
	}
	if !underDir(home, scratch) {
		return fmt.Errorf("HOME resolves to %q, outside the scratch home %q", home, scratch)
	}
	dir, err := stateDir()
	if err != nil {
		return fmt.Errorf("stateDir: %w", err)
	}
	if !underDir(dir, scratch) {
		return fmt.Errorf("the state directory resolves to %q, outside the scratch home %q", dir, scratch)
	}
	return nil
}

func useRealProbe(t *testing.T) {
	t.Helper()
	prev := earlierDurableProbe
	earlierDurableProbe = realEarlierDurableProbe
	t.Cleanup(func() { earlierDurableProbe = prev })
}
