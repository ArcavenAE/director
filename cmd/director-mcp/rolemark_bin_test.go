package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestShimStartHoldsTheDirectorIdentityOnlyWithTheMark runs the built shim, so
// it fails if main stops calling the check before connect, and it pins the
// check after --preflight: cast-launch runs a preflight child before it sets the
// mark, so a check above the preflight return would fail every cast director.
// The test process is the shim's parent here, so its pid is the right mark.
func TestShimStartHoldsTheDirectorIdentityOnlyWithTheMark(t *testing.T) {
	bin := buildShim(t, t.TempDir(), true)
	run := func(args []string, extra ...string) (int, string) {
		cmd := exec.Command(bin, args...)
		cmd.Env = append([]string{"PATH=/usr/bin:/bin", "NATS_URL=nats://127.0.0.1:1", "DIRECTOR_TEAM=ops", "DIRECTOR_WORKSPACE=w"}, extra...)
		var errb bytes.Buffer
		cmd.Stderr = &errb
		done := make(chan error, 1)
		if err := cmd.Start(); err != nil {
			t.Fatalf("start shim: %v", err)
		}
		go func() { done <- cmd.Wait() }()
		var err error
		select {
		case err = <-done:
		case <-time.After(20 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Fatalf("shim %v did not exit within 20s: %q", args, errb.String())
		}
		code := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		return code, errb.String()
	}
	const refusal = "refusing the director identity"
	mark := "DIRECTOR_ROLE_MARK=" + strconv.Itoa(os.Getpid())

	if code, out := run(nil, "DIRECTOR_AGENT_ID=director"); code != 2 || !strings.Contains(out, refusal) || !strings.Contains(out, "(agent id director)") || strings.Contains(out, "bus connect failed") {
		t.Errorf("unmarked director: want exit 2 with the refusal before any connect, got %d: %q", code, out)
	}
	if code, out := run(nil, "DIRECTOR_AGENT_ID=seat-a", "DIRECTOR_GLOBAL_ROLE=director", "DIRECTOR_GLOBAL_DOMAIN=global", "DIRECTOR_CLUSTER=c", "DIRECTOR_ROLE_MARK=1"); code != 2 || !strings.Contains(out, "does not match this shim's parent pid") {
		t.Errorf("director with a wrong mark: want exit 2 naming the mismatch, got %d: %q", code, out)
	}
	if code, out := run(nil, "DIRECTOR_AGENT_ID=director", mark); strings.Contains(out, refusal) || !strings.Contains(out, "bus connect failed") {
		t.Errorf("marked director: want past the check to the dead broker, got %d: %q", code, out)
	}
	if code, out := run(nil, "DIRECTOR_AGENT_ID=seat-a"); strings.Contains(out, refusal) || !strings.Contains(out, "bus connect failed") {
		t.Errorf("unmarked worker: want no refusal, got %d: %q", code, out)
	}
	if _, out := run([]string{"--preflight"}, "DIRECTOR_AGENT_ID=director"); strings.Contains(out, refusal) {
		t.Errorf("unmarked director preflight: the check must run after preflight, got %q", out)
	}
}
