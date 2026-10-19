package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// Build items 5 and the turn-on rules of design section 4: held mail, release
// on return, and the first-sighting rule. All on the leaf rig with an injected
// clock; a refused send is held only on a director shim with the watch on.

func (fx *watchFx) stream(cluster string) jetstream.Stream {
	fx.t.Helper()
	st, err := fx.gjs.CreateStream(fx.ctx, jetstream.StreamConfig{Name: globalStreamPrefix + cluster, Subjects: []string{"global." + cluster + ".>"}})
	if err != nil {
		fx.t.Fatal(err)
	}
	return st
}

func (fx *watchFx) msgs(cluster string) uint64 {
	fx.t.Helper()
	st, err := fx.gjs.Stream(fx.ctx, globalStreamPrefix+cluster)
	if err != nil {
		fx.t.Fatal(err)
	}
	info, err := st.Info(fx.ctx)
	if err != nil {
		fx.t.Fatal(err)
	}
	return info.State.Msgs
}

func (fx *watchFx) send(args map[string]any) (any, error) {
	fx.t.Helper()
	if _, ok := args["performative"]; !ok {
		args["performative"] = "INFORM"
	}
	if _, ok := args["text"]; !ok {
		args["text"] = "held test message"
	}
	raw, _ := json.Marshal(args)
	return toolSend(fx.ctx, fx.bus, raw)
}

func (fx *watchFx) held() []heldRec {
	fx.t.Helper()
	recs, err := newHeldStore(fx.dir).Current()
	if err != nil {
		fx.t.Fatal(err)
	}
	return recs
}

func (fx *watchFx) heldFileExists() bool {
	_, err := os.Stat(filepath.Join(fx.dir, "held.jsonl"))
	return err == nil
}

func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("result is %T, want a map", v)
	}
	return m
}

// otherCluster puts a live row on c2, so a send to c1 is refused as "nothing
// is registered under presence.c1." and not as an empty bucket.
func (fx *watchFx) otherCluster() {
	fx.put("c2", "z", "working")
}

func TestHoldOffKeepsTodaysRefusal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	fx := newWatchFx(t, ctx, 0, false)
	fx.otherCluster()
	_, err := fx.send(map[string]any{"to": "global://c1/supervisor"})
	if err == nil || !strings.Contains(err.Error(), "nothing is registered under") {
		t.Fatalf("with the watch off the send is refused as on main, got %v", err)
	}
	if fx.heldFileExists() {
		t.Fatal("with the watch off nothing is written to held.jsonl")
	}
}

func TestHoldOnNoPresenceSaysHeldNotDelivered(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	fx := newWatchFx(t, ctx, 0, true)
	fx.otherCluster()
	out, err := fx.send(map[string]any{"to": "global://c1/supervisor"})
	if err != nil {
		t.Fatalf("a send refused for absence is held, not an error: %v", err)
	}
	m := asMap(t, out)
	if m["status"] != "held, not delivered" {
		t.Fatalf("status = %v, want \"held, not delivered\" and never accepted for delivery (R-08)", m["status"])
	}
	id, _ := m["hold_id"].(string)
	if id == "" || id != m["message_id"] {
		t.Fatalf("hold_id %q should be the envelope message_id %v", id, m["message_id"])
	}
	if r, _ := m["reason"].(string); !strings.Contains(r, "nothing is registered under") {
		t.Fatalf("the reason should carry the refusal text, got %q", r)
	}
	recs := fx.held()
	if len(recs) != 1 || recs[0].State != holdHeld || recs[0].ID != id || recs[0].Envelope == nil || recs[0].To != "global://c1/supervisor" {
		t.Fatalf("held.jsonl = %+v, want one held record for %s with its envelope", recs, id)
	}
}

func TestFailFastKeepsRefusal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	fx := newWatchFx(t, ctx, 0, true)
	fx.otherCluster()
	// control: the same send without fail_fast is held.
	if out, err := fx.send(map[string]any{"to": "global://c1/supervisor"}); err != nil || asMap(t, out)["status"] != "held, not delivered" {
		t.Fatalf("CONTROL FAILED: the send should be held, got %v %v", out, err)
	}
	before := len(fx.held())
	_, err := fx.send(map[string]any{"to": "global://c1/supervisor", "fail_fast": true})
	if err == nil || !strings.Contains(err.Error(), "nothing is registered under") {
		t.Fatalf("fail_fast keeps today's refusal, got %v", err)
	}
	if len(fx.held()) != before {
		t.Fatal("a fail_fast send was held")
	}
}

