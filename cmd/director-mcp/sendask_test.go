package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// send_message takes an optional ask (an intake id) and kind. When the send is
// accepted, the shim writes the relay-log entry itself, through relay-log, so
// the entry is the one a manual `relay-log append --ask` writes. A send without
// ask is unchanged.

// askFixture points DIRECTOR_STATE at a scratch dir holding asks.jsonl with the
// given ids, and returns the dir.
func askFixture(t *testing.T, ids ...string) string {
	t.Helper()
	dir := t.TempDir()
	var b strings.Builder
	for _, id := range ids {
		b.WriteString(`{"ask_id": "` + id + `", "received_at": "2026-11-09T01:00:00Z", "text": "fixture", "source": "operator"}` + "\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "asks.jsonl"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DIRECTOR_STATE", dir)
	return dir
}

// relayFixture installs the repo's relay-log script as $DIRECTOR_HOME/bin/relay-log
// and points RELAY_LOG at a scratch log, and returns the log path.
func relayFixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
	src, err := os.ReadFile(filepath.Join("..", "..", "skills", "director", "scripts", "relay-log"))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "bin", "relay-log"), src, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "relay-log.md")
	t.Setenv("DIRECTOR_HOME", home)
	t.Setenv("RELAY_LOG", log)
	return log
}

func sendRaw(extra map[string]any) json.RawMessage {
	m := map[string]any{"to": "agent://fleet/builder", "performative": "REQUEST", "text": "do the thing\nsecond line"}
	for k, v := range extra {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return b
}

func TestSendAskRefusals(t *testing.T) {
	self := Sender{AgentID: "director", Team: "fleet", Workspace: "ops2"}
	askFixture(t, "I-7")
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"malformed ask", map[string]any{"ask": "7"}, `ask "7" is not an ask id`},
		{"unknown ask", map[string]any{"ask": "I-9"}, `ask "I-9" is not recorded in asks.jsonl`},
		{"kind without ask", map[string]any{"kind": "relay"}, `kind needs ask`},
		{"unknown kind", map[string]any{"ask": "I-7", "kind": "chat"}, `kind "chat" is not one of relay, ruling, build, research, question`},
		{"ask to a broadcast", map[string]any{"ask": "I-7", "to": "broadcast://ops2/fleet"}, `ask is for one recipient`},
	}
	for _, c := range cases {
		_, _, err := buildSendEnvelope(self, sendRaw(c.args))
		if err == nil {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "nothing sent") {
			t.Errorf("%s: error = %q, want it to name %q and say nothing sent", c.name, err, c.want)
		}
	}
}

func TestSendAskKnownIdIsAccepted(t *testing.T) {
	self := Sender{AgentID: "director", Team: "fleet", Workspace: "ops2"}
	askFixture(t, "I-6", "I-7")
	for _, k := range []string{"", "relay", "ruling", "build", "research", "question"} {
		args := map[string]any{"ask": "I-7"}
		if k != "" {
			args["kind"] = k
		}
		if _, _, err := buildSendEnvelope(self, sendRaw(args)); err != nil {
			t.Errorf("kind %q: %v", k, err)
		}
	}
}

func TestSendAskWithNoAsksFileIsRefusedNotTrusted(t *testing.T) {
	self := Sender{AgentID: "director", Team: "fleet", Workspace: "ops2"}
	t.Setenv("DIRECTOR_STATE", t.TempDir())
	_, _, err := buildSendEnvelope(self, sendRaw(map[string]any{"ask": "I-1"}))
	if err == nil || !strings.Contains(err.Error(), "not recorded in asks.jsonl") {
		t.Fatalf("err = %v, want the unknown-ask refusal", err)
	}
}

func TestSendWithoutAskNeverReadsTheAsksFile(t *testing.T) {
	self := Sender{AgentID: "director", Team: "fleet", Workspace: "ops2"}
	t.Setenv("DIRECTOR_STATE", filepath.Join(t.TempDir(), "absent"))
	if _, _, err := buildSendEnvelope(self, sendRaw(nil)); err != nil {
		t.Fatalf("a send with no ask changed: %v", err)
	}
}

func TestSendSchemaNamesAskAndKind(t *testing.T) {
	var props map[string]any
	for _, td := range toolCatalog(nil, false) {
		if td.Name == "send_message" {
			props = td.InputSchema["properties"].(map[string]any)
		}
	}
	for _, k := range []string{"ask", "kind"} {
		if _, ok := props[k]; !ok {
			t.Errorf("send_message schema has no %q", k)
		}
	}
	if !strings.Contains(sendArgNames, "ask, kind") {
		t.Errorf("sendArgNames = %q, want ask and kind listed", sendArgNames)
	}
}

