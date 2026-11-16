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

const hubPlistFile = "../../deploy/launchd/com.arcaven.nats-global.plist"

// The binary is pinned per host at install: a version in a tracked file would
// choose it for the fleet (design O3), so the file carries a placeholder the
// cutover fills in with the absolute directory of the pinned nats-server.
// start.sh runs nats-server by name from PATH, so the pin is the first PATH
// directory; the program itself is the checked-in start script, which writes
// the pid file the hub-user commands read before they send SIGHUP.
const (
	natsServerBinDirPlaceholder = "__NATS_SERVER_BIN_DIR__"
	checkoutPlaceholder         = "__DIRECTOR_CHECKOUT__"
	startScriptRel              = "probe/nats-global-tier/hub/start.sh"
	startScriptInRepo           = "../../" + startScriptRel
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

func hubProgramArgs(t *testing.T, m map[string]any) []string {
	t.Helper()
	raw, ok := m["ProgramArguments"].([]any)
	if !ok || len(raw) == 0 {
		t.Fatalf("ProgramArguments is missing or empty: %v", m["ProgramArguments"])
	}
	out := make([]string, len(raw))
	for i, v := range raw {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("ProgramArguments[%d] is not a string: %v", i, v)
		}
		out[i] = s
	}
	return out
}

func hubPathDirs(t *testing.T, m map[string]any) []string {
	t.Helper()
	env, ok := m["EnvironmentVariables"].(map[string]any)
	if !ok {
		t.Fatalf("EnvironmentVariables is missing")
	}
	p, ok := env["PATH"].(string)
	if !ok || p == "" {
		t.Fatalf("EnvironmentVariables.PATH is missing")
	}
	return strings.Split(p, ":")
}

// The hub must start through the checked-in start script: it writes the pid
// file and starts the provisioning that hub-user add and remove rely on.
func TestPlistProgramIsTheStartScript(t *testing.T) {
	m := parsePlist(t, hubPlistFile)
	args := hubProgramArgs(t, m)
	want := checkoutPlaceholder + "/" + startScriptRel
	if args[0] != want {
		t.Errorf("ProgramArguments[0] must be the start script %q, got %q", want, args[0])
	}
	if len(args) != 1 {
		t.Errorf("the start script takes no arguments, got %v", args[1:])
	}
	if _, set := m["Program"]; set && m["Program"] != args[0] {
		t.Errorf("Program and ProgramArguments[0] disagree: %v vs %s", m["Program"], args[0])
	}
}

func TestStartScriptThePlistRunsExistsAndExecsTheServer(t *testing.T) {
	text := readFileText(t, startScriptInRepo)
	for _, want := range []string{"exec nats-server", "--pid", "--log"} {
		if !strings.Contains(text, want) {
			t.Errorf("%s no longer contains %q, which the service file relies on", startScriptRel, want)
		}
	}
}

func TestPlistPathPinsTheBinaryAndHasNoShim(t *testing.T) {
	m := parsePlist(t, hubPlistFile)
	dirs := hubPathDirs(t, m)
	if dirs[0] != natsServerBinDirPlaceholder {
		t.Errorf("the first PATH directory must be the placeholder %s (the install pins the binary), got %q", natsServerBinDirPlaceholder, dirs[0])
	}
	for _, d := range dirs {
		if d != natsServerBinDirPlaceholder && !path.IsAbs(d) {
			t.Errorf("PATH directory %q must be absolute or the placeholder", d)
		}
		l := strings.ToLower(d)
		if strings.Contains(l, "shim") || strings.Contains(l, "mise") || strings.Contains(l, "asdf") {
			t.Errorf("PATH directory %q is a shim or version-manager directory", d)
		}
		if regexp.MustCompile(`\d+\.\d+\.\d+`).MatchString(d) {
			t.Errorf("PATH directory %q carries a version", d)
		}
	}
}

// Below the home placeholder the file names only what the checked-in start
// script already names.
func TestPlistPathsBelowTheHomePlaceholderAreOnesMainCarries(t *testing.T) {
	allowed := map[string]bool{
		hubHomePlaceholder + "/.director/nats-global":                     true,
		hubHomePlaceholder + "/.director/nats-global/log/nats-server.log": true,
	}
	m := parsePlist(t, hubPlistFile)
	for _, k := range []string{"WorkingDirectory", "StandardOutPath", "StandardErrorPath"} {
		p := plistString(t, m, k)
		if !allowed[p] {
			t.Errorf("%s = %q names a path the start script does not carry", k, p)
		}
	}
	text := readFileText(t, hubPlistFile)
	for _, line := range strings.Split(text, "\n") {
		if i := strings.Index(line, hubHomePlaceholder+"/"); i >= 0 {
			rest := line[i:]
			if j := strings.IndexAny(rest, "<\" "); j >= 0 {
				rest = rest[:j]
			}
			if !allowed[rest] {
				t.Errorf("a path below %s is not one main carries: %q", hubHomePlaceholder, rest)
			}
		}
	}
	start := readFileText(t, startScriptInRepo)
	for _, frag := range []string{"$HOME/.director/nats-global", "log/nats-server.log"} {
		if !strings.Contains(start, frag) {
			t.Errorf("%s no longer carries %q", startScriptRel, frag)
		}
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
	for _, f := range []string{hubPlistFile} {
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
	for _, f := range []string{hubPlistFile} {
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
	for _, f := range []string{hubPlistFile} {
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

func TestPlistFilesAreWellFormedPlists(t *testing.T) {
	for _, f := range []string{hubPlistFile} {
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
