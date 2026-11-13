package main

// Red tests for `director hub user add|list|adopt` and `director hub settings
// check|init` (sim/design/hub-user-lifecycle.md). Each test starts its own
// scratch nats-server (hubuser_harness_test.go) and runs the command in
// process against it. The two failures from the first live run of the I-469
// script come first: a missing tool, and a conf that fails the server test for a
// reason the check cannot see. Both must stop before any backup, any key or any
// registry write, and the tests compare the whole hub home before and after.
//
// Behavior that depends on nats-server (a settings variable beating the
// environment, an unreferenced settings variable being rejected) was measured on
// v2.14.6, the version on the machine that wrote these tests. CI pins another
// version; these tests report what that version does.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/fstest"
	"time"
)

func cctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 20*time.Second)
}

var serverParseText = regexp.MustCompile(`(?i)parse error|is invalid|unknown field|variable reference`)

// --- the two failures from the first live run ---------------------------------------------------

func TestHubUserRefusesWhenNatsServerIsMissing(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	confBefore, listing := string(h.readConf()), h.homeListing()
	t.Setenv("PATH", t.TempDir())

	code, out, errOut := h.run("user", "add", "obs", "--class", "observer", "--param", "holder=director")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	msg := out + errOut
	if !strings.Contains(msg, "nats-server not found on PATH") {
		t.Errorf("the message does not say nats-server is not found on PATH: %q", msg)
	}
	if !strings.Contains(msg, "https://") {
		t.Errorf("the message does not say where to get nats-server: %q", msg)
	}
	if string(h.readConf()) != confBefore || h.homeListing() != listing {
		t.Errorf("the hub home changed: before\n%s\nafter\n%s", listing, h.homeListing())
	}
	if len(h.seedFiles()) != 1 || h.readRegistry() != nil {
		t.Errorf("a key or the registry was written")
	}
}

func TestHubUserRefusesWhenTheUnchangedConfFailsTheServerTest(t *testing.T) {
	// The conf references $HUB_NAME and no settings.conf defines it, so a check
	// run from this shell cannot see what the running server sees.
	h := newHubScratch(t, hubScratchOpts{noSettings: true})
	confBefore, listing := string(h.readConf()), h.homeListing()

	code, out, errOut := h.run("user", "add", "obs", "--class", "observer", "--param", "holder=director")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	msg := out + errOut
	if !strings.Contains(msg, "HUB_NAME") {
		t.Errorf("the message does not name the variable HUB_NAME: %q", msg)
	}
	if !strings.Contains(msg, "cannot see the hub's own configuration") {
		t.Errorf("the message does not say the check cannot see the hub's own configuration: %q", msg)
	}
	if string(h.readConf()) != confBefore || h.homeListing() != listing {
		t.Errorf("the hub home changed: before\n%s\nafter\n%s", listing, h.homeListing())
	}
	if len(h.backups()) != 0 || len(h.seedFiles()) != 1 || h.readRegistry() != nil {
		t.Errorf("a backup, key or registry was written")
	}
}

func TestHubUserRefusesWhenTheUnchangedConfFailsForAnotherReason(t *testing.T) {
	// No variable is missing: the live conf has a syntax error. A scan for $NAME
	// references passes it; only the server test (run on the conf as it stands,
	// before anything is written) stops it.
	h := newHubScratch(t, hubScratchOpts{brokenConf: true})
	confBefore, listing := string(h.readConf()), h.homeListing()

	code, out, errOut := h.run("user", "add", "obs", "--class", "observer", "--param", "holder=director")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	msg := out + errOut
	if !strings.Contains(msg, "nats-server -t") || !serverParseText.MatchString(msg) {
		t.Errorf("the message does not carry nats-server -t and the server's own parse error: %q", msg)
	}
	if string(h.readConf()) != confBefore || h.homeListing() != listing {
		t.Errorf("the hub home changed: before\n%s\nafter\n%s", listing, h.homeListing())
	}
	if len(h.backups()) != 0 || len(h.seedFiles()) != 1 || h.readRegistry() != nil {
		t.Errorf("a backup, key or registry was written")
	}
}

func TestHubUserRefusesWhileASettingsReferenceIsUndefined(t *testing.T) {
	// The running server got HUB_TAG from its environment; a check run from this
	// shell did not, and settings.conf does not define it.
	h := newHubScratch(t, hubScratchOpts{refTag: true, serverEnv: []string{"HUB_TAG=from-env"}})
	confBefore, listing := string(h.readConf()), h.homeListing()
	code, out, errOut := h.run("user", "add", "obs", "--class", "observer", "--param", "holder=director")
	if code != 1 || !strings.Contains(out+errOut, "HUB_TAG") {
		t.Fatalf("exit = %d, want 1 naming HUB_TAG\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if string(h.readConf()) != confBefore || h.homeListing() != listing {
		t.Errorf("the hub home changed: before\n%s\nafter\n%s", listing, h.homeListing())
	}
	if len(h.backups()) != 0 || len(h.seedFiles()) != 1 || h.readRegistry() != nil {
		t.Errorf("a backup, key or registry was written")
	}
}

// --- settings.conf (option B) -------------------------------------------------------------------

func TestHubConfVariableResolvesFromTheSettingsInclude(t *testing.T) {
	// The server is started with HUB_NAME=from-env in its environment and
	// settings.conf says from-settings. On nats-server v2.14.6 the conf variable
	// wins, for the server and for -t alike. This test reports what the version
	// on this host does and that `settings check` agrees the name is defined.
	h := newHubScratch(t, hubScratchOpts{serverEnv: []string{"HUB_NAME=from-env"}})
	if got := h.serverName(); got != "from-settings" {
		t.Fatalf("the serving hub's name = %q, want from-settings (the conf variable beats the environment)", got)
	}
	code, out, errOut := h.run("settings", "check")
	if code != 0 {
		t.Fatalf("settings check exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "defined HUB_NAME") {
		t.Errorf("settings check does not report HUB_NAME as defined: %q", out)
	}
}

func TestHubSettingsCheckListsUndefinedReferences(t *testing.T) {
	// Subjects such as "$JS.API.INFO" are quoted strings, not references.
	extra := `      { nkey: ` + newPublicKey(t) + `
        permissions { publish { allow: [ "$JS.API.INFO", "$KV.>" ] } } }`
	h := newHubScratch(t, hubScratchOpts{noSettings: true, extraUsers: extra})
	code, out, errOut := h.run("settings", "check")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 while a reference is undefined\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "UNDEFINED HUB_NAME") {
		t.Errorf("HUB_NAME is not listed as undefined: %q", out)
	}
	if strings.Contains(out, "$JS") || strings.Contains(out, "$KV") || strings.Contains(out, "JS.API") {
		t.Errorf("a quoted subject was read as a reference: %q", out)
	}
}

func TestHubSettingsCheckListsUnreferencedNames(t *testing.T) {
	// A variable in an included file that the conf never references stops the
	// server and the -t check with `unknown field` (v2.14.6). So a settings.conf
	// entry the conf does not use is an error, not a harmless extra.
	h := newHubScratch(t, hubScratchOpts{lateSettings: "HUB_SPARE: \"b\"\n"})
	code, out, errOut := h.run("settings", "check")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "UNREFERENCED HUB_SPARE") {
		t.Errorf("HUB_SPARE is not listed as unreferenced: %q", out)
	}
	if !strings.Contains(out, "defined HUB_NAME") {
		t.Errorf("HUB_NAME is not reported as defined: %q", out)
	}
}

