package main

import (
	"context"
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

// aae-orc-e6otm: a cluster credential reaches the hub through a leaf whose
// allow-list answers named reads (STREAM.INFO, CONSUMER.INFO, ...) and leaves
// the list verbs without a responder (finding-006). `unread --global` opens with
// the stream-list request, so it failed with "hub in domain ... not reachable"
// while the hub was reachable. The error now says what is true.

// startScratchServerNoStreamList starts a one-account server with JetStream
// domain "global", the hub streams provisioned through it, and an account
// mapping that sends STREAM.NAMES to a subject nobody serves, so that request
// answers "no responders" as it does through the leaf while STREAM.INFO works.
func startScratchServerNoStreamList(t *testing.T) string {
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
	body := fmt.Sprintf(`listen: 127.0.0.1:%d
jetstream { store_dir: %q, domain: global }
accounts {
  FLEET {
    jetstream: enabled
    users: [{user: admin, password: admin}]
    mappings: { "$JS.global.API.STREAM.NAMES": "dead.stream.names" }
  }
}
`, port, filepath.Join(dir, "js"))
	conf := filepath.Join(dir, "s.conf")
	if err := os.WriteFile(conf, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-c", conf)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	url := fmt.Sprintf("nats://admin:admin@127.0.0.1:%d", port)
	for i := 0; i < 50; i++ {
		if nc, err := nats.Connect(url); err == nil {
			nc.Close()
			return url
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("scratch nats-server did not come up")
	return ""
}

func TestUnreadGlobalNamesTheListVerbWhenOnlyNamedReadsAnswer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	url := startScratchServerNoStreamList(t)
	provision(t, ctx, url)
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	_, err = readUnreadGlobal(ctx, nc, "global", time.Now())
	if err == nil {
		t.Fatal("the stream-list request has no responder here, yet the read succeeded")
	}
	msg := err.Error()
	if strings.Contains(msg, "not reachable") {
		t.Fatalf("the hub answered a named read, but the error says it is unreachable: %q", msg)
	}
	for _, want := range []string{`domain "global"`, "may not list streams", "leaf allow-list", "finding-006", "admin credential"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not say %q", msg, want)
		}
	}
}

// When nothing answers in the domain at all, the hub really is unreachable, and
// the original wording stays.
func TestUnreadGlobalStillSaysUnreachableWhenNothingAnswers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	url := startScratchServerNoStreamList(t)
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	_, err = readUnreadGlobal(ctx, nc, "nohub", time.Now())
	if err == nil || !strings.Contains(err.Error(), `hub in domain "nohub" not reachable`) {
		t.Fatalf("err = %v, want the unreachable wording for a domain no server answers", err)
	}
}

// Only a request nobody answers is read as the list-verb gap. A list request
// that is answered too slowly fails by deadline instead, and keeps the
// unreachable wording even though a named read still works.
func TestUnreadGlobalOtherListFailuresKeepTheUnreachableWording(t *testing.T) {
	url := startScratchServerNoStreamList(t)
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	// A listener on the subject the list request is mapped to, which never replies.
	sub, err := nc.Subscribe("dead.stream.names", func(*nats.Msg) {})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()
	provision(t, context.Background(), url)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err = readUnreadGlobal(ctx, nc, "global", time.Now())
	if err == nil || !strings.Contains(err.Error(), `hub in domain "global" not reachable`) || strings.Contains(err.Error(), "may not list streams") {
		t.Fatalf("err = %v, want the unreachable wording for a failure that is not no-responders", err)
	}
}