func TestNoHoldWhenPresencePassedAndPublishFailed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	fx := newWatchFx(t, ctx, 0, true)
	fx.put("c1", "s1", "working") // presence passes; the cluster's stream does not exist yet
	_, err := fx.send(map[string]any{"to": "global://c1/supervisor"})
	if err == nil {
		t.Fatal("a publish to a missing stream should fail")
	}
	if len(fx.held()) != 0 {
		t.Fatalf("a publish that failed after presence passed was held: %+v", fx.held())
	}
	// control: with the stream present the same send is accepted, so the failure
	// above was the stream and not the send.
	fx.stream("c1")
	out, err := fx.send(map[string]any{"to": "global://c1/supervisor"})
	if err != nil || asMap(t, out)["status"] != "accepted for delivery" {
		t.Fatalf("CONTROL FAILED: with the stream the send should be accepted, got %v %v", out, err)
	}
}

func TestNoHoldOnTwoDirectors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	fx := newWatchFx(t, ctx, 0, true)
	dir := func(inst string) {
		fx.putKey("presence.director."+inst, map[string]any{"agent_id": "operator", "instance": inst, "role": "director", "ts": fx.clk.wall.Format(time.RFC3339)})
	}
	dir("i1")
	// control: one director live, the send goes out.
	if out, err := fx.send(map[string]any{"to": "global://director"}); err != nil || asMap(t, out)["status"] != "accepted for delivery" {
		t.Fatalf("CONTROL FAILED: one live director should accept, got %v %v", out, err)
	}
	dir("i2")
	_, err := fx.send(map[string]any{"to": "global://director"})
	if err == nil || !strings.Contains(err.Error(), "R-78") {
		t.Fatalf("two directors must refuse (R-78), got %v", err)
	}
	if len(fx.held()) != 0 {
		t.Fatalf("a two-director refusal was held: %+v", fx.held())
	}
}

// returned brings c1 back: its supervisor row beats on every tick.
func (fx *watchFx) beatTicks(n int) {
	fx.t.Helper()
	for i := 0; i < n; i++ {
		fx.put("c1", "s1", "working")
		fx.tick()
	}
}

func TestReleaseOnBackPublishesOnceWithHoldID(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	fx := newWatchFx(t, ctx, 0, true)
	fx.stream("c1")
	fx.otherCluster()
	out, err := fx.send(map[string]any{"to": "global://c1/supervisor"})
	if err != nil {
		t.Fatal(err)
	}
	id := asMap(t, out)["hold_id"].(string)
	// control: while the cluster is absent, ticks release nothing.
	fx.tick()
	fx.tick()
	if fx.msgs("c1") != 0 || fx.held()[0].State != holdHeld {
		t.Fatalf("CONTROL FAILED: an absent cluster must not be released to: msgs %d, %+v", fx.msgs("c1"), fx.held())
	}
	fx.beatTicks(3)
	if n := fx.msgs("c1"); n != 1 {
		t.Fatalf("stream holds %d messages for c1, want exactly 1", n)
	}
	st, _ := fx.gjs.Stream(ctx, globalStreamPrefix+"c1")
	raw, err := st.GetLastMsgForSubject(ctx, "global.c1.supervisor.inbox")
	if err != nil || raw.Header.Get(jetstream.MsgIDHeader) != id {
		t.Fatalf("the released message must carry the hold id %s as its message id, got %v %v", id, raw, err)
	}
	recs := fx.held()
	if len(recs) != 1 || recs[0].State != holdReleased || recs[0].Stream != globalStreamPrefix+"c1" || recs[0].Seq == 0 {
		t.Fatalf("record = %+v, want released with the hub's stream and sequence", recs)
	}
	fx.beatTicks(2)
	if fx.msgs("c1") != 1 {
		t.Fatal("a released record was published again")
	}
}

