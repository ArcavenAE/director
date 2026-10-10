package main

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// send_message to a broadcast:// address with an empty workspace or a bad
// team passes envelope validation (it checks the scheme only) and is refused
// by the broadcast branch in toolSend. That refusal must be audited at stage
// validate like any other (LR-6); without this test, dropping those two
// audits left the suite green (review G434).
func TestSendToMalformedBroadcastAddressIsAudited(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	auditStream(t, ctx, js)
	sender := liveSeat(t, ctx, url, Sender{AgentID: "sup", Workspace: "w", Team: "t"})
	s, err := js.Stream(ctx, "AGENT_AUDIT")
	if err != nil {
		t.Fatal(err)
	}
	seqRe := regexp.MustCompile(`audit: recorded agent\.audit seq (\d+)`)

	for _, to := range []string{"broadcast:///t", "broadcast://w/bad.team"} {
		raw, _ := json.Marshal(map[string]any{"to": to, "performative": "INFORM", "text": "x"})
		out, err := toolSend(ctx, sender, raw)
		if err == nil {
			t.Fatalf("%s: accepted as %v, want a refusal", to, out)
		}
		m := seqRe.FindStringSubmatch(err.Error())
		if m == nil {
			t.Fatalf("%s: refusal does not report an audit record: %v", to, err)
		}
		seq, _ := strconv.ParseUint(m[1], 10, 64)
		rec, err := s.GetMsg(ctx, seq)
		if err != nil {
			t.Fatal(err)
		}
		if rec.Header.Get("Director-Outcome") != "refused" || rec.Header.Get("Director-Stage") != "validate" {
			t.Errorf("%s: outcome %q stage %q, want refused at validate", to, rec.Header.Get("Director-Outcome"), rec.Header.Get("Director-Stage"))
		}
		if !strings.Contains(string(rec.Data), `"address":"`+to+`"`) {
			t.Errorf("%s: audit record is not the send as asked: %s", to, rec.Data)
		}
	}
}
