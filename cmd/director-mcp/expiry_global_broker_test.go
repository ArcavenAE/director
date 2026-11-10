package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// A seat that polls the global tier must not read an empty poll as "no mail"
// when the hub stream has aged past its read position (aae-orc-t77rr, the
// global half). The check uses only the seat's own consumer info and the
// stream's info: a cluster credential cannot list consumers (finding-006), so
// the seat user here is denied listing, as in production.

// ageGlobalStream sets the stream's max age low enough for a test to wait out.
func ageGlobalStream(t *testing.T, ctx context.Context, gjs jetstream.JetStream, name string) {
	t.Helper()
	st, err := gjs.Stream(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	cfg := st.CachedInfo().Config
	cfg.MaxAge, cfg.Duplicates = 2*time.Second, time.Second
	if _, err := gjs.UpdateStream(ctx, cfg); err != nil {
		t.Fatal(err)
	}
}

func globalWaitOut(t *testing.T, ctx context.Context, bus *Bus, raw string) map[string]any {
	t.Helper()
	res, err := toolWait(ctx, bus, json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("toolWait returned %T", res)
	}
	return m
}

func TestEmptyGlobalPollReportsMailThatMayHaveExpired(t *testing.T) {
	shortFloorList(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	admin, seat := startScratchServerWithSeat(t)
	nc, _ := provision(t, ctx, admin)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	ageGlobalStream(t, ctx, gjs, globalDirectorStream)
	gcfg := &globalConfig{Domain: "global", Cluster: "c1", Role: roleDirector}

	pubEnv(t, ctx, gjs, "global.director.inbox", "g1", "INFORM", "read before the gap")
	bus, err := connect(ctx, seat, resumeSelf, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer bus.close()
	if r, err := bus.receiveTiered(ctx, 8*time.Second); err != nil || r.Env == nil || r.Env.MessageID != "g1" {
		t.Fatalf("first read %+v %v", r, err)
	}

	// Negative control: nothing has aged out, so an empty poll carries no
	// expiry field on either tier.
	out := globalWaitOut(t, ctx, bus, `{"timeout_seconds":1}`)
	for _, k := range []string{"expired_unread", "global_expired_unread"} {
		if _, ok := out[k]; ok {
			t.Fatalf("%s present with nothing aged out: %v", k, out)
		}
	}

	// Mail arrives while the seat is away and ages out; the seat polls nothing
	// meanwhile.
	for _, id := range []string{"g2", "g3", "g4"} {
		pubEnv(t, ctx, gjs, "global.director.inbox", id, "INFORM", "aged out unread")
	}
	time.Sleep(3 * time.Second)

	out = globalWaitOut(t, ctx, bus, `{"timeout_seconds":1}`)
	note, _ := out["global_expired_unread"].(string)
	for _, want := range []string{"may have", globalDirectorStream, "sequences 2 to 4", "2s"} {
		if !strings.Contains(note, want) {
			t.Fatalf("global_expired_unread = %q, want it to contain %q (%v)", note, want, out)
		}
	}
	if _, ok := out["expired_unread"]; ok {
		t.Errorf("the local field carries the global gap: %v", out)
	}

	// The same gap is not reported twice.
	out = globalWaitOut(t, ctx, bus, `{"timeout_seconds":1}`)
	if _, ok := out["global_expired_unread"]; ok {
		t.Errorf("the same gap was reported again: %v", out)
	}

	// A wider gap is reported by the batch form too.
	pubEnv(t, ctx, gjs, "global.director.inbox", "g5", "INFORM", "aged out unread")
	time.Sleep(3 * time.Second)
	b := globalWaitOut(t, ctx, bus, `{"timeout_seconds":1,"max":5}`)
	if _, ok := b["global_expired_unread"]; !ok {
		t.Errorf("batch wait carries no global_expired_unread: %v", b)
	}
}

// A supervisor-role seat reads its own cluster's stream, and the note names it.
func TestEmptyGlobalPollNamesTheSupervisorsClusterStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, _ := provision(t, ctx, url)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	if _, err := gjs.CreateStream(ctx, jetstream.StreamConfig{Name: "GLOBAL_TO_c1", Subjects: []string{"global.c1.>"}}); err != nil {
		t.Fatal(err)
	}
	ageGlobalStream(t, ctx, gjs, "GLOBAL_TO_c1")
	gcfg := &globalConfig{Domain: "global", Cluster: "c1", Role: roleSupervisor}
	pubEnv(t, ctx, gjs, "global.c1.supervisor.inbox", "s1", "INFORM", "read before the gap")
	bus, err := connect(ctx, url, resumeSelf, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer bus.close()
	if r, err := bus.receiveTiered(ctx, 8*time.Second); err != nil || r.Env == nil || r.Env.MessageID != "s1" {
		t.Fatalf("first read %+v %v", r, err)
	}
	for _, id := range []string{"s2", "s3"} {
		pubEnv(t, ctx, gjs, "global.c1.supervisor.inbox", id, "INFORM", "aged out unread")
	}
	time.Sleep(3 * time.Second)
	out := globalWaitOut(t, ctx, bus, `{"timeout_seconds":1}`)
	note, _ := out["global_expired_unread"].(string)
	if !strings.Contains(note, "GLOBAL_TO_c1") || !strings.Contains(note, "sequences 2 to 3") {
		t.Fatalf("global_expired_unread = %q, want the cluster stream and the window 2 to 3 (%v)", note, out)
	}
}
