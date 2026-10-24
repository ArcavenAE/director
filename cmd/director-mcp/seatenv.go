package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// The seat launch resolves its own identity and bus credentials, so every host
// and harness registers the one command, director-mcp, for a marvel-managed
// session and a hand-run one alike (aae-orc-crukm). The chain is the one the
// director-mcp-seat wrapper carried, with two changes: its shared fallback id
// (director-seat) is a refusal, because a shared name lets two sessions claim
// one address; and team and workspace no longer default to "default".
//
//	id:         DIRECTOR_AGENT_ID, else MARVEL_SESSION, else refuse
//	team:       DIRECTOR_TEAM, else MARVEL_TEAM, else refuse
//	workspace:  DIRECTOR_WORKSPACE, else MARVEL_WORKSPACE, else refuse
//	credential: DIRECTOR_NATS_CREDS, else DIRECTOR_NATS_USER with _PASS (both
//	            set), else user director with the cluster's seat password at
//	            ${MARVEL_BUS_STATE:-~/.marvel/state/nats}/director.pass, else
//	            the no-credential step below
//
// Explicit DIRECTOR_* still wins, so a launcher that sets them (cast-launch)
// behaves as before. The unread, asks, dossier and ask-reader commands take no
// identity and do not run this chain.

// refuseWithoutCredential is the chain's last step, for a seat with no
// credential in its environment and no seat password file. It is false: the
// shim connects anonymously, as it always has, and says so on stderr. Whether
// that step should refuse instead is an open question on the auth posture
// (bus.go keeps the anonymous connect for a coordinated relaunch), so the
// answer is this one value.
var refuseWithoutCredential = false

// seatEnv is what the seat launch resolves from its environment before it
// validates anything or connects: the identity it registers under, and the
// broker user and password when it must supply them itself. An empty natsUser
// leaves the credential the environment already carries, if any, to dial.
type seatEnv struct {
	self     Sender
	natsUser string
	natsPass string
	// note is a line for stderr, set when the seat connects with no credential.
	note string
}

const seatTeamRefusal = "DIRECTOR_TEAM and DIRECTOR_WORKSPACE are required\n" +
	"  outside a marvel session. Set them in the registration, or run this\n" +
	"  inside a session marvel spawned."

// resolveSeatEnv reads the seat launch's identity and credential from the
// environment, per the chain above.
func resolveSeatEnv() (seatEnv, error) {
	se := seatEnv{self: Sender{
		AgentID:   env("DIRECTOR_AGENT_ID", os.Getenv("MARVEL_SESSION")),
		Role:      os.Getenv("DIRECTOR_ROLE"),
		Team:      env("DIRECTOR_TEAM", os.Getenv("MARVEL_TEAM")),
		Workspace: env("DIRECTOR_WORKSPACE", os.Getenv("MARVEL_WORKSPACE")),
	}}
	if se.self.AgentID == "" {
		return se, errors.New("no DIRECTOR_AGENT_ID and no MARVEL_SESSION. Run inside a marvel\n" +
			"  session, or start a hand-run seat with the director-session launcher\n" +
			"  (probe/nats-phase-0/id-a-demo/director-session.sh), which assigns a unique id.")
	}
	if se.self.Team == "" || se.self.Workspace == "" {
		return se, errors.New(seatTeamRefusal)
	}
	if os.Getenv("DIRECTOR_NATS_CREDS") != "" {
		return se, nil
	}
	if os.Getenv("DIRECTOR_NATS_USER") != "" && os.Getenv("DIRECTOR_NATS_PASS") != "" {
		return se, nil
	}
	// With no MARVEL_BUS_STATE and no HOME there is no seat password to find,
	// which is the no-credential step, not a refusal of its own.
	state := os.Getenv("MARVEL_BUS_STATE")
	if state == "" {
		if home, err := os.UserHomeDir(); err == nil {
			state = filepath.Join(home, ".marvel", "state", "nats")
		}
	}
	passFile := filepath.Join(state, "director.pass")
	if state == "" {
		passFile = "~/.marvel/state/nats/director.pass (no HOME)"
	}
	noSeat := "no bus credentials in the environment, and no seat\n" +
		"  password at " + passFile + ".\n" +
		"  Either run inside a marvel session, or declare a seat on the\n" +
		"  cluster's bus and restart the daemon:\n" +
		"    bus: { managed: true, seat: { workspace: <ws>, team: <team> } }"
	var b []byte
	err := os.ErrNotExist
	if state != "" {
		b, err = os.ReadFile(passFile)
	}
	if errors.Is(err, os.ErrNotExist) {
		if refuseWithoutCredential {
			return se, errors.New(noSeat)
		}
		se.note = "no bus credentials in the environment and no seat password at " + passFile + "; connecting anonymously"
		return se, nil
	}
	// Strip trailing newlines only, as the wrapper's $(cat) did.
	pass := strings.TrimRight(string(b), "\n")
	if err != nil || pass == "" {
		return se, errors.New(noSeat)
	}
	se.natsUser, se.natsPass = "director", pass
	return se, nil
}
