package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// I-413: the cue is on unless DIRECTOR_CUE is set to something other than 1.
func TestTheCueIsOnByDefaultAndOffOnlyWhenOptedOut(t *testing.T) {
	cases := []struct {
		name string
		set  bool
		val  string
		want bool
	}{
		{"unset", false, "", true},
		{"one", true, "1", true},
		{"zero", true, "0", false},
		{"empty", true, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DIRECTOR_CUE", tc.val)
			if !tc.set {
				os.Unsetenv("DIRECTOR_CUE")
			}
			if got := loadCueConfig().Enabled; got != tc.want {
				t.Fatalf("Enabled = %v, want %v", got, tc.want)
			}
		})
	}
}

// A wait blocks until the cue is proven live, and stops blocking once it is.
func TestOnlyALiveCueMakesWaitsPushOnly(t *testing.T) {
	r := newCueRig(t)
	if r.c.pushes() {
		t.Fatal("before the handshake a wait must block")
	}
	r.started()
	if r.c.pushes() {
		t.Fatal("an unverified cue must not make waits push-only")
	}
	r.c.ack("nonce1")
	if !r.c.pushes() {
		t.Fatalf("a live cue makes waits push-only: %v", r.c.status())
	}
	r.c.notePushOnly()
	r.c.notePushOnly()
	if got := r.c.status()["push_only_waits"]; got != "2" {
		t.Fatalf("push_only_waits = %q, want 2", got)
	}
}

func TestAPushOnlyWaitKeepsMaxAndTakesTheShortestTimeout(t *testing.T) {
	out := pushOnlyWaitArgs(json.RawMessage(`{"max":9,"timeout_seconds":110}`))
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if m["max"] != float64(9) || m["timeout_seconds"] != float64(1) {
		t.Fatalf("args: %v", m)
	}
	out = pushOnlyWaitArgs(nil)
	if err := json.Unmarshal(out, &m); err != nil || m["timeout_seconds"] != float64(1) {
		t.Fatalf("empty args: %s %v", out, err)
	}
	bad := json.RawMessage(`{not json`)
	if got := pushOnlyWaitArgs(bad); string(got) != string(bad) {
		t.Fatalf("unparseable args must be left for toolWait: %s", got)
	}
}

func TestTheWaitDescriptionSaysPushOnlyWhenTheCueIsOn(t *testing.T) {
	desc := func(on bool) string {
		for _, td := range toolCatalog(nil, on) {
			if td.Name == "wait_for_message" {
				return td.Description
			}
		}
		t.Fatal("no wait_for_message")
		return ""
	}
	if off := desc(false); !strings.Contains(off, "This is a poll") {
		t.Fatalf("cue off keeps the poll text: %s", off)
	}
	on := desc(true)
	if strings.Contains(on, "The server cannot push") || !strings.Contains(on, "end the turn idle") {
		t.Fatalf("cue on: %s", on)
	}
}
