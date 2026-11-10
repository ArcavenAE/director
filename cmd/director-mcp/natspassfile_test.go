package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/nats.go"
)

// DIRECTOR_NATS_PASS_FILE names a 0600 file holding the bus password, so the
// secret stays out of argv and the process listing (aae-orc-xs6om). Every
// password in these tests is a fixture written into a scratch directory.

func writePassFile(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "bus.pass")
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil { // WriteFile is subject to umask
		t.Fatal(err)
	}
	return p
}

func clearNatsEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"DIRECTOR_NATS_CREDS", "DIRECTOR_NATS_USER", "DIRECTOR_NATS_PASS", "DIRECTOR_NATS_PASS_FILE"} {
		t.Setenv(k, "")
	}
}

// userInfoOf applies the dial credential option to a bare Options and returns
// what the client would present on a connect.
func userInfoOf(t *testing.T) func() (string, string) {
	t.Helper()
	opt, err := natsAuthOption()
	if err != nil {
		t.Fatalf("natsAuthOption: %v", err)
	}
	if opt == nil {
		t.Fatal("no credential option")
	}
	var o nats.Options
	if err := opt(&o); err != nil {
		t.Fatal(err)
	}
	if o.UserInfo != nil {
		return o.UserInfo
	}
	return func() (string, string) { return o.User, o.Password }
}

func TestPassFileIsReadAndWinsOverTheEnvValue(t *testing.T) {
	clearNatsEnv(t)
	t.Setenv("DIRECTOR_NATS_USER", "team-a")
	t.Setenv("DIRECTOR_NATS_PASS", "env-fixture")
	t.Setenv("DIRECTOR_NATS_PASS_FILE", writePassFile(t, "file-fixture\n\n", 0o600))
	u, p := userInfoOf(t)()
	if u != "team-a" || p != "file-fixture" {
		t.Errorf("presented %q/%q, want team-a/file-fixture", u, p)
	}
}

func TestEnvPassStillWorksWithoutAFile(t *testing.T) {
	clearNatsEnv(t)
	t.Setenv("DIRECTOR_NATS_USER", "team-a")
	t.Setenv("DIRECTOR_NATS_PASS", "env-fixture")
	u, p := userInfoOf(t)()
	if u != "team-a" || p != "env-fixture" {
		t.Errorf("presented %q/%q, want team-a/env-fixture", u, p)
	}
}

func TestNoCredentialStaysAnonymous(t *testing.T) {
	clearNatsEnv(t)
	opt, err := natsAuthOption()
	if err != nil || opt != nil {
		t.Errorf("got option=%v err=%v, want neither", opt != nil, err)
	}
}

func TestCredsFileStillWinsOverThePassFile(t *testing.T) {
	clearNatsEnv(t)
	t.Setenv("DIRECTOR_NATS_CREDS", "/x/ops.creds")
	t.Setenv("DIRECTOR_NATS_USER", "team-a")
	t.Setenv("DIRECTOR_NATS_PASS_FILE", filepath.Join(t.TempDir(), "absent"))
	opt, err := natsAuthOption()
	if err != nil || opt == nil {
		t.Fatalf("creds path: option=%v err=%v", opt != nil, err)
	}
}

func TestPassFileReReadOnEveryConnect(t *testing.T) {
	clearNatsEnv(t)
	t.Setenv("DIRECTOR_NATS_USER", "team-a")
	p := writePassFile(t, "first-fixture", 0o600)
	t.Setenv("DIRECTOR_NATS_PASS_FILE", p)
	ui := userInfoOf(t)
	if _, got := ui(); got != "first-fixture" {
		t.Fatalf("first connect presented %q", got)
	}
	if err := os.WriteFile(p, []byte("second-fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, got := ui(); got != "second-fixture" {
		t.Errorf("reconnect presented %q, want the rewritten file's value", got)
	}
}

func TestPassFileLostAfterStartDoesNotFallBackToTheEnvValue(t *testing.T) {
	clearNatsEnv(t)
	t.Setenv("DIRECTOR_NATS_USER", "team-a")
	t.Setenv("DIRECTOR_NATS_PASS", "env-fixture")
	p := writePassFile(t, "file-fixture", 0o600)
	t.Setenv("DIRECTOR_NATS_PASS_FILE", p)
	ui := userInfoOf(t)
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if _, got := ui(); got != "" {
		t.Errorf("a reconnect with the file gone presented %q, want an empty password the broker refuses", got)
	}
}

func TestPassFileRefusals(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		path func(t *testing.T) string
		want []string
	}{
		{"missing", func(t *testing.T) string { return filepath.Join(dir, "absent") }, []string{"DIRECTOR_NATS_PASS_FILE", "absent"}},
		{"group readable", func(t *testing.T) string { return writePassFile(t, "pw-fixture", 0o640) }, []string{"DIRECTOR_NATS_PASS_FILE", "0600"}},
		{"world readable", func(t *testing.T) string { return writePassFile(t, "pw-fixture", 0o644) }, []string{"DIRECTOR_NATS_PASS_FILE", "0600"}},
		{"empty", func(t *testing.T) string { return writePassFile(t, "\n", 0o600) }, []string{"DIRECTOR_NATS_PASS_FILE", "empty"}},
		{"a directory", func(t *testing.T) string { return dir }, []string{"DIRECTOR_NATS_PASS_FILE", "regular file"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearNatsEnv(t)
			t.Setenv("DIRECTOR_NATS_USER", "team-a")
			t.Setenv("DIRECTOR_NATS_PASS", "env-fixture") // must not rescue a bad file
			t.Setenv("DIRECTOR_NATS_PASS_FILE", tc.path(t))
			opt, err := natsAuthOption()
			if err == nil {
				t.Fatalf("accepted, option=%v; want a loud refusal", opt != nil)
			}
			for _, s := range tc.want {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("refusal %q does not carry %q", err, s)
				}
			}
			if strings.Contains(err.Error(), "pw-fixture") || strings.Contains(err.Error(), "env-fixture") {
				t.Errorf("refusal %q carries a password value", err)
			}
		})
	}
}

