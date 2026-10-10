package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// LR-3 slice S: each seat's presence names the shim revision it runs, and
// `director-mcp unread` reads unread counts and ages from the durables' ack
// floors without creating anything.

func TestParseArgsRefusesUnknown(t *testing.T) {
	for _, c := range []struct {
		args []string
		mode string
		json bool
		bad  bool
	}{
		{nil, "serve", false, false},
		{[]string{"--preflight"}, "preflight", false, false},
		{[]string{"-preflight"}, "preflight", false, false},
		{[]string{"unread"}, "unread", false, false},
		{[]string{"unread", "--json"}, "unread", true, false},
		{[]string{"--bogus"}, "", false, true},
		{[]string{"unread", "--bogus"}, "", false, true},
		{[]string{"--preflight", "extra"}, "", false, true},
		{[]string{"serve"}, "", false, true},
	} {
		got, err := parseArgs(c.args)
		if c.bad {
			if err == nil {
				t.Errorf("%v: accepted as %+v, want a refusal", c.args, got)
			}
			continue
		}
		if err != nil || got.mode != c.mode || got.json != c.json {
			t.Errorf("%v: got %+v, %v; want mode %s json %v", c.args, got, err, c.mode, c.json)
		}
	}
}

func TestPresenceCarriesShimRevision(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	b, err := connect(ctx, url, Sender{AgentID: "a", Workspace: "w", Team: "t"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	if err := b.writePresence(ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	kv, _ := js.KeyValue(ctx, "AGENT_STATE")
	e, err := kv.Get(ctx, "presence.t.a."+b.instance)
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	_ = json.Unmarshal(e.Value(), &rec)
	rev, _ := rec["rev"].(string)
	if rev == "" || rev != shimRevision() {
		t.Fatalf("presence rev = %q, want %q", rev, shimRevision())
	}
}

// buildShim builds the shim binary into dir. vcs stamping is on unless
// noVCS, so its presence rev can be checked against `go version -m`.
func buildShim(t *testing.T, dir string, noVCS bool) string {
	t.Helper()
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not on PATH")
	}
	bin := filepath.Join(dir, "director-mcp")
	args := []string{"build", "-o", bin}
	if noVCS {
		args = append(args, "-buildvcs=false")
	} else {
		args = append(args, "-buildvcs=true")
	}
	cmd := exec.Command(gobin, append(args, ".")...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

// expectedRev reads the revision the toolchain stamped, independently of the
// shim's own reading.
func expectedRev(t *testing.T, bin string) string {
	t.Helper()
	out, err := exec.Command("go", "version", "-m", bin).Output()
	if err != nil {
		t.Fatal(err)
	}
	rev := regexp.MustCompile(`(?m)^\s*build\s+vcs\.revision=(\S+)`).FindSubmatch(out)
	if rev == nil {
		t.Fatalf("no vcs.revision in go version -m:\n%s", out)
	}
	r := string(rev[1])
	if regexp.MustCompile(`(?m)^\s*build\s+vcs\.modified=true`).Match(out) {
		r += "+dirty"
	}
	return r
}

// startShim runs the binary as a live seat with stdin held open and returns
// once its presence row exists.
func startShim(t *testing.T, ctx context.Context, bin, url string, self Sender, kv jetstream.KeyValue) map[string]any {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "NATS_URL="+url, "DIRECTOR_AGENT_ID="+self.AgentID, "DIRECTOR_TEAM="+self.Team, "DIRECTOR_WORKSPACE="+self.Workspace, "DIRECTOR_GLOBAL_DOMAIN=")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	prefix := "presence." + self.Team + "." + self.AgentID + "."
	for i := 0; i < 100; i++ {
		keys, _ := kv.Keys(ctx)
		for _, k := range keys {
			if strings.HasPrefix(k, prefix) {
				e, err := kv.Get(ctx, k)
				if err == nil {
					var rec map[string]any
					_ = json.Unmarshal(e.Value(), &rec)
					return rec
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("shim %s never wrote presence", self.AgentID)
	return nil
}

func runUnread(t *testing.T, bin, url string) unreadReport {
	t.Helper()
	cmd := exec.Command(bin, "unread", "--json")
	cmd.Env = append(os.Environ(), "NATS_URL="+url, "DIRECTOR_AGENT_ID=", "DIRECTOR_GLOBAL_DOMAIN=")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("unread exited %v: %s", err, out)
	}
	var r unreadReport
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("unread --json is not JSON: %v\n%s", err, out)
	}
	return r
}

func counts(t *testing.T, ctx context.Context, js jetstream.JetStream, kv jetstream.KeyValue) (int, int) {
	t.Helper()
	s, _ := js.Stream(ctx, "AGENT_INBOX")
	info, err := s.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	keys, _ := kv.Keys(ctx)
	return info.State.Consumers, len(keys)
}

func TestUnreadReportsPendingAgeAndRevision(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	kv, _ := js.KeyValue(ctx, "AGENT_STATE")
	bin := buildShim(t, t.TempDir(), false)
	want := expectedRev(t, bin)

	rec := startShim(t, ctx, bin, url, Sender{AgentID: "a", Workspace: "w", Team: "t"}, kv)
	if rec["rev"] != want {
		t.Fatalf("presence rev = %v, want %s", rec["rev"], want)
	}
	for i, text := range []string{"one", "two", "three"} {
		if i > 0 {
			time.Sleep(2 * time.Second)
		}
		pubEnv(t, ctx, js, "agent.w.t.a.inbox", "m"+text, "INFORM", text)
	}

	consumers, keys := counts(t, ctx, js, kv)
	r := runUnread(t, bin, url)
	if c2, k2 := counts(t, ctx, js, kv); c2 != consumers || k2 != keys {
		t.Fatalf("unread changed the broker: consumers %d->%d presence keys %d->%d", consumers, c2, keys, k2)
	}
	d := findDurable(t, r, "a")
	if d.Pending != 3 {
		t.Errorf("pending = %d, want 3", d.Pending)
	}
	if d.OldestAgeSeconds < 4 {
		t.Errorf("oldest unread age = %.1fs, want at least 4", d.OldestAgeSeconds)
	}
	if d.Presence == nil || d.Presence.Rev != want {
		t.Errorf("presence = %+v, want rev %s", d.Presence, want)
	}

	// Drain A's durable the way its seat would: fetch and ack all three.
	cons, err := js.Consumer(ctx, "AGENT_INBOX", d.Durable)
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := cons.Fetch(3, jetstream.FetchMaxWait(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for m := range msgs.Messages() {
		_ = m.Ack()
		n++
	}
	if n != 3 {
		t.Fatalf("drained %d, want 3", n)
	}
	time.Sleep(200 * time.Millisecond)
	d = findDurable(t, runUnread(t, bin, url), "a")
	if d.Pending != 0 || d.OldestSeq != 0 || d.OldestAgeSeconds != 0 {
		t.Errorf("after drain: %+v, want pending 0 and no age", d)
	}
}

func TestUnreadMarksDurableWithNoPresence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	provision(t, ctx, url)
	b, err := connect(ctx, url, Sender{AgentID: "gone", Workspace: "w", Team: "t"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	b.close() // a durable, and no presence row: mail for a seat that is gone
	bin := buildShim(t, t.TempDir(), false)
	d := findDurable(t, runUnread(t, bin, url), "gone")
	if d.Presence != nil || !strings.Contains(d.Note, "no presence") {
		t.Errorf("durable with no presence: %+v", d)
	}
}

func TestShimBuiltWithoutVCSReportsUnknown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	kv, _ := js.KeyValue(ctx, "AGENT_STATE")
	bin := buildShim(t, t.TempDir(), true)
	rec := startShim(t, ctx, bin, url, Sender{AgentID: "novcs", Workspace: "w", Team: "t"}, kv)
	if rec["rev"] != "unknown" {
		t.Fatalf("rev = %#v, want \"unknown\"", rec["rev"])
	}
}

func findDurable(t *testing.T, r unreadReport, agent string) unreadDurable {
	t.Helper()
	for _, d := range r.Durables {
		if d.AgentID == agent {
			return d
		}
	}
	t.Fatalf("no durable for %s in %+v", agent, r)
	return unreadDurable{}
}
