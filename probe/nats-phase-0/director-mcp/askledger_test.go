package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// The ask ledger's pure core, from recorded envelopes and no broker
// (sim/design/ask-ledger.md section 9, part A1).

var t0 = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

var seqCounter uint64

type envOpt func(*Envelope)

func inReplyTo(id string) envOpt  { return func(e *Envelope) { e.InReplyTo = id } }
func replyBy(s string) envOpt     { return func(e *Envelope) { e.ReplyBy = s } }
func senderRole(r string) envOpt  { return func(e *Envelope) { e.Sender.Role = r } }
func refs(r ...string) envOpt     { return func(e *Envelope) { e.Content.Refs = r } }
func ctype(c string) envOpt       { return func(e *Envelope) { e.Content.Type = c } }
func claimedSent(s string) envOpt { return func(e *Envelope) { e.SentAt = s } }

func rec(at time.Time, id, perf, from, to, data string, opts ...envOpt) auditRec {
	seqCounter++
	e := Envelope{
		SchemaVersion: 1,
		MessageID:     id,
		Performative:  perf,
		Sender:        Sender{AgentID: from, Workspace: "w"},
		Recipient:     Recipient{Address: to},
		Content:       Content{Type: "text", Data: data},
		SentAt:        at.UTC().Format(time.RFC3339),
	}
	for _, o := range opts {
		o(&e)
	}
	return auditRec{Broker: "local", Seq: seqCounter, At: at, Env: e}
}

const (
	owner1 = "agent://ops/builder-1"
	owner2 = "agent://ops/builder-2"
)

func keyAt(t *testing.T, rows []rollupRow, i int) string {
	t.Helper()
	if i >= len(rows) {
		t.Fatalf("rollup has %d rows, want index %d", len(rows), i)
	}
	return rows[i].Key
}

func rowOf(t *testing.T, l *askLedger, id string) *askRow {
	t.Helper()
	r := l.Rows[id]
	if r == nil {
		t.Fatalf("no row %s in %v", id, l.Rows)
	}
	return r
}

func ledgerWith(recs ...auditRec) *askLedger {
	l := newAskLedger()
	for _, r := range recs {
		l.Ingest(r)
	}
	return l
}

func builderObs() []idObs {
	return []idObs{{Agent: "builder-1", Team: "ops", Role: "builder"}, {Agent: "sup-1", Team: "ops", Role: "supervisor"}}
}

func resolved(l *askLedger, now time.Time, obs []idObs) {
	l.NotePass(now)
	l.ObserveIDs(now, obs)
	l.Resolve(now)
}

func TestARequestAloneIsSentAndAnAlarmPast15Minutes(t *testing.T) {
	l := ledgerWith(rec(t0, "m1", "REQUEST", "sup-1", owner1, "please build x", replyBy("2026-10-07T00:00:00Z")))
	resolved(l, t0.Add(14*time.Minute), builderObs())
	r := rowOf(t, l, "m1")
	if r == nil || r.State != askSent {
		t.Fatalf("row = %+v, want state sent", r)
	}
	if got := l.Report(t0.Add(14*time.Minute), nil, false).Alarms; len(got) != 0 {
		t.Fatalf("alarms at 14m = %v, want none", got)
	}
	rep := l.Report(t0.Add(16*time.Minute), nil, false)
	if len(rep.Alarms) != 1 || rep.Alarms[0].Ask != "m1" || !strings.Contains(rep.Alarms[0].Text, "unacked") ||
		!strings.Contains(rep.Alarms[0].Text, "supervisor -> builder: please build x") {
		t.Fatalf("alarms = %+v", rep.Alarms)
	}
}

func TestAnAskPastItsSellByIsAnAlarmEvenWhenRecent(t *testing.T) {
	l := ledgerWith(rec(t0, "m1", "REQUEST", "sup-1", owner1, "x", replyBy(t0.Add(5*time.Minute).Format(time.RFC3339))))
	resolved(l, t0.Add(6*time.Minute), builderObs())
	rep := l.Report(t0.Add(6*time.Minute), nil, false)
	if len(rep.Alarms) != 1 || !strings.Contains(rep.Alarms[0].Text, "sell_by") {
		t.Fatalf("alarms = %+v", rep.Alarms)
	}
}

