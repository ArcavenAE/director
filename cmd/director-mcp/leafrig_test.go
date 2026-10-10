package main

import (
	"context"
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

// leafRig is a scratch hub and one leaf broker on loopback, the production
// topology in miniature: the shim connects to the leaf, and reaches the hub's
// JetStream domain "global" over the leaf link. A single server serving both
// domains (startScratchServer) answers a pull on a missing durable with no
// responders; across a leaf link the same pull can go unanswered, which is the
// case that matters in production.
type leafRig struct {
	t       *testing.T
	bin     string
	hubConf string
	hubURL  string
	leafURL string
	hub     *exec.Cmd
	leaf    *exec.Cmd
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func startLeafRig(t *testing.T) *leafRig {
	t.Helper()
	bin, err := exec.LookPath("nats-server")
	if err != nil {
		t.Skip("nats-server not on PATH")
	}
	dir := t.TempDir()
	hp, hl, lp := freePort(t), freePort(t), freePort(t)
	r := &leafRig{t: t, bin: bin,
		hubConf: filepath.Join(dir, "hub.conf"),
		hubURL:  fmt.Sprintf("nats://127.0.0.1:%d", hp),
		leafURL: fmt.Sprintf("nats://127.0.0.1:%d", lp)}
	write := func(p, body string) {
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(r.hubConf, fmt.Sprintf("listen: 127.0.0.1:%d\njetstream { store_dir: %q, domain: global }\nleafnodes { listen: 127.0.0.1:%d }\n", hp, filepath.Join(dir, "hubjs"), hl))
	leafConf := filepath.Join(dir, "leaf.conf")
	write(leafConf, fmt.Sprintf("listen: 127.0.0.1:%d\njetstream { store_dir: %q, domain: leaf }\nleafnodes { remotes: [ { url: \"nats-leaf://127.0.0.1:%d\" } ] }\n", lp, filepath.Join(dir, "leafjs"), hl))
	r.hub = r.run(r.hubConf)
	r.leaf = r.run(leafConf)
	t.Cleanup(func() {
		for _, c := range []*exec.Cmd{r.leaf, r.hub} {
			if c != nil && c.Process != nil {
				_ = c.Process.Kill()
				_, _ = c.Process.Wait()
			}
		}
	})
	r.waitUp(r.hubURL)
	r.waitUp(r.leafURL)
	r.waitLink()
	return r
}

func (r *leafRig) run(conf string) *exec.Cmd {
	c := exec.Command(r.bin, "-c", conf)
	if err := c.Start(); err != nil {
		r.t.Fatal(err)
	}
	return c
}

func (r *leafRig) waitUp(url string) {
	for i := 0; i < 50; i++ {
		if nc, err := nats.Connect(url); err == nil {
			nc.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	r.t.Fatalf("scratch server %s did not come up", url)
}

// waitLink waits until the hub domain answers through the leaf.
func (r *leafRig) waitLink() {
	nc, err := nats.Connect(r.leafURL)
	if err != nil {
		r.t.Fatal(err)
	}
	defer nc.Close()
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	for i := 0; i < 100; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		_, err := gjs.AccountInfo(ctx)
		cancel()
		if err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	r.t.Fatal("hub domain not reachable through the leaf")
}

// provision creates the local stream and bucket on the leaf and the hub's
// director stream and bucket, through the leaf, as the shim will see them.
func (r *leafRig) provision(ctx context.Context) (jetstream.JetStream, jetstream.JetStream) {
	nc, err := nats.Connect(r.leafURL)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(nc.Close)
	js, _ := jetstream.New(nc)
	if _, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: "AGENT_INBOX", Subjects: []string{"agent.*.*.*.inbox", "agent.*.*.role.*.inbox"}}); err != nil {
		r.t.Fatal(err)
	}
	if _, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: "AGENT_STATE", TTL: 90 * time.Second}); err != nil {
		r.t.Fatal(err)
	}
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	if _, err := gjs.CreateStream(ctx, jetstream.StreamConfig{Name: globalDirectorStream, Subjects: []string{"global.director.>"}}); err != nil {
		r.t.Fatal(err)
	}
	if _, err := gjs.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: globalPresenceBucket, TTL: 90 * time.Second}); err != nil {
		r.t.Fatal(err)
	}
	return js, gjs
}

// restartHub stops the hub and starts it again on the same port and store.
func (r *leafRig) restartHub() {
	_ = r.hub.Process.Signal(os.Interrupt)
	_, _ = r.hub.Process.Wait()
	r.hub = r.run(r.hubConf)
	r.waitUp(r.hubURL)
	r.waitLink()
}
