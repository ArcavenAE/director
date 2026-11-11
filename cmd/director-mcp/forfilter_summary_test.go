package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func summaryFilter(t *testing.T, r *pipeRig) map[string]any {
	t.Helper()
	out, err := toolSummary(r.ctx, r.bus, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("summary is %T", out)
	}
	ff, _ := m["for_filter"].(map[string]any)
	return ff
}

// inbox_summary reports the FOR filter's state and how many messages it has
// dropped since attach, so a seat can see the filter at work without its log.
func TestInboxSummaryReportsTheForFilterAndItsDrops(t *testing.T) {
	r, _ := forPathRig(t)
	publishForPair(t, r)

	ff := summaryFilter(t, r)
	if ff["state"] != "on" || ff["dropped"] != int64(0) {
		t.Fatalf("before any pull, for_filter = %v, want state on, dropped 0", ff)
	}
	// The summary reads without pulling, so it drops and acks nothing.
	if ff := summaryFilter(t, r); ff["dropped"] != int64(0) {
		t.Fatalf("a second summary changed the count: %v", ff)
	}

	ctx, cancel := context.WithTimeout(r.ctx, 10*time.Second)
	defer cancel()
	if _, err := r.bus.receiveBatch(ctx, 3*time.Second, 5); err != nil {
		t.Fatal(err)
	}
	if ff := summaryFilter(t, r); ff["state"] != "on" || ff["dropped"] != int64(1) {
		t.Fatalf("after the pull, for_filter = %v, want state on, dropped 1", ff)
	}
}

// With the filter off, or set to something other than 1, the summary says so
// and never prints the variable's value; with no global tier it adds nothing.
func TestInboxSummaryForFilterStates(t *testing.T) {
	t.Run("off", func(t *testing.T) {
		r := newPipeRig(t, true)
		t.Setenv("DIRECTOR_FOR_FILTER", "")
		if ff := summaryFilter(t, r); ff["state"] != "off" || ff["dropped"] != int64(0) {
			t.Fatalf("for_filter = %v, want off, 0", ff)
		}
	})
	t.Run("set but not 1", func(t *testing.T) {
		r := newPipeRig(t, true)
		t.Setenv("DIRECTOR_FOR_FILTER", "sekrit-true")
		ff := summaryFilter(t, r)
		if ff["state"] != "set but not 1" {
			t.Fatalf("for_filter = %v, want set but not 1", ff)
		}
		if b, _ := json.Marshal(ff); string(b) != "" && strings.Contains(string(b), "sekrit-true") {
			t.Fatalf("the value was printed: %s", b)
		}
	})
	t.Run("no global tier", func(t *testing.T) {
		r := newPipeRig(t, false)
		if ff := summaryFilter(t, r); ff != nil {
			t.Fatalf("for_filter = %v, want absent without a global tier", ff)
		}
	})
}
