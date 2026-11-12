package main

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

var classifyNow = time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

func classifyEnv(t *testing.T, perf, body, replyBy string) []byte {
	t.Helper()
	return classifyEnvReply(t, perf, body, replyBy, "")
}

func classifyEnvReply(t *testing.T, perf, body, replyBy, inReplyTo string) []byte {
	t.Helper()
	e := Envelope{InReplyTo: inReplyTo,
		SchemaVersion: 1, MessageID: "01TESTMSG", Performative: perf,
		Recipient: Recipient{Address: "agent://cue/x"},
		Content:   Content{Type: "text", Data: body}, ReplyBy: replyBy,
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestClassifyActAndRoutine(t *testing.T) {
	past := classifyNow.Add(-time.Minute).Format(time.RFC3339)
	future := classifyNow.Add(time.Hour).Format(time.RFC3339)
	cases := []struct {
		name                string
		perf, body, replyBy string
		want                msgClass
	}{
		{"body GATE line in an INFORM", "INFORM", "status\nGATE ArcavenAE/director#9 @abc ready", "", classAct},
		{"CANCEL", "CANCEL", "stop", "", classAct},
		{"FAILURE", "FAILURE", "x", "", classAct},
		{"REFUSE", "REFUSE", "x", "", classAct},
		{"REQUEST", "REQUEST", "do it", "", classAct},
		{"QUERY", "QUERY", "which?", "", classAct},
		{"REQUEST saying no wake is needed stays act", "REQUEST", "no wake needed, read it later", "", classAct},
		{"passed reply_by", "INFORM", "fyi", past, classAct},
		{"unknown performative", "SHOUT", "x", "", classAct},
		{"unparseable reply_by", "INFORM", "fyi", "soon", classAct},
		{"bare AGREE", "AGREE", "ok", "", classRoutine},
		{"plain INFORM", "INFORM", "status: working", "", classRoutine},
		{"INFORM with a future reply_by", "INFORM", "fyi", future, classRoutine},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := newClassifier().classify("local", 1, classifyEnv(t, c.perf, c.body, c.replyBy), classifyNow)
			if got != c.want {
				t.Fatalf("classify = %q, want %q", got, c.want)
			}
		})
	}
}

func TestClassifyFailsOpen(t *testing.T) {
	for name, data := range map[string][]byte{
		"undecodable":   []byte("{not json"),
		"empty":         nil,
		"no message id": []byte(`{"performative":"AGREE"}`),
	} {
		if got := newClassifier().classify("local", 1, data, classifyNow); got != classAct {
			t.Errorf("%s: classify = %q, want act", name, got)
		}
	}
	if got := newClassifier().classify("local", 0, classifyEnv(t, "AGREE", "ok", ""), classifyNow); got != classAct {
		t.Errorf("missing sequence: classify = %q, want act", got)
	}
}

func TestClassifyReplyByPassesLater(t *testing.T) {
	c := newClassifier()
	rb := classifyNow.Add(time.Minute).Format(time.RFC3339)
	data := classifyEnv(t, "INFORM", "fyi", rb)
	if got := c.classify("local", 5, data, classifyNow); got != classRoutine {
		t.Fatalf("before reply_by: %q, want routine", got)
	}
	if got := c.classify("local", 5, data, classifyNow.Add(2*time.Minute)); got != classAct {
		t.Fatalf("after reply_by: %q, want act", got)
	}
}

func TestClassifyOncePerSequence(t *testing.T) {
	c := newClassifier()
	const n = 3000
	data := classifyEnv(t, "INFORM", "backlog", "")
	for round := 0; round < 2; round++ {
		for seq := uint64(1); seq <= n; seq++ {
			c.classify("local", seq, data, classifyNow)
		}
	}
	if c.decodes != n {
		t.Fatalf("decoded %d times for %d messages seen twice, want %d", c.decodes, n, n)
	}
}

func TestClassifyKeyIncludesTier(t *testing.T) {
	c := newClassifier()
	routine := classifyEnv(t, "INFORM", "fyi", "")
	cancel := classifyEnv(t, "CANCEL", "stop", "")
	if got := c.classify("local", 7, routine, classifyNow); got != classRoutine {
		t.Fatalf("local 7: %q", got)
	}
	if got := c.classify("global", 7, cancel, classifyNow); got != classAct {
		t.Fatalf("global 7 reused the local sequence 7 entry: %q, want act", got)
	}
}

func TestClassifyMemoIsBounded(t *testing.T) {
	c := newClassifier()
	data := classifyEnv(t, "INFORM", "x", "")
	for seq := uint64(1); seq <= 100000; seq++ {
		c.classify("local", seq, data, classifyNow)
	}
	if got := c.size(); got == 0 || got > 20000 {
		t.Fatal(fmt.Sprintf("memo holds %d entries after 100000 messages", got))
	}
}

