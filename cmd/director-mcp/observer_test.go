package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"
)

// I-469 (sim/design/hub-observer.md): director reads every cluster's hub mail
// through one read-only observer user that connects straight to the hub. The
// shim learns it from DIRECTOR_HUB_OBSERVER_URL, _NKEY (a seed file path) and
// _CA, and uses it only for `unread --global` and the global durable check of
// list_roster. These tests stand up a hub whose observer user carries the
// permissions written in the hub conf template, so a grant that drifts from the
// template fails here, and a reader that creates a consumer is refused by the
// server and not by a mock.

const (
	observerEnvURL  = "DIRECTOR_HUB_OBSERVER_URL"
	observerEnvNKey = "DIRECTOR_HUB_OBSERVER_NKEY"
)

// observerTemplatePermissions returns the observer.director block of the hub conf
// template with its comment markers removed and the key filled in.
func observerTemplatePermissions(t *testing.T, pub string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "probe", "nats-global-tier", "hub", "nats-server.conf"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	in := false
	for _, line := range strings.Split(string(b), "\n") {
		trim := strings.TrimSpace(line)
		switch {
		case trim == "# BEGIN observer.director":
			in = true
		case trim == "# END observer.director":
			in = false
		case in:
			out = append(out, strings.TrimPrefix(strings.TrimPrefix(trim, "#"), " "))
		}
	}
	if len(out) == 0 {
		t.Fatal("the hub conf template has no observer.director block between its BEGIN and END lines")
	}
	return strings.ReplaceAll(strings.Join(out, "\n"), "OBSERVER_DIRECTOR_PUBLIC_KEY", pub)
}

type observerHub struct {
	adminURL    string
	leafURL     string
	hubURL      string // the client port, for the observer
	seedPath    string
	unknownSeed string // a seed the hub does not know
}

