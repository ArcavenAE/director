package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// A resumed global inbox names where its floor came from, so a successor can
// tell the backlog it inherited from its predecessor from its own.

func TestResumeNoteNamesTheFloorSource(t *testing.T) {
	pred := resumeNote("global", 8305, 2763, "", floorSource{pred: "team-g1-0"})
	for _, s := range []string{"global inbox resumed after stream sequence 8305", "predecessor team-g1-0", "2763 message(s) waiting"} {
		if !strings.Contains(pred, s) {
			t.Errorf("predecessor note %q lacks %q", pred, s)
		}
	}
	if strings.Contains(pred, "the seat's last ack") {
		t.Errorf("predecessor note still says the seat's last ack: %q", pred)
	}
	own := resumeNote("global", 8305, 4, "", floorSource{own: true})
	if !strings.Contains(own, "own earlier durable") || strings.Contains(own, "predecessor") {
		t.Errorf("own-durable note: %q", own)
	}
	// No source (the local tier, or a floor with none recorded): the wording is
	// what it was.
	if got := resumeNote("local", 219, 0, "", floorSource{}); !strings.Contains(got, "the seat's last ack") {
		t.Errorf("unsourced note changed: %q", got)
	}
	// No floor: unchanged, whatever the source field holds.
	if got := resumeNote("global", 0, 2, "", floorSource{}); !strings.Contains(got, "no earlier durable") {
		t.Errorf("no-floor note changed: %q", got)
	}
}

// resumeNotes attaches the global tier with one read, then returns the start
// notes the bus holds.
func resumeNotes(t *testing.T, ctx context.Context, b *Bus) string {
	t.Helper()
	if _, err := b.receiveBatch(ctx, 3*time.Second, 5); err != nil {
		t.Fatal(err)
	}
	return strings.Join(b.takeResumed(), "\n")
}

func TestBrokerResumeNoteNamesThePredecessor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, _ := provision(t, ctx, url)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	gcfg := &globalConfig{Domain: "global", Cluster: "c1", Role: roleDirector}
	pred := Sender{AgentID: "worker-old", Workspace: "ws1", Team: "ops"}
	succ := Sender{AgentID: "worker-new", Workspace: "ws1", Team: "ops"}

	pubEnv(t, ctx, gjs, "global.director.inbox", "g0", "INFORM", "read by the predecessor")
	b1, err := connect(ctx, url, pred, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := b1.receiveTiered(ctx, 8*time.Second); err != nil || r.Env == nil {
		t.Fatalf("predecessor first read %+v %v", r, err)
	}
	b1.close()

	t.Setenv("MARVEL_PREDECESSOR", "ws1/"+pred.AgentID)
	b2, err := connect(ctx, url, succ, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b2.close()
	if got := resumeNotes(t, ctx, b2); !strings.Contains(got, "predecessor worker-old") || strings.Contains(got, "own earlier durable") {
		t.Errorf("successor note: %q", got)
	}
}

func TestBrokerResumeNoteNamesTheSeatsOwnDurable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, _ := provision(t, ctx, url)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	gcfg := &globalConfig{Domain: "global", Cluster: "c1", Role: roleDirector}
	self := Sender{AgentID: "worker-same", Workspace: "ws1", Team: "ops"}

	pubEnv(t, ctx, gjs, "global.director.inbox", "g0", "INFORM", "read before the restart")
	b1, err := connect(ctx, url, self, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := b1.receiveTiered(ctx, 8*time.Second); err != nil || r.Env == nil {
		t.Fatalf("first read %+v %v", r, err)
	}
	b1.close()

	t.Setenv("MARVEL_PREDECESSOR", "")
	b2, err := connect(ctx, url, self, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b2.close()
	if got := resumeNotes(t, ctx, b2); !strings.Contains(got, "own earlier durable") || strings.Contains(got, "predecessor") {
		t.Errorf("restart note: %q", got)
	}
}

func TestBrokerResumeNoteWithNoFloorIsUnchanged(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	provision(t, ctx, url)
	gcfg := &globalConfig{Domain: "global", Cluster: "c1", Role: roleDirector}
	t.Setenv("MARVEL_PREDECESSOR", "ws1/worker-other") // names no durable
	b, err := connect(ctx, url, Sender{AgentID: "worker-fresh", Workspace: "ws1", Team: "ops"}, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	if got := resumeNotes(t, ctx, b); !strings.Contains(got, "no earlier durable") {
		t.Errorf("fresh seat note: %q", got)
	}
}
