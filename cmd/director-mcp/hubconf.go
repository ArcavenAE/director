package main

// The hub conf: its managed region, the variables it references, the server
// test, class templates and the grant comparison used by adopt.

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	hubMarkBegin = "# BEGIN director managed users"
	hubMarkEnd   = "# END director managed users"
	hubMinServer = "2.11.0"
)

var (
	hubRefRe      = regexp.MustCompile(`\$([A-Za-z_][A-Za-z0-9_]*)`)
	hubIncludeRe  = regexp.MustCompile(`(?m)^\s*include\s+["']?\.?/?settings\.conf["']?\s*$`)
	hubDefRe      = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*[:=]`)
	hubUsersRe    = regexp.MustCompile(`^\s*users\s*:\s*\[`)
	hubVarRefMsg  = regexp.MustCompile(`variable reference for '([^']+)'`)
	hubServerVers = regexp.MustCompile(`v?(\d+)\.(\d+)\.(\d+)`)
)

// stripConf removes comments and quoted strings from conf text, so a subject
// such as "$JS.API.INFO" is not read as a variable reference.
func stripConf(s string) string {
	var b strings.Builder
	inStr := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inStr != 0:
			if c == '\\' && i+1 < len(s) {
				i++
			} else if c == inStr {
				inStr = 0
			}
		case c == '"' || c == '\'':
			inStr = c
		case c == '#' || (c == '/' && i+1 < len(s) && s[i+1] == '/'):
			for i < len(s) && s[i] != '\n' {
				i++
			}
			b.WriteByte('\n')
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// confReferences lists the variable names the conf text references, sorted.
func confReferences(conf string) []string {
	seen := map[string]bool{}
	for _, m := range hubRefRe.FindAllStringSubmatch(stripConf(conf), -1) {
		seen[m[1]] = true
	}
	return sortedKeys(seen)
}

// settingsDefinitions lists the names settings.conf defines at top level.
func settingsDefinitions(settings string) []string {
	seen := map[string]bool{}
	depth := 0
	for _, line := range strings.Split(stripConf(settings), "\n") {
		if depth == 0 {
			if m := hubDefRe.FindStringSubmatch(line); m != nil {
				seen[m[1]] = true
			}
		}
		depth += strings.Count(line, "{") + strings.Count(line, "[") - strings.Count(line, "}") - strings.Count(line, "]")
	}
	return sortedKeys(seen)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// settingsState is what `settings check` reports: for each referenced name
// whether settings.conf defines it, and each defined name nothing references.
type settingsState struct {
	Defined, Undefined, Unreferenced []string
	Included                         bool
	SettingsPresent                  bool
}

func readSettingsState(cfg hubCfg) (settingsState, error) {
	conf, err := os.ReadFile(cfg.conf)
	if err != nil {
		return settingsState{}, fmt.Errorf("cannot read the hub conf: %v", err)
	}
	var st settingsState
	st.Included = hubIncludeRe.Match(conf)
	var defs []string
	if b, err := os.ReadFile(cfg.settings); err == nil {
		st.SettingsPresent = true
		defs = settingsDefinitions(string(b))
	}
	refs := confReferences(string(conf))
	for _, r := range refs {
		if st.Included && contains(defs, r) {
			st.Defined = append(st.Defined, r)
		} else {
			st.Undefined = append(st.Undefined, r)
		}
	}
	for _, d := range defs {
		if !contains(refs, d) {
			st.Unreferenced = append(st.Unreferenced, d)
		}
	}
	return st, nil
}

// natsServer finds nats-server and checks its version.
func natsServer() (path, version string, err error) {
	path, err = exec.LookPath("nats-server")
	if err != nil {
		return "", "", fmt.Errorf("nats-server not found on PATH; this command needs nats-server %s or newer to test the hub conf. Install it from https://github.com/nats-io/nats-server/releases or your package manager, then run again. Nothing was changed", hubMinServer)
	}
	out, err := exec.Command(path, "--version").CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("%s --version failed: %v. Nothing was changed", path, err)
	}
	m := hubServerVers.FindStringSubmatch(string(out))
	if m == nil {
		return "", "", fmt.Errorf("cannot read a version from %q. Nothing was changed", strings.TrimSpace(string(out)))
	}
	version = m[1] + "." + m[2] + "." + m[3]
	if versionLess(version, hubMinServer) {
		return "", "", fmt.Errorf("nats-server %s is older than the %s this command needs; install a newer one. Nothing was changed", version, hubMinServer)
	}
	return path, version, nil
}

func versionLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x < y
		}
	}
	return false
}

// serverTest runs `nats-server -t -c conf` and returns its output and whether
// the conf is valid. Relative includes resolve against the conf's own directory,
// so a candidate is tested beside the live conf.
func serverTest(bin, conf string) (string, bool) {
	out, err := exec.Command(bin, "-t", "-c", conf).CombinedOutput()
	return strings.TrimSpace(string(out)), err == nil
}

