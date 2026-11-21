package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// The global presence row carries stands_in_for while the seat has one set
// (director#729). The local row is covered in standsin_test.go. Coverage of
// behaviour that already works; it does not fail before a fix.

func TestGlobalPresenceRowCarriesStandsInFor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, _ := provision(t, ctx, url)
	// A supervisor's global tier opens a durable on GLOBAL_TO_<cluster>, which
	// provision does not create.
	gjs, err := jetstream.NewWithDomain(nc, "global")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gjs.CreateStream(ctx, jetstream.StreamConfig{Name: "GLOBAL_TO_kinu", Subjects: []string{"global.kinu.>"}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ id, standsIn string }{{"mirror", "cue"}, {"plain", ""}} {
		b, err := connect(ctx, url, Sender{AgentID: tc.id, Workspace: "aae-orc", Team: "ops", StandsIn: tc.standsIn},
			&globalConfig{Domain: "global", Cluster: "kinu", Role: "supervisor"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(b.close)
		g, err := b.globalReady(ctx)
		if err != nil {
			t.Fatalf("setup: the global tier is not attached: %v", err)
		}
		if err := g.writePresence(ctx, b.self, b.instance, b.pid, "idle"); err != nil {
			t.Fatal(err)
		}
		entry, err := g.kv.Get(ctx, g.cfg.presenceKey(b.instance))
		if err != nil {
			t.Fatal(err)
		}
		var rec map[string]any
		if err := json.Unmarshal(entry.Value(), &rec); err != nil {
			t.Fatal(err)
		}
		got, has := rec["stands_in_for"]
		if tc.standsIn != "" && got != tc.standsIn {
			t.Errorf("%s: stands_in_for = %v, want %q", tc.id, got, tc.standsIn)
		}
		if tc.standsIn == "" && has {
			t.Errorf("%s: stands_in_for = %v, want no field when unset", tc.id, got)
		}
	}
}
