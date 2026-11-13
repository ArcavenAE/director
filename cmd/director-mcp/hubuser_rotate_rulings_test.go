package main

// Red tests for the architect's rulings on `director hub user rotate` and
// `revoke` (I-473 pr2-rulings.md, v2 with Amendment 1). They sit beside
// hubuser_rotate_test.go and use the same scratch hub.
//
//   - rotate takes --public-key and mints nothing; --finish moves a seed only
//     if one is held here and says so when none is (ruling 1).
//   - revoke of a user whose key is absent from the conf records it, after
//     checking the live hub refuses the key (ruling 2).
//   - the open-connection line reads "unknown" when the monitor cannot be read
//     (ruling 3); a revoked seed is named <name>-<time>.nk (ruling 1).
//   - last-admin counts active users only, and the admin whose seed is
//     DIRECTOR_HUB_ADMIN_NKEY is not revoked (ruling 4).
//   - the leaf template publishes global.*.supervisor.inbox, a class
//     leaf-director carries the director's grants, rotate refuses a narrowing
//     and prints additions, and an unrotated adopted row may change class
//     (Amendment 1, A to E).

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// reloadByHand sends the hub a SIGHUP and waits for it to reload, as an
// operator does after editing the conf.
func reloadByHand(t *testing.T, h *hubScratch) {
	t.Helper()
	n := h.reloads()
	if err := syscall.Kill(h.pid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	if !h.waitReloads(n + 1) {
		t.Fatalf("setup: the hub did not reload")
	}
}

// watched is a connection that records the permission violations the server
// reports for it.
type watched struct {
	nc  *nats.Conn
	mu  sync.Mutex
	bad []string
}

func (h *hubScratch) connectWatched(t *testing.T, seedPath string) *watched {
	t.Helper()
	w := &watched{}
	opt, err := nats.NkeyOptionFromSeed(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	nc, err := nats.Connect(h.url(), opt, nats.Timeout(2*time.Second), nats.MaxReconnects(0),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, e error) {
			w.mu.Lock()
			w.bad = append(w.bad, e.Error())
			w.mu.Unlock()
		}))
	if err != nil {
		t.Fatalf("the key in %s is refused: %v", seedPath, err)
	}
	t.Cleanup(nc.Close)
	w.nc = nc
	return w
}

// violationFor publishes to subject and reports whether the server refused it.
func (w *watched) violationFor(t *testing.T, subject string) bool {
	t.Helper()
	w.mu.Lock()
	w.bad = nil
	w.mu.Unlock()
	if err := w.nc.Publish(subject, []byte("x")); err != nil {
		t.Fatal(err)
	}
	_ = w.nc.Flush()
	time.Sleep(400 * time.Millisecond)
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, b := range w.bad {
		if strings.Contains(b, subject) {
			return true
		}
	}
	return false
}

// handLeaf is a hand-written leaf block for cluster probe whose grants all lie
// inside the class grant, plus the given extra publish subjects.
func handLeaf(pub string, extraPublish, extraSubscribe []string) string {
	q := func(l []string) string {
		s := ""
		for _, x := range l {
			s += `, "` + x + `"`
		}
		return s
	}
	return `      { nkey: ` + pub + `
        permissions {
          publish   { allow: [ "global.probe.>"` + q(extraPublish) + ` ] }
          subscribe { allow: [ "global.probe.>", "_INBOX.>"` + q(extraSubscribe) + ` ] }
        } }`
}

func adoptAs(t *testing.T, h *hubScratch, pub, name, class string) {
	t.Helper()
	if code, out, errOut := h.run("user", "adopt", pub, "--name", name, "--class", class, "--param", "cluster=probe"); code != 0 {
		t.Fatalf("setup: adopt exited %d\n%s%s", code, out, errOut)
	}
}

// --- Amendment 1 ------------------------------------------------------------------------------

