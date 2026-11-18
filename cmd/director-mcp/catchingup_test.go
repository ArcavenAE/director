package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// director#222 and #220, plan T-A3 (asks-die-unread D2, design tests 5, 6, 7 and
// 7a). A session writes presence state `catching-up` at connect, not `idle`, and
// keeps it until every source has reported zero waiting and the seat has called
// wait_for_message once since connect. Until then its own set_presence is
// recorded but presence stays `catching-up`, and the tool result says why and how
// many are waiting. Sends still resolve. These run on a scratch nats-server; the
// two pins hold today and must keep holding.

// caConnect attaches a session as the shim's startup does: connect, then
// announce, with no explicit presence write. pred, when set, is the lineage input.
func caConnect(t *testing.T, ctx context.Context, url, id, pred string) *Bus {
	t.Helper()
	t.Setenv("DIRECTOR_STATE", t.TempDir())
	t.Setenv("DIRECTOR_HANDLED", "")
	t.Setenv("DIRECTOR_PREDECESSOR", "")
	t.Setenv("MARVEL_PREDECESSOR", "")
	if pred != "" {
		t.Setenv("DIRECTOR_PREDECESSOR", pred)
		t.Setenv("MARVEL_PREDECESSOR", lineWS+"/"+pred)
	}
	b, err := connect(ctx, url, Sender{AgentID: id, Workspace: lineWS, Team: lineTeam}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.close)
	if _, err := b.announce(ctx); err != nil {
		t.Fatal(err)
	}
	b.takeResumed()
	return b
}

// presenceStateOf reads the state field of the session's own presence row.
func presenceStateOf(t *testing.T, ctx context.Context, b *Bus) string {
	t.Helper()
	e, err := b.kv.Get(ctx, "presence."+b.self.Team+"."+b.self.AgentID+"."+b.instance)
	if err != nil {
		t.Fatalf("reading the presence row: %v", err)
	}
	var rec map[string]any
	if err := json.Unmarshal(e.Value(), &rec); err != nil {
		t.Fatal(err)
	}
	s, _ := rec["state"].(string)
	return s
}

