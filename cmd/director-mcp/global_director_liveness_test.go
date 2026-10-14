package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// R-78: a send to an address with two present instances fails loud or fans out
// explicitly, never picks one. The director role has one instance behind
// role://director, so a second live director refuses the send, naming both. The
// liveness count must also count only the director's own rows: a cluster named
// "director" would put its supervisor's row under the same prefix.

func newDirectorRig(t *testing.T) (context.Context, *globalTier, jetstream.KeyValue, jetstream.JetStream) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	url := startScratchServer(t)
	nc, _ := provision(t, ctx, url)
	gjs, err := jetstream.NewWithDomain(nc, "global")
	if err != nil {
		t.Fatal(err)
	}
	kv, err := gjs.KeyValue(ctx, globalPresenceBucket)
	if err != nil {
		t.Fatal(err)
	}
	g := &globalTier{cfg: globalConfig{Domain: "global", Cluster: "c1", Role: roleSupervisor}, js: gjs, kv: kv}
	return ctx, g, kv, gjs
}

func putRow(t *testing.T, ctx context.Context, kv jetstream.KeyValue, key, body string) {
	t.Helper()
	if _, err := kv.Put(ctx, key, []byte(body)); err != nil {
		t.Fatal(err)
	}
}

func directorRow(agent, instance string) string {
	return `{"cluster":"c1","role":"director","agent_id":"` + agent + `","instance":"` + instance + `","state":"idle"}`
}

func sendTo(t *testing.T, ctx context.Context, g *globalTier, addr, id string) error {
	t.Helper()
	e := &Envelope{MessageID: id, Recipient: Recipient{Address: addr}}
	_, err := g.publish(ctx, e, []byte(`{}`))
	return err
}

func storedDirectorMessages(t *testing.T, ctx context.Context, gjs jetstream.JetStream) uint64 {
	t.Helper()
	st, err := gjs.Stream(ctx, globalDirectorStream)
	if err != nil {
		t.Fatal(err)
	}
	info, err := st.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return info.State.Msgs
}

// Two live director rows: the send is refused and names each instance, through
// both address forms, and nothing is stored.
func TestTwoLiveDirectorsRefuseTheSendNamingBothThroughBothAddressForms(t *testing.T) {
	ctx, g, kv, gjs := newDirectorRig(t)
	putRow(t, ctx, kv, "presence.director.01AAAAAAAAAAAAAAAAAAAAAAAA", directorRow("director", "01AAAAAAAAAAAAAAAAAAAAAAAA"))
	putRow(t, ctx, kv, "presence.director.01BBBBBBBBBBBBBBBBBBBBBBBB", directorRow("director-2", "01BBBBBBBBBBBBBBBBBBBBBBBB"))
	for _, addr := range []string{"global://director", "role://director"} {
		err := sendTo(t, ctx, g, addr, "m-"+addr)
		if err == nil {
			t.Fatalf("%s: two live directors, the send was accepted (it would reach both durables)", addr)
		}
		for _, want := range []string{"01AAAAAAAAAAAAAAAAAAAAAAAA", "01BBBBBBBBBBBBBBBBBBBBBBBB", "R-78"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: refusal %q does not name %q", addr, err, want)
			}
		}
	}
	if n := storedDirectorMessages(t, ctx, gjs); n != 0 {
		t.Errorf("%d message(s) stored despite the refusal", n)
	}
}

// One live director plus a cluster named "director" with a supervisor row: the
// supervisor's key starts with the director prefix and must not count.
func TestASupervisorRowUnderAClusterNamedDirectorIsNotADirector(t *testing.T) {
	ctx, g, kv, _ := newDirectorRig(t)
	putRow(t, ctx, kv, "presence.director.01AAAAAAAAAAAAAAAAAAAAAAAA", directorRow("director", "01AAAAAAAAAAAAAAAAAAAAAAAA"))
	putRow(t, ctx, kv, "presence.director.supervisor.01SUPSUPSUPSUPSUPSUPSUPSU", `{"cluster":"director","role":"supervisor","agent_id":"s","instance":"01SUPSUPSUPSUPSUPSUPSUPSU"}`)
	live, _, err := g.records(ctx, globalPresencePrefix("", roleDirector))
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 1 {
		t.Fatalf("counted %d director row(s), want 1 (the supervisor row has two tokens after the prefix)", len(live))
	}
	if err := sendTo(t, ctx, g, "global://director", "m-one"); err != nil {
		t.Fatalf("one live director must still be reachable: %v", err)
	}
}

// With only the cluster-named-director supervisor row there is no director,
// and the send refuses as no live presence.
func TestOnlyASupervisorRowUnderAClusterNamedDirectorDoesNotMakeTheDirectorLive(t *testing.T) {
	ctx, g, kv, gjs := newDirectorRig(t)
	putRow(t, ctx, kv, "presence.director.supervisor.01SUPSUPSUPSUPSUPSUPSUPSU", `{"cluster":"director","role":"supervisor","agent_id":"s","instance":"01SUPSUPSUPSUPSUPSUPSUPSU"}`)
	if err := sendTo(t, ctx, g, "global://director", "m-none"); err == nil {
		t.Fatal("no director is live, the send was accepted on the strength of a supervisor row")
	}
	if n := storedDirectorMessages(t, ctx, gjs); n != 0 {
		t.Errorf("%d message(s) stored", n)
	}
}

// An unreadable row beside a readable one: whether there is exactly one live
// director is not established, so the send refuses and says so.
func TestAnUnreadableDirectorRowRefusesAsNotEstablished(t *testing.T) {
	ctx, g, kv, gjs := newDirectorRig(t)
	putRow(t, ctx, kv, "presence.director.01AAAAAAAAAAAAAAAAAAAAAAAA", directorRow("director", "01AAAAAAAAAAAAAAAAAAAAAAAA"))
	putRow(t, ctx, kv, "presence.director.01BBBBBBBBBBBBBBBBBBBBBBBB", `not json`)
	err := sendTo(t, ctx, g, "role://director", "m-unreadable")
	if err == nil {
		t.Fatal("an unreadable director row was ignored and the send accepted")
	}
	if !strings.Contains(err.Error(), "NOT established") {
		t.Errorf("refusal %q does not say it was not established", err)
	}
	if n := storedDirectorMessages(t, ctx, gjs); n != 0 {
		t.Errorf("%d message(s) stored", n)
	}
}

// Zero live directors still refuses as it does today.
func TestZeroLiveDirectorsStillRefuse(t *testing.T) {
	ctx, g, _, _ := newDirectorRig(t)
	if err := sendTo(t, ctx, g, "global://director", "m-zero"); err == nil {
		t.Fatal("no director is live, the send was accepted")
	}
}

// "director" is reserved: no cluster may take it, as a configured cluster or in
// an address.
func TestDirectorIsAReservedClusterName(t *testing.T) {
	cfg := globalConfig{Domain: "global", Cluster: "director", Role: roleSupervisor}
	err := cfg.validate()
	if err == nil {
		t.Fatal("DIRECTOR_CLUSTER=director was accepted; its supervisor key would collide with the director prefix")
	}
	if !strings.Contains(err.Error(), "reserved") {
		t.Errorf("refusal %q does not say the name is reserved", err)
	}
	if _, _, err := parseGlobalAddress("global://director/supervisor"); err == nil {
		t.Error("global://director/supervisor was accepted")
	} else if !strings.Contains(err.Error(), "reserved") {
		t.Errorf("address refusal %q does not say the name is reserved", err)
	}
}
