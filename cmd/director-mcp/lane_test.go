package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The priority lane. inbox_summary lists the replies to open operator asks
// before everything else. A reply joins an ask when its in_reply_to is the id
// of a message director sent under that ask (the relay log's "Outcome: sent;
// message <id>" line beside its "Intake: I-<n>" line). An ask is open until
// ask-close writes a close row for it. Asks received before the relay link
// existed cannot join anything, so they appear once, as a backlog line.

var laneNow = time.Date(2026, 11, 9, 8, 0, 0, 0, time.UTC)

const laneRelayLog = `# relay log

## R-1 (2026-11-09T05:00:00Z) -> agent://arcaven/arcaven-supervisor
**Intake:** I-1
**Kind:** relay
**Text, verbatim:**
> please review the thing
**Outcome:** sent; message 01MSENT1

## R-2 (2026-11-09T05:10:00Z) -> agent://arcaven/arcaven-supervisor
**Kind:** relay
**Text, verbatim:**
> an entry with no ask
**Outcome:** sent; message 01MNOASK

## R-3 (2026-11-09T05:20:00Z) -> agent://arcaven/arcaven-supervisor
**Intake:** I-2
**Kind:** relay
**Text, verbatim:**
> not sent yet
**Outcome:** pending
`

func laneMsg(tier string, seq uint64, id, perf, replyTo string) summaryItem {
	e := testEnv(id, "someone", perf, "text of "+id)
	e.InReplyTo = replyTo
	return summaryItem{Tier: tier, Seq: seq, Env: e}
}

func laneAsks(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "asks.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseRelaySentJoinsMessageIDsToAsks(t *testing.T) {
	got := parseRelaySent(laneRelayLog)
	if len(got) != 1 || got["01MSENT1"] != "I-1" {
		t.Fatalf("parseRelaySent = %v, want only 01MSENT1 -> I-1 (no ask, and not sent, join nothing)", got)
	}
}

func TestLaneListsTheReplyBeforeTheGates(t *testing.T) {
	asks := laneAsks(t, `{"ask_id":"I-1","received_at":"2026-11-09T04:59:00Z","text":"x","source":"operator"}`)
	rec, err := readAskRecords(asks)
	if err != nil {
		t.Fatal(err)
	}
	items := []summaryItem{
		laneMsg("local", 10, "g1", "INFORM", ""),
		laneMsg("local", 11, "g2", "INFORM", ""),
		laneMsg("local", 12, "g3", "INFORM", ""),
		laneMsg("local", 13, "reply", "INFORM", "01MSENT1"),
	}
	l := buildLane(items, parseRelaySent(laneRelayLog), rec, laneNow)
	if len(l.Items) != 1 || l.Items[0].MessageID != "reply" || l.Items[0].Sequence != 13 || l.Items[0].AskID != "I-1" {
		t.Fatalf("lane = %+v, want only the reply (seq 13) for I-1, ahead of the three GATEs", l.Items)
	}
	if l.Backlog.Count != 0 {
		t.Errorf("backlog = %+v, want empty: I-1 was received after the cutoff", l.Backlog)
	}
}

func TestLaneIsEmptyOnceTheAskIsClosed(t *testing.T) {
	asks := laneAsks(t,
		`{"ask_id":"I-1","received_at":"2026-11-09T04:59:00Z","text":"x","source":"operator"}`,
		`{"event":"close","ask_id":"I-1","closed_at":"2026-11-09T06:00:00Z","as":"answered","ref":"M"}`)
	rec, err := readAskRecords(asks)
	if err != nil {
		t.Fatal(err)
	}
	items := []summaryItem{laneMsg("local", 13, "reply", "INFORM", "01MSENT1")}
	l := buildLane(items, parseRelaySent(laneRelayLog), rec, laneNow)
	if len(l.Items) != 0 {
		t.Fatalf("lane = %+v, want empty after ask-close I-1", l.Items)
	}
}

func TestPreCutoffOpenAskShowsOnlyInTheBacklogLine(t *testing.T) {
	asks := laneAsks(t,
		`{"ask_id":"I-0","received_at":"2026-11-09T03:00:00Z","text":"old","source":"operator"}`,
		`{"ask_id":"I-1","received_at":"2026-11-09T04:59:00Z","text":"x","source":"operator"}`,
		`{"ask_id":"I-5","received_at":"2026-11-08T20:00:00Z","text":"older, closed","source":"operator"}`,
		`{"event":"close","ask_id":"I-5","closed_at":"2026-11-09T06:00:00Z","as":"not-an-ask","reason":"chatter"}`)
	rec, err := readAskRecords(asks)
	if err != nil {
		t.Fatal(err)
	}
	l := buildLane(nil, parseRelaySent(laneRelayLog), rec, laneNow)
	if len(l.Items) != 0 {
		t.Errorf("lane items = %+v, want none: a pre-cutoff ask lists no item", l.Items)
	}
	if l.Backlog.Count != 1 || l.Backlog.OldestAgeSeconds != 5*3600 {
		t.Errorf("backlog = %+v, want 1 open ask, oldest 5h old (I-0 at 03:00Z; I-5 is closed, I-1 is after the cutoff)", l.Backlog)
	}
}