func TestAgreeThenWorkingThenInformMovesTheRow(t *testing.T) {
	l := ledgerWith(rec(t0, "m1", "REQUEST", "sup-1", owner1, "x"))
	step := func(at time.Time, id, perf, data string, o ...envOpt) string {
		l.Ingest(rec(at, id, perf, "builder-1", "agent://ops/sup-1", data, append(o, inReplyTo("m1"))...))
		return rowOf(t, l, "m1").State
	}
	if s := step(t0.Add(time.Minute), "m2", "AGREE", "will do"); s != askAcked {
		t.Fatalf("after AGREE: %s", s)
	}
	if s := step(t0.Add(2*time.Minute), "m3", "INFORM", "working", ctype("signal")); s != askWorking {
		t.Fatalf("after working: %s", s)
	}
	if s := step(t0.Add(3*time.Minute), "m4", "INFORM", "done, see pr"); s != askAnswered {
		t.Fatalf("after INFORM: %s", s)
	}
	// A closed row stays closed.
	if s := step(t0.Add(4*time.Minute), "m5", "INFORM", "working", ctype("signal")); s != askAnswered {
		t.Fatalf("after a late status: %s", s)
	}
	if got := rowOf(t, l, "m1").StateAt; got != t0.Add(3*time.Minute).UTC().Format(time.RFC3339) {
		t.Fatalf("state_at = %s", got)
	}
}

func TestRefuseFailureAndCancelCloseTheRow(t *testing.T) {
	for perf, want := range map[string]string{"REFUSE": askRefused, "FAILURE": askFailed} {
		l := ledgerWith(rec(t0, "m1", "REQUEST", "sup-1", owner1, "x"))
		l.Ingest(rec(t0.Add(time.Minute), "m2", perf, "builder-1", "agent://ops/sup-1", "no", inReplyTo("m1")))
		if got := rowOf(t, l, "m1").State; got != want {
			t.Errorf("%s -> %s, want %s", perf, got, want)
		}
	}
	l := ledgerWith(rec(t0, "m1", "REQUEST", "sup-1", owner1, "x"))
	l.Ingest(rec(t0.Add(time.Minute), "m2", "CANCEL", "sup-1", owner1, "never mind", inReplyTo("m1")))
	if got := rowOf(t, l, "m1").State; got != askCancelled {
		t.Errorf("CANCEL from the asker -> %s", got)
	}
	l = ledgerWith(rec(t0, "m1", "REQUEST", "sup-1", owner1, "x"))
	l.Ingest(rec(t0.Add(time.Minute), "m2", "CANCEL", "intruder", owner1, "x", inReplyTo("m1")))
	if got := rowOf(t, l, "m1").State; got != askSent {
		t.Errorf("CANCEL from someone other than the asker -> %s, want sent", got)
	}
}

func TestBlockedOnAnAddressWhoseOwnRowIsBlockedBackPrintsACycle(t *testing.T) {
	l := ledgerWith(
		rec(t0, "r1", "REQUEST", "sup-1", owner1, "a"),
		rec(t0, "r2", "REQUEST", "sup-1", owner2, "b"),
	)
	l.Ingest(rec(t0.Add(time.Minute), "s1", "INFORM", "builder-1", "agent://ops/sup-1", "blocked-on "+owner2, inReplyTo("r1"), ctype("signal")))
	l.Ingest(rec(t0.Add(time.Minute), "s2", "INFORM", "builder-2", "agent://ops/sup-1", "blocked-on "+owner1, inReplyTo("r2"), ctype("signal")))
	if rowOf(t, l, "r1").State != askBlocked || rowOf(t, l, "r1").BlockedOn != owner2 {
		t.Fatalf("r1 = %+v", l.Rows["r1"])
	}
	rep := l.Report(t0.Add(2*time.Minute), nil, false)
	if len(rep.Chains) == 0 {
		t.Fatalf("no chains in %+v", rep)
	}
	cyc := false
	for _, c := range rep.Chains {
		cyc = cyc || c.Cycle
	}
	if !cyc {
		t.Fatalf("chains = %+v, want a cycle", rep.Chains)
	}
}

func TestABlockedChainThatEndsIsNotACycle(t *testing.T) {
	l := ledgerWith(rec(t0, "r1", "REQUEST", "sup-1", owner1, "a"), rec(t0, "r2", "REQUEST", "sup-1", owner2, "b"))
	l.Ingest(rec(t0.Add(time.Minute), "s1", "INFORM", "builder-1", "agent://ops/sup-1", "blocked-on "+owner2, inReplyTo("r1"), ctype("signal")))
	rep := l.Report(t0.Add(2*time.Minute), nil, false)
	if len(rep.Chains) != 1 || rep.Chains[0].Cycle || len(rep.Chains[0].Asks) != 2 {
		t.Fatalf("chains = %+v, want one chain r1 then r2, no cycle", rep.Chains)
	}
}

