package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// director#677 review: the default single-message wait, the summary's flagged
// list and the single form of a returned result left the inherited marker or the
// inherited mail itself out. Each case runs on a scratch nats-server.

func inheritedSuccessor(t *testing.T, ctx context.Context, perf string, n int) *Bus {
	b, _ := inheritedSuccessorJS(t, ctx, perf, n)
	return b
}

func inheritedSuccessorJS(t *testing.T, ctx context.Context, perf string, n int) (*Bus, jetstream.JetStream) {
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
	return succ, js
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

// With the default long wait and no mail of its own, a single wait must hand out
// the inherited mail at once, not one message per timeout: that delay is what
// the inheritance exists to remove.
func TestALongSingleWaitReturnsInheritedMailWithoutWaitingOutTheTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	succ := inheritedSuccessor(t, ctx, "INFORM", 3)
	for i := 1; i <= 3; i++ {
		start := time.Now()
		out, err := toolWait(ctx, succ, json.RawMessage(`{"timeout_seconds":30}`))
		if err != nil {
			t.Fatal(err)
		}
		env, _ := out.(map[string]any)["message"].(*Envelope)
		if env == nil {
			t.Fatalf("wait %d returned no message", i)
		}
		if took := time.Since(start); took > 5*time.Second {
			t.Errorf("wait %d took %v with inherited mail already waiting, want under 5s (timeout 30s)", i, took)
		}
	}
}

// Pin: the seat's own mail that is already waiting is returned before inherited
// mail, as in the batch form.
func TestOwnMailIsReturnedBeforeInheritedMailByASingleWait(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	succ, js := inheritedSuccessorJS(t, ctx, "INFORM", 2)
	pubEnv(t, ctx, js, lineInbox("succ"), "own1", "INFORM", "to this seat")
	var got []string
	for i := 0; i < 3; i++ {
		out, err := toolWait(ctx, succ, json.RawMessage(`{"timeout_seconds":2}`))
		if err != nil {
			t.Fatal(err)
		}
		if env, _ := out.(map[string]any)["message"].(*Envelope); env != nil {
			got = append(got, env.MessageID)
		}
	}
	if len(got) < 1 || got[0] != "own1" {
		t.Errorf("single waits returned %v, want own1 first", got)
	}
}
