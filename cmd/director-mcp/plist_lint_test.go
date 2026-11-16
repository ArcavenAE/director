package main

// Lint for the reviewed launchd service files under deploy/launchd (I-510,
// design sim/design/kinu-nats-deployment.md, KN-2). The files are read as
// text and never installed or loaded; nothing here starts a process or opens
// a socket. The hub file is the hub's current host's service file, not a
// decision on who supervises the hub (open question Q1).

import (
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	hubPlistFile      = "../../deploy/launchd/com.arcaven.nats-global.plist"
	rollbackPlistFile = "../../deploy/launchd/rollback/com.arcaven.nats-local.plist"
)

// parsePlist reads an XML property list into map[string]any, []any, string and
// bool values. It handles the element set a launchd service file uses.
func parsePlist(t *testing.T, file string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("service file missing: %s: %v", file, err)
	}
	dec := xml.NewDecoder(strings.NewReader(string(b)))
	for {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("%s: no <dict> found: %v", file, err)
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "dict" {
			v := readPlistValue(t, dec, se, file)
			m, ok := v.(map[string]any)
			if !ok {
				t.Fatalf("%s: top level is not a dict", file)
			}
			return m
		}
	}
}

func readPlistValue(t *testing.T, dec *xml.Decoder, start xml.StartElement, file string) any {
	t.Helper()
	switch start.Name.Local {
	case "true":
		_ = dec.Skip()
		return true
	case "false":
		_ = dec.Skip()
		return false
	case "string", "integer", "real", "key":
		var s string
		if err := dec.DecodeElement(&s, &start); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		return s
	case "array":
		out := []any{}
		for {
			tok, err := dec.Token()
			if err != nil {
				t.Fatalf("%s: array: %v", file, err)
			}
			switch e := tok.(type) {
			case xml.StartElement:
				out = append(out, readPlistValue(t, dec, e, file))
			case xml.EndElement:
				return out
			}
		}
	case "dict":
		out := map[string]any{}
		key := ""
		for {
			tok, err := dec.Token()
			if err != nil {
				t.Fatalf("%s: dict: %v", file, err)
			}
			switch e := tok.(type) {
			case xml.StartElement:
				v := readPlistValue(t, dec, e, file)
				if e.Name.Local == "key" {
					key = v.(string)
					continue
				}
				out[key] = v
			case xml.EndElement:
				return out
			}
		}
	}
	t.Fatalf("%s: unsupported plist element <%s>", file, start.Name.Local)
	return nil
}

func plistString(t *testing.T, m map[string]any, key string) string {
	t.Helper()
	s, ok := m[key].(string)
	if !ok || s == "" {
		t.Fatalf("%s is missing or not a string: %v", key, m[key])
	}
	return s
}

var plistSeedShape = regexp.MustCompile(`S[ACNOPUX][A-Z2-7]{56}`)

func readFileText(t *testing.T, file string) string {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("service file missing: %s: %v", file, err)
	}
	return string(b)
}

func TestPlistProgramIsAbsoluteAndNotShim(t *testing.T) {
	m := parsePlist(t, hubPlistFile)
	args, ok := m["ProgramArguments"].([]any)
	if !ok || len(args) == 0 {
		t.Fatalf("ProgramArguments is missing or empty: %v", m["ProgramArguments"])
	}
	prog, ok := args[0].(string)
	if !ok || !path.IsAbs(prog) {
		t.Fatalf("ProgramArguments[0] must be an absolute path, got %q", args[0])
	}
	if path.Base(prog) != "nats-server" {
		t.Errorf("ProgramArguments[0] must be the nats-server binary, got %q", prog)
	}
	for _, el := range strings.Split(prog, "/") {
		l := strings.ToLower(el)
		if strings.Contains(l, "shim") || l == ".mise" || l == "mise" || l == ".asdf" || l == "asdf" {
			t.Errorf("ProgramArguments[0] is under a shim or version-manager directory (%q): %s", el, prog)
		}
	}
	if _, set := m["Program"]; set && m["Program"] != prog {
		t.Errorf("Program and ProgramArguments[0] disagree: %v vs %s", m["Program"], prog)
	}
}

func TestPlistKeepAliveIsUnconditional(t *testing.T) {
	m := parsePlist(t, hubPlistFile)
	v, ok := m["KeepAlive"]
	if !ok {
		t.Fatalf("KeepAlive is missing")
	}
	b, isBool := v.(bool)
	if !isBool {
		t.Fatalf("KeepAlive must be the plain boolean true, so a clean exit 0 is relaunched; got %T %v", v, v)
	}
	if !b {
		t.Fatalf("KeepAlive is false")
	}
}

