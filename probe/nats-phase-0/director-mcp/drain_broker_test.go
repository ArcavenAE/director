package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// These tests run the batch drain and the unread summary against a real
// nats-server on a random loopback port with its own store dir. The server is
// started with JetStream domain "global", so one process serves both tiers:
// the local API on $JS.API and the hub API on $JS.global.API, the same split
// the shim sees through a leaf. They skip when nats-server is not on PATH.

func startScratchServer(t *testing.T) string {
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
	conf := filepath.Join(dir, "s.conf")
	body := fmt.Sprintf("listen: 127.0.0.1:%d\njetstream { store_dir: %q, domain: global }\n", port, filepath.Join(dir, "js"))
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
			return url
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("scratch nats-server did not come up")
	return ""
}

// provision creates what the broker setup and the hub provisioning create:
// the local inbox stream and presence bucket, and the hub's director stream
// and global presence bucket.
func provision(t *testing.T, ctx context.Context, url string) (*nats.Conn, jetstream.JetStream) {
	t.Helper()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, _ := jetstream.New(nc)
	if _, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: "AGENT_INBOX", Subjects: []string{"agent.*.*.*.inbox", "agent.*.*.role.*.inbox"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: "AGENT_STATE", TTL: 90 * time.Second}); err != nil {
		t.Fatal(err)
	}
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	if _, err := gjs.CreateStream(ctx, jetstream.StreamConfig{Name: globalDirectorStream, Subjects: []string{"global.director.>"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := gjs.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: globalPresenceBucket, TTL: 90 * time.Second}); err != nil {
		t.Fatal(err)
	}
	return nc, js
}

func pubEnv(t *testing.T, ctx context.Context, js jetstream.JetStream, subject, id, perf, text string) {
	t.Helper()
	e := testEnv(id, "sender-"+id, perf, text)
	e.Recipient.Address = "agent://ops/michael"
	b, _ := json.Marshal(e)
	if _, err := js.Publish(ctx, subject, b); err != nil {
		t.Fatal(err)
	}
}

func ids(items []drained) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Tier+":"+it.Env.MessageID)
	}
	return out
}

func TestBrokerSummaryThenBatchDrainBothTiers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, js := provision(t, ctx, url)
	gjs, _ := jetstream.NewWithDomain(nc, "global")

	self := Sender{AgentID: "michael", Workspace: "aae-orc", Team: "ops"}
	bus, err := connect(ctx, url, self, &globalConfig{Domain: "global", Cluster: "kinu", Role: roleDirector})
	if err != nil {
		t.Fatal(err)
	}
	defer bus.close()

	mine := "agent.aae-orc.ops.michael.inbox"
	pubEnv(t, ctx, js, mine, "l1", "INFORM", "roll call: here")
	pubEnv(t, ctx, js, mine, "l2", "REQUEST", "please review")
	if _, err := js.Publish(ctx, mine, []byte("not an envelope")); err != nil {
		t.Fatal(err)
	}
	pubEnv(t, ctx, js, mine, "l3", "INFORM", "new seat, holding for director instructions")
	pubEnv(t, ctx, js, "agent.aae-orc.ops.someone-else.inbox", "x1", "INFORM", "not mine")
	pubEnv(t, ctx, js, mine, "l4", "INFORM", "I hold custody of the findings")
	pubEnv(t, ctx, js, mine, "l5", "INFORM", "done")
	pubEnv(t, ctx, js, mine, "l6", "INFORM", "done too")
	pubEnv(t, ctx, gjs, "global.director.inbox", "g1", "QUERY", "which cluster?")
	pubEnv(t, ctx, gjs, "global.director.inbox", "g2", "INFORM", "report")

	// Summary: sees everything waiting, acks nothing, and is repeatable.
	for pass := 1; pass <= 2; pass++ {
		res, err := bus.summarizeInbox(ctx, summaryDefault)
		if err != nil {
			t.Fatal(err)
		}
		s := res.Summary
		if s.Total != 9 || s.Undecodable != 1 {
			t.Fatalf("pass %d: total %d undecodable %d, want 9 and 1 (%+v)", pass, s.Total, s.Undecodable, res)
		}
		if res.Expected["local"] != 7 || res.Expected["global"] != 2 {
			t.Errorf("pass %d: waiting = %v, want local 7 global 2", pass, res.Expected)
		}
		if len(s.Sequences["local"]) != 7 || len(s.Sequences["global"]) != 2 {
			t.Errorf("pass %d: sequences = %v", pass, s.Sequences)
		}
		for i := 1; i < len(s.Sequences["local"]); i++ {
			if s.Sequences["local"][i] <= s.Sequences["local"][i-1] {
				t.Errorf("pass %d: local sequences not ascending: %v", pass, s.Sequences["local"])
			}
		}
		// l2 REQUEST, l3 awaits, l4 custody, g1 QUERY
		if len(s.Flagged) != 4 {
			t.Errorf("pass %d: flagged %d, want 4: %+v", pass, len(s.Flagged), s.Flagged)
		}
		if res.GlobalWarn != "" {
			t.Errorf("pass %d: global warning %q", pass, res.GlobalWarn)
		}
	}
	info, err := bus.consumer.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.AckFloor.Stream != 0 || info.NumAckPending != 0 || info.NumPending != 7 {
		t.Fatalf("summary moved the durable: floor %d ack-pending %d pending %d", info.AckFloor.Stream, info.NumAckPending, info.NumPending)
	}

	// First batch of 4: the budget is shared between the tiers so a local
	// backlog cannot starve global. This call is local's turn: local takes
	// half (the oldest two, which end before the poison line), global takes
	// its two, each tier oldest first.
	b1, err := bus.receiveBatch(ctx, 2*time.Second, 4)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(b1.Items), []string{"local:l1", "local:l2", "global:g1", "global:g2"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("batch 1 = %v, want %v", got, want)
	}
	if b1.Discarded != 0 {
		t.Errorf("batch 1 discarded %d, want 0", b1.Discarded)
	}
	if rem := bus.remaining(ctx); rem["local"] != 5 || rem["global"] != 0 {
		t.Errorf("remaining after batch 1 = %v, want local 5 (the poison line included) global 0", rem)
	}

	// Second batch: the rest of local, oldest first, the poison skipped.
	b2, err := bus.receiveBatch(ctx, 2*time.Second, maxDrain)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(b2.Items), []string{"local:l3", "local:l4", "local:l5", "local:l6"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("batch 2 = %v, want %v", got, want)
	}
	if b2.Discarded != 1 {
		t.Errorf("batch 2 discarded %d, want 1", b2.Discarded)
	}

	// Drained: the summary is empty and a batch waits, then returns nothing.
	res, err := bus.summarizeInbox(ctx, summaryDefault)
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.Total != 0 {
		t.Errorf("after drain the summary total = %d, want 0", res.Summary.Total)
	}
	start := time.Now()
	empty, err := bus.receiveBatch(ctx, time.Second, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Items) != 0 || time.Since(start) < 900*time.Millisecond {
		t.Errorf("empty batch returned %d items after %v; want none after the full wait", len(empty.Items), time.Since(start))
	}
}

