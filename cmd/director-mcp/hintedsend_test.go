package main

import (
	"context"
	"encoding/json"
	"errors"
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
	if !strings.Contains(err.Error(), "no consumer filters agent.aae-orc.director.director.inbox") || !strings.Contains(err.Error(), "cold: true") {
		t.Errorf("the refusal should name the subject no consumer filters and name cold: %v", err)
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
	if !strings.Contains(err.Error(), "no consumer filters agent.aae-orc.ops.role.nobody.inbox") {
		t.Errorf("the refusal should name the role subject no consumer filters: %v", err)
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

// A raw send that carries cold decodes into sendArgs with Cold set, so a
// check that refuses arguments sendArgs does not declare still accepts it.
func TestSendArgsDeclareCold(t *testing.T) {
	raw := json.RawMessage(`{"to":"agent://ops/newseat","performative":"INFORM","text":"hi","workspace":"aae-orc","cold":true}`)
	var a sendArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatalf("a send carrying cold did not decode: %v", err)
	}
	if !a.Cold {
		t.Errorf("cold: true decoded to Cold=false: %+v", a)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&sendArgs{}); err != nil {
		t.Errorf("a strict decode of a cold send was refused: %v", err)
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

	// The same address without cold is refused, for the same reason.
	raw = json.RawMessage(`{"to":"agent://ops/newseat","performative":"INFORM","text":"again","workspace":"aae-orc"}`)
	if _, err := toolSend(ctx, sender, raw); err == nil || !strings.Contains(err.Error(), "no consumer filters agent.aae-orc.ops.newseat.inbox") {
		t.Errorf("the same hinted send without cold = %v, want a refusal naming the subject", err)
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

// A consumer is a mailbox for a subject only if it is durable and one of its
// filters equals the subject literally. An ephemeral reader, an unfiltered
// durable and a wildcard durable are not.
func TestReadsSubject(t *testing.T) {
	const subj = "agent.a.t.x.inbox"
	for _, c := range []struct {
		name string
		cfg  jetstream.ConsumerConfig
		want bool
	}{
		{"durable, exact filter", jetstream.ConsumerConfig{Durable: "d", FilterSubject: subj}, true},
		{"durable, exact entry among FilterSubjects", jetstream.ConsumerConfig{Durable: "d", FilterSubjects: []string{"agent.a.t.y.inbox", subj}}, true},
		{"ephemeral, exact filter", jetstream.ConsumerConfig{Name: "e", FilterSubject: subj}, false},
		{"durable, no filter", jetstream.ConsumerConfig{Durable: "d"}, false},
		{"durable, star filter", jetstream.ConsumerConfig{Durable: "d", FilterSubject: "agent.a.t.*.inbox"}, false},
		{"durable, tail wildcard", jetstream.ConsumerConfig{Durable: "d", FilterSubject: "agent.a.>"}, false},
		{"durable, other exact filter", jetstream.ConsumerConfig{Durable: "d", FilterSubject: "agent.a.t.y.inbox"}, false},
	} {
		if got := readsSubject(c.cfg, subj); got != c.want {
			t.Errorf("%s: readsSubject = %v, want %v", c.name, got, c.want)
		}
	}
}

// The same four cases through a real broker: only the exact durable lets a
// hinted send through.
func TestHintedSendOnlyCountsAnExactDurable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	sender := roleBus(t, ctx, url, "sender", "")

	mk := func(cfg jetstream.ConsumerConfig) {
		t.Helper()
		cfg.AckPolicy = jetstream.AckExplicitPolicy
		if _, err := js.CreateOrUpdateConsumer(ctx, "AGENT_INBOX", cfg); err != nil {
			t.Fatal(err)
		}
	}
	mk(jetstream.ConsumerConfig{Name: "eph", FilterSubject: "agent.aae-orc.ops.eph.inbox", InactiveThreshold: time.Minute})
	mk(jetstream.ConsumerConfig{Durable: "unfiltered"})
	mk(jetstream.ConsumerConfig{Durable: "wild", FilterSubject: "agent.aae-orc.ops.>"})
	mk(jetstream.ConsumerConfig{Durable: "exact", FilterSubject: "agent.aae-orc.ops.exact.inbox"})

	for _, id := range []string{"eph", "unfiltered", "wild"} {
		if err := sendRole(ctx, sender, "agent://ops/"+id, "m-"+id, "aae-orc"); err == nil {
			t.Errorf("%s: a send whose only reader is not a durable exact filter was accepted", id)
		}
	}
	// "unfiltered" and "wild" read those subjects, but are not mailboxes; the
	// subject below has an exact durable.
	if err := sendRole(ctx, sender, "agent://ops/exact", "m-exact", "aae-orc"); err != nil {
		t.Errorf("a send to an exact durable filter was refused: %v", err)
	}
}

// When the consumer list cannot be read, a hinted send is refused, and a cold
// one publishes but must not claim that no consumer filters the subject.
func TestHintedVerdictWhenTheCheckCannotRun(t *testing.T) {
	boom := errors.New("list failed")
	const subj, addr = "agent.w.t.x.inbox", "agent://t/x"

	refusal, _, _ := hintedVerdict(subj, addr, false, boom, false)
	if refusal == nil || !strings.Contains(refusal.Error(), "NOT established") || !strings.Contains(refusal.Error(), "list failed") {
		t.Errorf("unreadable list without cold = %v, want a refusal saying it was not established", refusal)
	}
	if got := refusalStage(refusal, "none"); got != "resolve" {
		t.Errorf("refusal stage = %q, want resolve", got)
	}

	refusal, noConsumer, notChecked := hintedVerdict(subj, addr, false, boom, true)
	if refusal != nil || noConsumer || notChecked != "list failed" {
		t.Errorf("unreadable list with cold = (%v, %v, %q), want published, not a no-consumer claim, and the error", refusal, noConsumer, notChecked)
	}

	refusal, noConsumer, notChecked = hintedVerdict(subj, addr, false, nil, true)
	if refusal != nil || !noConsumer || notChecked != "" {
		t.Errorf("no consumer with cold = (%v, %v, %q)", refusal, noConsumer, notChecked)
	}
	refusal, noConsumer, notChecked = hintedVerdict(subj, addr, true, nil, false)
	if refusal != nil || noConsumer || notChecked != "" {
		t.Errorf("filtered subject = (%v, %v, %q), want a plain accept", refusal, noConsumer, notChecked)
	}
}

// With the consumer list unreadable, a hinted send is refused by default,
// naming the error, and published under cold: true with a result that says the
// check could not run and does not claim that no consumer filters the subject.
func TestHintedSendWhenTheConsumerListFails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	sender := roleBus(t, ctx, url, "sender", "")
	sender.inboxConsumers = func(context.Context) ([]jetstream.ConsumerConfig, error) {
		return nil, errors.New("consumer list unreadable")
	}

	raw := json.RawMessage(`{"to":"agent://ops/seat","performative":"INFORM","text":"hi","workspace":"aae-orc"}`)
	_, err := toolSend(ctx, sender, raw)
	if err == nil || !strings.Contains(err.Error(), "NOT established") || !strings.Contains(err.Error(), "consumer list unreadable") {
		t.Fatalf("without cold = %v, want a refusal that says it was not established and carries the error", err)
	}
	if n := heldOn(t, ctx, js, "agent.aae-orc.ops.seat.inbox"); n != 0 {
		t.Errorf("%d message(s) published despite the refusal", n)
	}

	raw = json.RawMessage(`{"to":"agent://ops/seat","performative":"INFORM","text":"hi","workspace":"aae-orc","cold":true}`)
	out, err := toolSend(ctx, sender, raw)
	if err != nil {
		t.Fatalf("with cold = %v, want it published", err)
	}
	note, _ := out.(map[string]any)["cold"].(string)
	if !strings.Contains(note, "could not run") || !strings.Contains(note, "consumer list unreadable") || strings.Contains(note, "no consumer filters") {
		t.Errorf("cold note = %q, want one that says the check could not run, with the error, and no no-consumer claim", note)
	}
	if n := heldOn(t, ctx, js, "agent.aae-orc.ops.seat.inbox"); n != 1 {
		t.Errorf("held = %d, want 1", n)
	}
}