func TestSendAcceptedWritesTheRelayEntry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	auditStream(t, ctx, js)
	sender := liveSeat(t, ctx, url, Sender{AgentID: "director", Workspace: "w", Team: "t"})
	liveSeat(t, ctx, url, Sender{AgentID: "builder", Workspace: "w", Team: "t"})
	askFixture(t, "I-7")
	log := relayFixture(t)

	out, err := toolSend(ctx, sender, sendRaw(map[string]any{"to": "agent://t/builder", "ask": "I-7", "kind": "ruling"}))
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	id, _ := m["message_id"].(string)
	if m["relay_entry"] != "R-1" {
		t.Errorf("result relay_entry = %v, want R-1", m["relay_entry"])
	}
	if _, has := m["warning"]; has {
		t.Errorf("a clean write carries a warning: %v", m["warning"])
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("no relay log written: %v", err)
	}
	got := string(b)
	for _, want := range []string{"-> agent://t/builder", "**Intake:** I-7", "**Kind:** ruling", "> do the thing\n> second line", "**Outcome:** sent; message " + id} {
		if !strings.Contains(got, want) {
			t.Errorf("relay entry lacks %q:\n%s", want, got)
		}
	}
}

func TestSendAskDefaultsKindToRelay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	auditStream(t, ctx, js)
	sender := liveSeat(t, ctx, url, Sender{AgentID: "director", Workspace: "w", Team: "t"})
	liveSeat(t, ctx, url, Sender{AgentID: "builder", Workspace: "w", Team: "t"})
	askFixture(t, "I-7")
	log := relayFixture(t)
	if _, err := toolSend(ctx, sender, sendRaw(map[string]any{"to": "agent://t/builder", "ask": "I-7"})); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(log)
	if !strings.Contains(string(b), "**Kind:** relay") {
		t.Errorf("kind did not default to relay:\n%s", b)
	}
}

func TestSendWithoutAskWritesNoRelayEntryAndAddsNoResultKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	auditStream(t, ctx, js)
	sender := liveSeat(t, ctx, url, Sender{AgentID: "director", Workspace: "w", Team: "t"})
	liveSeat(t, ctx, url, Sender{AgentID: "builder", Workspace: "w", Team: "t"})
	log := relayFixture(t)
	out, err := toolSend(ctx, sender, sendRaw(map[string]any{"to": "agent://t/builder"}))
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	for _, k := range []string{"relay_entry", "warning"} {
		if _, has := m[k]; has {
			t.Errorf("a send without ask carries %q", k)
		}
	}
	if _, err := os.Stat(log); err == nil {
		t.Error("a send without ask wrote the relay log")
	}
}

// The send has left once it is accepted, so a relay-log that cannot run is a
// warning on the accepted result, not a failed send.
func TestSendAcceptedWithNoRelayLogWarnsAndStaysAccepted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	auditStream(t, ctx, js)
	sender := liveSeat(t, ctx, url, Sender{AgentID: "director", Workspace: "w", Team: "t"})
	liveSeat(t, ctx, url, Sender{AgentID: "builder", Workspace: "w", Team: "t"})
	askFixture(t, "I-7")
	t.Setenv("DIRECTOR_HOME", t.TempDir()) // no bin/relay-log in it
	out, err := toolSend(ctx, sender, sendRaw(map[string]any{"to": "agent://t/builder", "ask": "I-7"}))
	if err != nil {
		t.Fatalf("an accepted send failed on the relay write: %v", err)
	}
	m := out.(map[string]any)
	if m["status"] != "accepted for delivery" {
		t.Errorf("status = %v", m["status"])
	}
	w, _ := m["warning"].(string)
	if !strings.Contains(w, "no relay-log entry was written") {
		t.Errorf("warning = %q, want it to say no relay-log entry was written", w)
	}
}

func TestSendRefusedForAnUnknownAskPublishesAndWritesNothing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	auditStream(t, ctx, js)
	sender := liveSeat(t, ctx, url, Sender{AgentID: "director", Workspace: "w", Team: "t"})
	liveSeat(t, ctx, url, Sender{AgentID: "builder", Workspace: "w", Team: "t"})
	askFixture(t, "I-7")
	log := relayFixture(t)
	before := streamMsgs(t, ctx, js, "AGENT_INBOX")
	if _, err := toolSend(ctx, sender, sendRaw(map[string]any{"to": "agent://t/builder", "ask": "I-8"})); err == nil {
		t.Fatal("an unknown ask was accepted")
	}
	if after := streamMsgs(t, ctx, js, "AGENT_INBOX"); after != before {
		t.Errorf("inbox grew from %d to %d on a refused send", before, after)
	}
	if _, err := os.Stat(log); err == nil {
		t.Error("a refused send wrote the relay log")
	}
}