func TestAReplyToAnUnknownAskIsAnOrphanAndOpensNoRow(t *testing.T) {
	l := ledgerWith(rec(t0, "m2", "AGREE", "builder-1", "agent://ops/sup-1", "ok", inReplyTo("ghost")))
	if len(l.Rows) != 0 {
		t.Fatalf("rows = %v, want none", l.Rows)
	}
	rep := l.Report(t0.Add(time.Minute), nil, false)
	if rep.Gaps.Orphans["local"] != 1 {
		t.Fatalf("orphans = %v, want 1 for local", rep.Gaps.Orphans)
	}
}

func TestAMissingRequestCopyWithItsReplyPresentCountsOneOrphan(t *testing.T) {
	l := ledgerWith(
		rec(t0.Add(time.Minute), "m2", "AGREE", "builder-1", "agent://ops/sup-1", "ok", inReplyTo("m1")),
		rec(t0.Add(2*time.Minute), "m3", "INFORM", "builder-1", "agent://ops/sup-1", "done", inReplyTo("m1")),
	)
	if got := l.Report(t0.Add(3*time.Minute), nil, false).Gaps.Orphans["local"]; got != 2 {
		t.Fatalf("orphans = %d, want one per reply naming the missing ask (2 replies)", got)
	}
}

func TestAReplyToANonAskMessageIsNotAnOrphan(t *testing.T) {
	l := ledgerWith(
		rec(t0, "i1", "INFORM", "sup-1", owner1, "fyi"),
		rec(t0.Add(time.Minute), "i2", "INFORM", "builder-1", "agent://ops/sup-1", "thanks", inReplyTo("i1")),
	)
	if got := l.Report(t0.Add(2*time.Minute), nil, false).Gaps.Orphans["local"]; got != 0 {
		t.Fatalf("orphans = %d, want 0", got)
	}
}

func TestARefusedSendOnTheAuditStreamOpensNoRow(t *testing.T) {
	r := rec(t0, "m1", "REQUEST", "sup-1", owner1, "x")
	r.Refused = true
	l := ledgerWith(r, rec(t0, "m2", "REQUEST", "sup-1", owner1, "y"))
	if _, ok := l.Rows["m1"]; ok || len(l.Rows) != 1 {
		t.Fatalf("rows = %v, want only m2", l.Rows)
	}
	// A refused record is not a reply either: it does not close or orphan anything.
	rr := rec(t0, "m3", "REFUSE", "builder-1", "agent://ops/sup-1", "no", inReplyTo("m2"))
	rr.Refused = true
	l.Ingest(rr)
	if rowOf(t, l, "m2").State != askSent || l.Report(t0, nil, false).Gaps.Orphans["local"] != 0 {
		t.Fatalf("a refused record changed the ledger: %+v", rowOf(t, l, "m2"))
	}
}

func TestTheSameMessageIdFromTwoBrokersMakesOneRow(t *testing.T) {
	a := rec(t0, "m1", "REQUEST", "sup-1", owner1, "x")
	b := a
	b.Broker = "other"
	l := ledgerWith(a, b)
	if len(l.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(l.Rows))
	}
	if got := rowOf(t, l, "m1").Brokers; len(got) != 2 {
		t.Fatalf("brokers = %v, want both", got)
	}
}

func TestReadingTheSameRecordTwiceChangesNothing(t *testing.T) {
	a := rec(t0, "m1", "REQUEST", "sup-1", owner1, "x", refs("pr:a/b#1"))
	l := ledgerWith(a)
	l.Ingest(rec(t0.Add(time.Minute), "m2", "AGREE", "builder-1", "agent://ops/sup-1", "ok", inReplyTo("m1")))
	before := fmt.Sprintf("%+v", *rowOf(t, l, "m1"))
	l.Ingest(a)
	l.Ingest(rec(t0.Add(time.Minute), "m2", "AGREE", "builder-1", "agent://ops/sup-1", "ok", inReplyTo("m1")))
	if after := fmt.Sprintf("%+v", *rowOf(t, l, "m1")); after != before {
		t.Fatalf("row changed:\n%s\n%s", before, after)
	}
}

