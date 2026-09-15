package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The global tier's derivations are pure: an address and a configuration
// determine the subject, the stream, the durable, and the presence key with no
// broker in the loop. These pin them, because every one of them is a place a
// wrong string would be accepted for delivery and never read.

func TestGlobalConfigRefusesAnythingButTheTwoRoles(t *testing.T) {
	for _, role := range []string{"worker", "", "Supervisor", "director ", "builder"} {
		cfg := globalConfig{Domain: "global", Cluster: "mokuzai", Role: role}
		err := cfg.validate()
		if err == nil {
			t.Errorf("role %q was accepted; exactly two global roles exist (R-94)", role)
			continue
		}
		if !strings.Contains(err.Error(), "supervisor") || !strings.Contains(err.Error(), "director") {
			t.Errorf("refusal for %q should list both legal roles: %v", role, err)
		}
	}
	for _, role := range []string{roleSupervisor, roleDirector} {
		cfg := globalConfig{Domain: "global", Cluster: "mokuzai", Role: role}
		if err := cfg.validate(); err != nil {
			t.Errorf("role %q should be accepted: %v", role, err)
		}
	}
}

func TestGlobalConfigRequiresClusterAndSubjectTokens(t *testing.T) {
	if err := (&globalConfig{Domain: "global", Role: roleSupervisor}).validate(); err == nil {
		t.Error("an empty cluster was accepted; the cluster is the session's namespace at the global tier (R-94)")
	}
	// A cluster or a domain outside the identity class would add subject
	// tokens or a wildcard: rejected, never rewritten (R-76).
	for _, cluster := range []string{"my.cluster", "*", ">", "mo kuzai"} {
		if err := (&globalConfig{Domain: "global", Cluster: cluster, Role: roleSupervisor}).validate(); err == nil {
			t.Errorf("cluster %q built a global namespace, want rejection", cluster)
		}
	}
	for _, domain := range []string{"glo.bal", "*", "glo bal"} {
		if err := (&globalConfig{Domain: domain, Cluster: "mokuzai", Role: roleSupervisor}).validate(); err == nil {
			t.Errorf("domain %q was accepted, want rejection: it becomes a token in $JS.<domain>.API.>", domain)
		}
	}
}

func TestGlobalConfigDerivesSupervisorSurface(t *testing.T) {
	cfg := globalConfig{Domain: "global", Cluster: "mokuzai", Role: roleSupervisor}
	if got, want := cfg.inboxSubject(), "global.mokuzai.supervisor.inbox"; got != want {
		t.Errorf("inbox subject = %q, want %q", got, want)
	}
	if got, want := cfg.streamName(), "GLOBAL_TO_mokuzai"; got != want {
		t.Errorf("stream = %q, want %q", got, want)
	}
	if got, want := cfg.selfAddress(), "global://mokuzai/supervisor"; got != want {
		t.Errorf("self address = %q, want %q", got, want)
	}
	if got, want := cfg.presenceKey("01ABC"), "presence.mokuzai.supervisor.01ABC"; got != want {
		t.Errorf("presence key = %q, want %q", got, want)
	}
}

func TestGlobalConfigDerivesDirectorSurface(t *testing.T) {
	// The director is one seat for the fleet: no cluster token in its subject,
	// its stream, its address, or its presence key, even though it is cast
	// with a cluster like every other session.
	cfg := globalConfig{Domain: "global", Cluster: "kinu", Role: roleDirector}
	if got, want := cfg.inboxSubject(), "global.director.inbox"; got != want {
		t.Errorf("inbox subject = %q, want %q", got, want)
	}
	if got, want := cfg.streamName(), "GLOBAL_TO_DIRECTOR"; got != want {
		t.Errorf("stream = %q, want %q", got, want)
	}
	if got, want := cfg.selfAddress(), "global://director"; got != want {
		t.Errorf("self address = %q, want %q", got, want)
	}
	if got, want := cfg.presenceKey("01ABC"), "presence.director.01ABC"; got != want {
		t.Errorf("presence key = %q, want %q", got, want)
	}
}

func TestParseGlobalAddressAcceptsTheTwoForms(t *testing.T) {
	cluster, role, err := parseGlobalAddress("global://director")
	if err != nil || cluster != "" || role != roleDirector {
		t.Fatalf("global://director = %q, %q, %v", cluster, role, err)
	}
	cluster, role, err = parseGlobalAddress("global://mokuzai/supervisor")
	if err != nil || cluster != "mokuzai" || role != roleSupervisor {
		t.Fatalf("global://mokuzai/supervisor = %q, %q, %v", cluster, role, err)
	}
}

func TestParseGlobalAddressRefusesEverythingElse(t *testing.T) {
	// Each of these would otherwise become a subject: a cluster with no role,
	// a per-cluster director (there is one seat, R-94), a worker at the global
	// tier (never), a deeper path, and a cluster outside the identity class.
	for _, addr := range []string{
		"global://",
		"global://mokuzai",
		"global://mokuzai/director",
		"global://mokuzai/worker",
		"global://mokuzai/supervisor/extra",
		"global://mo.kuzai/supervisor",
		"global://*/supervisor",
		"agent://fleet/builder",
	} {
		if _, _, err := parseGlobalAddress(addr); err == nil {
			t.Errorf("address %q was routed, want refusal", addr)
		}
	}
}

