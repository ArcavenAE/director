package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestParseArgsAcceptsVersionAloneAndRefusesItWithOthers(t *testing.T) {
	for _, a := range [][]string{{"--version"}, {"version"}} {
		got, err := parseArgs(a)
		if err != nil || got.mode != "version" {
			t.Errorf("%v: got %+v, %v", a, got, err)
		}
	}
	for _, a := range [][]string{{"--version", "--json"}, {"--version", "x"}, {"--versions"}} {
		if _, err := parseArgs(a); err == nil {
			t.Errorf("%v was accepted", a)
		}
	}
}

func TestVersionLineNamesAStampedBuildAndMarksAnAdHocOne(t *testing.T) {
	was := [3]string{version, revision, channel}
	defer func() { version, revision, channel = was[0], was[1], was[2] }()
	version, revision, channel = "0.1.0-alpha.20261001.000000.abc1234", "abc1234def", "alpha"
	if got, want := versionLine(), "director-mcp 0.1.0-alpha.20261001.000000.abc1234 rev abc1234def channel alpha"; got != want {
		t.Fatalf("stamped: %q, want %q", got, want)
	}
	if shimRevision() != "abc1234def" {
		t.Fatalf("a stamped revision wins over the vcs stamp: %q", shimRevision())
	}
	version, revision, channel = "", "", ""
	if got := versionLine(); !strings.HasPrefix(got, "director-mcp devel rev ") || !strings.HasSuffix(got, " channel dev") {
		t.Fatalf("ad hoc: %q", got)
	}
}

// The built binary prints the stamp it was built with, exits 0, and needs no
// broker or config: no NATS_URL is set and the environment is empty.
func TestBuiltShimPrintsItsStampWithoutAnyConfig(t *testing.T) {
	bin := buildShim(t, t.TempDir(), true)
	ld := "-X main.version=9.9.9-test -X main.revision=0123456789abcdef0123456789abcdef01234567 -X main.channel=testchan"
	stamped := t.TempDir()
	cmd := exec.Command("go", "build", "-buildvcs=false", "-ldflags", ld, "-o", stamped+"/director-mcp", ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	for _, a := range []string{"--version", "version"} {
		c := exec.Command(stamped+"/director-mcp", a)
		c.Env = []string{"PATH=/usr/bin:/bin"}
		out, err := c.Output()
		want := "director-mcp 9.9.9-test rev 0123456789abcdef0123456789abcdef01234567 channel testchan\n"
		if err != nil || string(out) != want {
			t.Errorf("%s: %q, %v; want %q", a, out, err, want)
		}
	}
	// An unstamped build says devel and still exits 0.
	out, err := exec.Command(bin, "--version").Output()
	if err != nil || !strings.HasPrefix(string(out), "director-mcp devel rev ") {
		t.Errorf("unstamped: %q, %v", out, err)
	}
}
