package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
)

// The per-seat global inbox design (sim/design/global-per-seat-inbox.md,
// section 3) changes what a seat reads and how an address resolves. These pin
// the derivations as pure functions before anything is wired: nothing here
// touches a broker, and nothing in the live publish or receive path calls them.

func TestFabricAddressSubjects(t *testing.T) {
	cases := []struct {
		addr, subject string
	}{
		{"agent://kinu/ws1/team1/builder-1", "agent.kinu.ws1.team1.builder-1.inbox"},
		{"role://kinu/ws1/team1/supervisor", "agent.kinu.ws1.team1.role.supervisor.inbox"},
		{"role://mokuzai/ws1/team1/research-supervisor", "agent.mokuzai.ws1.team1.role.research-supervisor.inbox"},
		{"director", "director.inbox"},
	}
	for _, c := range cases {
		a, err := parseFabricAddress(c.addr)
		if err != nil {
			t.Errorf("%s was refused: %v", c.addr, err)
			continue
		}
		if got := a.subject(); got != c.subject {
			t.Errorf("%s: subject %q, want %q", c.addr, got, c.subject)
		}
	}
}

func TestFabricAddressRefusals(t *testing.T) {
	for _, bad := range []string{
		"",
		"agent://kinu/ws1/team1",         // too few tokens
		"agent://kinu/ws1/team1/b/extra", // too many tokens
		"agent://kinu/ws1/team1/role",    // role is reserved as a seat id
		"agent://audit/ws1/team1/b",      // audit is reserved as a cluster token
		"agent://kinu/broadcast/team1/b", // broadcast is reserved as a workspace token
		"agent://kinu/ws1/arc.aven/b",    // a dot would add a subject token
		"agent://kinu/ws1/arc*aven/b",    // a wildcard is never an identity
		"agent://kinu/ws1/team1/>",       // nor is a tail match
		"role://kinu/ws1/team1",          // a role address names a team role
		"global://kinu/supervisor",       // the legacy form is an alias, resolved elsewhere
		"director/extra",                 // the director has one address
		"agent://kinu//team1/b",          // an empty token
	} {
		if _, err := parseFabricAddress(bad); err == nil {
			t.Errorf("%q was accepted; it must be refused, not rewritten (R-76, R-78)", bad)
		}
	}
}

func TestFabricDurableIsPerAddressAndNeverPerInstance(t *testing.T) {
	a, _ := parseFabricAddress("agent://kinu/ws1/team1/builder-1")
	b, _ := parseFabricAddress("role://kinu/ws1/team1/builder-1")
	if a.durable() == "" || b.durable() == "" {
		t.Fatal("a durable name was empty")
	}
	if a.durable() != a.durable() {
		t.Error("the durable name must be a pure function of the address")
	}
	if a.durable() == b.durable() {
		t.Error("an agent address and a role address of the same words must not share a durable")
	}
	if strings.ContainsAny(a.durable(), ".*> ") {
		t.Errorf("durable %q carries a character a consumer name cannot", a.durable())
	}
	// Tokens may hold "-" and "_", so a plain join would let two different
	// addresses name one durable. The name must keep them apart.
	x, _ := parseFabricAddress("agent://a-b/c/d/e")
	y, _ := parseFabricAddress("agent://a/b-c/d/e")
	if x.durable() == y.durable() {
		t.Errorf("distinct addresses share durable %q", x.durable())
	}
}

func seat(cluster, team, id, role string) fabricSeat {
	a, _ := parseFabricAddress("agent://" + cluster + "/ws1/" + team + "/" + id)
	return fabricSeat{Cluster: cluster, Addr: a, Role: role}
}

