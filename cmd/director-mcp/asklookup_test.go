package main

import (
	"strings"
	"testing"
	"time"
)

// A sender holding a message id can ask what became of it (aae-orc-t77rr,
// item 3 of the issue). An unanswered ask older than the inbox keeps mail is
// named as one that may have expired unread, which the ledger's own
// open-and-stale view does not say for one id.
func TestFindByIDAnswersByMessageIDAndNamesAnExpiredAsk(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	l := newAskLedger()
	add := func(id string, state string, age time.Duration, thread ...string) {
		l.Rows[id] = &askRow{Ask: id, State: state, StateAt: stamp(now.Add(-age)),
			LastMsgAt: now.Add(-age), AskedAt: now.Add(-age), ThreadIDs: thread}
	}
	add("m-fresh", askSent, 10*time.Minute)
	add("m-old", askSent, inboxMaxAge+time.Hour)
	add("m-done", askAnswered, 2*time.Hour, "m-done-reply")
	add("m-cancel", askCancelled, 3*time.Hour)
	l.reindex()

	cases := []struct {
		id    string
		found bool
		want  string
	}{
		{"m-fresh", true, "no acknowledgement yet"},
		{"m-old", true, "no acknowledgement yet"}, // age alone no longer says expired
		{"m-done-reply", true, "answered"},        // a thread id finds its row
		{"m-nobody", false, "no row"},
	}
	for _, c := range cases {
		got := l.FindByID(c.id, now)
		if got.Found != c.found || !strings.Contains(got.Verdict, c.want) {
			t.Errorf("FindByID(%q) = found %v, %q; want found %v containing %q", c.id, got.Found, got.Verdict, c.found, c.want)
		}
	}
	// A cancel is the asker's own message, so the verdict must not credit the
	// recipient with it.
	if got := l.FindByID("m-cancel", now); !strings.Contains(got.Verdict, "cancelled by the asker") || strings.Contains(got.Verdict, "recipient") {
		t.Errorf("a cancelled ask read as %q", got.Verdict)
	}
	if got := l.FindByID("m-fresh", now); strings.Contains(got.Verdict, "expired") {
		t.Errorf("a fresh unanswered ask read as expired: %q", got.Verdict)
	}
}

func TestAsksParsesTheIDFlag(t *testing.T) {
	cli, err := parseArgs([]string{"asks", "--id", "01ABC"})
	if err != nil || cli.askID != "01ABC" {
		t.Fatalf("parseArgs asks --id = %+v, %v", cli, err)
	}
	if _, err := parseArgs([]string{"asks", "--id"}); err == nil {
		t.Error("asks --id with no value parsed")
	}
}
