package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A sender learns nothing today when the recipient's cue is off, unverified or
// stalled: the send reads "accepted for delivery" and a missed ask stays
// invisible for hours. The send result now carries the recipient's cue state
// and the age of its oldest unread message, from the presence row the shim
// already reads, and one warning line when the state means the cue will not
// wake the seat (director#719).

type cueSendFixture struct {
	t      *testing.T
	ctx    context.Context
	sender *Bus
}

func newCueSendFixture(t *testing.T) *cueSendFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	url := startScratchServer(t)
	provision(t, ctx, url)
	return &cueSendFixture{t: t, ctx: ctx, sender: roleBus(t, ctx, url, "sender", "")}
}

// recipientRow puts a presence row for ops/rcpt with the given extra fields.
func (f *cueSendFixture) recipientRow(instance string, extra map[string]any) {
	f.t.Helper()
	rec := map[string]any{
		"agent_id": "rcpt", "team": "ops", "workspace": "aae-orc", "instance": instance,
		"state": "idle", "ts": time.Now().UTC().Format(time.RFC3339),
	}
	for k, v := range extra {
		rec[k] = v
	}
	putPresence(f.t, f.ctx, f.sender, "presence.ops.rcpt."+instance, rec)
}

func (f *cueSendFixture) send() map[string]any {
	f.t.Helper()
	raw, _ := json.Marshal(map[string]any{"to": "agent://ops/rcpt", "performative": "INFORM", "text": "hello"})
	res, err := toolSend(f.ctx, f.sender, raw)
	if err != nil {
		f.t.Fatalf("send: %v", err)
	}
	out, ok := res.(map[string]any)
	if !ok {
		f.t.Fatalf("send result is %T, want a map", res)
	}
	if out["status"] != "accepted for delivery" {
		f.t.Fatalf("status = %v, want accepted for delivery", out["status"])
	}
	return out
}

func TestSendResultReportsAnUnverifiedRecipientCue(t *testing.T) {
	f := newCueSendFixture(t)
	f.recipientRow("01INSTUNVER", map[string]any{
		"cue": "unverified", "cue_reason": "no nonce echo after self-test", "oldest_pending_s": 700,
	})
	out := f.send()
	if out["recipient_cue"] != "unverified" {
		t.Errorf("recipient_cue = %v, want unverified", out["recipient_cue"])
	}
	if out["recipient_cue_reason"] != "no nonce echo after self-test" {
		t.Errorf("recipient_cue_reason = %v, want the row's reason", out["recipient_cue_reason"])
	}
	if out["recipient_oldest_pending_s"] != int64(700) {
		t.Errorf("recipient_oldest_pending_s = %v (%T), want 700", out["recipient_oldest_pending_s"], out["recipient_oldest_pending_s"])
	}
	w, _ := out["recipient_warning"].(string)
	if !strings.Contains(w, "unverified") || strings.Contains(w, "\n") {
		t.Errorf("recipient_warning = %q, want one line that names the state", w)
	}
}

func TestSendResultWarnsForOffAndStalledRecipientCues(t *testing.T) {
	for _, state := range []string{"off", "stalled"} {
		t.Run(state, func(t *testing.T) {
			f := newCueSendFixture(t)
			f.recipientRow("01INST"+strings.ToUpper(state), map[string]any{"cue": state, "cue_reason": "why", "oldest_pending_s": 4048})
			out := f.send()
			if out["recipient_cue"] != state {
				t.Errorf("recipient_cue = %v, want %s", out["recipient_cue"], state)
			}
			if w, _ := out["recipient_warning"].(string); !strings.Contains(w, state) {
				t.Errorf("recipient_warning = %q, want a line naming %s", w, state)
			}
		})
	}
}

func TestSendResultHasNoWarningForALiveRecipientCue(t *testing.T) {
	f := newCueSendFixture(t)
	f.recipientRow("01INSTLIVE", map[string]any{"cue": "live", "oldest_pending_s": 3})
	out := f.send()
	if out["recipient_cue"] != "live" {
		t.Errorf("recipient_cue = %v, want live", out["recipient_cue"])
	}
	if out["recipient_oldest_pending_s"] != int64(3) {
		t.Errorf("recipient_oldest_pending_s = %v, want 3", out["recipient_oldest_pending_s"])
	}
	if _, has := out["recipient_warning"]; has {
		t.Errorf("a live cue must carry no warning, got %v", out["recipient_warning"])
	}
}

