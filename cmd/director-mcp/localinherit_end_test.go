package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Cases for the inheritance beyond the gated red (director#220, T-A2a): the
// trigger counter pinned as a unit, the durable-name parser, the end rules, the
// stream-less start, and the counts. Each pins a branch a mutant left alive or
// a rule the design states.

// The trigger counts successful empty reads in a row (design 3b): empty, empty,
// failed, empty does not trigger, and three empties in a row do.
func TestTheLineageCounterTriggersOnThreeSuccessfulEmptyReadsInARow(t *testing.T) {
	run := func(reads ...lineageRead) (fired bool, at int) {
		var c lineageCounter
		for i, r := range reads {
			if c.observe(r) {
				return true, i + 1
			}
		}
		return false, 0
	}
	if f, _ := run(readGone, readGone, readUnknown, readGone); f {
		t.Error("empty, empty, failed, empty triggered")
	}
	if f, _ := run(readGone, readGone, readLive, readGone, readGone); f {
		t.Error("a live row between empties did not reset the run")
	}
	if f, at := run(readGone, readGone, readGone); !f || at != 3 {
		t.Errorf("three empties in a row: fired %v at read %d, want true at 3", f, at)
	}
	if f, at := run(readGone, readGone, readUnknown, readGone, readGone, readGone); !f || at != 6 {
		t.Errorf("the run restarts after a failure: fired %v at read %d, want true at 6", f, at)
	}
	if f, _ := run(readUnknown, readUnknown, readUnknown, readUnknown); f {
		t.Error("failed reads alone triggered")
	}
}

// The durable-name parser (design 4d): the prefix names one id, an id that
// merely starts with another's name is not it, and the name after the prefix is
// a 26-character ULID with an optional known suffix.
func TestSeatDurableNamesParseAgainstTheirOwnPrefixOnly(t *testing.T) {
	const ulid = "01M7PCJZVF44DERD6Z9NXHM58Z"
	for _, c := range []struct {
		name, prefix string
		inst         string
		ok           bool
	}{
		{"mcp_sup_" + ulid, "mcp_sup_", ulid, true},
		{"mcp_sup_" + ulid + "-role", "mcp_sup_", ulid, true},
		{"mcp_sup_" + ulid + "-inherit", "mcp_sup_", ulid, true},
		{"mcp_sup_T1_" + ulid, "mcp_sup_", "", false},
		{"mcp_sup_T1_" + ulid, "mcp_sup_T1_", ulid, true},
		{"mcp_sup_" + ulid + "-other", "mcp_sup_", "", false},
		{"mcp_sup_" + ulid[:25], "mcp_sup_", "", false},
		{"mcp_sup_" + strings.ToLower(ulid), "mcp_sup_", "", false},
		{"peek_" + ulid, "mcp_sup_", "", false},
	} {
		inst, ok := seatDurableInstance(c.name, c.prefix)
		if ok != c.ok || inst != c.inst {
			t.Errorf("seatDurableInstance(%q, %q) = %q, %v; want %q, %v", c.name, c.prefix, inst, ok, c.inst, c.ok)
		}
	}
}

// A lineage value that is not a valid identity is no lineage: it would be built
// into a subject and a durable filter.
func TestAnInvalidLineageNameIsNoLineage(t *testing.T) {
	for _, v := range []string{"a.b", "*", "x y", "ws/>", "ws/"} {
		t.Setenv("DIRECTOR_PREDECESSOR", v)
		t.Setenv("MARVEL_PREDECESSOR", "")
		if got := predecessorID("me"); got != "" {
			t.Errorf("predecessorID with DIRECTOR_PREDECESSOR=%q = %q, want none", v, got)
		}
	}
}

// A predecessor that left no durable and whose mail the stream still holds: the
// successor reads all of it, and the note says the durable was absent.
func TestAPredecessorWithNoDurableIsInheritedFromTheStreamStart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	publishN(t, ctx, js, lineInbox("ghost"), "g", 1, 3)

	succ := lineConnect(t, ctx, url, "succ", "", "ghost")
	runHeartbeat(t, ctx, succ, 40*time.Millisecond)
	if !waitFor(5*time.Second, func() bool { return len(inheritDurables(t, ctx, js, "succ")) == 1 }) {
		t.Fatal("nothing inherited from a predecessor with no durable")
	}
	res, err := succ.receiveBatch(ctx, 3*time.Second, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := lineIDs(lineItems(t, res)); got != "g1,g2,g3" {
		t.Errorf("received %q, want g1,g2,g3", got)
	}
	notes := strings.Join(succ.takeResumed(), "\n")
	if !strings.Contains(notes, "left no durable") {
		t.Errorf("the resume note does not say the predecessor left no durable: %q", notes)
	}
}

// Design 3a: a predecessor row that reappears after inheritance started stops
// the inherited durable at once, and the note says why.
func TestAPredecessorThatReturnsStopsTheInheritance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	pred := lineConnect(t, ctx, url, "pred", "", "")
	publishN(t, ctx, js, lineInbox("pred"), "p", 1, 3)
	lineExit(t, ctx, pred)

	succ := lineConnect(t, ctx, url, "succ", "", "pred")
	runHeartbeat(t, ctx, succ, 40*time.Millisecond)
	if !waitFor(5*time.Second, func() bool { return len(inheritDurables(t, ctx, js, "succ")) == 1 }) {
		t.Fatal("nothing inherited")
	}
	succ.takeResumed()
	lineConnect(t, ctx, url, "pred", "", "") // the predecessor's row is back
	if !waitFor(5*time.Second, func() bool { return len(inheritDurables(t, ctx, js, "succ")) == 0 }) {
		t.Fatal("the inherited durable survived the predecessor's return")
	}
	if notes := strings.Join(succ.takeResumed(), "\n"); !strings.Contains(notes, "live again") {
		t.Errorf("the resume note does not say why it stopped: %q", notes)
	}
	if res, err := succ.receiveBatch(ctx, 500*time.Millisecond, 10); err != nil || len(res.Items) != 0 {
		t.Errorf("mail was read after the inheritance stopped: %v (err %v)", lineIDs(lineItems(t, res)), err)
	}
}