func TestAReplyWithNoInReplyToDoesNotJoin(t *testing.T) {
	asks := laneAsks(t, `{"ask_id":"I-1","received_at":"2026-11-09T04:59:00Z","text":"x","source":"operator"}`)
	rec, _ := readAskRecords(asks)
	// From the very seat the ask went to, but it names nothing it replies to.
	m := laneMsg("local", 20, "unlinked", "INFORM", "")
	m.Env.Sender.AgentID = "arcaven-supervisor-g98-0"
	l := buildLane([]summaryItem{m, laneMsg("local", 21, "stray", "INFORM", "01MUNKNOWN")}, parseRelaySent(laneRelayLog), rec, laneNow)
	if len(l.Items) != 0 {
		t.Fatalf("lane = %+v, want empty: a join is by in_reply_to only, never by sender", l.Items)
	}
}

func TestLaneOrdersTiersThenSequence(t *testing.T) {
	log := laneRelayLog + `
## R-4 (2026-11-09T05:30:00Z) -> agent://x/y
**Intake:** I-3
**Kind:** relay
**Text, verbatim:**
> second
**Outcome:** sent; message 01MSENT3
`
	asks := laneAsks(t,
		`{"ask_id":"I-1","received_at":"2026-11-09T04:59:00Z","text":"x","source":"operator"}`,
		`{"ask_id":"I-3","received_at":"2026-11-09T05:29:00Z","text":"y","source":"operator"}`)
	rec, _ := readAskRecords(asks)
	items := []summaryItem{
		laneMsg("global", 3, "gr", "INFORM", "01MSENT1"),
		laneMsg("local", 40, "b", "INFORM", "01MSENT3"),
		laneMsg("local", 30, "a", "INFORM", "01MSENT1"),
	}
	l := buildLane(items, parseRelaySent(log), rec, laneNow)
	var got []string
	for _, it := range l.Items {
		got = append(got, it.Tier+":"+it.MessageID)
	}
	if strings.Join(got, ",") != "local:a,local:b,global:gr" {
		t.Fatalf("lane order = %v, want local by sequence then global", got)
	}
}

func TestLoadLaneWithoutRelayLogWarnsAndHasNoLane(t *testing.T) {
	t.Setenv("DIRECTOR_STATE", t.TempDir())
	t.Setenv("RELAY_LOG", "")
	l := loadLane([]summaryItem{laneMsg("local", 1, "r", "INFORM", "01MSENT1")}, laneNow)
	if len(l.Items) != 0 || !strings.Contains(l.Warning, "RELAY_LOG") {
		t.Fatalf("lane = %+v, want no items and a warning that names RELAY_LOG, never a silent empty lane", l)
	}
}

func TestLoadLaneReadsTheRelayLogAndAsksFile(t *testing.T) {
	state := t.TempDir()
	t.Setenv("DIRECTOR_STATE", state)
	rl := filepath.Join(t.TempDir(), "relay-log.md")
	if err := os.WriteFile(rl, []byte(laneRelayLog), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RELAY_LOG", rl)
	if err := os.WriteFile(filepath.Join(state, "asks.jsonl"), []byte(`{"ask_id":"I-1","received_at":"2026-11-09T04:59:00Z","text":"x","source":"operator"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l := loadLane([]summaryItem{laneMsg("local", 9, "r", "INFORM", "01MSENT1")}, laneNow)
	if l.Warning != "" || len(l.Items) != 1 || l.Items[0].AskID != "I-1" {
		t.Fatalf("lane = %+v, want the one reply for I-1 and no warning", l)
	}
	if err := os.Remove(rl); err != nil {
		t.Fatal(err)
	}
	l = loadLane([]summaryItem{laneMsg("local", 9, "r", "INFORM", "01MSENT1")}, laneNow)
	if len(l.Items) != 0 || l.Warning == "" {
		t.Fatalf("an unreadable relay log: lane = %+v, want a warning and no items", l)
	}
}
