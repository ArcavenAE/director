package main

// The verbs: user add, user adopt, user list, settings check, settings init.
// Every verb that changes the hub follows one sequence: take the lock, test the
// conf as it stands, render a candidate, test the candidate before anything is
// written, back up, rename the candidate over the conf, reload by SIGHUP, verify,
// and on any later failure restore the backup and reload again.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"
)

// --- registry -----------------------------------------------------------------------------------

type hubUser struct {
	Name      string            `json:"name"`
	Class     string            `json:"class"`
	Params    map[string]string `json:"params,omitempty"`
	PublicKey string            `json:"public_key"`
	State     string            `json:"state"`    // active or adopted
	Location  string            `json:"location"` // managed region or outside the region
	Created   string            `json:"created"`
}

// hubRegistry is users.json. It holds names, classes, parameters and public
// keys, and never a seed.
type hubRegistry struct {
	Version int       `json:"version"`
	Users   []hubUser `json:"users"`
}

func loadRegistry(cfg hubCfg) (hubRegistry, error) {
	b, err := os.ReadFile(cfg.registry)
	if os.IsNotExist(err) {
		return hubRegistry{Version: 1}, nil
	}
	if err != nil {
		return hubRegistry{}, err
	}
	var r hubRegistry
	if err := json.Unmarshal(b, &r); err != nil {
		return hubRegistry{}, fmt.Errorf("%s is not valid JSON: %v", cfg.registry, err)
	}
	if r.Version == 0 {
		r.Version = 1
	}
	return r, nil
}

func saveRegistry(cfg hubCfg, r hubRegistry) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(cfg.registry, append(b, '\n'), 0o600)
}

func (r hubRegistry) find(name string) *hubUser {
	for i := range r.Users {
		if r.Users[i].Name == name {
			return &r.Users[i]
		}
	}
	return nil
}

func (r hubRegistry) findKey(pub string) *hubUser {
	for i := range r.Users {
		if r.Users[i].PublicKey == pub {
			return &r.Users[i]
		}
	}
	return nil
}

// --- argument parsing ---------------------------------------------------------------------------

type hubUserArgs struct {
	name      string
	class     string
	params    map[string]string
	publicKey string
	asJSON    bool
	positKey  string // adopt's PUBLIC_KEY
}

// parseUserArgs reads the words after the verb. positional is how many bare
// words come first (1 for add's NAME and adopt's PUBLIC_KEY, 0 for list).
func parseUserArgs(verb string, args []string) (hubUserArgs, error) {
	var a hubUserArgs
	var paramVals []string
	positional := 0
	if verb == "add" || verb == "adopt" {
		positional = 1
	}
	i := 0
	for i < len(args) && positional > 0 && !strings.HasPrefix(args[i], "--") {
		if verb == "add" {
			a.name = args[i]
		} else {
			a.positKey = args[i]
		}
		positional--
		i++
	}
	for ; i < len(args); i++ {
		w := args[i]
		val := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", w)
			}
			i++
			return args[i], nil
		}
		var err error
		var v string
		switch w {
		case "--class":
			if v, err = val(); err == nil {
				a.class = v
			}
		case "--param":
			if v, err = val(); err == nil {
				paramVals = append(paramVals, v)
			}
		case "--public-key":
			if v, err = val(); err == nil {
				a.publicKey = v
			}
		case "--name":
			if v, err = val(); err == nil {
				a.name = v
			}
		case "--json":
			a.asJSON = true
		default:
			return a, fmt.Errorf("unknown argument %q", w)
		}
		if err != nil {
			return a, err
		}
	}
	var err error
	if a.params, err = parseParams(paramVals); err != nil {
		return a, err
	}
	switch verb {
	case "add":
		if a.name == "" {
			return a, fmt.Errorf("add needs a NAME")
		}
		if a.class == "" {
			return a, fmt.Errorf("add needs --class")
		}
	case "adopt":
		if a.positKey == "" {
			return a, fmt.Errorf("adopt needs the PUBLIC_KEY of a user already in the conf")
		}
		if a.name == "" {
			return a, fmt.Errorf("adopt needs --name")
		}
		if a.class == "" {
			return a, fmt.Errorf("adopt needs --class")
		}
	case "list":
		if a.name != "" || a.class != "" || a.publicKey != "" || len(a.params) > 0 {
			return a, fmt.Errorf("list takes only --json")
		}
	}
	if a.name != "" && !hubNameRe.MatchString(a.name) {
		return a, fmt.Errorf("the name %q is not a name: letters, digits, dot, dash and underscore, starting with a letter or digit", a.name)
	}
	return a, nil
}

func usageErr(stderr io.Writer, msg string) int {
	fmt.Fprintf(stderr, "hub: %s\n%s\n", msg, hubUsage)
	return 2
}

// --- the shared steps ---------------------------------------------------------------------------

