package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Role mail publishes to agent.<ws>.<team>.role.<role>.inbox. Before this
// change no session filtered that subject, so a role send was accepted and
// never read (20 messages over about 21 hours in dtu, 2026-09-25).

func roleBus(t *testing.T, ctx context.Context, url, id, role string) *Bus {
	t.Helper()
	b, err := connect(ctx, url, Sender{AgentID: id, Role: role, Workspace: "aae-orc", Team: "ops"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.close)
	if err := b.writePresence(ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	return b
}

func sendRole(ctx context.Context, from *Bus, addr, id, wsHint string) error {
	e := testEnv(id, from.self.AgentID, "INFORM", "to a role")
	e.Recipient.Address = addr
	_, err := from.publish(ctx, e, wsHint)
	return err
}

func TestBrokerRoleMailReachesTheHolder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	provision(t, ctx, url)
	holder := roleBus(t, ctx, url, "rev-1", "reviewer")
	sender := roleBus(t, ctx, url, "sender", "")

	if err := sendRole(ctx, sender, "role://ops/reviewer", "r1", ""); err != nil {
		t.Fatal(err)
	}
	e, _, err := holder.receive(ctx, 3*time.Second)
	if err != nil || e == nil || e.MessageID != "r1" {
		t.Fatalf("holder got %v, %v; want r1", e, err)
	}
	// Its own agent mail still arrives on the same durable.
	if err := sendRole(ctx, sender, "agent://ops/rev-1", "a1", ""); err != nil {
		t.Fatal(err)
	}
	e, _, err = holder.receive(ctx, 3*time.Second)
	if err != nil || e == nil || e.MessageID != "a1" {
		t.Fatalf("holder agent mail %v, %v; want a1", e, err)
	}
}

// The replica rule: every live holder of a role gets its own copy. A role
// send is fan-out to the holders, not a work queue.
func TestBrokerEveryRoleHolderGetsACopy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	provision(t, ctx, url)
	h1 := roleBus(t, ctx, url, "rev-1", "reviewer")
	h2 := roleBus(t, ctx, url, "rev-2", "reviewer")
	other := roleBus(t, ctx, url, "bld-1", "builder")
	sender := roleBus(t, ctx, url, "sender", "")

	if err := sendRole(ctx, sender, "role://ops/reviewer", "r1", ""); err != nil {
		t.Fatal(err)
	}
	for i, h := range []*Bus{h1, h2} {
		e, _, err := h.receive(ctx, 3*time.Second)
		if err != nil || e == nil || e.MessageID != "r1" {
			t.Errorf("holder %d got %v, %v; want r1", i+1, e, err)
		}
	}
	if e, _, _ := other.receive(ctx, time.Second); e != nil {
		t.Errorf("a builder received reviewer mail %s", e.MessageID)
	}
}

// A send to a role nobody live holds is refused before publish, like a send
// to an agent with no live presence. An explicit workspace still addresses
// the role's mailbox verbatim, as it does a cold agent mailbox.
func TestBrokerRoleSendWithNoHolderIsRefused(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	provision(t, ctx, url)
	roleBus(t, ctx, url, "bld-1", "builder")
	sender := roleBus(t, ctx, url, "sender", "")

	err := sendRole(ctx, sender, "role://ops/reviewer", "r1", "")
	if err == nil || !strings.Contains(err.Error(), "holds role") {
		t.Fatalf("send to a role nobody holds = %v; want a refusal naming the role", err)
	}
	if err := sendRole(ctx, sender, "role://ops/reviewer", "r2", "aae-orc"); err != nil {
		t.Errorf("explicit workspace to a cold role mailbox refused: %v", err)
	}
}

func TestRoleLeverIsValidated(t *testing.T) {
	base := Sender{AgentID: "a", Workspace: "w", Team: "t"}
	if err := validateIdentity(base); err != nil {
		t.Errorf("no role must stay valid: %v", err)
	}
	base.Role = "reviewer"
	if err := validateIdentity(base); err != nil {
		t.Errorf("role reviewer refused: %v", err)
	}
	base.Role = "rev.iewer"
	if err := validateIdentity(base); err == nil {
		t.Errorf("role with a dot accepted")
	}
}

// A reconnect of a role holder resumes after the seat's last ack on its role
// inbox too, so role mail already read is not replayed (the #84 rule applied
// to the two-subject durable). The departed holder wrote no presence row, as a
// session that has exited; the sends name the workspace, as to a cold mailbox.
func TestBrokerRoleMailIsNotReplayedAfterReconnect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	provision(t, ctx, url)
	sender := roleBus(t, ctx, url, "sender", "")
	self := Sender{AgentID: "rev-1", Role: "reviewer", Workspace: "aae-orc", Team: "ops"}

	h1, err := connect(ctx, url, self, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := sendRole(ctx, sender, "role://ops/reviewer", "r1", "aae-orc"); err != nil {
		t.Fatal(err)
	}
	if err := sendRole(ctx, sender, "agent://ops/rev-1", "a1", "aae-orc"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"r1", "a1"} {
		if e, _, err := h1.receive(ctx, 3*time.Second); err != nil || e == nil || e.MessageID != want {
			t.Fatalf("first holder got %v, %v; want %s", e, err, want)
		}
	}
	h1.close()
	if err := sendRole(ctx, sender, "role://ops/reviewer", "r2", "aae-orc"); err != nil {
		t.Fatal(err)
	}

	h2, err := connect(ctx, url, self, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer h2.close()
	e, _, err := h2.receive(ctx, 3*time.Second)
	if err != nil || e == nil || e.MessageID != "r2" {
		t.Fatalf("reconnected holder got %v, %v; want r2 with no replay of r1 or a1", e, err)
	}
	if e, _, _ := h2.receive(ctx, time.Second); e != nil {
		t.Errorf("reconnected holder got a further message %s", e.MessageID)
	}
}

// A seat that takes a role it did not hold before reads from the start: the
// departed durable read only the agent inbox, so its position says nothing
// about the role inbox. A replay, never a skip.
func TestBrokerTakingARoleDoesNotInheritTheAgentOnlyFloor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	provision(t, ctx, url)
	sender := roleBus(t, ctx, url, "sender", "")
	plain := Sender{AgentID: "rev-1", Workspace: "aae-orc", Team: "ops"}

	h1, err := connect(ctx, url, plain, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := sendRole(ctx, sender, "role://ops/reviewer", "r1", "aae-orc"); err != nil {
		t.Fatal(err)
	}
	if err := sendRole(ctx, sender, "agent://ops/rev-1", "a1", "aae-orc"); err != nil {
		t.Fatal(err)
	}
	if e, _, err := h1.receive(ctx, 3*time.Second); err != nil || e == nil || e.MessageID != "a1" {
		t.Fatalf("agent-only session got %v, %v; want a1", e, err)
	}
	h1.close()

	withRole := plain
	withRole.Role = "reviewer"
	h2, err := connect(ctx, url, withRole, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer h2.close()
	var got []string
	for {
		e, _, _ := h2.receive(ctx, time.Second)
		if e == nil {
			break
		}
		got = append(got, e.MessageID)
	}
	if strings.Join(got, ",") != "r1,a1" {
		t.Errorf("session that took the role read %v, want [r1 a1] (r1 was never read by this seat)", got)
	}
}
