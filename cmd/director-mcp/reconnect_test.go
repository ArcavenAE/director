package main

// The shim must keep reconnecting through a host bus outage of any length, and
// must say when its connection ends without being asked to (aae-orc I-870).
//
// With the nats.go defaults the client gives up after 60 reconnect attempts, about
// two minutes at the 2 s default wait, and then holds a closed connection that
// nothing reports. These tests run against a scratch nats-server the test starts
// and stops, on a random loopback port with its own directory, never the live bus.
// They shorten the reconnect wait through dialReconnectWait so an outage of five
// times the old attempt budget takes seconds.

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

type scratchNats struct {
	bin  string
	port int
	dir  string
	pass string // when set, the server requires user "u" with this password
	cmd  *exec.Cmd
}

func newScratchNats(t *testing.T, pass string) *scratchNats {
	t.Helper()
	bin, err := exec.LookPath("nats-server")
	if err != nil {
		t.Skip("nats-server not on PATH")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	s := &scratchNats{bin: bin, port: port, dir: t.TempDir(), pass: pass}
	t.Cleanup(func() { s.stop() })
	return s
}

func (s *scratchNats) url() string { return fmt.Sprintf("nats://127.0.0.1:%d", s.port) }

// start writes the config and starts the server, then waits until it accepts a
// connection that presents the right credential.
func (s *scratchNats) start(t *testing.T) {
	t.Helper()
	body := fmt.Sprintf("listen: 127.0.0.1:%d\n", s.port)
	if s.pass != "" {
		body += fmt.Sprintf("authorization { user: u, password: %q }\n", s.pass)
	}
	conf := filepath.Join(s.dir, "s.conf")
	if err := os.WriteFile(conf, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s.cmd = exec.Command(s.bin, "-c", conf)
	if err := s.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var opts []nats.Option
	if s.pass != "" {
		opts = append(opts, nats.UserInfo("u", s.pass))
	}
	for i := 0; i < 100; i++ {
		if nc, err := nats.Connect(s.url(), opts...); err == nil {
			nc.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("scratch nats-server did not come up")
}

func (s *scratchNats) stop() {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_, _ = s.cmd.Process.Wait()
		s.cmd = nil
	}
}

// shortReconnect sets a 10 ms reconnect wait with no jitter for the test, so the
// old budget of 60 attempts is under a second.
func shortReconnect(t *testing.T) {
	t.Helper()
	w, j, jt := dialReconnectWait, dialReconnectJitter, dialReconnectJitterTLS
	dialReconnectWait, dialReconnectJitter, dialReconnectJitterTLS = 10*time.Millisecond, 0, 0
	t.Cleanup(func() { dialReconnectWait, dialReconnectJitter, dialReconnectJitterTLS = w, j, jt })
}

type closedReports struct {
	mu   sync.Mutex
	got  []string
	errs []error
}

func (c *closedReports) note(name string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.got = append(c.got, name)
	c.errs = append(c.errs, err)
}

func (c *closedReports) snapshot() ([]string, []error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.got...), append([]error(nil), c.errs...)
}

// captureClosedReports replaces the report for the test and restores it after.
func captureClosedReports(t *testing.T) *closedReports {
	t.Helper()
	c := &closedReports{}
	old := closedReport
	closedReport = c.note
	t.Cleanup(func() { closedReport = old })
	return c
}

func TestShimReconnectsAfterAnOutageLongerThanTheOldAttemptBudget(t *testing.T) {
	shortReconnect(t)
	srv := newScratchNats(t, "")
	srv.start(t)
	nc, _, err := dialRing(srv.url(), Sender{AgentID: "reconnect-outage"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	srv.stop()
	// 60 attempts at 10 ms is under a second; stay down five times as long.
	time.Sleep(5 * time.Second)
	if nc.IsClosed() {
		t.Fatalf("the connection gave up during the outage (last error: %v)", nc.LastError())
	}
	srv.start(t)
	deadline := time.Now().Add(10 * time.Second)
	for nc.Status() != nats.CONNECTED && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if nc.Status() != nats.CONNECTED {
		t.Fatalf("the shim did not reconnect after the server came back: status %v, last error %v", nc.Status(), nc.LastError())
	}
}

func TestAConnectionThatEndsUnaskedIsReported(t *testing.T) {
	shortReconnect(t)
	reports := captureClosedReports(t)
	srv := newScratchNats(t, "one")
	srv.start(t)
	pass := filepath.Join(t.TempDir(), "pass")
	if err := os.WriteFile(pass, []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DIRECTOR_NATS_USER", "u")
	t.Setenv("DIRECTOR_NATS_PASS_FILE", pass)
	t.Setenv("DIRECTOR_NATS_CREDS", "")
	t.Setenv("DIRECTOR_NATS_PASS", "")
	nc, _, err := dialRing(srv.url(), Sender{AgentID: "reconnect-closed"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	// The broker comes back with a different password and the password file still
	// holds the old one, so every reconnect is refused and the client ends the
	// connection on its own.
	srv.stop()
	srv.pass = "two"
	srv.start(t)
	deadline := time.Now().Add(10 * time.Second)
	for !nc.IsClosed() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !nc.IsClosed() {
		t.Fatalf("the connection was expected to end on the refused credential; status %v", nc.Status())
	}
	time.Sleep(500 * time.Millisecond)
	names, errs := reports.snapshot()
	if len(names) != 1 {
		t.Fatalf("an unasked close must be reported once, got %d reports: %v", len(names), names)
	}
	if names[0] != "director-mcp/reconnect-closed" || errs[0] == nil {
		t.Fatalf("the report must name the connection and carry the error: %q, %v", names[0], errs[0])
	}
}

func TestAnOrderlyCloseIsNotReported(t *testing.T) {
	shortReconnect(t)
	reports := captureClosedReports(t)
	srv := newScratchNats(t, "")
	srv.start(t)
	nc, _, err := dialRing(srv.url(), Sender{AgentID: "reconnect-orderly"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	nc.Close()
	time.Sleep(500 * time.Millisecond)
	if names, _ := reports.snapshot(); len(names) != 0 {
		t.Fatalf("a close the caller asked for must not be reported: %v", names)
	}
}