func TestPassFileWithoutAUserRefuses(t *testing.T) {
	clearNatsEnv(t)
	t.Setenv("DIRECTOR_NATS_PASS_FILE", writePassFile(t, "pw-fixture", 0o600))
	_, err := natsAuthOption()
	if err == nil || !strings.Contains(err.Error(), "DIRECTOR_NATS_USER") {
		t.Errorf("err = %v, want a refusal naming DIRECTOR_NATS_USER", err)
	}
}

func TestResolveSeatEnvAcceptsAPassFile(t *testing.T) {
	for _, k := range seatVars {
		t.Setenv(k, "")
	}
	state := t.TempDir() // holds no director.pass: the file must satisfy the chain
	t.Setenv("MARVEL_BUS_STATE", state)
	t.Setenv("DIRECTOR_AGENT_ID", "hand-kinu-7k2m")
	t.Setenv("DIRECTOR_TEAM", "ops")
	t.Setenv("DIRECTOR_WORKSPACE", "w")
	t.Setenv("DIRECTOR_NATS_USER", "ops")
	t.Setenv("DIRECTOR_NATS_PASS_FILE", writePassFile(t, "pw-fixture", 0o600))
	se, err := resolveSeatEnv()
	if err != nil {
		t.Fatalf("refused: %v", err)
	}
	if se.natsUser != "" || se.natsPass != "" || se.note != "" {
		t.Errorf("the chain supplied %q/%q note %q; the environment already carries the credential", se.natsUser, se.natsPass, se.note)
	}
}

func TestResolveSeatEnvRefusesABadPassFile(t *testing.T) {
	for _, k := range seatVars {
		t.Setenv(k, "")
	}
	state := t.TempDir()
	if err := os.WriteFile(filepath.Join(state, "director.pass"), []byte("seat-fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MARVEL_BUS_STATE", state)
	t.Setenv("DIRECTOR_AGENT_ID", "hand-kinu-7k2m")
	t.Setenv("DIRECTOR_TEAM", "ops")
	t.Setenv("DIRECTOR_WORKSPACE", "w")
	t.Setenv("DIRECTOR_NATS_USER", "ops")
	t.Setenv("DIRECTOR_NATS_PASS_FILE", writePassFile(t, "pw-fixture", 0o644))
	_, err := resolveSeatEnv()
	if err == nil || !strings.Contains(err.Error(), "DIRECTOR_NATS_PASS_FILE") {
		t.Errorf("err = %v, want a refusal naming DIRECTOR_NATS_PASS_FILE, not a fall through to the seat file", err)
	}
}

// Against a real (scratch) broker: the password in the file is the one the
// broker checks, and a wrong one is refused.
func TestDialPresentsThePassFileToTheBroker(t *testing.T) {
	_, seatURL := startScratchServerWithSeat(t)
	bare := strings.Replace(seatURL, "seat:seat@", "", 1)
	self := Sender{AgentID: "pf-test", Workspace: "aae-orc", Team: "ops"}

	clearNatsEnv(t)
	t.Setenv("DIRECTOR_NATS_USER", "seat")
	t.Setenv("DIRECTOR_NATS_PASS", "wrong-fixture") // the file must win
	t.Setenv("DIRECTOR_NATS_PASS_FILE", writePassFile(t, "seat\n", 0o600))
	nc, _, err := dial(bare, self)
	if err != nil {
		t.Fatalf("dial with the right password in the file: %v", err)
	}
	nc.Close()

	t.Setenv("DIRECTOR_NATS_PASS_FILE", writePassFile(t, "not-the-password\n", 0o600))
	if nc, _, err := dial(bare, self); err == nil {
		nc.Close()
		t.Error("dial with a wrong password in the file connected")
	}
}