// stageCandidate writes the candidate files into a staging directory inside the
// hub home and links every other entry of the home into it, so a relative
// include or path in the conf resolves as it does for the live conf. It returns
// the candidate conf's path and a cleanup that removes the staging directory.
func stageCandidate(cfg hubCfg, files map[string][]byte) (confPath string, cleanup func(), err error) {
	dir, err := os.MkdirTemp(cfg.home, ".stage-")
	if err != nil {
		return "", nil, fmt.Errorf("cannot stage the candidate conf in %s: %v", cfg.home, err)
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	ents, _ := os.ReadDir(cfg.home)
	for _, e := range ents {
		n := e.Name()
		if _, replaced := files[n]; replaced || n == filepath.Base(dir) || n == ".hub-user.lock" {
			continue
		}
		if err := os.Symlink(filepath.Join(cfg.home, n), filepath.Join(dir, n)); err != nil {
			cleanup()
			return "", nil, err
		}
	}
	for n, data := range files {
		if err := os.WriteFile(filepath.Join(dir, n), data, 0o600); err != nil {
			cleanup()
			return "", nil, err
		}
	}
	return filepath.Join(dir, filepath.Base(cfg.conf)), cleanup, nil
}

func reloadHub(cfg hubCfg) error {
	b, err := os.ReadFile(cfg.pidfile)
	if err != nil {
		return fmt.Errorf("cannot read the hub's pid file %s: %v", cfg.pidfile, err)
	}
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(b)), "%d", &pid); err != nil || pid <= 1 {
		return fmt.Errorf("the pid file %s does not hold a pid", cfg.pidfile)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return fmt.Errorf("the hub's pid %d is not running: %v", pid, err)
	}
	if err := syscall.Kill(pid, syscall.SIGHUP); err != nil {
		return fmt.Errorf("cannot signal pid %d: %v", pid, err)
	}
	return nil
}

func dialHub(cfg hubCfg, seedPath, inboxPrefix string, onErr nats.ErrHandler) (*nats.Conn, error) {
	opt, err := nats.NkeyOptionFromSeed(seedPath)
	if err != nil {
		return nil, fmt.Errorf("cannot read the key %s: %v", seedPath, err)
	}
	opts := []nats.Option{opt, nats.Name("director-hub-user"), nats.Timeout(3 * time.Second), nats.MaxReconnects(0)}
	if inboxPrefix != "" {
		opts = append(opts, nats.CustomInboxPrefix(inboxPrefix))
	}
	if cfg.ca != "" {
		opts = append(opts, nats.RootCAs(cfg.ca))
	}
	if onErr != nil {
		opts = append(opts, nats.ErrorHandler(onErr))
	}
	return nats.Connect(cfg.adminURL, opts...)
}

