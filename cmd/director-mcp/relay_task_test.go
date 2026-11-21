package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// A relay entry for a send with an ask also names the task (the ask id, the
// fleet's form of a2a.task.id, marvel's session `task`) and the session as
// marvel keys it, <workspace>/<name>, never <team>/<id>. For a role recipient
// the session is unknown at send time, so the entry says unresolved, and the
// first reply that names the send appends a second entry that names it.

// relayEntries returns the entries of the relay log, each from its "## R-<n>" heading.
func relayEntries(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("no relay log written: %v", err)
	}
	var out []string
	for _, part := range strings.Split("\n"+string(b), "\n## R-")[1:] {
		out = append(out, "## R-"+part)
	}
	return out
}

// taskURL is the scratch server of the fixture's last run, for a test that needs a third seat. The tests do not run in parallel.
var taskURL string

func taskFixture(t *testing.T) (ctx context.Context, director, builder *Bus, log string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	url := startScratchServer(t)
	taskURL = url
	_, js := provision(t, ctx, url)
	auditStream(t, ctx, js)
	director = liveSeat(t, ctx, url, Sender{AgentID: "director", Workspace: "w", Team: "t"})
	builder = liveSeat(t, ctx, url, Sender{AgentID: "builder", Role: "builder", Workspace: "w", Team: "t"})
	askFixture(t, "I-7")
	log = relayFixture(t)
	return ctx, director, builder, log
}

func sendAsk(t *testing.T, ctx context.Context, from *Bus, extra map[string]any) string {
	t.Helper()
	out, err := toolSend(ctx, from, sendRaw(extra))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := out.(map[string]any)["message_id"].(string)
	if id == "" {
		t.Fatalf("no message_id in %v", out)
	}
	return id
}

func TestRelayEntryNamesTheTaskAndTheSessionForAnAgentRecipient(t *testing.T) {
	ctx, director, _, log := taskFixture(t)
	sendAsk(t, ctx, director, map[string]any{"to": "agent://t/builder", "ask": "I-7"})
	es := relayEntries(t, log)
	if len(es) != 1 {
		t.Fatalf("entries = %d, want 1:\n%v", len(es), es)
	}
	for _, want := range []string{"**Task:** I-7", "**Session:** w/builder"} {
		if !strings.Contains(es[0], want) {
			t.Errorf("entry lacks %q:\n%s", want, es[0])
		}
	}
	if strings.Contains(es[0], "**Session:** t/builder") {
		t.Errorf("the session is written as team/id, want workspace/name:\n%s", es[0])
	}
}

func TestRelayEntryForARoleRecipientSaysTheSessionIsUnresolved(t *testing.T) {
	ctx, director, _, log := taskFixture(t)
	sendAsk(t, ctx, director, map[string]any{"to": "role://t/builder", "ask": "I-7"})
	es := relayEntries(t, log)
	if len(es) != 1 {
		t.Fatalf("entries = %d, want 1:\n%v", len(es), es)
	}
	for _, want := range []string{"**Task:** I-7", "**Session:** unresolved"} {
		if !strings.Contains(es[0], want) {
			t.Errorf("entry lacks %q:\n%s", want, es[0])
		}
	}
}

func TestTheFirstReplyToAnUnresolvedSendAppendsAnEntryNamingTheSession(t *testing.T) {
	ctx, director, builder, log := taskFixture(t)
	id := sendAsk(t, ctx, director, map[string]any{"to": "role://t/builder", "ask": "I-7"})
	sendAsk(t, ctx, builder, map[string]any{"to": "agent://t/director", "performative": "INFORM", "text": "on it", "in_reply_to": id})
	if _, err := toolWait(ctx, director, json.RawMessage(`{"timeout_seconds":5}`)); err != nil {
		t.Fatal(err)
	}
	es := relayEntries(t, log)
	if len(es) != 2 {
		t.Fatalf("entries = %d, want 2 (the send, then the resolution):\n%v", len(es), es)
	}
	for _, want := range []string{"**Task:** I-7", "**Session:** w/builder"} {
		if !strings.Contains(es[1], want) {
			t.Errorf("second entry lacks %q:\n%s", want, es[1])
		}
	}
	if !strings.Contains(es[1], id) {
		t.Errorf("second entry does not name the message %s it resolves:\n%s", id, es[1])
	}
}

