package main

import (
	"context"
	"fmt"
	"strings"
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

// When the credential cannot list the hub's consumers, the by-name read finds
// the same floor through the local durables each instance also holds.
func TestBrokerGlobalGenerationFloorByName(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, _ := provision(t, ctx, url)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	if _, err := gjs.CreateStream(ctx, jetstream.StreamConfig{Name: globalStreamPrefix + "c1", Subjects: []string{"global.c1.>"}}); err != nil {
		t.Fatal(err)
	}
	gcfg := &globalConfig{Domain: "global", Cluster: "c1", Role: "supervisor"}
	pubEnv(t, ctx, gjs, "global.c1.supervisor.inbox", "g0", "INFORM", "read by the earlier generation")
	b1, err := connect(ctx, url, Sender{AgentID: "ops-supervisor-g1-0", Workspace: "ws1", Team: "ops"}, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := b1.receiveTiered(ctx, 8*time.Second); err != nil || r.Env == nil || r.Env.MessageID != "g0" {
		t.Fatalf("earlier generation first read %+v %v", r, err)
	}
	b1.close()

	b2, err := connect(ctx, url, Sender{AgentID: "ops-supervisor-g2-0", Workspace: "ws1", Team: "ops"}, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b2.close()
	g, err := b2.globalReady(ctx)
	if err != nil {
		t.Fatal(err)
	}
	me, _ := parseGenID("ops-supervisor-g2-0")
	got, err := g.generationFloorByName(ctx, me, "self", []string{gcfg.inboxSubject()}, func(string) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("by-name floors = %v, want one durable", got)
	}
	for d, f := range got {
		if !strings.HasPrefix(d, "mcp_global_ops-supervisor-g1-0_") || f == 0 {
			t.Errorf("by-name donor %q floor %d, want the g1 durable with a floor", d, f)
		}
	}
}

// A cluster credential cannot list the hub's consumers (finding-006). A new
// generation still resumes after the earlier generation's floor, found by
// name through the local durables, and a different replica's durable is still
// not taken. The scratch seat user is denied CONSUMER.LIST and CONSUMER.NAMES
// on the hub stream, so the listing path fails and only the by-name read can
// find the floor.
func TestBrokerGlobalNewGenerationResumesWithoutListingHubConsumers(t *testing.T) {
	for _, c := range []struct {
		name string
		pred string
		want string
	}{
		{"same replica", "ops-director-g1-0", "[global:g1]"},
		{"another replica", "ops-director-g1-1", "[global:g0 global:g1]"},
	} {
		t.Run(c.name, func(t *testing.T) {
			shortFloorList(t)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			admin, seat := startScratchServerWithSeat(t)
			nc, _ := provision(t, ctx, admin)
			gjs, _ := jetstream.NewWithDomain(nc, "global")
			gcfg := &globalConfig{Domain: "global", Cluster: "c1", Role: roleDirector}
			pubEnv(t, ctx, gjs, "global.director.inbox", "g0", "INFORM", "read by the earlier generation")
			b1, err := connect(ctx, seat, Sender{AgentID: c.pred, Workspace: "ws1", Team: "ops"}, gcfg)
			if err != nil {
				t.Fatal(err)
			}
			if r, err := b1.receiveTiered(ctx, 8*time.Second); err != nil || r.Env == nil || r.Env.MessageID != "g0" {
				t.Fatalf("earlier generation first read %+v %v", r, err)
			}
			b1.close()
			pubEnv(t, ctx, gjs, "global.director.inbox", "g1", "INFORM", "while the seat was down")

			t.Setenv("MARVEL_PREDECESSOR", "")
			b2, err := connect(ctx, seat, Sender{AgentID: "ops-director-g2-0", Workspace: "ws1", Team: "ops"}, gcfg)
			if err != nil {
				t.Fatal(err)
			}
			defer b2.close()
			res, err := b2.receiveBatch(ctx, 3*time.Second, 5)
			if err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprint(ids(res.Items)); got != c.want {
				t.Errorf("new generation read %s, want %s", got, c.want)
			}
		})
	}
}
