package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The pure halves of the batch drain and the unread summary: argument caps,
// ordering, flagging, and the counts. The broker-backed behavior is in
// drain_broker_test.go.

func testEnv(id, sender, perf, text string) *Envelope {
	return &Envelope{
		SchemaVersion: 1, MessageID: id, Performative: perf,
		Sender:  Sender{AgentID: sender, Workspace: "aae-orc"},
		Content: Content{Type: "text", Data: text},
	}
}

func TestParseWaitArgsDefaultsAndCaps(t *testing.T) {
	cases := []struct {
		raw          string
		timeout, max int
	}{
		{`{}`, 30, 1},
		{`{"max":0}`, 30, 1},
		{`{"max":-4}`, 30, 1},
		{`{"max":12,"timeout_seconds":5}`, 5, 12},
		{`{"max":5000,"timeout_seconds":999}`, 120, maxDrain},
		{`not json`, 30, 1},
	}
	for _, c := range cases {
		a := parseWaitArgs(json.RawMessage(c.raw))
		if a.TimeoutSeconds != c.timeout || a.Max != c.max {
			t.Errorf("%s: got timeout %d max %d, want %d %d", c.raw, a.TimeoutSeconds, a.Max, c.timeout, c.max)
		}
	}
}

func TestOrderDrainedIsOldestFirstPerTierLocalFirst(t *testing.T) {
	items := []drained{
		{Tier: "global", Seq: 3}, {Tier: "local", Seq: 9}, {Tier: "local", Seq: 2},
		{Tier: "global", Seq: 1}, {Tier: "local", Seq: 5},
	}
	orderDrained(items)
	var got []string
	for _, it := range items {
		got = append(got, it.Tier+":"+string(rune('0'+it.Seq)))
	}
	want := []string{"local:2", "local:5", "local:9", "global:1", "global:3"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestFlagReasons(t *testing.T) {
	cases := []struct {
		name string
		e    *Envelope
		want []string
	}{
		{"plain inform", testEnv("a", "x", "INFORM", "roll call: present, nothing pending"), nil},
		{"request", testEnv("b", "x", "REQUEST", "please review"), []string{"performative REQUEST"}},
		{"failure", testEnv("c", "x", "FAILURE", "build broke"), []string{"performative FAILURE"}},
		{"query", testEnv("d", "x", "QUERY", "which branch?"), []string{"performative QUERY"}},
		{"holding for director", testEnv("e", "x", "INFORM", "New seat up, holding for director instructions."), []string{"awaits a reply"}},
		{"custody", testEnv("f", "x", "INFORM", "I hold custody of the findings until someone reads them."), []string{"holds custody"}},
		{"awaiting reply", testEnv("g", "x", "INFORM", "Done; awaiting your reply before I merge."), []string{"awaits a reply"}},
	}
	for _, c := range cases {
		got := flagReasons(c.e)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: reasons = %v, want %v", c.name, got, c.want)
		}
	}
	e := testEnv("h", "x", "INFORM", "fyi")
	e.ReplyBy = "2026-09-25T12:00:00Z"
	if got := flagReasons(e); len(got) != 1 || !strings.HasPrefix(got[0], "reply_by ") {
		t.Errorf("reply_by should flag: %v", got)
	}
}

func TestSummarizeCountsFlagsAndSequences(t *testing.T) {
	items := []summaryItem{
		{Tier: "global", Seq: 7, Env: testEnv("g1", "director", "REQUEST", "status?")},
		{Tier: "local", Seq: 40, Env: testEnv("l3", "seat-b", "INFORM", "holding for director instructions")},
		{Tier: "local", Seq: 12, Env: testEnv("l1", "seat-a", "INFORM", "roll call: here")},
		{Tier: "local", Seq: 20, Env: testEnv("l2", "seat-a", "INFORM", "roll call: here again")},
		{Tier: "local", Seq: 31, Env: nil},
	}
	s := summarize(items)
	if s.Total != 5 || s.Undecodable != 1 {
		t.Errorf("total %d undecodable %d, want 5 and 1", s.Total, s.Undecodable)
	}
	if want := map[string]int{"seat-a@aae-orc": 2, "seat-b@aae-orc": 1, "director@aae-orc": 1}; !reflect.DeepEqual(s.BySender, want) {
		t.Errorf("by_sender = %v, want %v", s.BySender, want)
	}
	if want := map[string]int{"INFORM": 3, "REQUEST": 1}; !reflect.DeepEqual(s.ByPerformative, want) {
		t.Errorf("by_performative = %v, want %v", s.ByPerformative, want)
	}
	if want := map[string][]uint64{"local": {12, 20, 31, 40}, "global": {7}}; !reflect.DeepEqual(s.Sequences, want) {
		t.Errorf("sequences = %v, want %v (oldest first per tier)", s.Sequences, want)
	}
	if len(s.Flagged) != 2 || s.Flagged[0].Sequence != 40 || s.Flagged[1].Tier != "global" {
		t.Errorf("flagged = %+v, want local seq 40 then the global REQUEST", s.Flagged)
	}
}

func TestSummarizeEmptyHasNonNilCollections(t *testing.T) {
	s := summarize(nil)
	b, _ := json.Marshal(s)
	if strings.Contains(string(b), "null") {
		t.Errorf("an empty summary should marshal empty collections, not null: %s", b)
	}
}

func TestCatalogCarriesBatchAndSummary(t *testing.T) {
	var sawMax, sawSummary bool
	for _, td := range toolCatalog(nil) {
		if td.Name == "wait_for_message" {
			props := td.InputSchema["properties"].(map[string]any)
			_, sawMax = props["max"]
		}
		if td.Name == "inbox_summary" {
			sawSummary = true
			if !strings.Contains(td.Description, "Acks nothing") {
				t.Errorf("inbox_summary must say it acks nothing: %q", td.Description)
			}
		}
	}
	if !sawMax || !sawSummary {
		t.Errorf("catalog missing max (%v) or inbox_summary (%v)", sawMax, sawSummary)
	}
}

func TestExcerptCollapsesAndTruncates(t *testing.T) {
	if got := excerpt("a\n\n  b\tc", 10); got != "a b c" {
		t.Errorf("excerpt = %q", got)
	}
	if got := excerpt(strings.Repeat("x", 200), 5); got != "xxxxx..." {
		t.Errorf("excerpt = %q", got)
	}
}
