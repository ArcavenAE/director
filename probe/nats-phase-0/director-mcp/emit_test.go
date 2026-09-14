package main

import (
	"encoding/json"
	"testing"

	"github.com/arcavenae/marvel/contracts/go/envelope"
)

func selfFixture() Sender {
	return Sender{AgentID: "builder", Workspace: "aae-orc", Team: "ops", Role: "builder"}
}

// A self-authored send builds a frame that passes the canonical schema, states
// authority none with a null seat (R-02, R-03: the shim holds no seat so it
// carries no authority and says so), and keeps the body on the wire. This is
// the shape that regressed to an empty body, now validated on emit.
func TestBuiltSendIsCanonicalAndCarriesBody(t *testing.T) {
	e := newEnvelope(selfFixture())
	e.Recipient.Address = "agent://ops/planner"
	e.Performative = envelope.PerformativeINFORM
	e.Content = envelope.Content{Type: envelope.TypeText, Data: "a real body"}

	if e.Authority.Strength != envelope.StrengthNone {
		t.Errorf("authority.strength: got %q, want none", e.Authority.Strength)
	}
	if e.Authority.Seat != nil {
		t.Errorf("authority.seat: got %v, want nil", e.Authority.Seat)
	}
	if err := checkEmitPolicy(e); err != nil {
		t.Fatalf("emit policy rejected a bodied send: %v", err)
	}
	body, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := envelope.Validate(body); err != nil {
		t.Fatalf("built frame fails the canonical schema: %v\n%s", err, body)
	}
}

// The empty-body guard is an emit-path policy, not a schema rule: the schema
// makes content.data optional, so it would accept this frame, but the shim must
// not (silent loss). Proves both halves: the schema accepts it, the policy does
// not.
func TestEmptyBodyPolicyRejectsWhatSchemaAccepts(t *testing.T) {
	e := newEnvelope(selfFixture())
	e.Recipient.Address = "agent://ops/planner"
	e.Performative = envelope.PerformativeINFORM
	e.Content = envelope.Content{Type: envelope.TypeText, Data: ""}

	body, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := envelope.Validate(body); err != nil {
		t.Fatalf("expected the schema to accept an empty text body (data is optional), got: %v", err)
	}
	if err := checkEmitPolicy(e); err == nil {
		t.Fatal("emit policy accepted an empty text body; want rejection")
	}
}

// A broadcast with no team addresses the workspace and is canonical.
func TestBuiltBroadcastIsCanonical(t *testing.T) {
	e := newEnvelope(selfFixture())
	e.Recipient.Address = "broadcast://aae-orc"
	e.Performative = envelope.PerformativeINFORM
	e.Content = envelope.Content{Type: envelope.TypeText, Data: "notice"}
	body, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := envelope.Validate(body); err != nil {
		t.Fatalf("broadcast frame fails the canonical schema: %v\n%s", err, body)
	}
}
