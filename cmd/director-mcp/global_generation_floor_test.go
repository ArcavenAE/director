package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// A seat respawned without a predecessor link gets a new generation in its
// agent id (<team>-<role>-g<N>-<I>). With no floor of its own and no link, its
// first global read used to start at the oldest message the stream holds. It
// now starts after the highest ack floor of a departed durable of the same
// team, role and replica from an earlier generation, on the same cluster.
func TestBrokerGlobalNewGenerationResumesAfterAnEarlierGenerationsFloor(t *testing.T) {
	for _, c := range []struct {
		name        string
		pred        string // the earlier generation's agent id
		predCluster string
		predLive    bool
		self        string
		want        []string
	}{
		{"earlier generation, same replica", "ops-supervisor-g1-0", "c1", false, "ops-supervisor-g2-0", []string{"global:g1"}},
		{"earlier generation still live", "ops-supervisor-g1-0", "c1", true, "ops-supervisor-g2-0", []string{"global:g0", "global:g1"}},
		{"another replica's floor is not taken", "ops-supervisor-g1-1", "c1", false, "ops-supervisor-g2-0", []string{"global:g0", "global:g1"}},
		{"a later generation is not a predecessor", "ops-supervisor-g3-0", "c1", false, "ops-supervisor-g2-0", []string{"global:g0", "global:g1"}},
		{"another role is not a predecessor", "ops-builder-g1-0", "c1", false, "ops-supervisor-g2-0", []string{"global:g0", "global:g1"}},
		{"a durable on another cluster is not taken", "ops-supervisor-g1-0", "c2", false, "ops-supervisor-g2-0", []string{"global:g0", "global:g1"}},
		{"an id with no generation is left alone", "ops-supervisor", "c1", false, "ops-supervisor-g2-0", []string{"global:g0", "global:g1"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			url := startScratchServer(t)
			nc, _ := provision(t, ctx, url)
			gjs, _ := jetstream.NewWithDomain(nc, "global")
			for _, cl := range []string{"c1", "c2"} {
				if _, err := gjs.CreateStream(ctx, jetstream.StreamConfig{Name: globalStreamPrefix + cl, Subjects: []string{"global." + cl + ".>"}}); err != nil {
					t.Fatal(err)
				}
			}
			predCfg := &globalConfig{Domain: "global", Cluster: c.predCluster, Role: "supervisor"}
			selfCfg := &globalConfig{Domain: "global", Cluster: "c1", Role: "supervisor"}

			pubEnv(t, ctx, gjs, "global."+c.predCluster+".supervisor.inbox", "g0", "INFORM", "read by the earlier generation")
			if c.predCluster != "c1" {
				pubEnv(t, ctx, gjs, "global.c1.supervisor.inbox", "g0", "INFORM", "read by the earlier generation")
			}
			b1, err := connect(ctx, url, Sender{AgentID: c.pred, Workspace: "ws1", Team: "ops"}, predCfg)
			if err != nil {
				t.Fatal(err)
			}
			if r, err := b1.receiveTiered(ctx, 8*time.Second); err != nil || r.Env == nil || r.Env.MessageID != "g0" {
				t.Fatalf("earlier generation first read %+v %v", r, err)
			}
			if c.predLive {
				if warn := b1.writeGlobalPresence(ctx, "idle"); warn != "" {
					t.Fatal(warn)
				}
				defer b1.close()
			} else {
				b1.close()
			}
			pubEnv(t, ctx, gjs, "global.c1.supervisor.inbox", "g1", "INFORM", "while the seat was down")

			t.Setenv("MARVEL_PREDECESSOR", "")
			b2, err := connect(ctx, url, Sender{AgentID: c.self, Workspace: "ws1", Team: "ops"}, selfCfg)
			if err != nil {
				t.Fatal(err)
			}
			defer b2.close()
			res, err := b2.receiveBatch(ctx, 3*time.Second, 5)
			if err != nil {
				t.Fatal(err)
			}
			if got := ids(res.Items); fmt.Sprint(got) != fmt.Sprint(c.want) {
				t.Errorf("new generation read %v, want %v", got, c.want)
			}
		})
	}
}