func TestHubUserRotateLeafKeepsCrossClusterSupervisorSend(t *testing.T) {
	pub := newPublicKey(t)
	h := newHubScratch(t, hubScratchOpts{extraUsers: handLeaf(pub, []string{"global.*.supervisor.inbox"}, nil)})
	adoptAs(t, h, pub, "probe-leaf", "leaf")
	admin, err := h.connectAs(h.adminSeed, "")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	js, err := jetstream.New(admin)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := cctx()
	defer cancel()
	st, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: "GLOBAL_TO_other", Subjects: []string{"global.other.>"}})
	if err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := h.run("user", "rotate", "probe-leaf"); code != 0 {
		t.Fatalf("rotate exited %d\n%s%s", code, out, errOut)
	}
	w := h.connectWatched(t, filepath.Join(h.keys, "probe-leaf.next.nk"))
	if w.violationFor(t, "global.other.supervisor.inbox") {
		t.Errorf("the rotated leaf may not publish global.other.supervisor.inbox")
	}
	if !w.violationFor(t, "global.other.director.inbox") {
		t.Errorf("the rotated leaf may publish global.other.director.inbox, which the leaf grant does not allow")
	}
	info, err := st.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.State.Msgs != 1 {
		t.Errorf("GLOBAL_TO_other holds %d messages, want the one the rotated leaf published", info.State.Msgs)
	}
}

func TestHubUserRotateDirectorLeafKeepsDirectorSubscribe(t *testing.T) {
	pub := newPublicKey(t)
	hand := handLeaf(pub, []string{"global.director.>", "global.*.supervisor.inbox", "$JS.global.API.STREAM.MSG.DELETE.GLOBAL_TO_DIRECTOR"}, []string{"global.director.>"})
	h := newHubScratch(t, hubScratchOpts{extraUsers: hand})
	adoptAs(t, h, pub, "kinu-leaf", "leaf-director")
	if code, out, errOut := h.run("user", "rotate", "kinu-leaf"); code != 0 {
		t.Fatalf("rotate exited %d\n%s%s", code, out, errOut)
	}
	seed := filepath.Join(h.keys, "kinu-leaf.next.nk")
	w := h.connectWatched(t, seed)
	got := make(chan struct{}, 4)
	if _, err := w.nc.Subscribe("global.director.>", func(*nats.Msg) { got <- struct{}{} }); err != nil {
		t.Fatal(err)
	}
	_ = w.nc.Flush()
	admin, err := h.connectAs(h.adminSeed, "")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if err := admin.Publish("global.director.inbox", []byte("m")); err != nil {
		t.Fatal(err)
	}
	_ = admin.Flush()
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Errorf("the rotated leaf-director key did not receive a global.director.inbox publish")
	}
	// The scratch hub maps the JetStream domain prefix before it checks a publish, so a
	// permission on $JS.global.API.* cannot be probed by publishing to it here. The
	// grant is read from the block the command rendered for the new key instead.
	conf := string(h.readConf())
	block, found := entryBlock(conf, seedPub(t, seed))
	if !found {
		t.Fatalf("the new key has no block in the conf")
	}
	if n := strings.Count(block, "STREAM.MSG.DELETE."); n != 2 ||
		!strings.Contains(block, `"$JS.global.API.STREAM.MSG.DELETE.GLOBAL_TO_DIRECTOR"`) ||
		!strings.Contains(block, `"$JS.global.API.STREAM.MSG.DELETE.GLOBAL_TO_probe"`) {
		t.Errorf("the new key's block must allow message delete on GLOBAL_TO_DIRECTOR and GLOBAL_TO_probe only (found %d MSG.DELETE grants):\n%s", n, block)
	}
}

