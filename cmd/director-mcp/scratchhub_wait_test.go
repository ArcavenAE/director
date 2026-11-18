package main

// A scratch hub that fails to start must say why (ArcavenAE/director#681).
//
// Two CI runs failed with only "scratch hub did not come up" after 6.06 s, and a slow start, a
// crashed server and a taken port all read the same. The wait must name the server's own log and
// its exit state when it gives up. These cases start a server that fails at once, so nothing
// here depends on how long a start takes.
//
// Each case runs the test binary again as a child (the helper-process pattern): the inner test
// does the failing start and is expected to fail, and the outer test reads what the inner run
// printed. They are told apart by the environment variable scratchHubInnerEnv: the outer tests
// set it only for the child, and an inner test run without it returns at once (it neither skips
// nor fails, so a whole-package run keeps its skip count).

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	scratchHubInnerEnv = "SCRATCH_HUB_WAIT_INNER"
	// logMarker is written by the fake server only to the file named by -l, so its presence in the
	// failure output proves the log file was read. errMarker goes only to stderr and is not asserted:
	// a fix that also relays stderr must not be able to pass the log assertion by doing so.
	logMarker = "FAKE_SCRATCH_SERVER_LOG_7f3a"
	errMarker = "FAKE_SCRATCH_SERVER_ERR_7f3a"
)

// writeFailingServer makes a fake nats-server in dir: it appends logMarker to the file named by
// -l when given, writes errMarker to stderr, and exits 3 without ever listening.
func writeFailingServer(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "nats-server")
	script := `#!/bin/sh
log=""
while [ $# -gt 0 ]; do
  case "$1" in
    -l) log="$2"; shift ;;
  esac
  shift
done
if [ -n "$log" ]; then echo "` + logMarker + `: cannot start" >> "$log"; fi
echo "` + errMarker + `: cannot start" >&2
exit 3
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

// runInner runs the named inner test in a child of this test binary and returns what it printed
// and whether it failed.
func runInner(t *testing.T, name string) (out string, failed bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run", "^"+name+"$", "-test.v")
	cmd.Env = append(os.Environ(), scratchHubInnerEnv+"=1")
	b, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("the inner run %s did not finish in 90s:\n%s", name, b)
	}
	return string(b), err != nil
}

// checkFailureNamesServer asserts the output of an inner run that failed its start: the server's log
// line, the log's path (wantLogPath is a substring of it) and the server's exit state.
func checkFailureNamesServer(t *testing.T, out string, failed bool, wantLogPath string) {
	t.Helper()
	if !failed {
		t.Fatalf("the inner run passed; a server that exits at once must fail the start:\n%s", out)
	}
	if !strings.Contains(out, "--- FAIL: TestScratchHubStartFailureInner") {
		t.Fatalf("the inner run failed, but not in its own test:\n%s", out)
	}
	if !strings.Contains(out, logMarker) {
		t.Errorf("the failure output does not carry the server's log line %q:\n%s", logMarker, out)
	}
	if !strings.Contains(out, wantLogPath) {
		t.Errorf("the failure output does not name the server's log file (%q):\n%s", wantLogPath, out)
	}
	if !strings.Contains(out, "exit status 3") {
		t.Errorf("the failure output does not carry the server's exit state (\"exit status 3\"):\n%s", out)
	}
}

// --- the hub user scratch hub (startServer in hubuser_harness_test.go) ---------------------------

func TestScratchHubStartFailureNamesTheServerLogAndExit(t *testing.T) {
	out, failed := runInner(t, "TestScratchHubStartFailureInnerHubScratch")
	checkFailureNamesServer(t, out, failed, "nats-server.log") // the file name the inner test gives h.logfile
}

func TestScratchHubStartFailureInnerHubScratch(t *testing.T) {
	if os.Getenv(scratchHubInnerEnv) == "" {
		return // only meaningful as the child of TestScratchHubStartFailureNamesTheServerLogAndExit
	}
	dir := t.TempDir()
	h := &hubScratch{t: t}
	h.home = dir
	h.logfile = filepath.Join(dir, "log", "nats-server.log")
	h.pidfile = filepath.Join(dir, "nats-server.pid")
	h.conf = filepath.Join(dir, "nats-server.conf")
	h.adminSeed = filepath.Join(dir, "admin.nk")
	h.adminPub = newSeedFile(t, h.adminSeed)
	h.port = freePort(t)
	if err := os.MkdirAll(filepath.Dir(h.logfile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.conf, []byte("# the fake server never reads this\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.startServer(writeFailingServer(t, dir), h.conf, nil)
	t.Fatal("startServer returned for a server that exited at once")
}

// --- the observer scratch hub (startObserverHub in observer_test.go) -----------------------------

func TestObserverHubStartFailureNamesTheServerLogAndExit(t *testing.T) {
	out, failed := runInner(t, "TestScratchHubStartFailureInnerObserver")
	checkFailureNamesServer(t, out, failed, ".log") // the observer hub chooses its own log file; any .log path will do
}

func TestScratchHubStartFailureInnerObserver(t *testing.T) {
	if os.Getenv(scratchHubInnerEnv) == "" {
		return // only meaningful as the child of TestObserverHubStartFailureNamesTheServerLogAndExit
	}
	fake := t.TempDir()
	writeFailingServer(t, fake)
	t.Setenv("PATH", fake+string(os.PathListSeparator)+os.Getenv("PATH"))
	startObserverHub(t)
	t.Fatal("startObserverHub returned for a server that exited at once")
}
