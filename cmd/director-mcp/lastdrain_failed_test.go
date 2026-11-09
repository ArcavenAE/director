package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// C6 follow-up. A tier whose consumer state cannot be read is left out of the
// remaining map, so a local 0 beside an unreadable global read as a clean
// drain and wrote last-drain. The beat must not be written when any tier the
// shim tried to read failed to read.

// With the session's global durable gone from the hub, the global read fails
// while local reads 0. remainingRead says which tier failed, and the beat is
// not written for it.
func TestFailedGlobalReadWritesNoLastDrain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	state := t.TempDir()
	t.Setenv("DIRECTOR_STATE", state)
	bus, _ := globalLossFixture(t, ctx)

	rem, failed := bus.remainingRead(ctx)
	if rem["local"] != 0 {
		t.Fatalf("setup: remaining = %v, want local 0", rem)
	}
	if len(failed) != 1 || failed[0] != "global" {
		t.Fatalf("failed tiers = %v, want [global]", failed)
	}
	beatLastDrain(rem, failed, time.Now())
	if _, err := os.Stat(filepath.Join(state, "last-drain")); err == nil {
		t.Errorf("last-drain was written although the global read failed")
	}
}

// With every tier read, a clean drain still writes the beat.
func TestReadTiersAllZeroStillWriteLastDrain(t *testing.T) {
	state := t.TempDir()
	t.Setenv("DIRECTOR_STATE", state)
	beatLastDrain(map[string]uint64{"local": 0, "global": 0}, nil, time.Now())
	if _, err := os.Stat(filepath.Join(state, "last-drain")); err != nil {
		t.Errorf("a clean drain wrote no last-drain: %v", err)
	}
}

// A failed tier blocks the beat whatever the other tiers say.
func TestAnyFailedTierBlocksTheBeat(t *testing.T) {
	for _, failed := range [][]string{{"global"}, {"local"}, {"local", "global"}} {
		state := t.TempDir()
		t.Setenv("DIRECTOR_STATE", state)
		beatLastDrain(map[string]uint64{"local": 0}, failed, time.Now())
		if _, err := os.Stat(filepath.Join(state, "last-drain")); err == nil {
			t.Errorf("failed %v: last-drain was written", failed)
		}
	}
}
