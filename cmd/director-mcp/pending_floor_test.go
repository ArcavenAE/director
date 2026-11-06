package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// I-100 P5 (aae-orc-mjckm): a seat that has consumed and acked all its mail
// reads 0 pending with an oldest pending age of 0 on its next sample.

// TestDrainedSeatPendingIsZero is the O-2128 shape: 18 messages delivered,
// every one consumed and acked, then a sample and a presence read.
func TestDrainedSeatPendingIsZero(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	bus := roleBus(t, ctx, url, "drain-seat", "")
	bus.pending.mark("local", pendingSample{at: time.Now().Add(-7800 * time.Second)})
	for i := 0; i < 18; i++ {
		pubEnv(t, ctx, js, "agent.aae-orc.ops.drain-seat.inbox", fmt.Sprintf("m-%02d", i), "INFORM", "hello")
	}
	if n, err := (busCueSource{bus}).count(ctx); err != nil || n != 18 {
		t.Fatalf("setup: count = %d, %v; want 18", n, err)
	}
	if got := bus.oldestPending(); got < 7800*time.Second {
		t.Fatalf("setup: the backlog is stamped at the old sample: want at least 7800s, got %s", got)
	}
	for i := 0; i < 18; i++ {
		if err := fetchOneLocal(t, bus).DoubleAck(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := (busCueSource{bus}).count(ctx); err != nil || n != 0 {
		t.Fatalf("after the drain: count = %d, %v; want 0", n, err)
	}
	if got := bus.oldestPending(); got != 0 {
		t.Fatalf("a drained seat reads an oldest pending age of %s, want 0", got)
	}
	if v := bus.presenceView(ctx); v.oldestS != 0 {
		t.Fatalf("the presence row carries oldest_pending_s = %d, want 0", v.oldestS)
	}
}

// TestFanoutConsumedUnackedLabelled: mail a seat has fetched and not acked is
// reported under its own ack_pending field on the roster row, and is not counted
// in the row's unread. Two of three messages are held ack-pending.
func TestFanoutConsumedUnackedLabelled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	bus := roleBus(t, ctx, url, "held-seat", "")
	for i := 0; i < 3; i++ {
		pubEnv(t, ctx, js, "agent.aae-orc.ops.held-seat.inbox", fmt.Sprintf("m-%d", i), "INFORM", "hello")
	}
	for i := 0; i < 2; i++ {
		fetchOneLocal(t, bus) // delivered, never acked
	}
	rows, warn, err := bus.rosterMerged(ctx)
	if err != nil || warn != "" {
		t.Fatalf("roster: %v %q", err, warn)
	}
	row := rosterRowFor(t, rows, "held-seat")
	if row["ack_pending"] != uint64(2) {
		t.Errorf("ack_pending = %v, want 2 under its own field", row["ack_pending"])
	}
	if row["unread"] != uint64(1) {
		t.Errorf("unread = %v, want 1: held mail is not unread", row["unread"])
	}
}

// TestFailedReadIsUnverifiedNotZero: a tier read that fails marks the tier
// unverified with its reason, and the age is kept, never reset to 0.
func TestFailedReadIsUnverifiedNotZero(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	bus := roleBus(t, ctx, url, "blind-seat", "")
	bus.pending.mark("local", pendingSample{at: time.Now().Add(-900 * time.Second)})
	pubEnv(t, ctx, js, "agent.aae-orc.ops.blind-seat.inbox", "m-1", "INFORM", "hello")
	if n, err := (busCueSource{bus}).count(ctx); err != nil || n != 1 {
		t.Fatalf("setup: count = %d, %v; want 1", n, err)
	}
	dead, stop := context.WithCancel(ctx)
	stop() // the read cannot complete
	if _, err := bus.pendingByTier(dead); err == nil {
		t.Fatal("the forced read did not fail")
	}
	if why := bus.pending.unverified(); !strings.Contains(why, "unverified on the local tier") {
		t.Fatalf("a failed read is not marked unverified: %q", why)
	}
	if got := bus.oldestPending(); got < 900*time.Second {
		t.Fatalf("a failed read reset the age to %s; it must keep the age and say unverified", got)
	}
}

// TestPresenceOnlyOpensBusConnection: reading counts and writing presence adds
// no connection to the server and no non-loopback client, so this work opens no
// network path of its own.
func TestPresenceOnlyOpensBusConnection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url, mon := startMonitoredServer(t)
	_, js := provision(t, ctx, url)
	bus := roleBus(t, ctx, url, "quiet-seat", "")
	before := connz(t, mon)
	if len(before) < 2 {
		t.Fatalf("setup: the server lists %d clients, want the test's and the bus's", len(before))
	}
	pubEnv(t, ctx, js, "agent.aae-orc.ops.quiet-seat.inbox", "m-1", "INFORM", "hello")
	if _, err := bus.pendingByTier(ctx); err != nil {
		t.Fatal(err)
	}
	if err := bus.writePresence(ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	after := connz(t, mon)
	if len(after) != len(before) {
		t.Fatalf("connections went from %d to %d across a count read and a presence write", len(before), len(after))
	}
	for _, ip := range after {
		if ip != "127.0.0.1" && ip != "::1" {
			t.Fatalf("a client connected from %q, want loopback only", ip)
		}
	}
}

// startMonitoredServer is startScratchServer with the monitoring port on, so a
// test can list the server's connections. It returns the bus URL and the
// monitoring base URL.
func startMonitoredServer(t *testing.T) (string, string) {
	t.Helper()
	bin, err := exec.LookPath("nats-server")
	if err != nil {
		t.Skip("nats-server not on PATH")
	}
	free := func() int {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		return l.Addr().(*net.TCPAddr).Port
	}
	port, mport := free(), free()
	dir := t.TempDir()
	conf := filepath.Join(dir, "s.conf")
	body := fmt.Sprintf("listen: 127.0.0.1:%d\nhttp: 127.0.0.1:%d\njetstream { store_dir: %q, domain: global }\n", port, mport, filepath.Join(dir, "js"))
	if err := os.WriteFile(conf, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-c", conf)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	url := fmt.Sprintf("nats://127.0.0.1:%d", port)
	for i := 0; i < 50; i++ {
		if nc, err := nats.Connect(url); err == nil {
			nc.Close()
			return url, fmt.Sprintf("http://127.0.0.1:%d", mport)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("scratch nats-server did not come up")
	return "", ""
}

// connz is the remote address of every client the server holds.
func connz(t *testing.T, mon string) []string {
	t.Helper()
	resp, err := http.Get(mon + "/connz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Conns []struct {
			IP string `json:"ip"`
		} `json:"connections"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	var ips []string
	for _, c := range out.Conns {
		ips = append(ips, c.IP)
	}
	return ips
}

// TestNoReplyAckClearsPending: in the handled mode, a message the seat read and
// marks no_reply (read, no reply owed) is recorded, acked and leaves the count the
// cue reads, so the cue has nothing left to name. director#448.
func TestNoReplyAckClearsPending(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, js, _, dir := handledBus(t, ctx, true, "")
	pubEnv(t, ctx, js, mineSubject, "nr1", "INFORM", "for another seat; take no action")
	res, err := bus.receiveTiered(ctx, 2*time.Second)
	if err != nil || res.Env == nil || res.Env.MessageID != "nr1" {
		t.Fatalf("receive = %+v, %v", res, err)
	}
	if n, err := (busCueSource{bus}).count(ctx); err != nil || n != 1 {
		t.Fatalf("setup: a read, unmarked message counts %d, %v; want 1", n, err)
	}
	out, err := bus.markHandled(ctx, "local", "nr1", "no_reply", "")
	if err != nil {
		t.Fatal(err)
	}
	if out["status"] != "recorded" || out["acked"] != true {
		t.Errorf("mark result = %v", out)
	}
	if n, err := (busCueSource{bus}).count(ctx); err != nil || n != 0 {
		t.Errorf("after no_reply the cue's count = %d, %v; want 0", n, err)
	}
	f, _ := newHandledLedger(dir).fold()
	if e, ok := f.ByID["nr1"]; !ok || e.Disposition != "no_reply" {
		t.Errorf("ledger entry = %+v, %v; want disposition no_reply", e, ok)
	}
	if _, err := parseMarkArgs([]byte(`{"message_id":"x","tier":"local","disposition":"no_reply"}`)); err != nil {
		t.Errorf("mark_handled refuses no_reply: %v", err)
	}
}
