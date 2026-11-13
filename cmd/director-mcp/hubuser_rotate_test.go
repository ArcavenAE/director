package main

// Red tests for `director hub user rotate [--finish]` and `director hub user
// revoke` (sim/design/hub-user-lifecycle.md, PR 2). They run against the same
// scratch hub as the add tests (hubuser_harness_test.go).
//
// Contracts these tests pin, each stated where the design is silent:
//   - rotate NAME mints a second key into keys/NAME.next.nk (0600), renders a
//     second block with the same grant into the managed region, reloads, and
//     verifies the new key as add does and that the old key still connects. The
//     registry keeps public_key (the old key) and gains pending_key.
//   - rotate --finish NAME removes the old key's block, reloads, verifies the
//     old key is refused and the new key connects, moves the old seed to
//     keys/revoked/, renames keys/NAME.next.nk to keys/NAME.nk, and sets
//     public_key to the new key, clears pending_key and sets rotated.
//   - revoke NAME removes the user's blocks from the managed region, reloads,
//     verifies the old key is refused, moves its seed to keys/revoked/ (never
//     deletes it), sets the registry state to revoked, and says whether an open
//     connection of that user was closed by the reload.
//   - The command edits only the managed region. A user outside it (adopted)
//     can be rotated into the region, but its old block is removed by hand, and
//     revoke of such a user is refused.
//   - revoke refuses the last admin in the registry.

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
)

// seedPub reads a seed file and returns its public key.
func seedPub(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}
	kp, err := nkeys.FromSeed([]byte(strings.TrimSpace(string(b))))
	if err != nil {
		t.Fatalf("%s is not a seed: %v", path, err)
	}
	pub, _ := kp.PublicKey()
	return pub
}

// addUser adds a user with a key born here and returns its public key.
func addUser(t *testing.T, h *hubScratch, name, class string, params ...string) string {
	t.Helper()
	args := []string{"user", "add", name, "--class", class}
	for _, p := range params {
		args = append(args, "--param", p)
	}
	code, out, errOut := h.run(args...)
	if code != 0 {
		t.Fatalf("setup: add %s exited %d\nstdout: %s\nstderr: %s", name, code, out, errOut)
	}
	return seedPub(t, filepath.Join(h.keys, name+".nk"))
}

func registryUser(h *hubScratch, name string) map[string]any {
	for _, u := range h.registryUsers() {
		if u["name"] == name {
			return u
		}
	}
	return nil
}

// --- rotate -------------------------------------------------------------------------------------

