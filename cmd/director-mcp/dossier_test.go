package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// The dossier (P1): a reconstruction of what a predecessor left open, read
// from the bus and the state directory and never written back to either.

const bodySentinel = "SENTINEL-BODY-7f3a91"

func dossierScratch(t *testing.T) (state string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MARVEL_SOCKET", "")
	state = filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	return state
}

func baseOpts(state string) dossierOpts {
	return dossierOpts{
		Role: "ops/builder", Predecessor: "builder-old", PredSource: "flag",
		State: state, Successor: "builder-new", DWS: "/nonexistent/dws", Now: time.Now(),
	}
}

func auditAt(t *testing.T, ctx context.Context, js jetstream.JetStream, at time.Time, id, perf, from, to, data string, opts ...envOpt) {
	t.Helper()
	r := rec(at, id, perf, from, to, data, opts...)
	auditPublish(t, ctx, js, r.Env, false)
}

func runOnce(t *testing.T, ctx context.Context, url string, o dossierOpts) (stdout, stderr string, code int) {
	t.Helper()
	var so, se bytes.Buffer
	code = runDossier(ctx, url, o, &so, &se)
	return so.String(), se.String(), code
}

func dossierFiles(t *testing.T, state, successor string) (md, js string) {
	t.Helper()
	dir := filepath.Join(state, "dossier", successor)
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("no dossier dir: %v", err)
	}
	for _, e := range ents {
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		switch {
		case strings.HasSuffix(e.Name(), ".md"):
			md = string(b)
		case strings.HasSuffix(e.Name(), ".json"):
			js = string(b)
		}
	}
	return md, js
}

// section returns the text of one numbered section of the markdown.
func section(md string, n int) string {
	head := fmt.Sprintf("\n## %d. ", n)
	i := strings.Index(md, head)
	if i < 0 {
		return ""
	}
	rest := md[i+1:]
	if j := strings.Index(rest[3:], "\n## "); j >= 0 {
		return rest[:j+3]
	}
	return rest
}

// T1: the kill. A sent with no reply, B received with no reply, C sent and answered.
func TestDossierT1KillListsTheRightAsks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	state := dossierScratch(t)
	url := startScratchServer(t)
	_, js := provisionAudit(t, ctx, url)
	now := time.Now()
	old := "agent://ops/builder-old"
	auditAt(t, ctx, js, now.Add(-3*time.Hour), "ask-A", "REQUEST", "builder-old", "agent://ops/builder-x", "do A")
	auditAt(t, ctx, js, now.Add(-2*time.Hour), "ask-B", "REQUEST", "builder-x", old, "do B")
	auditAt(t, ctx, js, now.Add(-2*time.Hour), "ask-C", "REQUEST", "builder-old", "agent://ops/builder-x", "do C")
	auditAt(t, ctx, js, now.Add(-1*time.Hour), "ans-C", "INFORM", "builder-x", old, "done", inReplyTo("ask-C"))
	out, _, code := runOnce(t, ctx, url, baseOpts(state))
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	md, _ := dossierFiles(t, state, "builder-new")
	s4, s5 := section(md, 4), section(md, 5)
	if !strings.Contains(s4, "ask-A") || strings.Contains(s4, "ask-B") || strings.Contains(s4, "ask-C") {
		t.Errorf("section 4 = %q", s4)
	}
	if !strings.Contains(s5, "ask-B") || strings.Contains(s5, "ask-A") || strings.Contains(s5, "ask-C") {
		t.Errorf("section 5 = %q", s5)
	}
	if out != md {
		t.Errorf("stdout differs from the .md file")
	}
}

// T2: no envelope body reaches any output.
func TestDossierT2NoBody(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	state := dossierScratch(t)
	url := startScratchServer(t)
	_, js := provisionAudit(t, ctx, url)
	now := time.Now()
	auditAt(t, ctx, js, now.Add(-time.Hour), "ask-A", "REQUEST", "builder-old", "agent://ops/builder-x", bodySentinel, refs(bodySentinel))
	auditAt(t, ctx, js, now.Add(-time.Hour), "mail-1", "INFORM", "builder-x", "agent://ops/builder-old", bodySentinel)
	for _, asJSON := range []bool{false, true} {
		o := baseOpts(state)
		o.JSON = asJSON
		out, errs, _ := runOnce(t, ctx, url, o)
		md, js := dossierFiles(t, state, "builder-new")
		// positive control: the pointers are there, so an empty output cannot pass
		if !strings.Contains(section(md, 4), "ask-A") || !strings.Contains(section(md, 6), "mail-1") || !strings.Contains(js, "ask-A") {
			t.Errorf("json=%v: the ask and the mail are not listed as pointers:\n%s", asJSON, md)
		}
		for name, text := range map[string]string{"stdout": out, "stderr": errs, "md": md, "json": js} {
			if strings.Contains(text, bodySentinel) {
				t.Errorf("json=%v: %s carries a body", asJSON, name)
			}
		}
	}
}

