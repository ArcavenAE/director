package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Two resolvers that read the same reply at the same moment must produce exactly
// one resolution entry for a send: the session of the first to win, never two
// entries that name two sessions. The resolvers may be goroutines of one shim or
// two processes (two shims of one seat overlapping at a successor handover), so
// both are covered, and a failed resolution must not leave the send stuck.

func raceReply(id, agent string) *Envelope {
	return &Envelope{MessageID: "reply-" + agent, InReplyTo: id, Sender: Sender{AgentID: agent, Workspace: "w"}}
}

func resolutionEntries(t *testing.T, log, id string) []string {
	t.Helper()
	var out []string
	for _, e := range relayEntries(t, log) {
		if strings.Contains(e, "session resolved for message "+id) {
			out = append(out, e)
		}
	}
	return out
}

func raceFixture(t *testing.T) string {
	t.Helper()
	askFixture(t, "I-7")
	return relayFixture(t)
}

func TestTwoConcurrentResolversWriteOneResolutionEntryPerSend(t *testing.T) {
	log := raceFixture(t)
	for round := 0; round < 4; round++ {
		id := fmt.Sprintf("send-%d", round)
		if err := rememberUnresolved(id, "I-7", "role://t/builder"); err != nil {
			t.Fatal(err)
		}
		gate := make(chan struct{})
		var wg sync.WaitGroup
		for _, agent := range []string{"builder-a", "builder-b"} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-gate
				resolveRelaySession(context.Background(), raceReply(id, agent))
			}()
		}
		close(gate)
		wg.Wait()
		es := resolutionEntries(t, log, id)
		if len(es) != 1 {
			t.Fatalf("round %d: %d resolution entries for one send, want exactly 1:\n%v", round, len(es), es)
		}
	}
}

// TestRelayResolveHelper is the child of the two-process test. It runs only when
// RELAY_RESOLVE_HELPER names the reply's agent, waits until RELAY_RESOLVE_AT
// (unix nanoseconds) so both children start together, and resolves once.
func TestRelayResolveHelper(t *testing.T) {
	agent := os.Getenv("RELAY_RESOLVE_HELPER")
	if agent == "" {
		t.Skip("child of TestTwoProcessesWriteOneResolutionEntryPerSend")
	}
	at, _ := strconv.ParseInt(os.Getenv("RELAY_RESOLVE_AT"), 10, 64)
	for time.Now().UnixNano() < at {
		time.Sleep(time.Millisecond)
	}
	id := os.Getenv("RELAY_RESOLVE_ID")
	// The test binary's own setup points DIRECTOR_STATE at a scratch home, so the parent's state dir comes by its own name.
	t.Setenv("DIRECTOR_STATE", os.Getenv("RELAY_RESOLVE_STATE"))
	resolveRelaySession(context.Background(), raceReply(id, agent))
}

func TestTwoProcessesWriteOneResolutionEntryPerSend(t *testing.T) {
	log := raceFixture(t)
	id := "send-procs"
	if err := rememberUnresolved(id, "I-7", "role://t/builder"); err != nil {
		t.Fatal(err)
	}
	at := strconv.FormatInt(time.Now().Add(1500*time.Millisecond).UnixNano(), 10)
	var cmds []*exec.Cmd
	var outs []*bytes.Buffer
	for _, agent := range []string{"builder-a", "builder-b"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestRelayResolveHelper$", "-test.count=1")
		cmd.Env = append(os.Environ(), "RELAY_RESOLVE_HELPER="+agent, "RELAY_RESOLVE_AT="+at, "RELAY_RESOLVE_ID="+id, "RELAY_RESOLVE_STATE="+os.Getenv("DIRECTOR_STATE"))
		out := &bytes.Buffer{}
		cmd.Stdout, cmd.Stderr = out, out
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		cmds = append(cmds, cmd)
		outs = append(outs, out)
	}
	for i, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("helper: %v\n%s", err, outs[i])
		}
	}
	es := resolutionEntries(t, log, id)
	if len(es) != 1 {
		t.Fatalf("%d resolution entries for one send from two processes, want exactly 1:\n%v", len(es), es)
	}
}

// A resolution that fails (relay-log cannot run) leaves the send pending, so the
// next reply, with relay-log back, still resolves it. A claim taken for the first
// attempt must not outlive it. This passes before the fix too: it pins what the
// fix must keep.
func TestAFailedResolutionLeavesTheSendToTheNextReply(t *testing.T) {
	log := raceFixture(t)
	id := "send-retry"
	if err := rememberUnresolved(id, "I-7", "role://t/builder"); err != nil {
		t.Fatal(err)
	}
	good := os.Getenv("DIRECTOR_HOME")
	t.Setenv("DIRECTOR_HOME", t.TempDir()) // no bin/relay-log here
	resolveRelaySession(context.Background(), raceReply(id, "builder-a"))
	t.Setenv("DIRECTOR_HOME", good)
	resolveRelaySession(context.Background(), raceReply(id, "builder-b"))
	es := resolutionEntries(t, log, id)
	if len(es) != 1 || !strings.Contains(es[0], "**Session:** w/builder-b") {
		t.Fatalf("want one resolution naming w/builder-b after the failed first try, got %d:\n%v", len(es), es)
	}
}