func TestPlistCarriesNoSeed(t *testing.T) {
	deny := regexp.MustCompile(`(?i)seed|password|passwd|secret|token|nkey|cred|key`)
	for _, f := range []string{hubPlistFile, rollbackPlistFile} {
		m := parsePlist(t, f)
		if env, ok := m["EnvironmentVariables"].(map[string]any); ok {
			for k := range env {
				if deny.MatchString(k) {
					t.Errorf("%s: EnvironmentVariables carries a secret-shaped name %q", f, k)
				}
			}
		}
		if plistSeedShape.MatchString(readFileText(t, f)) {
			t.Errorf("%s: contains an nkey seed shaped value", f)
		}
	}
}

// The hub's conf and keys sit under the user's home (design KN-3), so its
// working directory and log paths are home paths. The file carries the
// placeholder below in place of the home; the cutover script substitutes it at
// install, so no real account name is ever written in a tracked file.
const hubHomePlaceholder = "__HUB_HOME__"

func TestPlistHasWorkingDirectoryAndLogPaths(t *testing.T) {
	m := parsePlist(t, hubPlistFile)
	for _, k := range []string{"WorkingDirectory", "StandardOutPath", "StandardErrorPath"} {
		p := plistString(t, m, k)
		if !strings.HasPrefix(p, hubHomePlaceholder+"/") {
			t.Errorf("%s must start with the placeholder %s/ (home paths are filled in at install), got %q", k, hubHomePlaceholder, p)
		}
		if !path.IsAbs(strings.Replace(p, hubHomePlaceholder, "/h", 1)) {
			t.Errorf("%s is not absolute once the placeholder is replaced: %q", k, p)
		}
	}
}

func TestPlistWritesNoLiteralAccountHome(t *testing.T) {
	literal := regexp.MustCompile(`/(Users|home)/[^/<\s]+`)
	for _, f := range []string{hubPlistFile, rollbackPlistFile} {
		if m := literal.FindString(readFileText(t, f)); m != "" {
			t.Errorf("%s: names a literal account home %q; use %s", f, m, hubHomePlaceholder)
		}
	}
	// The placeholder itself is allowed, and it is not a literal home.
	if literal.MatchString(hubHomePlaceholder + "/logs") {
		t.Errorf("the placeholder must not look like a literal account home")
	}
}

func TestPlistNamesNoHomeOrSeat(t *testing.T) {
	for _, f := range []string{hubPlistFile, rollbackPlistFile} {
		text := readFileText(t, f)
		for _, bad := range []string{"/Users/", "/home/", "~/", "$HOME", "/var/root"} {
			if strings.Contains(text, bad) {
				t.Errorf("%s: names a home path (%q)", f, bad)
			}
		}
	}
}

func TestHubPlistLabelAndStart(t *testing.T) {
	m := parsePlist(t, hubPlistFile)
	if got := plistString(t, m, "Label"); got != "com.arcaven.nats-global" {
		t.Errorf("Label = %q, want com.arcaven.nats-global", got)
	}
	if m["RunAtLoad"] != true {
		t.Errorf("RunAtLoad must be true, got %v", m["RunAtLoad"])
	}
	if filepath.Base(hubPlistFile) != plistString(t, m, "Label")+".plist" {
		t.Errorf("file name must be <Label>.plist")
	}
}

func TestRollbackPlistIsTheOldLocalBrokerFile(t *testing.T) {
	m := parsePlist(t, rollbackPlistFile)
	if got := plistString(t, m, "Label"); got != "com.arcaven.nats-local" {
		t.Errorf("Label = %q, want com.arcaven.nats-local", got)
	}
	if m["RunAtLoad"] != true {
		t.Errorf("RunAtLoad must be true (the old rule, kept as it was), got %v", m["RunAtLoad"])
	}
	ka, ok := m["KeepAlive"].(map[string]any)
	if !ok || ka["SuccessfulExit"] != false {
		t.Errorf("the rollback copy keeps the old rule KeepAlive SuccessfulExit=false, got %v", m["KeepAlive"])
	}
	if filepath.Base(rollbackPlistFile) != plistString(t, m, "Label")+".plist" {
		t.Errorf("file name must be <Label>.plist")
	}
	hub := parsePlist(t, hubPlistFile)
	if hub["Label"] == m["Label"] {
		t.Errorf("the hub file and the rollback copy must have different labels")
	}
}

func TestPlistFilesAreWellFormedPlists(t *testing.T) {
	for _, f := range []string{hubPlistFile, rollbackPlistFile} {
		text := readFileText(t, f)
		if !strings.Contains(text, "<!DOCTYPE plist") || !strings.Contains(text, `<plist version="1.0">`) {
			t.Errorf("%s: missing the plist doctype or version header", f)
		}
		dec := xml.NewDecoder(strings.NewReader(text))
		for {
			_, err := dec.Token()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Errorf("%s: not well-formed XML: %v", f, err)
				break
			}
		}
	}
}
