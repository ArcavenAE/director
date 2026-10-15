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
	"github.com/nats-io/nats.go/jetstream"
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

func TestViolationRingKeysBySubjectAndTime(t *testing.T) {
	r := &violationRing{}
	mk := func(subject string) error {
		return fmt.Errorf("%w: Permissions Violation for Publish to %q on connection [1]", nats.ErrPermissionViolation, subject)
	}
	before := time.Now().Add(-time.Second)
	r.note(mk("a.b"))
	r.note(mk("c.d"))
	r.note(fmt.Errorf("%w: Permissions Violation for Subscription to %q", nats.ErrPermissionViolation, "a.b"))
	r.note(errors.New("Permissions Violation for Publish to \"x.y\" but not the sentinel error"))
	r.note(nil)
	if text, ok := r.since("a.b", before); !ok || !strings.Contains(text, `Publish to "a.b"`) {
		t.Errorf("a.b: got %q, %v", text, ok)
	}
	if _, ok := r.since("x.y", before); ok {
		t.Error("an error that is not ErrPermissionViolation was recorded")
	}
	if _, ok := r.since("e.f", before); ok {
		t.Error("a subject with no violation read as violated")
	}
	if _, ok := r.since("a.b", time.Now().Add(time.Second)); ok {
		t.Error("a violation older than the publish explained it")
	}
	// A subscription violation for a subject is not a publish violation.
	r2 := &violationRing{}
	r2.note(fmt.Errorf("%w: Permissions Violation for Subscription to %q", nats.ErrPermissionViolation, "s.t"))
	if _, ok := r2.since("s.t", before); ok {
		t.Error("a subscription violation was read as a publish violation")
	}
	// The ring is bounded and keeps the newest.
	r3 := &violationRing{}
	for i := 0; i < violationRingSize+5; i++ {
		r3.note(mk(fmt.Sprintf("s.%d", i)))
	}
	if _, ok := r3.since("s.0", before); ok {
		t.Error("the oldest entry survived past the ring size")
	}
	if _, ok := r3.since(fmt.Sprintf("s.%d", violationRingSize+4), before); !ok {
		t.Error("the newest entry was dropped")
	}
	// A nil ring is a no-op and returns the error unchanged.
	var nilRing *violationRing
	nilRing.note(mk("n.l"))
	base := errors.New("boom")
	if got := nilRing.explain("n.l", before, base); got != base {
		t.Errorf("a nil ring changed the error: %v", got)
	}
}

func TestExplainKeepsTheOriginalErrorAndAddsTheReason(t *testing.T) {
	r := &violationRing{}
	start := time.Now()
	r.note(fmt.Errorf("%w: Permissions Violation for Publish to %q", nats.ErrPermissionViolation, "q.r"))
	got := r.explain("q.r", start.Add(-time.Second), context.DeadlineExceeded)
	if !errors.Is(got, context.DeadlineExceeded) {
		t.Errorf("the original error is not reachable: %v", got)
	}
	if !strings.Contains(got.Error(), `Permissions Violation for Publish to "q.r"`) {
		t.Errorf("the reason is missing: %v", got)
	}
	if got := r.explain("other", start.Add(-time.Second), context.DeadlineExceeded); got != context.DeadlineExceeded {
		t.Errorf("an unrelated subject was explained: %v", got)
	}
	if r.explain("q.r", start, nil) != nil {
		t.Error("a nil error became non-nil")
	}
}

// The global tier shares the connection, so a denied hub publish must name its
// violation too.
func TestDeniedGlobalPublishNamesTheViolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	subject := globalSubject("c1", roleDirector)
	admin, seat := startScratchServerPublishDeny(t, subject)
	nc, _ := provision(t, ctx, admin)
	agjs, err := jetstream.NewWithDomain(nc, "global")
	if err != nil {
		t.Fatal(err)
	}
	akv, err := agjs.KeyValue(ctx, globalPresenceBucket)
	if err != nil {
		t.Fatal(err)
	}
	putRow(t, ctx, akv, "presence.director.01AAAAAAAAAAAAAAAAAAAAAAAA", directorRow("director", "01AAAAAAAAAAAAAAAAAAAAAAAA"))

	ring := &violationRing{}
	snc, _, err := dialRing(seat, Sender{AgentID: "sup"}, ring)
	if err != nil {
		t.Fatal(err)
	}
	defer snc.Close()
	gjs, err := jetstream.NewWithDomain(snc, "global")
	if err != nil {
		t.Fatal(err)
	}
	kv, err := gjs.KeyValue(ctx, globalPresenceBucket)
	if err != nil {
		t.Fatal(err)
	}
	g := &globalTier{violations: ring, cfg: globalConfig{Domain: "global", Cluster: "c1", Role: roleSupervisor}, js: gjs, kv: kv}
	sctx, scancel := context.WithTimeout(ctx, 2*time.Second)
	defer scancel()
	err = sendTo(t, sctx, g, "global://director", "m-denied")
	if err == nil {
		t.Fatal("a hub publish the broker denies was accepted")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("the deadline error was lost: %v", err)
	}
	if !strings.Contains(err.Error(), `Permissions Violation for Publish to "`+subject+`"`) {
		t.Errorf("the error does not name the violation for %s: %v", subject, err)
	}
}