// startObserverHub starts a one-account hub with JetStream domain "global",
// an admin, a leaf user that can only make the reads a cluster leaf is granted
// for its own cluster (STREAM.NAMES has no responder, as through the leaf), and
// the observer.director user from the conf template.
func startObserverHub(t *testing.T) observerHub {
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

	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := kp.PublicKey()
	seed, _ := kp.Seed()
	seedPath := filepath.Join(dir, "observer-director.nk")
	if err := os.WriteFile(seedPath, append(seed, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	other, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	otherSeed, _ := other.Seed()
	unknownPath := filepath.Join(dir, "unknown.nk")
	if err := os.WriteFile(unknownPath, append(otherSeed, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	body := fmt.Sprintf(`listen: 127.0.0.1:%d
jetstream { store_dir: %q, domain: global }
accounts {
  FLEET {
    jetstream: enabled
    users: [
      {user: admin, password: admin},
      {user: leaf, password: leaf, permissions: {
        publish: {allow: [
          "$JS.global.API.STREAM.NAMES",
          "$JS.global.API.STREAM.INFO.GLOBAL_TO_mokuzai",
          "$JS.global.API.STREAM.INFO.KV_GLOBAL_PRESENCE",
          "$JS.global.API.CONSUMER.INFO.GLOBAL_TO_mokuzai.>",
          "$JS.global.API.DIRECT.GET.>",
          "$JS.global.API.STREAM.MSG.GET.>",
          "$JS.API.STREAM.INFO.GLOBAL_TO_mokuzai", "$JS.API.STREAM.INFO.KV_GLOBAL_PRESENCE",
          "$JS.API.CONSUMER.INFO.GLOBAL_TO_mokuzai.>", "$JS.API.DIRECT.GET.>",
          "$JS.API.STREAM.MSG.GET.>",
          "dead.stream.names"]},
        subscribe: {allow: ["_INBOX.>"]}}},
%s
    ]
    mappings: { "$JS.global.API.STREAM.NAMES": "dead.stream.names" }
  }
}
`, port, filepath.Join(dir, "js"), observerTemplatePermissions(t, pub))
	conf := filepath.Join(dir, "s.conf")
	if err := os.WriteFile(conf, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-c", conf)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	h := observerHub{
		adminURL:    fmt.Sprintf("nats://admin:admin@127.0.0.1:%d", port),
		leafURL:     fmt.Sprintf("nats://leaf:leaf@127.0.0.1:%d", port),
		hubURL:      fmt.Sprintf("nats://127.0.0.1:%d", port),
		seedPath:    seedPath,
		unknownSeed: unknownPath,
	}
	for i := 0; i < 50; i++ {
		if nc, err := nats.Connect(h.adminURL); err == nil {
			nc.Close()
			return h
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("scratch hub did not come up")
	return h
}

// durableSnapshot is what a seat's own consumer state says, read with the admin
// credential: everything a read that took or acked mail would have moved.
type durableSnapshot struct {
	Pending    uint64
	AckPending int
	AckFloor   uint64
	Delivered  uint64
}

// hubState reads, as admin, every stream's consumer count and every durable's
// snapshot.
func hubState(t *testing.T, ctx context.Context, adminURL string) (map[string]int, map[string]durableSnapshot) {
	t.Helper()
	nc, err := nats.Connect(adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	gjs, err := jetstream.NewWithDomain(nc, "global")
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	snaps := map[string]durableSnapshot{}
	names := gjs.StreamNames(ctx)
	for n := range names.Name() {
		s, err := gjs.Stream(ctx, n)
		if err != nil {
			t.Fatal(err)
		}
		info, err := s.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		counts[n] = info.State.Consumers
		cl := s.ListConsumers(ctx)
		for ci := range cl.Info() {
			snaps[n+"/"+ci.Name] = durableSnapshot{ci.NumPending, ci.NumAckPending, ci.AckFloor.Stream, ci.Delivered.Stream}
		}
	}
	return counts, snaps
}

func setObserverEnv(t *testing.T, h observerHub) {
	t.Helper()
	t.Setenv("DIRECTOR_CLUSTER", "mokuzai")
	t.Setenv(observerEnvURL, h.hubURL)
	t.Setenv(observerEnvNKey, h.seedPath)
	t.Setenv("DIRECTOR_HUB_OBSERVER_CA", "")
}

func runGlobalUnreadJSON(t *testing.T, ctx context.Context, leafURL string) globalUnread {
	t.Helper()
	var out, errw strings.Builder
	runUnreadCmd(ctx, leafURL, cliArgs{global: true, json: true}, "global", &out, &errw)
	var g globalUnread
	if err := json.Unmarshal([]byte(out.String()), &g); err != nil {
		t.Fatalf("unread --global --json printed %q (stderr %q): %v", out.String(), errw.String(), err)
	}
	return g
}

// With the observer, one read lists every cluster's hub stream and every seat's
// unread mail, and the hub's consumer count on each stream is the same after.
func TestObserverReadsEveryClusterStreamWithoutAConsumer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	h := startObserverHub(t)
	seedPartialHub(t, ctx, h.adminURL)
	setObserverEnv(t, h)
	before, _ := hubState(t, ctx, h.adminURL)

	g := runGlobalUnreadJSON(t, ctx, h.leafURL)
	if g.Error != "" {
		t.Fatalf("unread --global with the observer set reported an error: %s", g.Error)
	}
	byStream := map[string]unreadReport{}
	for _, r := range g.Reports {
		byStream[r.Stream] = r
		if r.Partial {
			t.Errorf("%s was read partially; the observer reads in full", r.Stream)
		}
	}
	for stream, want := range map[string]struct {
		agent   string
		pending uint64
	}{
		"GLOBAL_TO_mokuzai": {"mokuzai-supervisor", 3},
		"GLOBAL_TO_other":   {"other-supervisor", 5},
	} {
		r, ok := byStream[stream]
		if !ok {
			t.Errorf("no report for %s; the observer should read every cluster's stream (reports: %v)", stream, reportNames(g.Reports))
			continue
		}
		found := false
		for _, d := range r.Durables {
			if d.AgentID == want.agent && d.Pending == want.pending {
				found = true
			}
		}
		if !found {
			t.Errorf("%s does not show %s with %d unread: %+v", stream, want.agent, want.pending, r.Durables)
		}
	}

	after, _ := hubState(t, ctx, h.adminURL)
	for stream, n := range before {
		if after[stream] != n {
			t.Errorf("%s consumer count went from %d to %d during the observer read", stream, n, after[stream])
		}
	}
}

func reportNames(rs []unreadReport) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Stream)
	}
	return out
}

// The read does not move any seat's durable: unread mail stays unread, nothing
// is delivered or acked on a seat's behalf.
func TestObserverReadLeavesEverySeatDurableFloorUnchanged(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	h := startObserverHub(t)
	seedPartialHub(t, ctx, h.adminURL)
	setObserverEnv(t, h)
	_, before := hubState(t, ctx, h.adminURL)

	g := runGlobalUnreadJSON(t, ctx, h.leafURL)
	// The control: the read really saw another cluster's mail, so an unchanged
	// floor below means a read that left it alone and not a read that did nothing.
	sawOther := false
	for _, r := range g.Reports {
		if r.Stream == "GLOBAL_TO_other" && len(r.Durables) == 1 && r.Durables[0].Pending == 5 {
			sawOther = true
		}
	}
	if !sawOther {
		t.Fatalf("the read did not see GLOBAL_TO_other's 5 unread messages (reports: %v, error %q)", reportNames(g.Reports), g.Error)
	}

	rows := []map[string]any{{"agent_id": "other-supervisor", "instance": partialInstB, "role": roleSupervisor, "cluster": "other"}}
	lnc := dialLeaf(t, h.leafURL)
	ljs, err := jetstream.NewWithDomain(lnc, "global")
	if err != nil {
		t.Fatal(err)
	}
	(&globalTier{cfg: globalConfig{Domain: "global", Cluster: "mokuzai", Role: roleDirector}, js: ljs}).flagGlobalRows(ctx, rows)

	_, after := hubState(t, ctx, h.adminURL)
	if len(after) != len(before) {
		t.Errorf("the durables on the hub went from %d to %d", len(before), len(after))
	}
	for k, b := range before {
		if a := after[k]; a != b {
			t.Errorf("durable %s changed during the read: before %+v, after %+v", k, b, a)
		}
	}
}

// list_roster's global durable check goes through the observer, so a seat on
// another cluster reads present with its unread count and not durable-unchecked.
func TestObserverRosterChecksAnotherClustersDurable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	h := startObserverHub(t)
	seedPartialHub(t, ctx, h.adminURL)
	setObserverEnv(t, h)

	lnc := dialLeaf(t, h.leafURL)
	ljs, err := jetstream.NewWithDomain(lnc, "global")
	if err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{
		{"agent_id": "mokuzai-supervisor", "instance": partialInstA, "role": roleSupervisor, "cluster": "mokuzai"},
		{"agent_id": "other-supervisor", "instance": partialInstB, "role": roleSupervisor, "cluster": "other"},
	}
	(&globalTier{cfg: globalConfig{Domain: "global", Cluster: "mokuzai", Role: roleDirector}, js: ljs}).flagGlobalRows(ctx, rows)
	for i, want := range []uint64{3, 5} {
		r := rows[i]
		if r["durable"] != "present" || r["unread"] != want {
			t.Errorf("row %d (%v): durable %v unread %v (note %v), want present with %d unread", i, r["agent_id"], r["durable"], r["unread"], r["durable_note"], want)
		}
	}
}

// A grant that is set but cannot be used falls back to the leaf path and says
// so, never printing an empty count in its place.
func TestObserverConnectFailureFallsBackAndNamesTheGap(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	h := startObserverHub(t)
	seedPartialHub(t, ctx, h.adminURL)

	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadURL := "nats://" + dead.Addr().String()
	dead.Close()

	cases := []struct {
		name string
		url  string
		seed string
	}{
		{"nothing listens at the observer url", deadURL, h.seedPath},
		{"the hub refuses the observer key", h.hubURL, h.unknownSeed},
		{"the seed file does not exist", h.hubURL, filepath.Join(t.TempDir(), "missing.nk")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("DIRECTOR_CLUSTER", "mokuzai")
			t.Setenv(observerEnvURL, c.url)
			t.Setenv(observerEnvNKey, c.seed)
			var out, errw strings.Builder
			runUnreadCmd(ctx, h.leafURL, cliArgs{global: true}, "global", &out, &errw)
			text := out.String()
			if !strings.Contains(text, "GAP observer") {
				t.Errorf("the output does not name the observer failure as a GAP: %q", text)
			}
			if !strings.Contains(text, "mokuzai-supervisor") || !strings.Contains(text, "PARTIAL") {
				t.Errorf("the output did not fall back to the leaf path's partial read of the own cluster: %q", text)
			}
			if strings.Contains(text, "other-supervisor") {
				t.Errorf("another cluster's seat appears, but the leaf path cannot read it: %q", text)
			}
		})
	}
}

// The observer connection is opened for the global unread and nothing else: a
// local unread with the variables set opens none.
func TestObserverConnectionOpensOnlyForTheGlobalRead(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	h := startObserverHub(t)
	seedPartialHub(t, ctx, h.adminURL)

	counter, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer counter.Close()
	var accepts atomic.Int64
	go func() {
		for {
			c, err := counter.Accept()
			if err != nil {
				return
			}
			accepts.Add(1)
			c.Close()
		}
	}()
	t.Setenv("DIRECTOR_CLUSTER", "mokuzai")
	t.Setenv(observerEnvURL, "nats://"+counter.Addr().String())
	t.Setenv(observerEnvNKey, h.seedPath)

	var out, errw strings.Builder
	runUnreadCmd(ctx, h.adminURL, cliArgs{}, "global", &out, &errw)
	if n := accepts.Load(); n != 0 {
		t.Errorf("a local unread opened %d observer connections, want none", n)
	}
	runUnreadCmd(ctx, h.leafURL, cliArgs{global: true}, "global", &out, &errw)
	if n := accepts.Load(); n == 0 {
		t.Errorf("unread --global with the observer set never tried the observer url")
	}
}