// Design 4: the inherited durable is deleted once it drains with the
// predecessor still absent.
func TestTheInheritedDurableIsDeletedAfterItDrains(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	pred := lineConnect(t, ctx, url, "pred", "", "")
	publishN(t, ctx, js, lineInbox("pred"), "p", 1, 2)
	lineExit(t, ctx, pred)

	succ := lineConnect(t, ctx, url, "succ", "", "pred")
	runHeartbeat(t, ctx, succ, 40*time.Millisecond)
	if !waitFor(5*time.Second, func() bool { return len(inheritDurables(t, ctx, js, "succ")) == 1 }) {
		t.Fatal("nothing inherited")
	}
	// while it holds mail it stays.
	time.Sleep(400 * time.Millisecond)
	if d := inheritDurables(t, ctx, js, "succ"); len(d) != 1 {
		t.Fatalf("the inherited durable was deleted while it still held mail: %v", d)
	}
	res, err := succ.receiveBatch(ctx, 3*time.Second, 10)
	if err != nil || len(res.Items) != 2 {
		t.Fatalf("drain read %d (err %v), want 2", len(res.Items), err)
	}
	if !waitFor(5*time.Second, func() bool { return len(inheritDurables(t, ctx, js, "succ")) == 0 }) {
		t.Fatal("the inherited durable was not deleted after it drained")
	}
	if notes := strings.Join(succ.takeResumed(), "\n"); !strings.Contains(notes, "drained") {
		t.Errorf("the resume note does not say it drained: %q", notes)
	}
}

// The arm expires at the stream's max age: before the inheritance starts, it
// never starts; after, it ends.
func TestTheArmExpiresAtTheStreamsMaxAge(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	pred := lineConnect(t, ctx, url, "pred", "", "")
	publishN(t, ctx, js, lineInbox("pred"), "p", 1, 2)
	lineExit(t, ctx, pred)

	succ := lineConnect(t, ctx, url, "succ", "", "pred")
	succ.inh.mu.Lock()
	succ.inh.armedAt = time.Now().Add(-succ.inh.maxAge - time.Minute)
	succ.inh.mu.Unlock()
	runHeartbeat(t, ctx, succ, 40*time.Millisecond)
	time.Sleep(600 * time.Millisecond)
	if d := inheritDurables(t, ctx, js, "succ"); len(d) != 0 {
		t.Errorf("an expired arm started an inheritance: %v", d)
	}
}

