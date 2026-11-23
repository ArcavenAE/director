package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const rosterUsage = "director-mcp roster --json"

// The roster output is a published interface, documented in docs/roster-json.md.
// rosterSchemaVersion changes only when a reader of the old shape would misread
// the new one; a field added beside the others does not change it.
const rosterSchemaVersion = 1

// rosterExitNoBroker is the exit status when no broker answers at the address.
// A broker that answers and then fails the read is exit 1, so a caller can tell
// "director is not present" from "director is present and unreadable".
const rosterExitNoBroker = 3

// rosterRow is one presence row of the local broker, as a script reads it.
// Fresh is true when the row is no older than presenceTTL, the bound the
// presence bucket itself applies: an older row may belong to a seat that has
// since moved or gone (forRowFresh).
type rosterRow struct {
	AgentID   string `json:"agent_id"`
	Team      string `json:"team"`
	Workspace string `json:"workspace,omitempty"`
	Role      string `json:"role"`
	Instance  string `json:"instance"`
	State     string `json:"state,omitempty"`
	TS        string `json:"ts,omitempty"`
	// ConnectedAt is when the shim connected, PolledAt when it first called
	// wait_for_message and Waiting the count across its sources while it is
	// catching-up; each is omitted when the record has none.
	ConnectedAt string   `json:"connected_at,omitempty"`
	PolledAt    string   `json:"polled_at,omitempty"`
	Waiting     *float64 `json:"waiting,omitempty"`
	AgeSeconds  float64  `json:"age_seconds"`
	Fresh       bool     `json:"fresh"`
	// The cue's state as the seat published it, each omitted when the record has
	// none (a shim with the cue off, or an older one).
	Cue           string   `json:"cue,omitempty"`
	CueReason     string   `json:"cue_reason,omitempty"`
	CueNoncesSent *float64 `json:"cue_nonces_sent,omitempty"`
	CueProvedAt   string   `json:"cue_proved_at,omitempty"`
}

// rosterBroker names the broker a roster was read from, so a host with more
// than one cluster can tell whose rows they are. URL has no credential.
type rosterBroker struct {
	URL        string `json:"url"`
	ServerName string `json:"server_name,omitempty"`
}

type rosterReport struct {
	SchemaVersion int          `json:"schema_version"`
	Broker        rosterBroker `json:"broker"`
	ReadAt        string       `json:"read_at"`
	Rows          []rosterRow  `json:"rows"`
	Warnings      []string     `json:"warnings,omitempty"`
}

// readRosterRows lists the local presence rows without creating a consumer or
// writing anything. A row whose record cannot be read, or has no ts, is kept
// with fresh false and a warning, never dropped, so it cannot read as a seat
// that is not there.
func readRosterRows(ctx context.Context, js jetstream.JetStream, now time.Time) (rosterReport, error) {
	rep := rosterReport{SchemaVersion: rosterSchemaVersion, ReadAt: now.UTC().Format(time.RFC3339), Rows: []rosterRow{}}
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
	addr := brokerAddress(url)
	nc, js, err := dial(url, Sender{AgentID: "unread-reader"})
	if err != nil {
		if errors.Is(err, nats.ErrNoServers) {
			_, _ = fmt.Fprintf(errw, "director-mcp roster: no broker reachable: connect %s: %s\n", addr, connectReason(err, url))
			return rosterExitNoBroker
		}
		_, _ = fmt.Fprintf(errw, "director-mcp roster: connect %s: %s\n", addr, connectReason(err, url))
		return 1
	}
	defer nc.Close()
	rep, err := readRosterRows(ctx, js, time.Now())
	if err != nil {
		_, _ = fmt.Fprintf(errw, "director-mcp roster: %v\n", err)
		return 1
	}
	rep.Broker = rosterBroker{URL: addr, ServerName: nc.ConnectedServerName()}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rep); err != nil {
		_, _ = fmt.Fprintf(errw, "director-mcp roster: %v\n", err)
		return 1
	}
	return 0
}

// brokerAddress is the broker's URL without any user or password, so neither the
// document nor an error line carries a credential. The URL may be a
// comma-separated list, each server with its own credential, so every part is
// redacted. A part that does not parse is reported as "unparseable" and never
// echoed; the other parts are kept.
func brokerAddress(raw string) string {
	parts := strings.Split(raw, ",")
	for i, part := range parts {
		u, err := url.Parse(strings.TrimSpace(part))
		if err != nil || u.Host == "" {
			parts[i] = "unparseable"
			continue
		}
		u.User = nil
		parts[i] = u.String()
	}
	return strings.Join(parts, ",")
}

// connectReason is the text of a failed connect, for an error line. It must not
// carry the address's user or password.
func connectReason(err error, raw string) string {
	return err.Error()
}

func rosterRowFromKey(key string, rec map[string]any, now time.Time) (rosterRow, string) {
	parts := strings.Split(key, ".") // presence.<team>.<id>.<instance>
	str := func(k string) string { s, _ := rec[k].(string); return s }
	r := rosterRow{
		AgentID: str("agent_id"), Team: str("team"), Workspace: str("workspace"),
		Role: str("role"), Instance: str("instance"), State: str("state"), TS: str("ts"),
	}
	r.Cue, r.CueReason, r.CueProvedAt = str("cue"), str("cue_reason"), str("cue_proved_at")
	r.ConnectedAt, r.PolledAt = str("connected_at"), str("polled_at")
	if n, ok := rec["waiting"].(float64); ok {
		r.Waiting = &n
	}
	if n, ok := rec["cue_nonces_sent"].(float64); ok {
		r.CueNoncesSent = &n
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
