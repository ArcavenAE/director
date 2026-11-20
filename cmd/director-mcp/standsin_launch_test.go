package main

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"
)

// Coverage for behaviour DIRECTOR_STANDS_IN_FOR already has (director#729): the
// launch read in resolveSeatEnv and its log line. Nothing here fails before a
// fix; it pins what exists.

func TestResolveSeatEnvReadsStandsInFor(t *testing.T) {
	for _, tc := range []struct {
		name, value, want string
		logged            []string // substrings the log must carry; none means the log stays empty
	}{
		{"a team name is kept", "cue", "cue", nil},
		{"unset stands in for no one", "", "", nil},
		{"a malformed value is logged and ignored", "cue.ent", "", []string{`"cue.ent"`, "ignored"}},
		{"the seat's own team is logged and ignored", "ops", "", []string{`"ops"`, "ignored", "its own team"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range seatVars {
				t.Setenv(k, "")
			}
			t.Setenv("MARVEL_BUS_STATE", t.TempDir())
			t.Setenv("DIRECTOR_AGENT_ID", "mirror-supervisor-g1-0")
			t.Setenv("DIRECTOR_TEAM", "ops")
			t.Setenv("DIRECTOR_WORKSPACE", "w")
			t.Setenv("DIRECTOR_NATS_USER", "u")
			t.Setenv("DIRECTOR_NATS_PASS", "p")
			t.Setenv("DIRECTOR_STANDS_IN_FOR", tc.value)
			var buf bytes.Buffer
			log.SetOutput(&buf)
			t.Cleanup(func() { log.SetOutput(os.Stderr) })
			se, err := resolveSeatEnv()
			if err != nil {
				t.Fatalf("a bad stand-in value must never refuse the launch: %v", err)
			}
			if se.self.StandsIn != tc.want {
				t.Errorf("StandsIn = %q, want %q", se.self.StandsIn, tc.want)
			}
			out := buf.String()
			if len(tc.logged) == 0 && strings.Contains(out, "DIRECTOR_STANDS_IN_FOR") {
				t.Errorf("logged %q, want nothing about DIRECTOR_STANDS_IN_FOR", out)
			}
			for _, s := range tc.logged {
				if !strings.Contains(out, s) {
					t.Errorf("log %q does not carry %q", out, s)
				}
			}
		})
	}
}
