package main

import (
	"context"
	"encoding/json"
	"fmt"
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

// captureLog collects the formatted log lines, so a test can read the text and
// not the format string.
func captureLog() (*[]string, func(string, ...any)) {
	var out []string
	return &out, func(f string, a ...any) { out = append(out, fmt.Sprintf(f, a...)) }
}

// assertSelfWarnLogged requires the warning to be logged with its text and no
// failed send: tellDirector swallows a publish error into a log line, so a send
// that was tried and failed must not read as "not sent".
func assertSelfWarnLogged(t *testing.T, lines []string, kind string) {
	t.Helper()
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "WARNING "+kind+": seat operator") || !strings.Contains(lines[0], "01MSGWAITING") {
		t.Fatalf("the %s warning must still be logged with its text, logged = %q", kind, lines)
	}
	for _, l := range lines {
		if strings.Contains(l, "not delivered to director") {
			t.Fatalf("a send was attempted and failed, which is not 'not sent': %q", l)
		}
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
	logged, logf := captureLog()
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
	assertSelfWarnLogged(t, *logged, "cue.unanswered")
}

// The same holds for the give-up INFORM, which also goes to global://director.
func TestDirectorSeatGaveUpDoesNotLandInItsOwnInbox(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, _, gjs, _ := handledBus(t, ctx, true, "")
	if bus.globalCfg == nil || bus.globalCfg.Role != roleDirector {
		t.Fatalf("setup: the seat is not the director seat: %+v", bus.globalCfg)
	}
	seatPresent(t, ctx, bus)
	logged, logf := captureLog()
	beforeMsgs := directorStreamMsgs(t, ctx, gjs)
	beforePending, err := bus.pendingByTier(ctx)
	if err != nil {
		t.Fatal(err)
	}

	informGaveUp(ctx, bus, logf, []string{"01MSGWAITING"}, "live")

	if got := directorStreamMsgs(t, ctx, gjs); got != beforeMsgs {
		t.Fatalf("the director seat's cue.gave_up was published to its own inbox: GLOBAL_TO_DIRECTOR holds %d messages, was %d", got, beforeMsgs)
	}
	afterPending, err := bus.pendingByTier(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if afterPending != beforePending {
		t.Fatalf("the give-up counted as pending for the seat that sent it: %d, was %d", afterPending, beforePending)
	}
	assertSelfWarnLogged(t, *logged, "cue.gave_up")
}

// Control, and #511's rule: a stuck cluster supervisor still reports to director,
// both the warning and the give-up, on the same hub and harness as the tests
// above with only the role different, so those tests are able to see a send at
// all.
func TestSupervisorSeatWarningStillReachesDirector(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dirBus, _, gjs, _ := handledBus(t, ctx, true, "")
	seatPresent(t, ctx, dirBus)
	if _, err := gjs.CreateStream(ctx, jetstream.StreamConfig{Name: "GLOBAL_TO_kinu", Subjects: []string{"global.kinu.>"}}); err != nil {
		t.Fatal(err)
	}
	sup, err := connect(ctx, dirBus.nc.ConnectedUrl(), Sender{AgentID: "sup", Workspace: "aae-orc", Team: "ops"}, &globalConfig{Domain: "global", Cluster: "kinu", Role: roleSupervisor})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sup.close)
	seatPresent(t, ctx, sup)

	for _, c := range []struct {
		kind string
		call func(context.Context, *Bus, func(string, ...any), []string, string)
	}{
		{"cue.unanswered", warnUnanswered},
		{"cue.gave_up", informGaveUp},
	} {
		before := directorStreamMsgs(t, ctx, gjs)
		c.call(ctx, sup, t.Logf, []string{"01MSGWAITING"}, "live")
		if got := directorStreamMsgs(t, ctx, gjs); got != before+1 {
			t.Fatalf("CONTROL FAILED: %s from a supervisor did not reach director: GLOBAL_TO_DIRECTOR holds %d messages, was %d", c.kind, got, before)
		}
		st, err := gjs.Stream(ctx, globalDirectorStream)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := st.GetLastMsgForSubject(ctx, "global.director.inbox")
		if err != nil {
			t.Fatal(err)
		}
		var e Envelope
		if err := json.Unmarshal(raw.Data, &e); err != nil {
			t.Fatal(err)
		}
		if e.Performative != "INFORM" || e.Sender.AgentID != "sup" || !strings.Contains(e.Content.Data, c.kind+": seat sup") {
			t.Fatalf("CONTROL FAILED: the message on director's inbox is not sup's %s: %+v", c.kind, e)
		}
	}
}