func TestOnlyTheFirstReplyResolvesSoALaterReplyFromAnotherSeatNamesNothingNew(t *testing.T) {
	ctx, director, builder, log := taskFixture(t)
	other := liveSeat(t, ctx, taskURL, Sender{AgentID: "builder2", Role: "builder", Workspace: "w", Team: "t"})
	id := sendAsk(t, ctx, director, map[string]any{"to": "role://t/builder", "ask": "I-7"})
	reply := func(from *Bus, text string) {
		sendAsk(t, ctx, from, map[string]any{"to": "agent://t/director", "performative": "INFORM", "text": text, "in_reply_to": id})
		if _, err := toolWait(ctx, director, json.RawMessage(`{"timeout_seconds":5}`)); err != nil {
			t.Fatal(err)
		}
	}
	reply(builder, "first")
	if es := relayEntries(t, log); len(es) != 2 || !strings.Contains(es[1], "**Session:** w/builder\n") {
		t.Fatalf("after the first reply: want 2 entries, the second naming w/builder, got %d:\n%v", len(es), es)
	}
	reply(other, "second, from another seat")
	es := relayEntries(t, log)
	if len(es) != 2 {
		t.Fatalf("entries = %d, want 2 (a later reply must not append a third):\n%v", len(es), es)
	}
	if strings.Contains(strings.Join(es, "\n"), "w/builder2") {
		t.Fatalf("the later replier was recorded as the session:\n%v", es)
	}
}

func TestAReplyToASendWithAnAgentRecipientAppendsNothingBecauseTheSessionIsKnown(t *testing.T) {
	ctx, director, builder, log := taskFixture(t)
	id := sendAsk(t, ctx, director, map[string]any{"to": "agent://t/builder", "ask": "I-7"})
	sendAsk(t, ctx, builder, map[string]any{"to": "agent://t/director", "performative": "INFORM", "text": "done", "in_reply_to": id})
	if _, err := toolWait(ctx, director, json.RawMessage(`{"timeout_seconds":5}`)); err != nil {
		t.Fatal(err)
	}
	es := relayEntries(t, log)
	if len(es) != 1 {
		t.Fatalf("entries = %d, want 1:\n%v", len(es), es)
	}
	if !strings.Contains(es[0], "**Session:** w/builder") {
		t.Fatalf("the one entry does not name the session w/builder:\n%s", es[0])
	}
}

func TestAReplyToASendWithNoAskAppendsNoEntry(t *testing.T) {
	ctx, director, builder, log := taskFixture(t)
	id := sendAsk(t, ctx, director, map[string]any{"to": "role://t/builder"})
	sendAsk(t, ctx, builder, map[string]any{"to": "agent://t/director", "performative": "INFORM", "text": "hi", "in_reply_to": id})
	if _, err := toolWait(ctx, director, json.RawMessage(`{"timeout_seconds":5}`)); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(log); err == nil && strings.Contains(string(b), "## R-") {
		t.Fatalf("a send with no ask left a relay entry:\n%s", b)
	}
	// The same exchange with an ask does resolve, so the silence above is a choice and not a dead path.
	id2 := sendAsk(t, ctx, director, map[string]any{"to": "role://t/builder", "ask": "I-7"})
	sendAsk(t, ctx, builder, map[string]any{"to": "agent://t/director", "performative": "INFORM", "text": "hi again", "in_reply_to": id2})
	if _, err := toolWait(ctx, director, json.RawMessage(`{"timeout_seconds":5}`)); err != nil {
		t.Fatal(err)
	}
	es := relayEntries(t, log)
	if len(es) != 2 || !strings.Contains(es[1], "**Session:** w/builder") {
		t.Fatalf("the asked exchange: want 2 entries, the second naming w/builder, got %d:\n%v", len(es), es)
	}
}

func TestAReportStatusInReplyToAnUnresolvedSendResolvesItToo(t *testing.T) {
	ctx, director, builder, log := taskFixture(t)
	id := sendAsk(t, ctx, director, map[string]any{"to": "role://t/builder", "ask": "I-7"})
	raw, _ := json.Marshal(map[string]any{"to": "agent://t/director", "in_reply_to": id, "status": "working"})
	if _, err := toolReportStatus(ctx, builder, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := toolWait(ctx, director, json.RawMessage(`{"timeout_seconds":5}`)); err != nil {
		t.Fatal(err)
	}
	es := relayEntries(t, log)
	if len(es) != 2 || !strings.Contains(es[1], "**Session:** w/builder") {
		t.Fatalf("want a second entry naming w/builder, got %d entries:\n%v", len(es), es)
	}
}
