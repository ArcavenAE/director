package main

// director hub user and director hub settings: the lifecycle of the global hub's
// users as a director command (sim/design/hub-user-lifecycle.md).
//
//	director-mcp hub user add NAME --class C [--param k=v]... [--public-key U]
//	director-mcp hub user list [--json]
//	director-mcp hub user adopt PUBLIC_KEY --name NAME --class C [--param k=v]...
//	director-mcp hub settings check
//	director-mcp hub settings init [--set NAME=VALUE]...
//
// The command runs on the hub's host. It reads its settings from the
// environment, each with a default under the user's home, and names no host,
// account or path of any one deployment:
//
//	DIRECTOR_GLOBAL_HOME      hub conf, keys/, pid file, log/, registry (default ~/.director/nats-global)
//	DIRECTOR_HUB_ADMIN_URL    where the command reaches the hub as admin (default nats://127.0.0.1:4242)
//	DIRECTOR_HUB_CA           CA file, when the client port has TLS
//	DIRECTOR_HUB_ADMIN_NKEY   admin seed (default $DIRECTOR_GLOBAL_HOME/keys/admin.nk)
//	DIRECTOR_HUB_MONITOR_URL  for list's connected column (default http://127.0.0.1:8242)

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

//go:embed hubclasses/*.conf
var hubClassesEmbedded embed.FS

// hubClassFS holds one conf template per user class (admin, director, leaf,
// observer), named <class>.conf. Nil means the embedded set; tests replace it to
// make a template wrong on purpose.
var hubClassFS fs.FS

func hubClasses() fs.FS {
	if hubClassFS != nil {
		return hubClassFS
	}
	sub, err := fs.Sub(hubClassesEmbedded, "hubclasses")
	if err != nil {
		return hubClassesEmbedded
	}
	return sub
}

const hubUsage = `usage: director hub user add NAME --class CLASS [--param k=v]... [--public-key U...]
       director hub user list [--json]
       director hub user adopt PUBLIC_KEY --name NAME --class CLASS [--param k=v]...
       director hub settings check
       director hub settings init [--set NAME=VALUE]... [--env NAME[=VALUE]]...
         (--env names a variable the hub's launcher supplies; it stays out of settings.conf)`

// hubCfg is where the command finds the hub.
type hubCfg struct {
	home, conf, settings, keys, pidfile, registry, lock string
	adminURL, ca, adminKey, monitorURL                  string
}

func hubCfgFrom(get func(string) string) (hubCfg, error) {
	pick := func(k, def string) string {
		if v := get(k); v != "" {
			return v
		}
		return def
	}
	home := get("DIRECTOR_GLOBAL_HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return hubCfg{}, fmt.Errorf("DIRECTOR_GLOBAL_HOME is not set and the home directory is unknown: %v", err)
		}
		home = filepath.Join(h, ".director", "nats-global")
	}
	c := hubCfg{
		home:     home,
		conf:     filepath.Join(home, "nats-server.conf"),
		settings: filepath.Join(home, "settings.conf"),
		keys:     filepath.Join(home, "keys"),
		pidfile:  filepath.Join(home, "nats-server.pid"),
		registry: filepath.Join(home, "users.json"),
		lock:     filepath.Join(home, ".hub-user.lock"),
	}
	c.adminURL = pick("DIRECTOR_HUB_ADMIN_URL", "nats://127.0.0.1:4242")
	c.ca = get("DIRECTOR_HUB_CA")
	c.adminKey = pick("DIRECTOR_HUB_ADMIN_NKEY", filepath.Join(keysDirOf(home), "admin.nk"))
	c.monitorURL = pick("DIRECTOR_HUB_MONITOR_URL", "http://127.0.0.1:8242")
	return c, nil
}

func keysDirOf(home string) string { return filepath.Join(home, "keys") }

// hubAfterReload, when a test sets it, runs right after the command reloads the
// hub and before it verifies. It lets a test act while the new conf is live.
var hubAfterReload func()

// runHubCmd runs `director-mcp hub ...`. args are the words after "hub". getenv
// reads the command's settings, so a test can hand it a scratch hub home. PATH
// is read from the process environment, not from getenv. Exit codes: 0 done, 1
// refused or rolled back, 2 usage.
func runHubCmd(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	usage := func(msg string) int {
		if msg != "" {
			fmt.Fprintf(stderr, "hub: %s\n", msg)
		}
		fmt.Fprintln(stderr, hubUsage)
		return 2
	}
	if len(args) == 0 {
		return usage("a command is needed")
	}
	cfg, err := hubCfgFrom(getenv)
	if err != nil {
		fmt.Fprintf(stderr, "hub: %v\n", err)
		return 1
	}
	switch args[0] {
	case "user":
		if len(args) < 2 {
			return usage("user needs a verb")
		}
		switch args[1] {
		case "add":
			return hubUserAdd(cfg, args[2:], stdout, stderr)
		case "list":
			return hubUserList(cfg, args[2:], stdout, stderr)
		case "adopt":
			return hubUserAdopt(cfg, args[2:], stdout, stderr)
		}
		return usage(fmt.Sprintf("unknown user verb %q", args[1]))
	case "settings":
		if len(args) < 2 {
			return usage("settings needs a verb")
		}
		switch args[1] {
		case "check":
			if len(args) != 2 {
				return usage("settings check takes no arguments")
			}
			return hubSettingsCheck(cfg, stdout, stderr)
		case "init":
			return hubSettingsInit(cfg, args[2:], stdout, stderr)
		}
		return usage(fmt.Sprintf("unknown settings verb %q", args[1]))
	}
	return usage(fmt.Sprintf("unknown command %q", args[0]))
}

// hubLock takes the hub home's lock for the life of a changing verb. It is an
// flock, so a crashed command leaves nothing to clean up.
var hubInLock func(verb string)

func hubLock(cfg hubCfg, verb string) (release func(), err error) {
	f, err := os.OpenFile(cfg.lock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("cannot open the lock %s: %v", cfg.lock, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another hub command holds %s; nothing was changed", cfg.lock)
	}
	if hubInLock != nil {
		hubInLock(verb)
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}

var (
	hubNameRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
	hubValueRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
)

// parseParams reads repeated --param k=v words.
func parseParams(vals []string) (map[string]string, error) {
	m := map[string]string{}
	for _, v := range vals {
		k, val, ok := strings.Cut(v, "=")
		if !ok || !hubNameRe.MatchString(k) {
			return nil, fmt.Errorf("--param %q is not name=value", v)
		}
		if !hubValueRe.MatchString(val) {
			return nil, fmt.Errorf("--param %s=%q: a value is letters, digits, dot, dash and underscore only", k, val)
		}
		if _, dup := m[k]; dup {
			return nil, fmt.Errorf("--param %s is given twice", k)
		}
		m[k] = val
	}
	return m, nil
}
