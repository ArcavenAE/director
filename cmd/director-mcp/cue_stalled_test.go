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

	"github.com/nats-io/nats.go/jetstream"
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

// smp is one tier sample: unread (NumPending), delivered (Delivered.Stream),
// the tag, and the second it was taken.
func smp(unread, delivered, tag uint64, s int) pendingSample {
	return pendingSample{unread: unread, delivered: delivered, tag: tag, at: atS(s)}
}

// Review 5638952074: message A (seq 5) leaves and B (seq 9) arrives inside one
// sample, so the count stays 1. B must not inherit A's first-seen time.
func TestTrackerSwapInsideOneSampleDoesNotInheritTheAge(t *testing.T) {
	var p pendingTracker
	p.mark("local", smp(0, 0, 0, 0))
	p.mark("local", smp(1, 0, 5, 2))    // A
	p.mark("local", smp(1, 5, 9, 1000)) // A delivered, B arrived
	if got := p.age(atS(1000)); got > 998*time.Second {
		t.Fatalf("B arrived after the sample at 2 s: want at most 998s, got %s", got)
	}
}

// A redelivery between samples moves neither Delivered.Stream nor the tag, so
// it cannot pop the oldest unread message early.
func TestTrackerARedeliveryDoesNotUnderstate(t *testing.T) {
	var p pendingTracker
	p.mark("local", smp(0, 0, 0, 0))
	p.mark("local", smp(1, 0, 5, 10)) // seen at 0
	p.mark("local", smp(1, 0, 5, 20)) // an older message was redelivered in between
	if got := p.age(atS(20)); got != 20*time.Second {
		t.Fatalf("the unread message has waited since 0: want 20s, got %s", got)
	}
}

// Two messages arrive in one sample; one is delivered. The mark covers both
// and stays until the second is delivered too.
func TestTrackerAPartlyDeliveredMarkIsKeptWhole(t *testing.T) {
	var p pendingTracker
	p.mark("local", smp(0, 0, 0, 0))
	p.mark("local", smp(2, 0, 7, 10)) // seqs 6 and 7, seen at 0
	p.mark("local", smp(1, 6, 7, 20)) // 6 delivered, 7 still unread
	if got := p.age(atS(20)); got != 20*time.Second {
		t.Fatalf("seq 7 has waited since 0: want 20s, got %s", got)
	}
}

// The reconcile: nothing unread clears every mark, and the next arrival is
// stamped at the sample before it.
func TestTrackerNothingUnreadClearsEveryMark(t *testing.T) {
	var p pendingTracker
	p.mark("local", smp(0, 0, 0, 0))
	p.mark("local", smp(1, 0, 5, 10))
	p.mark("local", smp(0, 5, 0, 20))
	if got := p.age(atS(20)); got != 0 {
		t.Fatalf("nothing unread: want 0, got %s", got)
	}
	p.mark("local", smp(1, 5, 8, 30))
	if got := p.age(atS(30)); got != 10*time.Second {
		t.Fatalf("the next arrival is stamped at 20: want 10s, got %s", got)
	}
}

// A count that drops while Delivered.Stream stays put (a message removed from
// the stream, not delivered) keeps the mark: the age reads high, never low.
func TestTrackerADropWithoutADeliveryKeepsTheAge(t *testing.T) {
	var p pendingTracker
	p.mark("local", smp(0, 0, 0, 0))
	p.mark("local", smp(2, 0, 7, 10))
	p.mark("local", smp(1, 0, 7, 20))
	if got := p.age(atS(20)); got != 20*time.Second {
		t.Fatalf("want 20s, got %s", got)
	}
}

// A wait drains local first, so a local message can leave before an older
// global one. Each tier keeps its own marks.
func TestTrackerCrossTierDrainReadsTheOlderFront(t *testing.T) {
	var p pendingTracker
	p.mark("local", smp(0, 0, 0, 0))
	p.mark("global", smp(0, 0, 0, 0))
	p.mark("global", smp(1, 0, 3, 10)) // seen 0
	p.mark("local", smp(0, 0, 0, 10))
	p.mark("local", smp(1, 0, 4, 20)) // seen 10
	p.mark("local", smp(0, 4, 0, 30)) // the wait took the local one
	if got := p.age(atS(30)); got != 30*time.Second {
		t.Fatalf("the global message is the oldest: want 30s, got %s", got)
	}
}

func TestTrackerDropsAStaleSample(t *testing.T) {
	var p pendingTracker
	p.mark("local", smp(0, 0, 0, 0))
	p.mark("local", smp(2, 0, 7, 10))
	p.mark("local", smp(0, 7, 0, 5)) // a read stamped earlier, landing late
	if got := p.age(atS(20)); got != 20*time.Second {
		t.Fatalf("a stale sample leaves the age unchanged: want 20s, got %s", got)
	}
}