// T7: the dossier creates no consumer and moves no ack floor.
func TestDossierT7AckFloorUntouched(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	state := dossierScratch(t)
	url := startScratchServer(t)
	_, js := provisionAudit(t, ctx, url)
	s, _ := js.Stream(ctx, "AGENT_INBOX")
	if _, err := s.CreateConsumer(ctx, jetstream.ConsumerConfig{Durable: "mcp_builder-old_01ABC", AckPolicy: jetstream.AckExplicitPolicy, FilterSubject: "agent.ops.*.builder-old.inbox"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		pubEnv(t, ctx, js, "agent.ops.w.builder-old.inbox", fmt.Sprintf("m%d", i), "INFORM", "x")
	}
	snap := func() string {
		var parts []string
		l := s.ListConsumers(ctx)
		for ci := range l.Info() {
			parts = append(parts, fmt.Sprintf("%s pend=%d ackpend=%d floor=%d", ci.Name, ci.NumPending, ci.NumAckPending, ci.AckFloor.Stream))
		}
		sort.Strings(parts)
		return strings.Join(parts, ";") + " " + streamState(t, ctx, js, "AGENT_INBOX") + " " + streamState(t, ctx, js, "AGENT_AUDIT")
	}
	before := snap()
	out, _, code := runOnce(t, ctx, url, baseOpts(state))
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if after := snap(); after != before {
		t.Errorf("bus state moved:\nbefore %s\nafter  %s", before, after)
	}
	if !strings.Contains(section(out, 8), "mcp_builder-old_01ABC pending=3") {
		t.Errorf("section 8 does not show the 3 pending: %q", section(out, 8))
	}
}

func hashTree(t *testing.T, root, skip string) string {
	t.Helper()
	h := sha256.New()
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && p == skip {
			return filepath.SkipDir
		}
		fi, _ := os.Lstat(p)
		if fi == nil || fi.IsDir() { // a directory's size and mtime move with its entries
			fmt.Fprintf(h, "%s dir\n", p)
			return nil
		}
		b := []byte{}
		if fi.Mode().IsRegular() {
			b, _ = os.ReadFile(p)
		}
		fmt.Fprintf(h, "%s %v %d %x\n", p, fi.Mode(), fi.Size(), sha256.Sum256(b))
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))
}

// T13: nothing outside dossier/ changes, in the state directory or the home.
func TestDossierT13NothingElseWritten(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	state := dossierScratch(t)
	home := os.Getenv("HOME")
	for _, f := range []string{"handled.jsonl", "rulings.jsonl", "workstreams.jsonl", "thresholds.json"} {
		body := "{}\n"
		if f == "handled.jsonl" {
			body = `{"at":"2026-10-20T00:00:00Z","tier":"local","stream":"AGENT_INBOX","sequence":4,"message_id":"p1","disposition":"parked","reason":"later"}` + "\n"
		}
		_ = os.WriteFile(filepath.Join(state, f), []byte(body), 0o600)
	}
	url := startScratchServer(t)
	_, js := provisionAudit(t, ctx, url)
	auditAt(t, ctx, js, time.Now().Add(-time.Hour), "ask-A", "REQUEST", "builder-old", "agent://ops/builder-x", "x")
	o := baseOpts(state)
	o.PredState = state
	before, beforeHome := hashTree(t, state, filepath.Join(state, "dossier")), hashTree(t, home, "")
	if _, _, code := runOnce(t, ctx, url, o); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if hashTree(t, state, filepath.Join(state, "dossier")) != before {
		t.Errorf("a file outside dossier/ changed")
	}
	if hashTree(t, home, "") != beforeHome {
		t.Errorf("the home changed")
	}
	md, _ := dossierFiles(t, state, "builder-new")
	if !strings.Contains(section(md, 4), "ask-A") || !strings.Contains(section(md, 7), "p1") {
		t.Errorf("the dossier does not list the ask and the parked item:\n%s", md)
	}
}