// unchangedConfControl runs the server test on the live conf exactly as it
// stands, before anything is changed. A conf that fails here cannot be judged by
// a check run from this shell, so the command stops and says why.
func unchangedConfControl(cfg hubCfg, bin string) error {
	st, err := readSettingsState(cfg)
	if err != nil {
		return err
	}
	if len(st.Undefined) > 0 || len(st.Unreferenced) > 0 {
		return fmt.Errorf("the check cannot see the hub's own configuration: %s. Run `director hub settings check`, fix settings.conf, then run again. Nothing was changed", settingsProblems(st))
	}
	if out, ok := serverTest(bin, cfg.conf); !ok {
		why := out
		if m := hubVarRefMsg.FindStringSubmatch(out); m != nil {
			why = fmt.Sprintf("it references %s, which this shell does not define (%s)", m[1], out)
		}
		return fmt.Errorf("the check cannot see the hub's own configuration: nats-server -t fails on the conf as it stands: %s. Nothing was changed", why)
	}
	return nil
}

func settingsProblems(st settingsState) string {
	var parts []string
	if len(st.Undefined) > 0 {
		parts = append(parts, "the conf references "+strings.Join(st.Undefined, ", ")+" and settings.conf does not define "+pluralIt(st.Undefined)+" (or the conf does not include settings.conf)")
	}
	if len(st.Unreferenced) > 0 {
		parts = append(parts, "settings.conf defines "+strings.Join(st.Unreferenced, ", ")+", which the conf never references, and nats-server rejects that as an unknown field")
	}
	return strings.Join(parts, "; ")
}

func pluralIt(l []string) string {
	if len(l) == 1 {
		return "it"
	}
	return "them"
}

// confRegion returns the byte offsets of the managed region's content: from
// the start of the line after the BEGIN marker to the start of the END marker's
// line. ok is false when either marker is missing.
func confRegion(conf string) (start, end int, ok bool) {
	b := strings.Index(conf, hubMarkBegin)
	if b < 0 {
		return 0, 0, false
	}
	nl := strings.IndexByte(conf[b:], '\n')
	if nl < 0 {
		return 0, 0, false
	}
	start = b + nl + 1
	e := strings.Index(conf[start:], hubMarkEnd)
	if e < 0 {
		return 0, 0, false
	}
	end = start + strings.LastIndexByte(conf[start:start+e], '\n') + 1
	if end < start {
		end = start
	}
	return start, end, true
}

// classTemplate reads a class's template and returns its placeholders other
// than {{key}}, in sorted order.
func classTemplate(class string) (text string, params []string, err error) {
	b, err := fs.ReadFile(hubClasses(), class+".conf")
	if err != nil {
		return "", nil, fmt.Errorf("unknown class %q (known: %s)", class, strings.Join(classNames(), ", "))
	}
	// The parameters a class takes are declared by the embedded template, so a
	// template swapped in by a test (or later by an operator) cannot widen or
	// narrow what the command accepts; a placeholder left unfilled is refused.
	paramsOf := func(text string) map[string]bool {
		seen := map[string]bool{}
		for _, m := range placeholderRe.FindAllStringSubmatch(text, -1) {
			if m[1] != "key" {
				seen[m[1]] = true
			}
		}
		return seen
	}
	seen := paramsOf(string(b))
	if sub, err := fs.Sub(hubClassesEmbedded, "hubclasses"); err == nil {
		if eb, err := fs.ReadFile(sub, class+".conf"); err == nil {
			seen = paramsOf(string(eb))
		}
	}
	return string(b), sortedKeys(seen), nil
}

var placeholderRe = regexp.MustCompile(`\{\{([A-Za-z0-9_]+)\}\}`)

func classNames() []string {
	ents, err := fs.ReadDir(hubClasses(), ".")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if n := strings.TrimSuffix(e.Name(), ".conf"); n != e.Name() {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// renderClass fills a class template with the key and the named parameters.
// Every parameter the template names must be given and no other is accepted.
func renderClass(class, key string, params map[string]string) (string, error) {
	text, want, err := classTemplate(class)
	if err != nil {
		return "", err
	}
	for _, w := range want {
		if _, ok := params[w]; !ok {
			return "", fmt.Errorf("class %s needs --param %s=VALUE", class, w)
		}
	}
	for k := range params {
		if !contains(want, k) {
			if len(want) == 0 {
				return "", fmt.Errorf("class %s takes no parameters (got %s)", class, k)
			}
			return "", fmt.Errorf("class %s has no parameter %s (it takes: %s)", class, k, strings.Join(want, ", "))
		}
	}
	text = strings.ReplaceAll(text, "{{key}}", key)
	for k, v := range params {
		text = strings.ReplaceAll(text, "{{"+k+"}}", v)
	}
	if m := placeholderRe.FindStringSubmatch(text); m != nil {
		return "", fmt.Errorf("class %s: the template has a placeholder {{%s}} that no parameter fills", class, m[1])
	}
	return text, nil
}

// indentBlock indents each non-empty line by six spaces, the depth of an entry
// in the account's users list.
func indentBlock(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString("      " + line + "\n")
	}
	return b.String()
}

func utcStamp() string { return time.Now().UTC().Format("20060102T150405.000000000Z") }

// writeFileExcl creates path with mode, failing if it exists, and never follows
// the umask: the mode is set on the open descriptor.
func writeFileExcl(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		_ = os.Remove(path)
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = os.Remove(path)
		return err
	}
	return f.Close()
}