func TestClassifyReplyToOpenAsk(t *testing.T) {
	c := newClassifier()
	c.noteSent("ASK1", "REQUEST")
	c.noteSent("ASK2", "QUERY")
	c.noteSent("INF1", "INFORM")
	reply := func(seq uint64, to string) msgClass {
		return c.classify("local", seq, classifyEnvReply(t, "AGREE", "ok", "", to), classifyNow)
	}
	if got := reply(1, "ASK1"); got != classAct {
		t.Errorf("first reply to an open REQUEST: %q, want act", got)
	}
	if got := reply(2, "ASK1"); got != classRoutine {
		t.Errorf("second reply to the same REQUEST: %q, want routine", got)
	}
	if got := reply(3, "ASK2"); got != classAct {
		t.Errorf("first reply to an open QUERY: %q, want act", got)
	}
	if got := reply(4, "NO-SUCH-ASK"); got != classAct {
		t.Errorf("reply to an id the seat cannot match: %q, want act (fail open)", got)
	}
	if got := reply(6, ""); got != classRoutine {
		t.Errorf("AGREE that replies to nothing: %q, want routine", got)
	}
}

func TestClassifyBacklogBoundary(t *testing.T) {
	c := newClassifier()
	if c.isBacklog("local", 1) {
		t.Fatal("no floor set: nothing is backlog")
	}
	c.setBacklogFloor("local", 100)
	for _, tc := range []struct {
		tier string
		seq  uint64
		want bool
	}{
		{"local", 1, true}, {"local", 100, true}, {"local", 101, false},
		{"global", 50, false},
	} {
		if got := c.isBacklog(tc.tier, tc.seq); got != tc.want {
			t.Errorf("isBacklog(%s, %d) = %v, want %v", tc.tier, tc.seq, got, tc.want)
		}
	}
}

func TestClassifyBacklogIsClassifiedOnceAndKeepsItsClass(t *testing.T) {
	c := newClassifier()
	const n = 3000
	c.setBacklogFloor("local", n)
	cancel := classifyEnv(t, "CANCEL", "stop", "")
	info := classifyEnv(t, "INFORM", "old status", "")
	for round := 0; round < 2; round++ {
		for seq := uint64(1); seq <= n; seq++ {
			data := info
			if seq == 7 {
				data = cancel
			}
			want := classRoutine
			if seq == 7 {
				want = classAct
			}
			if got := c.classify("local", seq, data, classifyNow); got != want {
				t.Fatalf("backlog seq %d: %q, want %q", seq, got, want)
			}
		}
	}
	if c.decodes != n {
		t.Fatalf("decoded %d times, want %d", c.decodes, n)
	}
}

func TestClassifyFirstReplySurvivesEviction(t *testing.T) {
	c := newClassifier()
	c.noteSent("ASK1", "REQUEST")
	first := classifyEnvReply(t, "AGREE", "ok", "", "ASK1")
	if got := c.classify("local", 1, first, classifyNow); got != classAct {
		t.Fatalf("first reply: %q, want act", got)
	}
	filler := classifyEnv(t, "INFORM", "x", "")
	for seq := uint64(2); seq <= 40000; seq++ {
		c.classify("local", seq, filler, classifyNow)
	}
	if got := c.classify("local", 1, first, classifyNow); got != classAct {
		t.Fatalf("first reply reclassified after eviction: %q, want act", got)
	}
	if got := c.classify("local", 50000, classifyEnvReply(t, "AGREE", "again", "", "ASK1"), classifyNow); got != classRoutine {
		t.Fatalf("second reply after the first was evicted: %q, want routine", got)
	}
}

func TestClassifyFirstReplyWithPassedReplyByConsumesAsk(t *testing.T) {
	c := newClassifier()
	c.noteSent("ASK1", "REQUEST")
	past := classifyNow.Add(-time.Minute).Format(time.RFC3339)
	if got := c.classify("local", 1, classifyEnvReply(t, "AGREE", "ok", past, "ASK1"), classifyNow); got != classAct {
		t.Fatalf("first reply with a passed reply_by: %q, want act", got)
	}
	if got := c.classify("local", 2, classifyEnvReply(t, "AGREE", "again", "", "ASK1"), classifyNow); got != classRoutine {
		t.Fatalf("next reply: %q, want routine, the ask was answered by the first", got)
	}
}