// From the second sample on, a message is stamped at the sample before it
// arrived, so its age is at least its true age and at most one sample more.
func TestTrackerAgeIsAtLeastTheTrueAge(t *testing.T) {
	var p pendingTracker
	p.mark("local", smp(0, 0, 0, 0))
	// the message arrives at 5
	p.mark("local", smp(1, 0, 3, 10))
	got := p.age(atS(20))
	if got < 15*time.Second || got > 20*time.Second {
		t.Fatalf("true age 15s, sample interval 10s: want 15s..20s, got %s", got)
	}
}

func TestTrackerPopsAMarkOnceDeliveredReachesIt(t *testing.T) {
	var p pendingTracker
	p.mark("local", smp(0, 0, 0, 0))
	p.mark("local", smp(1, 0, 3, 10)) // seen 0
	p.mark("local", smp(2, 0, 6, 20)) // seen 10
	p.mark("local", smp(1, 3, 6, 30)) // seq 3 delivered
	if got := p.age(atS(30)); got != 20*time.Second {
		t.Fatalf("after seq 3 leaves, the next mark is seen at 10: want 20s, got %s", got)
	}
}

// The first sample has nothing before it, so a backlog already waiting at
// start is stamped at that sample's own time and reads young (disclosed).
func TestTrackerAFirstSampleIsStampedAtItsOwnTime(t *testing.T) {
	var p pendingTracker
	p.mark("local", smp(3, 0, 9, 100))
	if got := p.age(atS(110)); got != 10*time.Second {
		t.Fatalf("want 10s, got %s", got)
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
	bus.pending.mark("local", pendingSample{at: time.Now().Add(-100 * time.Second)})
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
	bus.pending.mark("local", pendingSample{at: time.Now().Add(-100 * time.Second)})
	bus.samplePending(ctx)
	if got := bus.oldestPending(); got < 100*time.Second {
		t.Fatalf("the heartbeat sample marks the tracker: want at least 100s, got %s", got)
	}
}

// fetchOne takes the next message from the seat's local durable without the
// shim, so a test can deliver, hold or Nak it.
func fetchOneLocal(t *testing.T, bus *Bus) jetstream.Msg {
	t.Helper()
	b, err := bus.cons().Fetch(1, jetstream.FetchMaxWait(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for m := range b.Messages() {
		return m
	}
	t.Fatal("no message fetched")
	return nil
}

// The swap through the bus, on a stream other seats share: A is delivered and
// B arrives between two samples, with another seat's message on the stream
// after A. The tag is this seat's own last sequence, so A's mark pops and B is
// stamped at the sample before it.
func TestTheBusSwapOnASharedStreamDoesNotInheritTheAge(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	bus := roleBus(t, ctx, url, "swap-seat", "")
	bus.pending.mark("local", pendingSample{at: time.Now().Add(-1000 * time.Second)})
	pubEnv(t, ctx, js, "agent.aae-orc.ops.swap-seat.inbox", "m-a", "INFORM", "a")
	pubEnv(t, ctx, js, "agent.aae-orc.ops.other-seat.inbox", "m-x", "INFORM", "not ours")
	if _, err := (busCueSource{bus}).count(ctx); err != nil {
		t.Fatal(err)
	}
	if got := bus.oldestPending(); got < 1000*time.Second {
		t.Fatalf("setup: A is stamped at the old sample: want at least 1000s, got %s", got)
	}
	m := fetchOneLocal(t, bus)
	if err := m.DoubleAck(ctx); err != nil {
		t.Fatal(err)
	}
	pubEnv(t, ctx, js, "agent.aae-orc.ops.swap-seat.inbox", "m-b", "INFORM", "b")
	if _, err := (busCueSource{bus}).count(ctx); err != nil {
		t.Fatal(err)
	}
	if got := bus.oldestPending(); got > 60*time.Second {
		t.Fatalf("B arrived after the last sample: want under a minute, got %s", got)
	}
}

// Stalled ages unread mail only: a message delivered and held ack-pending does
// not count, and neither does one Nak'd after a cancel while it waits to be
// delivered again (disclosed in the body).
func TestTheTrackerCountsUnreadMailOnly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	bus := roleBus(t, ctx, url, "held-seat", "")
	bus.pending.mark("local", pendingSample{at: time.Now().Add(-1000 * time.Second)})
	pubEnv(t, ctx, js, "agent.aae-orc.ops.held-seat.inbox", "m-held", "INFORM", "held")
	m := fetchOneLocal(t, bus) // delivered, not acked: ack-pending
	if _, err := (busCueSource{bus}).count(ctx); err != nil {
		t.Fatal(err)
	}
	if got := bus.oldestPending(); got != 0 {
		t.Fatalf("an ack-pending message was read: want 0, got %s", got)
	}
	if err := m.Nak(); err != nil {
		t.Fatal(err)
	}
	if _, err := (busCueSource{bus}).count(ctx); err != nil {
		t.Fatal(err)
	}
	if got := bus.oldestPending(); got != 0 {
		t.Fatalf("a Nak'd message waiting for redelivery is not counted: want 0, got %s", got)
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
	bus.pending.mark("local", pendingSample{at: time.Now().Add(-700 * time.Second)})
	bus.pending.mark("local", pendingSample{unread: 1, tag: 1, at: time.Now().Add(-650 * time.Second)})
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

// The reason names the oldest message by the tracker's tier: a wait drains
// local first, so the local front can be younger than a global message.
func TestAStalledRowNamesTheOlderTiersMessage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, gjs := healthyGlobalBus(t, ctx)
	g, err := bus.globalReady(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pubEnv(t, ctx, bus.js, "agent.aae-orc.ops.operator.inbox", "m-local", "INFORM", "younger")
	pubEnv(t, ctx, gjs, g.cfg.inboxSubject(), "m-global", "INFORM", "older")
	r := newCueRig(t)
	r.started()
	r.c.ack("nonce1")
	bus.attachCue(r.c)
	now := time.Now()
	bus.pending.mark("global", pendingSample{at: now.Add(-800 * time.Second)})
	bus.pending.mark("global", pendingSample{unread: 1, tag: 1, at: now.Add(-790 * time.Second)}) // seen 800 s ago
	bus.pending.mark("local", pendingSample{at: now.Add(-700 * time.Second)})
	bus.pending.mark("local", pendingSample{unread: 1, tag: 1, at: now.Add(-690 * time.Second)}) // seen 700 s ago
	if _, err := bus.setPresence(ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	for name, row := range map[string]map[string]any{"local": presenceRow(t, ctx, bus), "global": globalRow(t, ctx, bus)} {
		reason, _ := row["cue_reason"].(string)
		if row["cue"] != cueStateStalled || !strings.HasPrefix(reason, "stalled: oldest pending m-global waiting 8") {
			t.Errorf("%s row: the reason names the global message, the older one: %v", name, row)
		}
	}
}

// The bus read marks each tier apart: a global message keeps its own first
// seen time, whatever the local count does.
func TestTheBusReadMarksEachTierApart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bus, gjs := healthyGlobalBus(t, ctx)
	g, err := bus.globalReady(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	bus.pending.mark("global", pendingSample{at: now.Add(-200 * time.Second)})
	bus.pending.mark("local", pendingSample{at: now.Add(-100 * time.Second)})
	pubEnv(t, ctx, gjs, g.cfg.inboxSubject(), "m-global", "INFORM", "hello")
	if n, err := (busCueSource{bus}).count(ctx); err != nil || n != 1 {
		t.Fatalf("count = %d, %v; want 1", n, err)
	}
	if got := bus.oldestPending(); got < 200*time.Second {
		t.Fatalf("the global message is stamped at the global tier's previous sample: want at least 200s, got %s", got)
	}
}

// The same through real dispatch (T1): a wait_for_message runs through serve,
// a second tools/call is answered while it is open, and a message is then
// delivered. Mail that arrives during the wait is not cued; what is still
// waiting when the wait ends is cued once.
func TestACueStaysSuppressedThroughARealWaitBesideASecondCall(t *testing.T) {
	p := newPipeRig(t, false)
	r := newCueRig(t)
	r.started()
	r.c.ack("nonce1")
	r.cues = nil
	p.s.cue = r.c // set before any call; the pipe write orders it before the calls read it
	r.step(2 * time.Second)

	p.wait(`41`, 10, 1)
	if !p.untilInFlight(1, 2*time.Second) {
		t.Fatal("the wait is not in flight")
	}
	p.send(`{"jsonrpc":"2.0","id":42,"method":"tools/call","params":{"name":"list_roster","arguments":{}}}`)
	if got := idOf(p.next(2 * time.Second)); got != `42` {
		t.Fatalf("the second call was not answered while the wait was open: got %s", got)
	}
	if n := p.s.inFlight(); n != 1 {
		t.Fatalf("the wait should still be open beside the second call: %d in flight", n)
	}
	r.src.n = 2 // two arrive while the wait is open
	r.step(2 * time.Second)
	if n := len(r.mailCues()); n != 0 {
		t.Fatalf("no cue while the wait is open: got %d", n)
	}

	p.publish("m-41")
	m := p.next(5 * time.Second)
	if idOf(m) != `41` || !strings.Contains(toolText(t, m), "m-41") {
		t.Fatalf("the wait did not deliver the message: %v", m)
	}
	r.src.n = 1 // the wait took one; one is still waiting
	r.step(2 * time.Second)
	cues := r.mailCues()
	if len(cues) != 1 || meta(t, cues[0])["count"] != "1" {
		t.Fatalf("the mail still waiting after the wait is cued once with count 1: %v", cues)
	}
}
