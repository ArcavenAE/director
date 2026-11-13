package main

// Leafnode tests for the leaf and leaf-director grants (Amendment 3). The leaf
// classes are credentials a broker process presents over a leafnode link, so
// their grants are proved on two scratch servers: a hub in the JetStream domain
// "global" that accepts leafnode connections, and a leaf server in its own domain
// whose remote authenticates with the rotated key. The assertions go through the
// domain from a client of the leaf server, as verify-global.sh does.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// leafServer starts a scratch leaf server whose remote authenticates to the hub
// with the seed in seedPath, and returns a client URL for it once the hub's
// domain answers through the link.
func (h *hubScratch) leafServer(t *testing.T, seedPath string) string {
	t.Helper()
	bin, err := exec.LookPath("nats-server")
	if err != nil {
		t.Fatalf("nats-server is not on PATH")
	}
	seed, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	port := freePort(t)
	conf := filepath.Join(dir, "leaf.conf")
	body := fmt.Sprintf("listen: 127.0.0.1:%d\njetstream { store_dir: %q, domain: leafdom }\nleafnodes { remotes: [ { urls: [\"nats-leaf://127.0.0.1:%d\"], nkey: $LEAF_NKEY } ] }\n",
		port, filepath.Join(dir, "js"), h.leafPort)
	if err := os.WriteFile(conf, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-c", conf)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LEAF_NKEY=" + strings.TrimSpace(string(seed))}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	url := fmt.Sprintf("nats://127.0.0.1:%d", port)
	// The link is up when the leaf server sees the hub's domain: a stream info
	// the leaf's own grant allows answers through it.
	for i := 0; i < 100; i++ {
		if nc, err := nats.Connect(url, nats.Timeout(time.Second)); err == nil {
			js, _ := jetstream.NewWithDomain(nc, "global")
			ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
			_, err := js.Stream(ctx, "GLOBAL_TO_probe")
			cancel()
			nc.Close()
			if err == nil {
				return url
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	return url
}

// leafnodeFixture makes a hub with a leafnode listener, three streams made as
// admin (GLOBAL_TO_DIRECTOR and GLOBAL_TO_other with one message each; the
// harness already made GLOBAL_TO_probe with three), and an adopted hand-written
// leaf of the given class, rotated. It returns the leaf server's URL.
func leafnodeFixture(t *testing.T, class string) (*hubScratch, string, jetstream.JetStream) {
	t.Helper()
	pub := newPublicKey(t)
	h := newHubScratch(t, hubScratchOpts{extraUsers: handFor(class, pub), leafListen: true})
	adoptAs(t, h, pub, "kinu-leaf", class)
	admin, err := h.connectAs(h.adminSeed, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	ajs, err := jetstream.New(admin)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := cctx()
	defer cancel()
	for _, s := range []struct{ name, subj string }{{"GLOBAL_TO_DIRECTOR", "global.director.>"}, {"GLOBAL_TO_other", "global.other.>"}} {
		if _, err := ajs.CreateStream(ctx, jetstream.StreamConfig{Name: s.name, Subjects: []string{s.subj}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, subj := range []string{"global.director.inbox", "global.other.supervisor.inbox"} {
		if _, err := ajs.Publish(ctx, subj, []byte("1")); err != nil {
			t.Fatal(err)
		}
	}
	if code, out, errOut := h.run("user", "rotate", "kinu-leaf"); code != 0 {
		t.Fatalf("rotate exited %d\n%s%s", code, out, errOut)
	}
	return h, h.leafServer(t, filepath.Join(h.keys, "kinu-leaf.next.nk")), ajs
}

func leafStreamMsgs(t *testing.T, ajs jetstream.JetStream, name string) uint64 {
	t.Helper()
	ctx, cancel := cctx()
	defer cancel()
	st, err := ajs.Stream(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	info, err := st.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return info.State.Msgs
}

// refused reports whether a raw JetStream API request through the domain gets no answer.
func refused(nc *nats.Conn, subject, body string) bool {
	_, err := nc.Request(subject, []byte(body), 1500*time.Millisecond)
	return err != nil
}

func TestLeafDirectorKeyOverALeafnodeReadsAndDeletesOnGlobalToDirectorOnly(t *testing.T) {
	_, url, ajs := leafnodeFixture(t, "leaf-director")
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := jetstream.NewWithDomain(nc, "global")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, err := js.Stream(ctx, "GLOBAL_TO_DIRECTOR")
	if err != nil {
		t.Fatalf("stream info on GLOBAL_TO_DIRECTOR: %v", err)
	}
	cons, err := st.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{Durable: "leafdir", FilterSubject: "global.director.inbox", AckPolicy: jetstream.AckExplicitPolicy})
	if err != nil {
		t.Fatalf("consumer create on GLOBAL_TO_DIRECTOR: %v", err)
	}
	msg, err := cons.Next(jetstream.FetchMaxWait(3 * time.Second))
	if err != nil {
		t.Fatalf("MSG.NEXT on GLOBAL_TO_DIRECTOR: %v", err)
	}
	md, _ := msg.Metadata()
	if err := st.DeleteMsg(ctx, md.Sequence.Stream); err != nil {
		t.Errorf("MSG.DELETE on GLOBAL_TO_DIRECTOR: %v", err)
	}
	if n := leafStreamMsgs(t, ajs, "GLOBAL_TO_DIRECTOR"); n != 0 {
		t.Errorf("GLOBAL_TO_DIRECTOR holds %d messages after the delete, want 0", n)
	}
	if !refused(nc, "$JS.global.API.STREAM.MSG.DELETE.GLOBAL_TO_other", `{"seq":1}`) {
		t.Errorf("MSG.DELETE on another cluster's stream was answered")
	}
	if n := leafStreamMsgs(t, ajs, "GLOBAL_TO_other"); n != 1 {
		t.Errorf("GLOBAL_TO_other holds %d messages, want its one untouched", n)
	}
}

func TestLeafKeyOverALeafnodeSendsAcrossClustersDeletesOwnAndIsRefusedTheDirectorStream(t *testing.T) {
	_, url, ajs := leafnodeFixture(t, "leaf")
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := jetstream.NewWithDomain(nc, "global")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// The cross-cluster supervisor send: the leaf is probe, the stream is another cluster's.
	if _, err := js.Publish(ctx, "global.other.supervisor.inbox", []byte("2")); err != nil {
		t.Errorf("publish to global.other.supervisor.inbox over the leafnode: %v", err)
	}
	if n := leafStreamMsgs(t, ajs, "GLOBAL_TO_other"); n != 2 {
		t.Errorf("GLOBAL_TO_other holds %d messages, want the second one stored", n)
	}
	// MSG.DELETE on its own stream.
	own, err := js.Stream(ctx, "GLOBAL_TO_probe")
	if err != nil {
		t.Fatalf("stream info on its own GLOBAL_TO_probe: %v", err)
	}
	before := leafStreamMsgs(t, ajs, "GLOBAL_TO_probe")
	if err := own.DeleteMsg(ctx, 1); err != nil {
		t.Errorf("MSG.DELETE on its own stream: %v", err)
	}
	if n := leafStreamMsgs(t, ajs, "GLOBAL_TO_probe"); n != before-1 {
		t.Errorf("GLOBAL_TO_probe holds %d messages, want %d", n, before-1)
	}
	// Any JetStream call on GLOBAL_TO_DIRECTOR.
	if _, err := js.Stream(ctx, "GLOBAL_TO_DIRECTOR"); err == nil {
		t.Errorf("stream info on GLOBAL_TO_DIRECTOR was answered for a plain leaf")
	}
	if !refused(nc, "$JS.global.API.STREAM.MSG.DELETE.GLOBAL_TO_DIRECTOR", `{"seq":1}`) {
		t.Errorf("MSG.DELETE on GLOBAL_TO_DIRECTOR was answered for a plain leaf")
	}
	if n := leafStreamMsgs(t, ajs, "GLOBAL_TO_DIRECTOR"); n != 1 {
		t.Errorf("GLOBAL_TO_DIRECTOR holds %d messages, want its one untouched", n)
	}
}

// --- the client-connection property -------------------------------------------------------------

// Measured on a domain-"global" hub (nats-server 2.14.6): on a client connection the server maps
// $JS.<domain>.API to $JS.API before it checks the grant. A leaf credential spells
// its JetStream grants $JS.global.API, which a client connection never presents, so a leaf
// key used as a plain client gets no JetStream. A client credential (director) spells
// $JS.API and works.
func TestLeafClassGrantsAllowNoJetStreamOnAClientConnection(t *testing.T) {
	for _, class := range []string{"leaf", "leaf-director"} {
		pub := newPublicKey(t)
		h := newHubScratch(t, hubScratchOpts{extraUsers: handFor(class, pub)})
		adoptAs(t, h, pub, "kinu-leaf", class)
		admin, err := h.connectAs(h.adminSeed, "")
		if err != nil {
			t.Fatal(err)
		}
		ajs, _ := jetstream.New(admin)
		ctx, cancel := cctx()
		if _, err := ajs.CreateStream(ctx, jetstream.StreamConfig{Name: "GLOBAL_TO_DIRECTOR", Subjects: []string{"global.director.>"}}); err != nil {
			t.Fatal(err)
		}
		if _, err := ajs.Publish(ctx, "global.director.inbox", []byte("1")); err != nil {
			t.Fatal(err)
		}
		// Positive control: the same JetStream answers admin on this hub, so a refusal
		// below is the grant and not an outage.
		if _, err := ajs.Stream(ctx, "GLOBAL_TO_DIRECTOR"); err != nil {
			t.Fatalf("%s: control: admin stream info on GLOBAL_TO_DIRECTOR failed: %v", class, err)
		}
		cancel()
		if code, out, errOut := h.run("user", "rotate", "kinu-leaf"); code != 0 {
			t.Fatalf("%s: rotate exited %d\n%s%s", class, code, out, errOut)
		}
		nc, err := nats.Connect(h.url(), nkeyOpt(t, filepath.Join(h.keys, "kinu-leaf.next.nk")), nats.Timeout(2*time.Second))
		if err != nil {
			t.Fatalf("%s: %v", class, err)
		}
		js, _ := jetstream.NewWithDomain(nc, "global")
		dctx, dcancel := context.WithTimeout(context.Background(), 2*time.Second)
		if _, err := js.Stream(dctx, "GLOBAL_TO_DIRECTOR"); err == nil {
			t.Errorf("%s: stream info answered on a client connection", class)
		}
		dcancel()
		nc.Close()
		if n := leafStreamMsgs(t, ajs, "GLOBAL_TO_DIRECTOR"); n != 1 {
			t.Errorf("%s: GLOBAL_TO_DIRECTOR holds %d messages, want its one untouched", class, n)
		}
		admin.Close()
	}
}

func TestDirectorClassGrantsTheSameCallsOnAClientConnection(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	addUser(t, h, "dir", "director")
	admin, err := h.connectAs(h.adminSeed, "")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	ajs, _ := jetstream.New(admin)
	ctx, cancel := cctx()
	defer cancel()
	if _, err := ajs.CreateStream(ctx, jetstream.StreamConfig{Name: "GLOBAL_TO_DIRECTOR", Subjects: []string{"global.director.>"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := ajs.Publish(ctx, "global.director.inbox", []byte("1")); err != nil {
		t.Fatal(err)
	}
	nc, err := nats.Connect(h.url(), nkeyOpt(t, filepath.Join(h.keys, "dir.nk")), nats.Timeout(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, _ := jetstream.NewWithDomain(nc, "global")
	dctx, dcancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer dcancel()
	st, err := js.Stream(dctx, "GLOBAL_TO_DIRECTOR")
	if err != nil {
		t.Fatalf("stream info as the director class: %v", err)
	}
	if err := st.DeleteMsg(dctx, 1); err != nil {
		t.Errorf("delete as the director class: %v", err)
	}
	if n := leafStreamMsgs(t, ajs, "GLOBAL_TO_DIRECTOR"); n != 0 {
		t.Errorf("GLOBAL_TO_DIRECTOR holds %d messages, want 0", n)
	}
}

// --- templates name their connection type and take the domain ------------------------------------

func TestClassTemplatesNameTheirConnectionTypeAndTakeTheDomain(t *testing.T) {
	for class, want := range map[string]string{"leaf": "leafnode", "leaf-director": "leafnode", "director": "client", "observer": "client", "admin": "client"} {
		b, err := os.ReadFile(filepath.Join("hubclasses", class+".conf"))
		if err != nil {
			t.Errorf("%s: %v", class, err)
			continue
		}
		first := strings.SplitN(string(b), "\n{", 2)[0]
		if !strings.Contains(first, want+" credential") {
			t.Errorf("hubclasses/%s.conf: the header comment must name the connection type %q credential:\n%s", class, want, first)
		}
	}
	for _, class := range []string{"leaf", "leaf-director"} {
		b, _ := os.ReadFile(filepath.Join("hubclasses", class+".conf"))
		if strings.Contains(string(b), "$JS.global.API") || !strings.Contains(string(b), "$JS.{{domain}}.API") {
			t.Errorf("hubclasses/%s.conf must spell its JetStream grants $JS.{{domain}}.API, never a literal domain and never $JS.API", class)
		}
		if strings.Contains(string(b), `"$JS.API`) {
			t.Errorf("hubclasses/%s.conf carries a $JS.API grant; a leafnode credential never does", class)
		}
	}
}

func TestLeafRendersTheDomainFromAParameterWithGlobalAsTheDefault(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	pubA, pubB := newPublicKey(t), newPublicKey(t)
	if code, out, errOut := h.run("user", "add", "far-a", "--class", "leaf", "--param", "cluster=fara", "--public-key", pubA); code != 0 {
		t.Fatalf("add without a domain exited %d\n%s%s", code, out, errOut)
	}
	if code, out, errOut := h.run("user", "add", "far-b", "--class", "leaf", "--param", "cluster=farb", "--param", "domain=ops", "--public-key", pubB); code != 0 {
		t.Fatalf("add with --param domain=ops exited %d\n%s%s", code, out, errOut)
	}
	conf := string(h.readConf())
	blockA, _ := entryBlock(conf, pubA)
	blockB, _ := entryBlock(conf, pubB)
	if !strings.Contains(blockA, "$JS.global.API.STREAM.INFO.GLOBAL_TO_fara") {
		t.Errorf("the default domain is global; block:\n%s", blockA)
	}
	if !strings.Contains(blockB, "$JS.ops.API.STREAM.INFO.GLOBAL_TO_farb") || strings.Contains(blockB, "$JS.global.API") {
		t.Errorf("--param domain=ops must render $JS.ops.API; block:\n%s", blockB)
	}
}

// handFor is a hand-written leaf block that lies inside the grant of the class.
func handFor(class, pub string) string {
	if class == "leaf-director" {
		return handLeaf(pub, []string{"global.director.>", "global.*.supervisor.inbox"}, []string{"global.director.>"})
	}
	return handLeaf(pub, []string{"global.*.supervisor.inbox"}, nil)
}
