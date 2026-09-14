package main

import (
	"context"
	"strings"
	"testing"
)

// R-92: a directed subject is built from the RECIPIENT's workspace, resolved
// from the recipient's live presence, not from the sender's. Before this fix a
// send from workspace aae-orc to agent://fleet/<id> landed on
// agent.aae-orc.fleet.<id>.inbox while the recipient consumed
// agent.ops2.fleet.<id>.inbox, so it was accepted and never delivered.
// pickWorkspace is the resolution core, split out so the three outcomes run
// without a broker.

func TestPickWorkspaceResolvesSingleLiveWorkspace(t *testing.T) {
	// Two live instances of one id, both in ops2: one workspace, it resolves.
	ws, err := pickWorkspace("agent://fleet/builder", []string{"ops2", "ops2"})
	if err != nil {
		t.Fatalf("one live workspace should resolve, got error: %v", err)
	}
	if ws != "ops2" {
		t.Fatalf("resolved workspace = %q, want ops2", ws)
	}
}

func TestPickWorkspaceRefusesNoLivePresence(t *testing.T) {
	_, err := pickWorkspace("agent://fleet/builder", nil)
	if err == nil {
		t.Fatal("no live presence must refuse, not resolve to a subject nobody filters")
	}
	if !strings.Contains(err.Error(), "R-92") || !strings.Contains(err.Error(), "no session would consume") {
		t.Fatalf("refusal should name the cause and R-92: %v", err)
	}
}

func TestPickWorkspaceRefusesAmbiguous(t *testing.T) {
	_, err := pickWorkspace("role://fleet/builder", []string{"ops2", "aae-orc"})
	if err == nil {
		t.Fatal("two live workspaces must refuse as ambiguous")
	}
	// The refusal lists both so the caller can disambiguate with a hint.
	if !strings.Contains(err.Error(), "ops2") || !strings.Contains(err.Error(), "aae-orc") {
		t.Fatalf("ambiguous refusal should list the workspaces: %v", err)
	}
}

// The escape hatch: an explicit workspace hint builds the subject verbatim and
// never touches the roster, so a cold mailbox (no live presence) is still
// addressable. The nil-kv Bus is the proof: the hint path must not read the
// roster, or this panics.
func TestResolveSubjectHintSkipsRoster(t *testing.T) {
	b := &Bus{self: Sender{AgentID: "director", Team: "fleet", Workspace: "aae-orc"}}
	ctx := context.Background()

	subject, durable, err := b.resolveSubject(ctx, "agent://fleet/builder", "ops2")
	if err != nil || !durable || subject != "agent.ops2.fleet.builder.inbox" {
		t.Fatalf("agent hint = %q, %v, %v", subject, durable, err)
	}
	subject, durable, err = b.resolveSubject(ctx, "role://fleet/builder", "ops2")
	if err != nil || !durable || subject != "agent.ops2.fleet.role.builder.inbox" {
		t.Fatalf("role hint = %q, %v, %v", subject, durable, err)
	}
}

// A malformed workspace hint is rejected as a subject token, so an untrusted
// override cannot inject subject metacharacters.
func TestResolveSubjectRejectsMalformedHint(t *testing.T) {
	b := &Bus{self: Sender{AgentID: "director", Team: "fleet", Workspace: "aae-orc"}}
	ctx := context.Background()

	for _, hint := range []string{"ops.2", "ops*", "ops 2", ">"} {
		if _, _, err := b.resolveSubject(ctx, "agent://fleet/builder", hint); err == nil {
			t.Errorf("hint %q built a subject, want rejection", hint)
		}
	}
}

// Broadcast builds from the workspace in the address, not the sender's, so a
// sender in one workspace can broadcast into another (the same defect class the
// directed sends had). No roster lookup, so the nil-kv Bus is fine.
func TestResolveSubjectBroadcastUsesAddressWorkspace(t *testing.T) {
	b := &Bus{self: Sender{AgentID: "director", Team: "fleet", Workspace: "aae-orc"}}
	ctx := context.Background()

	subject, durable, err := b.resolveSubject(ctx, "broadcast://ops2/fleet", "")
	if err != nil || durable || subject != "agent.ops2.fleet.broadcast" {
		t.Fatalf("broadcast team = %q, %v, %v", subject, durable, err)
	}
	subject, durable, err = b.resolveSubject(ctx, "broadcast://ops2", "")
	if err != nil || durable || subject != "agent.ops2.broadcast" {
		t.Fatalf("broadcast ws = %q, %v, %v", subject, durable, err)
	}
}
