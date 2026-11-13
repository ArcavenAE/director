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
)

// aae-orc-e6otm part B: a cluster credential reaches the hub through a leaf that
// answers named reads and leaves the list verbs without a responder. unread
// --global then reads what it can by name: STREAM.INFO and CONSUMER.INFO, plus
// an exact-key get of this cluster's presence records, for GLOBAL_TO_<own
// cluster> only, and says it is partial.

// startLeafLikeServer starts a one-account server with JetStream domain
// "global" and two users. admin may do anything and seeds the hub. leaf may
// publish only the requests a cluster credential gets through the leaf, with
// STREAM.NAMES mapped to a subject nobody serves (so it answers "no
// responders", as the leaf does). Any other JetStream request from leaf is a
// permission violation, so a read that strays past the allowed verbs fails.
func startLeafLikeServer(t *testing.T, refuseGet bool) (adminURL, leafURL string) {
	t.Helper()
	return startLeafLikeServerMapping(t, refuseGet, "")
}

// startLeafLikeServerMapping is startLeafLikeServer with one more account
// mapping, written as `"from": "to"`, so a test can send one request to a
// subject it listens on or that nobody serves.
func startLeafLikeServerMapping(t *testing.T, refuseGet bool, extraMap string) (adminURL, leafURL string) {
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
	mapGet := ""
	if refuseGet {
		mapGet = `, "$JS.global.API.DIRECT.GET.>": "dead.direct.get.>", "$JS.global.API.STREAM.MSG.GET.>": "dead.msg.get.>"`
	}
	if extraMap != "" {
		mapGet += ", " + extraMap
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
          "$JS.global.API.STREAM.INFO.>",
          "$JS.global.API.CONSUMER.INFO.>",
          "$JS.global.API.DIRECT.GET.>",
          "$JS.global.API.STREAM.MSG.GET.>",
          "$JS.API.STREAM.INFO.>", "$JS.API.CONSUMER.INFO.>", "$JS.API.DIRECT.GET.>",
          "dead.stream.names", "dead.direct.get.>", "dead.msg.get.>", "dead.probe.>"]},
        subscribe: {allow: ["_INBOX.>"]}}}
    ]
    mappings: { "$JS.global.API.STREAM.NAMES": "dead.stream.names"%s }
  }
}
`, port, filepath.Join(dir, "js"), mapGet)
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
	leafURL = fmt.Sprintf("nats://leaf:leaf@127.0.0.1:%d", port)
	for i := 0; i < 50; i++ {
		if nc, err := nats.Connect(adminURL); err == nil {
			nc.Close()
			return adminURL, leafURL
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("scratch nats-server did not come up")
	return "", ""
}

const (
	partialInstA = "01M6AAAAAAAAAAAAAAAAAAAAAA"
	partialInstB = "01M6BBBBBBBBBBBBBBBBBBBBBB"
)

// seedPartialHub provisions two cluster streams (mokuzai, other), a durable per
// seat, mail for the mokuzai seats, and a presence record per seat.
func seedPartialHub(t *testing.T, ctx context.Context, adminURL string) {
	t.Helper()
	nc, _ := provision(t, ctx, adminURL)
	gjs, err := jetstream.NewWithDomain(nc, "global")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"mokuzai", "other"} {
		if _, err := gjs.CreateStream(ctx, jetstream.StreamConfig{Name: globalStreamPrefix + c, Subjects: []string{"global." + c + ".>"}}); err != nil {
			t.Fatal(err)
		}
	}
	kv, err := gjs.KeyValue(ctx, globalPresenceBucket)
	if err != nil {
		t.Fatal(err)
	}
	seat := func(cluster, agent, inst string, mail int) {
		s, _ := gjs.Stream(ctx, globalStreamPrefix+cluster)
		if _, err := s.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
			Durable: globalDurable(agent, inst), FilterSubject: "global." + cluster + ".supervisor.inbox", AckPolicy: jetstream.AckExplicitPolicy,
		}); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < mail; i++ {
			if _, err := gjs.Publish(ctx, "global."+cluster+".supervisor.inbox", []byte("m")); err != nil {
				t.Fatal(err)
			}
		}
		rec, _ := json.Marshal(map[string]string{"agent_id": agent, "instance": inst, "state": "online", "ts": time.Now().UTC().Format(time.RFC3339)})
		if _, err := kv.Put(ctx, "presence."+cluster+".supervisor."+inst, rec); err != nil {
			t.Fatal(err)
		}
	}
	seat("mokuzai", "mokuzai-supervisor", partialInstA, 3)
	seat("other", "other-supervisor", partialInstB, 5)
}

func dialLeaf(t *testing.T, leafURL string) *nats.Conn {
	t.Helper()
	nc, err := nats.Connect(leafURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	return nc
}

func TestUnreadGlobalPartialReadsOwnClusterByName(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminURL, leafURL := startLeafLikeServer(t, false)
	seedPartialHub(t, ctx, adminURL)
	t.Setenv("DIRECTOR_CLUSTER", "mokuzai")

	reports, err := readUnreadGlobal(ctx, dialLeaf(t, leafURL), "global", time.Now())
	if err != nil {
		t.Fatalf("a cluster credential that can read by name got an error: %v", err)
	}
	// I-467 rec 2: the own cluster's partial report, then one self-reported
	// fallback report per other cluster that has a presence row.
	if len(reports) != 2 || reports[0].Stream != "GLOBAL_TO_mokuzai" || !reports[0].Partial {
		t.Fatalf("reports = %+v, want the partial report of GLOBAL_TO_mokuzai and one fallback for GLOBAL_TO_other", reports)
	}
	// the second report is the other cluster's self-reported fallback, not just any second report
	if reports[1].Stream != "GLOBAL_TO_other" || !reports[1].SelfReported {
		t.Fatalf("second report = %s (self_reported %v), want GLOBAL_TO_other self-reported", reports[1].Stream, reports[1].SelfReported)
	}
	r := reports[0]
	if len(r.Durables) != 1 || r.Durables[0].AgentID != "mokuzai-supervisor" || r.Durables[0].Pending != 3 || !r.Durables[0].Live {
		t.Fatalf("durables = %+v, want the mokuzai supervisor, live, pending 3", r.Durables)
	}
	text := strings.Join(r.Warnings, "\n")
	for _, want := range []string{"partial", "Other clusters", "not listed", "any total are not read"} {
		if !strings.Contains(text, want) {
			t.Errorf("warnings %q do not say %q", text, want)
		}
	}
	// A partial report never gives a total.
	var out strings.Builder
	printUnread(&out, r, unreadOpts{})
	if !strings.Contains(out.String(), "PARTIAL") {
		t.Errorf("printed report does not say PARTIAL: %q", out.String())
	}
	if strings.Contains(out.String(), "other-supervisor") {
		t.Errorf("another cluster's seat is in the report: %q", out.String())
	}
}

// A seat the read could not see is named with the leaf's error, never counted as zero.
func TestUnreadGlobalPartialNamesWhatItCouldNotSee(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminURL, leafURL := startLeafLikeServer(t, true)
	seedPartialHub(t, ctx, adminURL)
	t.Setenv("DIRECTOR_CLUSTER", "mokuzai")

	reports, err := readUnreadGlobal(ctx, dialLeaf(t, leafURL), "global", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 || !reports[0].Partial {
		t.Fatalf("reports = %+v", reports)
	}
	r := reports[0]
	if len(r.Durables) != 0 {
		t.Fatalf("durables = %+v, want none: the presence get was refused", r.Durables)
	}
	text := strings.Join(r.Warnings, "\n")
	if !strings.Contains(text, "unseen") || !strings.Contains(text, "presence.mokuzai.supervisor."+partialInstA) || !strings.Contains(text, "no responders") {
		t.Errorf("warnings %q do not name the unseen seat with the leaf's error", text)
	}
}

// With no cluster named, there is nothing to read by name: the existing error stays.
func TestUnreadGlobalPartialNeedsAnOwnCluster(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminURL, leafURL := startLeafLikeServer(t, false)
	seedPartialHub(t, ctx, adminURL)
	t.Setenv("DIRECTOR_CLUSTER", "")

	_, err := readUnreadGlobal(ctx, dialLeaf(t, leafURL), "global", time.Now())
	if err == nil || !strings.Contains(err.Error(), "may not list streams") {
		t.Fatalf("err = %v, want the list-verb wording", err)
	}
}

// When the presence instances cannot be named, the read prints a GAP and reads
// the durables the operator names with --durable.
func TestUnreadGlobalPartialFallsBackToDurableNames(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminURL, leafURL := startLeafLikeServer(t, false)
	nc, err := nats.Connect(adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	s, err := gjs.CreateStream(ctx, jetstream.StreamConfig{Name: "GLOBAL_TO_mokuzai", Subjects: []string{"global.mokuzai.>"}})
	if err != nil {
		t.Fatal(err)
	}
	durable := globalDurable("mokuzai-supervisor", partialInstA)
	if _, err := s.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{Durable: durable, FilterSubject: "global.mokuzai.supervisor.inbox", AckPolicy: jetstream.AckExplicitPolicy}); err != nil {
		t.Fatal(err)
	}
	// No presence bucket exists here, so the instances cannot be named.
	lgjs, _ := jetstream.NewWithDomain(dialLeaf(t, leafURL), "global")
	reports, err := readUnreadGlobalPartial(ctx, lgjs, "mokuzai", []string{durable, "not-a-hub-durable"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	r := reports[0]
	text := strings.Join(r.Warnings, "\n")
	if len(r.Durables) != 1 || r.Durables[0].Durable != durable {
		t.Fatalf("durables = %+v, want the one named by --durable", r.Durables)
	}
	for _, want := range []string{"GAP presence instances not listed", "pass --durable", "not-a-hub-durable: not a hub durable name"} {
		if !strings.Contains(text, want) {
			t.Errorf("warnings %q do not say %q", text, want)
		}
	}
}

func TestParseArgsUnreadDurable(t *testing.T) {
	got, err := parseArgs([]string{"unread", "--global", "--durable", "mcp_global_a_1", "--durable", "mcp_global_b_2"})
	if err != nil || !got.global || len(got.durables) != 2 || got.durables[1] != "mcp_global_b_2" {
		t.Fatalf("parseArgs = %+v, %v", got, err)
	}
	if _, err := parseArgs([]string{"unread", "--durable"}); err == nil {
		t.Error("--durable with no name was accepted")
	}
	// --durable names hub durables: without --global it would be ignored.
	if _, err := parseArgs([]string{"unread", "--durable", "mcp_global_a_1"}); err == nil || !strings.Contains(err.Error(), "needs --global") {
		t.Errorf("--durable without --global: err = %v, want a refusal naming --global", err)
	}
	// Order does not matter: --global after --durable is accepted.
	if got, err := parseArgs([]string{"unread", "--durable", "mcp_global_a_1", "--global"}); err != nil || !got.global {
		t.Errorf("--global after --durable: %+v, %v", got, err)
	}
}

// A presence key that the stream info names but whose get says not found (its
// latest entry is a delete marker) is printed as unseen, never skipped without
// a word (director ruling on review 5807160110, low 1).
func TestUnreadGlobalPartialNamesAKeyThatIsNotFound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminURL, leafURL := startLeafLikeServer(t, false)
	seedPartialHub(t, ctx, adminURL)
	t.Setenv("DIRECTOR_CLUSTER", "mokuzai")
	nc, err := nats.Connect(adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	kv, err := gjs.KeyValue(ctx, globalPresenceBucket)
	if err != nil {
		t.Fatal(err)
	}
	key := "presence.mokuzai.supervisor." + partialInstA
	if err := kv.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}

	reports, err := readUnreadGlobal(ctx, dialLeaf(t, leafURL), "global", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	r := reports[0]
	if len(r.Durables) != 0 {
		t.Fatalf("durables = %+v, want none: the only presence key is deleted", r.Durables)
	}
	if want := "unseen (not found): " + key; !strings.Contains(strings.Join(r.Warnings, "\n"), want) {
		t.Errorf("warnings %q do not say %q", r.Warnings, want)
	}
}

type countingKV struct {
	jetstream.KeyValue
	gets map[string]int
}

func (c *countingKV) Get(ctx context.Context, key string) (jetstream.KeyValueEntry, error) {
	c.gets[key]++
	return c.KeyValue.Get(ctx, key)
}

// The keys under a prefix are each got once, and the entry travels with the key.
func TestReadKeysUnderGetsEachKeyOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminURL, _ := startLeafLikeServer(t, false)
	seedPartialHub(t, ctx, adminURL)
	nc, err := nats.Connect(adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	kv, err := gjs.KeyValue(ctx, globalPresenceBucket)
	if err != nil {
		t.Fatal(err)
	}
	ckv := &countingKV{KeyValue: kv, gets: map[string]int{}}
	reads, err := readKeysUnder(ctx, gjs, globalPresenceBucket, ckv, "presence.mokuzai.supervisor.")
	if err != nil {
		t.Fatal(err)
	}
	key := "presence.mokuzai.supervisor." + partialInstA
	if len(reads) != 1 || reads[0].key != key || reads[0].err != nil || reads[0].entry == nil {
		t.Fatalf("reads = %+v, want the one mokuzai key with its entry", reads)
	}
	if ckv.gets[key] != 1 || len(ckv.gets) != 1 {
		t.Errorf("gets = %v, want exactly one get of %s", ckv.gets, key)
	}
}

// When the list verb is missing and the hub does not answer the named read, the
// reader probes once, not once to choose the path and again to word the error.
func TestUnreadGlobalProbesADownHubOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminURL, leafURL := startLeafLikeServerMapping(t, false, `"$JS.global.API.STREAM.INFO.KV_`+globalPresenceBucket+`": "dead.probe.presence"`)
	t.Setenv("DIRECTOR_CLUSTER", "mokuzai")
	nc, err := nats.Connect(adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	var probes atomic.Int32
	sub, err := nc.Subscribe("dead.probe.>", func(*nats.Msg) { probes.Add(1) }) // listens, never answers: a hub that does not reply
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sub.Unsubscribe() }()
	_ = nc.Flush()

	_, err = readUnreadGlobal(ctx, dialLeaf(t, leafURL), "global", time.Now())
	if err == nil || !strings.Contains(err.Error(), "not reachable") {
		t.Fatalf("err = %v, want the not-reachable wording", err)
	}
	if n := probes.Load(); n != 1 {
		t.Errorf("the hub was probed %d times, want 1", n)
	}
}

// jsCountingKV hands out a store that counts every get, whatever opens it.
type jsCountingKV struct {
	jetstream.JetStream
	gets map[string]int
}

func (j *jsCountingKV) KeyValue(ctx context.Context, bucket string) (jetstream.KeyValue, error) {
	kv, err := j.JetStream.KeyValue(ctx, bucket)
	if err != nil {
		return nil, err
	}
	return &countingKV{KeyValue: kv, gets: j.gets}, nil
}

// The partial read gets each presence key once end to end: a second get in its
// own loop, after the key helper has made one, fails here.
func TestUnreadGlobalPartialGetsEachPresenceKeyOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminURL, leafURL := startLeafLikeServer(t, false)
	seedPartialHub(t, ctx, adminURL)
	lgjs, err := jetstream.NewWithDomain(dialLeaf(t, leafURL), "global")
	if err != nil {
		t.Fatal(err)
	}
	counted := &jsCountingKV{JetStream: lgjs, gets: map[string]int{}}
	reports, err := readUnreadGlobalPartial(ctx, counted, "mokuzai", nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 2 || len(reports[0].Durables) != 1 {
		t.Fatalf("reports = %+v, want the one mokuzai seat read and the other cluster's self-reported report", reports)
	}
	// Each presence key, the own cluster's and the other's for the fallback,
	// is got once, from the one listing.
	keys := []string{"presence.mokuzai.supervisor." + partialInstA, "presence.other.supervisor." + partialInstB}
	for _, key := range keys {
		if counted.gets[key] != 1 {
			t.Errorf("gets = %v, want exactly one get of %s", counted.gets, key)
		}
	}
	if len(counted.gets) != len(keys) {
		t.Errorf("gets = %v, want only the keys %v", counted.gets, keys)
	}
}