func TestAFollowUpRequestJoinsItsRow(t *testing.T) {
	l := ledgerWith(
		rec(t0, "m1", "REQUEST", "sup-1", owner1, "first", refs("pr:a/b#1")),
		rec(t0.Add(time.Minute), "m2", "REQUEST", "sup-1", owner1, "second", inReplyTo("m1"), refs("pr:a/b#2")),
	)
	if len(l.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(l.Rows))
	}
	if got := strings.Join(rowOf(t, l, "m1").Links, ","); got != "pr:a/b#1,pr:a/b#2" {
		t.Fatalf("links = %s", got)
	}
	// A reply to the follow-up lands on the same row.
	l.Ingest(rec(t0.Add(2*time.Minute), "m3", "AGREE", "builder-1", "agent://ops/sup-1", "ok", inReplyTo("m2")))
	if rowOf(t, l, "m1").State != askAcked {
		t.Fatalf("state = %s", rowOf(t, l, "m1").State)
	}
}

func TestARequestRepliedToAClosedAskOpensANewRow(t *testing.T) {
	l := ledgerWith(
		rec(t0, "m1", "REQUEST", "sup-1", owner1, "first"),
		rec(t0.Add(time.Minute), "m2", "INFORM", "builder-1", "agent://ops/sup-1", "done", inReplyTo("m1")),
		rec(t0.Add(2*time.Minute), "m3", "REQUEST", "sup-1", owner1, "again", inReplyTo("m2")),
	)
	if len(l.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(l.Rows))
	}
}

func TestARequestWithNoReplyByIsListedUnderGaps(t *testing.T) {
	l := ledgerWith(rec(t0, "m1", "REQUEST", "sup-1", owner1, "x"), rec(t0, "m2", "REQUEST", "sup-1", owner1, "y", replyBy("2026-10-07T00:00:00Z")))
	got := l.Report(t0.Add(time.Minute), nil, false).Gaps.NoReplyBy
	if len(got) != 1 || got[0] != "m1" {
		t.Fatalf("no_reply_by = %v", got)
	}
}

func TestAStreamTheReaderCannotReadIsListedAndTheReportIsPartial(t *testing.T) {
	l := ledgerWith()
	rep := l.Report(t0, []string{"AGENT_AUDIT: permissions violation"}, false)
	if !rep.Partial || len(rep.Gaps.StreamsNotRead) != 1 {
		t.Fatalf("report = %+v", rep)
	}
	if rep2 := l.Report(t0, nil, false); rep2.Partial {
		t.Fatalf("report with nothing unread is partial: %+v", rep2)
	}
}

func TestTheLineIsTheFirstLineCappedAndStrippedOfControlCharacters(t *testing.T) {
	long := strings.Repeat("a", 200)
	l := ledgerWith(rec(t0, "m1", "REQUEST", "sup-1", owner1, "\x1b[31mhello\x07 world\nsecond line"), rec(t0, "m2", "REQUEST", "sup-1", owner1, long))
	if got := rowOf(t, l, "m1").Line; got != "[31mhello world" {
		t.Fatalf("line = %q", got)
	}
	if got := len([]rune(rowOf(t, l, "m2").Line)); got != 120 {
		t.Fatalf("capped line has %d runes, want 120", got)
	}
}

// Resolution (section 4a).

func TestAnOwnerWithNoRecordRollsUpUnderUnresolvedNeverItsAgentId(t *testing.T) {
	l := ledgerWith(rec(t0, "m1", "REQUEST", "sup-1", owner1, "x"))
	resolved(l, t0.Add(time.Minute), nil)
	rep := l.Report(t0.Add(time.Minute), nil, false)
	if len(rep.ByOwner) != 1 || keyAt(t, rep.ByOwner, 0) != "unresolved" {
		t.Fatalf("by owner = %+v", rep.ByOwner)
	}
	if len(rep.Gaps.Unresolved) == 0 || !strings.Contains(rep.Gaps.Unresolved[0].Reason, "no record of this id") {
		t.Fatalf("gaps = %+v", rep.Gaps.Unresolved)
	}
	if strings.Contains(fmt.Sprintf("%+v", rep.ByOwner), "builder-1") {
		t.Fatalf("rollup carries the agent id: %+v", rep.ByOwner)
	}
	// The same ask with the owner in the id table resolves.
	resolved(l, t0.Add(2*time.Minute), builderObs())
	rep = l.Report(t0.Add(2*time.Minute), nil, false)
	if keyAt(t, rep.ByOwner, 0) != "ops/builder" {
		t.Fatalf("by owner after the table has it = %+v", rep.ByOwner)
	}
}