func TestBrokerBatchBlocksForFirstMessage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	self := Sender{AgentID: "michael", Workspace: "aae-orc", Team: "ops"}
	bus, err := connect(ctx, url, self, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bus.close()

	go func() {
		time.Sleep(500 * time.Millisecond)
		pubEnv(t, ctx, js, "agent.aae-orc.ops.michael.inbox", "late", "INFORM", "arrived while waiting")
	}()
	start := time.Now()
	res, err := bus.receiveBatch(ctx, 10*time.Second, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 1 || res.Items[0].Env.MessageID != "late" || res.Items[0].Seq == 0 {
		t.Fatalf("got %+v, want the one late message with its sequence", res.Items)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("waited %v; a message arriving mid-wait should return promptly", time.Since(start))
	}

	// The single-message form keeps its old shape.
	pubEnv(t, ctx, js, "agent.aae-orc.ops.michael.inbox", "one", "INFORM", "single")
	out, err := toolWait(ctx, bus, json.RawMessage(`{"timeout_seconds":2}`))
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if _, ok := m["message"]; !ok || m["tier"] != "local" {
		t.Errorf("single form result = %v, want message and tier", m)
	}
	if _, ok := m["messages"]; ok {
		t.Errorf("single form must not change shape: %v", m)
	}
}

// The review's reproduction (director#79, finding 2): with one message
// delivered and unacked and a later one acked, the waiting set is not a prefix
// of the stream above the ack floor. The summary must still reach the newest
// waiting message, and must say that some listed messages may already be
// consumed rather than presenting its list as exact.
func TestBrokerSummaryAfterOutOfOrderAck(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	self := Sender{AgentID: "michael", Workspace: "aae-orc", Team: "ops"}
	bus, err := connect(ctx, url, self, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bus.close()
	mine := "agent.aae-orc.ops.michael.inbox"
	for _, id := range []string{"l1", "l2", "l3", "l4"} {
		pubEnv(t, ctx, js, mine, id, "INFORM", "x")
	}
	// l1 delivered and left unacked; l2 delivered and acked.
	b1, err := bus.consumer.Fetch(1, jetstream.FetchMaxWait(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for range b1.Messages() {
	}
	b2, err := bus.consumer.Fetch(1, jetstream.FetchMaxWait(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for m := range b2.Messages() {
		if err := m.DoubleAck(ctx); err != nil {
			t.Fatal(err)
		}
	}

	res, err := bus.summarizeInbox(ctx, summaryDefault)
	if err != nil {
		t.Fatal(err)
	}
	seqs := res.Summary.Sequences["local"]
	if len(seqs) == 0 || seqs[len(seqs)-1] != 4 {
		t.Errorf("sequences = %v; the newest waiting message (4) must be listed", seqs)
	}
	if res.Expected["local"] != 3 {
		t.Errorf("waiting = %d, want 3 (1 ack-pending, 2 never delivered)", res.Expected["local"])
	}
	if res.MaybeConsumed["local"] != 1 {
		t.Errorf("maybe_consumed = %v, want local 1 (sequence 2 was acked above the floor)", res.MaybeConsumed)
	}
}