func TestReleaseCrashBeforeRecordIsDeduped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	fx := newWatchFx(t, ctx, 0, true)
	fx.stream("c1")
	fx.otherCluster()
	if _, err := fx.send(map[string]any{"to": "global://c1/supervisor"}); err != nil {
		t.Fatal(err)
	}
	// the append that follows a release fails, as a crash between the publish
	// and the record would: the record stays held and the next tick publishes
	// the same id again, which the hub's dedupe window drops.
	if fx.bus.watch == nil || fx.bus.watch.hold == nil {
		t.Fatal("the watch has no held store")
	}
	fx.bus.watch.hold.appendHook = func(r heldRec) error {
		if r.State == holdReleased {
			return os.ErrClosed
		}
		return nil
	}
	fx.beatTicks(3)
	if n := fx.msgs("c1"); n != 1 {
		t.Fatalf("after the first release attempt the stream holds %d, want 1", n)
	}
	if fx.held()[0].State != holdHeld {
		t.Fatalf("the failed append should leave the record held, got %+v", fx.held())
	}
	fx.beatTicks(1) // publishes again
	if n := fx.msgs("c1"); n != 1 {
		t.Fatalf("the republish was not deduped: the stream holds %d, want 1", n)
	}
	fx.bus.watch.hold.appendHook = nil
	fx.beatTicks(1)
	if fx.held()[0].State != holdReleased || fx.msgs("c1") != 1 {
		t.Fatalf("with the append working the record should read released and the stream stay at 1, got %+v, %d", fx.held(), fx.msgs("c1"))
	}
}

func TestPastSellByIsProposedNotSent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	// control: a hold well inside its sell-by is released when the cluster returns.
	fx := newWatchFx(t, ctx, 0, true)
	fx.stream("c1")
	fx.otherCluster()
	soon := fx.clk.wall.Add(time.Hour).Format(time.RFC3339)
	if _, err := fx.send(map[string]any{"to": "global://c1/supervisor", "reply_by": soon}); err != nil {
		t.Fatal(err)
	}
	fx.beatTicks(3)
	if fx.msgs("c1") != 1 {
		t.Fatalf("CONTROL FAILED: a hold inside its sell-by should be released, stream has %d", fx.msgs("c1"))
	}

	fx2 := newWatchFx(t, ctx, 0, true)
	fx2.stream("c1")
	fx2.otherCluster()
	short := fx2.clk.wall.Add(time.Minute).Format(time.RFC3339)
	if _, err := fx2.send(map[string]any{"to": "global://c1/supervisor", "reply_by": short}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ { // two minutes pass with the cluster absent
		fx2.tick()
	}
	fx2.beatTicks(3)
	if fx2.msgs("c1") != 0 {
		t.Fatal("a hold past its sell-by was sent")
	}
	recs := fx2.held()
	if len(recs) != 1 || recs[0].State != holdProposed {
		t.Fatalf("record = %+v, want proposed", recs)
	}
	var said bool
	for _, e := range fx2.bus.presenceNotices() {
		if strings.Contains(e, "past sell-by") {
			said = true
		}
	}
	if !said {
		t.Fatal("the proposal should reach a presence notice: release, revise or cancel")
	}
}

func TestCancelledIsNeverSent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	fx := newWatchFx(t, ctx, 0, true)
	fx.stream("c1")
	fx.otherCluster()
	keep, err := fx.send(map[string]any{"to": "global://c1/supervisor", "text": "keep"})
	if err != nil {
		t.Fatal(err)
	}
	drop, err := fx.send(map[string]any{"to": "global://c1/supervisor", "text": "drop"})
	if err != nil {
		t.Fatal(err)
	}
	dropID := asMap(t, drop)["hold_id"].(string)
	keepID := asMap(t, keep)["hold_id"].(string)
	raw, _ := json.Marshal(map[string]any{"id": dropID, "replacement": keepID})
	if _, err := toolCancelHeld(ctx, fx.bus, raw); err != nil {
		t.Fatal(err)
	}
	fx.beatTicks(3)
	if n := fx.msgs("c1"); n != 1 {
		t.Fatalf("the stream holds %d, want only the message that was not cancelled", n)
	}
	for _, r := range fx.held() {
		switch r.ID {
		case dropID:
			if r.State != holdCancelled || r.SupersededBy != keepID {
				t.Fatalf("cancelled record = %+v, want cancelled and superseded by %s", r, keepID)
			}
		case keepID:
			if r.State != holdReleased {
				t.Fatalf("the other record should be released, got %+v", r)
			}
		}
	}
	// a cancel of a record that is not held is refused.
	if _, err := toolCancelHeld(ctx, fx.bus, raw); err == nil {
		t.Fatal("cancelling an already cancelled record should be refused")
	}
}