func TestHubSettingsInitRefusesAnUnreferencedName(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{noSettings: true, noRegion: true})
	confBefore, listing := string(h.readConf()), h.homeListing()
	code, out, errOut := h.run("settings", "init", "--set", "HUB_NAME=a", "--set", "HUB_SPARE=b")
	if code != 1 || !strings.Contains(out+errOut, "HUB_SPARE") {
		t.Errorf("exit = %d, want 1 naming HUB_SPARE\n%s%s", code, out, errOut)
	}
	if string(h.readConf()) != confBefore || h.homeListing() != listing {
		t.Errorf("init changed the hub home before refusing")
	}
}

func TestHubSettingsInitRefusesWhileAReferenceHasNoValue(t *testing.T) {
	// The conf references $HUB_NAME and $HUB_TAG; the input gives only HUB_NAME.
	// Init would leave a hub whose check cannot see HUB_TAG, so it names the
	// missing value and refuses before any write.
	h := newHubScratch(t, hubScratchOpts{noSettings: true, noRegion: true, refTag: true, serverEnv: []string{"HUB_TAG=from-env"}})
	confBefore, listing := string(h.readConf()), h.homeListing()
	code, out, errOut := h.run("settings", "init", "--set", "HUB_NAME=hub")
	if code != 1 || !strings.Contains(out+errOut, "HUB_TAG") {
		t.Errorf("exit = %d, want 1 naming HUB_TAG\n%s%s", code, out, errOut)
	}
	if string(h.readConf()) != confBefore || h.homeListing() != listing {
		t.Errorf("init changed the hub home before refusing: before\n%s\nafter\n%s", listing, h.homeListing())
	}
	if _, err := os.Stat(h.settings); err == nil {
		t.Errorf("settings.conf was written")
	}
}