func TestARoleAddressResolvesTeamAndRoleFromTheAddressAlone(t *testing.T) {
	l := ledgerWith(rec(t0, "m1", "REQUEST", "sup-1", "role://ops/builder", "x"))
	resolved(l, t0.Add(time.Minute), nil)
	rep := l.Report(t0.Add(time.Minute), nil, false)
	if keyAt(t, rep.ByOwner, 0) != "ops/builder" {
		t.Fatalf("by owner = %+v", rep.ByOwner)
	}
}

func TestAGlobalPartyWithNoIdTableEntryIsUnresolved(t *testing.T) {
	l := ledgerWith(rec(t0, "m1", "REQUEST", "sup-1", "global://cluster-b/supervisor", "x"))
	resolved(l, t0.Add(time.Minute), builderObs())
	rep := l.Report(t0.Add(time.Minute), nil, false)
	if keyAt(t, rep.ByOwner, 0) != "unresolved" {
		t.Fatalf("by owner = %+v", rep.ByOwner)
	}
}

func TestASenderRoleOnTheWireIsTheAskersRoleAndItsTeamComesFromTheTable(t *testing.T) {
	l := ledgerWith(rec(t0, "m1", "REQUEST", "sup-9", owner1, "x", senderRole("supervisor")))
	resolved(l, t0.Add(time.Minute), []idObs{{Agent: "sup-9", Team: "ops", Role: ""}})
	r := rowOf(t, l, "m1")
	if r.Asker.Role != "supervisor" || r.Asker.Team != "ops" {
		t.Fatalf("asker = %+v", r.Asker)
	}
}

func TestAReplyFromAnotherAgentIdNeverStampsARoleOnTheOwner(t *testing.T) {
	l := ledgerWith(rec(t0, "m1", "REQUEST", "sup-1", owner1, "x"))
	l.Ingest(rec(t0.Add(time.Minute), "m2", "AGREE", "someone-else", "agent://ops/sup-1", "ok", inReplyTo("m1"), senderRole("builder")))
	resolved(l, t0.Add(2*time.Minute), nil)
	if got := rowOf(t, l, "m1").Owner.Role; got != roleUnresolved {
		t.Fatalf("owner role = %q, want unresolved", got)
	}
}

func TestAReplyFromTheOwnersOwnAgentIdWithARoleResolvesIt(t *testing.T) {
	l := ledgerWith(rec(t0, "m1", "REQUEST", "sup-1", owner1, "x"))
	l.Ingest(rec(t0.Add(time.Minute), "m2", "AGREE", "builder-1", "agent://ops/sup-1", "ok", inReplyTo("m1"), senderRole("builder")))
	resolved(l, t0.Add(2*time.Minute), nil)
	if got := rowOf(t, l, "m1").Owner; got.Role != "builder" || got.Team != "ops" {
		t.Fatalf("owner = %+v", got)
	}
}

func TestAReplyWithNoSenderRoleResolvesNothing(t *testing.T) {
	l := ledgerWith(rec(t0, "m1", "REQUEST", "sup-1", owner1, "x"))
	l.Ingest(rec(t0.Add(time.Minute), "m2", "AGREE", "builder-1", "agent://ops/sup-1", "ok", inReplyTo("m1")))
	resolved(l, t0.Add(2*time.Minute), nil)
	if got := rowOf(t, l, "m1").Owner.Role; got != roleUnresolved {
		t.Fatalf("owner role = %q", got)
	}
}

func TestAnAgentIdWithTwoTeamRoleEntriesIsAmbiguousNeverEither(t *testing.T) {
	l := ledgerWith(rec(t0, "m1", "REQUEST", "sup-1", owner1, "x"))
	resolved(l, t0.Add(time.Minute), []idObs{{Agent: "builder-1", Team: "ops", Role: "builder"}, {Agent: "builder-1", Team: "ops", Role: "reviewer"}})
	if got := rowOf(t, l, "m1").Owner.Role; got != roleAmbiguous {
		t.Fatalf("owner role = %q, want %q", got, roleAmbiguous)
	}
	rep := l.Report(t0.Add(time.Minute), nil, false)
	if keyAt(t, rep.ByOwner, 0) != roleAmbiguous || len(rep.Gaps.Unresolved) == 0 || !strings.Contains(rep.Gaps.Unresolved[0].Reason, "ambiguous") {
		t.Fatalf("report = %+v", rep)
	}
}