// The counts: inherited mail is waiting mail, so remaining names it under its
// own key and a drain stamp is not written while it waits; the summary lists it
// and counts it apart.
func TestInheritedMailIsCountedAsWaiting(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	pred := lineConnect(t, ctx, url, "pred", "", "")
	publishN(t, ctx, js, lineInbox("pred"), "p", 1, 3)
	lineExit(t, ctx, pred)

	succ := lineConnect(t, ctx, url, "succ", "", "pred")
	runHeartbeat(t, ctx, succ, 40*time.Millisecond)
	if !waitFor(5*time.Second, func() bool { return len(inheritDurables(t, ctx, js, "succ")) == 1 }) {
		t.Fatal("nothing inherited")
	}
	rem := succ.remaining(ctx)
	if rem["inherited"] != 3 {
		t.Errorf("remaining = %v, want inherited 3", rem)
	}
	if drainedClean(rem) {
		t.Error("an inbox with inherited mail waiting reads as drained")
	}
	sum, err := succ.summarizeInbox(ctx, summaryDefault)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Read["inherited"] != 3 || sum.Expected["inherited"] != 3 {
		t.Errorf("summary counts read %v expected %v, want inherited 3", sum.Read, sum.Expected)
	}
	if sum.Read["local"] != 0 {
		t.Errorf("inherited mail was counted as local: %v", sum.Read)
	}
}

// A durable of another id, or one that does not filter the predecessor's
// subject, sets no floor (design 3's containment, by exact membership: a
// wildcard filter never counts).
func TestOnlyADurableThatFiltersTheSubjectSetsItsFloor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	pred := lineConnect(t, ctx, url, "pred", "", "")
	publishN(t, ctx, js, lineInbox("pred"), "p", 1, 4)
	lineReadAck(t, ctx, pred, 4)
	instance := pred.instance
	lineExit(t, ctx, pred)

	got := func(subject string) (uint64, bool) {
		f, found, err := subjectFloor(ctx, js, "AGENT_INBOX", "mcp_pred_", subject, nil)
		if err != nil {
			t.Fatal(err)
		}
		return f, found
	}
	if f, found := got(lineInbox("pred")); !found || f != 4 {
		t.Fatalf("control: floor for the predecessor's own subject = %d, %v; want 4, true (instance %s)", f, found, instance)
	}
	if f, found := got(lineInbox("other")); found || f != 0 {
		t.Errorf("a subject the durable does not filter got floor %d, found %v", f, found)
	}
	if f, found := got("agent." + lineWS + "." + lineTeam + ".>"); found || f != 0 {
		t.Errorf("a wildcard counted as containing a subject: floor %d, found %v", f, found)
	}
}

