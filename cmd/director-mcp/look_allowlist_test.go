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
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// The look runs as a user whose publish permission is exactly the design's
// allowlist (timed-inbox-look.md section 3.5). The broker, not a list of
// forbidden calls, then decides: a request outside the table is denied and the
// look would fail. A control shows the same user cannot publish or ack.

// lookAllowlist is the publish permission of the look's read-only user.
var lookAllowlist = []string{
	"$JS.API.STREAM.INFO.AGENT_INBOX",
	"$JS.API.CONSUMER.NAMES.AGENT_INBOX",
	"$JS.API.CONSUMER.INFO.AGENT_INBOX.*",
	"$JS.API.DIRECT.GET.AGENT_INBOX",
	"$JS.API.STREAM.MSG.GET.AGENT_INBOX",
}

func startAuthServer(t *testing.T) string {
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
	quoted := make([]string, len(lookAllowlist))
	for i, s := range lookAllowlist {
		quoted[i] = fmt.Sprintf("%q", s)
	}
	conf := fmt.Sprintf(`listen: 127.0.0.1:%d
jetstream { store_dir: %q }
authorization {
  users = [
    {user: "admin", password: "adm", permissions: {publish: ">", subscribe: ">"}}
    {user: "look", password: "lk", permissions: {publish: {allow: [%s]}, subscribe: {allow: ["_INBOX.>"]}}}
  ]
}
`, port, filepath.Join(dir, "js"), strings.Join(quoted, ", "))
	cf := filepath.Join(dir, "s.conf")
	if err := os.WriteFile(cf, []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-c", cf)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	url := fmt.Sprintf("127.0.0.1:%d", port)
	for i := 0; i < 50; i++ {
		if nc, err := nats.Connect("nats://admin:adm@"+url, nats.Timeout(time.Second)); err == nil {
			nc.Close()
			return url
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("scratch server did not start")
	return ""
}

func TestLookRunsUnderTheAllowlistOnBothStreamShapes(t *testing.T) {
	for _, allowDirect := range []bool{false, true} {
		t.Run(fmt.Sprintf("allow_direct=%v", allowDirect), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			addr := startAuthServer(t)
			nc, err := nats.Connect("nats://admin:adm@" + addr)
			if err != nil {
				t.Fatal(err)
			}
			defer nc.Close()
			js, _ := jetstream.New(nc)
			if _, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: "AGENT_INBOX", Subjects: []string{"agent.*.*.*.inbox"}, AllowDirect: allowDirect}); err != nil {
				t.Fatal(err)
			}
			if _, err := js.CreateOrUpdateConsumer(ctx, "AGENT_INBOX", jetstream.ConsumerConfig{
				Durable: "mcp_director_i1", FilterSubjects: []string{lookSubject}, AckPolicy: jetstream.AckExplicitPolicy,
			}); err != nil {
				t.Fatal(err)
			}
			for i, id := range []string{"a", "b"} {
				e := testEnv(id, "sup", "INFORM", fmt.Sprintf("GATE x#%d draft", i))
				b, _ := json.Marshal(e)
				if _, err := js.Publish(ctx, lookSubject, b); err != nil {
					t.Fatal(err)
				}
			}
			other := testEnv("theirs", "their-sender", "INFORM", "GATE CANARY-BODY")
			ob, _ := json.Marshal(other)
			if _, err := js.Publish(ctx, "agent.aae-orc.ops.someone-else.inbox", ob); err != nil {
				t.Fatal(err)
			}

			state := filepath.Join(t.TempDir(), "look")
			var out, errb strings.Builder
			code := runLook(ctx, lookOpts{URL: "nats://look:lk@" + addr, State: state, Seat: "director", Now: time.Now}, &out, &errb)
			if code != 0 {
				t.Fatalf("look under the allowlist exited %d: %s %s", code, out.String(), errb.String())
			}
			d, err := os.ReadFile(filepath.Join(state, "digest.txt"))
			if err != nil || !strings.Contains(string(d), "waiting: local 2") || strings.Contains(string(d), "theirs") || strings.Contains(string(d), "CANARY") {
				t.Fatalf("digest = %q (%v), want two of my messages and nothing of the other seat's", d, err)
			}
		})
	}
}

// The control: the allowlist bites. The same user cannot publish to an inbox or
// ack, so a look with one such call would fail here.
func TestTheLookUserCannotPublishOrConsume(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	addr := startAuthServer(t)
	admin, err := nats.Connect("nats://admin:adm@" + addr)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	ajs, _ := jetstream.New(admin)
	if _, err := ajs.CreateStream(ctx, jetstream.StreamConfig{Name: "AGENT_INBOX", Subjects: []string{"agent.*.*.*.inbox"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := ajs.CreateOrUpdateConsumer(ctx, "AGENT_INBOX", jetstream.ConsumerConfig{Durable: "mcp_director_i1", FilterSubjects: []string{lookSubject}, AckPolicy: jetstream.AckExplicitPolicy}); err != nil {
		t.Fatal(err)
	}
	seed, _ := json.Marshal(testEnv("seeded", "sup", "INFORM", "x"))
	if _, err := ajs.Publish(ctx, lookSubject, seed); err != nil {
		t.Fatal(err)
	}
	nc, err := nats.Connect("nats://look:lk@" + addr)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, _ := jetstream.New(nc)
	pctx, pcancel := context.WithTimeout(ctx, 2*time.Second)
	defer pcancel()
	if _, err := js.Publish(pctx, lookSubject, []byte("x")); err == nil {
		t.Errorf("the look user published to an inbox")
	}
	cons, err := js.Consumer(ctx, "AGENT_INBOX", "mcp_director_i1")
	if err != nil {
		t.Fatalf("positive control: the look user cannot read CONSUMER.INFO: %v", err)
	}
	batch, err := cons.Fetch(1, jetstream.FetchMaxWait(time.Second))
	if err == nil {
		got := 0
		for range batch.Messages() {
			got++
		}
		if got != 0 {
			t.Errorf("the look user pulled %d message(s) from a consumer", got)
		}
	}
	// And the consumer is untouched: the seeded message is still unread.
	info, err := ajs.Consumer(ctx, "AGENT_INBOX", "mcp_director_i1")
	if err != nil {
		t.Fatal(err)
	}
	ci, _ := info.Info(ctx)
	if ci.NumPending != 1 || ci.Delivered.Stream != 0 {
		t.Errorf("consumer moved: pending %d delivered %d, want 1 and 0", ci.NumPending, ci.Delivered.Stream)
	}
}
