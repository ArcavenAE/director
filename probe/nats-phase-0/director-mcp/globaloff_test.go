package main

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// A seat that holds the director role with the global tier off runs local-only
// without a word (aae-orc-d408k). The warning names what is missing and what
// still works; the off case stays silent and valid for a worker (R-94).

func TestGlobalOffWarningNamesWhatIsMissingAndWhatStillWorks(t *testing.T) {
	for name, c := range map[string]struct{ globalRole, role, trigger string }{
		"global role lever": {globalRole: "director", trigger: "DIRECTOR_GLOBAL_ROLE=director"},
		"local role lever":  {role: "director", trigger: "DIRECTOR_ROLE=director"},
	} {
		w := globalOffWarning(nil, c.globalRole, c.role)
		for _, want := range []string{c.trigger, "DIRECTOR_GLOBAL_DOMAIN", "DIRECTOR_CLUSTER", "global://", "local"} {
			if !strings.Contains(w, want) {
				t.Errorf("%s: warning lacks %q: %q", name, want, w)
			}
		}
	}
}

func TestGlobalOffStaysSilentForAWorkerAndWhenTheTierIsOn(t *testing.T) {
	on := &globalConfig{Domain: "global", Cluster: "c", Role: roleDirector}
	for name, c := range map[string]struct {
		cfg              *globalConfig
		globalRole, role string
	}{
		"worker, no role":      {},
		"worker with a role":   {role: "builder"},
		"research supervisor":  {role: "research-supervisor"},
		"director, tier is on": {cfg: on, globalRole: "director", role: "director"},
	} {
		if w := globalOffWarning(c.cfg, c.globalRole, c.role); w != "" {
			t.Errorf("%s: warned: %q", name, w)
		}
	}
}

// The shim prints the warning at start. Built binary, dead broker address: the
// warning comes before any connect, so it is on stderr whatever the broker does.
func TestShimStartWarnsAGlobalOffDirectorAndNotAWorker(t *testing.T) {
	bin := buildShim(t, t.TempDir(), true)
	run := func(extra ...string) string {
		cmd := exec.Command(bin, "--preflight")
		cmd.Env = append([]string{"PATH=" + "/usr/bin:/bin", "NATS_URL=nats://127.0.0.1:1", "DIRECTOR_AGENT_ID=seat-a", "DIRECTOR_TEAM=ops", "DIRECTOR_WORKSPACE=w"}, extra...)
		var errb bytes.Buffer
		cmd.Stderr = &errb
		done := make(chan struct{})
		go func() { _ = cmd.Run(); close(done) }()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		return errb.String()
	}
	if out := run("DIRECTOR_GLOBAL_ROLE=director"); !strings.Contains(out, "WARNING") || !strings.Contains(out, "DIRECTOR_GLOBAL_DOMAIN") {
		t.Errorf("director with the tier off: no warning on stderr: %q", out)
	}
	if out := run("DIRECTOR_ROLE=builder"); strings.Contains(out, "WARNING") {
		t.Errorf("worker: warned: %q", out)
	}
}
