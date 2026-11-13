package main

// `director hub user rotate [--finish] NAME` and `director hub user revoke NAME`
// (sim/design/hub-user-lifecycle.md, PR 2).
//
// Write order for every changing step: the conf, the reload, the verify, then
// the registry, then the seed files. A seed made here is staged in a directory
// of the hub home outside keys/ until the verify has passed, and then moved in.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
)

// --- argument parsing ---------------------------------------------------------------------------

type rotateArgs struct {
	name      string
	finish    bool
	publicKey string
}

func parseRotateArgs(args []string) (rotateArgs, error) {
	var a rotateArgs
	for i := 0; i < len(args); i++ {
		switch w := args[i]; {
		case w == "--finish":
			a.finish = true
		case w == "--public-key":
			if i+1 >= len(args) {
				return a, fmt.Errorf("--public-key needs a value")
			}
			i++
			a.publicKey = args[i]
		case strings.HasPrefix(w, "--"):
			return a, fmt.Errorf("unknown argument %q", w)
		case a.name == "":
			a.name = w
		default:
			return a, fmt.Errorf("rotate takes one NAME (got %q and %q)", a.name, w)
		}
	}
	if a.name == "" {
		return a, fmt.Errorf("rotate needs a NAME")
	}
	if !hubNameRe.MatchString(a.name) {
		return a, fmt.Errorf("the name %q is not a name: letters, digits, dot, dash and underscore, starting with a letter or digit", a.name)
	}
	if a.finish && a.publicKey != "" {
		return a, fmt.Errorf("--finish takes no --public-key: the key was given when the rotation started")
	}
	if a.publicKey != "" && !nkeys.IsValidPublicUserKey(a.publicKey) {
		return a, fmt.Errorf("--public-key %q is not a user public key", a.publicKey)
	}
	return a, nil
}

func parseRevokeArgs(args []string) (string, error) {
	name := ""
	for _, w := range args {
		switch {
		case strings.HasPrefix(w, "--"):
			return "", fmt.Errorf("unknown argument %q", w)
		case name == "":
			name = w
		default:
			return "", fmt.Errorf("revoke takes one NAME (got %q and %q)", name, w)
		}
	}
	if name == "" {
		return "", fmt.Errorf("revoke needs a NAME")
	}
	if !hubNameRe.MatchString(name) {
		return "", fmt.Errorf("the name %q is not a name: letters, digits, dot, dash and underscore, starting with a letter or digit", name)
	}
	return name, nil
}

// --- conf blocks --------------------------------------------------------------------------------

// regionHas reports whether a user entry for key lies inside the managed region.
func regionHas(conf, key string) bool {
	rs, re, ok := confRegion(conf)
	if !ok {
		return false
	}
	block, found := entryBlock(conf, key)
	if !found {
		return false
	}
	i := strings.Index(conf, block)
	return i >= rs && i < re
}

// removeEntry removes the user entry for key, with the director comment line
// that heads it, and returns the new conf.
func removeEntry(conf, key string) (string, bool) {
	block, found := entryBlock(conf, key)
	if !found {
		return conf, false
	}
	i := strings.Index(conf, block)
	start := strings.LastIndexByte(conf[:i], '\n') + 1
	end := i + len(block)
	if nl := strings.IndexByte(conf[end:], '\n'); nl >= 0 {
		end += nl + 1
	} else {
		end = len(conf)
	}
	// The comment line above the entry.
	if start > 0 {
		ps := strings.LastIndexByte(conf[:start-1], '\n') + 1
		if strings.HasPrefix(strings.TrimSpace(conf[ps:start]), "# director user:") {
			start = ps
		}
	}
	return conf[:start] + conf[end:], true
}

// addEntry appends a rendered entry at the end of the managed region.
func addEntry(conf, comment, block string) (string, error) {
	_, re, ok := confRegion(conf)
	if !ok {
		return "", fmt.Errorf("the conf has no managed region (%q to %q); run `director hub settings init` once", hubMarkBegin, hubMarkEnd)
	}
	entry := indentBlock("# director user: " + comment + "\n" + strings.TrimRight(block, "\n"))
	return conf[:re] + entry + conf[re:], nil
}