func TestHeldWhileWatchOffIsListedNotSent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	fx := newWatchFx(t, ctx, 0, true)
	fx.stream("c1")
	fx.otherCluster()
	if _, err := fx.send(map[string]any{"to": "global://c1/supervisor"}); err != nil {
		t.Fatal(err)
	}
	// control: with the watch on, the held record is not listed as stranded.
	for _, n := range fx.bus.presenceNotices() {
		if strings.Contains(n, "while the watch is off") {
			t.Fatalf("CONTROL FAILED: the watch is on, got %q", n)
		}
	}
	// the shim restarts with the flag off: nothing is sent, and the record is listed.
	t.Setenv("DIRECTOR_PRESENCE_WATCH", "")
	fx.restart()
	if fx.bus.watch != nil {
		t.Fatal("the flag is off but a watcher was built")
	}
	before := dirDigest(t, fx.dir)
	fx.put("c1", "s1", "working")
	fx.tick()
	fx.tick()
	var listed bool
	for _, n := range fx.bus.presenceNotices() {
		if strings.Contains(n, "1 held while the watch is off") {
			listed = true
		}
	}
	if !listed {
		t.Fatal("a held record with the watch off should be listed, not stranded silently")
	}
	if fx.msgs("c1") != 0 {
		t.Fatal("a held record was sent with the watch off")
	}
	if dirDigest(t, fx.dir) != before {
		t.Fatal("listing the held records changed the state directory")
	}
}

// ---- the turn-on rules (design section 4) ----

func TestHeldReleasedOnFirstSightingAfterTurnOn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	fx := newWatchFx(t, ctx, 0, true)
	fx.stream("c1")
	fx.otherCluster()
	if _, err := fx.send(map[string]any{"to": "global://c1/supervisor"}); err != nil {
		t.Fatal(err)
	}
	fx.restart() // stop the shim; the cluster comes up and keeps beating while it is down
	fx.beatTicks(1)
	if fx.msgs("c1") != 0 {
		t.Fatal("a row seen once is not yet known to be beating: nothing is sent")
	}
	fx.beatTicks(2)
	if n := fx.msgs("c1"); n != 1 {
		t.Fatalf("a row beating after turn-on should release the held send once, stream has %d", n)
	}
}

func TestReleaseToKeyFirstSeenLateAfterTurnOn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	fx := newWatchFx(t, ctx, 300*time.Second, true) // the old row cannot expire inside the test
	fx.stream("c1")
	fx.otherCluster()
	if _, err := fx.send(map[string]any{"to": "global://c1/supervisor"}); err != nil {
		t.Fatal(err)
	}
	// the old instance's last beat lands just before the restart, and its ts never moves again.
	fx.put("c1", "old", "working")
	fx.restart()
	pass := func() { fx.tick() }
	pass() // 1: the old row reads present, with no back event
	pass() // 2
	if fx.msgs("c1") != 0 {
		t.Fatal("a row that exists and never beats must not release (passes 1 and 2)")
	}
	fx.put("c1", "new", "working") // 3: the new instance's key first appears
	pass()
	if fx.msgs("c1") != 0 {
		t.Fatal("a key first seen on this pass is not yet known to beat (pass 3)")
	}
	fx.put("c1", "new", "working") // its ts moves on its next beat
	pass()                         // 4
	if n := fx.msgs("c1"); n != 1 {
		t.Fatalf("the new row's own beat should release exactly once, stream has %d", n)
	}
	for _, r := range fx.held() {
		if r.State == holdProposed {
			t.Fatalf("nothing should be proposed: %+v", r)
		}
	}
}

