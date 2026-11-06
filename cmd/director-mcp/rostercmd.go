package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

const rosterUsage = "director-mcp roster --json"

// rosterRow is one presence row of the local broker, as a script reads it.
// Fresh is true when the row is no older than presenceTTL, the bound the
// presence bucket itself applies: an older row may belong to a seat that has
// since moved or gone (forRowFresh).
type rosterRow struct {
	AgentID    string  `json:"agent_id"`
	Team       string  `json:"team"`
	Workspace  string  `json:"workspace,omitempty"`
	Role       string  `json:"role"`
	Instance   string  `json:"instance"`
	State      string  `json:"state,omitempty"`
	TS         string  `json:"ts,omitempty"`
	AgeSeconds float64 `json:"age_seconds"`
	Fresh      bool    `json:"fresh"`
}

type rosterReport struct {
	ReadAt   string      `json:"read_at"`
	Rows     []rosterRow `json:"rows"`
	Warnings []string    `json:"warnings,omitempty"`
}

// readRosterRows lists the local presence rows without creating a consumer or
// writing anything. A row whose record cannot be read, or has no ts, is kept
// with fresh false and a warning, never dropped, so it cannot read as a seat
// that is not there.
func readRosterRows(ctx context.Context, js jetstream.JetStream, now time.Time) (rosterReport, error) {
	rep := rosterReport{ReadAt: now.UTC().Format(time.RFC3339), Rows: []rosterRow{}}
	kv, err := js.KeyValue(ctx, "AGENT_STATE")
	if err != nil {
		return rep, fmt.Errorf("presence bucket AGENT_STATE: %w", err)
	}
	keys, err := quietKeys(ctx, js, "AGENT_STATE", kv)
	if err != nil {
		return rep, fmt.Errorf("presence keys: %w", err)
	}
	for _, k := range keys {
		if parts := strings.Split(k, "."); len(parts) != 4 || parts[0] != "presence" {
			continue
		}
		var rec map[string]any
		why := ""
		if e, err := kv.Get(ctx, k); err != nil {
			why = fmt.Sprintf("%s: unreadable: %v, so it is not fresh", k, err)
		} else if err := json.Unmarshal(e.Value(), &rec); err != nil {
			rec, why = nil, fmt.Sprintf("%s: the presence record is not JSON, so it is not fresh", k)
		}
		// the row is built from the key either way, so an unreadable record is a
		// seat that is not fresh, never one that is missing
		row, warn := rosterRowFromKey(k, rec, now)
		if why != "" {
			warn = why
		}
		if warn != "" {
			rep.Warnings = append(rep.Warnings, warn)
		}
		rep.Rows = append(rep.Rows, row)
	}
	sortRoster(rep.Rows)
	return rep, nil
}

// runRosterCmd connects, reads and prints the roster as JSON. It uses dial, as
// unread does, so it creates no durable and writes no presence. An unreachable
// broker is an error on stderr with a nonzero exit and nothing on stdout, so a
// caller never mistakes it for an empty roster.
func runRosterCmd(ctx context.Context, url string, cli cliArgs, out, errw io.Writer) int {
	nc, js, err := dial(url, Sender{AgentID: "unread-reader"})
	if err != nil {
		_, _ = fmt.Fprintf(errw, "director-mcp roster: connect %s: %v\n", url, err)
		return 1
	}
	defer nc.Close()
	rep, err := readRosterRows(ctx, js, time.Now())
	if err != nil {
		_, _ = fmt.Fprintf(errw, "director-mcp roster: %v\n", err)
		return 1
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rep); err != nil {
		_, _ = fmt.Fprintf(errw, "director-mcp roster: %v\n", err)
		return 1
	}
	return 0
}

func rosterRowFromKey(key string, rec map[string]any, now time.Time) (rosterRow, string) {
	parts := strings.Split(key, ".") // presence.<team>.<id>.<instance>
	str := func(k string) string { s, _ := rec[k].(string); return s }
	r := rosterRow{
		AgentID: str("agent_id"), Team: str("team"), Workspace: str("workspace"),
		Role: str("role"), Instance: str("instance"), State: str("state"), TS: str("ts"),
	}
	if r.AgentID == "" {
		r.AgentID = parts[2]
	}
	if r.Team == "" {
		r.Team = parts[1]
	}
	if r.Instance == "" {
		r.Instance = parts[3]
	}
	if r.Role == "" {
		// the id minus its generation suffix and its team prefix, as teamAndRole
		// derives it when no filter names the role
		r.Role = strings.TrimPrefix(instanceSuffix.ReplaceAllString(r.AgentID, ""), r.Team+"-")
	}
	warn := ""
	if ts, err := time.Parse(time.RFC3339, r.TS); err != nil {
		warn = fmt.Sprintf("%s: the presence row has no readable ts, so it is not fresh", key)
	} else {
		r.AgeSeconds = now.Sub(ts).Seconds()
		r.Fresh = r.AgeSeconds >= 0 && r.AgeSeconds <= presenceTTL.Seconds()
	}
	return r, warn
}

func sortRoster(rows []rosterRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Team != rows[j].Team {
			return rows[i].Team < rows[j].Team
		}
		if rows[i].AgentID != rows[j].AgentID {
			return rows[i].AgentID < rows[j].AgentID
		}
		return rows[i].Instance < rows[j].Instance
	})
}
