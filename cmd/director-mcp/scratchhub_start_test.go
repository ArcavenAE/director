package main

// The wait both scratch hubs share (ArcavenAE/director#681).
//
// A scratch hub starts a nats-server child and waits until a connect succeeds. The wait watches
// the child as well, so a server that exited fails the start at once, and when a start fails the
// message carries what the server said: its exit state, the last connect error and the tail of
// its log file. A slow start on a shared runner gets a deadline long enough to ride out, and a
// start that never completes says so with the same evidence.

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	// scratchHubStartDeadline bounds a start. The first version waited 60 tries of 100 ms (6 s),
	// which two CI runs on a shared runner outlasted. A healthy start returns on the first
	// successful connect, so only a failing start pays the full deadline.
	scratchHubStartDeadline = 30 * time.Second
	scratchHubPollEvery     = 100 * time.Millisecond
	// the log tail printed on a failed start: at most this many lines and this many bytes
	scratchHubLogLines = 40
	scratchHubLogBytes = 4096
)

// scratchServer is a started nats-server child that is watched for its exit.
type scratchServer struct {
	cmd     *exec.Cmd
	done    chan struct{} // closed once the child has been waited for
	waitErr error         // the result of cmd.Wait, valid after done is closed
}

// watchScratchServer starts watching a child that has been started. The test's own cleanup still
// kills the child and waits for it; the second Wait it makes returns an error that it ignores.
func watchScratchServer(cmd *exec.Cmd) *scratchServer {
	s := &scratchServer{cmd: cmd, done: make(chan struct{})}
	go func() {
		s.waitErr = cmd.Wait()
		close(s.done)
	}()
	return s
}

// await polls probe until it succeeds, the server exits, or the deadline passes. On a failure it
// stops the test with the server's exit state, the last probe error and the tail of its log.
func (s *scratchServer) await(t *testing.T, logPath string, probe func() error, deadline time.Duration) {
	t.Helper()
	stop := time.Now().Add(deadline)
	var last error
	for {
		if last = probe(); last == nil {
			return
		}
		select {
		case <-s.done:
			t.Fatalf("%s", s.report("the scratch hub did not come up: the server exited", logPath, last))
		default:
		}
		if time.Now().After(stop) {
			t.Fatalf("%s", s.report(fmt.Sprintf("the scratch hub did not come up: no connection in %s", deadline), logPath, last))
		}
		time.Sleep(scratchHubPollEvery)
	}
}

// report builds the failure text.
func (s *scratchServer) report(headline, logPath string, lastErr error) string {
	var b strings.Builder
	b.WriteString(headline)
	b.WriteString("\n  server: ")
	switch {
	case s.cmd.Process == nil:
		b.WriteString("not started")
	default:
		select {
		case <-s.done:
			if s.waitErr != nil {
				fmt.Fprintf(&b, "pid %d exited: %v", s.cmd.Process.Pid, s.waitErr)
			} else {
				fmt.Fprintf(&b, "pid %d exited: exit status 0", s.cmd.Process.Pid)
			}
		default:
			fmt.Fprintf(&b, "pid %d still running", s.cmd.Process.Pid)
		}
	}
	fmt.Fprintf(&b, "\n  last connect error: %v", lastErr)
	fmt.Fprintf(&b, "\n  server log %s (last %d lines):\n%s", logPath, scratchHubLogLines, logTail(logPath))
	return b.String()
}

// logTail returns the end of a log file, indented, or says why there is none.
func logTail(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "    (no log: " + err.Error() + ")"
	}
	if len(data) > scratchHubLogBytes {
		data = data[len(data)-scratchHubLogBytes:]
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > scratchHubLogLines {
		lines = lines[len(lines)-scratchHubLogLines:]
	}
	if len(lines) == 1 && lines[0] == "" {
		return "    (the log is empty)"
	}
	return "    " + strings.Join(lines, "\n    ")
}