func TestClassifyReplyToOwnNonAsk(t *testing.T) {
	c := newClassifier()
	c.noteSent("INF1", "INFORM")
	reply := func(seq uint64, perf, body, to string) msgClass {
		return c.classify("local", seq, classifyEnvReply(t, perf, body, "", to), classifyNow)
	}
	if got := reply(1, "AGREE", "ok", "INF1"); got != classRoutine {
		t.Errorf("AGREE replying to the seat's own INFORM: %q, want routine", got)
	}
	if got := reply(2, "REFUSE", "no", "INF1"); got != classAct {
		t.Errorf("REFUSE replying to the seat's own INFORM: %q, want act", got)
	}
	if got := reply(3, "FAILURE", "x", "INF1"); got != classAct {
		t.Errorf("FAILURE replying to the seat's own INFORM: %q, want act", got)
	}
	if got := reply(4, "INFORM", "done\nGATE ArcavenAE/director#9 @abc ready", "INF1"); got != classAct {
		t.Errorf("body GATE line replying to the seat's own INFORM: %q, want act", got)
	}
	if got := reply(5, "AGREE", "ok", "NEVER-SENT"); got != classAct {
		t.Errorf("reply to an id the seat never recorded: %q, want act (fail open)", got)
	}
}

func TestClassifySentRecordEvictionFallsBackToAct(t *testing.T) {
	c := newClassifier()
	c.noteSent("OLD", "INFORM")
	for i := 0; i < 5000; i++ {
		c.noteSent(fmt.Sprintf("NEW%d", i), "INFORM")
	}
	if got := c.classify("local", 1, classifyEnvReply(t, "AGREE", "ok", "", "OLD"), classifyNow); got != classAct {
		t.Fatalf("reply to an evicted sent id: %q, want act", got)
	}
	if got := c.classify("local", 2, classifyEnvReply(t, "AGREE", "ok", "", "NEW4999"), classifyNow); got != classRoutine {
		t.Fatalf("reply to a recent sent INFORM: %q, want routine", got)
	}
}

func classifySignal(t *testing.T, data, inReplyTo string) []byte {
	t.Helper()
	e := Envelope{
		SchemaVersion: 1, MessageID: "01TESTSIG", Performative: "INFORM", InReplyTo: inReplyTo,
		Recipient: Recipient{Address: "agent://cue/x"},
		Content:   Content{Type: "signal", Data: data},
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestClassifyWorkingSignalIsRoutineAndKeepsTheAskOpen(t *testing.T) {
	c := newClassifier()
	c.noteSent("ASK1", "REQUEST")
	if got := c.classify("local", 1, classifySignal(t, "working", "ASK1"), classifyNow); got != classRoutine {
		t.Fatalf("working signal: %q, want routine", got)
	}
	if got := c.classify("local", 2, classifyEnvReply(t, "AGREE", "done", "", "ASK1"), classifyNow); got != classAct {
		t.Fatalf("the answer after a working signal: %q, want act (still the first reply)", got)
	}
}

func TestClassifyBlockedOnSignalIsAct(t *testing.T) {
	c := newClassifier()
	c.noteSent("ASK1", "REQUEST")
	if got := c.classify("local", 1, classifySignal(t, "blocked-on agent://cue/x", "ASK1"), classifyNow); got != classAct {
		t.Fatalf("blocked-on signal: %q, want act", got)
	}
	if got := c.classify("local", 2, classifyEnvReply(t, "AGREE", "done", "", "ASK1"), classifyNow); got != classAct {
		t.Fatalf("the answer after a blocked-on signal: %q, want act", got)
	}
}

func TestClassifyUnknownSignalIsAct(t *testing.T) {
	c := newClassifier()
	c.noteSent("ASK1", "REQUEST")
	for i, data := range []string{"finished", "", "working on it", "WORKING"} {
		if got := c.classify("local", uint64(i+1), classifySignal(t, data, "ASK1"), classifyNow); got != classAct {
			t.Errorf("signal %q: %q, want act", data, got)
		}
	}
}

func TestClassifyProposalFamilyIsAct(t *testing.T) {
	for i, perf := range []string{"PROPOSE", "CFP", "NOT-UNDERSTOOD"} {
		got := newClassifier().classify("local", uint64(i+1), classifyEnv(t, perf, "x", ""), classifyNow)
		if got != classAct {
			t.Errorf("%s: %q, want act", perf, got)
		}
	}
}

func TestClassifyReplyToOwnProposeIsAct(t *testing.T) {
	for i, perf := range []string{"ACCEPT-PROPOSAL", "REJECT-PROPOSAL"} {
		c := newClassifier()
		c.noteSent("PROP1", "PROPOSE")
		got := c.classify("local", uint64(i+1), classifyEnvReply(t, perf, "x", "", "PROP1"), classifyNow)
		if got != classAct {
			t.Errorf("%s replying to the seat's own PROPOSE: %q, want act", perf, got)
		}
	}
	c := newClassifier()
	c.noteSent("CFP1", "CFP")
	if got := c.classify("local", 1, classifyEnvReply(t, "PROPOSE", "x", "", "CFP1"), classifyNow); got != classAct {
		t.Errorf("PROPOSE replying to the seat's own CFP: %q, want act", got)
	}
}