// A wait that is already blocking when the inheritance starts still returns the
// inherited mail before it gives up: the durable did not exist when the call
// began, so the one look after the poll is what delivers it.
func TestAWaitThatBeganBeforeTheInheritanceReturnsTheInheritedMail(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	pred := lineConnect(t, ctx, url, "pred", "", "")
	publishN(t, ctx, js, lineInbox("pred"), "p", 1, 3)
	lineExit(t, ctx, pred)

	succ := lineConnect(t, ctx, url, "succ", "", "pred")
	runHeartbeat(t, ctx, succ, 40*time.Millisecond)
	if d := inheritDurables(t, ctx, js, "succ"); len(d) != 0 {
		t.Fatalf("CONTROL FAILED: the inheritance had started before the wait: %v", d)
	}
	res, err := succ.receiveBatch(ctx, 2*time.Second, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := lineIDs(lineItems(t, res)); got != "p1,p2,p3" {
		t.Errorf("the wait that began before the inheritance returned %q, want p1,p2,p3", got)
	}
}

// Reading inherited mail must not move the position the seat's own durable
// rebuilds from: the sequences are the predecessor's, not the seat's own acks.
func TestReadingInheritedMailDoesNotMoveTheOwnRebuildPosition(t *testing.T) {
	old := durableCheckEvery
	durableCheckEvery = 0
	defer func() { durableCheckEvery = old }()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	pred := lineConnect(t, ctx, url, "pred", "", "")
	succ := lineConnect(t, ctx, url, "succ", "", "pred")
	publishN(t, ctx, js, lineInbox("succ"), "s", 1, 1) // sequence 1, the seat's own
	publishN(t, ctx, js, lineInbox("pred"), "p", 1, 3) // sequences 2 to 4
	lineExit(t, ctx, pred)
	runHeartbeat(t, ctx, succ, 40*time.Millisecond)
	if !waitFor(5*time.Second, func() bool { return len(inheritDurables(t, ctx, js, "succ")) == 1 }) {
		t.Fatal("nothing inherited")
	}
	var res batchResult
	succ.drainInherited(ctx, 10, &res) // the inherited mail only, the own s1 left unread
	if len(res.Items) != 3 {
		t.Fatalf("setup: read %d inherited messages, want 3", len(res.Items))
	}
	if err := js.DeleteConsumer(ctx, "AGENT_INBOX", "mcp_succ_"+succ.instance); err != nil {
		t.Fatal(err)
	}
	got, err := succ.receiveBatch(ctx, 5*time.Second, 10)
	if err != nil {
		t.Fatal(err)
	}
	if ids := lineIDs(lineItems(t, got)); !strings.Contains(ids, "s1") {
		t.Errorf("after the own durable was rebuilt the seat's own unread s1 was skipped: got %q", ids)
	}
}

// A live durable of the predecessor's id is a sibling's position, not a floor to
// take (the rule seatAckFloor already applies): it is skipped by its instance.
func TestALiveDurableOfThePredecessorIdSetsNoFloor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	a := lineConnect(t, ctx, url, "pred", "", "")
	publishN(t, ctx, js, lineInbox("pred"), "p", 1, 4)
	lineReadAck(t, ctx, a, 2)
	lineExit(t, ctx, a)
	b := lineConnect(t, ctx, url, "pred", "", "") // a second instance of the id, live
	lineReadAck(t, ctx, b, 2)                     // it resumed after the departed floor, so 2 remain

	floor := func(live func(string) bool) uint64 {
		f, _, err := subjectFloor(ctx, js, "AGENT_INBOX", "mcp_pred_", lineInbox("pred"), live)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	if f := floor(nil); f != 4 {
		t.Fatalf("CONTROL FAILED: with nothing skipped the floor is %d, want 4", f)
	}
	if f := floor(func(inst string) bool { return inst == b.instance }); f != 2 {
		t.Errorf("floor with the live instance skipped = %d, want 2 (the departed instance's)", f)
	}
}

// Mail fetched and not yet acked is still the inherited durable's: it has not
// drained, and must not be deleted out from under a held message.
func TestAnInheritedDurableHoldingUnackedMailHasNotDrained(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	pred := lineConnect(t, ctx, url, "pred", "", "")
	publishN(t, ctx, js, lineInbox("pred"), "p", 1, 2)
	lineExit(t, ctx, pred)

	succ := lineConnect(t, ctx, url, "succ", "", "pred")
	runHeartbeat(t, ctx, succ, 40*time.Millisecond)
	if !waitFor(5*time.Second, func() bool { return len(inheritDurables(t, ctx, js, "succ")) == 1 }) {
		t.Fatal("nothing inherited")
	}
	cons, _ := succ.inheritCons()
	batch, err := cons.FetchNoWait(10)
	if err != nil {
		t.Fatal(err)
	}
	var held []interface{ Ack() error }
	for m := range batch.Messages() {
		held = append(held, m)
	}
	if len(held) != 2 {
		t.Fatalf("setup: fetched %d, want 2", len(held))
	}
	if succ.inheritDrained(ctx) {
		t.Error("a durable holding 2 unacked messages reads as drained")
	}
	time.Sleep(400 * time.Millisecond)
	if d := inheritDurables(t, ctx, js, "succ"); len(d) != 1 {
		t.Errorf("the inherited durable was deleted while it held unacked mail: %v", d)
	}
	for _, m := range held {
		if err := m.Ack(); err != nil {
			t.Fatal(err)
		}
	}
	if !waitFor(5*time.Second, func() bool { return succ.inheritDrained(ctx) }) {
		t.Error("after the acks the durable still reads as not drained")
	}
}

// A predecessor whose durable exists but acked nothing: read from the start of
// what the stream holds, and the note says that and not "left no durable".
func TestAPredecessorThatAckedNothingIsInheritedFromTheStreamStart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	pred := lineConnect(t, ctx, url, "pred", "", "")
	publishN(t, ctx, js, lineInbox("pred"), "p", 1, 2)
	lineExit(t, ctx, pred)

	succ := lineConnect(t, ctx, url, "succ", "", "pred")
	runHeartbeat(t, ctx, succ, 40*time.Millisecond)
	if !waitFor(5*time.Second, func() bool { return len(inheritDurables(t, ctx, js, "succ")) == 1 }) {
		t.Fatal("nothing inherited")
	}
	notes := strings.Join(succ.takeResumed(), "\n")
	if !strings.Contains(notes, "acked nothing") || strings.Contains(notes, "left no durable") {
		t.Errorf("the note for a predecessor that acked nothing reads %q", notes)
	}
}
