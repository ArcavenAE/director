package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestParseArgsRoster(t *testing.T) {
	got, err := parseArgs([]string{"roster", "--json"})
	if err != nil || got.mode != "roster" || !got.json {
		t.Fatalf("parseArgs = %+v, %v", got, err)
	}
	for _, bad := range [][]string{{"roster"}, {"roster", "--json", "x"}, {"roster", "--all"}} {
		if _, err := parseArgs(bad); err == nil {
			t.Errorf("parseArgs(%v) accepted", bad)
		}
	}
}

// roster --json lists the presence rows of the local broker, with each row's age
// and whether it is fresh (no older than presenceTTL), so a script can build a
// seat table without marvel. It reads only; it writes no presence of its own.
func TestRunRosterCmdListsPresenceWithAge(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	url := startScratchServerNoStreamList(t)
	_, js := provision(t, ctx, url)
	kv, err := js.KeyValue(ctx, "AGENT_STATE")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	put := func(key string, rec map[string]any) {
		b, _ := json.Marshal(rec)
		if _, err := kv.Put(ctx, key, b); err != nil {
			t.Fatal(err)
		}
	}
	put("presence.crew.crew-builder-g1-0.01AAA", map[string]any{"agent_id": "crew-builder-g1-0", "team": "crew", "workspace": "ws", "instance": "01AAA", "role": "builder", "state": "online", "ts": now.Add(-10 * time.Second).Format(time.RFC3339)})
	put("presence.crew.crew-reviewer-g2-0.01BBB", map[string]any{"agent_id": "crew-reviewer-g2-0", "team": "crew", "workspace": "ws", "instance": "01BBB", "state": "online", "ts": now.Add(-10 * time.Minute).Format(time.RFC3339)})

	var out, errb bytes.Buffer
	if code := runRosterCmd(ctx, url, cliArgs{mode: "roster", json: true}, &out, &errb); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errb.String())
	}
	var got struct {
		Rows []struct {
			AgentID    string  `json:"agent_id"`
			Team       string  `json:"team"`
			Role       string  `json:"role"`
			Instance   string  `json:"instance"`
			AgeSeconds float64 `json:"age_seconds"`
			Fresh      bool    `json:"fresh"`
		} `json:"rows"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v: %q", err, out.String())
	}
	if len(got.Rows) != 2 {
		t.Fatalf("rows = %+v", got.Rows)
	}
	by := map[string]int{}
	for i, r := range got.Rows {
		by[r.AgentID] = i
	}
	b, r := got.Rows[by["crew-builder-g1-0"]], got.Rows[by["crew-reviewer-g2-0"]]
	if !b.Fresh || b.Team != "crew" || b.Role != "builder" || b.Instance != "01AAA" || b.AgeSeconds < 5 || b.AgeSeconds > 60 {
		t.Errorf("fresh row = %+v", b)
	}
	if r.Fresh || r.Role != "reviewer" || r.AgeSeconds < 500 {
		t.Errorf("old row (role derived from the id) = %+v", r)
	}
	if len(got.Warnings) != 0 {
		t.Errorf("warnings = %v", got.Warnings)
	}
}

// A broker that cannot be reached prints nothing on stdout and a reason on
// stderr, exits 1, and is never an empty roster.
func TestRunRosterCmdUnreachableIsNotAnEmptyRoster(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out, errb bytes.Buffer
	code := runRosterCmd(ctx, "nats://127.0.0.1:1", cliArgs{mode: "roster", json: true}, &out, &errb)
	if code == 0 || out.Len() != 0 || !strings.Contains(errb.String(), "connect") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out.String(), errb.String())
	}
}

// A presence record that cannot be read stays in the roster as a row that is not
// fresh, with a warning; it is never dropped, so it cannot read as no seat.
func TestRunRosterCmdKeepsAnUnreadableRowAsNotFresh(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	url := startScratchServerNoStreamList(t)
	_, js := provision(t, ctx, url)
	kv, err := js.KeyValue(ctx, "AGENT_STATE")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kv.Put(ctx, "presence.crew.crew-builder-g1-0.01AAA", []byte("not json")); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := runRosterCmd(ctx, url, cliArgs{mode: "roster", json: true}, &out, &errb); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errb.String())
	}
	var got rosterReport
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 || got.Rows[0].Fresh || got.Rows[0].AgentID != "crew-builder-g1-0" || got.Rows[0].Instance != "01AAA" {
		t.Fatalf("rows = %+v, want the one row, not fresh, named from its key", got.Rows)
	}
	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "not JSON") {
		t.Errorf("warnings = %v, want one saying the record is not JSON", got.Warnings)
	}
}

// The unknown-arguments error lists every mode, roster included.
func TestUnknownArgumentsUsageListsRoster(t *testing.T) {
	_, err := parseArgs([]string{"nonsense"})
	if err == nil || !strings.Contains(err.Error(), rosterUsage) {
		t.Fatalf("err = %v, want the usage list to name %q", err, rosterUsage)
	}
}
