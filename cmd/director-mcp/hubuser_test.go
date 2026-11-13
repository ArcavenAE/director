package main

// Red tests for `director hub user add|list|adopt` and `director hub settings
// check|init` (sim/design/hub-user-lifecycle.md). Each test starts its own
// scratch nats-server (hubuser_harness_test.go) and runs the command in
// process against it. The two failures that stopped the I-469 at-host script on
// its first live run are the first two tests: a missing tool and a conf that
// references a variable the check cannot see. Both must stop before any backup,
// any key or any registry write.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"
	"time"
)

func cctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 20*time.Second)
}

// --- the two failures from the first live run of the I-469 script -------------------------------------------------------------------

func TestHubUserRefusesWhenNatsServerIsMissing(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	confBefore := string(h.readConf())
	empty := t.TempDir()
	t.Setenv("PATH", empty)

	code, out, errOut := h.run("user", "add", "obs", "--class", "observer", "--param", "holder=director")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	msg := out + errOut
	if !strings.Contains(msg, "nats-server") || !strings.Contains(msg, "PATH") {
		t.Errorf("the message does not say nats-server is not on PATH: %q", msg)
	}
	if string(h.readConf()) != confBefore {
		t.Errorf("the conf changed")
	}
	if len(h.backups()) != 0 {
		t.Errorf("a backup was written: %v", h.backups())
	}
	if got := h.seedFiles(); len(got) != 1 {
		t.Errorf("a key was written beside admin.nk: %v", got)
	}
	if h.readRegistry() != nil {
		t.Errorf("the registry was written")
	}
}

func TestHubUserRefusesWhenTheUnchangedConfFailsTheServerTest(t *testing.T) {
	// The conf references $HUB_NAME and no settings.conf defines it, so a check
	// run from this shell cannot see what the running server sees.
	h := newHubScratch(t, hubScratchOpts{noSettings: true})
	confBefore := string(h.readConf())

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
	if string(h.readConf()) != confBefore {
		t.Errorf("the conf changed")
	}
	if len(h.backups()) != 0 {
		t.Errorf("a backup was written: %v", h.backups())
	}
	if got := h.seedFiles(); len(got) != 1 {
		t.Errorf("a key was minted: %v", got)
	}
	if h.readRegistry() != nil {
		t.Errorf("the registry was written")
	}
}

