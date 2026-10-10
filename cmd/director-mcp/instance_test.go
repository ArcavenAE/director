package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// The property under test is a cross-PROCESS one, so the test has to be one
// too. Generating ids in a loop inside a single process passes with the old
// clock-seeded source and proves nothing: within one process the stream is
// shared deliberately and never repeats. The defect was two processes drawing
// the SAME stream because they seeded from the same clock tick, so the only
// test that can fail on it starts two processes at once.
//
// Re-execs this test binary with a guard variable set; the child prints one id
// and exits. Deliberately NOT a re-run of the old 300ms-stagger control, which
// measured the workaround (separating the inits) rather than the fix.
const instanceProbeEnv = "DIRECTOR_INSTANCE_PROBE"

func TestInstanceIDsDifferAcrossSimultaneousProcesses(t *testing.T) {
	if os.Getenv(instanceProbeEnv) == "1" {
		fmt.Printf("ID:%s\n", newInstanceID())
		return
	}

	const pairs = 50
	for i := range pairs {
		ids := runSimultaneousPair(t)

		// Both sides are checked non-empty BEFORE they are compared. An
		// equality test between two empty strings passes, so a failed probe
		// would otherwise read as proof of distinctness: the same hazard the
		// round-trip harness's check 0 carries, and it is cheaper to anchor
		// than to reason about.
		for j, id := range ids {
			if id == "" {
				t.Fatalf("pair %d: child %d produced no id, so the pair proves nothing", i, j)
			}
		}
		if ids[0] == ids[1] {
			t.Fatalf("pair %d: two processes started simultaneously minted the same instance %s", i, ids[0])
		}
	}
}

func runSimultaneousPair(t *testing.T) [2]string {
	t.Helper()
	var out [2]bytes.Buffer
	var wg sync.WaitGroup
	start := make(chan struct{})
	for j := range 2 {
		wg.Add(1)
		go func(j int) {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$")
			cmd.Env = append(os.Environ(), instanceProbeEnv+"=1")
			cmd.Stdout = &out[j]
			<-start
			if err := cmd.Run(); err != nil {
				t.Errorf("child %d: %v", j, err)
			}
		}(j)
	}
	close(start) // release both as close together as the runtime allows
	wg.Wait()

	var ids [2]string
	for j := range out {
		for _, line := range strings.Split(out[j].String(), "\n") {
			if after, ok := strings.CutPrefix(line, "ID:"); ok {
				ids[j] = strings.TrimSpace(after)
				break
			}
		}
	}
	return ids
}