// awaitConnect retries a connection for up to d, for a key the hub should
// accept once its reload has taken effect.
func awaitConnect(cfg hubCfg, seedPath, inboxPrefix string, d time.Duration, onErr nats.ErrHandler) (*nats.Conn, error) {
	end := time.Now().Add(d)
	for {
		nc, err := dialHub(cfg, seedPath, inboxPrefix, onErr)
		if err == nil {
			return nc, nil
		}
		if time.Now().After(end) {
			return nil, err
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// awaitRefused waits until the hub refuses the key, after a reload that removed it.
func awaitRefused(cfg hubCfg, seedPath string, d time.Duration) bool {
	end := time.Now().Add(d)
	for {
		nc, err := dialHub(cfg, seedPath, "", nil)
		if err != nil {
			return true
		}
		nc.Close()
		if time.Now().After(end) {
			return false
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// consumerCounts reads each stream's consumer count as admin, as sorted lines.
func consumerCounts(nc *nats.Conn) (string, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return "", err
	}
	ctx, cancel := cctxHub()
	defer cancel()
	var lines []string
	l := js.ListStreams(ctx)
	for si := range l.Info() {
		lines = append(lines, fmt.Sprintf("%s %d", si.Config.Name, si.State.Consumers))
	}
	if err := l.Err(); err != nil {
		return "", err
	}
	sortStrings(lines)
	return strings.Join(lines, "\n"), nil
}

// connSet is what the monitor says is connected: client connections by id and
// leaf connections by name and address.
func connSet(monitorURL string) (map[string]bool, error) {
	get := func(path string, into any) error {
		c := http.Client{Timeout: 3 * time.Second}
		resp, err := c.Get(strings.TrimRight(monitorURL, "/") + path)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("%s answered %s", path, resp.Status)
		}
		return json.NewDecoder(resp.Body).Decode(into)
	}
	var cz struct {
		Conns []struct {
			Cid uint64 `json:"cid"`
		} `json:"connections"`
	}
	if err := get("/connz?limit=4096", &cz); err != nil {
		return nil, err
	}
	var lz struct {
		Leafs []struct {
			Name string `json:"name"`
			IP   string `json:"ip"`
			Port int    `json:"port"`
		} `json:"leafs"`
	}
	if err := get("/leafz", &lz); err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, c := range cz.Conns {
		out[fmt.Sprintf("client %d", c.Cid)] = true
	}
	for _, l := range lz.Leafs {
		out[fmt.Sprintf("leaf %s %s:%d", l.Name, l.IP, l.Port)] = true
	}
	return out, nil
}

// hubPreflight runs the checks every changing verb makes before it writes
// anything: the nats-server binary, then the unchanged-conf control.
func hubPreflight(cfg hubCfg, stderr io.Writer) (bin string, code int) {
	bin, _, err := natsServer()
	if err != nil {
		fmt.Fprintf(stderr, "hub: %v\n", err)
		return "", 1
	}
	if err := unchangedConfControl(cfg, bin); err != nil {
		fmt.Fprintf(stderr, "hub: %v\n", err)
		return "", 1
	}
	return bin, 0
}

// --- user add -----------------------------------------------------------------------------------

func hubUserAdd(cfg hubCfg, args []string, stdout, stderr io.Writer) int {
	a, err := parseUserArgs("add", args)
	if err != nil {
		return usageErr(stderr, err.Error())
	}
	tmplText, want, err := classTemplate(a.class)
	if err != nil {
		return usageErr(stderr, err.Error())
	}
	_ = tmplText
	_ = want
	if _, err := renderClass(a.class, "PLACEHOLDER", a.params); err != nil {
		return usageErr(stderr, err.Error())
	}
	if a.publicKey != "" && !nkeys.IsValidPublicUserKey(a.publicKey) {
		return usageErr(stderr, fmt.Sprintf("--public-key %q is not a user public key", a.publicKey))
	}
	release, err := hubLock(cfg, "add")
	if err != nil {
		fmt.Fprintf(stderr, "hub: %v\n", err)
		return 1
	}
	defer release()

	bin, code := hubPreflight(cfg, stderr)
	if code != 0 {
		return code
	}
	conf, err := os.ReadFile(cfg.conf)
	if err != nil {
		fmt.Fprintf(stderr, "hub: cannot read the hub conf: %v\n", err)
		return 1
	}
	rs, re, ok := confRegion(string(conf))
	if !ok {
		fmt.Fprintf(stderr, "hub: the conf has no managed region (%q to %q); run `director hub settings init` once. Nothing was changed\n", hubMarkBegin, hubMarkEnd)
		return 1
	}
	_ = rs
	reg, err := loadRegistry(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "hub: %v. Nothing was changed\n", err)
		return 1
	}
	if reg.find(a.name) != nil {
		fmt.Fprintf(stderr, "hub: the name %s already exists in the registry. Nothing was changed\n", a.name)
		return 1
	}
	seedPath := filepath.Join(cfg.keys, a.name+".nk")
	if a.publicKey == "" {
		if _, err := os.Lstat(seedPath); err == nil {
			fmt.Fprintf(stderr, "hub: %s exists; refusing to overwrite a key. Nothing was changed\n", seedPath)
			return 1
		}
	}
	var pub string
	var seed []byte
	if a.publicKey != "" {
		pub = a.publicKey
	} else {
		kp, err := nkeys.CreateUser()
		if err != nil {
			fmt.Fprintf(stderr, "hub: cannot make a key: %v\n", err)
			return 1
		}
		pub, _ = kp.PublicKey()
		seed, _ = kp.Seed()
	}
	if reg.findKey(pub) != nil {
		fmt.Fprintf(stderr, "hub: that public key is already registered as %s. Nothing was changed\n", reg.findKey(pub).Name)
		return 1
	}
	block, err := renderClass(a.class, pub, a.params)
	if err != nil {
		return usageErr(stderr, err.Error())
	}
	entry := indentBlock("# director user: " + a.name + " (class " + a.class + ")\n" + strings.TrimRight(block, "\n"))
	candidate := string(conf[:re]) + entry + string(conf[re:])
	candPath, cleanup, err := stageCandidate(cfg, map[string][]byte{filepath.Base(cfg.conf): []byte(candidate)})
	if err != nil {
		fmt.Fprintf(stderr, "hub: %v. Nothing was changed\n", err)
		return 1
	}
	out, valid := serverTest(bin, candPath)
	cleanup()
	if !valid {
		fmt.Fprintf(stderr, "hub: the candidate conf fails nats-server -t: %s\nThe hub is unchanged; nothing was written.\n", out)
		return 1
	}
	admin, err := dialHub(cfg, cfg.adminKey, "", nil)
	if err != nil {
		fmt.Fprintf(stderr, "hub: cannot reach the hub as admin at %s with %s: %v. Nothing was changed\n", cfg.adminURL, cfg.adminKey, err)
		return 1
	}
	defer admin.Close()
	countsBefore, err := consumerCounts(admin)
	if err != nil {
		fmt.Fprintf(stderr, "hub: cannot read the hub's consumer counts as admin: %v. Nothing was changed\n", err)
		return 1
	}

	// From here the hub changes. A key that is born here is written first, with
	// O_EXCL, so a name that appeared since the check is still refused.
	if seed != nil {
		if err := os.MkdirAll(cfg.keys, 0o700); err != nil {
			fmt.Fprintf(stderr, "hub: cannot make %s: %v. Nothing was changed\n", cfg.keys, err)
			return 1
		}
		if st, err := os.Stat(cfg.keys); err == nil && st.Mode().Perm() != 0o700 {
			_ = os.Chmod(cfg.keys, 0o700)
		}
		if err := writeFileExcl(seedPath, append(seed, '\n'), 0o600); err != nil {
			if os.IsExist(err) {
				fmt.Fprintf(stderr, "hub: %s exists; refusing to overwrite a key. Nothing was changed\n", seedPath)
			} else {
				fmt.Fprintf(stderr, "hub: cannot write %s: %v. Nothing was changed\n", seedPath, err)
			}
			return 1
		}
	}
	backup, err := backupConf(cfg)
	if err != nil {
		moveSeed(cfg, a.name, seed != nil)
		fmt.Fprintf(stderr, "hub: cannot back up the conf: %v. Nothing was changed\n", err)
		return 1
	}
	st, _ := os.Stat(cfg.conf)
	if err := writeFileAtomic(cfg.conf, []byte(candidate), st.Mode().Perm()); err != nil {
		moveSeed(cfg, a.name, seed != nil)
		fmt.Fprintf(stderr, "hub: cannot write the conf: %v. The conf may be partly written: restore %s\n", err, backup)
		return 1
	}
	fail := func(why string) int {
		restored := restoreConf(cfg, backup)
		moved := ""
		if seed != nil {
			moved = moveSeed(cfg, a.name, true)
			awaitRefused(cfg, seedPath+".gone", 0)
			if m := moved; m != "" {
				awaitRefused(cfg, m, 5*time.Second)
			}
		}
		fmt.Fprintf(stderr, "hub: %s\nRolled back: %s restored from %s and the hub reloaded", why, filepath.Base(cfg.conf), filepath.Base(backup))
		if restored != nil {
			fmt.Fprintf(stderr, " (the restore itself reported: %v)", restored)
		}
		if moved != "" {
			fmt.Fprintf(stderr, "; the seed moved to %s", moved)
		}
		fmt.Fprintln(stderr, ".")
		return 1
	}
	if err := reloadHub(cfg); err != nil {
		return fail("reload failed: " + err.Error())
	}
	if hubAfterReload != nil {
		hubAfterReload()
	}
	if a.publicKey != "" {
		live, _ := os.ReadFile(cfg.conf)
		if !strings.Contains(string(live), pub) {
			return fail("the reloaded conf does not hold the new user")
		}
		if nc, err := awaitConnect(cfg, cfg.adminKey, "", 5*time.Second, nil); err != nil {
			return fail("the hub does not answer as admin after the reload: " + err.Error())
		} else {
			nc.Close()
		}
		fmt.Fprintf(stdout, "ok, %s is in the reloaded conf and the hub answers\n", a.name)
	} else if err := verifyUser(cfg, a, seedPath, admin, countsBefore, stdout); err != nil {
		return fail("verify failed: " + err.Error())
	}
	reg.Users = append(reg.Users, hubUser{Name: a.name, Class: a.class, Params: a.params, PublicKey: pub, State: "active", Location: "managed region", Created: time.Now().UTC().Format(time.RFC3339)})
	if err := saveRegistry(cfg, reg); err != nil {
		fmt.Fprintf(stderr, "hub: the user is live but the registry write failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "added %s (class %s)\npublic key: %s\n", a.name, a.class, pub)
	if seed != nil {
		fmt.Fprintf(stdout, "seed: %s (mode 0600; never printed)\n", seedPath)
	}
	fmt.Fprintf(stdout, "backup: %s\n", backup)
	return 0
}

// restoreConf puts the backup back over the live conf and reloads.
func restoreConf(cfg hubCfg, backup string) error {
	data, err := os.ReadFile(backup)
	if err != nil {
		return err
	}
	st, _ := os.Stat(cfg.conf)
	mode := os.FileMode(0o600)
	if st != nil {
		mode = st.Mode().Perm()
	}
	if err := writeFileAtomic(cfg.conf, data, mode); err != nil {
		return err
	}
	return reloadHub(cfg)
}

// moveSeed moves a seed this command made into keys/revoked. It never deletes.
func moveSeed(cfg hubCfg, name string, made bool) string {
	if !made {
		return ""
	}
	from := filepath.Join(cfg.keys, name+".nk")
	dir := filepath.Join(cfg.keys, "revoked")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	to := filepath.Join(dir, fmt.Sprintf("%s-%s.nk", name, utcStamp()))
	if err := os.Rename(from, to); err != nil {
		return ""
	}
	return to
}

// --- verify -------------------------------------------------------------------------------------

var nonWord = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// verifyUser connects as the new key and, for an observer, runs its positive
// reads and its harmless negatives and checks the consumer counts did not move.
func verifyUser(cfg hubCfg, a hubUserArgs, seedPath string, admin *nats.Conn, countsBefore string, stdout io.Writer) error {
	if a.class != "observer" {
		nc, err := awaitConnect(cfg, seedPath, "", 5*time.Second, nil)
		if err != nil {
			return fmt.Errorf("the new key does not connect: %v", err)
		}
		nc.Close()
		fmt.Fprintf(stdout, "ok, the new %s key connects\n", a.class)
		return nil
	}
	holder := nonWord.ReplaceAllString(a.params["holder"], "_")
	var violations []string
	nc, err := awaitConnect(cfg, seedPath, "_INBOX_observer_"+holder, 5*time.Second, func(_ *nats.Conn, _ *nats.Subscription, err error) {
		violations = append(violations, err.Error())
	})
	if err != nil {
		return fmt.Errorf("the new observer key does not connect: %v", err)
	}
	defer nc.Close()
	reply := func(subj, payload string) ([]byte, error) {
		m, err := nc.Request(subj, []byte(payload), 3*time.Second)
		if err != nil {
			return nil, err
		}
		return m.Data, nil
	}
	// A hub that has just reloaded can answer "no responders" for a moment while
	// its JetStream API comes back, so the first read is retried for a few seconds.
	var data []byte
	for end := time.Now().Add(5 * time.Second); ; time.Sleep(150 * time.Millisecond) {
		data, err = reply("$JS.API.STREAM.NAMES", "{}")
		if err == nil || !errors.Is(err, nats.ErrNoResponders) || time.Now().After(end) {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("the observer cannot list streams: %v", err)
	}
	var names struct {
		Streams []string `json:"streams"`
	}
	if err := json.Unmarshal(data, &names); err != nil {
		return fmt.Errorf("the stream list is not JSON: %v", err)
	}
	fmt.Fprintf(stdout, "ok, read: %d stream(s) listed\n", len(names.Streams))
	first := ""
	for _, s := range names.Streams {
		if strings.HasPrefix(s, "GLOBAL_TO_") {
			first = s
			break
		}
	}
	if first == "" && len(names.Streams) > 0 {
		first = names.Streams[0]
	}
	if first == "" {
		fmt.Fprintln(stdout, "note: the hub has no stream to read; the reads of one are skipped")
	} else {
		if _, err := reply("$JS.API.CONSUMER.NAMES."+first, "{}"); err != nil {
			return fmt.Errorf("the observer cannot list the consumers of %s: %v", first, err)
		}
		info, err := reply("$JS.API.STREAM.INFO."+first, "")
		if err != nil {
			return fmt.Errorf("the observer cannot read %s: %v", first, err)
		}
		var si struct {
			State struct {
				FirstSeq uint64 `json:"first_seq"`
			} `json:"state"`
		}
		_ = json.Unmarshal(info, &si)
		if _, err := reply("$JS.API.STREAM.MSG.GET."+first, fmt.Sprintf(`{"seq":%d}`, max(si.State.FirstSeq, 1))); err != nil {
			return fmt.Errorf("the observer cannot get a message from %s: %v", first, err)
		}
		fmt.Fprintf(stdout, "ok, read: consumers, info and a message of %s\n", first)
	}
	stream := first
	if stream == "" {
		stream = "GLOBAL_TO_none"
	}
	negatives := []string{
		"$JS.API.CONSUMER.MSG.NEXT." + stream + ".no-such-durable-observer-check",
		"$JS.API.CONSUMER.CREATE.KV_GLOBAL_PRESENCE",
		"$JS.API.CONSUMER.DELETE." + stream + ".no-such-durable-observer-check",
		"$JS.API.STREAM.MSG.DELETE." + stream,
	}
	var bad []string
	for _, subj := range negatives {
		violations = nil
		m, err := nc.Request(subj, nil, 1500*time.Millisecond)
		switch {
		case err == nil && m != nil:
			bad = append(bad, fmt.Sprintf("%s was answered (it should be denied)", subj))
		case violatesOn(violations, subj):
			fmt.Fprintf(stdout, "ok, refused with a permission violation: %s\n", subj)
		default:
			bad = append(bad, fmt.Sprintf("%s was not answered, but the hub logged no permission violation naming it", subj))
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("%s", strings.Join(bad, "; "))
	}
	after, err := consumerCounts(admin)
	if err != nil {
		return fmt.Errorf("cannot read consumer counts after the checks: %v", err)
	}
	if after != countsBefore {
		return fmt.Errorf("consumer counts changed: before\n%s\nafter\n%s", countsBefore, after)
	}
	fmt.Fprintln(stdout, "ok, consumer counts unchanged")
	return nil
}

func violatesOn(violations []string, subj string) bool {
	for _, v := range violations {
		l := strings.ToLower(v)
		if strings.Contains(l, "permissions violation") && strings.Contains(v, `"`+subj+`"`) {
			return true
		}
	}
	return false
}

// --- user adopt ---------------------------------------------------------------------------------

func hubUserAdopt(cfg hubCfg, args []string, stdout, stderr io.Writer) int {
	a, err := parseUserArgs("adopt", args)
	if err != nil {
		return usageErr(stderr, err.Error())
	}
	if !nkeys.IsValidPublicUserKey(a.positKey) {
		return usageErr(stderr, fmt.Sprintf("%q is not a user public key", a.positKey))
	}
	if _, _, err := classTemplate(a.class); err != nil {
		return usageErr(stderr, err.Error())
	}
	tmpl, err := renderClass(a.class, a.positKey, a.params)
	if err != nil {
		return usageErr(stderr, err.Error())
	}
	release, err := hubLock(cfg, "adopt")
	if err != nil {
		fmt.Fprintf(stderr, "hub: %v\n", err)
		return 1
	}
	defer release()
	conf, err := os.ReadFile(cfg.conf)
	if err != nil {
		fmt.Fprintf(stderr, "hub: cannot read the hub conf: %v\n", err)
		return 1
	}
	block, found := entryBlock(string(conf), a.positKey)
	if !found {
		fmt.Fprintf(stderr, "hub: the key %s is not in %s as a user entry. Nothing was changed\n", a.positKey, cfg.conf)
		return 1
	}
	reg, err := loadRegistry(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "hub: %v\n", err)
		return 1
	}
	if reg.find(a.name) != nil {
		fmt.Fprintf(stderr, "hub: the name %s already exists in the registry. Nothing was changed\n", a.name)
		return 1
	}
	if u := reg.findKey(a.positKey); u != nil {
		fmt.Fprintf(stderr, "hub: that key is already registered as %s. Nothing was changed\n", u.Name)
		return 1
	}
	location := "outside the region"
	if rs, re, ok := confRegion(string(conf)); ok {
		if i := strings.Index(string(conf), block); i >= rs && i < re {
			location = "managed region"
		}
	}
	diffs := grantDifferences(block, tmpl)
	if len(diffs) == 0 {
		fmt.Fprintf(stdout, "no grant difference between the conf block and class %s\n", a.class)
	}
	for _, d := range diffs {
		fmt.Fprintln(stdout, d)
	}
	reg.Users = append(reg.Users, hubUser{Name: a.name, Class: a.class, Params: a.params, PublicKey: a.positKey, State: "adopted", Location: location, Created: time.Now().UTC().Format(time.RFC3339)})
	if err := saveRegistry(cfg, reg); err != nil {
		fmt.Fprintf(stderr, "hub: cannot write the registry: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "adopted %s as class %s (%s); no conf bytes were changed\n", a.name, a.class, location)
	if len(diffs) > 0 {
		fmt.Fprintln(stdout, "note: a changed grant reaches an established leaf only after that leaf reconnects")
	}
	return 0
}

// --- user list ----------------------------------------------------------------------------------

var regionKeyRe = regexp.MustCompile(`nkey\s*:\s*(U[A-Z2-7]{55})`)

func hubUserList(cfg hubCfg, args []string, stdout, stderr io.Writer) int {
	a, err := parseUserArgs("list", args)
	if err != nil {
		return usageErr(stderr, err.Error())
	}
	reg, err := loadRegistry(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "hub: %v\n", err)
		return 1
	}
	conf, err := os.ReadFile(cfg.conf)
	if err != nil {
		fmt.Fprintf(stderr, "hub: cannot read the hub conf: %v\n", err)
		return 1
	}
	inRegion := map[string]bool{}
	if rs, re, ok := confRegion(string(conf)); ok {
		for _, m := range regionKeyRe.FindAllStringSubmatch(stripConf(string(conf[rs:re])), -1) {
			inRegion[m[1]] = true
		}
	}
	registered := map[string]bool{}
	drift := []string{}
	type row struct {
		hubUser
		Connected string `json:"connected"`
	}
	rows := []row{}
	for _, u := range reg.Users {
		registered[u.PublicKey] = true
		r := row{hubUser: u, Connected: "unknown"}
		rows = append(rows, r)
		switch {
		case u.Location == "managed region" && !inRegion[u.PublicKey]:
			drift = append(drift, fmt.Sprintf("%s: in the registry and not in the conf's managed region (not in the conf)", u.Name))
		case u.Location == "outside the region" && !strings.Contains(string(conf), u.PublicKey):
			drift = append(drift, fmt.Sprintf("%s: adopted, and its key is not in the conf", u.Name))
		}
	}
	for k := range inRegion {
		if !registered[k] {
			drift = append(drift, fmt.Sprintf("%s: in the conf's managed region and not in the registry", k))
		}
	}
	sortStrings(drift)
	if a.asJSON {
		b, _ := json.Marshal(struct {
			Users []row    `json:"users"`
			Drift []string `json:"drift"`
		}{rows, drift})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	for _, r := range rows {
		fmt.Fprintf(stdout, "%-24s %-10s %-8s %-20s %s\n", r.Name, r.Class, r.State, r.Location, r.PublicKey)
	}
	for _, d := range drift {
		fmt.Fprintf(stdout, "DRIFT %s\n", d)
	}
	return 0
}

// --- settings -----------------------------------------------------------------------------------

func hubSettingsCheck(cfg hubCfg, stdout, stderr io.Writer) int {
	st, err := readSettingsState(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "hub: %v\n", err)
		return 1
	}
	for _, n := range st.Defined {
		fmt.Fprintf(stdout, "defined %s\n", n)
	}
	for _, n := range st.Undefined {
		fmt.Fprintf(stdout, "UNDEFINED %s\n", n)
	}
	for _, n := range st.Unreferenced {
		fmt.Fprintf(stdout, "UNREFERENCED %s\n", n)
	}
	if st.SettingsPresent && !st.Included {
		fmt.Fprintln(stdout, "NOTE settings.conf exists and the conf does not include it")
	}
	if len(st.Defined)+len(st.Undefined)+len(st.Unreferenced) == 0 {
		fmt.Fprintln(stdout, "the conf references no variables")
	}
	if len(st.Undefined) > 0 || len(st.Unreferenced) > 0 {
		return 1
	}
	return 0
}

var settingNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func hubSettingsInit(cfg hubCfg, args []string, stdout, stderr io.Writer) int {
	sets := map[string]string{}
	var order []string
	for i := 0; i < len(args); i++ {
		if args[i] != "--set" || i+1 >= len(args) {
			return usageErr(stderr, fmt.Sprintf("settings init: unexpected %q (use --set NAME=VALUE)", args[i]))
		}
		i++
		k, v, ok := strings.Cut(args[i], "=")
		if !ok || !settingNameRe.MatchString(k) {
			return usageErr(stderr, fmt.Sprintf("--set %q is not NAME=VALUE", args[i]))
		}
		if strings.ContainsAny(v, "\"\\\n\r") {
			return usageErr(stderr, fmt.Sprintf("--set %s: a value cannot hold a quote, a backslash or a newline", k))
		}
		if _, dup := sets[k]; dup {
			return usageErr(stderr, fmt.Sprintf("--set %s is given twice", k))
		}
		sets[k] = v
		order = append(order, k)
	}
	release, err := hubLock(cfg, "settings init")
	if err != nil {
		fmt.Fprintf(stderr, "hub: %v\n", err)
		return 1
	}
	defer release()
	bin, _, err := natsServer()
	if err != nil {
		fmt.Fprintf(stderr, "hub: %v\n", err)
		return 1
	}
	confB, err := os.ReadFile(cfg.conf)
	if err != nil {
		fmt.Fprintf(stderr, "hub: cannot read the hub conf: %v\n", err)
		return 1
	}
	conf := string(confB)
	if _, err := os.Lstat(cfg.settings); err == nil {
		fmt.Fprintf(stderr, "hub: settings.conf already exists in %s; init runs once per hub. Nothing was changed\n", cfg.home)
		return 1
	}
	if hubIncludeRe.MatchString(conf) || strings.Contains(conf, hubMarkBegin) {
		fmt.Fprintf(stderr, "hub: the conf already has the settings include or the managed region; init runs once per hub. Nothing was changed\n")
		return 1
	}
	refs := confReferences(conf)
	for _, k := range order {
		if !contains(refs, k) {
			fmt.Fprintf(stderr, "hub: --set %s: the conf does not reference $%s, and nats-server rejects a settings variable nothing references. Nothing was changed\n", k, k)
			return 1
		}
	}
	var missing []string
	for _, r := range refs {
		if _, ok := sets[r]; !ok {
			missing = append(missing, r)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(stderr, "hub: the conf references %s and init has no value for %s; give --set NAME=VALUE for each. Nothing was changed\n", strings.Join(missing, ", "), pluralIt(missing))
		return 1
	}
	// nats-server refuses a server_name change on reload ("config reload not
	// supported for ServerName") and a changed name is a different hub to its
	// leaves, so init moves values and never changes this one. Compare before
	// any write; a value that cannot be read is a refusal too.
	if m := serverNameVarRe.FindStringSubmatch(stripConf(conf)); m != nil {
		if given, ok := sets[m[1]]; ok {
			running, err := runningServerName(cfg.monitorURL)
			if err != nil {
				fmt.Fprintf(stderr, "hub: cannot read server_name from %s/varz to compare it with --set %s: %v (set DIRECTOR_HUB_MONITOR_URL). Nothing was changed\n", strings.TrimRight(cfg.monitorURL, "/"), m[1], err)
				return 1
			}
			if running != given {
				fmt.Fprintf(stderr, "hub: --set %s=%s would change server_name from %q (the running hub) to %q; nats-server refuses that on reload and a changed name is a different hub to its leaves. Give the running value. Nothing was changed\n", m[1], given, running, given)
				return 1
			}
		}
	}
	lines := strings.Split(conf, "\n")
	usersAt := -1
	n := 0
	for i, l := range lines {
		if hubUsersRe.MatchString(stripConf(l)) {
			n++
			if usersAt < 0 {
				usersAt = i
			}
		}
	}
	if n != 1 {
		fmt.Fprintf(stderr, "hub: expected one `users: [` line in the conf, found %d; add the managed region by hand. Nothing was changed\n", n)
		return 1
	}
	region := []string{"      " + hubMarkBegin, "      " + hubMarkEnd}
	newLines := append([]string{`include "./settings.conf"`}, lines[:usersAt+1]...)
	newLines = append(newLines, region...)
	newLines = append(newLines, lines[usersAt+1:]...)
	candidate := strings.Join(newLines, "\n")
	var sb strings.Builder
	for _, k := range order {
		fmt.Fprintf(&sb, "%s: \"%s\"\n", k, sets[k])
	}
	candPath, cleanup, err := stageCandidate(cfg, map[string][]byte{filepath.Base(cfg.conf): []byte(candidate), filepath.Base(cfg.settings): []byte(sb.String())})
	if err != nil {
		fmt.Fprintf(stderr, "hub: %v. Nothing was changed\n", err)
		return 1
	}
	out, valid := serverTest(bin, candPath)
	cleanup()
	if !valid {
		fmt.Fprintf(stderr, "hub: the candidate conf fails nats-server -t: %s\nThe hub is unchanged; nothing was written.\n", out)
		return 1
	}
	admin, err := dialHub(cfg, cfg.adminKey, "", nil)
	if err != nil {
		fmt.Fprintf(stderr, "hub: cannot reach the hub as admin at %s with %s: %v. Nothing was changed\n", cfg.adminURL, cfg.adminKey, err)
		return 1
	}
	defer admin.Close()
	before, err := connSet(cfg.monitorURL)
	if err != nil {
		fmt.Fprintf(stderr, "hub: cannot read the hub's connections from its monitor at %s (set DIRECTOR_HUB_MONITOR_URL): %v. Nothing was changed\n", cfg.monitorURL, err)
		return 1
	}
	backup, err := backupConf(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "hub: cannot back up the conf: %v. Nothing was changed\n", err)
		return 1
	}
	if err := writeFileExcl(cfg.settings, []byte(sb.String()), 0o600); err != nil {
		fmt.Fprintf(stderr, "hub: cannot write settings.conf: %v. Nothing was changed\n", err)
		return 1
	}
	st, _ := os.Stat(cfg.conf)
	if err := writeFileAtomic(cfg.conf, []byte(candidate), st.Mode().Perm()); err != nil {
		_ = os.Remove(cfg.settings)
		fmt.Fprintf(stderr, "hub: cannot write the conf: %v. settings.conf was removed; restore %s if the conf changed\n", err, backup)
		return 1
	}
	fail := func(why string) int {
		restored := restoreConf(cfg, backup)
		_ = os.Remove(cfg.settings)
		fmt.Fprintf(stderr, "hub: %s\nRolled back: %s restored from %s, settings.conf removed and the hub reloaded", why, filepath.Base(cfg.conf), filepath.Base(backup))
		if restored != nil {
			fmt.Fprintf(stderr, " (the restore itself reported: %v)", restored)
		}
		fmt.Fprintln(stderr, ".")
		return 1
	}
	if err := reloadHub(cfg); err != nil {
		return fail("reload failed: " + err.Error())
	}
	if hubAfterReload != nil {
		hubAfterReload()
	}
	time.Sleep(500 * time.Millisecond) // the server's connection list settles after a drop
	if nc, err := awaitConnect(cfg, cfg.adminKey, "", 5*time.Second, nil); err != nil {
		return fail("the hub does not answer as admin after the reload: " + err.Error())
	} else {
		nc.Close()
	}
	after, err := connSet(cfg.monitorURL)
	if err != nil {
		return fail("cannot read the hub's connections after the reload: " + err.Error())
	}
	var dropped []string
	for k := range before {
		if !after[k] {
			dropped = append(dropped, k)
		}
	}
	if len(dropped) > 0 {
		sortStrings(dropped)
		return fail(fmt.Sprintf("%d connection(s) dropped by the reload: %s", len(dropped), strings.Join(dropped, "; ")))
	}
	fmt.Fprintf(stdout, "ok, settings.conf written (mode 0600) with %s\n", strings.Join(order, ", "))
	fmt.Fprintf(stdout, "ok, the include and the managed region are in the conf; backup %s\n", filepath.Base(backup))
	fmt.Fprintf(stdout, "ok, the hub was reloaded, answers as admin, and all %d connection(s) it had before are still connected\n", len(before))
	return 0
}

// --- small helpers ------------------------------------------------------------------------------

func cctxHub() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

func sortStrings(a []string) { sort.Strings(a) }

var serverNameVarRe = regexp.MustCompile(`(?m)^\s*server_name\s*:?\s*\$([A-Za-z_][A-Za-z0-9_]*)\s*$`)

// runningServerName reads server_name from the hub's monitor /varz.
func runningServerName(monitorURL string) (string, error) {
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(strings.TrimRight(monitorURL, "/") + "/varz")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("/varz answered %s", resp.Status)
	}
	var v struct {
		Name string `json:"server_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return "", err
	}
	if v.Name == "" {
		return "", fmt.Errorf("/varz has no server_name")
	}
	return v.Name, nil
}
