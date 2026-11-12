package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// The timed look (sim/design/timed-inbox-look.md). `director-mcp look` reads
// one seat's unread mail, classifies it, and writes files; it never consumes,
// acks, publishes or creates anything, and no body leaves it. These run against
// the scratch broker of drain_broker_test.go and a scratch state directory.

const lookSubject = "agent.aae-orc.ops.director.inbox"

type lookRig struct {
	t     *testing.T
	ctx   context.Context
	url   string
	js    jetstream.JetStream
	state string
	now   time.Time
	bus   *Bus
}

func newLookRig(t *testing.T) *lookRig {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	bus, err := connect(ctx, url, Sender{AgentID: "director", Workspace: "aae-orc", Team: "ops"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bus.close)
	return &lookRig{t: t, ctx: ctx, url: url, js: js, state: filepath.Join(t.TempDir(), "look"), now: time.Now().UTC(), bus: bus}
}

// pub puts one envelope on a subject. Its body is the text; replyBy may be "".
func (r *lookRig) pub(subject, id, sender, perf, text, replyBy string) {
	r.t.Helper()
	e := testEnv(id, sender, perf, text)
	e.ReplyBy = replyBy
	e.Recipient.Address = "agent://ops/director"
	b, _ := json.Marshal(e)
	if _, err := r.js.Publish(r.ctx, subject, b); err != nil {
		r.t.Fatal(err)
	}
}

func (r *lookRig) run() (int, string) {
	r.t.Helper()
	var out, errb strings.Builder
	code := runLook(r.ctx, lookOpts{URL: r.url, State: r.state, Seat: "director", Now: func() time.Time { return r.now }}, &out, &errb)
	return code, out.String() + errb.String()
}

func (r *lookRig) file(name string) string {
	r.t.Helper()
	b, err := os.ReadFile(filepath.Join(r.state, name))
	if err != nil {
		r.t.Fatalf("the look wrote no %s: %v", name, err)
	}
	return string(b)
}

// brokerState is every durable's delivered sequence, ack floor, ack-pending,
// pending and redelivered counts, the consumer list, and the stream's range.
func brokerState(t *testing.T, ctx context.Context, js jetstream.JetStream) string {
	t.Helper()
	s, err := js.Stream(ctx, "AGENT_INBOX")
	if err != nil {
		t.Fatal(err)
	}
	si, err := s.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var rows []string
	for ci := range s.ListConsumers(ctx).Info() {
		rows = append(rows, fmt.Sprintf("%s delivered=%d floor=%d ackpending=%d pending=%d redelivered=%d", ci.Name, ci.Delivered.Stream, ci.AckFloor.Stream, ci.NumAckPending, ci.NumPending, ci.NumRedelivered))
	}
	sort.Strings(rows)
	return fmt.Sprintf("msgs=%d first=%d last=%d\n%s", si.State.Msgs, si.State.FirstSeq, si.State.LastSeq, strings.Join(rows, "\n"))
}

func TestLookShowsTheCountAndOldestAgeAndMovesNothing(t *testing.T) {
	r := newLookRig(t)
	r.pub(lookSubject, "m1", "sup", "INFORM", "first", "")
	r.pub(lookSubject, "m2", "sup", "INFORM", "second", "")
	before := brokerState(t, r.ctx, r.js)
	r.now = time.Now().UTC().Add(2 * time.Hour)
	code, out := r.run()
	if code != 0 {
		t.Fatalf("look exited %d: %s", code, out)
	}
	if after := brokerState(t, r.ctx, r.js); after != before {
		t.Fatalf("the look moved broker state:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	d := r.file("digest.txt")
	if !strings.Contains(d, "waiting: local 2") || !strings.Contains(d, "oldest 2h") {
		t.Errorf("digest = %q, want the count 2 and an oldest age of about 2h", d)
	}
	var hb struct {
		At     string `json:"at"`
		Local  int    `json:"local"`
		Reader string `json:"reader"`
	}
	if err := json.Unmarshal([]byte(r.file("heartbeat.json")), &hb); err != nil || hb.Local != 2 || hb.Reader != "ok" || hb.At == "" {
		t.Errorf("heartbeat = %+v (%v), want local 2, reader ok and a time", hb, err)
	}
}

func TestLookNamesGatesAndNeverAnotherSeatsMail(t *testing.T) {
	r := newLookRig(t)
	r.pub(lookSubject, "gate-mine", "builder-1", "INFORM", "GATE ArcavenAE/x#1 @abc1234 draft\nmore text", "")
	r.pub("agent.aae-orc.ops.someone-else.inbox", "gate-theirs", "their-sender", "INFORM", "GATE secret CANARY-BODY", "")
	code, out := r.run()
	if code != 0 {
		t.Fatalf("look exited %d: %s", code, out)
	}
	var n struct {
		Urgent []struct {
			Class string `json:"class"`
			ID    string `json:"id"`
			Stage string `json:"stage"`
		} `json:"urgent"`
	}
	if err := json.Unmarshal([]byte(r.file("notify.json")), &n); err != nil {
		t.Fatal(err)
	}
	if len(n.Urgent) != 1 || n.Urgent[0].Class != "gate" || n.Urgent[0].ID != "gate-mine" || n.Urgent[0].Stage != "draft" {
		t.Fatalf("urgent = %+v, want only my gate, stage draft", n.Urgent)
	}
	for _, name := range []string{"digest.txt", "notify.json", "heartbeat.json"} {
		for _, leak := range []string{"gate-theirs", "their-sender", "CANARY-BODY", "someone-else", "more text"} {
			if strings.Contains(r.file(name), leak) || strings.Contains(out, leak) {
				t.Errorf("%q leaked into %s or the output", leak, name)
			}
		}
	}
}

func TestLookClassifiesGatesRequestsAndOther(t *testing.T) {
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	future := time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339)
	cases := []struct {
		name         string
		perf, body   string
		replyBy      string
		class, stage string
		urgent       bool
	}{
		{"a REQUEST whose body starts with GATE is a gate", "REQUEST", "GATE (ready) x#2", "", "gate", "ready", true},
		{"an INFORM with a GATE line is a gate", "INFORM", "intro\nGATE x#3 draft", "", "gate", "draft", true},
		{"a REQUEST with no reply_by is never urgent", "REQUEST", "please look", "", "request", "", false},
		{"a REQUEST past its reply_by is urgent", "REQUEST", "please look", past, "request", "", true},
		{"a REQUEST before its reply_by is not urgent", "REQUEST", "please look", future, "request", "", false},
		{"anything else is other", "INFORM", "done", "", "other", "", false},
	}
	for _, c := range cases {
		class, stage, urgent := lookClassify(c.perf, c.body, c.replyBy, time.Now().UTC())
		if class != c.class || stage != c.stage || urgent != c.urgent {
			t.Errorf("%s: got %q %q urgent=%v, want %q %q urgent=%v", c.name, class, stage, urgent, c.class, c.stage, c.urgent)
		}
	}
}

func TestLookBodyIsNeverWritten(t *testing.T) {
	r := newLookRig(t)
	r.pub(lookSubject, "m1", "sup", "INFORM", "plain text with CANARY-ONE inside", "")
	r.pub(lookSubject, "m2", "sup", "REQUEST", "GATE draft with CANARY-TWO", "")
	code, out := r.run()
	if code != 0 {
		t.Fatalf("look exited %d: %s", code, out)
	}
	for _, name := range []string{"digest.txt", "notify.json", "heartbeat.json"} {
		c := r.file(name)
		if strings.Contains(c, "CANARY-ONE") || strings.Contains(c, "CANARY-TWO") || strings.Contains(out, "CANARY") {
			t.Errorf("a body canary reached %s or the output", name)
		}
	}
}

func TestLookWithTheBrokerDownWritesAFailureLineNotZero(t *testing.T) {
	r := newLookRig(t)
	r.url = "nats://127.0.0.1:1" // nothing listens
	code, _ := r.run()
	if code == 0 {
		t.Errorf("a look that could not read exited 0")
	}
	d := r.file("digest.txt")
	if !strings.Contains(d, "reader: ") || strings.Contains(d, "reader: ok") || strings.Contains(d, "waiting: local 0") {
		t.Errorf("digest = %q, want a reader failure line and no zero count", d)
	}
	var hb struct {
		Reader string `json:"reader"`
	}
	if err := json.Unmarshal([]byte(r.file("heartbeat.json")), &hb); err != nil || hb.Reader == "" || hb.Reader == "ok" {
		t.Errorf("heartbeat = %+v (%v), want a heartbeat whose reader names the failure", hb, err)
	}
}

func TestLookListsAClearedGateApartFromTheUrgentOnes(t *testing.T) {
	r := newLookRig(t)
	r.pub(lookSubject, "gate-a", "builder-1", "INFORM", "GATE x#1 draft", "")
	r.pub(lookSubject, "gate-b", "builder-2", "INFORM", "GATE x#2 ready", "")
	if err := os.MkdirAll(r.state, 0o700); err != nil {
		t.Fatal(err)
	}
	// gate-a is stream sequence 1 (the first message on the stream).
	cleared := `{"local/AGENT_INBOX/1":{"at":"2026-11-09T01:00:00Z","desk_id":"desk-9","desk_updated_at":"2026-11-09T01:00:00Z","author":"unverified"}}`
	if err := os.WriteFile(filepath.Join(r.state, "cleared.json"), []byte(cleared), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out := r.run(); code != 0 {
		t.Fatalf("look exited %d: %s", code, out)
	}
	var n struct {
		Urgent  []struct{ ID string } `json:"urgent"`
		Cleared []struct {
			ID     string `json:"id"`
			DeskID string `json:"desk_id"`
		} `json:"cleared"`
	}
	if err := json.Unmarshal([]byte(r.file("notify.json")), &n); err != nil {
		t.Fatal(err)
	}
	if len(n.Urgent) != 1 || n.Urgent[0].ID != "gate-b" || len(n.Cleared) != 1 || n.Cleared[0].ID != "gate-a" || n.Cleared[0].DeskID != "desk-9" {
		t.Fatalf("notify = %+v, want gate-b urgent and gate-a cleared with desk-9", n)
	}
	d := r.file("digest.txt")
	if !strings.Contains(d, "cleared") || !strings.Contains(d, "desk-9") {
		t.Errorf("digest = %q, want a cleared line with the desk id", d)
	}
}

func TestAMalformedClearedFileSilencesNothing(t *testing.T) {
	r := newLookRig(t)
	r.pub(lookSubject, "gate-a", "builder-1", "INFORM", "GATE x#1 draft", "")
	if err := os.MkdirAll(r.state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.state, "cleared.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _ = r.run()
	var n struct {
		Urgent []struct{ ID string } `json:"urgent"`
	}
	if err := json.Unmarshal([]byte(r.file("notify.json")), &n); err != nil || len(n.Urgent) != 1 {
		t.Fatalf("notify = %+v (%v), want the gate still urgent", n, err)
	}
	if d := r.file("digest.txt"); !strings.Contains(d, "cleared.json") {
		t.Errorf("digest = %q, want a reader line that names the bad cleared.json", d)
	}
}

func TestLookFilesAreModeZeroSix(t *testing.T) {
	r := newLookRig(t)
	r.pub(lookSubject, "m1", "sup", "INFORM", "x", "")
	if code, out := r.run(); code != 0 {
		t.Fatalf("look exited %d: %s", code, out)
	}
	for _, name := range []string{"digest.txt", "notify.json", "heartbeat.json"} {
		fi, err := os.Stat(filepath.Join(r.state, name))
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %v (%v), want 0600", name, fi.Mode().Perm(), err)
		}
	}
	if fi, err := os.Stat(r.state); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("state dir mode = %v (%v), want 0700", fi.Mode().Perm(), err)
	}
}

var _ = io.Discard

func TestLookClassifiesHandshakeLinesAsGates(t *testing.T) {
	for _, body := range []string{
		"Pre-GATE x#2@abc",
		"Pre-GATE v2 x#2@abc",
		"note\nWORD x#2@abc",
	} {
		class, _, urgent := lookClassify("INFORM", body, "", time.Now().UTC())
		if class != "gate" || !urgent {
			t.Errorf("%q: got %q urgent=%v, want gate urgent=true", body, class, urgent)
		}
	}
	if class, _, _ := lookClassify("INFORM", "WORDS about it", "", time.Now().UTC()); class != "other" {
		t.Errorf("WORDS about it: got %q, want other", class)
	}
}
