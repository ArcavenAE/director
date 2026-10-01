package main

// Tests for the P1 channel cue (sim/design/channel-cue.md section 5). The cue
// core takes its bus, clock, nonce and output as functions, so every case
// here runs without a broker.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

type fakeSource struct {
	n      uint64
	oldest *cueMeta
	err    error
}

func (f *fakeSource) count(context.Context) (uint64, error)        { return f.n, f.err }
func (f *fakeSource) oldestMeta(context.Context) (*cueMeta, error) { return f.oldest, nil }

type cueRig struct {
	t      *testing.T
	now    time.Time
	src    *fakeSource
	cues   []map[string]any
	warns  []string
	nonces int
	c      *cue
}

func newCueRig(t *testing.T) *cueRig {
	r := &cueRig{t: t, now: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
	r.src = &fakeSource{oldest: &cueMeta{Sender: "sup@ws", Performative: "REQUEST", MessageID: "m1"}}
	cfg := defaultCueConfig()
	cfg.Enabled = true
	r.c = newCue(cfg, r.src,
		func(p map[string]any) { r.cues = append(r.cues, p) },
		func(ids []string, state string) { r.warns = append(r.warns, strings.Join(ids, ",")+" "+state) },
		func() time.Time { return r.now },
		func() string { r.nonces++; return fmt.Sprintf("nonce%d", r.nonces) },
	)
	return r
}

// started runs the handshake on the measured version and the self-test.
func (r *cueRig) started() {
	if v, ch := r.c.handshake("2025-11-25"); v != "2025-11-25" || !ch {
		r.t.Fatalf("handshake on a measured version: got %q channel=%v", v, ch)
	}
	r.c.initialized()
}

func (r *cueRig) step(d time.Duration) { r.now = r.now.Add(d); r.c.tick(context.Background()) }

func meta(t *testing.T, p map[string]any) map[string]any {
	t.Helper()
	m, ok := p["meta"].(map[string]any)
	if !ok {
		t.Fatalf("cue without meta: %v", p)
	}
	return m
}

// ---- C-1: opt-in, handshake, version list -------------------------------

func TestCueOffInitializeAndToolsListAreUnchanged(t *testing.T) {
	var buf bytes.Buffer
	s := &Server{out: bufio.NewWriter(&buf), tools: toolCatalog(nil, false), log: func(string, ...any) {}}
	s.dispatch(context.Background(), &rpcRequest{ID: json.RawMessage(`1`), Method: "initialize",
		Params: json.RawMessage(`{"protocolVersion":"2025-11-25"}`)})
	want := `{"jsonrpc":"2.0","id":1,"result":{"capabilities":{"tools":{}},"protocolVersion":"2025-06-18","serverInfo":{"name":"director-mcp","version":"0.1-probe"}}}`
	if got := strings.TrimSpace(buf.String()); got != want {
		t.Fatalf("cue-off initialize changed:\n got %s\nwant %s", got, want)
	}
	for _, tool := range toolCatalog(nil, false) {
		if tool.Name == "inbox_summary" {
			props, _ := tool.InputSchema["properties"].(map[string]any)
			if _, ok := props["cue_ack"]; ok {
				t.Fatal("cue-off tools/list must not offer cue_ack")
			}
		}
	}
}

func TestServerDiscoverGetsMethodNotFoundWithCueOnOrOff(t *testing.T) {
	for _, on := range []bool{false, true} {
		var buf bytes.Buffer
		s := &Server{out: bufio.NewWriter(&buf), tools: toolCatalog(nil, on), log: func(string, ...any) {}}
		if on {
			s.cue = newCueRig(t).c
		}
		s.dispatch(context.Background(), &rpcRequest{ID: json.RawMessage(`"server-discover-probe-1"`), Method: "server/discover"})
		if !strings.Contains(buf.String(), `"code":-32601`) {
			t.Fatalf("cue=%v: server/discover must get -32601 so the client falls back to initialize: %s", on, buf.String())
		}
	}
}

func TestCueOnInitializeEchoesAMeasuredVersionAndDeclaresTheChannel(t *testing.T) {
	var buf bytes.Buffer
	s := &Server{out: bufio.NewWriter(&buf), tools: toolCatalog(nil, true), log: func(string, ...any) {}, cue: newCueRig(t).c}
	s.dispatch(context.Background(), &rpcRequest{ID: json.RawMessage(`1`), Method: "initialize",
		Params: json.RawMessage(`{"protocolVersion":"2025-11-25"}`)})
	out := buf.String()
	if !strings.Contains(out, `"protocolVersion":"2025-11-25"`) || !strings.Contains(out, `"claude/channel":{}`) {
		t.Fatalf("cue-on initialize: %s", out)
	}
}

func TestAClientVersionOutsideTheListTurnsTheCueOff(t *testing.T) {
	r := newCueRig(t)
	v, ch := r.c.handshake("2026-07-28")
	if v != "2025-06-18" || ch {
		t.Fatalf("unmeasured version must answer as today: got %q channel=%v", v, ch)
	}
	st := r.c.status()
	if st["state"] != "off" || !strings.Contains(st["reason"], "protocol 2026-07-28 not verified") {
		t.Fatalf("status: %v", st)
	}
	r.c.initialized()
	r.src.n = 1
	r.step(3 * time.Second)
	if len(r.cues) != 0 {
		t.Fatalf("an off cue must send nothing, sent %v", r.cues)
	}
}

// ---- C-2: watcher ---------------------------------------------------------

func TestARiseWithNoOpenWaitSendsOneMetadataOnlyCue(t *testing.T) {
	r := newCueRig(t)
	r.started()
	r.cues = nil // the self-test
	r.src.n = 1
	r.step(2 * time.Second)
	if len(r.cues) != 1 {
		t.Fatalf("want 1 cue, got %d: %v", len(r.cues), r.cues)
	}
	p := r.cues[0]
	if p["content"] != cueContent && !strings.HasPrefix(p["content"].(string), cueContent) {
		t.Fatalf("content: %v", p["content"])
	}
	for k := range p {
		if k != "content" && k != "meta" {
			t.Fatalf("cue carries %q; only content and meta are allowed", k)
		}
	}
	m := meta(t, p)
	if m["sender"] != "sup@ws" || m["performative"] != "REQUEST" || m["message_id"] != "m1" || m["count"] != "1" {
		t.Fatalf("meta: %v", m)
	}
	for k := range m {
		switch k {
		case "sender", "performative", "message_id", "count", "nonce", "kind":
		default:
			t.Fatalf("meta carries %q, which is not metadata", k)
		}
	}
}

func TestAnOpenWaitGetsNoCue(t *testing.T) {
	r := newCueRig(t)
	r.started()
	r.cues = nil
	r.c.waitBegin()
	r.src.n = 1
	r.step(2 * time.Second)
	r.c.waitEnd()
	r.src.n = 0
	r.step(2 * time.Second)
	if len(r.cues) != 0 {
		t.Fatalf("an open wait delivers the message; no cue: %v", r.cues)
	}
}

func TestArrivalsInsideTheFloorFoldIntoOneCue(t *testing.T) {
	r := newCueRig(t)
	r.started()
	r.cues = nil
	r.src.n = 1
	r.step(2 * time.Second) // first arrival after quiet: cues at once
	for i := 0; i < 3; i++ {
		r.src.n++
		r.step(1 * time.Second)
	}
	if len(r.cues) != 1 {
		t.Fatalf("arrivals inside the floor must wait for its end: %d cues", len(r.cues))
	}
	r.step(3 * time.Second) // past the 5 s floor
	if len(r.cues) != 2 {
		t.Fatalf("want the folded cue at the end of the floor, got %d", len(r.cues))
	}
	if c := meta(t, r.cues[1])["count"]; c != "3" {
		t.Fatalf("folded count: %v", c)
	}
}

func TestABurstSeenInOneTickIsOneCue(t *testing.T) {
	r := newCueRig(t)
	r.started()
	r.cues = nil
	r.src.n = 3
	r.step(2 * time.Second)
	if len(r.cues) != 1 || meta(t, r.cues[0])["count"] != "3" {
		t.Fatalf("burst: %v", r.cues)
	}
}

func TestAReconnectCuesOnceIfAnythingIsWaiting(t *testing.T) {
	r := newCueRig(t)
	r.started()
	r.src.n = 2
	r.step(2 * time.Second)
	r.c.answered() // drained by the model
	r.cues = nil
	r.step(10 * time.Second) // nothing new
	r.c.reconnected()
	r.step(2 * time.Second)
	if len(r.cues) != 1 {
		t.Fatalf("after reconnect with mail waiting, one cue: %v", r.cues)
	}
	r.step(10 * time.Second)
	if len(r.cues) != 1 {
		t.Fatalf("only once: %v", r.cues)
	}
}

// ---- C-3: receipt ---------------------------------------------------------

func TestAnUnansweredCueWarnsRecuesOnceThenStops(t *testing.T) {
	r := newCueRig(t)
	r.started()
	r.cues = nil
	r.src.n = 1
	r.step(2 * time.Second)
	r.step(299 * time.Second)
	if len(r.warns) != 0 {
		t.Fatalf("no warning inside W: %v", r.warns)
	}
	r.step(2 * time.Second)
	if len(r.warns) != 1 || len(r.cues) != 2 {
		t.Fatalf("after W: want 1 warning and 1 re-cue, got %v / %d cues", r.warns, len(r.cues))
	}
	if !strings.HasPrefix(r.warns[0], "m1 ") {
		t.Fatalf("the warning names the message: %q", r.warns[0])
	}
	r.step(301 * time.Second)
	if len(r.warns) != 2 || len(r.cues) != 2 {
		t.Fatalf("after a second W: a second warning and no third cue, got %v / %d cues", r.warns, len(r.cues))
	}
	r.step(900 * time.Second)
	if len(r.warns) != 2 || len(r.cues) != 2 {
		t.Fatalf("then it stops: %v / %d cues", r.warns, len(r.cues))
	}
}

func TestADrainInsideTheWindowEmitsNothing(t *testing.T) {
	r := newCueRig(t)
	r.started()
	r.cues = nil
	r.src.n = 1
	r.step(2 * time.Second)
	r.step(60 * time.Second)
	r.src.n = 0 // the cued message left the durable
	r.step(2 * time.Second)
	r.step(600 * time.Second)
	if len(r.warns) != 0 || len(r.cues) != 1 {
		t.Fatalf("a drained cue is answered: %v / %d cues", r.warns, len(r.cues))
	}
}

// ---- C-4: self-test -------------------------------------------------------

func TestTheSelfTestSendsANonceAndOnlyItsEchoMakesTheCueLive(t *testing.T) {
	r := newCueRig(t)
	r.started()
	if len(r.cues) != 1 {
		t.Fatalf("one self-test cue after initialized, got %d", len(r.cues))
	}
	m := meta(t, r.cues[0])
	if m["kind"] != "self-test" || m["nonce"] != "nonce1" {
		t.Fatalf("self-test meta: %v", m)
	}
	if !strings.Contains(r.cues[0]["content"].(string), "cue_ack") {
		t.Fatalf("self-test content must ask for cue_ack: %v", r.cues[0]["content"])
	}
	if r.c.status()["state"] != "unverified" {
		t.Fatalf("before the echo: %v", r.c.status())
	}
	r.c.answered() // an inbox_summary without cue_ack
	r.c.waitBegin()
	r.c.waitEnd()
	if r.c.status()["state"] != "unverified" {
		t.Fatalf("a drain without cue_ack proves nothing: %v", r.c.status())
	}
	r.c.ack("nonce1")
	if r.c.status()["state"] != "live" {
		t.Fatalf("the nonce echo makes it live: %v", r.c.status())
	}
}

func TestAWrongOrPreviousNonceLeavesItUnverified(t *testing.T) {
	prev := newCueRig(t)
	prev.started()
	old := meta(t, prev.cues[0])["nonce"].(string) // the previous start's self-test nonce
	r := newCueRig(t)
	r.c.nonce = func() string { return "this-start" }
	r.started()
	r.c.ack(old)
	if st := r.c.status(); st["state"] != "unverified" || st["reason"] != "wrong nonce" {
		t.Fatalf("previous start's nonce: %v", st)
	}
	r.c.ack("garbage")
	if st := r.c.status(); st["state"] != "unverified" || st["reason"] != "wrong nonce" {
		t.Fatalf("wrong nonce: %v", st)
	}
}

func TestNoEchoInsideTheSelfTestWindowNamesTheReason(t *testing.T) {
	r := newCueRig(t)
	r.started()
	r.step(121 * time.Second)
	if st := r.c.status(); st["state"] != "unverified" || st["reason"] != "no nonce echo after self-test" {
		t.Fatalf("status: %v", st)
	}
}

func TestWhileUnverifiedACueDrainedWithoutAckStaysUnverifiedAndALaterEchoPromotes(t *testing.T) {
	r := newCueRig(t)
	r.started()
	r.step(121 * time.Second) // self-test window passes unanswered
	r.cues = nil
	r.src.n = 1
	r.step(2 * time.Second)
	if len(r.cues) != 1 {
		t.Fatalf("an unverified seat is still cued: %v", r.cues)
	}
	n1, _ := meta(t, r.cues[0])["nonce"].(string)
	if n1 == "" || n1 == "nonce1" {
		t.Fatalf("each cue while unverified carries a fresh nonce: %q", n1)
	}
	r.c.waitBegin() // drains without cue_ack
	r.src.n = 0
	r.c.waitEnd()
	r.step(2 * time.Second)
	if r.c.status()["state"] != "unverified" {
		t.Fatalf("a drain never promotes: %v", r.c.status())
	}
	r.step(400 * time.Second)
	if len(r.warns) != 0 {
		t.Fatalf("the drain counted as answered: %v", r.warns)
	}
	r.src.n = 1
	r.step(2 * time.Second)
	n2, _ := meta(t, r.cues[len(r.cues)-1])["nonce"].(string)
	r.c.ack(n2)
	if r.c.status()["state"] != "live" {
		t.Fatalf("a later cue's nonce echo promotes: %v", r.c.status())
	}
	r.src.n = 2
	r.step(10 * time.Second)
	if _, has := meta(t, r.cues[len(r.cues)-1])["nonce"]; has {
		t.Fatal("a live seat's cues carry no nonce")
	}
}

func TestCueAckIsReadFromInboxSummaryArguments(t *testing.T) {
	if got := cueAckArg(json.RawMessage(`{"limit":5,"cue_ack":"abc"}`)); got != "abc" {
		t.Fatalf("cue_ack: %q", got)
	}
	if got := cueAckArg(json.RawMessage(`{}`)); got != "" {
		t.Fatalf("absent cue_ack: %q", got)
	}
}

func TestALateSelfTestEchoLeavesItUnverified(t *testing.T) {
	r := newCueRig(t)
	r.started()
	r.now = r.now.Add(121 * time.Second) // past the 120 s self-test window
	r.c.ack("nonce1")
	if st := r.c.status(); st["state"] != "unverified" || st["reason"] != "wrong nonce" {
		t.Fatalf("a late self-test echo must not promote: %v", st)
	}
}

func TestACueNonceEchoedAfterWLeavesItUnverifiedAndInsideWPromotes(t *testing.T) {
	for _, tc := range []struct {
		after time.Duration
		want  string
	}{{301 * time.Second, "unverified"}, {299 * time.Second, "live"}} {
		r := newCueRig(t)
		r.started()
		r.now = r.now.Add(121 * time.Second)
		r.src.n = 1
		r.step(2 * time.Second)
		n, _ := meta(t, r.cues[len(r.cues)-1])["nonce"].(string)
		r.now = r.now.Add(tc.after) // W is 300 s; no tick, so no re-cue
		r.c.ack(n)
		if st := r.c.status(); st["state"] != tc.want {
			t.Fatalf("echo %s after the cue: want %s, got %v", tc.after, tc.want, st)
		}
	}
}

func TestExpiredCueNoncesArePrunedWhileUnverified(t *testing.T) {
	r := newCueRig(t)
	r.started()
	for i := 0; i < 50; i++ {
		r.src.n++
		r.step(400 * time.Second) // each cue's nonce expires before the next
	}
	if got := len(r.c.valid); got > 2 {
		t.Fatalf("an unverified seat keeps only live nonces, kept %d", got)
	}
}

func TestAFailedInboxReadIsShownOnceAndClearsOnTheNextGoodRead(t *testing.T) {
	r := newCueRig(t)
	var logs []string
	r.c.logf = func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }
	r.started()
	r.c.ack("nonce1")
	r.src.err = fmt.Errorf("consumer not found")
	r.step(2 * time.Second)
	r.step(2 * time.Second)
	st := r.c.status()
	if st["state"] != "live" || !strings.Contains(st["reason"], "watcher: cannot read inbox state: consumer not found") {
		t.Fatalf("a failed read must show in the reason without changing the state: %v", st)
	}
	if len(logs) != 1 {
		t.Fatalf("logged once per transition, got %v", logs)
	}
	r.src.err = nil
	r.step(2 * time.Second)
	r.step(2 * time.Second)
	if st := r.c.status(); st["reason"] != "" {
		t.Fatalf("a good read clears it: %v", st)
	}
	if len(logs) != 2 {
		t.Fatalf("one log on failure, one on recovery, got %v", logs)
	}
}

func TestAWatcherReasonDoesNotEraseTheSelfTestReason(t *testing.T) {
	r := newCueRig(t)
	r.started()
	r.step(121 * time.Second) // no echo: the self-test reason is set
	r.src.err = fmt.Errorf("boom")
	r.step(2 * time.Second)
	r.src.err = nil
	r.step(2 * time.Second)
	if st := r.c.status(); st["reason"] != "no nonce echo after self-test" {
		t.Fatalf("the self-test reason must survive the watcher clearing: %v", st)
	}
}

func TestPruningKeepsANonceStillInsideW(t *testing.T) {
	r := newCueRig(t)
	r.started()
	r.now = r.now.Add(121 * time.Second)
	r.src.n = 1
	r.step(2 * time.Second)
	first, _ := meta(t, r.cues[len(r.cues)-1])["nonce"].(string)
	r.src.n = 2
	r.step(10 * time.Second) // a second cue while the first nonce is live
	if len(r.cues) < 3 {
		t.Fatalf("want a second cue, got %d", len(r.cues))
	}
	r.c.ack(first)
	if st := r.c.status(); st["state"] != "live" {
		t.Fatalf("an earlier cue's nonce still inside W must promote: %v", st)
	}
}
