package main

// Scratch-hub harness for the hub user tests. Every hub here is a real
// nats-server started by the test in its own home directory, under the same
// shape as a deployment's: a conf that includes settings.conf and references
// one variable the server must resolve, a managed region inside the account's
// users list, an admin key, a pid file and a log. The server is started with a
// bare environment (PATH only), as a service manager starts one, so a reference
// that settings.conf does not define cannot resolve. Nothing here touches a live
// hub: the ports are random on 127.0.0.1 and the homes are t.TempDir().
//
// A host without nats-server fails these tests; it does not skip them.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"
)

const (
	hubRegionBegin = "# BEGIN director managed users"
	hubRegionEnd   = "# END director managed users"
)

type hubScratchOpts struct {
	noSettings   bool   // no include and no settings.conf; the conf still references $HUB_NAME
	noRegion     bool   // an old-style conf: no managed region
	extraUsers   string // hand-written user blocks placed outside the managed region
	settingsTxt  string // replaces the default settings.conf body
	lateSettings string // appended to settings.conf after the server is up (a server will not start with it)
	refTag       bool   // the conf also references $HUB_TAG, which the default settings.conf does not define
	brokenConf   bool   // the live conf has a syntax error; the server runs from a valid sibling conf
	adminOutside bool   // the admin seed lives outside keys/, and keys/ does not exist
	serverEnv    []string
	jsVar        bool   // with noSettings: the conf reads store_dir from $JS_DIR, which the server gets in its environment
	badMonitor   bool   // the command's monitor URL points at a closed port, so /varz cannot be read
	leafListen   bool   // the hub also accepts leafnode connections, on h.leafPort
	jsDomain     string // the hub's JetStream domain; empty is "global", "-" is none
}

type hubScratch struct {
	envOverride map[string]string // settings the command reads, replacing the defaults
	t           *testing.T
	home        string
	conf        string
	settings    string
	keys        string
	pidfile     string
	logfile     string
	port        int
	monPort     int
	adminSeed   string
	adminPub    string
	cmd         *exec.Cmd
	badMonitor  bool
	leafPort    int
}