func TestHubUserRotateRefusesANarrowingGrant(t *testing.T) {
	pub := newPublicKey(t)
	h := newHubScratch(t, hubScratchOpts{extraUsers: handLeaf(pub, []string{"global.zzz.>"}, nil)})
	adoptAs(t, h, pub, "probe-leaf", "leaf")
	confBefore, regBefore := string(h.readConf()), string(h.readRegistry())
	listing := h.homeListing()
	reloads := h.reloads()
	code, out, errOut := h.run("user", "rotate", "probe-leaf")
	if code != 1 || !strings.Contains(errOut, "global.zzz.>") || !strings.Contains(errOut, "class") || !strings.Contains(errOut, "template") {
		t.Errorf("exit = %d, stderr = %q, want 1, the missing subject global.zzz.> and the two fixes (the right class, a template change)", code, errOut)
	}
	if string(h.readConf()) != confBefore || string(h.readRegistry()) != regBefore || h.homeListing() != listing || h.reloads() != reloads {
		t.Errorf("a refused rotate wrote the conf, the registry or a seed, or reloaded the hub (stdout %q)", out)
	}
	if code, _, _ := h.run("user", "rotate", "--finish", "probe-leaf"); code == 0 {
		t.Errorf("there is nothing to finish after a refused rotate")
	}
}

func TestHubUserRotateReportsMsgDeleteAsAnAddition(t *testing.T) {
	pub := newPublicKey(t)
	h := newHubScratch(t, hubScratchOpts{extraUsers: handLeaf(pub, nil, nil)})
	adoptAs(t, h, pub, "probe-leaf", "leaf")
	code, out, errOut := h.run("user", "rotate", "probe-leaf")
	if code != 0 {
		t.Fatalf("rotate exited %d\n%s%s", code, out, errOut)
	}
	var line string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "$JS.global.API.STREAM.MSG.DELETE.GLOBAL_TO_probe") {
			line = l
		}
	}
	if !strings.Contains(line, "in the class template and not in the conf block") {
		t.Errorf("rotate does not print MSG.DELETE on GLOBAL_TO_probe as an addition, line = %q\n%s", line, out)
	}
}

func TestHubUserReadoptChangesClassBeforeRotationAndRefusesAfter(t *testing.T) {
	pub := newPublicKey(t)
	h := newHubScratch(t, hubScratchOpts{extraUsers: handLeaf(pub, []string{"global.director.>"}, []string{"global.director.>"})})
	adoptAs(t, h, pub, "kinu-leaf", "leaf")
	confBefore := string(h.readConf())
	reloads := h.reloads()
	code, out, errOut := h.run("user", "adopt", pub, "--name", "kinu-leaf", "--class", "leaf-director", "--param", "cluster=probe")
	if code != 0 {
		t.Fatalf("re-adopt of a row never rotated: exit = %d\n%s%s", code, out, errOut)
	}
	if u := registryUser(h, "kinu-leaf"); u == nil || u["class"] != "leaf-director" || u["state"] != "adopted" {
		t.Errorf("registry = %v, want kinu-leaf as leaf-director, still adopted", u)
	}
	if string(h.readConf()) != confBefore || h.reloads() != reloads {
		t.Errorf("a re-adopt wrote the conf or reloaded the hub")
	}
	if n := len(h.registryUsers()); n != 1 {
		t.Errorf("a re-adopt left %d registry rows, want 1", n)
	}
	if code, out, errOut := h.run("user", "rotate", "kinu-leaf"); code != 0 {
		t.Fatalf("setup: rotate exited %d\n%s%s", code, out, errOut)
	}
	code, _, errOut = h.run("user", "adopt", pub, "--name", "kinu-leaf", "--class", "leaf", "--param", "cluster=probe")
	if code != 1 || !strings.Contains(errOut, "rotat") {
		t.Errorf("re-adopt after a rotation: exit = %d, stderr = %q, want 1 naming the rotation", code, errOut)
	}
}

