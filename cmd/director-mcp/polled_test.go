package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// A wait result names the tiers whose inbox the call read, so a caller can tell
// an empty global inbox from a global tier that was never watched (director#52).

// polledOf runs one wait and returns the result with its polled list rendered
// as JSON, so an absent field ("<absent>"), a null and an empty list differ.
func polledOf(t *testing.T, ctx context.Context, bus *Bus, args string) (map[string]any, string) {
	t.Helper()
	res, err := toolWait(ctx, bus, json.RawMessage(args))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("toolWait returned %T", res)
	}
	v, ok := m["polled"]
	if !ok {
		return m, "<absent>"
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return m, string(b)
}

const (
	polledLocal = `["local"]`
	polledBoth  = `["local","global"]`
)

func polledBus(t *testing.T, ctx context.Context, url string, gcfg *globalConfig) *Bus {
	t.Helper()
	bus, err := connect(ctx, url, Sender{AgentID: "operator", Workspace: "aae-orc", Team: "ops"}, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bus.close)
	return bus
}

func polledGlobalCfg() *globalConfig {
	return &globalConfig{Domain: "global", Cluster: "kinu", Role: roleDirector}
}

const polledOwnSubject = "agent.aae-orc.ops.operator.inbox"

func TestSilentWaitNamesTheTiersPolled(t *testing.T) {
	for name, c := range map[string]struct {
		gcfg *globalConfig
		want string
	}{
		"global tier off": {nil, polledLocal},
		"global tier on":  {polledGlobalCfg(), polledBoth},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			url := startScratchServer(t)
			provision(t, ctx, url)
			bus := polledBus(t, ctx, url, c.gcfg)
			for form, args := range map[string]string{
				"single": `{"timeout_seconds":1}`,
				"batch":  `{"timeout_seconds":1,"max":5}`,
			} {
				out, got := polledOf(t, ctx, bus, args)
				if _, ok := out["note"]; !ok {
					t.Fatalf("%s: setup: the wait was not silent: %v", form, out)
				}
				if got != c.want {
					t.Errorf("%s silent wait: polled = %s, want %s (%v)", form, got, c.want, out)
				}
			}
		})
	}
}

func TestHitWithTheGlobalTierOffNamesLocalOnly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	bus := polledBus(t, ctx, url, nil)
	for form, c := range map[string]struct{ id, args string }{
		"single": {"s1", `{"timeout_seconds":2}`},
		"batch":  {"b1", `{"timeout_seconds":2,"max":5}`},
	} {
		pubEnv(t, ctx, js, polledOwnSubject, c.id, "INFORM", "hello")
		out, got := polledOf(t, ctx, bus, c.args)
		if _, silent := out["note"]; silent {
			t.Fatalf("%s: setup: the wait was silent: %v", form, out)
		}
		if got != polledLocal {
			t.Errorf("%s hit: polled = %s, want %s (%v)", form, got, polledLocal, out)
		}
	}
}

// The single form stops at the first message. Which tier it looks at first
// alternates, so of two local hits one never reaches the global inbox and must
// not claim it, and the other reads the empty global inbox first and says so.
func TestSingleHitNamesOnlyTheTiersItRead(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	bus := polledBus(t, ctx, url, polledGlobalCfg())
	pubEnv(t, ctx, js, polledOwnSubject, "h1", "INFORM", "one")
	pubEnv(t, ctx, js, polledOwnSubject, "h2", "INFORM", "two")
	seen := map[string]int{}
	for i := 0; i < 2; i++ {
		out, got := polledOf(t, ctx, bus, `{"timeout_seconds":2}`)
		if out["tier"] != "local" {
			t.Fatalf("setup: hit %d is not local: %v", i, out)
		}
		seen[got]++
	}
	if seen[polledLocal] != 1 || seen[polledBoth] != 1 {
		t.Errorf("two local hits: polled lists = %v, want one %s and one %s", seen, polledLocal, polledBoth)
	}
}

func TestBatchHitWithTheGlobalTierOnNamesBothTiers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	bus := polledBus(t, ctx, url, polledGlobalCfg())
	pubEnv(t, ctx, js, polledOwnSubject, "h1", "INFORM", "one")
	out, got := polledOf(t, ctx, bus, `{"timeout_seconds":2,"max":5}`)
	if out["count"] != 1 {
		t.Fatalf("setup: count = %v, want 1 (%v)", out["count"], out)
	}
	if got != polledBoth {
		t.Errorf("batch hit: polled = %s, want %s (%v)", got, polledBoth, out)
	}
}

// The global tier is configured but its inbox cannot be attached (the hub has
// no director stream), so it was never read and must not be listed.
func TestUnreachableGlobalTierIsNotListedAsPolled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	if err := js.DeleteStream(ctx, "AGENT_INBOX"); err != nil {
		t.Fatal(err)
	}
	if _, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: "AGENT_INBOX", Subjects: []string{"agent.*.*.*.inbox", "agent.*.*.role.*.inbox"}}); err != nil {
		t.Fatal(err)
	}
	if err := js.DeleteStream(ctx, globalDirectorStream); err != nil {
		t.Fatal(err)
	}
	bus := polledBus(t, ctx, url, polledGlobalCfg())
	for form, args := range map[string]string{
		"single": `{"timeout_seconds":1}`,
		"batch":  `{"timeout_seconds":1,"max":5}`,
	} {
		out, got := polledOf(t, ctx, bus, args)
		if w, _ := out["global_warning"].(string); w == "" {
			t.Fatalf("%s: setup: the global tier reported no warning: %v", form, out)
		}
		if got != polledLocal {
			t.Errorf("%s with the global tier unreachable: polled = %s, want %s (%v)", form, got, polledLocal, out)
		}
	}
}

// A global inbox that comes up while the call blocks is read by the blocking
// poll, not by the opening look, and is listed.
func TestGlobalTierThatComesUpDuringTheWaitIsListed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	cfg := jetstream.StreamConfig{Name: globalDirectorStream, Subjects: []string{"global.director.>"}}
	if err := js.DeleteStream(ctx, globalDirectorStream); err != nil {
		t.Fatal(err)
	}
	bus := polledBus(t, ctx, url, polledGlobalCfg())
	created := make(chan error, 1)
	go func() {
		time.Sleep(time.Second)
		_, err := js.CreateStream(ctx, cfg)
		created <- err
	}()
	// Longer than one tier slice, so the global inbox gets a blocking poll.
	out, got := polledOf(t, ctx, bus, `{"timeout_seconds":7}`)
	if err := <-created; err != nil {
		t.Fatal(err)
	}
	if got != polledBoth {
		t.Errorf("global tier up mid-wait: polled = %s, want %s (%v)", got, polledBoth, out)
	}
}

// Returned mail comes from the shim's own queue: no inbox is read for it.
func TestReturnedMailNamesNoTierPolled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	provision(t, ctx, url)
	bus := polledBus(t, ctx, url, polledGlobalCfg())
	for form, args := range map[string]string{
		"single": `{"timeout_seconds":1}`,
		"batch":  `{"timeout_seconds":1,"max":5}`,
	} {
		bus.returnMail([]drained{{Env: testEnv("r-"+form, "sender", "INFORM", "again"), Tier: "local", Seq: 1}}, "test", t.Logf)
		out, got := polledOf(t, ctx, bus, args)
		if _, ok := out["returned_note"]; !ok {
			t.Fatalf("%s: setup: not a returned result: %v", form, out)
		}
		if got != `[]` {
			t.Errorf("%s returned mail: polled = %s, want [] (%v)", form, got, out)
		}
	}
}
