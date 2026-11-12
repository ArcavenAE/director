package main

import (
	"fmt"
	"strconv"
)

// roleMarkEnv names the variable a director launcher sets to its own pid just
// before it execs the harness, so the value equals the shim's parent pid.
const roleMarkEnv = "DIRECTOR_ROLE_MARK"

// directorClaim names the lever that claims the director identity, or returns
// "" when the seat claims none. The agent id "director" is a claim as well as
// either role lever, since the bus addresses a director by that id. The id is
// named as "agent id", not by variable, since it may come from DIRECTOR_AGENT_ID
// or from MARVEL_SESSION (seatenv.go).
func directorClaim(agentID, role, globalRole string) string {
	switch {
	case globalRole == roleDirector:
		return "DIRECTOR_GLOBAL_ROLE=" + roleDirector
	case role == roleDirector:
		return "DIRECTOR_ROLE=" + roleDirector
	case agentID == roleDirector:
		return "agent id " + roleDirector
	}
	return ""
}

// checkRoleMark refuses a director claim from a shim its launcher did not mark
// (aae-orc-20062, director sim O-2147). A config entry that bakes the director
// identity loads into every harness started in that directory, so a plain
// session there used to register as a second director. The mark must equal the
// shim's parent pid: director-session and cast-launch export it as $$ and then
// exec the harness, which keeps that pid. A static value in a config file
// cannot match, so it is not a way around the check. Refused, never rewritten:
// a shim that is not director has no identity of its own to fall back to.
func checkRoleMark(claim, mark string, ppid int) error {
	if claim == "" {
		return nil
	}
	if mark == "" {
		return fmt.Errorf("refusing the director identity (%s): this shim was not started by a director launcher (%s is unset). "+
			"Start a director with a launcher that sets it (scripts/director start, director-session, or cast-launch); "+
			"a plain harness in this directory is not director, so this server stays down for it (aae-orc-20062)", claim, roleMarkEnv)
	}
	if n, err := strconv.Atoi(mark); err != nil || n != ppid {
		return fmt.Errorf("refusing the director identity (%s): %s=%q does not match this shim's parent pid %d, "+
			"so the mark was not set by the launcher of this session (aae-orc-20062)", claim, roleMarkEnv, mark, ppid)
	}
	return nil
}
