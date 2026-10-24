package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The seat launch resolves its own identity and bus credentials, so every host
// registers the one command, director-mcp (aae-orc-crukm). The chain is the one
// director-mcp-seat carried, with its director-seat fallback turned into a
// refusal: explicit DIRECTOR_* wins, then marvel's stamps, then the cluster's
// seat password, then refuse.

// seatVars is every variable the chain reads. Each case starts with all of
// them empty, so nothing from the test process leaks in.
var seatVars = []string{
	"DIRECTOR_AGENT_ID", "DIRECTOR_ROLE", "DIRECTOR_TEAM", "DIRECTOR_WORKSPACE",
	"MARVEL_SESSION", "MARVEL_TEAM", "MARVEL_WORKSPACE",
	"DIRECTOR_NATS_CREDS", "DIRECTOR_NATS_USER", "DIRECTOR_NATS_PASS",
	"MARVEL_BUS_STATE",
}

func TestResolveSeatEnv(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		// seat is the content of director.pass under MARVEL_BUS_STATE; nil
		// means no file.
		seat *string
		// want, when err is empty.
		id, team, ws, user, pass string
		// err is a substring the refusal must carry; empty means success.
		err []string
		// note holds substrings the stderr note must carry; empty means none.
		note []string
		// seatDir puts a directory where director.pass goes, so it cannot be read.
		seatDir bool
		// refuse sets the no-credential switch to refuse; skip skips the case.
		refuse bool
		skip   string
	}{
		{
			name: "managed: marvel's stamps and its own credentials",
			env: map[string]string{
				"MARVEL_SESSION": "aae-orc-builder-g9-0", "MARVEL_TEAM": "aae-orc", "MARVEL_WORKSPACE": "aae",
				"DIRECTOR_NATS_USER": "aae-orc", "DIRECTOR_NATS_PASS": "from-marvel",
			},
			id: "aae-orc-builder-g9-0", team: "aae-orc", ws: "aae",
		},
		{
			name: "explicit DIRECTOR_* wins over marvel's stamps",
			env: map[string]string{
				"DIRECTOR_AGENT_ID": "planner", "DIRECTOR_TEAM": "ops", "DIRECTOR_WORKSPACE": "w",
				"MARVEL_SESSION": "other", "MARVEL_TEAM": "other", "MARVEL_WORKSPACE": "other",
				"DIRECTOR_NATS_USER": "ops", "DIRECTOR_NATS_PASS": "p",
			},
			seat: ptr("seat-pass\n"),
			id:   "planner", team: "ops", ws: "w",
		},
		{
			name: "hand-run with a seat: user director and the seat password",
			env:  map[string]string{"DIRECTOR_AGENT_ID": "hand-kinu-7k2m", "DIRECTOR_TEAM": "ops", "DIRECTOR_WORKSPACE": "w"},
			seat: ptr("s3cret\n\n"),
			id:   "hand-kinu-7k2m", team: "ops", ws: "w", user: "director", pass: "s3cret",
		},
		{
			name: "a user without a password falls back to the seat, as the wrapper did",
			env:  map[string]string{"DIRECTOR_AGENT_ID": "hand-kinu-7k2m", "DIRECTOR_TEAM": "ops", "DIRECTOR_WORKSPACE": "w", "DIRECTOR_NATS_USER": "ops"},
			seat: ptr("s3cret"),
			id:   "hand-kinu-7k2m", team: "ops", ws: "w", user: "director", pass: "s3cret",
		},
		{
			name: "a creds file is a credential of its own: no seat read",
			env:  map[string]string{"DIRECTOR_AGENT_ID": "hand-kinu-7k2m", "DIRECTOR_TEAM": "ops", "DIRECTOR_WORKSPACE": "w", "DIRECTOR_NATS_CREDS": "/x/ops.creds"},
			id:   "hand-kinu-7k2m", team: "ops", ws: "w",
		},
		{
			name: "hand-run missing team refuses instead of defaulting",
			env:  map[string]string{"DIRECTOR_AGENT_ID": "hand-kinu-7k2m", "DIRECTOR_WORKSPACE": "w"},
			seat: ptr("s3cret"),
			err:  []string{"DIRECTOR_TEAM and DIRECTOR_WORKSPACE are required", "outside a marvel session"},
		},
		{
			name: "hand-run missing workspace refuses instead of defaulting",
			env:  map[string]string{"DIRECTOR_AGENT_ID": "hand-kinu-7k2m", "DIRECTOR_TEAM": "ops"},
			seat: ptr("s3cret"),
			err:  []string{"DIRECTOR_TEAM and DIRECTOR_WORKSPACE are required"},
		},
		{
			name: "missing seat file connects anonymously, as today, and says so",
			env:  map[string]string{"DIRECTOR_AGENT_ID": "hand-kinu-7k2m", "DIRECTOR_TEAM": "ops", "DIRECTOR_WORKSPACE": "w"},
			id:   "hand-kinu-7k2m", team: "ops", ws: "w",
			note: []string{"no seat password at", "director.pass", "connecting anonymously"},
		},
		{
			name:   "missing seat file refuses and names the path",
			skip:   "open question: whether a seat with no credential refuses or connects anonymously is the auth posture's call (bus.go keeps the anonymous connect for a coordinated relaunch); refuseWithoutCredential is the one switch",
			refuse: true,
			env:    map[string]string{"DIRECTOR_AGENT_ID": "hand-kinu-7k2m", "DIRECTOR_TEAM": "ops", "DIRECTOR_WORKSPACE": "w"},
			err:    []string{"no bus credentials in the environment", "director.pass", "seat: { workspace: <ws>, team: <team> }"},
		},
		{
			name:    "an unreadable seat file refuses",
			env:     map[string]string{"DIRECTOR_AGENT_ID": "hand-kinu-7k2m", "DIRECTOR_TEAM": "ops", "DIRECTOR_WORKSPACE": "w"},
			seatDir: true,
			err:     []string{"no bus credentials in the environment", "director.pass"},
		},
		{
			name: "an empty seat file refuses",
			env:  map[string]string{"DIRECTOR_AGENT_ID": "hand-kinu-7k2m", "DIRECTOR_TEAM": "ops", "DIRECTOR_WORKSPACE": "w"},
			seat: ptr("\n"),
			err:  []string{"no bus credentials in the environment", "director.pass"},
		},
		{
			name: "no id and no marvel stamp refuses and names the hand-run launcher",
			env:  map[string]string{"DIRECTOR_TEAM": "ops", "DIRECTOR_WORKSPACE": "w"},
			seat: ptr("s3cret"),
			err:  []string{"no DIRECTOR_AGENT_ID and no MARVEL_SESSION", "director-session"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.skip != "" {
				t.Skip(tc.skip)
			}
			if tc.refuse {
				old := refuseWithoutCredential
				refuseWithoutCredential = true
				t.Cleanup(func() { refuseWithoutCredential = old })
			}
			for _, k := range seatVars {
				t.Setenv(k, "")
			}
			state := t.TempDir()
			t.Setenv("MARVEL_BUS_STATE", state)
			if tc.seat != nil {
				if err := os.WriteFile(filepath.Join(state, "director.pass"), []byte(*tc.seat), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.seatDir {
				if err := os.Mkdir(filepath.Join(state, "director.pass"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			se, err := resolveSeatEnv()
			if len(tc.err) > 0 {
				if err == nil {
					t.Fatalf("resolved %+v, want a refusal carrying %q", se, tc.err)
				}
				for _, s := range tc.err {
					if !strings.Contains(err.Error(), s) {
						t.Errorf("refusal %q does not carry %q", err, s)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			if se.self.AgentID != tc.id || se.self.Team != tc.team || se.self.Workspace != tc.ws {
				t.Errorf("identity = %s/%s in %s, want %s/%s in %s", se.self.Team, se.self.AgentID, se.self.Workspace, tc.team, tc.id, tc.ws)
			}
			if se.natsUser != tc.user || se.natsPass != tc.pass {
				t.Errorf("credential = %q/%q, want %q/%q", se.natsUser, se.natsPass, tc.user, tc.pass)
			}
			if len(tc.note) == 0 && se.note != "" {
				t.Errorf("unexpected note %q", se.note)
			}
			for _, s := range tc.note {
				if !strings.Contains(se.note, s) {
					t.Errorf("note %q does not carry %q", se.note, s)
				}
			}
		})
	}
}

// With MARVEL_BUS_STATE unset the seat password is read under HOME, as the
// wrapper's ${MARVEL_BUS_STATE:-~/.marvel/state/nats} did.
func TestResolveSeatEnvDefaultsTheBusStateUnderHome(t *testing.T) {
	for _, k := range seatVars {
		t.Setenv(k, "")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".marvel", "state", "nats")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "director.pass"), []byte("from-home\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DIRECTOR_AGENT_ID", "hand-kinu-7k2m")
	t.Setenv("DIRECTOR_TEAM", "ops")
	t.Setenv("DIRECTOR_WORKSPACE", "w")
	se, err := resolveSeatEnv()
	if err != nil {
		t.Fatalf("refused: %v", err)
	}
	if se.natsUser != "director" || se.natsPass != "from-home" {
		t.Fatalf("credential = %q/%q, want director/from-home", se.natsUser, se.natsPass)
	}
}

func ptr(s string) *string { return &s }