// resultMap is a tool result as the model sees it.
func resultMap(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// Test 5. Two messages wait at connect: presence reads catching-up, and a
// set_presence idle leaves it there and the result names 2.
func TestPresenceReadsCatchingUpRightAfterConnect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	publishN(t, ctx, js, lineInbox("sup"), "o", 1, 2)
	seat := caConnect(t, ctx, url, "sup", "")

	if got := presenceStateOf(t, ctx, seat); got != "catching-up" {
		t.Fatalf("presence right after connect reads %q, want catching-up", got)
	}
	out, err := toolPresence(ctx, seat, json.RawMessage(`{"state":"idle"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := presenceStateOf(t, ctx, seat); got != "catching-up" {
		t.Errorf("after set_presence idle with 2 waiting, presence reads %q, want catching-up", got)
	}
	m := resultMap(t, out)
	if m["state"] != "catching-up" {
		t.Errorf("the result's state = %v, want catching-up (the request is recorded, the state is not changed)", m["state"])
	}
	if m["waiting"] != float64(2) {
		t.Errorf("the result's waiting = %v, want 2", m["waiting"])
	}
}

// A seat that connects with nothing waiting is catching-up as well: it has not
// called wait_for_message yet.
func TestPresenceReadsCatchingUpWithNothingWaiting(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	provision(t, ctx, url)
	seat := caConnect(t, ctx, url, "sup", "")
	runHeartbeat(t, ctx, seat, 40*time.Millisecond)
	time.Sleep(300 * time.Millisecond)
	if got := presenceStateOf(t, ctx, seat); got != "catching-up" {
		t.Errorf("presence with nothing waiting and no wait_for_message yet reads %q, want catching-up", got)
	}
}

// Test 6. A drain to zero is not enough: after one wait_for_message the heartbeat
// writes idle, and later set_presence calls apply.
func TestPresenceTurnsIdleAfterADrainAndOneWait(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	publishN(t, ctx, js, lineInbox("sup"), "o", 1, 2)
	seat := caConnect(t, ctx, url, "sup", "")
	runHeartbeat(t, ctx, seat, 40*time.Millisecond)

	res, err := seat.receiveBatch(ctx, 3*time.Second, 10)
	if err != nil || len(res.Items) != 2 {
		t.Fatalf("setup: the drain gave %d items (err %v), want 2", len(res.Items), err)
	}
	time.Sleep(300 * time.Millisecond)
	if got := presenceStateOf(t, ctx, seat); got != "catching-up" {
		t.Fatalf("after a drain and before any wait_for_message presence reads %q, want catching-up", got)
	}
	if _, err := toolWait(ctx, seat, json.RawMessage(`{"timeout_seconds":1}`)); err != nil {
		t.Fatal(err)
	}
	if !waitFor(5*time.Second, func() bool { return presenceStateOf(t, ctx, seat) == "idle" }) {
		t.Fatalf("after a drain to zero and one wait_for_message presence reads %q, want idle", presenceStateOf(t, ctx, seat))
	}
	if _, err := toolPresence(ctx, seat, json.RawMessage(`{"state":"busy"}`)); err != nil {
		t.Fatal(err)
	}
	if got := presenceStateOf(t, ctx, seat); got != "busy" {
		t.Errorf("a later set_presence busy leaves presence at %q, want busy", got)
	}
}

// Test 7a. With an armed inheritance whose predecessor is still live, presence
// stays catching-up after the seat drains its own inbox, and turns idle only
// after the inherited mail drains.
func TestPresenceWaitsForTheInheritedMailToo(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	pred := caConnect(t, ctx, url, "pred", "")
	publishN(t, ctx, js, lineInbox("pred"), "p", 1, 3)
	succ := caConnect(t, ctx, url, "succ", "pred")
	runHeartbeat(t, ctx, succ, 40*time.Millisecond)
	if _, err := toolWait(ctx, succ, json.RawMessage(`{"timeout_seconds":1}`)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	if got := presenceStateOf(t, ctx, succ); got != "catching-up" {
		t.Fatalf("with the predecessor live and its mail unread, presence reads %q, want catching-up", got)
	}

	lineExit(t, ctx, pred)
	if !waitFor(5*time.Second, func() bool { return len(inheritDurables(t, ctx, js, "succ")) == 1 }) {
		t.Fatal("nothing inherited")
	}
	time.Sleep(300 * time.Millisecond)
	if got := presenceStateOf(t, ctx, succ); got != "catching-up" {
		t.Fatalf("with 3 inherited messages waiting, presence reads %q, want catching-up", got)
	}
	res, err := succ.receiveBatch(ctx, 3*time.Second, 10)
	if err != nil || len(res.Items) != 3 {
		t.Fatalf("setup: the drain gave %d items (err %v), want the 3 inherited", len(res.Items), err)
	}
	if !waitFor(5*time.Second, func() bool { return presenceStateOf(t, ctx, succ) == "idle" }) {
		t.Errorf("after the inherited mail drained presence reads %q, want idle", presenceStateOf(t, ctx, succ))
	}
}

// Test 7, a pin. A send to a catching-up seat resolves and is stored, and the
// seat receives it. Today the state is free text, so this holds with the state
// written by hand; it has to keep holding once the shim writes it.
func TestASendToACatchingUpSeatResolvesAndIsStored(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	provision(t, ctx, url)
	seat := lineConnect(t, ctx, url, "sup", "", "")
	if err := seat.writePresence(ctx, "catching-up"); err != nil {
		t.Fatal(err)
	}
	sender := lineConnect(t, ctx, url, "sender", "", "")
	if err := sendRole(ctx, sender, "agent://"+lineTeam+"/sup", "m1", lineWS); err != nil {
		t.Fatalf("a send to a catching-up seat was refused: %v", err)
	}
	res, err := seat.receiveBatch(ctx, 3*time.Second, 5)
	if err != nil || len(res.Items) != 1 || res.Items[0].Env.MessageID != "m1" {
		t.Errorf("the catching-up seat received %d items (err %v), want m1", len(res.Items), err)
	}
}

// A pin for R-92: a session whose state reads catching-up is live. Liveness is
// presence present, and nothing compares the state string.
func TestACatchingUpSessionCountsAsLive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	seat := lineConnect(t, ctx, url, "sup", "", "")
	if err := seat.writePresence(ctx, "catching-up"); err != nil {
		t.Fatal(err)
	}
	publishN(t, ctx, js, lineInbox("sup"), "o", 1, 1)
	r := mustRead(t, ctx, js, time.Now())
	if d := rowFor(t, r, seat.instance); !d.Live {
		t.Errorf("a catching-up session's row is not live: %+v", d)
	}
}