// The load-bearing invariant of the R-92 liveness refusal: the prefix a SENDER
// reads to decide whether anyone is alive must be a prefix of the key the
// addressed session WRITES. If these two derivations ever drift, every global
// send to a live session refuses, or worse, every send to a dead one is
// accepted.
func TestPresencePrefixMatchesTheKeyTheAddressedSessionWrites(t *testing.T) {
	cases := []struct {
		addr string
		cfg  globalConfig
	}{
		{"global://mokuzai/supervisor", globalConfig{Domain: "global", Cluster: "mokuzai", Role: roleSupervisor}},
		{"global://director", globalConfig{Domain: "global", Cluster: "kinu", Role: roleDirector}},
	}
	for _, c := range cases {
		cluster, role, err := parseGlobalAddress(c.addr)
		if err != nil {
			t.Fatalf("parse %q: %v", c.addr, err)
		}
		prefix := globalPresencePrefix(cluster, role)
		key := c.cfg.presenceKey("01INSTANCE")
		if !strings.HasPrefix(key, prefix) {
			t.Errorf("%s: sender reads prefix %q, recipient writes key %q; a live session would read as dead", c.addr, prefix, key)
		}
		if got, want := globalSubject(cluster, role), c.cfg.inboxSubject(); got != want {
			t.Errorf("%s: sender publishes %q, recipient consumes %q", c.addr, got, want)
		}
	}
}

func TestGlobalDurableIsPerSession(t *testing.T) {
	a := globalDurable("fleet-supervisor-g1-0", "01AAA")
	b := globalDurable("fleet-supervisor-g1-0", "01BBB")
	if a == b {
		t.Fatal("two sessions under one id share a global durable; they would race each other's mail (R-50)")
	}
	if !strings.HasPrefix(a, "mcp_global_") {
		t.Errorf("durable %q should be recognisable as a shim global consumer", a)
	}
}

func TestNoGlobalPresenceRefusalNamesTheRule(t *testing.T) {
	err := noGlobalPresenceErr("global://mokuzai/supervisor")
	if !strings.Contains(err.Error(), "R-92") || !strings.Contains(err.Error(), "no session would consume") {
		t.Fatalf("the liveness refusal should name its cause and R-92: %v", err)
	}
}

// The envelope gains the global forms without a schema change: version stays 1
// and recipient.team stays empty, because a global subject carries a cluster
// and a role and no team.
func TestEnvelopeAcceptsGlobalAddressWithNoTeam(t *testing.T) {
	self := Sender{AgentID: "fleet-supervisor-g1-0", Team: "fleet", Workspace: "ops2"}
	raw := json.RawMessage(`{"to":"global://director","performative":"INFORM","text":"receipt"}`)
	e, _, err := buildSendEnvelope(self, raw)
	if err != nil {
		t.Fatalf("a global address should validate: %v", err)
	}
	if e.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1: the global forms are an address change, not a schema change", e.SchemaVersion)
	}
	if e.Recipient.Team != "" {
		t.Errorf("recipient.team = %q, want empty for a global address", e.Recipient.Team)
	}
}

func TestEnvelopeStillRefusesAnUnknownScheme(t *testing.T) {
	self := Sender{AgentID: "builder", Team: "fleet", Workspace: "ops2"}
	raw := json.RawMessage(`{"to":"globalish://director","performative":"INFORM","text":"x"}`)
	if _, _, err := buildSendEnvelope(self, raw); err == nil {
		t.Fatal("an unknown address scheme was accepted")
	}
}

// The tiered poll divides its budget into slices so neither tier waits out the
// other's whole share. The budget must run out: a slice that returned zero, or
// kept returning true past the deadline, would spin on the broker.
func TestPollSliceSpendsTheBudgetAndStops(t *testing.T) {
	deadline := time.Now().Add(12 * time.Second)
	slice, ok := sliceLeft(deadline)
	if !ok || slice != tierPollSlice {
		t.Fatalf("a full budget should yield a whole slice, got %v, %v", slice, ok)
	}
	short, ok := sliceLeft(time.Now().Add(500 * time.Millisecond))
	if !ok || short <= 0 || short > tierPollSlice {
		t.Fatalf("a short remainder should yield what is left, got %v, %v", short, ok)
	}
	if _, ok := sliceLeft(time.Now().Add(-time.Second)); ok {
		t.Fatal("a spent budget should stop the poll, not hand out another slice")
	}
}

// With the global tier off the catalog must not advertise addresses the
// session cannot route; with it on, it must name the session's own address so
// a receiver knows how to be answered.
func TestToolCatalogDescribesGlobalOnlyWhenOn(t *testing.T) {
	off := toolCatalog(nil)
	for _, td := range off {
		if strings.Contains(td.Description, "global://") {
			t.Errorf("tool %q advertises a global address with the tier off", td.Name)
		}
	}
	on := toolCatalog(&globalConfig{Domain: "global", Cluster: "mokuzai", Role: roleSupervisor})
	var send toolDef
	for _, td := range on {
		if td.Name == "send_message" {
			send = td
		}
	}
	if !strings.Contains(send.Description, "global://mokuzai/supervisor") {
		t.Errorf("send_message should name this session's global address: %q", send.Description)
	}
}
