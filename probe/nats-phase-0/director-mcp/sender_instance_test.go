package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Part E1 of unread slice M (sim/design/unread-slice-m.md, sim/design/
// envelope-sender-instance.md, director#126): every send carries
// sender.instance, the sending session's instance. The unread reader counts a
// reply as evidence that a seat reads outside its durable only when
// sender.instance names that durable's instance, so two live sessions of one
// agent must never share it, and a send that leaves it empty can never be
// mistaken for one that names a session. sender.session stays the harness
// session UUID, marvel's to set, and the shim leaves it unset.

func TestSendCarriesTheSendingSessionInstance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	auditStream(t, ctx, js)

	one := liveSeat(t, ctx, url, Sender{AgentID: "seat", Workspace: "w", Team: "t"})
	two := liveSeat(t, ctx, url, Sender{AgentID: "seat", Workspace: "w", Team: "t"})
	rcpt := liveSeat(t, ctx, url, Sender{AgentID: "rcpt", Workspace: "w", Team: "t"})
	if one.instance == two.instance {
		t.Fatalf("two sessions of one agent share instance %q", one.instance)
	}

	for _, s := range []*Bus{one, two} {
		raw, _ := json.Marshal(map[string]any{"to": "agent://t/rcpt", "performative": "INFORM", "text": "from " + s.instance})
		if _, err := toolSend(ctx, s, raw); err != nil {
			t.Fatalf("send from %s: %v", s.instance, err)
		}
		e, _, err := rcpt.receive(ctx, 2*time.Second)
		if err != nil || e == nil {
			t.Fatalf("rcpt received nothing from %s: %v", s.instance, err)
		}
		if e.Sender.Instance != s.instance {
			t.Errorf("sender.instance = %q, want the sending session's instance %q", e.Sender.Instance, s.instance)
		}
		if e.Sender.Session != "" {
			t.Errorf("sender.session = %q, want it unset (the harness session UUID is marvel's to set)", e.Sender.Session)
		}
		if e.Sender.AgentID != "seat" {
			t.Errorf("sender.agent_id = %q, want seat", e.Sender.AgentID)
		}
	}
}

func TestBroadcastCarriesTheSendingSessionInstance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	auditStream(t, ctx, js)

	sender := liveSeat(t, ctx, url, Sender{AgentID: "sup", Workspace: "w", Team: "t"})
	a := liveSeat(t, ctx, url, Sender{AgentID: "a", Workspace: "w", Team: "t"})

	raw, _ := json.Marshal(map[string]any{"to": "broadcast://w/t", "performative": "INFORM", "text": "all"})
	if _, err := toolSend(ctx, sender, raw); err != nil {
		t.Fatalf("send to broadcast://w/t: %v", err)
	}
	e, _, err := a.receive(ctx, 2*time.Second)
	if err != nil || e == nil {
		t.Fatalf("a received nothing: %v", err)
	}
	if e.Sender.Instance != sender.instance {
		t.Errorf("sender.instance = %q, want %q", e.Sender.Instance, sender.instance)
	}
	if e.Sender.Session != "" {
		t.Errorf("sender.session = %q, want it unset", e.Sender.Session)
	}
}

// The field is on the wire under its envelope name, so a reader that decodes
// the raw message (the unread scan does, by sequence) sees it, and an unset
// session does not appear at all.
func TestSenderInstanceSerializesAsInstance(t *testing.T) {
	e := newEnvelope(Sender{AgentID: "a", Workspace: "w", Team: "t", Instance: "01ABC"})
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var back struct {
		Sender map[string]any `json:"sender"`
	}
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Sender["instance"] != "01ABC" {
		t.Errorf("sender = %v, want instance 01ABC", back.Sender)
	}
	if _, ok := back.Sender["session"]; ok {
		t.Errorf("sender = %v, want no session key when it is unset", back.Sender)
	}
}

// The canonical envelope schema, copied from ArcavenAE/marvel at 7177ca2
// (marvel#447 sender.instance, marvel#449 optional authority) into testdata, so
// a later drift in either copy fails here. The copy is byte-identical to
// contracts/schema/director-envelope.schema.json at that commit.
func compileEnvelopeSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	data, err := os.ReadFile("testdata/director-envelope.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	const id = "https://schema.arcaven.com/director/envelope/v1"
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func validateAgainstSchema(t *testing.T, s *jsonschema.Schema, e *Envelope) error {
	t.Helper()
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return s.Validate(inst)
}

// A send the shim builds validates against the whole canonical envelope: the
// instance is a real ULID that satisfies the sender.instance pattern, and with
// authority optional (director#197, marvel#449) nothing else is missing. The
// negative controls prove the check can fail: a non-ULID instance, and the
// original director#190 mistake of putting the ULID in sender.session.
func TestSendEnvelopeConformsToTheCanonicalSchema(t *testing.T) {
	s := compileEnvelopeSchema(t)
	self := Sender{AgentID: "seat", Workspace: "w", Team: "t", Instance: newInstanceID()}
	raw, _ := json.Marshal(map[string]any{"to": "agent://t/rcpt", "performative": "INFORM", "text": "hi"})

	e, _, err := buildSendEnvelope(self, raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateAgainstSchema(t, s, e); err != nil {
		t.Fatalf("a shim send does not validate against the canonical schema: %v", err)
	}

	bad := *e
	bad.Sender.Instance = "01ABC"
	if validateAgainstSchema(t, s, &bad) == nil {
		t.Error("negative control: a non-ULID sender.instance validated")
	}

	wrong := *e
	wrong.Sender.Instance = ""
	wrong.Sender.Session = self.Instance
	if validateAgainstSchema(t, s, &wrong) == nil {
		t.Error("negative control: a ULID in sender.session validated")
	}
}
