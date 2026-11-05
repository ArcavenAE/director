package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// A send that names a workspace builds its subject verbatim and skips the
// roster, so before this check a typo in the team or the id returned a
// sequence for a subject no consumer reads, and the mail was lost silently.
// A hinted send is now refused unless a durable consumer on AGENT_INBOX
// filters its subject, or the sender says cold: true.

func heldOn(t *testing.T, ctx context.Context, js jetstream.JetStream, subject string) uint64 {
	t.Helper()
	st, err := js.Stream(ctx, "AGENT_INBOX")
	if err != nil {
		t.Fatal(err)
	}
	info, err := st.Info(ctx, jetstream.WithSubjectFilter(subject))
	if err != nil {
		t.Fatal(err)
	}
	return info.State.Subjects[subject]
}

func TestHintedAgentSendWithNoConsumerIsRefused(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	sender := roleBus(t, ctx, url, "sender", "")

	err := sendRole(ctx, sender, "agent://director/director", "lost1", "aae-orc")
	if err == nil {
		t.Fatal("a hinted send to a subject no consumer filters was accepted")
	}
	if !strings.Contains(err.Error(), "no consumer") || !strings.Contains(err.Error(), "cold") {
		t.Errorf("the refusal should say no consumer filters the subject and name cold: %v", err)
	}
	if got := refusalStage(err, "none"); got != "resolve" {
		t.Errorf("refusal stage = %q, want resolve", got)
	}
	if n := heldOn(t, ctx, js, "agent.aae-orc.director.director.inbox"); n != 0 {
		t.Errorf("%d message(s) were published to the unread subject", n)
	}
}

func TestHintedRoleSendWithNoConsumerIsRefused(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	sender := roleBus(t, ctx, url, "sender", "")

	err := sendRole(ctx, sender, "role://ops/nobody", "lost2", "aae-orc")
	if err == nil {
		t.Fatal("a hinted role send to a subject no consumer filters was accepted")
	}
	if got := refusalStage(err, "none"); got != "resolve" {
		t.Errorf("refusal stage = %q, want resolve", got)
	}
	if n := heldOn(t, ctx, js, "agent.aae-orc.ops.role.nobody.inbox"); n != 0 {
		t.Errorf("%d message(s) were published to the unread role subject", n)
	}
}

// A hinted send to a seat that has a durable is not refused, for an agent
// inbox and for a role inbox.
func TestHintedSendToAFilteredSubjectIsAccepted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	provision(t, ctx, url)
	holder := roleBus(t, ctx, url, "rev-1", "reviewer")
	sender := roleBus(t, ctx, url, "sender", "")

	if err := sendRole(ctx, sender, "agent://ops/rev-1", "h1", "aae-orc"); err != nil {
		t.Fatalf("hinted send to a seat with a durable refused: %v", err)
	}
	if err := sendRole(ctx, sender, "role://ops/reviewer", "h2", "aae-orc"); err != nil {
		t.Fatalf("hinted role send to a holder's durable refused: %v", err)
	}
	for _, want := range []string{"h1", "h2"} {
		if e, _, err := holder.receive(ctx, 3*time.Second); err != nil || e == nil || e.MessageID != want {
			t.Fatalf("holder got %v, %v; want %s", e, err, want)
		}
	}
}

// cold: true keeps mail-before-first-start: it publishes as before, and the
// result says no consumer filters the subject yet.
func TestColdHintedSendPublishesAndSaysNoConsumer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	sender := roleBus(t, ctx, url, "sender", "")

	raw := json.RawMessage(`{"to":"agent://ops/newseat","performative":"INFORM","text":"sent before the seat starts","workspace":"aae-orc","cold":true}`)
	out, err := toolSend(ctx, sender, raw)
	if err != nil {
		t.Fatalf("a cold send was refused: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok || m["status"] != "accepted for delivery" {
		t.Fatalf("result = %#v, want accepted for delivery", out)
	}
	note, _ := m["cold"].(string)
	if !strings.Contains(note, "no consumer") {
		t.Errorf("a cold send to an unfiltered subject should say so in its result, got %#v", m)
	}
	if n := heldOn(t, ctx, js, "agent.aae-orc.ops.newseat.inbox"); n != 1 {
		t.Errorf("held = %d, want 1", n)
	}

	// The same address without cold is refused.
	raw = json.RawMessage(`{"to":"agent://ops/newseat","performative":"INFORM","text":"again","workspace":"aae-orc"}`)
	if _, err := toolSend(ctx, sender, raw); err == nil {
		t.Error("the same hinted send without cold was accepted")
	}
}

// A cold send to a subject a consumer does filter carries no such note.
func TestColdSendToAFilteredSubjectHasNoNote(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	provision(t, ctx, url)
	roleBus(t, ctx, url, "rev-1", "reviewer")
	sender := roleBus(t, ctx, url, "sender", "")

	raw := json.RawMessage(`{"to":"agent://ops/rev-1","performative":"INFORM","text":"hi","workspace":"aae-orc","cold":true}`)
	out, err := toolSend(ctx, sender, raw)
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := out.(map[string]any); m["cold"] != nil {
		t.Errorf("a cold send to a filtered subject carried a cold note: %#v", m)
	}
}

func TestSubjectMatchesFilter(t *testing.T) {
	for _, c := range []struct {
		filter, subject string
		want            bool
	}{
		{"agent.a.t.x.inbox", "agent.a.t.x.inbox", true},
		{"agent.a.t.x.inbox", "agent.a.t.y.inbox", false},
		{"agent.*.t.x.inbox", "agent.a.t.x.inbox", true},
		{"agent.a.>", "agent.a.t.x.inbox", true},
		{"agent.b.>", "agent.a.t.x.inbox", false},
		{"agent.a.t.x", "agent.a.t.x.inbox", false},
		{"agent.a.t.x.inbox.extra", "agent.a.t.x.inbox", false},
	} {
		if got := subjectMatchesFilter(c.filter, c.subject); got != c.want {
			t.Errorf("subjectMatchesFilter(%q, %q) = %v, want %v", c.filter, c.subject, got, c.want)
		}
	}
}