// narrowing splits grantDifferences into the subjects a hand block has and the
// class grant lacks (a narrowing) and those the class grant adds.
func splitDifferences(diffs []string) (narrow, add []string) {
	for _, d := range diffs {
		if strings.Contains(d, "is in the conf block and not in the class template") {
			narrow = append(narrow, d)
		} else {
			add = append(add, d)
		}
	}
	return
}

// --- seed staging -------------------------------------------------------------------------------

// stageSeed writes a seed in a private directory of the hub home, outside
// keys/, and returns the file's path and a remover for the directory.
func stageSeed(cfg hubCfg, name string, seed []byte) (path string, cleanup func(), err error) {
	dir, err := os.MkdirTemp(cfg.home, ".seed-")
	if err != nil {
		return "", nil, fmt.Errorf("cannot stage a seed in %s: %v", cfg.home, err)
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	path = filepath.Join(dir, name+".next.nk")
	if err := writeFileExcl(path, append(seed, '\n'), 0o600); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}

// revokeSeedFile moves a seed to keys/revoked/<name>-<time>.nk. It never deletes.
func revokeSeedFile(cfg hubCfg, from, label string) (string, error) {
	dir := filepath.Join(cfg.keys, "revoked")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	to := filepath.Join(dir, fmt.Sprintf("%s-%s.nk", label, utcStamp()))
	if err := os.Rename(from, to); err != nil {
		return "", err
	}
	return to, nil
}

func fileExists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func seedPublic(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	kp, err := nkeys.FromSeed([]byte(strings.TrimSpace(string(b))))
	if err != nil {
		return ""
	}
	pub, _ := kp.PublicKey()
	return pub
}

// --- rotate -------------------------------------------------------------------------------------

func hubUserRotate(cfg hubCfg, args []string, stdout, stderr io.Writer) int {
	a, err := parseRotateArgs(args)
	if err != nil {
		return usageErr(stderr, err.Error())
	}
	release, err := hubLock(cfg, "rotate")
	if err != nil {
		fmt.Fprintf(stderr, "hub: %v\n", err)
		return 1
	}
	defer release()
	bin, code := hubPreflight(cfg, stderr)
	if code != 0 {
		return code
	}
	reg, err := loadRegistry(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "hub: %v. Nothing was changed\n", err)
		return 1
	}
	u := reg.find(a.name)
	if u == nil {
		fmt.Fprintf(stderr, "hub: there is no user named %s in the registry. Nothing was changed\n", a.name)
		return 1
	}
	if u.State == "revoked" {
		fmt.Fprintf(stderr, "hub: %s is revoked; add a new user instead. Nothing was changed\n", a.name)
		return 1
	}
	if a.finish {
		return hubUserRotateFinish(cfg, bin, reg, u, stdout, stderr)
	}
	return hubUserRotateStart(cfg, bin, a, reg, u, stdout, stderr)
}

