package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

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

func inboxCountOrZero(ctx context.Context, js jetstream.JetStream) uint64 {
	s, err := js.Stream(ctx, "AGENT_INBOX")
	if err != nil {
		return 0
	}
	info, err := s.Info(ctx)
	if err != nil {
		return 0
	}
	return info.State.Msgs
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

	// The client's own publish already waits about half a second for a missing
	// stream. The audit stream comes back a little after that wait ends, so the
	// first attempt fails and only a second attempt can find the stream.
	created := make(chan error, 1)
	go func() {
		for inboxCountOrZero(ctx, js) < 1 {
			time.Sleep(time.Millisecond)
		}
		time.Sleep(600 * time.Millisecond)
		_, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: "AGENT_AUDIT", Subjects: []string{"agent.audit"}})
		created <- err
	}()

	raw, _ := json.Marshal(map[string]any{"to": "agent://t/a", "performative": "INFORM", "text": "hi"})
	out, err := toolSend(ctx, sender, raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-created; err != nil {
		t.Fatal(err)
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
