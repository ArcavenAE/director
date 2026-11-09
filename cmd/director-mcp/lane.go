package main

// The priority lane of inbox_summary. A waiting message that replies to a
// message director sent under an open operator ask is listed ahead of the
// rest, so an answer to an ask is not read after a run of GATEs.
//
// A reply joins an ask by one key only: its in_reply_to is the id of a message
// director sent, and the relay log ties that id to the ask ("Intake: I-<n>"
// beside "Outcome: sent; message <id>", written by send_message --ask). A reply
// that names nothing it replies to cannot join, and the join never guesses by
// sender. An ask is open until `dws ask-close` writes a close row for it. This
// reader writes nothing, and no clock closes anything: an item leaves the lane
// when its reply is acked or drained, or its ask is closed.
//
// Asks received before the relay link existed (laneCutoff, the merge of the
// change that writes it) have no message id to join. They show once, as a
// backlog line: how many are open and how old the oldest is.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// laneCutoff is when send_message began writing the relay link (director#540).
var laneCutoff = time.Date(2026, 11, 9, 3, 40, 49, 0, time.UTC)

type laneItem struct {
	Tier         string `json:"tier"`
	Sequence     uint64 `json:"sequence"`
	MessageID    string `json:"message_id"`
	Sender       string `json:"sender"`
	Performative string `json:"performative"`
	AskID        string `json:"ask_id"`
	Excerpt      string `json:"excerpt"`
}

type laneBacklog struct {
	Count            int     `json:"count"`
	OldestAgeSeconds float64 `json:"oldest_age_seconds"`
}

type inboxLane struct {
	Items   []laneItem  `json:"items"`
	Backlog laneBacklog `json:"backlog"`
	// Warning says why the lane could not be built. A lane that cannot be built
	// is reported, never shown as an empty one.
	Warning string `json:"warning,omitempty"`
}

// askRecords is what asks.jsonl says: when each ask arrived, and which are closed.
type askRecords struct {
	Received map[string]time.Time
	Closed   map[string]bool
}

var (
	relayHead    = regexp.MustCompile(`^## R-[0-9]+ `)
	relayIntake  = regexp.MustCompile(`^\*\*Intake:\*\* (I-[0-9]+)\s*$`)
	relaySentMsg = regexp.MustCompile(`^\*\*Outcome:\*\* sent; message (\S+)\s*$`)
	laneAskID    = regexp.MustCompile(`^I-[0-9]+$`)
)

// parseRelaySent maps the id of each message director sent under an ask to that
// ask. An entry with no ask, or one not sent, joins nothing.
func parseRelaySent(text string) map[string]string {
	out := map[string]string{}
	ask, msg := "", ""
	flush := func() {
		if ask != "" && msg != "" {
			out[msg] = ask
		}
		ask, msg = "", ""
	}
	for _, line := range strings.Split(text, "\n") {
		switch {
		case relayHead.MatchString(line):
			flush()
		case relayIntake.MatchString(line):
			ask = relayIntake.FindStringSubmatch(line)[1]
		case relaySentMsg.MatchString(line):
			msg = relaySentMsg.FindStringSubmatch(line)[1]
		}
	}
	flush()
	return out
}

var closeKinds = map[string]bool{"answered": true, "carded": true, "not-an-ask": true}

// readAskRecords reads asks.jsonl as dws does: torn or malformed lines are
// skipped, a row with an event key is not an ask, and a close row counts only
// with a minted-shape id and one of the three kinds. A missing file is no asks.
func readAskRecords(path string) (askRecords, error) {
	rec := askRecords{Received: map[string]time.Time{}, Closed: map[string]bool{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return rec, nil
	}
	if err != nil {
		return rec, err
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var e struct {
			Event      string `json:"event"`
			AskID      string `json:"ask_id"`
			ReceivedAt string `json:"received_at"`
			As         string `json:"as"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil || !laneAskID.MatchString(e.AskID) {
			continue
		}
		switch {
		case e.Event == "close":
			if closeKinds[e.As] {
				rec.Closed[e.AskID] = true
			}
		case e.Event == "":
			if t, err := time.Parse(time.RFC3339, e.ReceivedAt); err == nil {
				rec.Received[e.AskID] = t
			}
		}
	}
	return rec, sc.Err()
}

// buildLane is the pure part: the waiting items, the sent-message map, the ask
// records and the clock.
func buildLane(items []summaryItem, sent map[string]string, asks askRecords, now time.Time) inboxLane {
	l := inboxLane{Items: []laneItem{}}
	ordered := append([]summaryItem(nil), items...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ri, rj := tierRank(ordered[i].Tier), tierRank(ordered[j].Tier); ri != rj {
			return ri < rj
		}
		return ordered[i].Seq < ordered[j].Seq
	})
	for _, it := range ordered {
		if it.Env == nil || it.Env.InReplyTo == "" {
			continue
		}
		ask := sent[it.Env.InReplyTo]
		if ask == "" || asks.Closed[ask] {
			continue
		}
		l.Items = append(l.Items, laneItem{
			Tier: it.Tier, Sequence: it.Seq, MessageID: it.Env.MessageID,
			Sender: senderLabel(it.Env), Performative: it.Env.Performative,
			AskID: ask, Excerpt: excerpt(it.Env.Content.Data, 160),
		})
	}
	for id, at := range asks.Received {
		if asks.Closed[id] || !at.Before(laneCutoff) {
			continue
		}
		l.Backlog.Count++
		if age := now.Sub(at).Seconds(); age > l.Backlog.OldestAgeSeconds {
			l.Backlog.OldestAgeSeconds = age
		}
	}
	return l
}

// loadLane reads RELAY_LOG and $DIRECTOR_STATE/asks.jsonl and builds the lane.
// With no RELAY_LOG, or a relay log it cannot read, there is no lane and the
// Warning says so; the backlog line still comes from asks.jsonl.
func loadLane(items []summaryItem, now time.Time) inboxLane {
	var warns []string
	var asks askRecords
	if dir, err := stateDir(); err != nil {
		warns = append(warns, err.Error())
		asks = askRecords{Received: map[string]time.Time{}, Closed: map[string]bool{}}
	} else if rec, err := readAskRecords(filepath.Join(dir, "asks.jsonl")); err != nil {
		warns = append(warns, fmt.Sprintf("asks.jsonl not read: %v", err))
		asks = askRecords{Received: map[string]time.Time{}, Closed: map[string]bool{}}
	} else {
		asks = rec
	}
	sent := map[string]string{}
	if path := os.Getenv("RELAY_LOG"); path == "" {
		warns = append(warns, "RELAY_LOG is not set; replies cannot be joined to asks, so the lane is not built")
	} else if data, err := os.ReadFile(path); err != nil {
		warns = append(warns, fmt.Sprintf("RELAY_LOG %s not read: %v; the lane is not built", path, err))
	} else {
		sent = parseRelaySent(string(data))
	}
	l := buildLane(items, sent, asks, now)
	l.Warning = strings.Join(warns, "; ")
	return l
}
