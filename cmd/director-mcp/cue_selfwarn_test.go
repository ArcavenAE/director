package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// ArcavenAE/director#626: the director seat owns global://director, so the
// cue.unanswered warning it sends there lands in its own inbox. The warning is
// then a new waiting message that the next receipt window reports, and the loop
// never settles while the seat is busy. A warning whose recipient is the seat
// itself must not be sent, so it cannot count as pending. #511's rule stands: a
// stuck seat other than director is still reported once.

func directorStreamMsgs(t *testing.T, ctx context.Context, gjs jetstream.JetStream) uint64 {
	t.Helper()
	st, err := gjs.Stream(ctx, globalDirectorStream)
	if err != nil {
		t.Fatal(err)
	}
	info, err := st.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return info.State.Msgs
}

// seatPresent registers the seat's global presence row, as a running seat does,
// so a send to its address is not refused for want of a reader.
func seatPresent(t *testing.T, ctx context.Context, b *Bus) {
	t.Helper()
	g, err := b.globalReady(ctx)
	if err != nil {
		t.Fatalf("setup: the global tier is not attached: %v", err)
	}
	if err := g.writePresence(ctx, b.self, b.instance, b.pid, "idle"); err != nil {
		t.Fatalf("setup: presence row: %v", err)
	}
}

// The director seat's own warning is logged and sent nowhere: its inbox holds no
// new message and the pending count the cue measures does not move.
func TestDirectorSeatWarningDoesNotLandInItsOwnInbox(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, _, gjs, _ := handledBus(t, ctx, true, "")
	if bus.globalCfg == nil || bus.globalCfg.Role != roleDirector {
		t.Fatalf("setup: the seat is not the director seat: %+v", bus.globalCfg)
	}
	seatPresent(t, ctx, bus)
	var logged []string
	logf := func(f string, a ...any) { logged = append(logged, strings.TrimSpace(f)) }
	beforeMsgs := directorStreamMsgs(t, ctx, gjs)
	beforePending, err := bus.pendingByTier(ctx)
	if err != nil {
		t.Fatal(err)
	}

	warnUnanswered(ctx, bus, logf, []string{"01MSGWAITING"}, "live")

	if got := directorStreamMsgs(t, ctx, gjs); got != beforeMsgs {
		t.Fatalf("the director seat's cue.unanswered warning was published to its own inbox: GLOBAL_TO_DIRECTOR holds %d messages, was %d", got, beforeMsgs)
	}
	afterPending, err := bus.pendingByTier(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if afterPending != beforePending {
		t.Fatalf("the warning counted as pending for the seat that sent it: %d, was %d", afterPending, beforePending)
	}
	if len(logged) == 0 || !strings.Contains(logged[0], "WARNING") {
		t.Fatalf("the warning must still be logged, logged = %v", logged)
	}
}

// The same holds for the give-up INFORM, which also goes to global://director.
func TestDirectorSeatGaveUpDoesNotLandInItsOwnInbox(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, _, gjs, _ := handledBus(t, ctx, true, "")
	seatPresent(t, ctx, bus)
	before := directorStreamMsgs(t, ctx, gjs)

	informGaveUp(ctx, bus, func(string, ...any) {}, []string{"01MSGWAITING"}, "live")

	if got := directorStreamMsgs(t, ctx, gjs); got != before {
		t.Fatalf("the director seat's cue.gave_up was published to its own inbox: GLOBAL_TO_DIRECTOR holds %d messages, was %d", got, before)
	}
}

// Control, and #511's rule: a cluster supervisor that is stuck still reports to
// director, once per call, so the two tests above are able to see a send at all.
func TestSupervisorSeatWarningStillReachesDirector(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, _ := provision(t, ctx, url)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	if _, err := gjs.CreateStream(ctx, jetstream.StreamConfig{Name: "GLOBAL_TO_kinu", Subjects: []string{"global.kinu.>"}}); err != nil {
		t.Fatal(err)
	}
	sup, err := connect(ctx, url, Sender{AgentID: "sup", Workspace: "w", Team: "t"}, &globalConfig{Domain: "global", Cluster: "kinu", Role: roleSupervisor})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sup.close)
	// A live director seat to read the warning, on the same hub.
	dir, err := connect(ctx, url, Sender{AgentID: "director", Workspace: "w", Team: "t"}, &globalConfig{Domain: "global", Cluster: "kinu", Role: roleDirector})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(dir.close)
	seatPresent(t, ctx, dir)
	seatPresent(t, ctx, sup)
	before := directorStreamMsgs(t, ctx, gjs)

	warnUnanswered(ctx, sup, t.Logf, []string{"01MSGWAITING"}, "live")

	if got := directorStreamMsgs(t, ctx, gjs); got != before+1 {
		t.Fatalf("CONTROL FAILED: a supervisor's cue.unanswered did not reach director: GLOBAL_TO_DIRECTOR holds %d messages, was %d", got, before)
	}
}
