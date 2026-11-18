package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"
)

// A seat's cue proves itself on real mail, so the missing piece is a reader
// (design slice 3, sim/design/director-standing-loop.md sections 5 and 7). Four
// fields are involved. `cue` and `cue_reason` are in the presence record
// already and the roster CLI drops them. `cue_nonces_sent` is new: the cue
// nonces sent while unverified since start, excluding the start-up self-test,
// so 0 reads as "no mail since start" and more than 0 on an unverified seat as
// "nonces sent and never echoed". `cue_proved_at` is new too: when an echo last
// made the cue live, in RFC 3339 UTC, absent until the first proof. Both are
// present only when the cue is on, and the roster passes all four through.
//
// The presence and roster records are read as maps, so a missing field is an
// assertion failure here and never a build error. These run on the scratch
// broker of drain_broker_test.go.

// cueSeat is a seat on the scratch broker with a cue attached, and the cue rig
// that drives it. The presence row is written on demand.
func cueSeat(t *testing.T) (context.Context, string, *Bus, *cueRig) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	url := startScratchServer(t)
	provision(t, ctx, url)
	bus := roleBus(t, ctx, url, "cue-seat", "")
	r := newCueRig(t)
	r.started()
	bus.attachCue(r.c)
	return ctx, url, bus, r
}

func TestPresenceCountsNoncesSentWhileUnverified(t *testing.T) {
	ctx, _, bus, r := cueSeat(t)
	if err := bus.writePresence(ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	row := presenceRow(t, ctx, bus)
	if r.selfTests() == 0 {
		t.Fatalf("setup: the start-up self-test was not sent")
	}
	if n, ok := row["cue_nonces_sent"].(float64); !ok || n != 0 {
		t.Fatalf("cue on, no real cue sent yet: cue_nonces_sent = %v, want 0 (the self-test is not counted): %v", row["cue_nonces_sent"], row)
	}

	// One real cue while unverified carries one nonce.
	r.now = r.now.Add(121 * time.Second)
	r.src.n = 1
	r.step(2 * time.Second)
	nonce, _ := meta(t, r.cues[len(r.cues)-1])["nonce"].(string)
	if nonce == "" {
		t.Fatalf("setup: the cue sent while unverified carried no nonce: %v", r.cues[len(r.cues)-1])
	}
	if err := bus.writePresence(ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	row = presenceRow(t, ctx, bus)
	if n, _ := row["cue_nonces_sent"].(float64); n != 1 {
		t.Errorf("after one real cue while unverified: cue_nonces_sent = %v, want 1: %v", row["cue_nonces_sent"], row)
	}

	// Once the echo makes the cue live, a later cue carries no nonce and the
	// count stops growing.
	r.c.ack(nonce)
	r.src.n = 2
	r.step(2 * time.Second)
	if err := bus.writePresence(ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	row = presenceRow(t, ctx, bus)
	if n, _ := row["cue_nonces_sent"].(float64); n != 1 {
		t.Errorf("after the cue went live: cue_nonces_sent = %v, want it to stay 1: %v", row["cue_nonces_sent"], row)
	}
}

func TestPresenceCarriesCueProvedAt(t *testing.T) {
	ctx, _, bus, r := cueSeat(t)
	if err := bus.writePresence(ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	if _, ok := presenceRow(t, ctx, bus)["cue_proved_at"]; ok {
		t.Fatalf("a cue that has not been proved carries no cue_proved_at")
	}
	var selfNonce string
	for _, p := range r.cues {
		if m := meta(t, p); m["kind"] == "self-test" {
			selfNonce, _ = m["nonce"].(string)
		}
	}
	if selfNonce == "" {
		t.Fatalf("setup: no self-test nonce was sent")
	}
	r.now = r.now.Add(5 * time.Second)
	r.c.ack(selfNonce)
	if err := bus.writePresence(ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	row := presenceRow(t, ctx, bus)
	want := r.now.UTC().Format(time.RFC3339)
	if got, _ := row["cue_proved_at"].(string); got != want {
		t.Errorf("after the echo: cue_proved_at = %v, want %q (when the echo made the cue live): %v", row["cue_proved_at"], want, row)
	}
}

// cliRosterRowFor runs the roster CLI against the broker and returns the row for
// the seat, decoded as a map.
func cliRosterRowFor(t *testing.T, ctx context.Context, url, id string) map[string]any {
	t.Helper()
	var out, errw bytes.Buffer
	if code := runRosterCmd(ctx, url, cliArgs{mode: "roster", json: true}, &out, &errw); code != 0 {
		t.Fatalf("roster exit %d: %s", code, errw.String())
	}
	var rep struct {
		Rows []map[string]any `json:"rows"`
	}
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("roster output is not JSON: %v: %s", err, out.String())
	}
	for _, row := range rep.Rows {
		if row["agent_id"] == id {
			return row
		}
	}
	t.Fatalf("the roster has no row for %s: %s", id, out.String())
	return nil
}

func TestRosterJSONCarriesCueReason(t *testing.T) {
	ctx, url, bus, r := cueSeat(t)
	const reason = "no nonce echo after self-test"
	r.c.mu.Lock()
	r.c.reason = reason
	r.c.mu.Unlock()
	if err := bus.writePresence(ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	if row := presenceRow(t, ctx, bus); row["cue"] != "unverified" || row["cue_reason"] != reason {
		t.Fatalf("setup: the presence record lacks the cue state: %v", row)
	}
	row := cliRosterRowFor(t, ctx, url, "cue-seat")
	if row["cue"] != "unverified" {
		t.Errorf("roster row cue = %v, want unverified: %v", row["cue"], row)
	}
	if row["cue_reason"] != reason {
		t.Errorf("roster row cue_reason = %v, want %q: %v", row["cue_reason"], reason, row)
	}
}

func TestRosterJSONPassesTheProofFieldsThrough(t *testing.T) {
	ctx, url, bus, r := cueSeat(t)
	r.now = r.now.Add(121 * time.Second)
	r.src.n = 1
	r.step(2 * time.Second)
	nonce, _ := meta(t, r.cues[len(r.cues)-1])["nonce"].(string)
	r.now = r.now.Add(3 * time.Second)
	r.c.ack(nonce)
	if err := bus.writePresence(ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	row := cliRosterRowFor(t, ctx, url, "cue-seat")
	if row["cue"] != "live" {
		t.Errorf("roster row cue = %v, want live: %v", row["cue"], row)
	}
	if n, ok := row["cue_nonces_sent"].(float64); !ok || n != 1 {
		t.Errorf("roster row cue_nonces_sent = %v, want 1: %v", row["cue_nonces_sent"], row)
	}
	want := r.now.UTC().Format(time.RFC3339)
	if got, _ := row["cue_proved_at"].(string); got != want {
		t.Errorf("roster row cue_proved_at = %v, want %q: %v", row["cue_proved_at"], want, row)
	}
}
