package main

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// LR-6. A refused send used to leave no trace outside the sender's own tool
// result, so a compacted sender or a supervisor asking "did X try to reach Y"
// had nothing to read. These run against a scratch broker and skip without
// nats-server.

func createAudit(t *testing.T, ctx context.Context, js jetstream.JetStream) jetstream.Stream {
	t.Helper()
	s, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: "AGENT_AUDIT", Subjects: []string{"agent.audit"}})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func auditCount(t *testing.T, ctx context.Context, s jetstream.Stream) uint64 {
	t.Helper()
	info, err := s.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return info.State.Msgs
}

var recordedSeq = regexp.MustCompile(`audit: recorded agent\.audit seq (\d+)`)

func TestRefusedSendLeavesAuditRecordWithFullEnvelope(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	audit := createAudit(t, ctx, js)

	bus, err := connect(ctx, url, Sender{AgentID: "sup", Workspace: "w", Team: "t"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bus.close()

	raw, _ := json.Marshal(map[string]any{"to": "agent://t/nobody", "performative": "REQUEST", "text": "are you there"})
	out, err := toolSend(ctx, bus, raw)
	if err == nil {
		t.Fatalf("send to a seat with no presence returned %v, want the R-92 refusal", out)
	}
	if !strings.Contains(err.Error(), "R-92") {
		t.Errorf("refusal lost its R-92 text: %v", err)
	}
	m := recordedSeq.FindStringSubmatch(err.Error())
	if m == nil {
		t.Fatalf("refusal does not report the audit record: %v", err)
	}
	seq, _ := strconv.ParseUint(m[1], 10, 64)
	rec, err := audit.GetMsg(ctx, seq)
	if err != nil {
		t.Fatalf("get audit seq %d: %v", seq, err)
	}
	if got := rec.Header.Get("Director-Outcome"); got != "refused" {
		t.Errorf("Director-Outcome = %q, want refused", got)
	}
	if got := rec.Header.Get("Director-Stage"); got != "resolve" {
		t.Errorf("Director-Stage = %q, want resolve", got)
	}
	if !strings.Contains(rec.Header.Get("Director-Refusal"), "R-92") {
		t.Errorf("Director-Refusal = %q", rec.Header.Get("Director-Refusal"))
	}
	var e Envelope
	if err := json.Unmarshal(rec.Data, &e); err != nil {
		t.Fatalf("audit record is not an envelope: %v", err)
	}
	if e.Recipient.Address != "agent://t/nobody" || e.Content.Data != "are you there" || e.Performative != "REQUEST" || e.MessageID == "" {
		t.Errorf("audit envelope incomplete: %+v", e)
	}
	again, _ := json.Marshal(&e)
	if string(again) != string(rec.Data) {
		t.Errorf("audit record is not the envelope as built:\n got %s\nwant %s", rec.Data, again)
	}
	if got := rec.Header.Get("Nats-Msg-Id"); got != e.MessageID+"-refused" {
		t.Errorf("Nats-Msg-Id = %q, want %q", got, e.MessageID+"-refused")
	}
}

func TestRefusalOfUnparseableArgsRecordsRawArgs(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	audit := createAudit(t, ctx, js)
	bus, err := connect(ctx, url, Sender{AgentID: "sup", Workspace: "w", Team: "t"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bus.close()

	_, err = toolSend(ctx, bus, json.RawMessage(`{"to": 7}`))
	if err == nil {
		t.Fatal("unparseable arguments were accepted")
	}
	m := recordedSeq.FindStringSubmatch(err.Error())
	if m == nil {
		t.Fatalf("parse refusal does not report the audit record: %v", err)
	}
	seq, _ := strconv.ParseUint(m[1], 10, 64)
	rec, _ := audit.GetMsg(ctx, seq)
	if rec == nil || rec.Header.Get("Director-Stage") != "parse" || rec.Header.Get("Director-Outcome") != "refused" {
		t.Fatalf("parse refusal record: %+v", rec)
	}
	if !strings.Contains(string(rec.Data), `{\"to\": 7}`) || !strings.Contains(string(rec.Data), `"agent_id":"sup"`) {
		t.Errorf("parse record lacks the raw arguments or the sender: %s", rec.Data)
	}
}

func TestSuccessfulSendAddsOneUnmarkedAuditRecord(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	audit := createAudit(t, ctx, js)
	sender := liveSeatNoCleanup(t, ctx, url, Sender{AgentID: "sup", Workspace: "w", Team: "t"})
	liveSeatNoCleanup(t, ctx, url, Sender{AgentID: "a", Workspace: "w", Team: "t"})

	before := auditCount(t, ctx, audit)
	raw, _ := json.Marshal(map[string]any{"to": "agent://t/a", "performative": "INFORM", "text": "hi"})
	if _, err := toolSend(ctx, sender, raw); err != nil {
		t.Fatal(err)
	}
	if got := auditCount(t, ctx, audit) - before; got != 1 {
		t.Fatalf("audit records added = %d, want 1", got)
	}
	info, _ := audit.Info(ctx)
	rec, _ := audit.GetMsg(ctx, info.State.LastSeq)
	if rec.Header.Get("Director-Outcome") == "refused" {
		t.Errorf("a delivered send was marked refused")
	}
}

func TestRefusalWithoutAuditStreamStillRefuses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	createAudit(t, ctx, js)
	if err := js.DeleteStream(ctx, "AGENT_AUDIT"); err != nil {
		t.Fatal(err)
	}
	bus, err := connect(ctx, url, Sender{AgentID: "sup", Workspace: "w", Team: "t"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bus.close()

	raw, _ := json.Marshal(map[string]any{"to": "agent://t/nobody", "performative": "INFORM", "text": "x"})
	out, err := toolSend(ctx, bus, raw)
	if err == nil {
		t.Fatalf("refusal became a success with the audit stream gone: %v", out)
	}
	if !strings.Contains(err.Error(), "R-92") || !strings.Contains(err.Error(), "audit: not recorded:") {
		t.Errorf("want the refusal plus 'audit: not recorded', got %v", err)
	}
}

func liveSeatNoCleanup(t *testing.T, ctx context.Context, url string, self Sender) *Bus {
	t.Helper()
	b, err := connect(ctx, url, self, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.close)
	if err := b.writePresence(ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRefusalStagesValidateAndGlobal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	audit := createAudit(t, ctx, js)
	bus, err := connect(ctx, url, Sender{AgentID: "sup", Workspace: "w", Team: "t"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bus.close()

	for _, c := range []struct {
		name, stage string
		args        map[string]any
	}{
		{"bad performative", "validate", map[string]any{"to": "agent://t/a", "performative": "SHOUT", "text": "x"}},
		{"global tier off", "global", map[string]any{"to": "global://director", "performative": "INFORM", "text": "x"}},
	} {
		raw, _ := json.Marshal(c.args)
		_, err := toolSend(ctx, bus, raw)
		if err == nil {
			t.Fatalf("%s: accepted", c.name)
		}
		m := recordedSeq.FindStringSubmatch(err.Error())
		if m == nil {
			t.Fatalf("%s: no audit record reported: %v", c.name, err)
		}
		seq, _ := strconv.ParseUint(m[1], 10, 64)
		rec, err := audit.GetMsg(ctx, seq)
		if err != nil {
			t.Fatal(err)
		}
		if got := rec.Header.Get("Director-Stage"); got != c.stage {
			t.Errorf("%s: Director-Stage = %q, want %q", c.name, got, c.stage)
		}
	}
}