func TestHubUserRefusesWhileASettingsReferenceIsUndefined(t *testing.T) {
	// settings.conf exists and the conf includes it, but it does not define $HUB_TAG.
	// The running server got HUB_TAG from its environment; a check run from this shell did not.
	h := newHubScratch(t, hubScratchOpts{refTag: true, serverEnv: []string{"HUB_TAG=from-env"}})
	code, out, errOut := h.run("user", "add", "obs", "--class", "observer", "--param", "holder=director")
	if code != 1 || !strings.Contains(out+errOut, "HUB_TAG") {
		t.Fatalf("exit = %d, want 1 naming HUB_TAG\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if len(h.backups()) != 0 || h.readRegistry() != nil {
		t.Errorf("something was written: backups %v", h.backups())
	}
}

// --- settings.conf (option B) ----------------------------------------------------------------

func TestHubConfVariableResolvesFromTheSettingsInclude(t *testing.T) {
	// The server is started with HUB_NAME=from-env in its environment and
	// settings.conf says from-settings. Measured on nats-server 2.14.6: the conf
	// variable wins, for the server and for -t alike. The test records that the
	// check agrees with the server and that `settings check` reports it defined.
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
	if strings.Contains(out, "JS") || strings.Contains(out, "KV") {
		t.Errorf("a quoted subject was read as a reference: %q", out)
	}
}

func TestHubSettingsCheckFlagsADefinitionNothingReferences(t *testing.T) {
	// Measured on nats-server 2.14.6: a variable in an included file that the conf
	// never references stops the server and the -t check with `unknown field`. So a
	// settings.conf entry the conf does not use is an error, not a harmless extra.
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
	// settings init refuses a --set for a name the conf does not reference.
	h2 := newHubScratch(t, hubScratchOpts{noSettings: true, noRegion: true})
	confBefore := string(h2.readConf())
	code, out, errOut = h2.run("settings", "init", "--set", "HUB_NAME=a", "--set", "HUB_SPARE=b")
	if code != 1 || !strings.Contains(out+errOut, "HUB_SPARE") {
		t.Errorf("init with an unreferenced --set: exit = %d, want 1 naming HUB_SPARE\n%s%s", code, out, errOut)
	}
	if string(h2.readConf()) != confBefore || len(h2.backups()) != 0 {
		t.Errorf("init changed the conf before refusing")
	}
	if _, err := os.Stat(h2.settings); err == nil {
		t.Errorf("init wrote settings.conf before refusing")
	}
}

func TestHubSettingsInitMovesAnOldHubToTheIncludeAndRegion(t *testing.T) {
	// An old-style conf: no include, no managed region, one variable reference.
	h := newHubScratch(t, hubScratchOpts{noSettings: true, noRegion: true})
	confBefore := string(h.readConf())

	code, out, errOut := h.run("settings", "init", "--set", "HUB_NAME=zed")
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
	if !strings.Contains(string(b), "HUB_NAME") || !strings.Contains(string(b), "zed") {
		t.Errorf("settings.conf does not define HUB_NAME as zed: %q", b)
	}
	if got := h.backups(); len(got) != 1 {
		t.Errorf("backups = %v, want one", got)
	} else if bb, _ := os.ReadFile(got[0]); string(bb) != confBefore {
		t.Errorf("the backup is not the conf as it was")
	}
	if code, _, _ := h.run("settings", "check"); code != 0 {
		t.Errorf("settings check after init exits %d, want 0", code)
	}
	// A second init changes nothing and says why.
	confAfter := string(h.readConf())
	code2, out2, err2 := h.run("settings", "init", "--set", "HUB_NAME=other")
	if code2 != 1 || string(h.readConf()) != confAfter {
		t.Errorf("a second init exits %d and the conf changed = %v\n%s%s", code2, string(h.readConf()) != confAfter, out2, err2)
	}
}

// --- add ---------------------------------------------------------------------------------------

func TestHubUserAddMintsASeedThatIsNeverPrinted(t *testing.T) {
	old := syscall.Umask(0) // the key's mode must not follow the umask
	defer syscall.Umask(old)
	h := newHubScratch(t, hubScratchOpts{})

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
	for _, s := range []string{out, errOut, string(h.readRegistry()), string(h.readConf())} {
		if strings.Contains(s, "SU") && containsSeedShape(s) {
			t.Errorf("a seed appears in output, registry or conf")
		}
	}
	seed := filepath.Join(h.keys, "obs.nk")
	st, err := os.Stat(seed)
	if err != nil {
		t.Fatalf("the seed is not at keys/obs.nk: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("seed mode = %v, want 0600 under umask 0", st.Mode().Perm())
	}
	if ds, _ := os.Stat(h.keys); ds.Mode().Perm()&0o077 != 0 {
		t.Errorf("keys directory mode = %v, want no group or other access", ds.Mode().Perm())
	}
	if !h.canConnect(seed) {
		t.Errorf("the new key is not accepted by the reloaded hub")
	}
}

func containsSeedShape(s string) bool {
	// A user seed is 58 base32 characters starting SU.
	for i := 0; i+58 <= len(s); i++ {
		if s[i] == 'S' && s[i+1] == 'U' {
			ok := true
			for _, c := range s[i : i+58] {
				if !(c >= 'A' && c <= 'Z' || c >= '2' && c <= '7') {
					ok = false
					break
				}
			}
			if ok {
				return true
			}
		}
	}
	return false
}

func TestHubUserAddNeverOverwritesAnExistingSeed(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	seed := filepath.Join(h.keys, "obs.nk")
	if err := os.WriteFile(seed, []byte("keep me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	confBefore := string(h.readConf())
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
	if string(h.readConf()) != confBefore || h.readRegistry() != nil || len(h.backups()) != 0 {
		t.Errorf("something changed: conf changed %v, backups %v", string(h.readConf()) != confBefore, h.backups())
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
	} else if b, _ := os.ReadFile(got[0]); string(b) != string(before) {
		t.Errorf("the backup is not the conf as it was")
	}
}

func TestHubUserAddRollsBackWhenTheConfFailsTheServerTest(t *testing.T) {
	old := hubClassFS
	hubClassFS = fstest.MapFS{"observer.conf": {Data: []byte("{ nkey: {{key}}\n  permissions { publish { allow: [ \n")}}
	defer func() { hubClassFS = old }()
	h := newHubScratch(t, hubScratchOpts{})
	confBefore, regBefore := string(h.readConf()), string(h.readRegistry())

	code, out, errOut := h.run("user", "add", "obs", "--class", "observer", "--param", "holder=director")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if string(h.readConf()) != confBefore || string(h.readRegistry()) != regBefore {
		t.Errorf("the live conf or registry changed")
	}
	if len(h.backups()) != 0 {
		t.Errorf("a backup was written before the candidate passed -t: %v", h.backups())
	}
	if !strings.Contains(out+errOut, "-t") || !strings.Contains(out+errOut, "unchanged") {
		t.Errorf("the message does not say the candidate failed the server test and the hub is unchanged: %q", out+errOut)
	}
	if got := h.seedFiles(); len(got) != 1 {
		t.Errorf("a seed was left in keys: %v", got)
	}
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

	code, out, errOut := h.run("user", "add", "obs", "--class", "observer", "--param", "holder=director")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if string(h.readConf()) != confBefore {
		t.Errorf("the conf was not restored byte for byte")
	}
	if string(h.readRegistry()) != regBefore {
		t.Errorf("the registry changed")
	}
	if len(h.backups()) != 1 || !strings.Contains(out+errOut, filepath.Base(h.backups()[0])) {
		t.Errorf("the message does not name the backup it restored from: backups %v\n%s%s", h.backups(), out, errOut)
	}
	if got := h.seedFiles(); len(got) != 1 {
		t.Errorf("the rolled-back seed is still in keys: %v", got)
	}
	if m, _ := filepath.Glob(filepath.Join(h.keys, "revoked", "obs-*.nk")); len(m) != 1 {
		t.Errorf("the rolled-back seed was not moved to keys/revoked: %v", m)
	}
	// The hub was reloaded to the restored conf: the failed key is refused now.
	if h.canConnect(filepath.Join(h.keys, "revoked", mustBase(t, h.keys))) {
		t.Errorf("the rolled-back key is still accepted by the hub")
	}
	if got := h.consumerCounts(); got != countsBefore {
		t.Errorf("consumer counts changed: before\n%safter\n%s", countsBefore, got)
	}
}

func mustBase(t *testing.T, keys string) string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(keys, "revoked", "obs-*.nk"))
	if len(m) != 1 {
		return "missing.nk"
	}
	return filepath.Base(m[0])
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
	confBefore := string(h.readConf())
	cases := [][]string{
		{"user", "add", "x", "--class", "nope"},
		{"user", "add", "x", "--class", "observer"},                                          // missing holder
		{"user", "add", "x", "--class", "observer", "--param", "holder=a b"},                 // bad value
		{"user", "add", "x", "--class", "observer", "--param", "holder=d", "--param", "z=1"}, // unknown param
		{"user", "add", "bad name", "--class", "admin"},
		{"user", "add", "x", "--class", "leaf", "--param", "cluster=p", "--public-key", "not-a-key"},
	}
	for _, c := range cases {
		code, out, errOut := h.run(c...)
		if code != 2 && code != 1 {
			t.Errorf("%v: exit = %d, want 1 or 2\n%s%s", c, code, out, errOut)
		}
		if code == 0 || string(h.readConf()) != confBefore || len(h.backups()) != 0 || h.readRegistry() != nil {
			t.Errorf("%v: something changed or it succeeded", c)
		}
	}
	// A name already in the registry is refused.
	if code, out, errOut := h.run("user", "add", "obs", "--class", "observer", "--param", "holder=director"); code != 0 {
		t.Fatalf("first add exit = %d\n%s%s", code, out, errOut)
	}
	if code, _, _ := h.run("user", "add", "obs", "--class", "observer", "--param", "holder=director"); code == 0 {
		t.Errorf("a second add of the same name succeeded")
	}
}

// --- adopt and list -----------------------------------------------------------------------------

func TestHubUserAdoptReportsGrantDifferences(t *testing.T) {
	pub := newPublicKey(t)
	hand := `      { nkey: ` + pub + `
        permissions {
          publish   { allow: [ "global.probe.>", "global.extra.>" ] }
          subscribe { allow: [ "global.probe.>", "_INBOX.>" ] }
        } }`
	h := newHubScratch(t, hubScratchOpts{extraUsers: hand})
	confBefore := string(h.readConf())

	code, out, errOut := h.run("user", "adopt", pub, "--name", "probe-leaf", "--class", "leaf", "--param", "cluster=probe")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "difference") || !strings.Contains(out, "global.extra.>") {
		t.Errorf("adopt does not report the hand block's extra grant global.extra.>:\n%s", out)
	}
	if string(h.readConf()) != confBefore {
		t.Errorf("adopt changed the conf; a hand-written user outside the region is left alone")
	}
	users := h.registryUsers()
	if len(users) != 1 || users[0]["name"] != "probe-leaf" || users[0]["public_key"] != pub || users[0]["state"] != "adopted" {
		t.Errorf("registry = %v, want probe-leaf adopted with the key", users)
	}
	if code, _, _ := h.run("user", "adopt", newPublicKey(t), "--name", "ghost", "--class", "leaf", "--param", "cluster=probe"); code == 0 {
		t.Errorf("adopt of a key that is not in the conf succeeded")
	}
}

func TestHubUserListReportsRegistryConfDrift(t *testing.T) {
	h := newHubScratch(t, hubScratchOpts{})
	if code, out, errOut := h.run("user", "add", "obs", "--class", "observer", "--param", "holder=director"); code != 0 {
		t.Fatalf("add exit = %d\n%s%s", code, out, errOut)
	}
	code, out, errOut := h.run("user", "list", "--json")
	if code != 0 || !strings.Contains(out, `"name":"obs"`) && !strings.Contains(out, `"name": "obs"`) {
		t.Fatalf("list exit = %d, want 0 showing obs\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if strings.Contains(out, "drift\":[\"") {
		t.Errorf("list reports drift on a consistent hub: %s", out)
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
	if !strings.Contains(out, "obs") || !strings.Contains(strings.ToLower(out), "drift") || !strings.Contains(out, "not in the conf") {
		t.Errorf("list does not report that obs is in the registry and not in the conf:\n%s", out)
	}
}

// --- dispatch -------------------------------------------------------------------------------------

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
