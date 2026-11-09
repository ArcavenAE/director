package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// A send_message with ask that the presence watch holds is not accepted, so it
// writes no relay entry at send time. The hold keeps ask and kind, and the entry
// is written when the send actually goes out on release. A cancelled hold writes
// none. The held result says the entry is pending.

func heldAskFixture(t *testing.T, ctx context.Context) (*watchFx, string) {
	t.Helper()
	fx := newWatchFx(t, ctx, 0, true)
	fx.stream("c1")
	fx.otherCluster()
	askFixture(t, "I-7")
	log := relayFixture(t)
	return fx, log
}

func heldAskSend(t *testing.T, fx *watchFx, extra map[string]any) map[string]any {
	t.Helper()
	args := map[string]any{"to": "global://c1/supervisor", "ask": "I-7", "text": "held with an ask\nsecond line"}
	for k, v := range extra {
		args[k] = v
	}
	out, err := fx.send(args)
	if err != nil {
		t.Fatalf("a send refused for absence is held, not an error: %v", err)
	}
	return asMap(t, out)
}

func TestHeldSendWithAskSaysTheEntryIsPendingAndWritesNone(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	fx, log := heldAskFixture(t, ctx)
	m := heldAskSend(t, fx, nil)
	if m["status"] != "held, not delivered" {
		t.Fatalf("status = %v", m["status"])
	}
	p, _ := m["relay_entry"].(string)
	if !strings.Contains(p, "pending") || !strings.Contains(p, "release") {
		t.Errorf("relay_entry = %q, want it to say the entry is pending release", p)
	}
	if _, err := os.Stat(log); err == nil {
		t.Error("a held send wrote the relay log before it went out")
	}
	recs := fx.held()
	if len(recs) != 1 || recs[0].Ask != "I-7" || recs[0].Kind != "relay" {
		t.Errorf("the hold keeps ask and kind (kind defaults to relay), got %+v", recs)
	}
}

func TestHeldSendWithAskWritesTheEntryOnRelease(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	fx, log := heldAskFixture(t, ctx)
	m := heldAskSend(t, fx, map[string]any{"kind": "ruling"})
	id := m["hold_id"].(string)
	fx.beatTicks(3)
	if fx.msgs("c1") != 1 {
		t.Fatal("the held send was not released")
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("no relay entry after the release: %v", err)
	}
	got := string(b)
	for _, want := range []string{"-> global://c1/supervisor", "**Intake:** I-7", "**Kind:** ruling", "> held with an ask\n> second line", "**Outcome:** sent; message " + id} {
		if !strings.Contains(got, want) {
			t.Errorf("relay entry lacks %q:\n%s", want, got)
		}
	}
	fx.beatTicks(2)
	b2, _ := os.ReadFile(log)
	if strings.Count(string(b2), "## R-") != 1 {
		t.Errorf("a released hold wrote %d entries, want exactly one:\n%s", strings.Count(string(b2), "## R-"), b2)
	}
}

// A cancelled hold is never sent, so it never writes an entry: the entry records
// a send that went out.
func TestCancelledHeldSendWithAskWritesNoRelayEntry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	fx, log := heldAskFixture(t, ctx)
	m := heldAskSend(t, fx, nil)
	raw, _ := json.Marshal(map[string]any{"id": m["hold_id"].(string)})
	if _, err := toolCancelHeld(ctx, fx.bus, raw); err != nil {
		t.Fatal(err)
	}
	fx.beatTicks(3)
	if fx.msgs("c1") != 0 {
		t.Fatal("a cancelled hold was sent")
	}
	if _, err := os.Stat(log); err == nil {
		t.Error("a cancelled hold wrote a relay entry")
	}
}

// The release is published under the hold id and recorded after. A crash between
// the two repeats the publish (deduped by the hub), and the entry must still be
// written once, not once per attempt.
func TestReleaseCrashBeforeRecordWritesTheEntryOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	fx, log := heldAskFixture(t, ctx)
	heldAskSend(t, fx, nil)
	fx.bus.watch.hold.appendHook = func(r heldRec) error {
		if r.State == holdReleased {
			return os.ErrClosed
		}
		return nil
	}
	fx.beatTicks(3)
	if _, err := os.Stat(log); err == nil {
		t.Fatal("an entry was written for a release that was not recorded")
	}
	fx.bus.watch.hold.appendHook = nil
	fx.beatTicks(2)
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("no entry after the release was recorded: %v", err)
	}
	if n := strings.Count(string(b), "## R-"); n != 1 {
		t.Errorf("%d entries, want exactly one:\n%s", n, b)
	}
}

// The send has gone out once released, so a relay-log that cannot run is a notice,
// and the record still reads released.
func TestReleaseWithNoRelayLogNoticesAndStaysReleased(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	fx, _ := heldAskFixture(t, ctx)
	t.Setenv("DIRECTOR_HOME", t.TempDir()) // no bin/relay-log in it
	heldAskSend(t, fx, nil)
	fx.beatTicks(3)
	if fx.msgs("c1") != 1 || fx.held()[0].State != holdReleased {
		t.Fatalf("the send was not released: msgs %d, %+v", fx.msgs("c1"), fx.held())
	}
	var all string
	for _, n := range fx.bus.watch.takeNotices() {
		all += n + "\n"
	}
	if !strings.Contains(all, "no relay-log entry was written") {
		t.Errorf("notices = %q, want one saying no relay-log entry was written", all)
	}
}

// A held send without ask is unchanged: no relay key, nothing written.
func TestHeldSendWithoutAskIsUnchanged(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	fx, log := heldAskFixture(t, ctx)
	out, err := fx.send(map[string]any{"to": "global://c1/supervisor"})
	if err != nil {
		t.Fatal(err)
	}
	if _, has := asMap(t, out)["relay_entry"]; has {
		t.Error("a held send without ask carries relay_entry")
	}
	fx.beatTicks(3)
	if _, err := os.Stat(log); err == nil {
		t.Error("a release without ask wrote the relay log")
	}
}