func fakeDWS(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "dws")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

func scratchBus(t *testing.T) (context.Context, string, jetstream.JetStream) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	url := startScratchServer(t)
	_, js := provisionAudit(t, ctx, url)
	return ctx, url, js
}

// T3: a REQUEST older than the window is absent and counted as outside it.
func TestDossierT3Window(t *testing.T) {
	state := dossierScratch(t)
	ctx, url, js := scratchBus(t)
	now := time.Now()
	auditAt(t, ctx, js, now.Add(-10*24*time.Hour), "ask-old", "REQUEST", "builder-old", "agent://ops/x", "a")
	auditAt(t, ctx, js, now.Add(-time.Hour), "ask-new", "REQUEST", "builder-old", "agent://ops/x", "b")
	o := baseOpts(state)
	// The scratch stream stamps messages with the real clock, so a window that
	// starts after the first publish and before the second leaves one outside.
	time.Sleep(1100 * time.Millisecond)
	auditAt(t, ctx, js, now, "ask-late", "REQUEST", "builder-old", "agent://ops/x", "c")
	o.Now = time.Now()
	if err := os.WriteFile(filepath.Join(state, "thresholds.json"), []byte(`{"dossier_window":"1s"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, _ := runOnce(t, ctx, url, o)
	if strings.Contains(out, "ask-old") || strings.Contains(out, "ask-new") || !strings.Contains(out, "ask-late") {
		t.Errorf("window not applied:\n%s", out)
	}
	if strings.Contains(out, "0 sequences before it") {
		t.Errorf("header does not count the ones outside:\n%s", out)
	}
}

// T4: where the predecessor id came from, and no guess when there is none.
func TestDossierT4Lineage(t *testing.T) {
	state := dossierScratch(t)
	ctx, url, js := scratchBus(t)
	now := time.Now()
	auditAt(t, ctx, js, now.Add(-time.Hour), "a1", "REQUEST", "ops-builder-1", "agent://ops/x", "a", senderRole("builder"))
	auditAt(t, ctx, js, now.Add(-time.Hour), "a2", "REQUEST", "ops-builder-2", "agent://ops/x", "b", senderRole("builder"))
	t.Setenv("DIRECTOR_STATE", state)
	t.Setenv("DIRECTOR_AGENT_ID", "builder-new")
	t.Setenv("DIRECTOR_DWS", "/nonexistent/dws")
	run := func(args ...string) string {
		cli, err := parseArgs(append([]string{"dossier", "--role", "ops/builder"}, args...))
		if err != nil {
			t.Fatal(err)
		}
		var so, se bytes.Buffer
		if code := runDossierCmd(ctx, url, cli, &so, &se); code != 0 {
			t.Fatalf("exit %d", code)
		}
		return so.String()
	}
	t.Setenv("MARVEL_PREDECESSOR", "")
	if out := run("--predecessor", "ops-builder-1"); !strings.Contains(out, "predecessor: ops-builder-1 (from flag)") {
		t.Errorf("flag: %s", out)
	}
	t.Setenv("MARVEL_PREDECESSOR", "ops-builder-2")
	if out := run(); !strings.Contains(out, "(from env MARVEL_PREDECESSOR)") {
		t.Errorf("env: %s", out)
	}
	t.Setenv("MARVEL_PREDECESSOR", "")
	out := run()
	for _, want := range []string{"unknown", "candidate instance: ops-builder-1", "candidate instance: ops-builder-2"} {
		if !strings.Contains(out, want) {
			t.Errorf("neither: missing %q in\n%s", want, out)
		}
	}
	for n := 4; n <= 8; n++ {
		if s := section(out, n); !strings.Contains(s, "predecessor unknown") {
			t.Errorf("section %d = %q", n, s)
		}
	}
}

// T5: no broker. The files are still written and the exit is 0.
func TestDossierT5BusDown(t *testing.T) {
	state := dossierScratch(t)
	ctx := context.Background()
	out, _, code := runOnce(t, ctx, "nats://127.0.0.1:1", baseOpts(state))
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, n := range []int{4, 5, 6, 8} {
		if s := section(out, n); !strings.Contains(s, "UNREAD") || !strings.Contains(s, "connect") {
			t.Errorf("section %d = %q", n, s)
		}
	}
	if md, _ := dossierFiles(t, state, "builder-new"); md == "" {
		t.Errorf("no file written")
	}
}

// T6: dws absent, failing, or reporting its own UNREAD.
func TestDossierT6DWS(t *testing.T) {
	state := dossierScratch(t)
	ctx, url, _ := scratchBus(t)
	for name, c := range map[string]struct{ dws, want string }{
		"absent":      {"/nonexistent/dws", "UNREAD"},
		"failing":     {fakeDWS(t, "echo boom >&2; exit 1"), "boom"},
		"desk unread": {fakeDWS(t, `echo '{"role":"builder","sources":{"rulings":{"state":"read"},"desk":{"state":"UNREAD","why":"no desk"},"ledger":{"state":"read"}},"terms":[{"id":"T-1","text":"standing"}],"grants":[],"open_asks":null}'`), "no desk"},
	} {
		o := baseOpts(state)
		o.DWS = c.dws
		out, _, _ := runOnce(t, ctx, url, o)
		if name == "desk unread" {
			if !strings.Contains(section(out, 2), "T-1") || !strings.Contains(section(out, 3), c.want) || !strings.Contains(section(out, 3), "UNREAD") {
				t.Errorf("%s:\n%s", name, out)
			}
			continue
		}
		if !strings.Contains(section(out, 2), c.want) || !strings.Contains(section(out, 3), "UNREAD") {
			t.Errorf("%s:\n%s", name, out)
		}
	}
}

// T8: 500 open asks fit under both caps and name what was left out.
func TestDossierT8Caps(t *testing.T) {
	state := dossierScratch(t)
	ctx, url, js := scratchBus(t)
	now := time.Now()
	for i := 0; i < 500; i++ {
		auditAt(t, ctx, js, now.Add(-time.Hour), fmt.Sprintf("ask-%04d", i), "REQUEST", "builder-old", "agent://ops/builder-x", "x")
	}
	runOnce(t, ctx, url, baseOpts(state))
	md, jsn := dossierFiles(t, state, "builder-new")
	if len(md) > 8192 || !strings.Contains(section(md, 4), "more, see the .json") {
		t.Errorf("md %d bytes; section 4 = %q", len(md), section(md, 4))
	}
	if len(jsn) > 65536 {
		t.Errorf("json %d bytes", len(jsn))
	}
}

// T9: modes, first lines, no terminal marker.
func TestDossierT9FileShape(t *testing.T) {
	state := dossierScratch(t)
	ctx, url, _ := scratchBus(t)
	runOnce(t, ctx, url, baseOpts(state))
	dir := filepath.Join(state, "dossier", "builder-new")
	if fi, _ := os.Stat(dir); fi == nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", fi)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 2 {
		t.Fatalf("%d files", len(ents))
	}
	for _, e := range ents {
		fi, _ := e.Info()
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %v", e.Name(), fi.Mode())
		}
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		lines := strings.SplitN(string(b), "\n", 3)
		if !strings.HasPrefix(lines[0], "director-dossier-id: ") || lines[1] != "reconstructed: true" {
			t.Errorf("%s starts %q", e.Name(), lines[:2])
		}
		if strings.Contains(string(b), "HANDOFF-COMPLETE") {
			t.Errorf("%s carries the handoff marker", e.Name())
		}
	}
}

// T10: the rename fails. No final file, no temp left, exit 0.
func TestDossierT10TornWrite(t *testing.T) {
	state := dossierScratch(t)
	ctx, url, _ := scratchBus(t)
	old := dossierRename
	dossierRename = func(a, b string) error { return fmt.Errorf("injected rename failure") }
	defer func() { dossierRename = old }()
	_, errs, code := runOnce(t, ctx, url, baseOpts(state))
	if code != 0 || !strings.Contains(errs, "injected rename failure") {
		t.Errorf("code %d stderr %q", code, errs)
	}
	ents, _ := os.ReadDir(filepath.Join(state, "dossier", "builder-new"))
	if len(ents) != 0 {
		t.Errorf("left behind: %v", ents)
	}
}

// T11: only the run's own old files go.
func TestDossierT11Retention(t *testing.T) {
	state := dossierScratch(t)
	ctx, url, _ := scratchBus(t)
	dir := filepath.Join(state, "dossier", "builder-new")
	_ = os.MkdirAll(dir, 0o700)
	long := time.Now().Add(-30 * 24 * time.Hour)
	put := func(name, body string, mt time.Time) {
		p := filepath.Join(dir, name)
		_ = os.WriteFile(p, []byte(body), 0o600)
		_ = os.Chtimes(p, mt, mt)
	}
	put("own-old.md", "director-dossier-id: aa\nreconstructed: true\n", long)
	put("own-new.md", "director-dossier-id: bb\nreconstructed: true\n", time.Now())
	put("foreign-old.md", "someone else's notes\n", long)
	runOnce(t, ctx, url, baseOpts(state))
	for name, want := range map[string]bool{"own-old.md": false, "own-new.md": true, "foreign-old.md": true} {
		_, err := os.Stat(filepath.Join(dir, name))
		if (err == nil) != want {
			t.Errorf("%s present=%v want %v", name, err == nil, want)
		}
	}
}

// T12: a bad threshold row keeps its default and says so.
func TestDossierT12Thresholds(t *testing.T) {
	state := dossierScratch(t)
	ctx, url, _ := scratchBus(t)
	_ = os.WriteFile(filepath.Join(state, "thresholds.json"), []byte(`{"dossier_window":"-1h"}`), 0o600)
	out, errs, code := runOnce(t, ctx, url, baseOpts(state))
	if code != 0 || !strings.Contains(errs, "dossier_window") || !strings.Contains(out, "notice: threshold dossier_window") || !strings.Contains(out, "window: 168h0m0s") {
		t.Errorf("code %d stderr %q out %s", code, errs, out)
	}
}

// T14: an ask to the role address with no reply is section 5.
func TestDossierT14RoleAddress(t *testing.T) {
	state := dossierScratch(t)
	ctx, url, js := scratchBus(t)
	auditAt(t, ctx, js, time.Now().Add(-time.Hour), "ask-role", "REQUEST", "builder-x", "role://ops/builder", "x")
	out, _, _ := runOnce(t, ctx, url, baseOpts(state))
	if !strings.Contains(section(out, 5), "ask-role") {
		t.Errorf("section 5 = %q", section(out, 5))
	}
}

// T15: no handled.jsonl is "not kept", never UNREAD.
func TestDossierT15HandledNotKept(t *testing.T) {
	state := dossierScratch(t)
	ctx, url, _ := scratchBus(t)
	o := baseOpts(state)
	o.PredState = t.TempDir()
	out, _, _ := runOnce(t, ctx, url, o)
	s := section(out, 7)
	if !strings.Contains(s, "not kept") || strings.Contains(s, "UNREAD") {
		t.Errorf("section 7 = %q", s)
	}
}

func TestParseArgsDossier(t *testing.T) {
	for _, c := range []struct {
		args []string
		bad  bool
	}{
		{[]string{"dossier", "--role", "ops/builder"}, false},
		{[]string{"dossier", "--role=ops/builder", "--pointer", "/a", "--pointer", "/b", "--json"}, false},
		{[]string{"dossier"}, true},
		{[]string{"dossier", "--role", "builder"}, true},
		{[]string{"dossier", "--role", "ops/builder", "--bogus"}, true},
		{[]string{"dossier", "--role", "ops/builder", "--predecessor", "a/b"}, true},
	} {
		_, err := parseArgs(c.args)
		if (err != nil) != c.bad {
			t.Errorf("%v: err %v", c.args, err)
		}
	}
}

// accLine is the acceptance line alone.
func accLine(h handoffSpec) string { l, _ := acceptanceLine(h); return l }

func putHandoff(t *testing.T, dir, name, body string, mod time.Time) string {
	t.Helper()
	_ = os.MkdirAll(dir, 0o700)
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(p, mod, mod)
	return p
}

const (
	hdOK      = "# handoff\nstuff\nHANDOFF-COMPLETE\n\n"
	hdPartial = "# handoff\nstuff\n"
	nonePhr   = "no valid authored handoff; this dossier may back acceptance"
	unreadPhr = "handoff location unreadable; this dossier does not back acceptance"
)

// t6p-1: a dossier backs acceptance only when no valid authored handoff exists.
// The fixtures use the seat layout, <dir>/<agent-instance>-<date>.md, in scratch.
func TestDossierAcceptanceSwitch(t *testing.T) {
	now := time.Now()
	pred := "arcaven-architect-g6-1"
	type f struct {
		name, body string
		age        time.Duration
	}
	for name, c := range map[string]struct {
		files []f
		want  func(dir string) string
	}{
		"valid newest": {[]f{{pred + "-2026-10-10.md", hdOK, 0}}, func(d string) string {
			return "authored handoff present: " + filepath.Join(d, pred+"-2026-10-10.md") + "; this dossier does not back acceptance"
		}},
		"partial newest": {[]f{{pred + "-2026-10-10.md", hdPartial, 0}}, func(string) string { return nonePhr }},
		"older valid, newer partial: the newest decides": {[]f{
			{pred + "-2026-10-09.md", hdOK, 48 * time.Hour}, {pred + "-2026-10-10.md", hdPartial, 0}}, func(string) string { return nonePhr }},
		"older partial, newer valid": {[]f{
			{pred + "-2026-10-09.md", hdPartial, 48 * time.Hour}, {pred + "-2026-10-10.md", hdOK, 0}}, func(d string) string {
			return "authored handoff present: " + filepath.Join(d, pred+"-2026-10-10.md") + "; this dossier does not back acceptance"
		}},
		"marker not last":          {[]f{{pred + "-2026-10-10.md", "HANDOFF-COMPLETE\nmore text\n", 0}}, func(string) string { return nonePhr }},
		"another seat's file only": {[]f{{"arcaven-architect-g6-2-2026-10-10.md", hdOK, 0}, {"arcaven-architect-g6-1-2-2026-10-10.md", hdOK, 0}}, func(string) string { return nonePhr }},
		"no files":                 {nil, func(string) string { return nonePhr }},
	} {
		dir := filepath.Join(t.TempDir(), "handoff")
		_ = os.MkdirAll(dir, 0o700)
		for _, x := range c.files {
			putHandoff(t, dir, x.name, x.body, now.Add(-x.age))
		}
		if got := accLine(handoffSpec{Dir: dir, Pred: pred}); got != c.want(dir) {
			t.Errorf("%s: %q", name, got)
		}
	}
	// a link is refused as unreadable even when its target is valid
	dir := filepath.Join(t.TempDir(), "handoff")
	real := putHandoff(t, t.TempDir(), "real.md", hdOK, now)
	_ = os.MkdirAll(dir, 0o700)
	_ = os.Symlink(real, filepath.Join(dir, pred+"-2026-10-10.md"))
	if got := accLine(handoffSpec{Dir: dir, Pred: pred}); got != unreadPhr {
		t.Errorf("symlink: %q", got)
	}
}

// Could not look: never "may back acceptance".
func TestDossierAcceptanceUnreadableFailsClosed(t *testing.T) {
	pred := "arcaven-architect-g6-1"
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	_ = os.MkdirAll(locked, 0o700)
	putHandoff(t, locked, pred+"-2026-10-10.md", hdOK, time.Now())
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(locked, 0o700)
	notDir := filepath.Join(root, "afile")
	_ = os.WriteFile(notDir, []byte("x"), 0o600)
	for name, dir := range map[string]string{
		"unreadable directory": locked, "missing directory": filepath.Join(root, "nope"),
		"a file, not a directory": notDir, "location unknown": "",
	} {
		if os.Geteuid() == 0 && name == "unreadable directory" {
			continue // root reads a mode 0 directory
		}
		if got := accLine(handoffSpec{Dir: dir, Pred: pred}); got != unreadPhr {
			t.Errorf("%s: %q", name, got)
		}
	}
	if got := accLine(handoffSpec{Dir: filepath.Join(root, "locked")}); got != unreadPhr {
		t.Errorf("predecessor unknown: %q", got)
	}
}

func TestDossierHeaderAndJSONCarryAcceptance(t *testing.T) {
	state := dossierScratch(t)
	ctx, url, _ := scratchBus(t)
	o := baseOpts(state)
	hp := putHandoff(t, filepath.Join(state, "handoff"), "builder-old-2026-10-20.md", hdOK, time.Now())
	o.HandoffDir = filepath.Join(state, "handoff")
	out, _, _ := runOnce(t, ctx, url, o)
	_, js := dossierFiles(t, state, "builder-new")
	for name, text := range map[string]string{"stdout": out, "json": js} {
		if !strings.Contains(text, "authored handoff present: "+hp+"; this dossier does not back acceptance") {
			t.Errorf("%s lacks the acceptance line", name)
		}
	}
	// the dossier itself never carries the marker, whatever the handoff holds
	if strings.Contains(out+js, "HANDOFF-COMPLETE") {
		t.Errorf("the marker leaked into the dossier")
	}
}

// Every list is capped, and a record that cannot fit fails closed.
func TestDossierCapsEveryListAndFailsClosed(t *testing.T) {
	state := dossierScratch(t)
	ctx, url, js := scratchBus(t)
	now := time.Now()
	long := strings.Repeat("x", 150)
	for i := 0; i < 3000; i++ {
		auditAt(t, ctx, js, now.Add(-time.Hour), fmt.Sprintf("m%04d", i), "REQUEST", fmt.Sprintf("ops-builder-%s-%04d", long, i), "agent://ops/x", "b", senderRole("builder"))
	}
	o := baseOpts(state)
	o.Predecessor, o.PredSource = "", "unknown"
	out, _, _ := runOnce(t, ctx, url, o)
	md, jsn := dossierFiles(t, state, "builder-new")
	if len(md) > 8192 || len(out) > 8192 || len(jsn) > 65536 {
		t.Errorf("md %d stdout %d json %d", len(md), len(out), len(jsn))
	}
	if !strings.Contains(md, "more candidates, see the .json") {
		t.Errorf("candidates not capped in the md")
	}
	// fail closed: a record that stays over the cap with every list emptied
	d := &dossier{ID: "n", Role: strings.Repeat("r", 200000), Successor: "s", Acceptance: "a"}
	_, big := renderBoth(d)
	if len(big) > 65536 || !strings.Contains(string(big), `"overflow": true`) {
		t.Errorf("not failed closed: %d bytes", len(big))
	}
}

// The broker URL, and the userinfo it can carry, never reach the output.
func TestDossierRedactsTheBrokerURL(t *testing.T) {
	state := dossierScratch(t)
	ctx := context.Background()
	out, errs, _ := runOnce(t, ctx, "nats://alice:s3cretpw@127.0.0.1:1", baseOpts(state))
	md, jsn := dossierFiles(t, state, "builder-new")
	for name, text := range map[string]string{"stdout": out, "stderr": errs, "md": md, "json": jsn} {
		if strings.Contains(text, "s3cretpw") || strings.Contains(text, "alice") {
			t.Errorf("%s carries the userinfo", name)
		}
	}
	if !strings.Contains(out, "127.0.0.1:1") {
		t.Errorf("host and port missing: %s", out)
	}
}

// With no state directory the dossier still prints, says so, and writes nothing.
func TestDossierNoStateDirPrintsANotice(t *testing.T) {
	dossierScratch(t)
	ctx := context.Background()
	o := baseOpts("")
	out, _, code := runOnce(t, ctx, "nats://127.0.0.1:1", o)
	if code != 0 || !strings.Contains(out, "no state directory") {
		t.Errorf("code %d out %s", code, out)
	}
}

// A bad dossier row never changes how the other rows load.
func TestDossierRowsDoNotAffectTheHardLoader(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "thresholds.json"), []byte(`{"dossier_window":"-1h","dossier_keep":"nonsense","handled_ack_wait":"10m"}`), 0o600)
	got, err := loadThresholds(dir)
	if err != nil {
		t.Fatalf("a dossier row stopped the hard loader: %v", err)
	}
	if got[thresholdHandledAckWait] != 10*time.Minute {
		t.Errorf("handled_ack_wait = %v", got[thresholdHandledAckWait])
	}
	if _, in := got[thresholdDossierWindow]; in {
		t.Errorf("the hard table carries a dossier row")
	}
}

// The manifest's path and marker are taken as declared. The old search finds
// nothing here, so the test is red against a dossier that only searches.
func TestDossierDeclaredHandoffPathAndMarker(t *testing.T) {
	root := t.TempDir()
	declared := putHandoff(t, filepath.Join(root, "builder-old"), "handoff.md", "# h\nwork\nDONE-MARK-9\n", time.Now())
	empty := filepath.Join(root, "handoff") // the fallback directory: exists, holds nothing for this seat
	_ = os.MkdirAll(empty, 0o700)
	if l, fb := acceptanceLine(handoffSpec{Dir: empty, Pred: "builder-old"}); fb != true || l != nonePhr {
		t.Fatalf("control: the old search should find nothing: %q", l)
	}
	want := "authored handoff present: " + declared + "; this dossier does not back acceptance"
	if l, fb := acceptanceLine(handoffSpec{Path: declared, Mark: "DONE-MARK-9", Dir: empty, Pred: "builder-old"}); l != want || fb {
		t.Errorf("declared path and marker: %q fallback=%v", l, fb)
	}
	// the marker is the manifest's, not the standing one
	if l, _ := acceptanceLine(handoffSpec{Path: declared, Mark: "HANDOFF-COMPLETE"}); l != nonePhr {
		t.Errorf("a different marker must not match: %q", l)
	}
	partial := putHandoff(t, filepath.Join(root, "p"), "handoff.md", "# h\nwork\n", time.Now())
	if l, _ := acceptanceLine(handoffSpec{Path: partial, Mark: "DONE-MARK-9"}); l != nonePhr {
		t.Errorf("partial: %q", l)
	}
	if l, _ := acceptanceLine(handoffSpec{Path: filepath.Join(root, "none", "handoff.md")}); l != nonePhr {
		t.Errorf("absent: %q", l)
	}
	// a declared path decides alone: a valid fallback file does not rescue a partial declared one
	putHandoff(t, empty, "builder-old-2026-10-10.md", hdOK, time.Now())
	if l, fb := acceptanceLine(handoffSpec{Path: partial, Dir: empty, Pred: "builder-old"}); l != nonePhr || fb {
		t.Errorf("declared decides alone: %q fallback=%v", l, fb)
	}
	// end to end through the flags and the header
	cli, err := parseArgs([]string{"dossier", "--role", "ops/builder", "--handoff-path", declared, "--handoff-marker", "DONE-MARK-9"})
	if err != nil || cli.handoffPath != declared || cli.handoffMark != "DONE-MARK-9" {
		t.Fatalf("flags: %+v %v", cli, err)
	}
	if _, err := parseArgs([]string{"dossier", "--role", "ops/builder", "--handoff-marker", "a\nb"}); err == nil {
		t.Errorf("a multi-line marker was accepted")
	}
}

// An unreadable file is unreadable, never absent, whether declared or found.
func TestDossierUnreadableHandoffFileFailsClosed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode 000 file")
	}
	root := t.TempDir()
	pred := "arcaven-architect-g6-1"
	declared := putHandoff(t, filepath.Join(root, "s"), "handoff.md", hdOK, time.Now())
	dir := filepath.Join(root, "handoff")
	found := putHandoff(t, dir, pred+"-2026-10-10.md", hdOK, time.Now())
	for _, p := range []string{declared, found} {
		if err := os.Chmod(p, 0); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(p, 0o600)
	}
	if l, _ := acceptanceLine(handoffSpec{Path: declared}); l != unreadPhr {
		t.Errorf("declared, mode 000: %q", l)
	}
	if l, _ := acceptanceLine(handoffSpec{Dir: dir, Pred: pred}); l != unreadPhr {
		t.Errorf("newest found, mode 000: %q", l)
	}
}

// Anything at the handoff name that is not a readable regular file is
// "does not back acceptance", declared or found: a directory, a FIFO, a link,
// a file at mode 000. None of them may read as absent.
func TestDossierNonRegularHandoffFailsClosed(t *testing.T) {
	pred := "arcaven-architect-g6-1"
	name := pred + "-2026-10-10.md"
	mk := map[string]func(p string){
		"directory": func(p string) { _ = os.MkdirAll(p, 0o700) },
		"FIFO":      func(p string) { _ = syscall.Mkfifo(p, 0o600) },
		"symlink": func(p string) {
			real := putHandoff(t, t.TempDir(), "real.md", hdOK, time.Now())
			_ = os.Symlink(real, p)
		},
		"dangling symlink": func(p string) { _ = os.Symlink(filepath.Join(filepath.Dir(p), "gone"), p) },
		"mode 000": func(p string) {
			_ = os.WriteFile(p, []byte(hdOK), 0o600)
			_ = os.Chmod(p, 0)
		},
	}
	for kind, make := range mk {
		if kind == "mode 000" && os.Geteuid() == 0 {
			continue
		}
		dir := filepath.Join(t.TempDir(), "handoff")
		_ = os.MkdirAll(dir, 0o700)
		found := filepath.Join(dir, name)
		make(found)
		declared := filepath.Join(t.TempDir(), "handoff.md")
		make(declared)
		for where, h := range map[string]handoffSpec{"found": {Dir: dir, Pred: pred}, "declared": {Path: declared}} {
			if got := accLine(h); got != unreadPhr {
				t.Errorf("%s (%s): %q", kind, where, got)
			}
		}
		_ = os.Chmod(found, 0o600)
		_ = os.Chmod(declared, 0o600)
	}
}