func TestAliasResolvesToTheOneHolderOfTheRole(t *testing.T) {
	seats := []fabricSeat{
		seat("kinu", "team1", "supervisor-1", "supervisor"),
		seat("kinu", "team1", "research-1", "research-supervisor"),
		seat("kinu", "team1", "builder-1", "builder"),
		seat("mokuzai", "other", "supervisor-9", "supervisor"),
	}
	got, err := resolveSupervisorAlias("global://kinu/supervisor", seats)
	if err != nil {
		t.Fatalf("one supervisor on the cluster should resolve: %v", err)
	}
	if got.subject() != "agent.kinu.ws1.team1.supervisor-1.inbox" {
		t.Errorf("resolved to %q", got.subject())
	}
}

func TestAliasNeverMakesTheResearchSeatASupervisorCandidate(t *testing.T) {
	seats := []fabricSeat{
		seat("kinu", "team1", "supervisor-1", "supervisor"),
		seat("kinu", "team1", "research-1", "research-supervisor"),
	}
	if _, err := resolveSupervisorAlias("global://kinu/supervisor", seats); err != nil {
		t.Errorf("the research seat's cast role is research-supervisor, so it is no candidate: %v", err)
	}
}

func TestAliasRefusesWhenAmbiguousAndNamesTheCandidates(t *testing.T) {
	seats := []fabricSeat{
		seat("kinu", "team1", "supervisor-1", "supervisor"),
		seat("kinu", "second", "supervisor-2", "supervisor"),
	}
	_, err := resolveSupervisorAlias("global://kinu/supervisor", seats)
	if err == nil {
		t.Fatal("two supervisors on one cluster must refuse before publish; the alias never fans out (R-78)")
	}
	for _, want := range []string{"supervisor-1", "supervisor-2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal should name %s: %v", want, err)
		}
	}
}

func TestAliasRefusesWhenNobodyHoldsTheRole(t *testing.T) {
	_, err := resolveSupervisorAlias("global://kinu/supervisor", []fabricSeat{seat("mokuzai", "t", "s", "supervisor")})
	if err == nil {
		t.Error("no live holder on the cluster must refuse (R-92 liveness)")
	}
}

func TestAliasResolvesTheDirectorAndRefusesOtherForms(t *testing.T) {
	d, err := resolveSupervisorAlias("global://director", nil)
	if err != nil || d.subject() != "director.inbox" {
		t.Errorf("global://director should resolve to director.inbox: %v %v", d, err)
	}
	for _, bad := range []string{"global://kinu/worker", "global://kinu/supervisor/x", "agent://kinu/ws1/a/b", "global://"} {
		if _, err := resolveSupervisorAlias(bad, nil); err == nil {
			t.Errorf("%q was accepted as an alias", bad)
		}
	}
}

func TestClusterCutReadsTheRecordAndNeverTakesAFailedReadAsAbsence(t *testing.T) {
	listed := func(string) error { return nil }
	absent := func(string) error { return jetstream.ErrKeyNotFound }
	broken := func(string) error { return errors.New("nats: no responders available for request") }

	if cut, err := clusterCut(listed, "mokuzai"); err != nil || !cut {
		t.Errorf("a listed cluster is cut: cut=%v err=%v", cut, err)
	}
	if cut, err := clusterCut(absent, "mokuzai"); err != nil || cut {
		t.Errorf("key not found, which includes an empty bucket, means not listed: cut=%v err=%v", cut, err)
	}
	cut, err := clusterCut(broken, "mokuzai")
	if err == nil {
		t.Fatal("any other error must refuse, never read as not cut")
	}
	if cut {
		t.Error("a failed read must not report cut either")
	}
	if !strings.Contains(err.Error(), "no responders") {
		t.Errorf("the refusal should name the error it got: %v", err)
	}
}

func TestClusterCutAsksForTheClusterKey(t *testing.T) {
	var asked string
	_, _ = clusterCut(func(k string) error { asked = k; return nil }, "mokuzai")
	if asked != "mokuzai" {
		t.Errorf("asked for key %q, want the cluster token", asked)
	}
	if cut, err := clusterCut(func(string) error { return nil }, "bad.cluster"); err == nil || cut {
		t.Error("a cluster outside the identity class must be refused before the read")
	}
}