func TestNoReleaseToStaleRowAfterTurnOn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	fx := newWatchFx(t, ctx, 3*time.Second, true)
	fx.stream("c1")
	fx.otherCluster()
	if _, err := fx.send(map[string]any{"to": "global://c1/supervisor"}); err != nil {
		t.Fatal(err)
	}
	fx.put("c1", "s1", "working") // the row stops beating here, inside its TTL
	fx.restart()
	fx.tick()
	fx.tick()
	if fx.msgs("c1") != 0 || fx.held()[0].State != holdHeld {
		t.Fatalf("a stale row inside its TTL must not release: msgs %d, %+v", fx.msgs("c1"), fx.held())
	}
	fx.waitGone("presence.c1.supervisor.s1")
	fx.tick()
	fx.tick()
	if u := fx.bus.watch.w.Unit("c1/s1"); u == nil || u.Status != unitAbsent {
		t.Fatalf("the unit should read absent once the row expires, got %+v", u)
	}
	if fx.msgs("c1") != 0 || fx.held()[0].State != holdHeld {
		t.Fatalf("still nothing sent and the record still held: msgs %d, %+v", fx.msgs("c1"), fx.held())
	}
}

func TestFirstSightingIsNotReportedAsBack(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	fx := newWatchFx(t, ctx, 0, true)
	fx.put("c1", "s1", "working")
	fx.tick()
	fx.tick()
	fx.restart()
	fx.put("c1", "s1", "working")
	fx.tick()
	var gap, back int
	for _, e := range fx.events() {
		if e.Unit == "c1/s1" && e.Kind == "present-after-gap" {
			gap++
		}
		if e.Kind == "back" {
			back++
		}
	}
	if gap != 1 || back != 0 {
		t.Fatalf("after a restart the event file should hold one present-after-gap for the unit and no back, got %d and %d: %+v", gap, back, fx.events())
	}
	for _, n := range fx.bus.presenceNotices() {
		if strings.Contains(n, " back") {
			t.Fatalf("a first sighting after a restart was reported as a return: %q", n)
		}
	}
}

// With the watch off, cancel_held answers as a tool that does not exist, which
// is what main answers (design: cancel_held sits behind the watch's flag).
func TestCancelHeldIsAnUnknownToolWhileTheWatchIsOff(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	fx := newWatchFx(t, ctx, 0, true)
	// The catalog is read from the server the shim builds (newServer), so a
	// change to its withHoldTools call fails here. Control: with the watch on,
	// both parts of the hold surface are listed.
	holdSurface := func() (cancel, failFast bool) {
		for _, tl := range newServer(fx.bus, t.Logf, false).tools {
			switch tl.Name {
			case "cancel_held":
				cancel = true
			case "send_message":
				props, _ := tl.InputSchema["properties"].(map[string]any)
				_, failFast = props["fail_fast"]
			}
		}
		return
	}
	if c, f := holdSurface(); !c || !f {
		t.Fatalf("CONTROL FAILED: with the watch on the catalog lists cancel_held=%v fail_fast=%v, want both", c, f)
	}
	t.Setenv("DIRECTOR_PRESENCE_WATCH", "")
	fx.restart()
	if fx.bus.watch != nil {
		t.Fatal("the flag is off but a watcher was built")
	}
	if c, f := holdSurface(); c || f {
		t.Fatalf("the catalog lists cancel_held=%v fail_fast=%v with the watch off, want neither", c, f)
	}
	_, err := dispatchTool(ctx, fx.bus, "cancel_held", json.RawMessage(`{"id":"x"}`))
	_, want := dispatchTool(ctx, fx.bus, "no_such_tool_at_all", json.RawMessage(`{}`))
	if err == nil || want == nil {
		t.Fatalf("cancel_held err %v, unknown tool err %v: both must fail", err, want)
	}
	if got, exp := err.Error(), "unknown tool: cancel_held"; got != exp {
		t.Fatalf("cancel_held with the watch off answered %q, want main's %q", got, exp)
	}
	if want.Error() != "unknown tool: no_such_tool_at_all" {
		t.Fatalf("CONTROL FAILED: an unknown tool answered %q", want)
	}
}
