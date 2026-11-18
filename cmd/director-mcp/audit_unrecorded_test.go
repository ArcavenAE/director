package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// #248. The audit mirror discarded its publish error, so a send the inbox had
// accepted could leave no audit record and say nothing. These run against a
// scratch broker and skip without nats-server.

func inboxCount(t *testing.T, ctx context.Context, js jetstream.JetStream) uint64 {
	t.Helper()
	s, err := js.Stream(ctx, "AGENT_INBOX")
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return info.State.Msgs
}

// failFirstAuditJS wraps the broker handle so the first audit publish fails for
// real (the stream is gone) and the stream is back before the second. It
// counts audit attempts, so a test can show the retry ran and not infer it
// from timing.
type failFirstAuditJS struct {
	jetstream.JetStream
	attempts atomic.Int32
	restore  func()
}

func (j *failFirstAuditJS) PublishMsg(ctx context.Context, m *nats.Msg, opts ...jetstream.PublishOpt) (*jetstream.PubAck, error) {
	if m.Subject != "agent.audit" {
		return j.JetStream.PublishMsg(ctx, m, opts...)
	}
	n := j.attempts.Add(1)
	ack, err := j.JetStream.PublishMsg(ctx, m, opts...)
	if n == 1 && err != nil {
		j.restore()
	}
	return ack, err
}

func TestASendWhoseAuditPublishFailsIsAcceptedAndSaysUnrecorded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	createAudit(t, ctx, js)
	if err := js.DeleteStream(ctx, "AGENT_AUDIT"); err != nil {
		t.Fatal(err)
	}
	sender := liveSeatNoCleanup(t, ctx, url, Sender{AgentID: "sup", Workspace: "w", Team: "t"})
	liveSeatNoCleanup(t, ctx, url, Sender{AgentID: "a", Workspace: "w", Team: "t"})

	raw, _ := json.Marshal(map[string]any{"to": "agent://t/a", "performative": "INFORM", "text": "hi"})
	out, err := toolSend(ctx, sender, raw)
	if err != nil {
		t.Fatalf("a send the inbox accepted failed because its audit copy could not be written: %v", err)
	}
	m := asMap(t, out)
	if got := m["status"]; got != "accepted for delivery" {
		t.Errorf("status = %v, want accepted for delivery", got)
	}
	audit, _ := m["audit"].(string)
	if !strings.HasPrefix(audit, "not recorded:") {
		t.Errorf("audit = %q, want a text that starts with %q (the refusal path's wording)", audit, "not recorded:")
	}
	if got := inboxCount(t, ctx, js); got != 1 {
		t.Errorf("inbox holds %d copies of the send, want exactly 1", got)
	}
}

func TestASendWhoseAuditPublishFailsOnceIsRecordedByTheRetry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	createAudit(t, ctx, js)
	if err := js.DeleteStream(ctx, "AGENT_AUDIT"); err != nil {
		t.Fatal(err)
	}
	sender := liveSeatNoCleanup(t, ctx, url, Sender{AgentID: "sup", Workspace: "w", Team: "t"})
	liveSeatNoCleanup(t, ctx, url, Sender{AgentID: "a", Workspace: "w", Team: "t"})

	// The first audit attempt fails against the missing stream and the stream
	// is back before the second, so only a retry can record the copy. The
	// wrapper counts attempts: a first attempt that succeeded would leave one.
	wrap := &failFirstAuditJS{JetStream: js}
	wrap.restore = func() {
		if _, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: "AGENT_AUDIT", Subjects: []string{"agent.audit"}}); err != nil {
			t.Error(err)
		}
	}
	sender.js = wrap

	raw, _ := json.Marshal(map[string]any{"to": "agent://t/a", "performative": "INFORM", "text": "hi"})
	out, err := toolSend(ctx, sender, raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := wrap.attempts.Load(); got != 2 {
		t.Errorf("audit publish attempts = %d, want 2 (the first failed, the retry recorded it)", got)
	}
	m := asMap(t, out)
	if v, present := m["audit"]; present {
		t.Errorf("audit = %v on a send whose retry recorded the copy, want no audit key", v)
	}
	s, err := js.Stream(ctx, "AGENT_AUDIT")
	if err != nil {
		t.Fatal(err)
	}
	if got := auditCount(t, ctx, s); got != 1 {
		t.Errorf("audit holds %d records, want 1: the retry should have stored the copy once", got)
	}
}

// A retry of the audit publish reuses the envelope's Nats-Msg-Id, so a first
// attempt that landed and lost its ack leaves one record, not two. This holds
// at 5180c3e already (the id is set in auditMirrorStored), so it is a pin and
// passes before the fix.
func TestAuditRetryStoresOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	audit := createAudit(t, ctx, js)
	bus := liveSeatNoCleanup(t, ctx, url, Sender{AgentID: "sup", Workspace: "w", Team: "t"})

	raw, _ := json.Marshal(map[string]any{"to": "agent://t/a", "performative": "INFORM", "text": "hi"})
	e, _, err := buildSendEnvelope(bus.self, raw)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(e)
	if got := bus.auditMirror(ctx, e, body); got != "" {
		t.Fatalf("first mirror = %q, want recorded", got)
	}
	if got := bus.auditMirror(ctx, e, body); got != "" {
		t.Fatalf("second mirror = %q, want recorded", got)
	}
	if got := auditCount(t, ctx, audit); got != 1 {
		t.Errorf("audit holds %d records after two publishes of one message id, want 1", got)
	}
}