// newSeedFile mints a user key into path (0600) and returns its public key.
func newSeedFile(t *testing.T, path string) string {
	t.Helper()
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := kp.Seed()
	if err := os.WriteFile(path, append(seed, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	pub, _ := kp.PublicKey()
	return pub
}

func newPublicKey(t *testing.T) string {
	t.Helper()
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := kp.PublicKey()
	return pub
}

func newHubScratch(t *testing.T, o hubScratchOpts) *hubScratch {
	t.Helper()
	bin, err := exec.LookPath("nats-server")
	if err != nil {
		// These tests are the proof the hub commands work, so a host without the
		// server fails them instead of passing by skipping. CI installs nats-server.
		t.Fatalf("nats-server is not on PATH; the hub user tests need it (CI installs one)")
	}
	h := &hubScratch{t: t, badMonitor: o.badMonitor}
	h.home = t.TempDir()
	h.conf = filepath.Join(h.home, "nats-server.conf")
	h.settings = filepath.Join(h.home, "settings.conf")
	h.keys = filepath.Join(h.home, "keys")
	h.pidfile = filepath.Join(h.home, "nats-server.pid")
	h.logfile = filepath.Join(h.home, "log", "nats-server.log")
	h.port, h.monPort = freePort(t), freePort(t)
	dirs := []string{filepath.Dir(h.logfile), filepath.Join(h.home, "js")}
	if !o.adminOutside {
		dirs = append(dirs, h.keys)
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if o.adminOutside {
		h.adminSeed = filepath.Join(t.TempDir(), "admin-elsewhere.nk")
	} else {
		h.adminSeed = filepath.Join(h.keys, "admin.nk")
	}
	h.adminPub = newSeedFile(t, h.adminSeed)

	include := `include "./settings.conf"` + "\n"
	if o.noSettings {
		include = ""
	}
	tag := ""
	if o.refTag {
		tag = "server_tags: [$HUB_TAG]\n"
	}
	region := hubRegionBegin + "\n      " + hubRegionEnd + "\n"
	if o.noRegion {
		region = ""
	}
	domainClause := ", domain: global"
	switch o.jsDomain {
	case "":
	case "-":
		domainClause = ""
	default:
		domainClause = ", domain: " + o.jsDomain
	}
	leafLine := ""
	if o.leafListen {
		h.leafPort = freePort(t)
		leafLine = fmt.Sprintf("leafnodes { listen: 127.0.0.1:%d }\n", h.leafPort)
	}
	storeDir := fmt.Sprintf("%q", filepath.Join(h.home, "js"))
	if o.jsVar {
		storeDir = "$JS_DIR"
	}
	body := fmt.Sprintf(`%sserver_name: $HUB_NAME
%slisten: 127.0.0.1:%d
http: 127.0.0.1:%d
jetstream { store_dir: %s%s }
%saccounts {
  FLEET {
    jetstream: enabled
    users: [
      { nkey: %s }
%s
      %s
    ]
  }
}
`, include, tag, h.port, h.monPort, storeDir, domainClause, leafLine, h.adminPub, o.extraUsers, region)
	if err := os.WriteFile(h.conf, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if !o.noSettings {
		st := o.settingsTxt
		if st == "" {
			st = "HUB_NAME: \"from-settings\"\n"
		}
		if err := os.WriteFile(h.settings, []byte(st), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if o.noSettings && !o.brokenConf {
		// A real old hub: the server runs from the conf the command reads, with
		// the variable it references in its own environment. A reload then reads
		// the conf the command wrote, so a test can tell the new conf went live.
		env := o.serverEnv
		hasName := false
		for _, e := range env {
			if strings.HasPrefix(e, "HUB_NAME=") {
				hasName = true
			}
		}
		if !hasName {
			env = append(append([]string{}, env...), "HUB_NAME=hub")
		}
		if o.jsVar {
			env = append(append([]string{}, env...), "JS_DIR="+filepath.Join(h.home, "js"))
		}
		h.startServer(bin, h.conf, env)
	} else if o.brokenConf {
		// The server cannot read a conf the test made unreadable on purpose, so the
		// scratch hub is started from a sibling conf that is valid. The conf under
		// test stays the one the command reads.
		alt := filepath.Join(h.home, "serve.conf")
		altBody := strings.Replace(body, "server_name: $HUB_NAME", "server_name: hub", 1)
		altBody = strings.Replace(altBody, "include \"./settings.conf\"\n", "", 1)
		if err := os.WriteFile(alt, []byte(altBody), 0o600); err != nil {
			t.Fatal(err)
		}
		h.startServer(bin, alt, o.serverEnv)
	} else {
		h.startServer(bin, h.conf, o.serverEnv)
	}
	if o.brokenConf {
		if err := os.WriteFile(h.conf, []byte(body+"\n]\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	h.provision()
	// The pid guard reads the server's command line. Under sandbox-exec the
	// setuid ps cannot run, so the scratch hub answers for its own pid and any
	// other pid reads as an unrelated process; TestHubPidGuardReadsTheRealPs
	// exercises the real ps.
	oldArgs := hubProcArgs
	hubProcArgs = func(pid int) (string, error) {
		if h.cmd != nil && h.cmd.Process != nil && pid == h.cmd.Process.Pid {
			return "nats-server -c " + h.conf + " -P " + h.pidfile, nil
		}
		return "sleep 60", nil
	}
	t.Cleanup(func() { hubProcArgs = oldArgs })
	// A scratch hub's environment is not read unless a test opts in. The server
	// is started with a controlled environment, and a conf that references a name
	// the server has but settings.conf does not is the case the add and check
	// tests above build on: the name is undefined for the command. With the real
	// ps the command would find it in the server's environment and treat it as
	// supplied by a launcher (hubuser_launcher_test.go tests that case), so these
	// tests state that the environment cannot be read.
	oldEnv := hubProcEnv
	hubProcEnv = func(pid int) (map[string]string, error) {
		return nil, fmt.Errorf("the scratch hub's environment is not read in this test")
	}
	t.Cleanup(func() { hubProcEnv = oldEnv })
	if o.lateSettings != "" {
		b, _ := os.ReadFile(h.settings)
		if err := os.WriteFile(h.settings, append(b, []byte(o.lateSettings)...), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

func (h *hubScratch) startServer(bin, conf string, extraEnv []string) {
	t := h.t
	t.Helper()
	cmd := exec.Command(bin, "-c", conf, "-P", h.pidfile, "-l", h.logfile)
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH")}, extraEnv...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	h.cmd = cmd
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	for i := 0; i < 60; i++ {
		if nc, err := h.connectAs(h.adminSeed, ""); err == nil {
			nc.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("scratch hub did not come up")
}

func (h *hubScratch) url() string { return fmt.Sprintf("nats://127.0.0.1:%d", h.port) }

func (h *hubScratch) connectAs(seedPath, inboxPrefix string) (*nats.Conn, error) {
	opt, err := nats.NkeyOptionFromSeed(seedPath)
	if err != nil {
		return nil, err
	}
	opts := []nats.Option{opt, nats.Timeout(2 * time.Second), nats.MaxReconnects(0)}
	if inboxPrefix != "" {
		opts = append(opts, nats.CustomInboxPrefix(inboxPrefix))
	}
	return nats.Connect(h.url(), opts...)
}

// provision gives the hub one stream and one durable with mail, so the observer
// verify has something whose consumer count must not change.
func (h *hubScratch) provision() {
	t := h.t
	nc, err := h.connectAs(h.adminSeed, "")
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := cctx()
	defer cancel()
	st, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: "GLOBAL_TO_probe", Subjects: []string{"global.probe.>"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{Durable: "mcp_global_probe_01AAA", FilterSubject: "global.probe.supervisor.inbox", AckPolicy: jetstream.AckExplicitPolicy}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := js.Publish(ctx, "global.probe.supervisor.inbox", []byte("m")); err != nil {
			t.Fatal(err)
		}
	}
}

// env is the command's settings for this hub. The admin key is the default
// keys/admin.nk unless the hub keeps it elsewhere.
func (h *hubScratch) env() func(string) string {
	m := map[string]string{
		"DIRECTOR_GLOBAL_HOME":     h.home,
		"DIRECTOR_HUB_ADMIN_URL":   h.url(),
		"DIRECTOR_HUB_MONITOR_URL": fmt.Sprintf("http://127.0.0.1:%d", h.monPort),
	}
	if h.badMonitor {
		m["DIRECTOR_HUB_MONITOR_URL"] = "http://127.0.0.1:1" // port 1: nothing listens there
	}
	if filepath.Dir(h.adminSeed) != h.keys {
		m["DIRECTOR_HUB_ADMIN_NKEY"] = h.adminSeed
	}
	for k, v := range h.envOverride {
		m[k] = v
	}
	return func(k string) string { return m[k] }
}

// run runs one hub command and returns its exit code, stdout and stderr.
func (h *hubScratch) run(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := runHubCmd(args, h.env(), &out, &errb)
	return code, out.String(), errb.String()
}

func (h *hubScratch) readConf() []byte {
	b, err := os.ReadFile(h.conf)
	if err != nil {
		h.t.Fatal(err)
	}
	return b
}

func (h *hubScratch) readRegistry() []byte {
	b, err := os.ReadFile(filepath.Join(h.home, "users.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		h.t.Fatal(err)
	}
	return b
}

// registryUsers decodes the registry's users, with only the fields the tests
// pin: name, class, public_key and state.
func (h *hubScratch) registryUsers() []map[string]any {
	b := h.readRegistry()
	if b == nil {
		return nil
	}
	var f struct {
		Users []map[string]any `json:"users"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		h.t.Fatalf("users.json is not JSON: %v\n%s", err, b)
	}
	return f.Users
}

// regionOf returns the text between the managed region's markers.
func regionOf(conf []byte) string {
	s := string(conf)
	i := strings.Index(s, hubRegionBegin)
	j := strings.Index(s, hubRegionEnd)
	if i < 0 || j < i {
		return ""
	}
	return s[i+len(hubRegionBegin) : j]
}

// outsideRegion is the conf with the region's content removed, to show that
// bytes outside the two marker lines are untouched.
func outsideRegion(conf []byte) string {
	s := string(conf)
	i := strings.Index(s, hubRegionBegin)
	j := strings.Index(s, hubRegionEnd)
	if i < 0 || j < i {
		return s
	}
	return s[:i+len(hubRegionBegin)] + s[j:]
}

func (h *hubScratch) backups() []string {
	m, _ := filepath.Glob(filepath.Join(h.home, "nats-server.conf.bak-*"))
	return m
}

// seedFiles lists the *.nk files directly in keys/.
func (h *hubScratch) seedFiles() []string {
	m, _ := filepath.Glob(filepath.Join(h.keys, "*.nk"))
	return m
}

func (h *hubScratch) revokedSeeds() []string {
	m, _ := filepath.Glob(filepath.Join(h.keys, "revoked", "*.nk"))
	return m
}

// consumerCounts reads each stream's consumer count as admin.
func (h *hubScratch) consumerCounts() string {
	t := h.t
	nc, err := h.connectAs(h.adminSeed, "")
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := cctx()
	defer cancel()
	var b strings.Builder
	l := js.ListStreams(ctx)
	for si := range l.Info() {
		fmt.Fprintf(&b, "%s %d\n", si.Config.Name, si.State.Consumers)
	}
	if l.Err() != nil {
		t.Fatal(l.Err())
	}
	return b.String()
}

// serverName reads the running server's name from its monitor port.
func (h *hubScratch) serverName() string {
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/varz", h.monPort))
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var v struct {
		Name string `json:"server_name"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		h.t.Fatal(err)
	}
	return v.Name
}

// canConnect reports whether the key in seedPath is accepted by the hub now.
func (h *hubScratch) canConnect(seedPath string) bool {
	nc, err := h.connectAs(seedPath, "")
	if err != nil {
		return false
	}
	nc.Close()
	return true
}

// waitConnect polls canConnect for up to d, for a key the hub should accept
// once a reload has taken effect.
func (h *hubScratch) waitConnect(seedPath string, d time.Duration) bool {
	end := time.Now().Add(d)
	for {
		if h.canConnect(seedPath) {
			return true
		}
		if time.Now().After(end) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (h *hubScratch) pid() int {
	b, err := os.ReadFile(h.pidfile)
	if err != nil {
		h.t.Fatal(err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		h.t.Fatal(err)
	}
	return n
}

// reloads counts the server's own "Reloaded server configuration" log lines.
// A reload by SIGHUP writes one; a restart writes none.
func (h *hubScratch) reloads() int {
	b, _ := os.ReadFile(h.logfile)
	return strings.Count(string(b), "Reloaded server configuration")
}

// waitReloads waits until the log shows at least n reloads.
func (h *hubScratch) waitReloads(n int) bool {
	for i := 0; i < 50; i++ {
		if h.reloads() >= n {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

var seedShape = regexp.MustCompile(`S[UAOCNX][A-Z2-7]{56}`)

// assertNoSeedAnywhere fails the test if any seed this hub's keys hold, or
// anything shaped like a seed, appears in the command's output, the registry,
// the conf, settings.conf, any backup or the hub's log.
func (h *hubScratch) assertNoSeedAnywhere(out, errOut string) {
	t := h.t
	t.Helper()
	haystacks := map[string]string{"stdout": out, "stderr": errOut, "registry": string(h.readRegistry()), "conf": string(h.readConf())}
	if b, err := os.ReadFile(h.settings); err == nil {
		haystacks["settings.conf"] = string(b)
	}
	for _, p := range h.backups() {
		b, _ := os.ReadFile(p)
		haystacks[filepath.Base(p)] = string(b)
	}
	if b, err := os.ReadFile(h.logfile); err == nil {
		haystacks["hub log"] = string(b)
	}
	for _, p := range append(append([]string{}, h.seedFiles()...), h.revokedSeeds()...) {
		b, _ := os.ReadFile(p)
		seed := strings.TrimSpace(string(b))
		if seed == "" {
			continue
		}
		for name, s := range haystacks {
			if strings.Contains(s, seed) {
				t.Errorf("the seed in %s appears in %s", filepath.Base(p), name)
			}
		}
	}
	for name, s := range haystacks {
		if seedShape.MatchString(s) {
			t.Errorf("something shaped like a seed appears in %s", name)
		}
	}
}

// homeListing names every entry a command could have left in the hub home or
// its keys directory: top-level files and directories (not the server's own js
// and log, and not the lock file) and the key files. Two listings that match
// show a refused command left nothing behind, staging files included.
func (h *hubScratch) homeListing() string {
	var names []string
	ents, _ := os.ReadDir(h.home)
	for _, e := range ents {
		switch e.Name() {
		case "js", "log", ".hub-user.lock":
			continue
		}
		names = append(names, e.Name())
	}
	kents, _ := os.ReadDir(h.keys)
	for _, e := range kents {
		names = append(names, "keys/"+e.Name())
	}
	return strings.Join(names, "\n")
}