func TestHubShippedTemplatesCarryTheAmendedGrants(t *testing.T) {
	leaf, err := os.ReadFile("hubclasses/leaf.conf")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(leaf), `"global.*.supervisor.inbox"`) {
		t.Errorf("hubclasses/leaf.conf does not publish global.*.supervisor.inbox")
	}
	ld, err := os.ReadFile("hubclasses/leaf-director.conf")
	if err != nil {
		t.Fatalf("hubclasses/leaf-director.conf: %v", err)
	}
	for _, want := range []string{"global.*.supervisor.inbox", "global.director.>", "GLOBAL_TO_DIRECTOR", "STREAM.MSG.DELETE.GLOBAL_TO_DIRECTOR", "GLOBAL_TO_{{cluster}}"} {
		if !strings.Contains(string(ld), want) {
			t.Errorf("hubclasses/leaf-director.conf lacks %s", want)
		}
	}
	doc, err := os.ReadFile("../../sim/design/hub-user-lifecycle.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), "`leaf-director`") || !strings.Contains(string(doc), "global.*.supervisor.inbox") {
		t.Errorf("the design doc's class table and grant row do not carry leaf-director and the supervisor inbox grant")
	}
}

// --- ruling 1: keys born elsewhere, seed handling ---------------------------------------------

func TestHubUserRotatePublicKeyMintsNoSeed(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	oldPub := newPublicKey(t)
	if code, out, errOut := h.run("user", "add", "far-leaf", "--class", "leaf", "--param", "cluster=far", "--public-key", oldPub); code != 0 {
		t.Fatalf("setup: add exited %d\n%s%s", code, out, errOut)
	}
	seeds := h.seedFiles()
	newPub := newPublicKey(t)
	code, out, errOut := h.run("user", "rotate", "far-leaf", "--public-key", newPub)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if got := h.seedFiles(); len(got) != len(seeds) {
		t.Errorf("rotate --public-key wrote a seed: %v", got)
	}
	if u := registryUser(h, "far-leaf"); u == nil || u["public_key"] != oldPub || u["pending_key"] != newPub {
		t.Errorf("registry = %v, want public_key %s and pending_key %s", u, oldPub, newPub)
	}
	if r := regionOf(h.readConf()); !strings.Contains(r, oldPub) || !strings.Contains(r, newPub) {
		t.Errorf("the region does not hold both keys:\n%s", r)
	}
	code, out, errOut = h.run("user", "rotate", "--finish", "far-leaf")
	if code != 0 || !strings.Contains(out, "no seed") {
		t.Errorf("finish with no seed held: exit = %d, stdout = %q, stderr = %q, want 0 and a line saying no seed is held here", code, out, errOut)
	}
	if len(h.revokedSeeds()) != 0 {
		t.Errorf("a seed appeared in keys/revoked for a key whose seed is not held: %v", h.revokedSeeds())
	}
	if u := registryUser(h, "far-leaf"); u == nil || u["public_key"] != newPub || u["pending_key"] != nil && u["pending_key"] != "" {
		t.Errorf("registry after finish = %v, want the new key and no pending key", u)
	}
}

func TestHubUserListReportsAnOrphanNextSeed(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	addUser(t, h, "svc", "director")
	newSeedFile(t, filepath.Join(h.keys, "svc.next.nk"))
	code, out, errOut := h.run("user", "list")
	if code != 0 {
		t.Fatalf("list exit = %d\n%s", code, errOut)
	}
	if !strings.Contains(out, "orphan") || !strings.Contains(out, "svc.next.nk") {
		t.Errorf("list does not report keys/svc.next.nk (no pending key) as an orphan:\n%s", out)
	}
}

func TestHubUserRevokeNamesTheMovedSeedByNameAndTime(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	addUser(t, h, "svc", "director")
	if code, out, errOut := h.run("user", "revoke", "svc"); code != 0 {
		t.Fatalf("revoke exited %d\n%s%s", code, out, errOut)
	}
	got := h.revokedSeeds()
	if len(got) != 1 || !strings.HasPrefix(filepath.Base(got[0]), "svc-") || !strings.HasSuffix(got[0], ".nk") {
		t.Errorf("revoked seeds = %v, want keys/revoked/svc-<time>.nk", got)
	}
}

