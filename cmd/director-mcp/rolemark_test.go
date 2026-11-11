package main

import (
	"strconv"
	"strings"
	"testing"
)

func TestDirectorClaim(t *testing.T) {
	cases := []struct {
		id, role, grole, want string
	}{
		{"director", "", "", "DIRECTOR_AGENT_ID=director"},
		{"reviewer-a", roleDirector, "", "DIRECTOR_ROLE=director"},
		{"x", "", roleDirector, "DIRECTOR_GLOBAL_ROLE=director"},
		{"bootstrap-supervisor-g1-0", "supervisor", roleSupervisor, ""},
		{"verifydirector", "", "", ""},
	}
	for _, c := range cases {
		if got := directorClaim(c.id, c.role, c.grole); got != c.want {
			t.Errorf("directorClaim(%q,%q,%q) = %q, want %q", c.id, c.role, c.grole, got, c.want)
		}
	}
}

func TestCheckRoleMark(t *testing.T) {
	const ppid = 4242
	claim := "DIRECTOR_AGENT_ID=director"
	if err := checkRoleMark("", "", ppid); err != nil {
		t.Errorf("no claim, no mark: %v", err)
	}
	if err := checkRoleMark(claim, strconv.Itoa(ppid), ppid); err != nil {
		t.Errorf("matching mark refused: %v", err)
	}
	for _, mark := range []string{"", "1", "4243", "director", " 4242"} {
		err := checkRoleMark(claim, mark, ppid)
		if err == nil {
			t.Errorf("mark %q accepted for parent %d", mark, ppid)
			continue
		}
		if !strings.Contains(err.Error(), "refusing the director identity") || !strings.Contains(err.Error(), claim) {
			t.Errorf("mark %q: refusal does not name the claim: %v", mark, err)
		}
	}
}
