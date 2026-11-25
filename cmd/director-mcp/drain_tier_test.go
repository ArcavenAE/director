package main

import (
	"strings"
	"testing"
	"time"
)

// The pure half of the drain's proposed tier: per-message tier and reason, and
// the all-routine fact line. The broker-backed half is drain_tier_broker_test.go.

func tierItem(seq uint64, perf, body string, edit func(*Envelope)) drained {
	e := testEnv("01TIER"+string(rune('A'+seq)), "sender", perf, body)
	e.Recipient.Address = "agent://ops/operator"
	if edit != nil {
		edit(e)
	}
	return drained{Env: e, Tier: "local", Seq: seq}
}

func statusInforms() []drained {
	return []drained{
		tierItem(1, "INFORM", "status: working on the digest step", nil),
		tierItem(2, "INFORM", "status: tests are running, nothing to decide", nil),
		tierItem(3, "INFORM", "status: pushed the branch; waiting on CI", nil),
	}
}

func TestTierBatchThreeStatusInformsEndWithTheFactLine(t *testing.T) {
	if allRoutineLine != "no board change; no reply owed" {
		t.Fatalf("allRoutineLine = %q", allRoutineLine)
	}
	items := statusInforms()
	line := tierBatch(newClassifier(), items, inboxLane{}, classifyNow)
	if line != "no board change; no reply owed" {
		t.Errorf("fact line = %q, want %q", line, allRoutineLine)
	}
	for _, it := range items {
		if it.ProposedTier != "routine" || it.Reason == "" {
			t.Errorf("seq %d: proposed_tier %q reason %q, want routine with a reason", it.Seq, it.ProposedTier, it.Reason)
		}
	}
}

func TestTierBatchOneCancelRemovesTheLine(t *testing.T) {
	items := append(statusInforms(), tierItem(4, "CANCEL", "stop that build", nil))
	line := tierBatch(newClassifier(), items, inboxLane{}, classifyNow)
	if line != "" {
		t.Errorf("fact line = %q, want none: one CANCEL is act", line)
	}
	if got := items[3]; got.ProposedTier != "act" || got.Reason != "performative CANCEL" {
		t.Errorf("CANCEL: proposed_tier %q reason %q, want act and %q", got.ProposedTier, got.Reason, "performative CANCEL")
	}
	for _, it := range items[:3] {
		if it.ProposedTier != "routine" {
			t.Errorf("seq %d: proposed_tier %q, want routine: the line leaving does not raise its neighbours", it.Seq, it.ProposedTier)
		}
	}
}

func TestTierBatchEmptyBatchHasNoLine(t *testing.T) {
	if line := tierBatch(newClassifier(), nil, inboxLane{}, classifyNow); line != "" {
		t.Errorf("fact line = %q for an empty batch, want none: nothing arrived is not a fact about what arrived", line)
	}
}

func TestTierBatchLaneItemIsNeverRoutine(t *testing.T) {
	// A status reply to an ask director sent is routine to the classifier alone
	// and a lane item to the operator: the lane wins.
	reply := tierItem(5, "INFORM", "status: working", func(e *Envelope) { e.Content = Content{Type: "signal", Data: "working"}; e.InReplyTo = "01MSENT1" })
	other := tierItem(6, "INFORM", "status: working", func(e *Envelope) { e.Content = Content{Type: "signal", Data: "working"} })
	items := []drained{reply, other}
	lane := inboxLane{Items: []laneItem{{Tier: "local", Sequence: 5, MessageID: reply.Env.MessageID, AskID: "I-1"}}}
	line := tierBatch(newClassifier(), items, lane, classifyNow)
	if line != "" {
		t.Errorf("fact line = %q, want none: a lane item is in the batch", line)
	}
	if got := items[0]; got.ProposedTier != "act" || !strings.HasPrefix(got.Reason, "lane") || !strings.Contains(got.Reason, "I-1") {
		t.Errorf("lane item: proposed_tier %q reason %q, want act and a reason that starts with lane and names I-1", got.ProposedTier, got.Reason)
	}
	if got := items[1]; got.ProposedTier != "routine" {
		t.Errorf("a status that is not in the lane: proposed_tier %q, want routine", got.ProposedTier)
	}
}

func TestTierBatchReasons(t *testing.T) {
	past := classifyNow.Add(-time.Hour).Format(time.RFC3339)
	cases := []struct {
		name       string
		item       drained
		tier, why  string
		priorAsk   string // an id this seat sent as a REQUEST before the batch
		repliesAsk bool
	}{
		{"request", tierItem(1, "REQUEST", "please review", nil), "act", "performative REQUEST", "", false},
		{"failure", tierItem(2, "FAILURE", "the build broke", nil), "act", "performative FAILURE", "", false},
		{"gate line", tierItem(3, "INFORM", "GATE director#1 @abc draft", nil), "act", "gate line", "", false},
		{"reply_by passed", tierItem(4, "INFORM", "fyi", func(e *Envelope) { e.ReplyBy = past }), "act", "reply_by passed", "", false},
		{"signal not working", tierItem(5, "INFORM", "x", func(e *Envelope) { e.Content = Content{Type: "signal", Data: "blocked-on y"} }), "act", "signal not working", "", false},
		{"first reply to an open ask", tierItem(6, "INFORM", "done", func(e *Envelope) { e.InReplyTo = "01ASKED" }), "act", "reply to an open ask", "01ASKED", true},
		{"routine inform", tierItem(7, "INFORM", "done", nil), "routine", "nothing asks for a turn", "", false},
		{"routine working signal", tierItem(8, "INFORM", "x", func(e *Envelope) { e.Content = Content{Type: "signal", Data: "working"} }), "routine", "nothing asks for a turn", "", false},
	}
	for _, c := range cases {
		cls := newClassifier()
		if c.repliesAsk {
			cls.noteSent(c.priorAsk, "REQUEST")
		}
		items := []drained{c.item}
		tierBatch(cls, items, inboxLane{}, classifyNow)
		if items[0].ProposedTier != c.tier || items[0].Reason != c.why {
			t.Errorf("%s: proposed_tier %q reason %q, want %q and %q", c.name, items[0].ProposedTier, items[0].Reason, c.tier, c.why)
		}
	}
}

func TestTierBatchClassifiesOncePerSequence(t *testing.T) {
	cls := newClassifier()
	items := statusInforms()
	tierBatch(cls, items, inboxLane{}, classifyNow)
	tierBatch(cls, items, inboxLane{}, classifyNow)
	if cls.decodes != 3 {
		t.Errorf("decodes = %d after two passes over three messages, want 3: a message is classified once, by tier and sequence", cls.decodes)
	}
}
