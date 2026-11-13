package main

// The environment a hub's launcher gives its nats-server. A conf may reference
// $NAME for a variable the launcher exports at each start (an address it works
// out from the network, say). The hub commands read that environment from the
// pid-file process, behind the same guard reloadHub uses, so a name the launcher
// supplies is not reported as missing and the -t runs see the values the server
// has.

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// hubProcEnv returns the environment of the hub's server process. It is a
// variable so tests can run where the setuid ps cannot (a sandbox-exec
// profile); one test uses the real one.
var hubProcEnv = realHubProcEnv

func realHubProcEnv(pid int) (map[string]string, error) {
	out, err := exec.Command("ps", "eww", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return nil, fmt.Errorf("ps eww -p %d: %v", pid, err)
	}
	env := parseProcEnv(string(out))
	if len(env) == 0 {
		return nil, fmt.Errorf("ps eww -p %d printed no environment (another user's process, or a platform whose ps does not show it)", pid)
	}
	return env, nil
}

var envWordRe = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)

// parseProcEnv reads the NAME=VALUE words that `ps eww` prints after the command
// line. A value is kept as printed, quotes included. A value that contains a
// space parses wrong: ps prints the environment as one space-separated line, so
// the value ends at its first space.
func parseProcEnv(s string) map[string]string {
	env := map[string]string{}
	for _, w := range strings.Fields(s) {
		if m := envWordRe.FindStringSubmatch(w); m != nil {
			env[m[1]] = m[2]
		}
	}
	return env
}

// hubLauncherEnv reads the running hub's environment. It fails, without
// reading anything, when the pid file does not name this hub's nats-server.
func hubLauncherEnv(cfg hubCfg) (map[string]string, error) {
	pid, err := hubPid(cfg)
	if err != nil {
		return nil, err
	}
	return hubProcEnv(pid)
}

var plainNumberRe = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})$`)

// hubSettingLiteral is how init writes a value into settings.conf. A plain
// number is written bare, because nats-server rejects a quoted string where it
// wants a number (a port). A leading zero would change the number when read
// bare, and ten digits can overflow an int, so those stay quoted.
func hubSettingLiteral(v string) string {
	if plainNumberRe.MatchString(v) {
		return v
	}
	return `"` + v + `"`
}
