package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// RT5: a fresh seat with no predecessor whose only trouble is that the lookup
// for earlier durables errored starts at the stream head, not at the start of
// the stream. The gap is logged and told to the seat on its next summary, with
// the probe error and the count of earlier messages it skipped.
func TestGlobalDurableProbeErrorFreshSeatStartsAtHead(t *testing.T) {
	prev := earlierDurableProbe
	earlierDurableProbe = func(context.Context, *globalTier, string, string) (bool, error) {
		return false, fmt.Errorf("listing hub durables failed (no responders)")
	}
	t.Cleanup(func() { earlierDurableProbe = prev })
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, _ := provision(t, ctx, url)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	gcfg := &globalConfig{Domain: "global", Cluster: "c1", Role: roleDirector}
	t.Setenv("MARVEL_PREDECESSOR", "")
	t.Setenv("DIRECTOR_PREDECESSOR", "") // read before MARVEL_PREDECESSOR (localinherit.go)
	pubEnv(t, ctx, gjs, "global.director.inbox", "g0", "INFORM", "old")
	pubEnv(t, ctx, gjs, "global.director.inbox", "g1", "INFORM", "old")
	b, err := connect(ctx, url, freshSeat("probeerr-g1-0"), gcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	g, err := b.globalReady(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cfg := g.cons().CachedInfo().Config
	if cfg.DeliverPolicy != jetstream.DeliverByStartSequencePolicy || cfg.OptStartSeq != 3 {
		t.Errorf("consumer starts %v at %d, want by start sequence at 3 (head 2 plus 1)", cfg.DeliverPolicy, cfg.OptStartSeq)
	}
	pubEnv(t, ctx, gjs, "global.director.inbox", "g2", "INFORM", "new")
	if got := readIDs(t, ctx, b); fmt.Sprint(got) != "[global:g2]" {
		t.Errorf("read %v, want [global:g2]: the earlier mail is skipped, the new mail is not", got)
	}
	note := strings.Join(b.takeResumed(), "\n")
	for _, want := range []string{"no responders", "2 earlier message(s)"} {
		if !strings.Contains(note, want) {
			t.Errorf("summary note %q does not name %q", note, want)
		}
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log %q does not name %q", logs.String(), want)
		}
	}
}

// Guard (director's narrowing of Q2): only a first-generation seat with no
// predecessor starts at the head. A g2 seat with no predecessor set may be a
// respawn whose lineage was lost, so a probe error keeps the replay.
func TestGlobalDurableProbeErrorLaterGenerationKeepsTheReplay(t *testing.T) {
	prev := earlierDurableProbe
	earlierDurableProbe = func(context.Context, *globalTier, string, string) (bool, error) {
		return false, fmt.Errorf("listing hub durables failed (no responders)")
	}
	t.Cleanup(func() { earlierDurableProbe = prev })

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, _ := provision(t, ctx, url)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	gcfg := &globalConfig{Domain: "global", Cluster: "c1", Role: roleDirector}
	t.Setenv("MARVEL_PREDECESSOR", "")
	t.Setenv("DIRECTOR_PREDECESSOR", "") // read before MARVEL_PREDECESSOR (localinherit.go)
	pubEnv(t, ctx, gjs, "global.director.inbox", "g0", "INFORM", "may be a lost predecessor's unread mail")
	b, err := connect(ctx, url, freshSeat("probeerr-g2-0"), gcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	if got := readIDs(t, ctx, b); fmt.Sprint(got) != "[global:g0]" {
		t.Errorf("a g2 seat with a probe error read %v, want the replay [global:g0]", got)
	}
}
