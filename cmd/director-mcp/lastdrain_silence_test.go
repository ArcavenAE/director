package main

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// An empty wait is a drain too: with nothing waiting on any tier and no failed
// read, the inbox is empty at that moment, which is what last-drain means
// (design slice 2, sim/design/director-standing-loop.md sections 7 and 8).
// Until now only a wait that returned mail ever wrote the beat, so a director
// whose inbox stayed quiet read as stale. These run the real call path on the
// scratch broker of drain_broker_test.go with a scratch DIRECTOR_STATE.

func TestASilentWaitWithNothingPendingWritesLastDrain(t *testing.T) {
	for _, tc := range []struct {
		name string
		max  int
	}{{"single wait", 1}, {"batch wait", 5}} {
		t.Run(tc.name, func(t *testing.T) {
			state := t.TempDir()
			t.Setenv("DIRECTOR_STATE", state)
			r := newPipeRig(t, false)
			before := time.Now().UTC().Truncate(time.Second)
			r.wait(`1`, 1, tc.max)
			m := r.next(8 * time.Second)
			if idOf(m) != `1` {
				t.Fatalf("the wait was not answered first: %v", m)
			}
			if txt := toolText(t, m); !strings.Contains(txt, "silence") {
				t.Fatalf("setup: the wait returned mail or an error, want the silence note: %s", txt)
			}
			raw, err := os.ReadFile(filepath.Join(state, "last-drain"))
			if err != nil {
				t.Fatalf("an empty wait with nothing pending wrote no last-drain: %v", err)
			}
			first, _, _ := strings.Cut(string(raw), "\n")
			ts, err := time.Parse("2006-01-02T15:04:05Z", first)
			if err != nil {
				t.Fatalf("last-drain first line = %q, want an RFC 3339 UTC stamp", first)
			}
			if ts.Before(before) || ts.After(time.Now().UTC().Add(time.Second)) {
				t.Errorf("last-drain %v is outside the call's window starting %v", ts, before)
			}
		})
	}
}

// The reason an empty wait beat is logged, not stored: last-drain stays one
// stamp line (design section 7). A wait that returned mail logs no such reason.
func TestASilentDrainLogsItsReasonAndStoresOnlyTheStamp(t *testing.T) {
	ctx, bus, pub, path := lastDrainFixture(t)
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	if _, err := toolWait(ctx, bus, json.RawMessage(`{"timeout_seconds":1,"max":5}`)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "ended: silence") {
		t.Errorf("an empty wait that wrote last-drain logged no reason: %q", buf.String())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no last-drain: %v", err)
	}
	if n := strings.Count(string(raw), "\n"); n != 1 || strings.Contains(string(raw), "silence") {
		t.Errorf("last-drain = %q, want one stamp line and no reason", raw)
	}
	buf.Reset()
	pub("a")
	if _, err := toolWait(ctx, bus, json.RawMessage(`{"timeout_seconds":2,"max":5}`)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "ended: silence") {
		t.Errorf("a wait that returned mail logged the silence reason: %q", buf.String())
	}
}
