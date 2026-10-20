package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// The unread reader's header says it creates no consumer. kv.Keys opens a
// watcher, which is an ephemeral consumer on the bucket's stream, so the
// check watches the broker's consumer-created advisories for the bucket
// while the report is built. A count taken afterwards would miss it: the
// watcher is gone by then.
func TestReadUnreadCreatesNoConsumerOnPresenceBucket(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, js := provision(t, ctx, url)
	b, err := connect(ctx, url, Sender{AgentID: "a", Workspace: "w", Team: "t"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	if err := b.writePresence(ctx, "idle"); err != nil {
		t.Fatal(err)
	}

	var created atomic.Int32
	sub, err := nc.Subscribe("$JS.EVENT.ADVISORY.CONSUMER.CREATED.KV_AGENT_STATE.*", func(*nats.Msg) { created.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}

	r, err := readUnread(ctx, js, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // advisories are published after the create returns
	if n := created.Load(); n != 0 {
		t.Fatalf("readUnread created %d consumer(s) on KV_AGENT_STATE, its header says none", n)
	}
	if len(r.Warnings) != 0 {
		t.Fatalf("warnings: %v", r.Warnings)
	}
}
