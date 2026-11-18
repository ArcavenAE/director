package main

import (
	"context"
	"testing"
	"time"
)

// director#220, plan T-A2b. Three cases added after the first mutant pass, each
// for a reading of the split role durable that the first nine tests left alone.

// A role message acked later (the reply path) is credited to the role durable's
// acked counter, not the own one. With one shared maximum, a rebuilt own durable
// would start after the role message's sequence and skip own mail below it.
func TestARoleAckLaterDoesNotMoveTheOwnDurablesRestartPoint(t *testing.T) {
	old := durableCheckEvery
	durableCheckEvery = 0
	defer func() { durableCheckEvery = old }()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	holder := lineConnect(t, ctx, url, "sup", "supervisor", "")
	publishN(t, ctx, js, lineInbox("sup"), "o", 1, 1)       // stream sequence 1
	publishN(t, ctx, js, lineRole("supervisor"), "r", 1, 1) // sequence 2
	publishN(t, ctx, js, lineInbox("sup"), "o", 2, 2)       // sequence 3

	wctx, al := withAckLater(ctx)
	e, seq, err := holder.receiveOn(wctx, true, 2*time.Second)
	if err != nil || e == nil || seq != 2 {
		t.Fatalf("setup: the role read gave %v (seq %d, err %v), want the role message at sequence 2", e, seq, err)
	}
	al.ack(holder, t.Logf)

	if err := js.DeleteConsumer(ctx, "AGENT_INBOX", "mcp_sup_"+holder.instance); err != nil {
		t.Fatal(err)
	}
	res, err := holder.receiveBatch(ctx, 5*time.Second, 10)
	if err != nil {
		t.Fatal(err)
	}
	got := idSet(lineItems(t, res))
	if !got["o1"] || !got["o2"] || len(res.Items) != 2 {
		t.Errorf("after the own durable was rebuilt the holder received %q, want o1 and o2 (the role ack at sequence 2 is not the own durable's)", lineIDs(lineItems(t, res)))
	}
}

// A role message fetched and not acked is ack-pending on the role durable, and
// remaining counts it as waiting, as it does for the own durable.
func TestRemainingCountsTheRoleDurablesAckPending(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	holder := lineConnect(t, ctx, url, "sup", "supervisor", "")
	publishN(t, ctx, js, lineRole("supervisor"), "r", 1, 2)
	wctx, _ := withAckLater(ctx)
	if e, _, err := holder.receiveOn(wctx, true, 2*time.Second); err != nil || e == nil {
		t.Fatalf("setup: the role read gave %v, %v", e, err)
	}
	if rem := holder.remaining(ctx); rem["local"] != 2 {
		t.Errorf("remaining local = %d, want 2 (one role message ack-pending and one unfetched)", rem["local"])
	}
}

// A seat that has stopped polling still asks the broker whether its role durable
// exists, as it does for the own one (director#269).
func TestAHeartbeatRebuildsARoleDurableDeletedWhileTheSeatIsNotPolling(t *testing.T) {
	old := durableCheckEvery
	durableCheckEvery = 0
	defer func() { durableCheckEvery = old }()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	holder := lineConnect(t, ctx, url, "sup", "supervisor", "")
	roleName := "mcp_sup_" + holder.instance + "-role"
	if err := js.DeleteConsumer(ctx, "AGENT_INBOX", roleName); err != nil {
		t.Fatal(err)
	}
	runHeartbeat(t, ctx, holder, 40*time.Millisecond)
	if !waitFor(5*time.Second, func() bool {
		_, ok := seatDurables(t, ctx, js, "sup", holder.instance)[roleName]
		return ok
	}) {
		t.Fatal("the heartbeat did not rebuild the deleted role durable")
	}
}

// A restart does not skip own-subject mail that sits between the own durable's
// floor and the role durable's: the own subject starts at its own floor then,
// not at the seat's furthest read.
func TestARestartDoesNotSkipOwnMailBelowTheRoleDurablesFloor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	old := lineConnect(t, ctx, url, "sup", "supervisor", "")
	publishN(t, ctx, js, lineInbox("sup"), "o", 1, 1)       // sequence 1, never read
	publishN(t, ctx, js, lineRole("supervisor"), "r", 1, 2) // sequences 2 and 3
	for i := 0; i < 2; i++ {
		if e, _, err := old.receiveOn(ctx, true, 2*time.Second); err != nil || e == nil {
			t.Fatalf("setup: role read %d gave %v, %v", i, e, err)
		}
	}
	lineExit(t, ctx, old)

	fresh := lineConnect(t, ctx, url, "sup", "supervisor", "")
	res, err := fresh.receiveBatch(ctx, 3*time.Second, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := idSet(lineItems(t, res)); !got["o1"] {
		t.Errorf("the restarted seat received %q, want o1 (own mail below the role durable's floor)", lineIDs(lineItems(t, res)))
	}
}

// With two holders of one role at different floors, a new holder starts its
// role subject after the highest of them.
func TestANewRoleHolderStartsAfterTheHighestOfSeveralHolderFloors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	a := lineConnect(t, ctx, url, "sup-a", "supervisor", "")
	c := lineConnect(t, ctx, url, "sup-c", "supervisor", "")
	publishN(t, ctx, js, lineRole("supervisor"), "r", 1, 40)
	lineReadAck(t, ctx, a, 37)
	lineReadAck(t, ctx, c, 10)

	fresh := lineConnect(t, ctx, url, "sup-b", "supervisor", "")
	res, err := fresh.receiveBatch(ctx, 3*time.Second, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 3 {
		t.Errorf("the new holder received %d messages (%q), want role 38 to 40 only", len(res.Items), lineIDs(lineItems(t, res)))
	}
}