func TestADurableWithFiltersResolvesAnOwnerWithNoPresence(t *testing.T) {
	o, ok := idObsFromDurable("mcp_builder-1_01ABC", []string{"agent.w.ops.builder-1.inbox", "agent.w.ops.role.builder.inbox"})
	if !ok || o.Agent != "builder-1" || o.Team != "ops" || o.Role != "builder" {
		t.Fatalf("obs = %+v ok=%v", o, ok)
	}
	l := ledgerWith(rec(t0, "m1", "REQUEST", "sup-1", owner1, "x"))
	resolved(l, t0.Add(time.Minute), []idObs{o})
	if got := rowOf(t, l, "m1").Owner; got.Role != "builder" {
		t.Fatalf("owner = %+v", got)
	}
}

func TestAReplicaSuffixAloneNeverGivesARole(t *testing.T) {
	o, ok := idObsFromDurable("mcp_ops-builder-g9-9_01ABC", []string{"agent.w.ops.ops-builder-g9-9.inbox"})
	if !ok || o.Role != "" {
		t.Fatalf("obs = %+v ok=%v, want no role", o, ok)
	}
	l := ledgerWith(rec(t0, "m1", "REQUEST", "sup-1", "agent://ops/ops-builder-g9-9", "x"))
	resolved(l, t0.Add(time.Minute), []idObs{o})
	if got := rowOf(t, l, "m1").Owner.Role; got != roleNoneDecl {
		t.Fatalf("owner role = %q, want %q", got, roleNoneDecl)
	}
}

func TestAPresenceKeyIsReadWithItsRole(t *testing.T) {
	o, ok := idObsFromPresence("presence.ops.builder-1.01ABC", []byte(`{"agent_id":"builder-1","team":"ops","role":"builder","state":"idle"}`))
	if !ok || o.Agent != "builder-1" || o.Team != "ops" || o.Role != "builder" {
		t.Fatalf("obs = %+v ok=%v", o, ok)
	}
	if _, ok := idObsFromPresence("presence.ops.x", []byte(`{}`)); ok {
		t.Fatal("a malformed key was accepted")
	}
}

