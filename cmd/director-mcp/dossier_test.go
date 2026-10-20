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
	if _, err := s.CreateConsumer(ctx, jetstream.ConsumerConfig{Durable: "mcp_builder-old_01ABC", AckPolicy: jetstream.AckExplicitPolicy, FilterSubject: "agent.ops.builder-old.*.inbox"}); err != nil {
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
	if !strings.Contains(section(out, 8), "3") {
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
		b := []byte{}
		if fi != nil && fi.Mode().IsRegular() {
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
	if _, err := os.Stat(filepath.Join(state, "dossier", "builder-new")); err != nil {
		t.Errorf("no dossier written: %v", err)
	}
}
