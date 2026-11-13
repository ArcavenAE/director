package main

// Red tests for launcher-supplied names (I-476, option A): a hub whose launcher
// exports a variable the conf references (kinu's HUB_LEAF_ADVERTISE) must not
// stop every changing verb. `settings check` and `settings init` read the
// running hub's environment from its pid-file process; `init --env NAME` leaves
// the name out of settings.conf; the -t runs of every changing verb carry the
// launcher's values.
//
// Measured on nats-server 2.14.6 (the version on the machine that wrote these
// tests; CI pins another): an environment value that carries its own double or
// single quotes is read as a conf value, so "h:p" and h:p both give h:p, and
// both pass -t; a doubled quote fails -t. So --env must pass the value through
// byte for byte: no stripping and no extra quoting.

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type launcherHub struct {
	*hubScratch
	leaf int
	bin  string
	env  []string
}

// newLauncherHub makes an old-style hub (no settings.conf, no include, no
// managed region) whose conf references $HUB_NAME and $HUB_LEAF_ADVERTISE, both
// supplied by the hub's own environment, as a launcher supplies them. The
// scratch hub answers hubProcEnv for its own pid with that environment.
func newLauncherHub(t *testing.T, adv string, extraEnv ...string) *launcherHub {
	t.Helper()
	return newLauncherHubWith(t, "", adv, extraEnv...)
}

// newLauncherHubWith also places hand-written user blocks outside the managed region.
func newLauncherHubWith(t *testing.T, handUsers, adv string, extraEnv ...string) *launcherHub {
	t.Helper()
	h := newHubScratch(t, hubScratchOpts{noSettings: true, noRegion: true, extraUsers: handUsers, serverEnv: []string{"HUB_NAME=hub"}})
	bin, err := exec.LookPath("nats-server")
	if err != nil {
		t.Fatal(err)
	}
	l := &launcherHub{hubScratch: h, leaf: freePort(t), bin: bin}
	base := string(h.readConf())
	port := fmt.Sprint(l.leaf)
	for _, e := range extraEnv {
		if strings.HasPrefix(e, "HUB_LEAF_PORT=") {
			port = "$HUB_LEAF_PORT"
		}
	}
	conf := strings.Replace(base, "jetstream {", fmt.Sprintf("leafnodes { port: %s, advertise: $HUB_LEAF_ADVERTISE }\njetstream {", port), 1)
	if conf == base {
		t.Fatal("the conf edit did not apply")
	}
	if err := os.WriteFile(h.conf, []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	l.restart(t, adv, extraEnv...)
	old := hubProcEnv
	hubProcEnv = func(pid int) (map[string]string, error) {
		if l.cmd == nil || l.cmd.Process == nil || pid != l.cmd.Process.Pid {
			return nil, fmt.Errorf("pid %d is not the scratch hub", pid)
		}
		m := map[string]string{"PATH": "/usr/bin"}
		for _, e := range l.env {
			k, v, _ := strings.Cut(e, "=")
			m[k] = v
		}
		return m, nil
	}
	t.Cleanup(func() { hubProcEnv = old })
	return l
}

func (l *launcherHub) restart(t *testing.T, adv string, extraEnv ...string) {
	t.Helper()
	_ = l.cmd.Process.Kill()
	_, _ = l.cmd.Process.Wait()
	l.env = append([]string{"HUB_NAME=hub", "HUB_LEAF_ADVERTISE=" + adv}, extraEnv...)
	l.startServer(l.bin, l.conf, l.env)
}

// advertised reads the leafnode_urls the hub's leaf port announces.
func (l *launcherHub) advertised(t *testing.T) string {
	t.Helper()
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", l.leaf), 2*time.Second)
	if err != nil {
		return "dial failed: " + err.Error()
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return "read failed: " + err.Error()
	}
	i := strings.Index(line, `"leafnode_urls":[`)
	if i < 0 {
		return "no leafnode_urls in: " + line
	}
	j := strings.Index(line[i:], "]")
	return line[i : i+j+1]
}

func (l *launcherHub) settingsText() string {
	b, err := os.ReadFile(l.settings)
	if err != nil {
		return "unreadable: " + err.Error()
	}
	return string(b)
}

// installNatsServerWrapper puts a nats-server wrapper first on PATH. natsServer
// finds the binary with LookPath, so every -t the commands run goes through it.
// The wrapper appends the arguments and the HUB_LEAF_ADVERTISE it was started
// with to a log in scratch (its absolute path is in the script), then execs the real binary. It writes nowhere else.
func installNatsServerWrapper(t *testing.T) (logPath string) {
	t.Helper()
	real, err := exec.LookPath("nats-server")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	logPath = filepath.Join(dir, "wrap.log")
	// The log path is written into the script, not passed in the environment, so
	// a command that runs -t with a clean environment still logs to the same file.
	script := "#!/bin/sh\nprintf 'args=%s HUB_LEAF_ADVERTISE=[%s]\\n' \"$*\" \"$HUB_LEAF_ADVERTISE\" >> '" + logPath + "'\nexec " + real + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "nats-server"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	return logPath
}

// testRuns returns the HUB_LEAF_ADVERTISE value of each `nats-server -t` run the wrapper logged.
func testRuns(t *testing.T, logPath string) []string {
	t.Helper()
	b, _ := os.ReadFile(logPath)
	var vals []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if !strings.HasPrefix(l, "args=-t ") && !strings.Contains(l, " -t ") {
			continue
		}
		i := strings.Index(l, "HUB_LEAF_ADVERTISE=[")
		vals = append(vals, strings.TrimSuffix(l[i+len("HUB_LEAF_ADVERTISE=["):], "]"))
	}
	return vals
}