func TestHubUserRotateKeepsTheOldKeyWorkingUntilFinish(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	oldPub := addUser(t, h, "svc", "director")
	oldSeed := filepath.Join(h.keys, "svc.nk")
	confBefore := string(h.readConf())
	reloads, backups := h.reloads(), len(h.backups())

	code, out, errOut := h.run("user", "rotate", "svc")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	newSeed := filepath.Join(h.keys, "svc.next.nk")
	st, err := os.Stat(newSeed)
	if err != nil {
		t.Fatalf("the new seed is not at %s: %v", newSeed, err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("the new seed mode = %o, want 600", st.Mode().Perm())
	}
	newPub := seedPub(t, newSeed)
	if newPub == oldPub {
		t.Fatalf("rotate reused the old key")
	}
	if !h.canConnect(oldSeed) {
		t.Errorf("the old key is refused while a rotation is pending")
	}
	if !h.canConnect(newSeed) {
		t.Errorf("the new key is refused after rotate")
	}
	region := regionOf(h.readConf())
	if !strings.Contains(region, oldPub) || !strings.Contains(region, newPub) {
		t.Errorf("the region does not hold both keys:\n%s", region)
	}
	if outsideRegion([]byte(confBefore)) != outsideRegion(h.readConf()) {
		t.Errorf("rotate changed bytes outside the managed region")
	}
	u := registryUser(h, "svc")
	if u == nil || u["public_key"] != oldPub || u["pending_key"] != newPub {
		t.Errorf("registry = %v, want public_key %s and pending_key %s", u, oldPub, newPub)
	}
	if !strings.Contains(out, "rotate --finish svc") {
		t.Errorf("the output does not say how to finish: %s", out)
	}
	if !strings.Contains(out, newPub) {
		t.Errorf("the output does not print the new public key")
	}
	if h.reloads() != reloads+1 {
		t.Errorf("reloads = %d, want %d", h.reloads(), reloads+1)
	}
	if len(h.backups()) != backups+1 {
		t.Errorf("backups = %d, want %d", len(h.backups()), backups+1)
	}
	h.assertNoSeedAnywhere(out, errOut)
}

func TestHubUserRotateFinishRefusesTheOldKey(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	oldPub := addUser(t, h, "svc", "director")
	oldSeed := filepath.Join(h.keys, "svc.nk")
	oldSeedBytes, _ := os.ReadFile(oldSeed)
	if code, out, errOut := h.run("user", "rotate", "svc"); code != 0 {
		t.Fatalf("setup: rotate exited %d\n%s%s", code, out, errOut)
	}
	newSeed := filepath.Join(h.keys, "svc.next.nk")
	newPub := seedPub(t, newSeed)

	code, out, errOut := h.run("user", "rotate", "--finish", "svc")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	revoked := h.revokedSeeds()
	if len(revoked) != 1 {
		t.Fatalf("revoked seeds = %v, want the old seed moved there", revoked)
	}
	if b, _ := os.ReadFile(revoked[0]); string(b) != string(oldSeedBytes) {
		t.Errorf("the file in keys/revoked is not the old seed")
	}
	if h.canConnect(revoked[0]) {
		t.Errorf("the old key is still accepted after finish")
	}
	if _, err := os.Stat(newSeed); !os.IsNotExist(err) {
		t.Errorf("keys/svc.next.nk still exists after finish")
	}
	if seedPub(t, oldSeed) != newPub {
		t.Errorf("keys/svc.nk does not hold the new seed")
	}
	if !h.canConnect(oldSeed) {
		t.Errorf("the new key (now keys/svc.nk) is refused")
	}
	region := regionOf(h.readConf())
	if strings.Contains(region, oldPub) || !strings.Contains(region, newPub) {
		t.Errorf("the region should hold only the new key:\n%s", region)
	}
	u := registryUser(h, "svc")
	if u == nil || u["public_key"] != newPub || u["pending_key"] != nil && u["pending_key"] != "" {
		t.Errorf("registry = %v, want public_key %s and no pending_key", u, newPub)
	}
	if u != nil && (u["rotated"] == nil || u["rotated"] == "") {
		t.Errorf("registry has no rotated time: %v", u)
	}
	h.assertNoSeedAnywhere(out, errOut)
}

func TestHubUserRotateRefusesASecondRotationAndAFinishWithNothingPending(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	addUser(t, h, "svc", "director")
	confBefore, regBefore := string(h.readConf()), string(h.readRegistry())
	listing := h.homeListing()
	code, out, errOut := h.run("user", "rotate", "--finish", "svc")
	if code != 1 || !strings.Contains(errOut, "no rotation") {
		t.Errorf("finish with nothing pending: exit = %d, stderr = %q, want 1 and a message naming that no rotation is pending", code, errOut)
	}
	if string(h.readConf()) != confBefore || string(h.readRegistry()) != regBefore || h.homeListing() != listing {
		t.Errorf("a refused finish left something behind")
	}
	if code, out, errOut := h.run("user", "rotate", "svc"); code != 0 {
		t.Fatalf("setup: rotate exited %d\n%s%s", code, out, errOut)
	}
	confBefore, regBefore = string(h.readConf()), string(h.readRegistry())
	listing = h.homeListing()
	code, out, errOut = h.run("user", "rotate", "svc")
	if code != 1 || !strings.Contains(errOut, "--finish") {
		t.Errorf("second rotate: exit = %d, stderr = %q, want 1 and a message naming --finish", code, errOut)
	}
	if string(h.readConf()) != confBefore || string(h.readRegistry()) != regBefore || h.homeListing() != listing {
		t.Errorf("a refused second rotate left something behind\n%s", out)
	}
}

func TestHubUserRotateRollsBackWhenTheNewKeyFailsVerify(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	oldPub := addUser(t, h, "obs", "observer", "holder=director")
	oldSeed := filepath.Join(h.keys, "obs.nk")
	// The grant is now wrong on purpose: it allows every JetStream API subject,
	// so the observer's negatives are not refused and verify must fail.
	old := hubClassFS
	hubClassFS = fstest.MapFS{"observer.conf": {Data: []byte(
		"{ nkey: {{key}}\n  permissions {\n    publish { allow: [ \"$JS.API.>\" ] }\n    subscribe { allow: [ \"_INBOX_observer_{{holder}}.>\" ] }\n  } }\n")}}
	defer func() { hubClassFS = old }()
	confBefore, regBefore := string(h.readConf()), string(h.readRegistry())
	countsBefore := h.consumerCounts()
	reloads := h.reloads()

	code, out, errOut := h.run("user", "rotate", "obs")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if string(h.readConf()) != confBefore {
		t.Errorf("the conf was not restored byte for byte")
	}
	if string(h.readRegistry()) != regBefore {
		t.Errorf("the registry changed")
	}
	if _, err := os.Stat(filepath.Join(h.keys, "obs.next.nk")); !os.IsNotExist(err) {
		t.Errorf("the rolled-back new seed is still at keys/obs.next.nk")
	}
	revoked := h.revokedSeeds()
	if len(revoked) != 1 || h.canConnect(revoked[0]) {
		t.Errorf("the rolled-back new seed is not in keys/revoked or is still accepted: %v", revoked)
	}
	if !h.canConnect(oldSeed) || seedPub(t, oldSeed) != oldPub {
		t.Errorf("the old key no longer works after the rollback")
	}
	if !h.waitReloads(reloads + 2) {
		t.Errorf("the log shows %d reloads, want %d (one forward, one back)", h.reloads(), reloads+2)
	}
	if !strings.Contains(out+errOut, "Rolled back") {
		t.Errorf("the message does not say it rolled back: %s%s", out, errOut)
	}
	if got := h.consumerCounts(); got != countsBefore {
		t.Errorf("consumer counts changed: before\n%safter\n%s", countsBefore, got)
	}
	h.assertNoSeedAnywhere(out, errOut)
}

func TestHubUserRotateAdoptedUserReRendersIntoTheRegion(t *testing.T) {
	pub := newPublicKey(t)
	hand := `      { nkey: ` + pub + `
        permissions {
          publish   { allow: [ "global.probe.>", "global.extra.>" ] }
          subscribe { allow: [ "global.probe.>", "_INBOX.>" ] }
        } }`
	h := newHubScratch(t, hubScratchOpts{extraUsers: hand})
	if code, out, errOut := h.run("user", "adopt", pub, "--name", "probe-leaf", "--class", "leaf", "--param", "cluster=probe"); code != 0 {
		t.Fatalf("setup: adopt exited %d\n%s%s", code, out, errOut)
	}
	confBefore := string(h.readConf())

	code, out, errOut := h.run("user", "rotate", "probe-leaf")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	newSeed := filepath.Join(h.keys, "probe-leaf.next.nk")
	newPub := seedPub(t, newSeed)
	region := regionOf(h.readConf())
	if !strings.Contains(region, newPub) || !strings.Contains(region, "global.probe.>") || strings.Contains(region, "global.extra.>") {
		t.Errorf("the region does not hold the new key with the class grant (and without the hand block's extra grant):\n%s", region)
	}
	if outsideRegion([]byte(confBefore)) != outsideRegion(h.readConf()) {
		t.Errorf("rotate changed the hand-written block or other bytes outside the region")
	}
	u := registryUser(h, "probe-leaf")
	if u == nil || u["public_key"] != pub || u["pending_key"] != newPub {
		t.Errorf("registry = %v, want public_key %s and pending_key %s", u, pub, newPub)
	}

	// The command never edits outside the region, so finish waits for the hand block to go.
	confBefore, regBefore := string(h.readConf()), string(h.readRegistry())
	code, out, errOut = h.run("user", "rotate", "--finish", "probe-leaf")
	if code != 1 || !strings.Contains(errOut, pub) || !strings.Contains(errOut, "outside the managed region") {
		t.Errorf("finish while the hand block is still there: exit = %d, stderr = %q, want 1 naming the old key and that it is outside the managed region", code, errOut)
	}
	if string(h.readConf()) != confBefore || string(h.readRegistry()) != regBefore {
		t.Errorf("a refused finish changed the conf or the registry")
	}
	removed := strings.Replace(string(h.readConf()), hand, "", 1)
	if removed == string(h.readConf()) {
		t.Fatalf("setup: the hand block was not found in the conf")
	}
	if err := os.WriteFile(h.conf, []byte(removed), 0o600); err != nil {
		t.Fatal(err)
	}
	reloads := h.reloads()
	if err := syscall.Kill(h.pid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	if !h.waitReloads(reloads + 1) {
		t.Fatalf("setup: the hub did not reload after the hand block was removed")
	}
	code, out, errOut = h.run("user", "rotate", "--finish", "probe-leaf")
	if code != 0 {
		t.Fatalf("finish after the hand block was removed: exit = %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	u = registryUser(h, "probe-leaf")
	if u == nil || u["public_key"] != newPub || u["state"] != "active" || u["location"] != "managed region" {
		t.Errorf("registry = %v, want the new key, state active, in the managed region", u)
	}
	if !h.canConnect(filepath.Join(h.keys, "probe-leaf.nk")) {
		t.Errorf("the new key is refused after finish")
	}
	h.assertNoSeedAnywhere(out, errOut)
}

// --- revoke -------------------------------------------------------------------------------------

func TestHubUserRevokeRefusesTheOldKeyAfterReload(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	pub := addUser(t, h, "svc", "director")
	seed := filepath.Join(h.keys, "svc.nk")
	seedBytes, _ := os.ReadFile(seed)
	// An open connection of the user, to measure what the reload does to it.
	nc, err := h.connectAs(seed, "")
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	reloads, backups := h.reloads(), len(h.backups())

	code, out, errOut := h.run("user", "revoke", "svc")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	revoked := h.revokedSeeds()
	if len(revoked) != 1 {
		t.Fatalf("revoked seeds = %v, want the seed moved there", revoked)
	}
	if b, _ := os.ReadFile(revoked[0]); string(b) != string(seedBytes) {
		t.Errorf("the file in keys/revoked is not the seed")
	}
	if _, err := os.Stat(seed); !os.IsNotExist(err) {
		t.Errorf("keys/svc.nk still exists")
	}
	if h.canConnect(revoked[0]) {
		t.Errorf("the revoked key is still accepted")
	}
	if strings.Contains(regionOf(h.readConf()), pub) {
		t.Errorf("the region still holds the revoked key")
	}
	u := registryUser(h, "svc")
	if u == nil || u["state"] != "revoked" || u["revoked"] == nil || u["revoked"] == "" || u["public_key"] != pub {
		t.Errorf("registry = %v, want state revoked, a revoked time and the key kept", u)
	}
	if h.reloads() != reloads+1 || len(h.backups()) != backups+1 {
		t.Errorf("reloads = %d (want %d), backups = %d (want %d)", h.reloads(), reloads+1, len(h.backups()), backups+1)
	}
	// The design left open whether a reload closes the removed user's open
	// connection. The command reports what it measured, and the report is true.
	closed := false
	for i := 0; i < 30 && !closed; i++ {
		closed = nc.IsClosed() || nc.Status() != nats.CONNECTED
		if !closed {
			time.Sleep(100 * time.Millisecond)
		}
	}
	t.Logf("measured: an open connection of the removed user was closed by the reload: %v", closed)
	if closed && !strings.Contains(out, "1 open connection closed by the reload") {
		t.Errorf("the connection was closed and the output does not say so:\n%s", out)
	}
	if !closed && !strings.Contains(out, "1 open connection still open") {
		t.Errorf("the connection is still open and the output does not say so:\n%s", out)
	}
	h.assertNoSeedAnywhere(out, errOut)
}

func TestHubUserRevokeRemovesBothKeysOfARotatingUser(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	oldPub := addUser(t, h, "svc", "director")
	if code, out, errOut := h.run("user", "rotate", "svc"); code != 0 {
		t.Fatalf("setup: rotate exited %d\n%s%s", code, out, errOut)
	}
	newSeed := filepath.Join(h.keys, "svc.next.nk")
	newPub := seedPub(t, newSeed)
	code, out, errOut := h.run("user", "revoke", "svc")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	region := regionOf(h.readConf())
	if strings.Contains(region, oldPub) || strings.Contains(region, newPub) {
		t.Errorf("the region still holds a key of the revoked user:\n%s", region)
	}
	if got := h.revokedSeeds(); len(got) != 2 {
		t.Errorf("revoked seeds = %v, want both seeds", got)
	}
	for _, p := range h.revokedSeeds() {
		if h.canConnect(p) {
			t.Errorf("%s is still accepted", filepath.Base(p))
		}
	}
	if len(h.seedFiles()) != 1 {
		t.Errorf("keys still hold %v, want only admin.nk", h.seedFiles())
	}
	h.assertNoSeedAnywhere(out, errOut)
}

func TestHubUserRevokeRollsBackWhenTheOldKeyIsStillAccepted(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	addUser(t, h, "svc", "director")
	seed := filepath.Join(h.keys, "svc.nk")
	confBefore, regBefore := string(h.readConf()), string(h.readRegistry())
	// After the command's reload, put the user back and reload again, so the old
	// key is accepted when verify checks that it is refused.
	hubAfterReload = func() {
		n := h.reloads()
		if err := os.WriteFile(h.conf, []byte(confBefore), 0o600); err != nil {
			t.Error(err)
			return
		}
		_ = syscall.Kill(h.pid(), syscall.SIGHUP)
		h.waitReloads(n + 1)
	}
	defer func() { hubAfterReload = nil }()

	code, out, errOut := h.run("user", "revoke", "svc")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(out+errOut, "Rolled back") {
		t.Errorf("the message does not say it rolled back: %s%s", out, errOut)
	}
	if string(h.readConf()) != confBefore || string(h.readRegistry()) != regBefore {
		t.Errorf("the conf or the registry is not as before")
	}
	if _, err := os.Stat(seed); err != nil {
		t.Errorf("the seed is no longer at keys/svc.nk: %v", err)
	}
	if len(h.revokedSeeds()) != 0 {
		t.Errorf("a seed was moved to keys/revoked although the revoke did not complete: %v", h.revokedSeeds())
	}
	if !h.canConnect(seed) {
		t.Errorf("the user cannot connect after the rollback")
	}
}

func TestHubUserRevokeRefusesTheLastAdmin(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	addUser(t, h, "admin-a", "admin")
	confBefore, regBefore := string(h.readConf()), string(h.readRegistry())
	listing := h.homeListing()
	code, _, errOut := h.run("user", "revoke", "admin-a")
	if code != 1 || !strings.Contains(errOut, "last admin") {
		t.Errorf("exit = %d, stderr = %q, want 1 and a message naming the last admin", code, errOut)
	}
	if string(h.readConf()) != confBefore || string(h.readRegistry()) != regBefore || h.homeListing() != listing {
		t.Errorf("a refused revoke left something behind")
	}
	addUser(t, h, "admin-b", "admin")
	if code, out, errOut := h.run("user", "revoke", "admin-a"); code != 0 {
		t.Errorf("with a second admin, revoke exited %d\n%s%s", code, out, errOut)
	}
	if code, _, errOut := h.run("user", "revoke", "admin-b"); code != 1 || !strings.Contains(errOut, "last admin") {
		t.Errorf("revoking the remaining admin: exit = %d, stderr = %q, want 1 and the last-admin message", code, errOut)
	}
}

func TestHubUserRevokeRefusesAUserOutsideTheRegionAndUnknownNames(t *testing.T) {
	h, pub := adoptFixture(t)
	if code, out, errOut := h.run("user", "adopt", pub, "--name", "probe-leaf", "--class", "leaf", "--param", "cluster=probe"); code != 0 {
		t.Fatalf("setup: adopt exited %d\n%s%s", code, out, errOut)
	}
	confBefore, regBefore := string(h.readConf()), string(h.readRegistry())
	listing := h.homeListing()
	code, _, errOut := h.run("user", "revoke", "probe-leaf")
	if code != 1 || !strings.Contains(errOut, "outside the managed region") || !strings.Contains(errOut, "Nothing was changed") {
		t.Errorf("revoke of an adopted user: exit = %d, stderr = %q, want 1, outside the managed region, nothing changed", code, errOut)
	}
	code, _, errOut = h.run("user", "revoke", "nobody")
	if code != 1 || !strings.Contains(errOut, "nobody") {
		t.Errorf("revoke of an unknown name: exit = %d, stderr = %q, want 1 naming it", code, errOut)
	}
	code, _, errOut = h.run("user", "rotate", "nobody")
	if code != 1 || !strings.Contains(errOut, "nobody") {
		t.Errorf("rotate of an unknown name: exit = %d, stderr = %q, want 1 naming it", code, errOut)
	}
	if string(h.readConf()) != confBefore || string(h.readRegistry()) != regBefore || h.homeListing() != listing {
		t.Errorf("a refused command left something behind")
	}
}

func TestHubUserRevokeRefusesAUserAlreadyRevokedAndOneThatDriftedOutOfTheConf(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	addUser(t, h, "svc", "director")
	addUser(t, h, "gone", "director")
	if code, out, errOut := h.run("user", "revoke", "svc"); code != 0 {
		t.Fatalf("setup: revoke exited %d\n%s%s", code, out, errOut)
	}
	if code, _, errOut := h.run("user", "revoke", "svc"); code != 1 || !strings.Contains(errOut, "already revoked") {
		t.Errorf("second revoke: exit = %d, stderr = %q, want 1 and already revoked", code, errOut)
	}
	if code, _, errOut := h.run("user", "rotate", "svc"); code != 1 || !strings.Contains(errOut, "revoked") {
		t.Errorf("rotate of a revoked user: exit = %d, stderr = %q, want 1 and revoked", code, errOut)
	}
	// "gone" is in the registry and its block is removed by hand, so the registry and the conf disagree.
	s := string(h.readConf())
	i, j := strings.Index(s, hubRegionBegin), strings.Index(s, hubRegionEnd)
	empty := s[:i+len(hubRegionBegin)] + "\n      " + s[j:]
	if err := os.WriteFile(h.conf, []byte(empty), 0o600); err != nil {
		t.Fatal(err)
	}
	confBefore, regBefore := string(h.readConf()), string(h.readRegistry())
	code, _, errOut := h.run("user", "revoke", "gone")
	if code != 1 || !strings.Contains(errOut, "not in the conf") {
		t.Errorf("revoke of a user missing from the conf: exit = %d, stderr = %q, want 1 and not in the conf", code, errOut)
	}
	if string(h.readConf()) != confBefore || string(h.readRegistry()) != regBefore {
		t.Errorf("a refused revoke changed the conf or the registry")
	}
}

// --- shared behavior ----------------------------------------------------------------------------

func TestHubUserRotateAndRevokeTakeTheLock(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	addUser(t, h, "svc", "director")
	confBefore, regBefore := string(h.readConf()), string(h.readRegistry())
	f, err := os.OpenFile(filepath.Join(h.home, ".hub-user.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"user", "rotate", "svc"}, {"user", "rotate", "--finish", "svc"}, {"user", "revoke", "svc"}} {
		code, _, errOut := h.run(args...)
		if code != 1 || !strings.Contains(errOut, "another hub command holds") {
			t.Errorf("%v with the lock held: exit = %d, stderr = %q, want 1 and the lock message", args, code, errOut)
		}
	}
	if string(h.readConf()) != confBefore || string(h.readRegistry()) != regBefore {
		t.Errorf("a refused command changed the conf or the registry")
	}
}

func TestHubUserRotateAndRevokeUsage(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	addUser(t, h, "svc", "director")
	listing := h.homeListing()
	for _, args := range [][]string{
		{"user", "rotate"},
		{"user", "rotate", "--finish"},
		{"user", "rotate", "a", "b"},
		{"user", "rotate", "svc", "--class", "admin"},
		{"user", "revoke"},
		{"user", "revoke", "a", "b"},
		{"user", "revoke", "svc", "--finish"},
	} {
		code, _, errOut := h.run(args...)
		if code != 2 || !strings.Contains(errOut, "usage") {
			t.Errorf("%v: exit = %d, stderr = %q, want 2 and usage", args, code, errOut)
		}
		if !strings.Contains(errOut, "director hub user rotate [--finish] NAME") || !strings.Contains(errOut, "director hub user revoke NAME") {
			t.Errorf("%v: the usage text does not show the rotate and revoke lines: %q", args, errOut)
		}
	}
	if h.homeListing() != listing {
		t.Errorf("a usage error left something behind")
	}
}

func TestHubUserListShowsARotationAndARevocation(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	oldPub := addUser(t, h, "svc", "director")
	addUser(t, h, "old", "director")
	if code, out, errOut := h.run("user", "rotate", "svc"); code != 0 {
		t.Fatalf("setup: rotate exited %d\n%s%s", code, out, errOut)
	}
	newPub := seedPub(t, filepath.Join(h.keys, "svc.next.nk"))
	if code, out, errOut := h.run("user", "revoke", "old"); code != 0 {
		t.Fatalf("setup: revoke exited %d\n%s%s", code, out, errOut)
	}
	code, out, errOut := h.run("user", "list")
	if code != 0 {
		t.Fatalf("list exit = %d\n%s", code, errOut)
	}
	var svcRow, oldRow string
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(l, "svc "):
			svcRow = l
		case strings.HasPrefix(l, "old "):
			oldRow = l
		}
	}
	if !strings.Contains(svcRow, "rotating") || !strings.Contains(svcRow, oldPub) || !strings.Contains(svcRow, newPub) {
		t.Errorf("the svc row does not show a rotation with both keys: %q", svcRow)
	}
	if !strings.Contains(oldRow, "revoked") {
		t.Errorf("the old row does not show revoked: %q", oldRow)
	}
	if strings.Contains(out, "DRIFT") {
		t.Errorf("a rotation and a revocation are reported as drift:\n%s", out)
	}
}