// A recipient whose row names no cue (the cue is off in its own config, or the
// shim predates the field) says nothing about the cue, so the result adds no
// cue fields and no warning rather than guessing.
func TestSendResultAddsNoCueFieldsWhenTheRowHasNoCue(t *testing.T) {
	f := newCueSendFixture(t)
	f.recipientRow("01INSTNOCUE", map[string]any{"oldest_pending_s": 5})
	out := f.send()
	for _, k := range []string{"recipient_cue", "recipient_cue_reason", "recipient_oldest_pending_s", "recipient_warning"} {
		if _, has := out[k]; has {
			t.Errorf("%s present for a row with no cue field: %v", k, out[k])
		}
	}
}

// The relay-log warning already uses the "warning" key; the cue warning must
// not displace it, so the cue line has its own key.
func TestSendResultKeepsTheCueWarningOffTheWarningKey(t *testing.T) {
	f := newCueSendFixture(t)
	f.recipientRow("01INSTKEY", map[string]any{"cue": "stalled", "cue_reason": "why", "oldest_pending_s": 900})
	out := f.send()
	if _, has := out["warning"]; has {
		t.Errorf(`the cue state must not use the "warning" key, got %v`, out["warning"])
	}
	if _, has := out["recipient_warning"]; !has {
		t.Errorf("a stalled recipient needs a recipient_warning")
	}
}

// sendToWorkspace sends with an explicit workspace hint and cold: true, so a
// mailbox with no live consumer can be addressed.
func (f *cueSendFixture) sendToWorkspace(ws string) map[string]any {
	f.t.Helper()
	raw, _ := json.Marshal(map[string]any{"to": "agent://ops/rcpt", "performative": "INFORM", "text": "hello", "workspace": ws, "cold": true})
	res, err := toolSend(f.ctx, f.sender, raw)
	if err != nil {
		f.t.Fatalf("send to %s: %v", ws, err)
	}
	out, ok := res.(map[string]any)
	if !ok {
		f.t.Fatalf("send result is %T, want a map", res)
	}
	return out
}

// A recipient live in two workspaces has one presence row in each. The send is
// built for one workspace, so its result reads that workspace's row and never
// the other session's, whichever row is newer (director#725 review, F1).
func TestSendResultReadsOnlyTheRowOfTheWorkspaceTheSendWentTo(t *testing.T) {
	for _, tc := range []struct {
		name              string
		liveTS, stalledTS time.Duration
	}{
		{"the stalled row is newer", -30 * time.Second, -5 * time.Second},
		{"the live row is newer", -5 * time.Second, -30 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCueSendFixture(t)
			now := time.Now().UTC()
			f.recipientRow("01INSTLIVE", map[string]any{
				"workspace": "ws-live", "cue": "live", "ts": now.Add(tc.liveTS).Format(time.RFC3339),
			})
			f.recipientRow("01INSTSTALL", map[string]any{
				"workspace": "ws-stalled", "cue": "stalled", "cue_reason": "no drain", "oldest_pending_s": 900,
				"ts": now.Add(tc.stalledTS).Format(time.RFC3339),
			})
			stalled := f.sendToWorkspace("ws-stalled")
			if stalled["recipient_cue"] != "stalled" {
				t.Errorf("send to ws-stalled: recipient_cue = %v, want stalled", stalled["recipient_cue"])
			}
			if w, _ := stalled["recipient_warning"].(string); !strings.Contains(w, "stalled") {
				t.Errorf("send to ws-stalled: recipient_warning = %q, want a warning that names stalled", w)
			}
			live := f.sendToWorkspace("ws-live")
			if live["recipient_cue"] != "live" {
				t.Errorf("send to ws-live: recipient_cue = %v, want live", live["recipient_cue"])
			}
			if w, ok := live["recipient_warning"]; ok {
				t.Errorf("send to ws-live: recipient_warning = %v, want none for a live cue", w)
			}
		})
	}
}
