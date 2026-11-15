package main

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A seat cast by sim/twin/cast-launch.sh reads its relay log through RELAY_LOG
// (relayLogPath). The launcher is what has to set it, per seat, because the
// seat is known only at cast; with it unset the lane is not built and
// inbox_summary warns on every call (director#634). This runs the real launcher
// against stubs (no broker, no claude, no wardrobe checkout) and then builds the
// lane with the RELAY_LOG the launched session inherited.

func writeStub(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// castEnv runs the launcher for a builder seat with a cleared environment and
// returns the environment the stub claude was started with.
func castEnv(t *testing.T, extra ...string) map[string]string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found")
	}
	launcher, err := filepath.Abs(filepath.Join("..", "..", "sim", "twin", "cast-launch.sh"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeStub(t, filepath.Join(root, "wardrobe", "scripts", "slice.sh"), "#!/bin/sh\necho stub slice\n")
	if err := os.MkdirAll(filepath.Join(root, "wardrobe", "contents", "roles"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeStub(t, filepath.Join(root, "bin", "director-mcp"), "#!/bin/sh\nexit 0\n")
	out := filepath.Join(root, "claude.env")
	writeStub(t, filepath.Join(root, "bin", "claude"), "#!/bin/sh\nenv > '"+out+"'\nexit 0\n")
	cmd := exec.Command(bash, launcher)
	cmd.Env = append([]string{
		"PATH=" + filepath.Join(root, "bin") + ":/usr/bin:/bin",
		"HOME=" + root,
		"TMPDIR=" + root,
		"MARVEL_ROLE=builder",
		"MARVEL_SESSION=verify-builder-0",
		"WARDROBE_ROOT=" + filepath.Join(root, "wardrobe", "contents"),
		"DIRECTOR_SHIM_BIN=" + filepath.Join(root, "bin", "director-mcp"),
		"TWIN_CWD=" + root,
		"DIRECTOR_TEAM=fleet",
		"DIRECTOR_WORKSPACE=verifyws",
	}, extra...)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("launcher failed: %v\n%s", err, b)
	}
	f, err := os.Open(out)
	if err != nil {
		t.Fatalf("the stub claude did not run: %v", err)
	}
	defer f.Close()
	env := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok {
			env[k] = v
		}
	}
	return env
}

func TestLaunchedSeatBuildsTheLaneWithNoWarning(t *testing.T) {
	state := t.TempDir()
	env := castEnv(t, "DIRECTOR_STATE="+state)
	rl := env["RELAY_LOG"]
	if rl == "" {
		t.Fatalf("the launcher exported no RELAY_LOG, so the seat's inbox_summary warns that replies cannot be joined to asks")
	}
	if err := os.WriteFile(filepath.Join(state, "asks.jsonl"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DIRECTOR_STATE", state)
	t.Setenv("DIRECTOR_HOME", t.TempDir()) // no installed relay-log to fall back on
	t.Setenv("RELAY_LOG", rl)
	l := loadLane(nil, laneNow)
	if l.Warning != "" {
		t.Fatalf("lane warning = %q, want none for RELAY_LOG=%s as the launcher exported it", l.Warning, rl)
	}
}