func TestHubSettingsInitMovesAnOldHubToTheIncludeAndRegion(t *testing.T) {
	// An old-style conf: no include, no managed region, one variable reference.
	h := newHubScratch(t, hubScratchOpts{noSettings: true, noRegion: true})
	confBefore := string(h.readConf())
	pid, reloads := h.pid(), h.reloads()

	code, out, errOut := h.run("settings", "init", "--set", "HUB_NAME=hub")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	conf := string(h.readConf())
	if !strings.HasPrefix(conf, `include "./settings.conf"`) {
		t.Errorf("the conf does not start with the include: %q", conf[:min(60, len(conf))])
	}
	if !strings.Contains(conf, hubRegionBegin) || !strings.Contains(conf, hubRegionEnd) {
		t.Errorf("the conf has no managed region after init")
	}
	st, err := os.Stat(h.settings)
	if err != nil {
		t.Fatalf("settings.conf was not written: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("settings.conf mode = %v, want 0600", st.Mode().Perm())
	}
	b, _ := os.ReadFile(h.settings)
	if !strings.Contains(string(b), "HUB_NAME") || !strings.Contains(string(b), "hub") {
		t.Errorf("settings.conf does not define HUB_NAME as hub: %q", b)
	}
	if got := h.backups(); len(got) != 1 {
		t.Errorf("backups = %v, want one", got)
	} else if bb, _ := os.ReadFile(got[0]); string(bb) != confBefore {
		t.Errorf("the backup is not the conf as it was")
	}
	if !strings.Contains(out, "reloaded") {
		t.Errorf("the output does not say the hub was reloaded:\n%s", out)
	}
	if !h.waitReloads(reloads + 1) {
		t.Errorf("the hub log shows no new reload (%d before)", reloads)
	}
	if h.pid() != pid {
		t.Errorf("the server pid changed: a reload must not restart it")
	}
	if code, _, _ := h.run("settings", "check"); code != 0 {
		t.Errorf("settings check after init exits %d, want 0", code)
	}
	// The reload count above proves a reload happened. This add is a further
	// check that the hub runs from the conf init wrote: it needs the region and
	// the include init put there, and its own verify connects as the new key.
	if code, out, errOut := h.run("user", "add", "probe", "--class", "observer", "--param", "holder=director"); code != 0 {
		t.Errorf("an add after init exits %d: the new conf is not what the hub runs\nstdout: %s\nstderr: %s", code, out, errOut)
	}
}

func TestHubSettingsInitRefusesAServerNameThatDiffersFromTheRunningOne(t *testing.T) {
	// nats-server 2.14.6 refuses a server_name change on reload ("config reload not
	// supported for ServerName: old=one, new=two") and keeps the old name. Init
	// moves values and never changes one, so it refuses up front, before any write.
	h := newHubScratch(t, hubScratchOpts{noSettings: true, noRegion: true})
	confBefore, listing := string(h.readConf()), h.homeListing()
	reloads := h.reloads()
	code, out, errOut := h.run("settings", "init", "--set", "HUB_NAME=zed")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	msg := out + errOut
	if !strings.Contains(msg, "server_name") || !strings.Contains(msg, `"hub"`) || !strings.Contains(msg, `"zed"`) {
		t.Errorf("the message does not name server_name with the running value \"hub\" and the given value \"zed\" quoted: %q", msg)
	}
	if string(h.readConf()) != confBefore || h.homeListing() != listing || len(h.backups()) != 0 {
		t.Errorf("the hub home changed: before\n%s\nafter\n%s", listing, h.homeListing())
	}
	if _, err := os.Stat(h.settings); err == nil {
		t.Errorf("settings.conf was written")
	}
	if h.reloads() != reloads {
		t.Errorf("the hub was reloaded (%d before, %d now); a refusal comes before any reload", reloads, h.reloads())
	}
	if got := h.serverName(); got != "hub" {
		t.Errorf("the running server_name = %q, want hub", got)
	}
}

func TestHubSettingsInitRollsBackWhenTheServerRefusesTheReload(t *testing.T) {
	// A refused reload keeps the old config, so "the hub answers and nothing
	// dropped" is true of it. nats-server refuses a changed jetstream store_dir
	// ("config reload not supported for jetstream storage directory"), and init
	// must see that the new conf did not take, restore, and say so.
	h := newHubScratch(t, hubScratchOpts{noSettings: true, noRegion: true, jsVar: true})
	confBefore, listing := string(h.readConf()), h.homeListing()
	code, out, errOut := h.run("settings", "init", "--set", "HUB_NAME=hub", "--set", "JS_DIR="+filepath.Join(h.home, "js2"))
	msg := out + errOut
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (the server refused the reload)\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if strings.Contains(msg, "ok, the hub was reloaded") {
		t.Errorf("init reports a reload that the server refused: %q", msg)
	}
	if !strings.Contains(msg, "reload") || !strings.Contains(msg, "restored") {
		t.Errorf("the message does not say the reload did not take and the conf was restored: %q", msg)
	}
	if string(h.readConf()) != confBefore {
		t.Errorf("the conf was not restored byte for byte")
	}
	if _, err := os.Stat(h.settings); err == nil {
		t.Errorf("settings.conf is still there; the next restart would open an empty store")
	}
	if got := h.backups(); len(got) != 1 || !strings.Contains(msg, filepath.Base(got[0])) {
		t.Errorf("the message does not name the one backup: %v\n%s", got, msg)
	}
	_ = listing
}

func TestHubUserAddRefusesAPidFileThatIsNotThisHub(t *testing.T) {
	// A stale pid file can name any process. The command signals a pid only when
	// its command line shows nats-server running this conf.
	h := newHubScratch(t, hubScratchOpts{})
	other := exec.Command("sleep", "60")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = other.Wait(); close(exited) }()
	t.Cleanup(func() { _ = other.Process.Kill() })
	if err := os.WriteFile(h.pidfile, []byte(fmt.Sprintf("%d\n", other.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	confBefore, listing := string(h.readConf()), h.homeListing()
	code, out, errOut := h.run("user", "add", "obs", "--class", "observer", "--param", "holder=director")
	if code != 1 {
		t.Errorf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	select {
	case <-exited:
		t.Errorf("the command signalled an unrelated process named by the pid file")
	case <-time.After(time.Second):
	}
	if !strings.Contains(out+errOut, "nats-server") || !strings.Contains(out+errOut, "pid") {
		t.Errorf("the message does not say the pid is not this hub's nats-server: %q", out+errOut)
	}
	if string(h.readConf()) != confBefore || h.homeListing() != listing || h.readRegistry() != nil {
		t.Errorf("something changed: the refusal comes before any write")
	}
}

func TestHubSettingsInitRefusesWhenTheRunningValueCannotBeRead(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{noSettings: true, noRegion: true, badMonitor: true})
	confBefore, listing := string(h.readConf()), h.homeListing()
	code, out, errOut := h.run("settings", "init", "--set", "HUB_NAME=hub")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(out+errOut, "/varz") || !strings.Contains(out+errOut, "server_name") {
		t.Errorf("the message does not say /varz could not be read for server_name: %q", out+errOut)
	}
	if string(h.readConf()) != confBefore || h.homeListing() != listing || len(h.backups()) != 0 {
		t.Errorf("the hub home changed after the refusal")
	}
}

func TestHubSettingsInitRefusesASecondRun(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{noSettings: true, noRegion: true})
	if code, out, errOut := h.run("settings", "init", "--set", "HUB_NAME=hub"); code != 0 {
		t.Fatalf("first init exit = %d\n%s%s", code, out, errOut)
	}
	confAfter, listing := string(h.readConf()), h.homeListing()
	settingsAfter, _ := os.ReadFile(h.settings)
	code, out, errOut := h.run("settings", "init", "--set", "HUB_NAME=other")
	if code != 1 {
		t.Errorf("a second init exits %d, want 1\n%s%s", code, out, errOut)
	}
	if !strings.Contains(out+errOut, "settings.conf") || !strings.Contains(out+errOut, "already") {
		t.Errorf("the message does not say settings.conf already exists: %q", out+errOut)
	}
	if string(h.readConf()) != confAfter || h.homeListing() != listing {
		t.Errorf("a refused second init changed the hub home")
	}
	if b, _ := os.ReadFile(h.settings); string(b) != string(settingsAfter) {
		t.Errorf("settings.conf changed under the second init: before %q, after %q", settingsAfter, b)
	}
}

func TestHubSettingsInitWritesNothingWhenTheCandidateFailsTheServerTest(t *testing.T) {
	// The unchanged conf fails the server test by design on an old hub (it
	// references a variable the shell lacks), so the candidate test is the guard:
	// here the conf also has a syntax error, and the candidate must fail -t before
	// anything is written.
	h := newHubScratch(t, hubScratchOpts{noSettings: true, noRegion: true, brokenConf: true})
	confBefore, listing := string(h.readConf()), h.homeListing()
	code, out, errOut := h.run("settings", "init", "--set", "HUB_NAME=hub")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(out+errOut, "nats-server -t") || !serverParseText.MatchString(out+errOut) {
		t.Errorf("the message does not carry nats-server -t and the server's parse error: %q", out+errOut)
	}
	if string(h.readConf()) != confBefore || h.homeListing() != listing {
		t.Errorf("the hub home changed: before\n%s\nafter\n%s", listing, h.homeListing())
	}
	if _, err := os.Stat(h.settings); err == nil {
		t.Errorf("settings.conf was written before the candidate passed")
	}
	if len(h.backups()) != 0 {
		t.Errorf("a backup was written before the candidate passed")
	}
}

func TestHubSettingsInitRestoresWhenTheReloadDropsALeaf(t *testing.T) {
	// After the reload the command checks that the server answers and that every
	// connection it saw before is still there. The test closes one connection in
	// the window between the reload and that check, as a leaf dropped by a bad
	// grant would be, and expects the conf restored.
	h := newHubScratch(t, hubScratchOpts{noSettings: true, noRegion: true})
	keep, err := h.connectAs(h.adminSeed, "")
	if err != nil {
		t.Fatal(err)
	}
	defer keep.Close()
	hubAfterReload = func() { keep.Close() }
	defer func() { hubAfterReload = nil }()
	confBefore := string(h.readConf())
	reloads := h.reloads()

	code, out, errOut := h.run("settings", "init", "--set", "HUB_NAME=hub")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	msg := out + errOut
	if !strings.Contains(msg, "dropped") || !strings.Contains(msg, "restored") {
		t.Errorf("the message does not say a connection dropped and the conf was restored: %q", msg)
	}
	if string(h.readConf()) != confBefore {
		t.Errorf("the conf was not restored byte for byte")
	}
	if _, err := os.Stat(h.settings); err == nil {
		t.Errorf("settings.conf is still there after the restore")
	}
	if got := h.backups(); len(got) != 1 || !strings.Contains(msg, filepath.Base(got[0])) {
		t.Errorf("the message does not name the backup it restored from: %v\n%s", got, msg)
	}
	if !h.waitReloads(reloads + 2) {
		t.Errorf("the log shows %d reloads, want %d (one forward, one back)", h.reloads(), reloads+2)
	}
}

// --- add ------------------------------------------------------------------------------------------

func TestHubUserAddMintsASeedThatIsNeverPrinted(t *testing.T) {
	old := syscall.Umask(0) // the key's mode must not follow the umask
	defer syscall.Umask(old)
	h := newHubScratch(t, hubScratchOpts{})
	pid, reloads := h.pid(), h.reloads()

	code, out, errOut := h.run("user", "add", "obs", "--class", "observer", "--param", "holder=director")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	users := h.registryUsers()
	if len(users) != 1 {
		t.Fatalf("registry users = %v, want the one new user", users)
	}
	pub, _ := users[0]["public_key"].(string)
	if !strings.HasPrefix(pub, "U") || !strings.Contains(out, pub) {
		t.Errorf("the public key %q is not printed: %q", pub, out)
	}
	h.assertNoSeedAnywhere(out, errOut)
	seed := filepath.Join(h.keys, "obs.nk")
	st, err := os.Stat(seed)
	if err != nil {
		t.Fatalf("the seed is not at keys/obs.nk: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("seed mode = %v, want 0600 under umask 0", st.Mode().Perm())
	}
	if got := h.seedFiles(); len(got) != 2 {
		t.Errorf("keys/ holds %v, want admin.nk and obs.nk only", got)
	}
	if ds, _ := os.Stat(h.keys); ds.Mode().Perm()&0o077 != 0 {
		t.Errorf("keys directory mode = %v, want no group or other access", ds.Mode().Perm())
	}
	if !h.canConnect(seed) {
		t.Errorf("the new key is not accepted by the reloaded hub")
	}
	if h.pid() != pid {
		t.Errorf("the server pid changed: a reload must not restart it")
	}
	if !h.waitReloads(reloads + 1) {
		t.Errorf("the hub log shows no new reload (%d before)", reloads)
	}
}

func TestHubUserAddCreatesAKeysDirectoryThatOnlyTheOwnerCanRead(t *testing.T) {
	// The admin key lives elsewhere, so keys/ does not exist until the command
	// makes it. Under umask 0 a directory made with the default mode would be
	// open to everyone.
	old := syscall.Umask(0)
	defer syscall.Umask(old)
	h := newHubScratch(t, hubScratchOpts{adminOutside: true})
	if _, err := os.Stat(h.keys); err == nil {
		t.Fatalf("the harness left a keys directory")
	}
	code, out, errOut := h.run("user", "add", "obs", "--class", "observer", "--param", "holder=director")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	ds, err := os.Stat(h.keys)
	if err != nil {
		t.Fatalf("keys/ was not created: %v", err)
	}
	if ds.Mode().Perm() != 0o700 {
		t.Errorf("keys directory mode = %v, want 0700", ds.Mode().Perm())
	}
	if st, err := os.Stat(filepath.Join(h.keys, "obs.nk")); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("seed missing or not 0600: %v %v", st, err)
	}
}

func TestWriteFileExclRefusesAnExistingNameAndSetsTheModeUnderAnyUmask(t *testing.T) {
	old := syscall.Umask(0)
	defer syscall.Umask(old)
	dir := t.TempDir()
	p := filepath.Join(dir, "a")
	if err := writeFileExcl(p, []byte("one\n"), 0o600); err != nil {
		t.Fatalf("a new name was refused: %v", err)
	}
	if st, err := os.Stat(p); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v (%v), want 0600 under umask 0", st, err)
	}
	if err := writeFileExcl(p, []byte("two\n"), 0o600); err == nil {
		t.Errorf("an existing file was overwritten")
	}
	if b, _ := os.ReadFile(p); string(b) != "one\n" {
		t.Errorf("the existing file now holds %q", b)
	}
	// O_EXCL refuses a name that is a dangling symlink; a plain create would
	// follow it and make the target.
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := writeFileExcl(link, []byte("x\n"), 0o600); err == nil {
		t.Errorf("a dangling symlink was followed")
	}
	if _, err := os.Stat(target); err == nil {
		t.Errorf("the symlink's target was created")
	}
}

func TestHubUserAddNeverOverwritesAnExistingSeed(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	seed := filepath.Join(h.keys, "obs.nk")
	if err := os.WriteFile(seed, []byte("keep me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	confBefore, listing := string(h.readConf()), h.homeListing()
	code, out, errOut := h.run("user", "add", "obs", "--class", "observer", "--param", "holder=director")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if b, _ := os.ReadFile(seed); string(b) != "keep me\n" {
		t.Errorf("the existing seed was overwritten: %q", b)
	}
	if !strings.Contains(out+errOut, "obs.nk") || !strings.Contains(out+errOut, "exists") {
		t.Errorf("the message does not say that keys/obs.nk exists: %q", out+errOut)
	}
	if string(h.readConf()) != confBefore || h.readRegistry() != nil || len(h.backups()) != 0 || h.homeListing() != listing {
		t.Errorf("something changed: conf changed %v, backups %v", string(h.readConf()) != confBefore, h.backups())
	}
}

func TestHubUserAddRefusesANameThatIsADanglingSymlink(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	target := filepath.Join(t.TempDir(), "elsewhere.nk")
	if err := os.Symlink(target, filepath.Join(h.keys, "obs.nk")); err != nil {
		t.Fatal(err)
	}
	confBefore, listing, seeds := string(h.readConf()), h.homeListing(), h.seedFiles()
	code, out, errOut := h.run("user", "add", "obs", "--class", "observer", "--param", "holder=director")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if got := h.seedFiles(); !reflect.DeepEqual(got, seeds) || h.homeListing() != listing {
		t.Errorf("the refusal changed the keys: seeds before %v, after %v", seeds, got)
	}
	if _, err := os.Stat(target); err == nil {
		t.Errorf("the command followed the symlink and wrote a seed at %s", target)
	}
	if !strings.Contains(out+errOut, "obs.nk") || !strings.Contains(out+errOut, "exists") {
		t.Errorf("the message does not say that keys/obs.nk exists: %q", out+errOut)
	}
	if string(h.readConf()) != confBefore || len(h.backups()) != 0 || h.readRegistry() != nil {
		t.Errorf("something changed after the refusal")
	}
}

func TestHubUserAddRendersOnlyTheManagedRegion(t *testing.T) {
	hand := `      { nkey: ` + newPublicKey(t) + ` }`
	h := newHubScratch(t, hubScratchOpts{extraUsers: hand})
	before := h.readConf()
	code, out, errOut := h.run("user", "add", "obs", "--class", "observer", "--param", "holder=director")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	after := h.readConf()
	if outsideRegion(before) != outsideRegion(after) {
		t.Errorf("bytes outside the managed region changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	pub, _ := h.registryUsers()[0]["public_key"].(string)
	if !strings.Contains(regionOf(after), pub) {
		t.Errorf("the new key is not inside the managed region:\n%s", regionOf(after))
	}
	if got := h.backups(); len(got) != 1 {
		t.Errorf("backups = %v, want one", got)
	} else {
		if b, _ := os.ReadFile(got[0]); string(b) != string(before) {
			t.Errorf("the backup is not the conf as it was")
		}
		if st, _ := os.Stat(got[0]); st.Mode().Perm() != 0o600 {
			t.Errorf("backup mode = %v, want the conf's 0600", st.Mode().Perm())
		}
	}
	h.assertNoSeedAnywhere(out, errOut)
}

func TestHubUserAddWritesNothingWhenTheCandidateFailsTheServerTest(t *testing.T) {
	old := hubClassFS
	hubClassFS = fstest.MapFS{"observer.conf": {Data: []byte("{ nkey: {{key}}\n  permissions { publish { allow: [ \n")}}
	defer func() { hubClassFS = old }()
	h := newHubScratch(t, hubScratchOpts{})
	confBefore, regBefore, listing := string(h.readConf()), string(h.readRegistry()), h.homeListing()

	code, out, errOut := h.run("user", "add", "obs", "--class", "observer", "--param", "holder=director")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	msg := out + errOut
	if !strings.Contains(msg, "nats-server -t") || !serverParseText.MatchString(msg) {
		t.Errorf("the message does not carry nats-server -t and the server's own parse error: %q", msg)
	}
	if !strings.Contains(msg, "unchanged") {
		t.Errorf("the message does not say the hub is unchanged: %q", msg)
	}
	if string(h.readConf()) != confBefore || string(h.readRegistry()) != regBefore {
		t.Errorf("the live conf or registry changed")
	}
	if h.homeListing() != listing {
		t.Errorf("the hub home changed (a backup, a key or a staging file was left): before\n%s\nafter\n%s", listing, h.homeListing())
	}
	if len(h.backups()) != 0 || len(h.seedFiles()) != 1 {
		t.Errorf("a backup or seed was written before the candidate passed")
	}
	h.assertNoSeedAnywhere(out, errOut)
}

func TestHubUserAddRollsBackWhenVerifyFails(t *testing.T) {
	// A grant wrong on purpose: it allows every JetStream API subject, so the
	// observer's negatives are not refused and verify must fail.
	old := hubClassFS
	hubClassFS = fstest.MapFS{"observer.conf": {Data: []byte(
		"{ nkey: {{key}}\n  permissions {\n    publish { allow: [ \"$JS.API.>\" ] }\n    subscribe { allow: [ \"_INBOX_observer_{{holder}}.>\" ] }\n  } }\n")}}
	defer func() { hubClassFS = old }()
	h := newHubScratch(t, hubScratchOpts{})
	confBefore, regBefore := string(h.readConf()), string(h.readRegistry())
	countsBefore := h.consumerCounts()
	pid, reloads := h.pid(), h.reloads()
	seedPath := filepath.Join(h.keys, "obs.nk")
	acceptedLive := false
	hubAfterReload = func() { acceptedLive = h.waitConnect(seedPath, 5*time.Second) }
	defer func() { hubAfterReload = nil }()

	code, out, errOut := h.run("user", "add", "obs", "--class", "observer", "--param", "holder=director")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !acceptedLive {
		t.Errorf("the new key was not accepted while the new conf was live, so the failure was not a verify failure")
	}
	if string(h.readConf()) != confBefore {
		t.Errorf("the conf was not restored byte for byte")
	}
	if string(h.readRegistry()) != regBefore {
		t.Errorf("the registry changed")
	}
	backups := h.backups()
	if len(backups) != 1 {
		t.Fatalf("backups = %v, want exactly one", backups)
	}
	if !strings.Contains(out+errOut, filepath.Base(backups[0])) {
		t.Errorf("the message does not name the backup it restored from: %s%s", out, errOut)
	}
	if got := h.seedFiles(); len(got) != 1 {
		t.Errorf("the rolled-back seed is still in keys: %v", got)
	}
	revoked := h.revokedSeeds()
	if len(revoked) != 1 {
		t.Fatalf("the rolled-back seed was not moved to keys/revoked: %v", revoked)
	}
	if h.canConnect(revoked[0]) {
		t.Errorf("the rolled-back key is still accepted by the hub")
	}
	if !h.waitReloads(reloads + 2) {
		t.Errorf("the log shows %d reloads, want %d (one forward, one back)", h.reloads(), reloads+2)
	}
	if h.pid() != pid {
		t.Errorf("the server pid changed")
	}
	if got := h.consumerCounts(); got != countsBefore {
		t.Errorf("consumer counts changed: before\n%safter\n%s", countsBefore, got)
	}
	h.assertNoSeedAnywhere(out, errOut)
}

func TestHubUserObserverVerifyLeavesConsumerCountsUnchanged(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	before := h.consumerCounts()
	code, out, errOut := h.run("user", "add", "obs", "--class", "observer", "--param", "holder=director")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if after := h.consumerCounts(); after != before {
		t.Errorf("consumer counts changed: before\n%safter\n%s", before, after)
	}
	for _, want := range []string{"consumer counts unchanged", "refused with a permission violation"} {
		if !strings.Contains(out, want) {
			t.Errorf("the output does not say %q:\n%s", want, out)
		}
	}
}

func TestHubUserAddWithAPublicKeyWritesNoSeed(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	pub := newPublicKey(t)
	code, out, errOut := h.run("user", "add", "leaf-probe", "--class", "leaf", "--param", "cluster=probe", "--public-key", pub)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if got := h.seedFiles(); len(got) != 1 {
		t.Errorf("a seed was written for a key that was given: %v", got)
	}
	if !strings.Contains(regionOf(h.readConf()), pub) {
		t.Errorf("the given key is not in the managed region")
	}
	if users := h.registryUsers(); len(users) != 1 || users[0]["public_key"] != pub {
		t.Errorf("registry = %v, want the given key", users)
	}
	if !strings.Contains(out, "in the reloaded conf") {
		t.Errorf("the output does not say the user was found in the reloaded conf:\n%s", out)
	}
}

func TestHubUserAddRefusesBadInput(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	confBefore, listing := string(h.readConf()), h.homeListing()
	cases := []struct {
		args []string
		want []string // each must appear in the message
	}{
		{[]string{"user", "add", "x", "--class", "nope"}, []string{"nope"}},
		{[]string{"user", "add", "x", "--class", "observer"}, []string{"holder"}},
		{[]string{"user", "add", "x", "--class", "observer", "--param", "holder=a b"}, []string{"holder", "a b"}},
		{[]string{"user", "add", "x", "--class", "observer", "--param", "holder=d", "--param", "z=1"}, []string{"z"}},
		{[]string{"user", "add", "bad name", "--class", "admin"}, []string{"bad name"}},
		{[]string{"user", "add", "x", "--class", "leaf", "--param", "cluster=p", "--public-key", "not-a-key"}, []string{"not-a-key"}},
	}
	for _, c := range cases {
		code, out, errOut := h.run(c.args...)
		if code != 2 {
			t.Errorf("%v: exit = %d, want 2 (a usage error)\n%s%s", c.args, code, out, errOut)
		}
		for _, w := range c.want {
			if !strings.Contains(out+errOut, w) {
				t.Errorf("%v: the message does not name %q: %q", c.args, w, out+errOut)
			}
		}
		if string(h.readConf()) != confBefore || h.homeListing() != listing || h.readRegistry() != nil {
			t.Errorf("%v: something changed", c.args)
		}
	}
	// A name already in the registry is refused, and says so.
	if code, out, errOut := h.run("user", "add", "obs", "--class", "observer", "--param", "holder=director"); code != 0 {
		t.Fatalf("first add exit = %d\n%s%s", code, out, errOut)
	}
	code, out, errOut := h.run("user", "add", "obs", "--class", "observer", "--param", "holder=director")
	if code != 1 || !strings.Contains(out+errOut, "obs") || !strings.Contains(out+errOut, "already") {
		t.Errorf("a second add of the same name: exit = %d, message %q; want 1 saying obs already exists", code, out+errOut)
	}
}

func TestHubUserAddHoldsTheLockAgainstASecondAdd(t *testing.T) {
	// A hook holds the first add inside its lock, so the second add arrives while
	// the lock is certainly held; the lost update the old test could only make
	// likely is made certain.
	h := newHubScratch(t, hubScratchOpts{})
	inside, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	hubInLock = func(verb string) {
		once.Do(func() {
			close(inside)
			<-release
		})
	}
	defer func() { hubInLock = nil }()
	done := make(chan [3]any, 1)
	go func() {
		c, o, e := h.run("user", "add", "obs-a", "--class", "observer", "--param", "holder=a")
		done <- [3]any{c, o, e}
	}()
	select {
	case <-inside:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatalf("the first add never reached the lock hook")
	}
	type res struct {
		code     int
		out, err string
	}
	second := make(chan res, 1)
	go func() {
		c, o, e := h.run("user", "add", "obs-b", "--class", "observer", "--param", "holder=b")
		second <- res{c, o, e}
	}()
	var sec res
	select {
	case sec = <-second:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatalf("the second add waited for the lock instead of refusing")
	}
	close(release)
	code, out, errOut := sec.code, sec.out, sec.err
	r := <-done
	if code != 1 || !strings.Contains(out+errOut, "another hub command") {
		t.Errorf("the second add: exit = %d, message %q; want 1 saying another hub command holds the lock", code, out+errOut)
	}
	if r[0].(int) != 0 {
		t.Errorf("the first add exit = %v, want 0\n%v%v", r[0], r[1], r[2])
	}
	users := h.registryUsers()
	if len(users) != 1 || users[0]["name"] != "obs-a" {
		t.Errorf("registry users = %v, want only obs-a", users)
	}
	if got := strings.Count(regionOf(h.readConf()), "nkey:"); got != 1 {
		t.Errorf("the region holds %d users, want 1", got)
	}
}

func TestHubUserConcurrentAddsLoseNothing(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	type res struct {
		code     int
		out, err string
	}
	var wg sync.WaitGroup
	results := make([]res, 2)
	for i, n := range []string{"obs-a", "obs-b"} {
		wg.Add(1)
		go func(i int, n string) {
			defer wg.Done()
			c, o, e := h.run("user", "add", n, "--class", "observer", "--param", "holder="+strings.TrimPrefix(n, "obs-"))
			results[i] = res{c, o, e}
		}(i, n)
	}
	wg.Wait()
	succeeded := 0
	for _, r := range results {
		switch r.code {
		case 0:
			succeeded++
		case 1:
			if !strings.Contains(r.out+r.err, "another hub command") {
				t.Errorf("a refused add does not say another hub command holds the lock: %q", r.out+r.err)
			}
		default:
			t.Errorf("exit = %d, want 0 or 1\n%s%s", r.code, r.out, r.err)
		}
	}
	if succeeded == 0 {
		t.Fatalf("no add succeeded")
	}
	if got := len(h.registryUsers()); got != succeeded {
		t.Errorf("registry holds %d users, want %d (one per add that exited 0)", got, succeeded)
	}
	if got := strings.Count(regionOf(h.readConf()), "nkey:"); got != succeeded {
		t.Errorf("the region holds %d users, want %d", got, succeeded)
	}
	if code, out, errOut := h.run("user", "add", "obs-c", "--class", "observer", "--param", "holder=c"); code != 0 {
		t.Errorf("a later add exit = %d (the lock was not released)\n%s%s", code, out, errOut)
	}
}

// --- adopt and list ---------------------------------------------------------------------------------

func adoptFixture(t *testing.T) (*hubScratch, string) {
	t.Helper()
	pub := newPublicKey(t)
	hand := `      { nkey: ` + pub + `
        permissions {
          publish   { allow: [ "global.probe.>", "global.extra.>" ] }
          subscribe { allow: [ "global.probe.>", "_INBOX.>" ] }
        } }`
	return newHubScratch(t, hubScratchOpts{extraUsers: hand}), pub
}

func TestHubUserAdoptReportsGrantDifferences(t *testing.T) {
	h, pub := adoptFixture(t)
	code, out, errOut := h.run("user", "adopt", pub, "--name", "probe-leaf", "--class", "leaf", "--param", "cluster=probe")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "difference") || !strings.Contains(out, "global.extra.>") {
		t.Errorf("adopt does not report the hand block's extra grant global.extra.>:\n%s", out)
	}
	users := h.registryUsers()
	if len(users) != 1 || users[0]["name"] != "probe-leaf" || users[0]["public_key"] != pub || users[0]["state"] != "adopted" {
		t.Errorf("registry = %v, want probe-leaf adopted with the key", users)
	}
	if code, _, _ := h.run("user", "adopt", newPublicKey(t), "--name", "ghost", "--class", "leaf", "--param", "cluster=probe"); code == 0 {
		t.Errorf("adopt of a key that is not in the conf succeeded")
	}
}

func TestHubUserAdoptChangesNoConfBytes(t *testing.T) {
	h, pub := adoptFixture(t)
	confBefore := string(h.readConf())
	st0, _ := os.Stat(h.conf)
	pid, reloads := h.pid(), h.reloads()
	code, out, errOut := h.run("user", "adopt", pub, "--name", "probe-leaf", "--class", "leaf", "--param", "cluster=probe")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	st1, _ := os.Stat(h.conf)
	if string(h.readConf()) != confBefore || !st0.ModTime().Equal(st1.ModTime()) {
		t.Errorf("adopt wrote the conf")
	}
	if len(h.backups()) != 0 {
		t.Errorf("adopt made a backup: %v", h.backups())
	}
	if h.reloads() != reloads || h.pid() != pid {
		t.Errorf("adopt reloaded the hub")
	}
	if len(h.seedFiles()) != 1 {
		t.Errorf("adopt wrote a key: %v", h.seedFiles())
	}
	if len(h.registryUsers()) != 1 {
		t.Errorf("adopt did not record the user in the registry")
	}
}

func TestHubUserListShowsAdoptedUsersOutsideTheRegionNotAsDrift(t *testing.T) {
	h, pub := adoptFixture(t)
	if code, out, errOut := h.run("user", "adopt", pub, "--name", "probe-leaf", "--class", "leaf", "--param", "cluster=probe"); code != 0 {
		t.Fatalf("adopt exit = %d\n%s%s", code, out, errOut)
	}
	code, out, errOut := h.run("user", "list", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	var got struct {
		Users []map[string]any `json:"users"`
		Drift []string         `json:"drift"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("list --json is not JSON: %v\n%s", err, out)
	}
	if len(got.Users) != 1 || got.Users[0]["name"] != "probe-leaf" || got.Users[0]["location"] != "outside the region" {
		t.Errorf("users = %v, want probe-leaf with location \"outside the region\"", got.Users)
	}
	if len(got.Drift) != 0 {
		t.Errorf("an adopted user outside the region is reported as drift: %v", got.Drift)
	}
}

func TestHubUserListReportsRegistryConfDrift(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	if code, out, errOut := h.run("user", "add", "obs", "--class", "observer", "--param", "holder=director"); code != 0 {
		t.Fatalf("add exit = %d\n%s%s", code, out, errOut)
	}
	code, out, errOut := h.run("user", "list", "--json")
	if code != 0 {
		t.Fatalf("list exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	var clean struct {
		Users []map[string]any `json:"users"`
		Drift []string         `json:"drift"`
	}
	if err := json.Unmarshal([]byte(out), &clean); err != nil {
		t.Fatalf("list --json is not JSON: %v\n%s", err, out)
	}
	if len(clean.Users) != 1 || clean.Users[0]["name"] != "obs" || len(clean.Drift) != 0 {
		t.Errorf("a consistent hub lists users %v with drift %v, want obs and no drift", clean.Users, clean.Drift)
	}
	// A hand edit: the user's block is deleted from the region, the registry keeps it.
	conf := h.readConf()
	i := strings.Index(string(conf), hubRegionBegin) + len(hubRegionBegin)
	j := strings.Index(string(conf), hubRegionEnd)
	if err := os.WriteFile(h.conf, []byte(string(conf[:i])+"\n      "+string(conf[j:])), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ = h.run("user", "list", "--json")
	if code != 0 {
		t.Errorf("list exit = %d on drift, want 0 (it is a diagnostic)", code)
	}
	var drifted struct {
		Drift []string `json:"drift"`
	}
	if err := json.Unmarshal([]byte(out), &drifted); err != nil {
		t.Fatalf("list --json is not JSON: %v\n%s", err, out)
	}
	if len(drifted.Drift) != 1 || !strings.Contains(drifted.Drift[0], "obs") || !strings.Contains(drifted.Drift[0], "not in the conf") {
		t.Errorf("drift = %v, want one line saying obs is in the registry and not in the conf", drifted.Drift)
	}
}

// --- dispatch -----------------------------------------------------------------------------------------

func TestHubUserUnknownVerbExitsTwoAndStartsNoShim(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	for _, args := range [][]string{{"user", "frobnicate"}, {"frobnicate"}, {}, {"user"}, {"settings", "frobnicate"}} {
		code, out, errOut := h.run(args...)
		if code != 2 {
			t.Errorf("%v: exit = %d, want 2\nstdout: %s\nstderr: %s", args, code, out, errOut)
		}
		if !strings.Contains(errOut, "usage") {
			t.Errorf("%v: stderr does not show usage: %q", args, errOut)
		}
	}
	cli, err := parseArgs([]string{"hub", "user", "frobnicate"})
	if err != nil || cli.mode != "hub" || strings.Join(cli.hubArgs, " ") != "user frobnicate" {
		t.Errorf("parseArgs hub = %+v, %v; want mode hub carrying its words, so main runs the hub command and never serves", cli, err)
	}
}

// TestHubPidGuardReadsTheRealPs is the one case that runs the real ps. It cannot
// run under sandbox-exec (ps is setuid), so a sandboxed run skips it with
// -skip TestHubPidGuardReadsTheRealPs and it runs unsandboxed in CI.
func TestHubPidGuardReadsTheRealPs(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	hubProcArgs = realHubProcArgs
	if _, err := hubPid(hubCfgOf(t, h)); err != nil {
		t.Errorf("the real ps does not show this hub's nats-server for its own pid: %v", err)
	}
	other := exec.Command("sleep", "60")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Process.Kill(); _ = other.Wait() })
	if err := os.WriteFile(h.pidfile, []byte(fmt.Sprintf("%d\n", other.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := hubPid(hubCfgOf(t, h)); err == nil || !strings.Contains(err.Error(), "not this hub's nats-server") {
		t.Errorf("the real ps accepted a sleep process as the hub: %v", err)
	}
}

func hubCfgOf(t *testing.T, h *hubScratch) hubCfg {
	t.Helper()
	c, err := hubCfgFrom(h.env())
	if err != nil {
		t.Fatal(err)
	}
	return c
}
