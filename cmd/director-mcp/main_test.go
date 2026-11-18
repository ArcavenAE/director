package main

import (
	"context"
	"fmt"
	"os"
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
	defer os.RemoveAll(scratch)
	os.Setenv("HOME", scratch)
	os.Setenv("DIRECTOR_STATE", filepath.Join(scratch, ".director", "state"))
	if err := homeGuardHolds(scratch); err != nil {
		fmt.Fprintf(os.Stderr, "director-mcp tests: %v\n", err)
		return 1
	}
	code := m.Run()
	// A test that set HOME or DIRECTOR_STATE with os.Setenv and did not restore it
	// would let later tests reach the real home, so the run fails.
	if err := homeGuardHolds(scratch); err != nil && code == 0 {
		fmt.Fprintf(os.Stderr, "director-mcp tests: after the run, %v\n", err)
		return 1
	}
	return code
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
