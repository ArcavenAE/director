package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// A seat started by a shift gets a new agent id (a new generation) and names
// its predecessor in MARVEL_PREDECESSOR as "<workspace>/<name>". Its first
// global read starts after the predecessor's ack floor, so mail the
// predecessor already acked is not delivered again. A seat with no
// predecessor link, or a link that names no durable, still reads everything the
// stream holds.
func TestBrokerGlobalSuccessorResumesAfterItsPredecessorsFloor(t *testing.T) {
	pred := Sender{AgentID: "worker-old", Workspace: "ws1", Team: "ops"}
	succ := Sender{AgentID: "worker-new", Workspace: "ws1", Team: "ops"}
	for _, c := range []struct {
		name          string
		link          string // MARVEL_PREDECESSOR, "" for none
		predStaysLive bool   // the predecessor still holds its presence row at the successor's start
		want          []string
	}{
		{"predecessor exited", "ws1/" + pred.AgentID, false, []string{"global:g1"}},
		{"predecessor still live", "ws1/" + pred.AgentID, true, []string{"global:g1"}},
		{"no link reads everything", "", false, []string{"global:g0", "global:g1"}},
		{"link names no durable reads everything", "ws1/worker-other", false, []string{"global:g0", "global:g1"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			url := startScratchServer(t)
			nc, _ := provision(t, ctx, url)
			gjs, _ := jetstream.NewWithDomain(nc, "global")
			gcfg := &globalConfig{Domain: "global", Cluster: "c1", Role: roleDirector}

			pubEnv(t, ctx, gjs, "global.director.inbox", "g0", "INFORM", "read by the predecessor")
			b1, err := connect(ctx, url, pred, gcfg)
			if err != nil {
				t.Fatal(err)
			}
			if r, err := b1.receiveTiered(ctx, 8*time.Second); err != nil || r.Env == nil || r.Env.MessageID != "g0" {
				t.Fatalf("predecessor first read %+v %v", r, err)
			}
			if c.predStaysLive {
				if warn := b1.writeGlobalPresence(ctx, "idle"); warn != "" {
					t.Fatal(warn)
				}
				defer b1.close()
			} else {
				b1.close()
			}
			pubEnv(t, ctx, gjs, "global.director.inbox", "g1", "INFORM", "while the shift ran")

			t.Setenv("MARVEL_PREDECESSOR", c.link)
			b2, err := connect(ctx, url, succ, gcfg)
			if err != nil {
				t.Fatal(err)
			}
			defer b2.close()
			res, err := b2.receiveBatch(ctx, 3*time.Second, 5)
			if err != nil {
				t.Fatal(err)
			}
			if got := ids(res.Items); fmt.Sprint(got) != fmt.Sprint(c.want) {
				t.Errorf("successor read %v, want %v", got, c.want)
			}
		})
	}
}
