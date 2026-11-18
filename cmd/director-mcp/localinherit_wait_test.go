package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// director#677 review: the default single-message wait, the summary's flagged
// list and the single form of a returned result left the inherited marker or the
// inherited mail itself out. Each case runs on a scratch nats-server.

func inheritedSuccessor(t *testing.T, ctx context.Context, perf string, n int) *Bus {
	t.Helper()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	pred := lineConnect(t, ctx, url, "pred", "", "")
	for i := 1; i <= n; i++ {
		pubEnv(t, ctx, js, lineInbox("pred"), "p"+string(rune('0'+i)), perf, "to the predecessor")
	}
	lineExit(t, ctx, pred)
	succ := lineConnect(t, ctx, url, "succ", "", "pred")
	runHeartbeat(t, ctx, succ, 40*time.Millisecond)
	if !waitFor(5*time.Second, func() bool { return len(inheritDurables(t, ctx, js, "succ")) == 1 }) {
		t.Fatal("nothing inherited")
	}
	return succ
}

// The default wait (max omitted) is one message per call. It must hand over the
// inherited mail once the seat's own is empty, mark each message, and let the
// drained end rule fire after the last.
func TestADefaultSingleWaitReturnsTheInheritedMail(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	succ := inheritedSuccessor(t, ctx, "INFORM", 3)
	var got []string
	for i := 0; i < 3; i++ {
		out, err := toolWait(ctx, succ, json.RawMessage(`{"timeout_seconds":2}`))
		if err != nil {
			t.Fatal(err)
		}
		m := out.(map[string]any)
		env, _ := m["message"].(*Envelope)
		if env == nil {
			t.Fatalf("wait %d returned no message: %v", i+1, m["note"])
		}
		got = append(got, env.MessageID)
		if m["inherited_from"] != "pred" {
			t.Errorf("wait %d: inherited_from = %v, want pred", i+1, m["inherited_from"])
		}
	}
	if len(got) != 3 || got[0] != "p1" || got[1] != "p2" || got[2] != "p3" {
		t.Errorf("single waits returned %v, want p1 p2 p3", got)
	}
	if !waitFor(5*time.Second, func() bool { c, _ := succ.inheritCons(); return c == nil }) {
		t.Error("the inherited durable was not deleted after its mail was read")
	}
}

// A QUERY left for the predecessor is flagged in the summary, and the flag must
// say it was the predecessor's, not read as addressed to this seat.
func TestTheSummarysFlaggedListMarksInheritedMail(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	succ := inheritedSuccessor(t, ctx, "QUERY", 1)
	sum, err := succ.summarizeInbox(ctx, summaryDefault)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Summary.Flagged) != 1 {
		t.Fatalf("CONTROL FAILED: %d flagged, want the one QUERY", len(sum.Summary.Flagged))
	}
	raw, _ := json.Marshal(sum.Summary.Flagged[0])
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["inherited_from"] != "pred" {
		t.Errorf("the flagged QUERY carries inherited_from = %v, want pred: %s", m["inherited_from"], raw)
	}
}

// Mail that came back from a cancelled wait keeps its marker in the single form.
func TestAReturnedInheritedMessageKeepsItsMarkerInTheSingleForm(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	succ := inheritedSuccessor(t, ctx, "INFORM", 1)
	res, err := succ.receiveBatch(ctx, 2*time.Second, 1)
	if err != nil || len(res.Items) != 1 || res.Items[0].InheritedFrom != "pred" {
		t.Fatalf("CONTROL FAILED: setup read %v (err %v)", res.Items, err)
	}
	succ.returned = append(succ.returned, res.Items[0])
	out := returnedResult(ctx, succ, waitArgs{Max: 1}, succ.takeReturned(1))
	if out["inherited_from"] != "pred" {
		t.Errorf("the single-form returned result carries inherited_from = %v, want pred: %v", out["inherited_from"], out)
	}
}
