package marry

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// agentIDPattern is the envelope schema's agent_id class (R-76): the closed
// character class every agent id and team part is held to. A test reads it
// back from director's copy of the envelope schema and fails if they differ.
const agentIDPattern = `[a-z0-9][a-z0-9-]{0,31}`

var agentAddress = regexp.MustCompile("^agent://(?:" + agentIDPattern + ")/(?:" + agentIDPattern + ")$")

// CheckAddress accepts only an agent:// seat address of the form
// agent://{team}/{id}, each part in the envelope schema's agent_id class. No
// path through the matcher builds a role://, broadcast:// or global:// address,
// and no wildcard passes, so nothing it produces can fan out.
func CheckAddress(addr string) error {
	rest, ok := strings.CutPrefix(addr, "agent://")
	if !ok {
		scheme, _, found := strings.Cut(addr, "://")
		if !found {
			return errors.New("not a seat address")
		}
		return fmt.Errorf("%s:// is not a seat address", scheme)
	}
	if rest == "" {
		return errors.New("agent:// names no seat")
	}
	if !agentAddress.MatchString(addr) {
		return fmt.Errorf("%s is not agent://{team}/{id}", addr)
	}
	return nil
}
