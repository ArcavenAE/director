package main

import (
	"context"
	"strings"
	"testing"
)

// The identity class is closed and enforced by rejection, never by rewriting
// (director#3, R-76). These tests pin the two failure shapes the issue named:
// a wildcard id that would subscribe to every inbox on the team, and two ids
// that the old sanitize() collapsed onto one presence key while routing to
// distinct inboxes. No broker is needed: subject building is pure string
// work, so resolveSubject runs on a Bus with only self set.
func TestValidTokenRejectsSubjectMetacharacters(t *testing.T) {
	bad := map[string]string{
		"wildcard star":  "*",
		"wildcard gt":    ">",
		"embedded star":  "plan*ner",
		"dot separator":  "ops.planner",
		"space":          "ops planner",
		"leading dot":    ".planner",
		"trailing gt":    "planner>",
		"empty":          "",
		"tab":            "plan\tner",
		"unicode letter": "plannér",
	}
	for name, id := range bad {
		if err := validToken("DIRECTOR_AGENT_ID", id); err == nil {
			t.Errorf("%s: validToken(%q) = nil, want rejection", name, id)
		}
	}
	good := []string{"planner", "builder", "reviewer-a", "ops_planner", "Agent01", "a", "x-y_z-9"}
	for _, id := range good {
		if err := validToken("DIRECTOR_AGENT_ID", id); err != nil {
			t.Errorf("validToken(%q) = %v, want accepted", id, err)
		}
	}
}

// The two ids that used to collapse: "ops.planner" and "ops_planner" shared
// one presence key (sanitize mapped "." to "_") and two inbox subjects. Now
// the dotted one is refused outright and the underscored one stands alone, so
// there is no pair left to collapse.
func TestFormerlyCollapsingIdsNoLongerShareAnything(t *testing.T) {
	if err := validToken("DIRECTOR_AGENT_ID", "ops.planner"); err == nil {
		t.Fatal(`"ops.planner" accepted; it must be rejected, not rewritten to "ops_planner"`)
	}
	if err := validToken("DIRECTOR_AGENT_ID", "ops_planner"); err != nil {
		t.Fatalf(`"ops_planner" rejected: %v`, err)
	}
	if err := validToken("DIRECTOR_AGENT_ID", "ops planner"); err == nil {
		t.Fatal(`"ops planner" accepted; it must be rejected`)
	}
}

// validateIdentity covers all three launcher-assigned levers, because every
// one of them becomes a subject token.
func TestValidateIdentityCoversEveryLever(t *testing.T) {
	ok := Sender{AgentID: "builder", Team: "ops", Workspace: "aae-orc"}
	if err := validateIdentity(ok); err != nil {
		t.Fatalf("valid identity rejected: %v", err)
	}
	cases := map[string]Sender{
		"id":        {AgentID: "*", Team: "ops", Workspace: "aae-orc"},
		"team":      {AgentID: "builder", Team: "ops.*", Workspace: "aae-orc"},
		"workspace": {AgentID: "builder", Team: "ops", Workspace: ">"},
		"blank id":  {AgentID: "", Team: "ops", Workspace: "aae-orc"},
	}
	for name, s := range cases {
		err := validateIdentity(s)
		if err == nil {
			t.Errorf("%s: validateIdentity(%+v) = nil, want rejection", name, s)
			continue
		}
		if !strings.Contains(err.Error(), "director#3") {
			t.Errorf("%s: error does not name the issue: %v", name, err)
		}
	}
}

// resolveSubject is the send-side gate: a target id with a wildcard or a dot
// must be refused before a subject is built, on every address scheme. The
// target validation runs before workspace resolution, so an explicit workspace
// hint (which skips the roster) still exercises the rejection path on a Bus
// with no broker.
func TestResolveSubjectRefusesWildcardTargets(t *testing.T) {
	b := &Bus{self: Sender{AgentID: "builder", Team: "ops", Workspace: "aae-orc"}}
	ctx := context.Background()

	subject, durable, err := b.resolveSubject(ctx, "agent://ops/planner", "aae-orc")
	if err != nil || !durable || subject != "agent.aae-orc.ops.planner.inbox" {
		t.Fatalf("agent://ops/planner = %q, %v, %v", subject, durable, err)
	}

	refused := []string{
		"agent://ops/*",
		"agent://ops/>",
		"agent://ops/a.b",
		"agent://ops/ops planner",
		"agent://*/planner",
		"agent://ops.dev/planner",
		"role://ops/*",
		"role://ops/rev.iewer",
		"role://o.ps/reviewer",
		"broadcast://aae-orc/*",
		"broadcast://aae-orc/o.ps",
	}
	for _, addr := range refused {
		subject, _, err := b.resolveSubject(ctx, addr, "aae-orc")
		if err == nil {
			t.Errorf("%s resolved to %q, want rejection", addr, subject)
		}
	}

	// Workspace-wide broadcast has no team token to validate and still routes.
	subject, durable, err = b.resolveSubject(ctx, "broadcast://aae-orc", "")
	if err != nil || durable || subject != "agent.aae-orc.broadcast" {
		t.Fatalf("broadcast://aae-orc = %q, %v, %v", subject, durable, err)
	}
}