func TestHubUserRevokeSaysUnknownWhenTheConnectionCountCannotBeRead(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	addUser(t, h, "svc", "director")
	// The reload is proven through /varz, so the monitor must answer there; only the
	// connection listing is made unreadable.
	real := fmt.Sprintf("http://127.0.0.1:%d", h.monPort)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/connz") {
			http.Error(w, "unavailable", http.StatusInternalServerError)
			return
		}
		resp, err := http.Get(real + r.URL.RequestURI())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	defer proxy.Close()
	h.envOverride = map[string]string{"DIRECTOR_HUB_MONITOR_URL": proxy.URL}
	code, out, errOut := h.run("user", "revoke", "svc")
	if code != 0 {
		t.Fatalf("exit = %d\n%s%s", code, out, errOut)
	}
	if !strings.Contains(out, "open connections: unknown") || strings.Contains(out, "still open") || strings.Contains(out, "closed by the reload") {
		t.Errorf("with the connection listing unreadable the output must say \"open connections: unknown\" and make no claim about closing:\n%s", out)
	}
}

// --- ruling 2: a key absent from the conf --------------------------------------------------------

func TestHubUserRevokeRecordsAnAdoptedUserWhoseBlockIsGone(t *testing.T) {
	seedPath := filepath.Join(t.TempDir(), "held.nk")
	pub := newSeedFile(t, seedPath)
	hand := handLeaf(pub, nil, nil)
	h := newHubScratch(t, hubScratchOpts{extraUsers: hand})
	adoptAs(t, h, pub, "probe-leaf", "leaf")
	b, _ := os.ReadFile(seedPath)
	if err := os.WriteFile(filepath.Join(h.keys, "probe-leaf.nk"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	removed := strings.Replace(string(h.readConf()), hand, "", 1)
	if removed == string(h.readConf()) {
		t.Fatalf("setup: the hand block was not found")
	}
	if err := os.WriteFile(h.conf, []byte(removed), 0o600); err != nil {
		t.Fatal(err)
	}
	// The conf no longer holds the key but the hub has not reloaded, so it still accepts it.
	regBefore := string(h.readRegistry())
	code, _, errOut := h.run("user", "revoke", "probe-leaf")
	if code != 1 || !strings.Contains(errOut, "still accepted") {
		t.Errorf("revoke while the live hub still accepts the key: exit = %d, stderr = %q, want 1 and still accepted", code, errOut)
	}
	if string(h.readRegistry()) != regBefore {
		t.Errorf("a refused revoke changed the registry")
	}
	reloadByHand(t, h)
	confBefore, reloads := string(h.readConf()), h.reloads()
	code, out, errOut := h.run("user", "revoke", "probe-leaf")
	if code != 0 || !strings.Contains(out, "absent from the conf; recorded only") {
		t.Fatalf("revoke after the hub refuses the key: exit = %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if string(h.readConf()) != confBefore || h.reloads() != reloads {
		t.Errorf("a recorded-only revoke wrote the conf or reloaded the hub")
	}
	if u := registryUser(h, "probe-leaf"); u == nil || u["state"] != "revoked" {
		t.Errorf("registry = %v, want probe-leaf revoked", u)
	}
	if got := h.revokedSeeds(); len(got) != 1 {
		t.Errorf("revoked seeds = %v, want the held seed moved there", got)
	}
}

// --- ruling 4: last admin ------------------------------------------------------------------------

func TestHubUserLastAdminCountsActiveUsersOnly(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	addUser(t, h, "admin-a", "admin")
	addUser(t, h, "admin-b", "admin")
	if code, out, errOut := h.run("user", "revoke", "admin-b"); code != 0 {
		t.Fatalf("setup: revoke exited %d\n%s%s", code, out, errOut)
	}
	if code, _, errOut := h.run("user", "revoke", "admin-a"); code != 1 || !strings.Contains(errOut, "last admin") {
		t.Errorf("revoke of the only active admin: exit = %d, stderr = %q, want 1 and the last-admin message (admin-b is revoked and does not count)", code, errOut)
	}
}

func TestHubUserRevokeRefusesTheAdminTheHubSettingNames(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	addUser(t, h, "admin-a", "admin")
	addUser(t, h, "admin-b", "admin")
	// DIRECTOR_HUB_ADMIN_NKEY names admin-a's seed: the command's own admin is that user.
	h.envOverride = map[string]string{"DIRECTOR_HUB_ADMIN_NKEY": filepath.Join(h.keys, "admin-a.nk")}
	confBefore, regBefore := string(h.readConf()), string(h.readRegistry())
	code, _, errOut := h.run("user", "revoke", "admin-a")
	if code != 1 || !strings.Contains(errOut, "DIRECTOR_HUB_ADMIN_NKEY") {
		t.Errorf("exit = %d, stderr = %q, want 1 naming DIRECTOR_HUB_ADMIN_NKEY to repoint first", code, errOut)
	}
	if string(h.readConf()) != confBefore || string(h.readRegistry()) != regBefore {
		t.Errorf("a refused revoke changed the conf or the registry")
	}
}

// --- Red Gate read, round 1 -------------------------------------------------------------------

// keysListing lists the keys directory only. The hub home also holds the conf
// backup the command writes before the conf, which is not a seed file.
func keysListing(t *testing.T, h *hubScratch) string {
	t.Helper()
	var names []string
	err := filepath.WalkDir(h.keys, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(h.keys, p)
		names = append(names, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(names, "\n")
}

// Ruling 1: the order is conf, reload, verify, then the registry, then the seed
// files. At the moment the hub has reloaded, neither the registry nor the keys
// directory has changed.
func TestHubUserRotateAndRevokeWriteTheRegistryAndSeedsAfterTheReload(t *testing.T) {
	defer func() { hubAfterReload = nil }()
	steps := []struct {
		name string
		args []string
		pre  func(h *hubScratch)
	}{
		{"rotate", []string{"user", "rotate", "svc"}, nil},
		{"finish", []string{"user", "rotate", "--finish", "svc"}, func(h *hubScratch) {
			if code, out, errOut := h.run("user", "rotate", "svc"); code != 0 {
				t.Fatalf("setup: rotate exited %d\n%s%s", code, out, errOut)
			}
		}},
		{"revoke", []string{"user", "revoke", "svc"}, nil},
	}
	for _, st := range steps {
		h := newHubScratch(t, hubScratchOpts{})
		addUser(t, h, "svc", "director")
		if st.pre != nil {
			st.pre(h)
		}
		reg, keys := string(h.readRegistry()), keysListing(t, h)
		var called bool
		hubAfterReload = func() {
			called = true
			if string(h.readRegistry()) != reg {
				t.Errorf("%s: the registry changed before the reload was verified", st.name)
			}
			if keysListing(t, h) != keys {
				t.Errorf("%s: the keys directory changed before the reload was verified", st.name)
			}
		}
		if code, out, errOut := h.run(st.args...); code != 0 {
			t.Fatalf("%s exited %d\n%s%s", st.name, code, out, errOut)
		}
		hubAfterReload = nil
		if !called {
			t.Errorf("%s: the command did not reach the post-reload point", st.name)
		}
		if string(h.readRegistry()) == reg {
			t.Errorf("%s: the registry was never written", st.name)
		}
	}
}

// Ruling 3: with no seed held here, revoke says so and moves nothing.
func TestHubUserRevokeSaysSoWhenNoSeedIsHeldHere(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	pub := newPublicKey(t)
	if code, out, errOut := h.run("user", "add", "far-leaf", "--class", "leaf", "--param", "cluster=far", "--public-key", pub); code != 0 {
		t.Fatalf("setup: add exited %d\n%s%s", code, out, errOut)
	}
	code, out, errOut := h.run("user", "revoke", "far-leaf")
	if code != 0 || !strings.Contains(out, "no seed") {
		t.Errorf("exit = %d, stdout = %q, stderr = %q, want 0 and a line saying no seed is held here", code, out, errOut)
	}
	if got := h.revokedSeeds(); len(got) != 0 {
		t.Errorf("keys/revoked holds %v for a key whose seed is not held here", got)
	}
}

// Ruling 4: a rotating admin counts once. With one admin holding two keys,
// revoking it leaves no admin, so it is refused; counting keys would allow it.
func TestHubUserLastAdminCountsARotatingAdminOnce(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	addUser(t, h, "admin-a", "admin")
	if code, out, errOut := h.run("user", "rotate", "admin-a"); code != 0 {
		t.Fatalf("setup: rotate exited %d\n%s%s", code, out, errOut)
	}
	confBefore, regBefore := string(h.readConf()), string(h.readRegistry())
	code, _, errOut := h.run("user", "revoke", "admin-a")
	if code != 1 || !strings.Contains(errOut, "last admin") {
		t.Errorf("revoke of the one rotating admin: exit = %d, stderr = %q, want 1 and the last-admin message", code, errOut)
	}
	if string(h.readConf()) != confBefore || string(h.readRegistry()) != regBefore {
		t.Errorf("a refused revoke changed the conf or the registry")
	}
	// With a second admin, revoking the rotating one is allowed, and the second is then the last.
	addUser(t, h, "admin-b", "admin")
	if code, out, errOut := h.run("user", "revoke", "admin-a"); code != 0 {
		t.Fatalf("revoke of a rotating admin with a second admin: exit = %d\n%s%s", code, out, errOut)
	}
	if code, _, errOut := h.run("user", "revoke", "admin-b"); code != 1 || !strings.Contains(errOut, "last admin") {
		t.Errorf("revoke of the remaining admin: exit = %d, stderr = %q, want 1 and the last-admin message", code, errOut)
	}
}

// Ruling 4: the refusal for a hand-written admin suggests adopt.
func TestHubUserLastAdminRefusalSuggestsAdoptForAHandWrittenAdmin(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	addUser(t, h, "admin-a", "admin")
	// The hub's own hand-written admin block is in the conf and not in the registry.
	code, _, errOut := h.run("user", "revoke", "admin-a")
	if code != 1 || !strings.Contains(errOut, "last admin") || !strings.Contains(errOut, "adopt") {
		t.Errorf("exit = %d, stderr = %q, want 1, the last-admin message and a suggestion to adopt the hand-written admin", code, errOut)
	}
}

// Ruling 5: hubAfterReload is nil in shipped code and nothing outside a test file
// assigns it, by flag, environment or anything else.
func TestHubAfterReloadIsNilAndNoShippedCodeSetsIt(t *testing.T) {
	if hubAfterReload != nil {
		t.Errorf("hubAfterReload is set at the start of a test run")
	}
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range pkgs {
		for name, f := range pkg.Files {
			ast.Inspect(f, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.AssignStmt:
					for _, l := range x.Lhs {
						if id, ok := l.(*ast.Ident); ok && id.Name == "hubAfterReload" {
							t.Errorf("%s assigns hubAfterReload at %s", name, fset.Position(x.Pos()))
						}
					}
				case *ast.ValueSpec:
					for i, id := range x.Names {
						if id.Name == "hubAfterReload" && i < len(x.Values) {
							t.Errorf("%s initialises hubAfterReload at %s", name, fset.Position(x.Pos()))
						}
					}
				}
				return true
			})
		}
	}
}

// --- Amendment 2 ------------------------------------------------------------------------------

// A revoke recorded without a seed to try says the refusal was not verified and
// why, and the registry row carries it so list shows it.
func TestHubUserRevokeRecordsUnverifiedWhenNoSeedIsHeld(t *testing.T) {
	pub := newPublicKey(t)
	hand := handLeaf(pub, nil, nil)
	h := newHubScratch(t, hubScratchOpts{extraUsers: hand})
	adoptAs(t, h, pub, "probe-leaf", "leaf")
	removed := strings.Replace(string(h.readConf()), hand, "", 1)
	if removed == string(h.readConf()) {
		t.Fatalf("setup: the hand block was not found")
	}
	if err := os.WriteFile(h.conf, []byte(removed), 0o600); err != nil {
		t.Fatal(err)
	}
	reloadByHand(t, h)
	code, out, errOut := h.run("user", "revoke", "probe-leaf")
	if code != 0 {
		t.Fatalf("exit = %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "not verified") || !strings.Contains(out, "no seed is held on this host") {
		t.Errorf("the note must say the refusal was not verified because no seed is held on this host:\n%s", out)
	}
	if u := registryUser(h, "probe-leaf"); u == nil || u["verified"] != false {
		t.Errorf("registry = %v, want verified: false on the revoked row", u)
	}
	_, list, _ := h.run("user", "list")
	var row string
	for _, l := range strings.Split(list, "\n") {
		if strings.HasPrefix(l, "probe-leaf ") {
			row = l
		}
	}
	if !strings.Contains(row, "revoked") || !strings.Contains(row, "unverified") {
		t.Errorf("list does not show the unverified revoke: %q", row)
	}
}

// The same holds when the key is still in the conf and removed by revoke: a seed
// held and refused records verified: true, no seed records verified: false.
func TestHubUserRevokeRowCarriesWhetherTheRefusalWasVerified(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	addUser(t, h, "held", "director")
	if code, out, errOut := h.run("user", "add", "far-leaf", "--class", "leaf", "--param", "cluster=far", "--public-key", newPublicKey(t)); code != 0 {
		t.Fatalf("setup: add exited %d\n%s%s", code, out, errOut)
	}
	code, out, errOut := h.run("user", "revoke", "held")
	if code != 0 {
		t.Fatalf("revoke held exited %d\n%s%s", code, out, errOut)
	}
	if u := registryUser(h, "held"); u == nil || u["verified"] != true {
		t.Errorf("registry = %v, want verified: true after the hub refused the held seed", u)
	}
	code, out, errOut = h.run("user", "revoke", "far-leaf")
	if code != 0 {
		t.Fatalf("revoke far-leaf exited %d\n%s%s", code, out, errOut)
	}
	if !strings.Contains(out, "not verified") {
		t.Errorf("the revoke with no seed held does not say the refusal was not verified:\n%s", out)
	}
	if u := registryUser(h, "far-leaf"); u == nil || u["verified"] != false {
		t.Errorf("registry = %v, want verified: false", u)
	}
}

// An adopted admin counts for the last-admin rule; a revoked one does not.
func TestHubUserLastAdminCountsAnAdoptedAdmin(t *testing.T) {
	pub := newPublicKey(t)
	hand := `      { nkey: ` + pub + ` }`
	h := newHubScratch(t, hubScratchOpts{extraUsers: hand})
	addUser(t, h, "admin-a", "admin")
	if code, out, errOut := h.run("user", "adopt", pub, "--name", "admin-old", "--class", "admin"); code != 0 {
		t.Fatalf("setup: adopt exited %d\n%s%s", code, out, errOut)
	}
	code, out, errOut := h.run("user", "revoke", "admin-a")
	if code != 0 {
		t.Errorf("revoke of the added admin with an adopted admin present: exit = %d\nstdout: %s\nstderr: %s, want 0", code, out, errOut)
	}
}
