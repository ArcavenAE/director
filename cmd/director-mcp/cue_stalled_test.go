package main

// director#297: presence read `cue: live` for a seat whose cues went
// unanswered twice. Every row now carries the age of the seat's oldest waiting
// message, and the cue reads `stalled` while that age is at least 2W with no
// wait open. Stalled is computed on each read and never stored.

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"
)

// trackedRig is a cue rig whose source marks a tracker on the rig's clock, the
// way busCueSource marks the seat's, and whose presence reads that tracker.
func trackedRig(t *testing.T) *cueRig {
	r := newCueRig(t)
	tr := &pendingTracker{}
	r.src.tr = tr
	r.src.clock = func() time.Time { return r.now }
	r.c.oldest = func() time.Duration { return tr.age(r.now) }
	return r
}

// liveWithOnePending makes the cue live, takes an empty sample, then sees one
// message on the next one.
func (r *cueRig) liveWithOnePending() {
	r.started()
	r.c.ack("nonce1")
	r.step(2 * time.Second)
	r.src.n = 1
	r.step(2 * time.Second)
}

func TestAMessagePendingTwoWindowsWithNoWaitReadsStalled(t *testing.T) {
	r := trackedRig(t)
	r.liveWithOnePending() // first seen at the previous sample, t=2
	r.step(590 * time.Second)
	if st, _ := r.c.presence(); st != cueStateLive {
		t.Fatalf("pending 592 s, under 2W: want live, got %q", st)
	}
	r.step(10 * time.Second)
	st, reason := r.c.presence()
	if st != cueStateStalled {
		t.Fatalf("pending 602 s with no wait open: want stalled, got %q (%q)", st, reason)
	}
	if !strings.Contains(reason, "stalled: oldest pending") || !strings.Contains(reason, "waiting 602s with no wait open") {
		t.Fatalf("the reason names the age: %q", reason)
	}
}

func TestAStalledCueClearsWhenTheCountDropsWithNoNonce(t *testing.T) {
	r := trackedRig(t)
	r.started() // never echoed: unverified
	r.step(2 * time.Second)
	r.src.n = 1
	r.step(2 * time.Second)
	r.step(700 * time.Second)
	if st, _ := r.c.presence(); st != cueStateStalled {
		t.Fatalf("an unverified cue with mail waiting 2W reads stalled, got %q", st)
	}
	r.src.n = 0
	r.step(2 * time.Second)
	if st, reason := r.c.presence(); st != cueStateUnver || strings.Contains(reason, "stalled") {
		t.Fatalf("the drop clears stalled with no nonce: got %q (%q)", st, reason)
	}
}

func TestAWaitOpenAcrossThreeWindowsStaysLive(t *testing.T) {
	r := trackedRig(t)
	r.started()
	r.c.ack("nonce1")
	r.step(2 * time.Second)
	r.c.waitBegin()
	r.src.n = 1
	r.step(2 * time.Second)
	r.step(900 * time.Second)
	if st, reason := r.c.presence(); st != cueStateLive {
		t.Fatalf("a wait is open, so the seat is not stalled: got %q (%q)", st, reason)
	}
}

func TestAReadErrorStaysUnverifiedNeverStalled(t *testing.T) {
	r := trackedRig(t)
	r.liveWithOnePending()
	r.step(700 * time.Second)
	if st, _ := r.c.presence(); st != cueStateStalled {
		t.Fatalf("setup: want stalled, got %q", st)
	}
	r.src.err = context.DeadlineExceeded
	r.step(2 * time.Second)
	st, reason := r.c.presence()
	if st != cueStateUnver || strings.Contains(reason, "stalled") || !strings.Contains(reason, "cannot read inbox state") {
		t.Fatalf("a failed count read is unverified, never stalled: got %q (%q)", st, reason)
	}
}

// The #297 regression: twice unanswered is back on the manual wake, and
// presence must not read live there.
func TestTwiceUnansweredReadsStalledNotLive(t *testing.T) {
	r := trackedRig(t)
	r.liveWithOnePending()
	r.step(299 * time.Second)
	r.step(2 * time.Second)   // first window: warning and re-cue
	r.step(301 * time.Second) // second window: back to the manual wake
	if len(r.warns) != 2 {
		t.Fatalf("setup: want two unanswered warnings, got %v", r.warns)
	}
	if st, reason := r.c.presence(); st != cueStateStalled {
		t.Fatalf("twice unanswered must not read live: got %q (%q)", st, reason)
	}
}