func hubUserRotateStart(cfg hubCfg, bin string, a rotateArgs, reg hubRegistry, u *hubUser, stdout, stderr io.Writer) int {
	if u.PendingKey != "" {
		fmt.Fprintf(stderr, "hub: a rotation of %s is already pending; run `director hub user rotate --finish %s` first. Nothing was changed\n", a.name, a.name)
		return 1
	}
	conf, err := os.ReadFile(cfg.conf)
	if err != nil {
		fmt.Fprintf(stderr, "hub: cannot read the hub conf: %v. Nothing was changed\n", err)
		return 1
	}
	if _, _, ok := confRegion(string(conf)); !ok {
		fmt.Fprintf(stderr, "hub: the conf has no managed region (%q to %q); run `director hub settings init` once. Nothing was changed\n", hubMarkBegin, hubMarkEnd)
		return 1
	}
	nextSeed := filepath.Join(cfg.keys, a.name+".next.nk")
	if a.publicKey == "" && fileExists(nextSeed) {
		fmt.Fprintf(stderr, "hub: %s exists with no rotation pending (an orphan); refusing to overwrite a key. Nothing was changed\n", nextSeed)
		return 1
	}
	var pub string
	var seed []byte
	if a.publicKey != "" {
		pub = a.publicKey
	} else {
		kp, err := nkeys.CreateUser()
		if err != nil {
			fmt.Fprintf(stderr, "hub: cannot make a key: %v. Nothing was changed\n", err)
			return 1
		}
		pub, _ = kp.PublicKey()
		seed, _ = kp.Seed()
	}
	if o := reg.findKey(pub); o != nil || strings.Contains(string(conf), pub) {
		fmt.Fprintf(stderr, "hub: that public key is already in use. Nothing was changed\n")
		return 1
	}
	block, err := renderClass(u.Class, pub, u.Params)
	if err != nil {
		fmt.Fprintf(stderr, "hub: %v. Nothing was changed\n", err)
		return 1
	}
	// An adopted user's hand block is compared with the class grant the new key
	// gets. A subject the hand block has and the class lacks is a narrowing,
	// which is refused; additions are printed and the rotation goes on.
	var adds []string
	if u.Location != "managed region" {
		hand, found := entryBlock(string(conf), u.PublicKey)
		if !found {
			fmt.Fprintf(stderr, "hub: the key %s of %s is not in the conf. Nothing was changed\n", u.PublicKey, a.name)
			return 1
		}
		narrow, add := splitDifferences(grantDifferences(hand, block))
		if len(narrow) > 0 {
			fmt.Fprintf(stderr, "hub: rotating %s would narrow its grant. The hand block allows what class %s does not:\n", a.name, u.Class)
			for _, n := range narrow {
				fmt.Fprintf(stderr, "  %s\n", n)
			}
			fmt.Fprintf(stderr, "Fix it with the right class (director hub user adopt, before the first rotation) or with a template change by PR. Nothing was changed\n")
			return 1
		}
		adds = add
	}
	candidate, err := addEntry(string(conf), fmt.Sprintf("%s (class %s, new key)", a.name, u.Class), block)
	if err != nil {
		fmt.Fprintf(stderr, "hub: %v. Nothing was changed\n", err)
		return 1
	}
	candPath, cleanup, err := stageCandidate(cfg, map[string][]byte{filepath.Base(cfg.conf): []byte(candidate)})
	if err != nil {
		fmt.Fprintf(stderr, "hub: %v. Nothing was changed\n", err)
		return 1
	}
	liveSt, _ := readSettingsState(cfg)
	out, valid := serverTest(bin, candPath, launcherEnvList(liveSt))
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
	var stagedPath string
	if seed != nil {
		p, rm, err := stageSeed(cfg, a.name, seed)
		if err != nil {
			fmt.Fprintf(stderr, "hub: %v. Nothing was changed\n", err)
			return 1
		}
		defer rm()
		stagedPath = p
	}
	if len(adds) > 0 || u.Location != "managed region" {
		if len(adds) == 0 {
			fmt.Fprintf(stdout, "no grant difference between the hand block and class %s\n", u.Class)
		}
		for _, d := range adds {
			fmt.Fprintln(stdout, d)
		}
	}
	backup, err := backupConf(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "hub: cannot back up the conf: %v. Nothing was changed\n", err)
		return 1
	}
	st, _ := os.Stat(cfg.conf)
	if err := writeFileAtomic(cfg.conf, []byte(candidate), st.Mode().Perm()); err != nil {
		fmt.Fprintf(stderr, "hub: cannot write the conf: %v. The conf may be partly written: restore %s\n", err, backup)
		return 1
	}
	fail := func(why string) int {
		restored := restoreConf(cfg, backup)
		moved := ""
		if stagedPath != "" {
			if m, err := revokeSeedFile(cfg, stagedPath, a.name); err == nil {
				moved = m
				awaitRefused(cfg, m, 5*time.Second)
			}
		}
		fmt.Fprintf(stderr, "hub: %s\nRolled back: %s restored from %s and the hub reloaded; the old key is still accepted", why, filepath.Base(cfg.conf), filepath.Base(backup))
		if restored != nil {
			fmt.Fprintf(stderr, " (the restore itself reported: %v)", restored)
		}
		if moved != "" {
			fmt.Fprintf(stderr, "; the new seed moved to %s", moved)
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
	va := hubUserArgs{name: a.name, class: u.Class, params: u.Params}
	if stagedPath != "" {
		if err := verifyUser(cfg, va, stagedPath, admin, countsBefore, stdout); err != nil {
			return fail("verify failed: " + err.Error())
		}
	} else {
		live, _ := os.ReadFile(cfg.conf)
		if !strings.Contains(string(live), pub) {
			return fail("the reloaded conf does not hold the new key")
		}
		if nc, err := awaitConnect(cfg, cfg.adminKey, "", 5*time.Second, nil); err != nil {
			return fail("the hub does not answer as admin after the reload: " + err.Error())
		} else {
			nc.Close()
		}
		fmt.Fprintf(stdout, "ok, the new key of %s is in the reloaded conf and the hub answers\n", a.name)
	}
	oldSeed := filepath.Join(cfg.keys, a.name+".nk")
	if fileExists(oldSeed) {
		nc, err := awaitConnect(cfg, oldSeed, "", 5*time.Second, nil)
		if err != nil {
			return fail("the old key no longer connects while the rotation is pending: " + err.Error())
		}
		nc.Close()
		fmt.Fprintf(stdout, "ok, the old key still connects\n")
	}
	u.PendingKey = pub
	if err := saveRegistry(cfg, reg); err != nil {
		fmt.Fprintf(stderr, "hub: the new key is live but the registry write failed: %v\n", err)
		return 1
	}
	if stagedPath != "" {
		if err := os.MkdirAll(cfg.keys, 0o700); err != nil {
			fmt.Fprintf(stderr, "hub: cannot make %s: %v. The staged seed stays at %s\n", cfg.keys, err, stagedPath)
			return 1
		}
		if err := os.Rename(stagedPath, nextSeed); err != nil {
			fmt.Fprintf(stderr, "hub: cannot move the new seed to %s: %v. It stays at %s\n", nextSeed, err, stagedPath)
			return 1
		}
	}
	fmt.Fprintf(stdout, "rotating %s: the old key is still accepted\nnew public key: %s\n", a.name, pub)
	if stagedPath != "" {
		fmt.Fprintf(stdout, "new seed: %s (mode 0600; never printed)\n", nextSeed)
	} else {
		fmt.Fprintf(stdout, "no seed was written here: the new key's seed stays on its own host\n")
	}
	fmt.Fprintf(stdout, "switch the holder to the new key, then run: director hub user rotate --finish %s\nbackup: %s\n", a.name, backup)
	return 0
}

func hubUserRotateFinish(cfg hubCfg, bin string, reg hubRegistry, u *hubUser, stdout, stderr io.Writer) int {
	if u.PendingKey == "" {
		fmt.Fprintf(stderr, "hub: no rotation is pending for %s. Nothing was changed\n", u.Name)
		return 1
	}
	conf, err := os.ReadFile(cfg.conf)
	if err != nil {
		fmt.Fprintf(stderr, "hub: cannot read the hub conf: %v. Nothing was changed\n", err)
		return 1
	}
	oldPub, newPub := u.PublicKey, u.PendingKey
	if !regionHas(string(conf), newPub) {
		fmt.Fprintf(stderr, "hub: the new key %s of %s is not in the conf's managed region. Nothing was changed\n", newPub, u.Name)
		return 1
	}
	_, oldInConf := entryBlock(string(conf), oldPub)
	oldInRegion := regionHas(string(conf), oldPub)
	if oldInConf && !oldInRegion {
		fmt.Fprintf(stderr, "hub: the old key %s is in a block outside the managed region, which this command does not edit. Remove that block by hand and reload the hub, then run finish again. Nothing was changed\n", oldPub)
		return 1
	}
	newSeedHeld := filepath.Join(cfg.keys, u.Name+".next.nk")
	oldSeedHeld := filepath.Join(cfg.keys, u.Name+".nk")
	candidate := string(conf)
	if oldInRegion {
		candidate, _ = removeEntry(candidate, oldPub)
		candPath, cleanup, err := stageCandidate(cfg, map[string][]byte{filepath.Base(cfg.conf): []byte(candidate)})
		if err != nil {
			fmt.Fprintf(stderr, "hub: %v. Nothing was changed\n", err)
			return 1
		}
		liveSt, _ := readSettingsState(cfg)
		out, valid := serverTest(bin, candPath, launcherEnvList(liveSt))
		cleanup()
		if !valid {
			fmt.Fprintf(stderr, "hub: the candidate conf fails nats-server -t: %s\nThe hub is unchanged; nothing was written.\n", out)
			return 1
		}
	}
	admin, err := dialHub(cfg, cfg.adminKey, "", nil)
	if err != nil {
		fmt.Fprintf(stderr, "hub: cannot reach the hub as admin at %s with %s: %v. Nothing was changed\n", cfg.adminURL, cfg.adminKey, err)
		return 1
	}
	admin.Close()
	backup := ""
	if oldInRegion {
		backup, err = backupConf(cfg)
		if err != nil {
			fmt.Fprintf(stderr, "hub: cannot back up the conf: %v. Nothing was changed\n", err)
			return 1
		}
		st, _ := os.Stat(cfg.conf)
		if err := writeFileAtomic(cfg.conf, []byte(candidate), st.Mode().Perm()); err != nil {
			fmt.Fprintf(stderr, "hub: cannot write the conf: %v. The conf may be partly written: restore %s\n", err, backup)
			return 1
		}
		fail := func(why string) int {
			restored := restoreConf(cfg, backup)
			fmt.Fprintf(stderr, "hub: %s\nRolled back: %s restored from %s and the hub reloaded; the old key is still accepted", why, filepath.Base(cfg.conf), filepath.Base(backup))
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
		if fileExists(oldSeedHeld) && !awaitRefused(cfg, oldSeedHeld, 5*time.Second) {
			return fail("the old key is still accepted after the reload")
		}
	}
	if fileExists(newSeedHeld) {
		nc, err := awaitConnect(cfg, newSeedHeld, "", 5*time.Second, nil)
		if err != nil {
			if oldInRegion {
				restored := restoreConf(cfg, backup)
				fmt.Fprintf(stderr, "hub: the new key does not connect after the old one was removed: %v\nRolled back: the conf is restored and the hub reloaded; the old key is still accepted (restore: %v).\n", err, restored)
			} else {
				fmt.Fprintf(stderr, "hub: the new key does not connect: %v\n", err)
			}
			return 1
		}
		nc.Close()
		fmt.Fprintf(stdout, "ok, the new key connects\n")
	} else {
		fmt.Fprintf(stdout, "no seed is held here for the new key, so it was not tried; it is in the conf\n")
	}
	u.PublicKey, u.PendingKey = newPub, ""
	u.Rotated = time.Now().UTC().Format(time.RFC3339)
	u.State, u.Location = "active", "managed region"
	if err := saveRegistry(cfg, reg); err != nil {
		fmt.Fprintf(stderr, "hub: the rotation is live but the registry write failed: %v\n", err)
		return 1
	}
	if fileExists(oldSeedHeld) {
		if m, err := revokeSeedFile(cfg, oldSeedHeld, u.Name); err != nil {
			fmt.Fprintf(stderr, "hub: cannot move the old seed: %v\n", err)
			return 1
		} else {
			fmt.Fprintf(stdout, "old seed moved to %s\n", m)
		}
	} else {
		fmt.Fprintf(stdout, "no seed is held here for the old key, so none was moved to keys/revoked\n")
	}
	if fileExists(newSeedHeld) {
		if err := os.Rename(newSeedHeld, oldSeedHeld); err != nil {
			fmt.Fprintf(stderr, "hub: cannot move %s to %s: %v\n", newSeedHeld, oldSeedHeld, err)
			return 1
		}
	}
	fmt.Fprintf(stdout, "finished the rotation of %s; public key: %s\n", u.Name, newPub)
	if backup != "" {
		fmt.Fprintf(stdout, "backup: %s\n", backup)
	}
	return 0
}

// --- revoke -------------------------------------------------------------------------------------

// userConns counts the open client connections authenticated with one of keys,
// as the monitor reports them. ok is false when the monitor cannot be read.
func userConns(monitorURL string, keys ...string) (n int, ok bool) {
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(strings.TrimRight(monitorURL, "/") + "/connz?auth=true&limit=4096")
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, false
	}
	var cz struct {
		Conns []struct {
			User string `json:"authorized_user"`
		} `json:"connections"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cz); err != nil {
		return 0, false
	}
	for _, cn := range cz.Conns {
		for _, k := range keys {
			if cn.User == k {
				n++
			}
		}
	}
	return n, true
}

var _ = regexp.MustCompile
var _ nats.Conn

func hubUserRevoke(cfg hubCfg, args []string, stdout, stderr io.Writer) int {
	name, err := parseRevokeArgs(args)
	if err != nil {
		return usageErr(stderr, err.Error())
	}
	release, err := hubLock(cfg, "revoke")
	if err != nil {
		fmt.Fprintf(stderr, "hub: %v\n", err)
		return 1
	}
	defer release()
	bin, code := hubPreflight(cfg, stderr)
	if code != 0 {
		return code
	}
	reg, err := loadRegistry(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "hub: %v. Nothing was changed\n", err)
		return 1
	}
	u := reg.find(name)
	if u == nil {
		fmt.Fprintf(stderr, "hub: there is no user named %s in the registry. Nothing was changed\n", name)
		return 1
	}
	if u.State == "revoked" {
		fmt.Fprintf(stderr, "hub: %s is already revoked. Nothing was changed\n", name)
		return 1
	}
	keys := []string{u.PublicKey}
	if u.PendingKey != "" {
		keys = append(keys, u.PendingKey)
	}
	// The admin the command itself uses must not be revoked from under it.
	if p := seedPublic(cfg.adminKey); p != "" {
		for _, k := range keys {
			if k == p {
				fmt.Fprintf(stderr, "hub: %s holds the admin key that DIRECTOR_HUB_ADMIN_NKEY (%s) names. Repoint DIRECTOR_HUB_ADMIN_NKEY at another admin's seed first. Nothing was changed\n", name, cfg.adminKey)
				return 1
			}
		}
	}
	if u.Class == "admin" {
		others := 0
		for i := range reg.Users {
			if o := &reg.Users[i]; o.Class == "admin" && o.State == "active" && o.Name != name {
				others++
			}
		}
		if others == 0 {
			fmt.Fprintf(stderr, "hub: %s is the last admin in the registry. Revoking it would leave none. Add another admin first; if the hub has a hand-written admin block, run `director hub user adopt` on it so it is counted. Nothing was changed\n", name)
			return 1
		}
	}
	conf, err := os.ReadFile(cfg.conf)
	if err != nil {
		fmt.Fprintf(stderr, "hub: cannot read the hub conf: %v. Nothing was changed\n", err)
		return 1
	}
	for _, k := range keys {
		if _, in := entryBlock(string(conf), k); in && !regionHas(string(conf), k) {
			fmt.Fprintf(stderr, "hub: the key %s of %s is in a block outside the managed region, which this command does not edit. Remove that block by hand and reload the hub, then run revoke again. Nothing was changed\n", k, name)
			return 1
		}
	}
	var present []string
	for _, k := range keys {
		if regionHas(string(conf), k) {
			present = append(present, k)
		}
	}
	seeds := []struct{ path, label string }{
		{filepath.Join(cfg.keys, name+".nk"), name},
		{filepath.Join(cfg.keys, name+".next.nk"), name + "-next"},
	}
	held := seeds[:0:0]
	for _, s := range seeds {
		if fileExists(s.path) {
			held = append(held, s)
		}
	}
	revokedAt := time.Now().UTC().Format(time.RFC3339)
	finishSeeds := func(stdout io.Writer) int {
		if len(held) == 0 {
			fmt.Fprintf(stdout, "no seed is held here, so none was moved to keys/revoked\n")
		}
		for _, s := range held {
			m, err := revokeSeedFile(cfg, s.path, s.label)
			if err != nil {
				fmt.Fprintf(stderr, "hub: cannot move %s: %v\n", s.path, err)
				return 1
			}
			fmt.Fprintf(stdout, "seed moved to %s\n", m)
		}
		return 0
	}
	if len(present) == 0 {
		// Every key of the user is already out of the conf. Record it, after the
		// live hub is shown to refuse each key it can be tried with.
		for _, s := range held {
			if !awaitRefused(cfg, s.path, 3*time.Second) {
				fmt.Fprintf(stderr, "hub: the key in %s is still accepted by the live hub although the conf does not hold it. Reload the hub, then run revoke again. Nothing was changed\n", s.path)
				return 1
			}
		}
		if len(held) == 0 {
			fmt.Fprintf(stdout, "note: no seed is held here, so the live hub was not asked to refuse the key\n")
		}
		u.State, u.Revoked = "revoked", revokedAt
		if err := saveRegistry(cfg, reg); err != nil {
			fmt.Fprintf(stderr, "hub: cannot write the registry: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "revoked %s: absent from the conf; recorded only (no conf write, no reload)\n", name)
		return finishSeeds(stdout)
	}
	candidate := string(conf)
	for _, k := range present {
		candidate, _ = removeEntry(candidate, k)
	}
	candPath, cleanup, err := stageCandidate(cfg, map[string][]byte{filepath.Base(cfg.conf): []byte(candidate)})
	if err != nil {
		fmt.Fprintf(stderr, "hub: %v. Nothing was changed\n", err)
		return 1
	}
	liveSt, _ := readSettingsState(cfg)
	out, valid := serverTest(bin, candPath, launcherEnvList(liveSt))
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
	admin.Close()
	before, connsOK := userConns(cfg.monitorURL, keys...)
	backup, err := backupConf(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "hub: cannot back up the conf: %v. Nothing was changed\n", err)
		return 1
	}
	st, _ := os.Stat(cfg.conf)
	if err := writeFileAtomic(cfg.conf, []byte(candidate), st.Mode().Perm()); err != nil {
		fmt.Fprintf(stderr, "hub: cannot write the conf: %v. The conf may be partly written: restore %s\n", err, backup)
		return 1
	}
	fail := func(why string) int {
		restored := restoreConf(cfg, backup)
		fmt.Fprintf(stderr, "hub: %s\nRolled back: %s restored from %s and the hub reloaded; the key is still accepted", why, filepath.Base(cfg.conf), filepath.Base(backup))
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
	for _, s := range held {
		if !awaitRefused(cfg, s.path, 5*time.Second) {
			return fail(fmt.Sprintf("the key in %s is still accepted after the reload", filepath.Base(s.path)))
		}
	}
	if len(held) == 0 {
		fmt.Fprintf(stdout, "no seed is held here, so the live hub was not asked to refuse the key; the conf no longer holds it\n")
	}
	u.State, u.Revoked = "revoked", revokedAt
	if err := saveRegistry(cfg, reg); err != nil {
		fmt.Fprintf(stderr, "hub: the key is removed but the registry write failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "revoked %s\n", name)
	if connsOK {
		after := before
		for end := time.Now().Add(3 * time.Second); after > 0 && time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
			if n, ok := userConns(cfg.monitorURL, keys...); ok {
				after = n
			}
		}
		switch {
		case before == 0:
			fmt.Fprintf(stdout, "open connections: 0 at the reload\n")
		case after == 0:
			fmt.Fprintf(stdout, "%d open connection%s closed by the reload\n", before, plural(before))
		default:
			fmt.Fprintf(stdout, "%d open connection%s still open\n", after, plural(after))
		}
	} else {
		fmt.Fprintf(stdout, "open connections: unknown (the monitor's connection list could not be read)\n")
	}
	if code := finishSeeds(stdout); code != 0 {
		return code
	}
	fmt.Fprintf(stdout, "backup: %s\n", backup)
	return 0
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