// --- check --------------------------------------------------------------------------------------

func TestHubSettingsCheckNamesALauncherSuppliedName(t *testing.T) {
	l := newLauncherHub(t, "127.0.0.1:7501")
	code, out, errOut := l.run("settings", "check")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (both names are supplied by the running hub)\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, n := range []string{"HUB_LEAF_ADVERTISE", "HUB_NAME"} {
		if !strings.Contains(out, "launcher "+n) {
			t.Errorf("the output does not name %s as supplied by the launcher:\n%s", n, out)
		}
		if strings.Contains(out, "UNDEFINED "+n) {
			t.Errorf("%s is still reported UNDEFINED:\n%s", n, out)
		}
	}
	if !strings.Contains(out, "running hub's environment") {
		t.Errorf("the output does not say where the value comes from:\n%s", out)
	}
}

func TestHubSettingsCheckReportsUndefinedWhenTheHubEnvironmentCannotBeRead(t *testing.T) {
	l := newLauncherHub(t, "127.0.0.1:7501")
	hubProcEnv = func(pid int) (map[string]string, error) { return nil, fmt.Errorf("boom: ps refused") }
	code, out, errOut := l.run("settings", "check")
	if code != 1 || !strings.Contains(out, "UNDEFINED HUB_LEAF_ADVERTISE") {
		t.Errorf("exit = %d, stdout = %q, want 1 and UNDEFINED HUB_LEAF_ADVERTISE", code, out)
	}
	if !strings.Contains(out, "running hub's environment") || !strings.Contains(out, "boom: ps refused") {
		t.Errorf("the output does not say the hub's environment could not be read, and why:\n%s%s", out, errOut)
	}
}