func TestStalledIsComputedNotLatched(t *testing.T) {
	r := trackedRig(t)
	r.liveWithOnePending()
	r.step(700 * time.Second)
	if st, _ := r.c.presence(); st != cueStateStalled {
		t.Fatalf("setup: want stalled, got %q", st)
	}
	r.c.waitBegin()
	if st, _ := r.c.presence(); st != cueStateLive {
		t.Fatalf("a wait opened: want live, got %q", st)
	}
	r.c.waitEnd()
	if st, _ := r.c.presence(); st != cueStateStalled {
		t.Fatalf("the wait ended with the message still waiting: want stalled, got %q", st)
	}
	r.src.n = 0
	r.step(2 * time.Second)
	if st, _ := r.c.presence(); st != cueStateLive {
		t.Fatalf("drained: want live, got %q", st)
	}
}

func TestAnArrivalAbsorbedDuringAWaitIsCuedAfterTheWaitEnds(t *testing.T) {
	r := newCueRig(t)
	r.started()
	r.c.ack("nonce1")
	r.cues = nil
	r.step(2 * time.Second)
	r.c.waitBegin()
	r.src.n = 2
	r.step(2 * time.Second)
	if n := len(r.mailCues()); n != 0 {
		t.Fatalf("no cue while a wait is open, got %d", n)
	}
	r.src.n = 1 // the wait took one; one is left
	r.c.waitEnd()
	r.step(2 * time.Second)
	cues := r.mailCues()
	if len(cues) != 1 || meta(t, cues[0])["count"] != "1" {
		t.Fatalf("the message left after the wait is cued once with count 1: %v", cues)
	}
}

// With concurrent tool calls a second call can run while a wait is open. A
// cue stays suppressed while any wait is open and re-arms when the last ends.
func TestACueDuringOneOfTwoWaitsRearmsWhenTheLastEnds(t *testing.T) {
	r := newCueRig(t)
	r.started()
	r.c.ack("nonce1")
	r.cues = nil
	r.step(2 * time.Second)
	r.c.waitBegin()
	r.c.waitBegin()
	r.src.n = 1
	r.step(2 * time.Second)
	r.c.waitEnd()
	r.step(2 * time.Second)
	if n := len(r.mailCues()); n != 0 {
		t.Fatalf("one wait is still open: want no cue, got %d", n)
	}
	r.c.waitEnd()
	r.step(2 * time.Second)
	if n := len(r.mailCues()); n != 1 {
		t.Fatalf("the last wait ended with mail waiting: want one cue, got %d", n)
	}
}

// ---- the tracker ---------------------------------------------------------

var trackT0 = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

func atS(s int) time.Time { return trackT0.Add(time.Duration(s) * time.Second) }

// A wait drains local first, so a local message can leave before an older
// global one. One tracker per tier keeps the global message's age.
func TestTrackerCrossTierDrainReadsTheOlderFront(t *testing.T) {
	var p pendingTracker
	p.mark("local", 0, atS(0))
	p.mark("global", 0, atS(0))
	p.mark("global", 1, atS(10)) // seen at the previous sample, 0
	p.mark("local", 1, atS(20))  // seen at 10
	p.mark("local", 0, atS(30))  // the wait took the local one
	if got := p.age(atS(30)); got != 30*time.Second {
		t.Fatalf("the global message is the oldest: want 30s, got %s", got)
	}
}

func TestTrackerDropsAStaleMark(t *testing.T) {
	var p pendingTracker
	p.mark("local", 0, atS(0))
	p.mark("local", 2, atS(10))
	p.mark("local", 0, atS(5)) // a read stamped earlier, landing late
	if got := p.age(atS(20)); got != 20*time.Second {
		t.Fatalf("a stale mark leaves the age unchanged: want 20s, got %s", got)
	}
}

// A message that arrives between two samples is stamped at the earlier one, so
// its age is never below its true age (at most one sample above it).
func TestTrackerAgeIsAtLeastTheTrueAge(t *testing.T) {
	var p pendingTracker
	p.mark("local", 0, atS(0))
	// the message arrives at 5
	p.mark("local", 1, atS(10))
	got := p.age(atS(20))
	if got < 15*time.Second || got > 20*time.Second {
		t.Fatalf("true age 15s, sample interval 10s: want 15s..20s, got %s", got)
	}
}

func TestTrackerPopsFromTheFrontAndEmpties(t *testing.T) {
	var p pendingTracker
	p.mark("local", 0, atS(0))
	p.mark("local", 1, atS(10)) // seen 0
	p.mark("local", 2, atS(20)) // seen 10
	p.mark("local", 1, atS(30)) // the first one left
	if got := p.age(atS(30)); got != 20*time.Second {
		t.Fatalf("after the oldest leaves, the next is seen at 10: want 20s, got %s", got)
	}
	p.mark("local", 0, atS(40))
	if got := p.age(atS(40)); got != 0 {
		t.Fatalf("nothing waiting: want 0, got %s", got)
	}
}

// ---- the bus: what feeds the tracker, and what the rows carry ------------

