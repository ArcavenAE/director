package main

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestBusURLKeepsTheLocalDefaultForAHandWiredShim(t *testing.T) {
	for name, m := range map[string]map[string]string{
		"nothing set":     {},
		"marker is zero":  {"DIRECTOR_BUS_REQUIRED": "0"},
		"marker is empty": {"DIRECTOR_BUS_REQUIRED": ""},
	} {
		got, err := busURL(envMap(m))
		if err != nil || got != "nats://127.0.0.1:4222" {
			t.Errorf("%s: got %q, %v; want the local default", name, got, err)
		}
	}
}

func TestBusURLUsesNATSURLWhetherOrNotTheMarkerIsSet(t *testing.T) {
	for _, marker := range []string{"", "1"} {
		got, err := busURL(envMap(map[string]string{"NATS_URL": "nats://broker.example:4222", "DIRECTOR_BUS_REQUIRED": marker}))
		if err != nil || got != "nats://broker.example:4222" {
			t.Errorf("marker %q: got %q, %v", marker, got, err)
		}
	}
}

func TestBusURLFailsLoudlyWhenALaunchedSeatGotNoURL(t *testing.T) {
	got, err := busURL(envMap(map[string]string{"DIRECTOR_BUS_REQUIRED": "1"}))
	if err == nil || got != "" {
		t.Fatalf("got %q, %v; want an error and no URL", got, err)
	}
	if !strings.Contains(err.Error(), "NATS_URL") || strings.Contains(err.Error(), "\n") {
		t.Errorf("the message must name NATS_URL on one line: %q", err)
	}
}

// The built binary exits 2 with one line naming NATS_URL, before it connects to
// anything, when the launcher's marker is set and no URL arrived. HOME is a
// scratch dir, so no seat password file is read.
func TestBuiltShimExitsTwoWhenTheMarkerIsSetAndNoURLArrived(t *testing.T) {
	bin := buildShim(t, t.TempDir(), true)
	c := exec.Command(bin)
	c.Env = []string{
		"PATH=/usr/bin:/bin", "HOME=" + t.TempDir(),
		"DIRECTOR_AGENT_ID=verify-seat", "DIRECTOR_TEAM=demo", "DIRECTOR_WORKSPACE=first",
		"DIRECTOR_BUS_REQUIRED=1",
	}
	out, err := c.CombinedOutput()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 2 {
		t.Fatalf("want exit 2, got %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	last := lines[len(lines)-1]
	if !strings.Contains(last, "NATS_URL") || !strings.Contains(last, "DIRECTOR_BUS_REQUIRED") {
		t.Errorf("the last line must name NATS_URL and the marker: %q", last)
	}
}