func TestHubSettingsCheckWarnsWhenSettingsConfShadowsALauncherName(t *testing.T) {
	l := newLauncherHub(t, "127.0.0.1:7501")
	conf := l.readConf()
	if err := os.WriteFile(l.conf, append([]byte("include \"./settings.conf\"\n"), conf...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.settings, []byte("HUB_NAME: \"hub\"\nHUB_LEAF_ADVERTISE: \"127.0.0.1:7777\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := l.run("settings", "check")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (a warning is not a failure)\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "WARN SHADOWED HUB_LEAF_ADVERTISE") {
		t.Errorf("no warning that settings.conf shadows the launcher's HUB_LEAF_ADVERTISE:\n%s", out)
	}
}

// --- init ---------------------------------------------------------------------------------------

func TestHubSettingsInitEnvLeavesALauncherNameOutOfSettingsConf(t *testing.T) {
	l := newLauncherHub(t, "127.0.0.1:7501")
	reloads := l.reloads()
	code, out, errOut := l.run("settings", "init", "--set", "HUB_NAME=hub", "--env", "HUB_LEAF_ADVERTISE")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if got := l.settingsText(); got != "HUB_NAME: \"hub\"\n" {
		t.Errorf("settings.conf = %q, want only HUB_NAME", got)
	}
	conf := string(l.readConf())
	if !strings.Contains(conf, `include "./settings.conf"`) || !strings.Contains(conf, hubRegionBegin) {
		t.Errorf("the conf has no include or no managed region")
	}
	if l.reloads() != reloads+1 {
		t.Errorf("reloads = %d, want %d", l.reloads(), reloads+1)
	}
	if code, out, _ := l.run("settings", "check"); code != 0 || !strings.Contains(out, "launcher HUB_LEAF_ADVERTISE") {
		t.Errorf("check after init: exit = %d, stdout = %q, want 0 and the launcher line", code, out)
	}
	// The launcher recomputes at its next start: the hub follows it, because
	// settings.conf no longer pins the name.
	l.restart(t, "127.0.0.1:7502")
	if got := l.advertised(t); !strings.Contains(got, "127.0.0.1:7502") {
		t.Errorf("after a launcher restart with a new address the hub advertises %s, want 127.0.0.1:7502", got)
	}
	l.assertNoSeedAnywhere(out, errOut)
}

func TestHubSettingsInitEnvPassesAQuotedValueThroughUnchanged(t *testing.T) {
	// The launcher exports the value with its own double quotes, as kinu's does.
	quoted := `"127.0.0.1:7501"`
	l := newLauncherHub(t, quoted)
	if got := l.advertised(t); !strings.Contains(got, `"127.0.0.1:7501"`) || strings.Contains(got, `\"`) {
		t.Fatalf("setup: the hub with a quoted environment value advertises %s", got)
	}
	wrapLog := installNatsServerWrapper(t)
	confBefore, regBefore := string(l.readConf()), string(l.readRegistry())
	listing := l.homeListing()
	// The unquoted spelling is a different string. It is refused, with both values shown.
	code, _, errOut := l.run("settings", "init", "--set", "HUB_NAME=hub", "--env", "HUB_LEAF_ADVERTISE=127.0.0.1:7501")
	if code != 1 || !strings.Contains(errOut, "differs from the running hub's value") || !strings.Contains(errOut, quoted) {
		t.Errorf("a value that differs in its quotes: exit = %d, stderr = %q, want 1, the differs message and the hub's quoted value", code, errOut)
	}
	if string(l.readConf()) != confBefore || string(l.readRegistry()) != regBefore || l.homeListing() != listing {
		t.Errorf("a refused init left something behind")
	}
	// The exact spelling, and the name alone (read from the hub), both succeed and write no value.
	code, out, errOut := l.run("settings", "init", "--set", "HUB_NAME=hub", "--env", "HUB_LEAF_ADVERTISE="+quoted)
	if code != 0 {
		t.Fatalf("the exact value: exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if got := l.settingsText(); got != "HUB_NAME: \"hub\"\n" {
		t.Errorf("settings.conf = %q, want only HUB_NAME", got)
	}
	if got := l.advertised(t); !strings.Contains(got, `"127.0.0.1:7501"`) {
		t.Errorf("after init the hub advertises %s", got)
	}
	// A later add runs -t on the unchanged conf and on its candidate.
	if code, out, errOut := l.run("user", "add", "obs", "--class", "observer", "--param", "holder=director"); code != 0 {
		t.Errorf("add after init: exit = %d\n%s%s", code, out, errOut)
	}
	// Every -t run carries the launcher's value byte for byte, quotes included:
	// the init candidate, and the add's control and candidate.
	runs := testRuns(t, wrapLog)
	if len(runs) < 3 {
		t.Errorf("the wrapper logged %d -t runs, want at least 3 (init candidate, add control, add candidate): %v", len(runs), runs)
	}
	for i, v := range runs {
		if v != quoted {
			t.Errorf("-t run %d saw HUB_LEAF_ADVERTISE=[%s], want [%s] exactly", i, v, quoted)
		}
	}
}

func TestHubSettingsInitEnvWhenTheHubEnvironmentCannotBeRead(t *testing.T) {
	l := newLauncherHub(t, "127.0.0.1:7501")
	hubProcEnv = func(pid int) (map[string]string, error) { return nil, fmt.Errorf("boom: ps refused") }
	confBefore, regBefore := string(l.readConf()), string(l.readRegistry())
	listing := l.homeListing()
	code, _, errOut := l.run("settings", "init", "--set", "HUB_NAME=hub", "--env", "HUB_LEAF_ADVERTISE")
	if code != 1 || !strings.Contains(errOut, "cannot read the running hub's environment") || !strings.Contains(errOut, "boom: ps refused") || !strings.Contains(errOut, "--env HUB_LEAF_ADVERTISE=VALUE") {
		t.Errorf("--env NAME with no readable environment: exit = %d, stderr = %q, want 1, the reason and the --env NAME=VALUE form", code, errOut)
	}
	if string(l.readConf()) != confBefore || string(l.readRegistry()) != regBefore || l.homeListing() != listing {
		t.Errorf("a refused init left something behind")
	}
	code, out, errOut := l.run("settings", "init", "--set", "HUB_NAME=hub", "--env", "HUB_LEAF_ADVERTISE=127.0.0.1:7501")
	if code != 0 {
		t.Fatalf("--env NAME=VALUE: exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if got := l.settingsText(); got != "HUB_NAME: \"hub\"\n" {
		t.Errorf("settings.conf = %q, want only HUB_NAME", got)
	}
}

func TestHubSettingsInitEnvRefusesWhatItCannotUse(t *testing.T) {
	l := newLauncherHub(t, "127.0.0.1:7501")
	confBefore, regBefore := string(l.readConf()), string(l.readRegistry())
	listing := l.homeListing()
	cases := []struct {
		args []string
		code int
		want string
	}{
		{[]string{"--set", "HUB_NAME=hub", "--env", "NOT_IN_THE_CONF=1"}, 1, "does not reference $NOT_IN_THE_CONF"},
		{[]string{"--set", "HUB_NAME=hub", "--set", "HUB_LEAF_ADVERTISE=127.0.0.1:7501", "--env", "HUB_LEAF_ADVERTISE"}, 1, "both --set and --env"},
		{[]string{"--env", "HUB_LEAF_ADVERTISE"}, 1, "HUB_NAME"},
		{[]string{"--set", "HUB_NAME=hub", "--env", "1bad"}, 2, "1bad"},
		{[]string{"--set", "HUB_NAME=hub", "--env"}, 2, "--env needs"},
	}
	for _, c := range cases {
		code, _, errOut := l.run(append([]string{"settings", "init"}, c.args...)...)
		if code != c.code || !strings.Contains(errOut, c.want) {
			t.Errorf("%v: exit = %d, stderr = %q, want %d and %q", c.args, code, errOut, c.code, c.want)
		}
	}
	// The usage text names --env and says where its names come from.
	_, _, usageText := l.run("settings", "init", "--env")
	if !strings.Contains(usageText, "--env NAME[=VALUE]") || !strings.Contains(usageText, "launcher") {
		t.Errorf("the usage text does not show --env NAME[=VALUE] and that its names come from the launcher: %q", usageText)
	}
	// A name the running hub does not supply, and no value given.
	hubProcEnv = func(pid int) (map[string]string, error) { return map[string]string{"HUB_NAME": "hub"}, nil }
	code, _, errOut := l.run("settings", "init", "--set", "HUB_NAME=hub", "--env", "HUB_LEAF_ADVERTISE")
	if code != 1 || !strings.Contains(errOut, "does not supply HUB_LEAF_ADVERTISE") {
		t.Errorf("a name the hub does not supply: exit = %d, stderr = %q, want 1 and does not supply", code, errOut)
	}
	if string(l.readConf()) != confBefore || string(l.readRegistry()) != regBefore || l.homeListing() != listing {
		t.Errorf("a refused init left something behind")
	}
}

func TestHubSettingsInitEnvWithAValueRefusesAMissingNameWhenTheHubEnvironmentIsReadable(t *testing.T) {
	// The running hub's environment can be read and does not hold the name, so
	// the name is not one the launcher supplies. A value on the flag must not
	// make it one: the refusal points at --set.
	l := newLauncherHub(t, "127.0.0.1:7501")
	hubProcEnv = func(pid int) (map[string]string, error) { return map[string]string{"HUB_NAME": "hub"}, nil }
	confBefore, regBefore := string(l.readConf()), string(l.readRegistry())
	listing := l.homeListing()
	code, _, errOut := l.run("settings", "init", "--set", "HUB_NAME=hub", "--env", "HUB_LEAF_ADVERTISE=127.0.0.1:7501")
	if code != 1 || !strings.Contains(errOut, "does not supply HUB_LEAF_ADVERTISE") || !strings.Contains(errOut, "--set") {
		t.Errorf("exit = %d, stderr = %q, want 1, the does-not-supply message and a pointer to --set", code, errOut)
	}
	if string(l.readConf()) != confBefore || string(l.readRegistry()) != regBefore || l.homeListing() != listing {
		t.Errorf("a refused init left something behind")
	}
}

func TestHubSettingsInitWritesAllDigitValuesUnquoted(t *testing.T) {
	// The conf reads the leafnode port from a variable. nats-server rejects a
	// quoted string where it wants a number, so the value is written bare.
	probe := freePort(t)
	l := newLauncherHub(t, "127.0.0.1:7501", fmt.Sprintf("HUB_LEAF_PORT=%d", probe))
	code, out, errOut := l.run("settings", "init", "--set", "HUB_NAME=hub", "--set", fmt.Sprintf("HUB_LEAF_PORT=%d", probe), "--env", "HUB_LEAF_ADVERTISE")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	want := fmt.Sprintf("HUB_NAME: \"hub\"\nHUB_LEAF_PORT: %d\n", probe)
	if got := l.settingsText(); got != want {
		t.Errorf("settings.conf = %q, want %q", got, want)
	}
}

func TestHubSettingLiteralQuotesOnlyWhatIsNotAPlainNumber(t *testing.T) {
	// Director's ruling (2026-11-13): all-digit values up to 18 digits stay
	// unquoted; a leading zero, and 19 or more digits, are quoted. nats-server
	// reads a bare number as an int64, and rejects a quoted string where it
	// wants one.
	for in, want := range map[string]string{
		"7422":                `7422`,
		"0":                   `0`,
		"1073741824":          `1073741824`, // 2^30: a size limit above the int32 range
		"1234567890":          `1234567890`,
		"999999999999999999":  `999999999999999999`,    // 18 digits
		"1000000000000000000": `"1000000000000000000"`, // 19 digits: can overflow an int64
		"hub":                 `"hub"`,
		"0123":                `"0123"`, // a leading zero would change the value when read bare
		"00":                  `"00"`,
		"12a":                 `"12a"`,
		"":                    `""`,
		"1.5":                 `"1.5"`,
	} {
		if got := hubSettingLiteral(in); got != want {
			t.Errorf("hubSettingLiteral(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestHubSettingsInitWritesALargeNumberUnquotedAndTheServerAcceptsIt(t *testing.T) {
	// 1073741824 is above the int32 range. nats-server reads a bare one as an
	// int64 and rejects the quoted string, so the value is written bare and
	// init's -t run and reload accept it.
	l := newLauncherHub(t, "127.0.0.1:7501")
	conf := string(l.readConf())
	edited := strings.Replace(conf, "jetstream { ", "jetstream { max_memory_store: $HUB_MEM, ", 1)
	if edited == conf {
		t.Fatal("setup: the conf edit did not apply")
	}
	if err := os.WriteFile(l.conf, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	l.restart(t, "127.0.0.1:7501", "HUB_MEM=1073741824")
	code, out, errOut := l.run("settings", "init", "--set", "HUB_NAME=hub", "--set", "HUB_MEM=1073741824", "--env", "HUB_LEAF_ADVERTISE")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if got, want := l.settingsText(), "HUB_NAME: \"hub\"\nHUB_MEM: 1073741824\n"; got != want {
		t.Errorf("settings.conf = %q, want %q", got, want)
	}
}

// --- the other verbs ----------------------------------------------------------------------------

func TestHubUserAddWorksOnAHubWhoseLauncherSuppliesAName(t *testing.T) {
	l := newLauncherHub(t, "127.0.0.1:7501")
	if code, out, errOut := l.run("settings", "init", "--set", "HUB_NAME=hub", "--env", "HUB_LEAF_ADVERTISE"); code != 0 {
		t.Fatalf("setup: init exited %d\n%s%s", code, out, errOut)
	}
	code, out, errOut := l.run("user", "add", "observer.director", "--class", "observer", "--param", "holder=director")
	if code != 0 {
		t.Fatalf("add exit = %d, want 0 (the launcher's name is in the -t environment)\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if code, out, _ := l.run("settings", "check"); code != 0 {
		t.Errorf("check after add: exit = %d\n%s", code, out)
	}
	l.assertNoSeedAnywhere(out, errOut)
}

func TestHubUserAddStillRefusesWhenTheLauncherNameCannotBeSeen(t *testing.T) {
	l := newLauncherHub(t, "127.0.0.1:7501")
	if code, out, errOut := l.run("settings", "init", "--set", "HUB_NAME=hub", "--env", "HUB_LEAF_ADVERTISE"); code != 0 {
		t.Fatalf("setup: init exited %d\n%s%s", code, out, errOut)
	}
	hubProcEnv = func(pid int) (map[string]string, error) { return nil, fmt.Errorf("boom: ps refused") }
	confBefore, regBefore := string(l.readConf()), string(l.readRegistry())
	listing := l.homeListing()
	code, _, errOut := l.run("user", "add", "obs", "--class", "observer", "--param", "holder=director")
	if code != 1 || !strings.Contains(errOut, "the check cannot see the hub's own configuration") || !strings.Contains(errOut, "HUB_LEAF_ADVERTISE") {
		t.Errorf("exit = %d, stderr = %q, want 1, the control's message and the name", code, errOut)
	}
	if string(l.readConf()) != confBefore || string(l.readRegistry()) != regBefore || l.homeListing() != listing {
		t.Errorf("a refused add left something behind")
	}
}

// --- reading the environment --------------------------------------------------------------------

func TestParseProcEnvReadsTheWordsAfterTheCommandLine(t *testing.T) {
	got := parseProcEnv(`/opt/homebrew/bin/nats-server -c /x/y.conf -P /x/pid HUB_NAME=hub HUB_LEAF_ADVERTISE="127.0.0.1:7501" PATH=/usr/bin A_B=c=d`)
	want := map[string]string{"HUB_NAME": "hub", "HUB_LEAF_ADVERTISE": `"127.0.0.1:7501"`, "PATH": "/usr/bin", "A_B": "c=d"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("parseProcEnv[%s] = %q, want %q (quotes are part of the value)", k, got[k], v)
		}
	}
	if _, ok := got["-c"]; ok || len(got) != len(want) {
		t.Errorf("parseProcEnv = %v, want exactly %v (the command line is not environment)", got, want)
	}
}

// TestHubProcEnvReadsTheRealPs runs the real ps on a child process. It cannot
// run under sandbox-exec (ps is setuid), so a sandboxed run skips it with
// -skip TestHubProcEnvReadsTheRealPs and it runs unsandboxed in CI.
func TestHubProcEnvReadsTheRealPs(t *testing.T) {
	// The real server, as the hub is: macOS shows no environment for a system
	// binary such as sleep (a platform binary under SIP), and nats-server is not one.
	bin, err := exec.LookPath("nats-server")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-a", "127.0.0.1", "-p", "-1", "-l", filepath.Join(t.TempDir(), "probe.log"))
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), `LAUNCH_PROBE="a:b"`, "LAUNCH_PLAIN=xyz"}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	var got map[string]string
	for i := 0; i < 30; i++ { // the process is read once it has exec'd
		if got, err = realHubProcEnv(cmd.Process.Pid); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("realHubProcEnv: %v", err)
	}
	if got["LAUNCH_PROBE"] != `"a:b"` || got["LAUNCH_PLAIN"] != "xyz" {
		t.Errorf("realHubProcEnv = %v, want LAUNCH_PROBE=\"a:b\" (quotes kept) and LAUNCH_PLAIN=xyz", got)
	}
}

// --- the pid guard and a hub that is not there --------------------------------------------------

func TestHubLauncherEnvironmentIsNeverReadFromAProcessThatIsNotThisHub(t *testing.T) {
	l := newLauncherHub(t, "127.0.0.1:7501")
	hubProcArgs = func(pid int) (string, error) { return "/usr/bin/other-daemon -c /elsewhere/other.conf", nil }
	calls := 0
	hubProcEnv = func(pid int) (map[string]string, error) {
		calls++
		return map[string]string{"HUB_NAME": "hub", "HUB_LEAF_ADVERTISE": "127.0.0.1:7501"}, nil
	}
	confBefore, regBefore := string(l.readConf()), string(l.readRegistry())
	listing := l.homeListing()
	code, out, errOut := l.run("settings", "check")
	if code != 1 || !strings.Contains(out, "UNDEFINED HUB_LEAF_ADVERTISE") || !strings.Contains(out+errOut, "is not this hub's nats-server") {
		t.Errorf("check: exit = %d, stdout = %q, stderr = %q, want 1, UNDEFINED and the guard's reason", code, out, errOut)
	}
	code, _, errOut = l.run("settings", "init", "--set", "HUB_NAME=hub", "--env", "HUB_LEAF_ADVERTISE")
	if code != 1 || !strings.Contains(errOut, "is not this hub's nats-server") {
		t.Errorf("init --env NAME: exit = %d, stderr = %q, want 1 and the guard's reason", code, errOut)
	}
	if calls != 0 {
		t.Errorf("hubProcEnv was called %d time(s) for a process that is not this hub's nats-server -c <conf>", calls)
	}
	if string(l.readConf()) != confBefore || string(l.readRegistry()) != regBefore || l.homeListing() != listing {
		t.Errorf("a refused init left something behind")
	}
}

func TestHubLauncherEnvironmentWithNoHubRunning(t *testing.T) {
	for _, how := range []string{"no pid file", "a dead pid"} {
		l := newLauncherHub(t, "127.0.0.1:7501")
		calls := 0
		old := hubProcEnv
		hubProcEnv = func(pid int) (map[string]string, error) { calls++; return old(pid) }
		if how == "no pid file" {
			if err := os.Remove(l.pidfile); err != nil {
				t.Fatal(err)
			}
		} else {
			dead := exec.Command("true")
			if err := dead.Run(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(l.pidfile, []byte(fmt.Sprint(dead.Process.Pid)+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		confBefore, regBefore := string(l.readConf()), string(l.readRegistry())
		listing := l.homeListing()
		code, out, _ := l.run("settings", "check")
		if code != 1 || !strings.Contains(out, "UNDEFINED HUB_LEAF_ADVERTISE") || !strings.Contains(out, "running hub's environment") {
			t.Errorf("%s: check exit = %d, stdout = %q, want 1, UNDEFINED and a note about the environment", how, code, out)
		}
		code, _, errOut := l.run("settings", "init", "--set", "HUB_NAME=hub", "--env", "HUB_LEAF_ADVERTISE")
		if code != 1 || !strings.Contains(errOut, "cannot read the running hub's environment") || !strings.Contains(errOut, "--env HUB_LEAF_ADVERTISE=VALUE") {
			t.Errorf("%s: init --env NAME: exit = %d, stderr = %q, want 1, the reason and the --env NAME=VALUE form", how, code, errOut)
		}
		// init needs a hub to reload and to answer as admin, so with none it
		// still writes nothing; --env NAME=VALUE gets past the environment read
		// and stops at the pid check, with the same message init always gave.
		code, _, errOut = l.run("settings", "init", "--set", "HUB_NAME=hub", "--env", "HUB_LEAF_ADVERTISE=127.0.0.1:7501")
		if code != 1 || !strings.Contains(errOut, "Nothing was changed") || strings.Contains(errOut, "environment") {
			t.Errorf("%s: init --env NAME=VALUE: exit = %d, stderr = %q, want 1, Nothing was changed, and no complaint about the environment", how, code, errOut)
		}
		if calls != 0 {
			t.Errorf("%s: hubProcEnv was called %d time(s) with no hub running", how, calls)
		}
		if string(l.readConf()) != confBefore || string(l.readRegistry()) != regBefore || l.homeListing() != listing {
			t.Errorf("%s: a refused init left something behind", how)
		}
	}
}

// --- adopt --------------------------------------------------------------------------------------

func TestHubUserAdoptWorksOnAHubWhoseLauncherSuppliesANameAndNeedsNoEnvironment(t *testing.T) {
	pubA, pubB := newPublicKey(t), newPublicKey(t)
	hand := func(pub, extra string) string {
		return `      { nkey: ` + pub + `
        permissions {
          publish   { allow: [ "global.probe.>"` + extra + ` ] }
          subscribe { allow: [ "global.probe.>", "_INBOX.>" ] }
        } }`
	}
	l := newLauncherHubWith(t, hand(pubA, "")+"\n"+hand(pubB, ""), "127.0.0.1:7501")
	// Adopt writes no conf and runs no -t, so it works on this hub as it is.
	// This case passes before the change and pins that it keeps passing.
	if code, out, errOut := l.run("user", "adopt", pubA, "--name", "probe-a", "--class", "leaf", "--param", "cluster=probe"); code != 0 {
		t.Fatalf("adopt before init: exit = %d\n%s%s", code, out, errOut)
	}
	if code, out, errOut := l.run("settings", "init", "--set", "HUB_NAME=hub", "--env", "HUB_LEAF_ADVERTISE"); code != 0 {
		t.Fatalf("setup: init exited %d\n%s%s", code, out, errOut)
	}
	// With the hub's environment unreadable, adopt still works: it reads none.
	hubProcEnv = func(pid int) (map[string]string, error) { return nil, fmt.Errorf("boom: ps refused") }
	confBefore := string(l.readConf())
	code, out, errOut := l.run("user", "adopt", pubB, "--name", "probe-b", "--class", "leaf", "--param", "cluster=probe")
	if code != 0 {
		t.Fatalf("adopt after init with no readable environment: exit = %d\n%s%s", code, out, errOut)
	}
	if string(l.readConf()) != confBefore {
		t.Errorf("adopt wrote the conf")
	}
	code, out, _ = l.run("user", "list")
	if code != 0 || strings.Contains(out, "DRIFT") || !strings.Contains(out, "probe-a") || !strings.Contains(out, "probe-b") {
		t.Errorf("list after two adopts: exit = %d\n%s", code, out)
	}
}