func TestTheCueTickReadMarksTheSeatsTracker(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	bus := roleBus(t, ctx, url, "cue-seat", "")
	pubEnv(t, ctx, js, "agent.aae-orc.ops.cue-seat.inbox", "m-tick", "INFORM", "hello")
	bus.pending.mark("local", 0, time.Now().Add(-100*time.Second))
	n, err := busCueSource{bus}.count(ctx)
	if err != nil || n != 1 {
		t.Fatalf("count = %d, %v; want 1", n, err)
	}
	if got := bus.oldestPending(); got < 100*time.Second {
		t.Fatalf("the tick's read marks the tracker: want at least 100s, got %s", got)
	}
}

func TestTheHeartbeatSampleMarksTheTrackerWithTheCueOff(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	bus := roleBus(t, ctx, url, "beat-seat", "")
	pubEnv(t, ctx, js, "agent.aae-orc.ops.beat-seat.inbox", "m-beat", "INFORM", "hello")
	bus.pending.mark("local", 0, time.Now().Add(-100*time.Second))
	bus.samplePending(ctx)
	if got := bus.oldestPending(); got < 100*time.Second {
		t.Fatalf("the heartbeat sample marks the tracker: want at least 100s, got %s", got)
	}
}

func globalRow(t *testing.T, ctx context.Context, b *Bus) map[string]any {
	t.Helper()
	g, err := b.globalReady(ctx)
	if err != nil {
		t.Fatal(err)
	}
	e, err := g.kv.Get(ctx, g.cfg.presenceKey(b.instance))
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err := json.Unmarshal(e.Value(), &row); err != nil {
		t.Fatal(err)
	}
	return row
}

func keysOf(m map[string]any) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// Control: with the cue off the global row is unchanged except for
// oldest_pending_s, and the local row gains only that field too.
func TestWithTheCueOffTheRowsGainOnlyOldestPending(t *testing.T) {
	t.Setenv("DIRECTOR_PRESENCE_LOST_AFTER", "")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, _ := healthyGlobalBus(t, ctx)
	if _, err := bus.setPresence(ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	g := globalRow(t, ctx, bus)
	want := "agent_id cluster instance oldest_pending_s pid role state team ts workspace"
	if got := strings.Join(keysOf(g), " "); got != want {
		t.Fatalf("cue-off global row keys:\n got %s\nwant %s", got, want)
	}
	if _, ok := g["oldest_pending_s"].(float64); !ok {
		t.Fatalf("oldest_pending_s is a number: %v", g)
	}
	l := presenceRow(t, ctx, bus)
	if _, ok := l["oldest_pending_s"].(float64); !ok {
		t.Fatalf("the local row carries a numeric oldest_pending_s with the cue off: %v", l)
	}
	if _, ok := l["cue"]; ok {
		t.Fatalf("no cue field with the cue off: %v", l)
	}
}

func TestTheGlobalRowHoldsCueReasonAndOldestPending(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, _ := healthyGlobalBus(t, ctx)
	r := newCueRig(t)
	r.started()
	r.c.mu.Lock()
	r.c.reason = "no nonce echo after self-test"
	r.c.mu.Unlock()
	bus.attachCue(r.c)
	if _, err := bus.setPresence(ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	for name, row := range map[string]map[string]any{"global": globalRow(t, ctx, bus), "local": presenceRow(t, ctx, bus)} {
		if row["cue"] != cueStateUnver || row["cue_reason"] != "no nonce echo after self-test" {
			t.Errorf("%s row: want the cue and its reason, got %v", name, row)
		}
		if _, ok := row["oldest_pending_s"].(float64); !ok {
			t.Errorf("%s row: want a numeric oldest_pending_s, got %v", name, row)
		}
	}
}

func TestAStalledRowNamesTheOldestMessageAndItsAge(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	bus := roleBus(t, ctx, url, "stall-seat", "")
	pubEnv(t, ctx, js, "agent.aae-orc.ops.stall-seat.inbox", "m-stalled", "REQUEST", "hello")
	r := newCueRig(t)
	r.started()
	r.c.ack("nonce1")
	bus.attachCue(r.c)
	bus.pending.mark("local", 0, time.Now().Add(-700*time.Second))
	bus.pending.mark("local", 1, time.Now().Add(-650*time.Second))
	if err := bus.writePresence(ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	row := presenceRow(t, ctx, bus)
	reason, _ := row["cue_reason"].(string)
	if row["cue"] != cueStateStalled || !strings.HasPrefix(reason, "stalled: oldest pending m-stalled waiting 7") || !strings.HasSuffix(reason, "s with no wait open") {
		t.Fatalf("a stalled row names the message and its age: %v", row)
	}
	if s, _ := row["oldest_pending_s"].(float64); s < 700 {
		t.Fatalf("oldest_pending_s: want at least 700, got %v", row["oldest_pending_s"])
	}
}