// writeFileAtomic writes data to a temp file beside path and renames it over.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := fmt.Sprintf("%s.tmp-%d", path, os.Getpid())
	_ = os.Remove(tmp)
	if err := writeFileExcl(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// backupConf copies the live conf to a timestamped file beside it and returns
// the path. It never overwrites an existing backup.
func backupConf(cfg hubCfg) (string, error) {
	data, err := os.ReadFile(cfg.conf)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(cfg.conf)
	if err != nil {
		return "", err
	}
	base := cfg.conf + ".bak-" + utcStamp()
	for i := 0; i < 100; i++ {
		p := base
		if i > 0 {
			p = fmt.Sprintf("%s-%d", base, i)
		}
		if err := writeFileExcl(p, data, st.Mode().Perm()); err == nil {
			return p, nil
		} else if !os.IsExist(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("cannot find a free backup name beside %s", cfg.conf)
}

// --- grant comparison for adopt ---------------------------------------------------------------

// grantSets reads the quoted subjects of a user block, keyed by their section:
// "publish allow", "publish deny", "subscribe allow", "subscribe deny".
func grantSets(block string) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	var stack []string
	word := func(s string, i int) (string, int) {
		j := i
		for j < len(s) && (s[j] == '_' || s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z' || s[j] >= '0' && s[j] <= '9') {
			j++
		}
		return s[i:j], j
	}
	var section []string
	s := block
	var last string
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '#':
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case c == '"' || c == '\'':
			j := i + 1
			for j < len(s) && s[j] != c {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			if j > len(s) {
				j = len(s)
			}
			key := strings.Join(section, " ")
			if key != "" {
				if out[key] == nil {
					out[key] = map[string]bool{}
				}
				out[key][s[i+1:j]] = true
			}
			i = j
		case c == '{' || c == '[':
			stack = append(stack, last)
			if last == "publish" || last == "subscribe" || last == "allow" || last == "deny" {
				section = append(section, last)
			}
			last = ""
		case c == '}' || c == ']':
			if n := len(stack); n > 0 {
				top := stack[n-1]
				stack = stack[:n-1]
				if (top == "publish" || top == "subscribe" || top == "allow" || top == "deny") && len(section) > 0 {
					section = section[:len(section)-1]
				}
			}
			last = ""
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			w, j := word(s, i)
			last = w
			i = j - 1
		}
	}
	return out
}

// grantDifferences compares a hand-written block with a rendered class template
// and returns one line per subject that is in only one of them.
func grantDifferences(hand, tmpl string) []string {
	a, b := grantSets(hand), grantSets(tmpl)
	secs := map[string]bool{}
	for k := range a {
		secs[k] = true
	}
	for k := range b {
		secs[k] = true
	}
	var out []string
	for _, sec := range sortedKeys(secs) {
		for _, s := range sortedKeys(a[sec]) {
			if !b[sec][s] {
				out = append(out, fmt.Sprintf("grant difference: %s: %q is in the conf block and not in the class template", sec, s))
			}
		}
		for _, s := range sortedKeys(b[sec]) {
			if !a[sec][s] {
				out = append(out, fmt.Sprintf("grant difference: %s: %q is in the class template and not in the conf block", sec, s))
			}
		}
	}
	return out
}

// entryBlock finds the user entry that carries nkey in conf: the text from the
// opening brace before `nkey: KEY` to its matching close.
func entryBlock(conf, key string) (string, bool) {
	re := regexp.MustCompile(`nkey\s*:\s*` + regexp.QuoteMeta(key) + `\b`)
	loc := re.FindStringIndex(conf)
	if loc == nil {
		return "", false
	}
	// A commented line is not an entry.
	ls := strings.LastIndexByte(conf[:loc[0]], '\n') + 1
	if strings.HasPrefix(strings.TrimSpace(conf[ls:loc[0]]), "#") {
		return "", false
	}
	open := strings.LastIndexByte(conf[:loc[0]], '{')
	if open < 0 {
		return "", false
	}
	depth := 0
	inStr := byte(0)
	for i := open; i < len(conf); i++ {
		c := conf[i]
		switch {
		case inStr != 0:
			if c == '\\' {
				i++
			} else if c == inStr {
				inStr = 0
			}
		case c == '"' || c == '\'':
			inStr = c
		case c == '#':
			for i < len(conf) && conf[i] != '\n' {
				i++
			}
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return conf[open : i+1], true
			}
		}
	}
	return "", false
}

var _ = bytes.NewBuffer
var _ = filepath.Join
