package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// A publish the broker denies gets no ack, so the shim saw only "context
// deadline exceeded" and the audit line read "Director-Refusal: publish to
// <subject>: context deadline exceeded". The broker does say why: a
// "Permissions Violation for Publish to <subject>" error, but it arrives on
// the connection's async error callback, not as the publish's return value.
// These tests hold the shim to naming that violation in the error it returns.

// startScratchServerPublishDeny starts a scratch server like startScratchServer
// with an admin user that provisions and a seat user that may publish anywhere
// except the deny subjects.
func startScratchServerPublishDeny(t *testing.T, deny ...string) (adminURL, seatURL string) {
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
	dir := t.TempDir()
	var quoted []string
	for _, d := range deny {
		quoted = append(quoted, fmt.Sprintf("%q", d))
	}
	body := fmt.Sprintf(`listen: 127.0.0.1:%d
jetstream { store_dir: %q, domain: global }
authorization {
  users = [
    {user: admin, password: admin}
    {user: seat, password: seat, permissions: {publish: {allow: [">"], deny: [%s]}, subscribe: {allow: [">"]}}}
  ]
}
`, port, filepath.Join(dir, "js"), strings.Join(quoted, ", "))
	conf := filepath.Join(dir, "s.conf")
	if err := os.WriteFile(conf, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-c", conf)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	adminURL = fmt.Sprintf("nats://admin:admin@127.0.0.1:%d", port)
	seatURL = fmt.Sprintf("nats://seat:seat@127.0.0.1:%d", port)
	for i := 0; i < 50; i++ {
		if nc, err := nats.Connect(adminURL); err == nil {
			nc.Close()
			return adminURL, seatURL
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("scratch nats-server did not come up")
	return "", ""
}

func TestDeniedPublishNamesTheViolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	const denied = "agent.w.t.blocked.inbox"
	admin, seat := startScratchServerPublishDeny(t, denied)
	provision(t, ctx, admin)
	liveSeatNoCleanup(t, ctx, admin, Sender{AgentID: "blocked", Workspace: "w", Team: "t"})
	liveSeatNoCleanup(t, ctx, admin, Sender{AgentID: "open", Workspace: "w", Team: "t"})

	bus, err := connect(ctx, seat, Sender{AgentID: "sup", Workspace: "w", Team: "t"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bus.close()
	send := func(to string) (any, error) {
		sctx, scancel := context.WithTimeout(ctx, 2*time.Second)
		defer scancel()
		raw, _ := json.Marshal(map[string]any{"to": to, "performative": "INFORM", "text": "x"})
		return toolSend(sctx, bus, raw)
	}

	// Positive control: a subject the seat may publish to is accepted.
	if out, err := send("agent://t/open"); err != nil {
		t.Fatalf("a send to an allowed seat failed: %v (%v)", err, out)
	}

	// The denied publish still fails, still reads as a timeout, and now names
	// the broker's reason and the subject.
	_, err = send("agent://t/blocked")
	if err == nil {
		t.Fatal("a send to a subject the broker denies was accepted")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("the deadline error was lost: %v", err)
	}
	if !strings.Contains(err.Error(), `Permissions Violation for Publish to "`+denied+`"`) {
		t.Errorf("the error does not name the broker's violation for %s: %v", denied, err)
	}
}