func TestASeatWithATeamAndNoRoleRollsUpUnderItsTeamAndNamesSB4(t *testing.T) {
	l := ledgerWith(rec(t0, "m1", "REQUEST", "sup-1", owner1, "x"))
	l.Ingest(rec(t0.Add(time.Minute), "m2", "AGREE", "builder-1", "agent://ops/sup-1", "ok", inReplyTo("m1")))
	resolved(l, t0.Add(2*time.Minute), []idObs{{Agent: "builder-1", Team: "ops"}})
	if got := rowOf(t, l, "m1").Owner; got.Team != "ops" || got.Role != roleNoneDecl {
		t.Fatalf("owner = %+v", got)
	}
	rep := l.Report(t0.Add(2*time.Minute), nil, false)
	if keyAt(t, rep.ByOwner, 0) != "ops/"+roleNoneDecl || !rep.Gaps.NamesSB4 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestARowFromASingleTableEntryTurnsAmbiguousWhenASecondAppearsButAWireRoleDoesNot(t *testing.T) {
	l := ledgerWith(
		rec(t0, "m1", "REQUEST", "sup-1", owner1, "x"),
		rec(t0, "m2", "REQUEST", "sup-1", "role://ops/builder", "y"),
	)
	resolved(l, t0.Add(time.Minute), builderObs())
	if rowOf(t, l, "m1").Owner.Role != "builder" {
		t.Fatalf("m1 = %+v", rowOf(t, l, "m1").Owner)
	}
	resolved(l, t0.Add(2*time.Minute), append(builderObs(), idObs{Agent: "builder-1", Team: "ops", Role: "reviewer"}))
	if got := rowOf(t, l, "m1").Owner.Role; got != roleAmbiguous {
		t.Fatalf("m1 after a second entry = %q, want ambiguous", got)
	}
	if got := rowOf(t, l, "m2").Owner.Role; got != "builder" {
		t.Fatalf("m2 (resolved from the wire) = %q, want builder", got)
	}
}

// the id table with A live t0..t3 and B first seen at t1: after A expires
// only an ask sent after A's last-seen time (plus the margin) resolves to B.
func expiryLedger(t *testing.T, asks map[string]time.Time) (*askLedger, time.Time) {
	t.Helper()
	l := newAskLedger()
	a := idObs{Agent: "builder-1", Team: "ops", Role: "builder"}
	b := idObs{Agent: "builder-1", Team: "ops", Role: "reviewer"}
	t1 := t0.Add(time.Minute)
	t3 := t0.Add(5 * time.Minute)
	for id, at := range asks {
		l.Ingest(rec(at, id, "REQUEST", "sup-1", owner1, id))
	}
	l.NotePass(t0)
	l.ObserveIDs(t0, []idObs{a})
	l.NotePass(t1)
	l.ObserveIDs(t1, []idObs{a, b})
	l.NotePass(t3)
	l.ObserveIDs(t3, []idObs{a, b}) // A last seen at t3
	return l, t3
}

func TestOnlyAnAskSentAfterTheExpiredEntrysLastSeenPlusMarginResolvesToTheSurvivor(t *testing.T) {
	t3 := t0.Add(5 * time.Minute)
	boundary := t3.Add(resolveMargin)
	asks := map[string]time.Time{
		"t2":       t0.Add(2 * time.Minute),
		"atT3":     t3,
		"onEdge":   boundary,
		"justPast": boundary.Add(time.Nanosecond),
		"t4":       t3.Add(10 * time.Minute),
	}
	l, _ := expiryLedger(t, asks)
	now := t3.Add(11 * time.Minute)
	l.NotePass(now)
	l.ObserveIDs(now, []idObs{{Agent: "builder-1", Team: "ops", Role: "reviewer"}}) // A has expired
	l.Resolve(now)
	want := map[string]string{"t2": roleAmbiguous, "atT3": roleAmbiguous, "onEdge": roleAmbiguous, "justPast": "reviewer", "t4": "reviewer"}
	for id, w := range want {
		if got := rowOf(t, l, id).Owner.Role; got != w {
			t.Errorf("ask %s: owner role = %q, want %q", id, got, w)
		}
	}
}

func TestTheStreamTimestampDecidesNotTheSendersClaimedSentAt(t *testing.T) {
	t3 := t0.Add(5 * time.Minute)
	l, _ := expiryLedger(t, nil)
	// The stream saw it before the boundary; the sender claims a time after it.
	l.Ingest(rec(t3.Add(time.Minute), "m1", "REQUEST", "sup-1", owner1, "x", claimedSent(t3.Add(time.Hour).Format(time.RFC3339))))
	now := t3.Add(11 * time.Minute)
	l.NotePass(now)
	l.ObserveIDs(now, []idObs{{Agent: "builder-1", Team: "ops", Role: "reviewer"}})
	l.Resolve(now)
	if got := rowOf(t, l, "m1").Owner.Role; got != roleAmbiguous {
		t.Fatalf("owner role = %q, want ambiguous (stream time is inside the margin)", got)
	}
}

func TestTheExpiredEntrysLastSeenSurvivesItsDropFromTheIdTable(t *testing.T) {
	l, t3 := expiryLedger(t, map[string]time.Time{"t2": t0.Add(2 * time.Minute)})
	now := t3.Add(11 * time.Minute)
	l.NotePass(now)
	l.ObserveIDs(now, []idObs{{Agent: "builder-1", Team: "ops", Role: "reviewer"}})
	l.Resolve(now)
	later := t3.Add(idKeep + 24*time.Hour)
	l.NotePass(later)
	l.ObserveIDs(later, []idObs{{Agent: "builder-1", Team: "ops", Role: "reviewer"}})
	l.Sweep(later)
	for _, e := range l.IDs {
		if e.Role == "builder" {
			t.Fatalf("the expired entry is still in the table: %+v", e)
		}
	}
	l.Resolve(later)
	if got := rowOf(t, l, "t2").Owner.Role; got != roleAmbiguous {
		t.Fatalf("t2 owner role after the drop = %q, want ambiguous", got)
	}
}

func TestTwoPassesFiveMinutesApartListReaderDown(t *testing.T) {
	l := newAskLedger()
	l.NotePass(t0)
	l.NotePass(t0.Add(30 * time.Second))
	l.NotePass(t0.Add(5*time.Minute + 30*time.Second))
	rep := l.Report(t0.Add(6*time.Minute), nil, false)
	if len(rep.Gaps.ReaderDown) != 1 || !strings.Contains(rep.Gaps.ReaderDown[0], "reader down") {
		t.Fatalf("reader_down = %v", rep.Gaps.ReaderDown)
	}
}

func TestAReaderThatHasNotRunSinceIsListedDownNow(t *testing.T) {
	l := newAskLedger()
	l.NotePass(t0)
	rep := l.Report(t0.Add(10*time.Minute), nil, false)
	if len(rep.Gaps.ReaderDown) != 1 {
		t.Fatalf("reader_down = %v, want the open gap", rep.Gaps.ReaderDown)
	}
}

// Aging out.

func TestAnOpenRowQuietFor72HoursLeavesTheAlarmsIsCountedStaleAndDropsThirtyDaysLater(t *testing.T) {
	l := ledgerWith(rec(t0, "m1", "REQUEST", "sup-1", owner1, "x"))
	resolved(l, t0, builderObs())
	at := t0.Add(staleAfter + time.Hour)
	l.Sweep(at)
	rep := l.Report(at, nil, false)
	if len(rep.Alarms) != 0 || rep.Gaps.Stale != 1 || rep.Gaps.StaleOldestSec == 0 {
		t.Fatalf("report = %+v", rep.Gaps)
	}
	if all := l.Report(at, nil, true); len(all.Alarms) != 1 {
		t.Fatalf("--all alarms = %+v, want the stale row listed", all.Alarms)
	}
	later := t0.Add(staleAfter + closedKeep + time.Hour)
	l.Sweep(later)
	if _, ok := l.Rows["m1"]; ok {
		t.Fatal("the stale row was not dropped")
	}
	if got := l.Report(later, nil, false).Gaps.Dropped; got != 1 {
		t.Fatalf("dropped = %d, want 1 in that pass", got)
	}
	l.Sweep(later.Add(time.Minute))
	if got := l.Report(later, nil, false).Gaps.Dropped; got != 0 {
		t.Fatalf("dropped = %d on the next pass, want 0", got)
	}
}

func TestAClosedRowIsKeptThirtyDaysThenDropped(t *testing.T) {
	l := ledgerWith(rec(t0, "m1", "REQUEST", "sup-1", owner1, "x"))
	l.Ingest(rec(t0.Add(time.Minute), "m2", "INFORM", "builder-1", "agent://ops/sup-1", "done", inReplyTo("m1")))
	l.Sweep(t0.Add(closedKeep - time.Hour))
	if _, ok := l.Rows["m1"]; !ok {
		t.Fatal("dropped too early")
	}
	l.Sweep(t0.Add(closedKeep + 2*time.Minute))
	if _, ok := l.Rows["m1"]; ok {
		t.Fatal("kept too long")
	}
}

func TestRollupsAreByRoleAndTeamAndCountOpenUnackedBlockedAndOldest(t *testing.T) {
	l := ledgerWith(
		rec(t0, "r1", "REQUEST", "sup-1", owner1, "a"),
		rec(t0.Add(time.Minute), "r2", "REQUEST", "sup-1", owner1, "b"),
	)
	l.Ingest(rec(t0.Add(2*time.Minute), "s1", "INFORM", "builder-1", "agent://ops/sup-1", "blocked-on pr:x", inReplyTo("r2"), ctype("signal")))
	resolved(l, t0.Add(3*time.Minute), builderObs())
	rep := l.Report(t0.Add(10*time.Minute), nil, false)
	if len(rep.ByOwner) != 1 {
		t.Fatalf("by owner = %+v", rep.ByOwner)
	}
	g := rep.ByOwner[0]
	if g.Key != "ops/builder" || g.Open != 2 || g.Unacked != 1 || g.Blocked != 1 || g.OldestOpenSec != 600 {
		t.Fatalf("rollup = %+v", g)
	}
	if len(rep.ByAsker) != 1 || rep.ByAsker[0].Key != "ops/supervisor" {
		t.Fatalf("by asker = %+v", rep.ByAsker)
	}
}

// Review items 2 to 4 are doc and help-text fixes.

func TestTheHelpTextSaysAHubAckTimeoutShowsItsReplyAsAnOrphan(t *testing.T) {
	h := asksHelp()
	for _, w := range []string{"ack timeout", "refused", "orphan"} {
		if !strings.Contains(h, w) {
			t.Errorf("asks help lacks %q:\n%s", w, h)
		}
	}
}

func designDoc(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../../sim/design/ask-ledger.md")
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(strings.Fields(string(b)), " ")
}

func TestTheDesignDocNoLongerOverstatesTheReRead(t *testing.T) {
	if strings.Contains(designDoc(t), "can still be re-read from the stream for as long as the row is kept") {
		t.Fatal("the overstatement is still in the design doc")
	}
}

func TestTheDesignDocCitesDeclaredGoAtTheRightLine(t *testing.T) {
	d := designDoc(t)
	if strings.Contains(d, "declared.go:113-114") || !strings.Contains(d, "declared.go:120") {
		t.Fatal("the declared.go cite is not corrected to :120")
	}
}

func TestTheDesignDocSaysAHubAckTimeoutShowsItsReplyAsAnOrphan(t *testing.T) {
	d := designDoc(t)
	if !strings.Contains(d, "ack timeout") || !strings.Contains(d, "orphan") {
		t.Fatal("the design doc does not name the ack timeout case")
	}
}
