package main

import (
	"context"
	"testing"
	"time"
)

// director#684. Acking a message inherited from a predecessor must not raise the
// successor's own durable's acked counter. The two durables' sequences interleave
// (the same reason roleSeqs exists for role mail, director#680), so a shared
// maximum would make a rebuilt own durable start past unread own mail and lose it
// without an error. These read the counter and the restart point directly rather
// than running a full read: the interleaving is built by hand, with own mail at
// stream sequence 1 unread and the predecessor's message at sequence 2 acked.

// inheritedFixture publishes own mail (sequence 1, left unread) and one
// predecessor message (sequence 2), starts a successor whose inheritance holds
// the predecessor's message, and returns the successor. handled turns the handled
// mode on for it.
func inheritedFixture(t *testing.T, ctx context.Context, handled bool) *Bus {
	t.Helper()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	pred := lineConnect(t, ctx, url, "pred", "", "")
	publishN(t, ctx, js, lineInbox("succ"), "o", 1, 1) // sequence 1: the successor's own mail
	publishN(t, ctx, js, lineInbox("pred"), "p", 1, 1) // sequence 2: the predecessor's
	lineExit(t, ctx, pred)

	t.Setenv("DIRECTOR_STATE", t.TempDir())
	if handled {
		t.Setenv("DIRECTOR_HANDLED", "1")
	} else {
		t.Setenv("DIRECTOR_HANDLED", "")
	}
	t.Setenv("DIRECTOR_PREDECESSOR", "pred")
	t.Setenv("MARVEL_PREDECESSOR", lineWS+"/pred")
	succ, err := connect(ctx, url, Sender{AgentID: "succ", Workspace: lineWS, Team: lineTeam}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(succ.close)
	if err := succ.writePresence(ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	succ.takeResumed()
	runHeartbeat(t, ctx, succ, 40*time.Millisecond)
	if !waitFor(5*time.Second, func() bool { return len(inheritDurables(t, ctx, js, "succ")) == 1 }) {
		t.Fatal("nothing inherited")
	}
	return succ
}

// inheritedHeld is how many inherited sequences still wait for their ack; an
// acked one leaves the set, so it does not grow for the life of the session.
func inheritedHeld(b *Bus) int {
	b.localMu.Lock()
	defer b.localMu.Unlock()
	return len(b.inheritedSeqs)
}

func ownCounter(b *Bus) uint64 {
	b.localMu.Lock()
	defer b.localMu.Unlock()
	return b.local.acked
}

// The ack-after-reply path: the inherited message is held, and the ack by
// sequence alone happens after the reply is written.
func TestAnInheritedMessageAckedAfterTheReplyDoesNotMoveTheOwnCounter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	succ := inheritedFixture(t, ctx, false)

	wctx, al := withAckLater(ctx)
	var res batchResult
	succ.drainInherited(wctx, 1, &res)
	if len(res.Items) != 1 || res.Items[0].Seq != 2 {
		t.Fatalf("setup: the inherited drain gave %d items %+v, want the predecessor's message at sequence 2", len(res.Items), res.Items)
	}
	al.ack(succ, t.Logf)

	if n := inheritedHeld(succ); n != 0 {
		t.Errorf("%d inherited sequences are still held after their ack, want 0", n)
	}
	if got := ownCounter(succ); got != 0 {
		t.Errorf("the own durable's acked counter = %d after an inherited message at sequence 2 was acked, want 0", got)
	}
	if start, _ := succ.localRestartSeq(false); start > 1 {
		t.Errorf("a rebuilt own durable would start at sequence %d, past the unread own mail at sequence 1", start)
	}
}

// The handled mode: mark_handled acks the inherited message by sequence alone.
func TestAnInheritedMessageMarkedHandledDoesNotMoveTheOwnCounter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	succ := inheritedFixture(t, ctx, true)

	var res batchResult
	succ.drainInherited(ctx, 1, &res)
	if len(res.Items) != 1 || res.Items[0].Seq != 2 {
		t.Fatalf("setup: the inherited drain gave %d items %+v, want the predecessor's message at sequence 2", len(res.Items), res.Items)
	}
	if _, err := succ.markHandled(ctx, "local", res.Items[0].Env.MessageID, "handled", ""); err != nil {
		t.Fatal(err)
	}

	if n := inheritedHeld(succ); n != 0 {
		t.Errorf("%d inherited sequences are still held after their ack, want 0", n)
	}
	if got := ownCounter(succ); got != 0 {
		t.Errorf("the own durable's acked counter = %d after an inherited message at sequence 2 was marked handled, want 0", got)
	}
	if start, _ := succ.localRestartSeq(false); start > 1 {
		t.Errorf("a rebuilt own durable would start at sequence %d, past the unread own mail at sequence 1", start)
	}
}

// A pin, and the positive control for the two above: an own message acked after
// the reply still credits the own counter, so a counter that reads 0 above is the
// fix and not a harness that never moves it.
func TestAnOwnMessageAckedAfterTheReplyMovesTheOwnCounter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	succ := inheritedFixture(t, ctx, false)

	wctx, al := withAckLater(ctx)
	items, _, err := succ.pullLocalNoWait(wctx, 1)
	if err != nil || len(items) != 1 || items[0].Seq != 1 {
		t.Fatalf("setup: the own pull gave %+v (err %v), want the own message at sequence 1", items, err)
	}
	al.ack(succ, t.Logf)
	if got := ownCounter(succ); got != 1 {
		t.Errorf("the own durable's acked counter = %d after its own message at sequence 1 was acked, want 1", got)
	}
}

// The handled-mode control for the two inherited tests above: an own message
// pulled under DIRECTOR_HANDLED=1 and marked handled still credits the own
// counter, so a 0 after an inherited mark is the fix and not a handled mode that
// never moves it.
func TestAnOwnMessageMarkedHandledMovesTheOwnCounter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	succ := inheritedFixture(t, ctx, true)

	items, _, err := succ.pullLocalNoWait(ctx, 1)
	if err != nil || len(items) != 1 || items[0].Seq != 1 {
		t.Fatalf("setup: the own pull gave %+v (err %v), want the own message at sequence 1", items, err)
	}
	if _, err := succ.markHandled(ctx, "local", items[0].Env.MessageID, "handled", ""); err != nil {
		t.Fatal(err)
	}
	if got := ownCounter(succ); got != 1 {
		t.Errorf("the own durable's acked counter = %d after its own message at sequence 1 was marked handled, want 1", got)
	}
}
