package main

// The timed look (sim/design/timed-inbox-look.md). `director-mcp look` reads
// one seat's unread mail and writes three files under a state directory; it
// never consumes, acks, publishes or creates anything, and no body leaves it.
//
//	digest.txt     the text an operator reads, rewritten in full each run
//	notify.json    the urgent set (and the cleared gates), for the poster step
//	heartbeat.json the time and the counts, written every run, failed or not
//
// What it asks the broker is limited to the design's allowlist: STREAM.INFO,
// CONSUMER.NAMES, CONSUMER.INFO and a subject-filtered message get (direct get,
// or STREAM.MSG.GET on a stream without it). The get always carries a filter
// subject taken from the seat's own durable, so another seat's mail is not
// read. This version reads the local tier (AGENT_INBOX) only; the digest says
// the global tier was not read, so an empty digest cannot be taken for an empty
// inbox. The poster (look-notify, with its reminders and the cap), look-clear
// and look-check are separate steps and are not here.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

type lookOpts struct {
	URL   string
	State string
	Seat  string
	Now   func() time.Time
}

const lookStream = "AGENT_INBOX"

type lookItem struct {
	Tier   string `json:"tier"`
	Stream string `json:"stream"`
	Seq    uint64 `json:"seq"`
	Class  string `json:"class"`
	ID     string `json:"id"`
	Sender string `json:"sender"`
	AgeS   int64  `json:"age_s"`
	Stage  string `json:"stage,omitempty"`

	word    string // the word a gate line starts with: GATE, Pre-GATE or WORD
	perf    string
	sellBy  string
	stored  time.Time
	urgent  bool
	cleared *lookCleared
}

type lookCleared struct {
	At     string `json:"at"`
	DeskID string `json:"desk_id"`
}

type lookRead struct {
	local    int
	items    []lookItem
	others   []string // older session durables: name, created, pending
	reader   string   // "ok" or the failure
	warnings []string
}

// gateLine matches a handshake line at line start: GATE, Pre-GATE (with an
// optional version, as in "Pre-GATE v2") and WORD. The line-start anchor keeps
// a GATE quoted mid-sentence from matching.
var gateLine = regexp.MustCompile(`(?m)^\s*(?:GATE|Pre-GATE(?:\s+v\d+)?|WORD)\b.*$`)
var gateStage = regexp.MustCompile(`(?i)\b(draft|ready)\b`)

// lookClassify sorts one message into gate, request or other. A gate is a body
// with a line that starts with GATE, Pre-GATE or WORD (see gateLine), whatever
// the performative (these are a text convention, not verbs), and carries the
// stage word on that line. A request
// is urgent only once its reply_by has passed; one with no reply_by never is.
func lookClassify(perf, body, replyBy string, now time.Time) (class, stage string, urgent bool) {
	if line := gateLine.FindString(body); line != "" {
		if m := gateStage.FindString(line); m != "" {
			stage = strings.ToLower(m)
		}
		return "gate", stage, true
	}
	if perf == "REQUEST" {
		if t, err := time.Parse(time.RFC3339, replyBy); err == nil && t.Before(now) {
			return "request", "", true
		}
		return "request", "", false
	}
	return "other", "", false
}

// gateWord is the word the first gate line of a body starts with, or "".
func gateWord(body string) string {
	if f := strings.Fields(gateLine.FindString(body)); len(f) > 0 {
		return f[0]
	}
	return ""
}

func writeLookFile(dir, name string, data []byte) error {
	f, err := os.CreateTemp(dir, ".look.")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	_, werr := f.Write(data)
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(tmp)
		return werr
	}
	if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func lookAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	h, m := int(d.Hours()), int(d.Minutes())%60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// readCleared reads cleared.json. A file that does not parse never silences a
// gate: it is a warning and the map is empty.
func readCleared(dir string) (map[string]lookCleared, string) {
	data, err := os.ReadFile(filepath.Join(dir, "cleared.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ""
	}
	if err != nil {
		return nil, "cleared.json unreadable (" + err.Error() + "): every gate stays on its schedule"
	}
	m := map[string]lookCleared{}
	if json.Unmarshal(data, &m) != nil {
		return nil, "cleared.json does not parse: every gate stays on its schedule"
	}
	return m, ""
}

// lookFetch is the read-only part: the seat's current durable, its unread set.
func lookFetch(ctx context.Context, o lookOpts, now time.Time) lookRead {
	r := lookRead{}
	nc, err := nats.Connect(o.URL, nats.Timeout(5*time.Second), nats.MaxReconnects(0))
	if err != nil {
		r.reader = "cannot connect to the broker: " + err.Error()
		return r
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		r.reader = "jetstream: " + err.Error()
		return r
	}
	stream, err := js.Stream(ctx, lookStream)
	if err != nil {
		r.reader = "stream " + lookStream + ": " + err.Error()
		return r
	}
	prefix := "mcp_" + o.Seat + "_"
	type kept struct {
		name    string
		created time.Time
		info    *jetstream.ConsumerInfo
	}
	var keeps []kept
	names := stream.ConsumerNames(ctx)
	for name := range names.Name() {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		c, err := stream.Consumer(ctx, name)
		if err != nil {
			continue // gone between the listing and the read
		}
		info, err := c.Info(ctx)
		if err != nil {
			continue
		}
		keeps = append(keeps, kept{name: name, created: info.Created, info: info})
	}
	if err := names.Err(); err != nil {
		r.reader = "listing consumers: " + err.Error()
		return r
	}
	if len(keeps) == 0 {
		r.reader = "no durable for seat " + o.Seat + " on " + lookStream + ": the inbox cannot be read, which is not an empty inbox"
		return r
	}
	sort.SliceStable(keeps, func(i, j int) bool { return keeps[i].created.After(keeps[j].created) })
	cur := keeps[0]
	for _, k := range keeps[1:] {
		r.others = append(r.others, fmt.Sprintf("%s created %s, pending %d", k.name, k.created.UTC().Format(time.RFC3339), k.info.NumPending+uint64(k.info.NumAckPending)))
	}
	pending := cur.info.NumPending + uint64(cur.info.NumAckPending)
	r.local = int(pending)
	filters := cur.info.Config.FilterSubjects
	if cur.info.Config.FilterSubject != "" {
		filters = []string{cur.info.Config.FilterSubject}
	}
	seen := map[uint64]bool{}
	for _, subj := range filters {
		seq := cur.info.AckFloor.Stream + 1
		for n := uint64(0); n <= pending && n < uint64(unreadScanCap); n++ {
			m, err := stream.GetMsg(ctx, seq, jetstream.WithGetMsgSubject(subj))
			if err != nil {
				if !errors.Is(err, jetstream.ErrMsgNotFound) {
					r.reader = fmt.Sprintf("reading %s: %v", subj, err)
				}
				break
			}
			seq = m.Sequence + 1
			if seen[m.Sequence] {
				continue
			}
			seen[m.Sequence] = true
			var e Envelope
			if json.Unmarshal(m.Data, &e) != nil || e.MessageID == "" {
				continue
			}
			class, stage, urgent := lookClassify(e.Performative, e.Content.Data, e.ReplyBy, now)
			r.items = append(r.items, lookItem{
				Tier: "local", Stream: lookStream, Seq: m.Sequence, Class: class, ID: e.MessageID,
				Sender: senderLabel(&e), AgeS: int64(now.Sub(m.Time).Seconds()), Stage: stage,
				word: gateWord(e.Content.Data), perf: e.Performative, sellBy: e.ReplyBy, stored: m.Time, urgent: urgent,
			})
		}
	}
	sort.SliceStable(r.items, func(i, j int) bool { return r.items[i].Seq < r.items[j].Seq })
	if r.reader == "" {
		r.reader = "ok"
	}
	return r
}

func runLook(ctx context.Context, o lookOpts, stdout, stderr io.Writer) int {
	if o.Now == nil {
		o.Now = time.Now
	}
	now := o.Now().UTC()
	if err := os.MkdirAll(o.State, 0o700); err != nil {
		fmt.Fprintf(stderr, "look: state directory: %v\n", err)
		return 2
	}
	_ = os.Chmod(o.State, 0o700)
	cleared, warn := readCleared(o.State)
	r := lookFetch(ctx, o, now)
	if warn != "" {
		r.warnings = append(r.warnings, warn)
	}
	failed := r.reader != "ok"

	var urgent, clearedOut []lookItem
	other, requests := 0, 0
	for _, it := range r.items {
		key := fmt.Sprintf("%s/%s/%d", it.Tier, it.Stream, it.Seq)
		if c, ok := cleared[key]; ok && it.Class == "gate" {
			cc := c
			it.cleared = &cc
			clearedOut = append(clearedOut, it)
			continue
		}
		switch {
		case it.urgent:
			urgent = append(urgent, it)
		case it.Class == "request":
			requests++
		default:
			other++
		}
	}

	var d strings.Builder
	fmt.Fprintf(&d, "as of %s (last look)\n", now.Format(time.RFC3339))
	if failed {
		fmt.Fprintf(&d, "waiting: not read\n")
	} else {
		oldest := ""
		if len(r.items) > 0 {
			o := r.items[0].stored
			for _, it := range r.items {
				if it.stored.Before(o) {
					o = it.stored
				}
			}
			oldest = "; oldest " + lookAge(now.Sub(o))
		}
		fmt.Fprintf(&d, "waiting: local %d, global not read%s\n", r.local, oldest)
	}
	for _, it := range urgent {
		label := "GATE"
		if it.word != "" {
			label = it.word
		}
		extra := ""
		if it.Class == "request" {
			label, extra = "REQUEST", "  past sell-by, re-check"
		} else if it.Stage != "" {
			extra = "  stage " + it.Stage
		}
		fmt.Fprintf(&d, "%-9s %s  from %s  age %s%s\n", label, it.ID, it.Sender, lookAge(time.Duration(it.AgeS)*time.Second), extra)
	}
	for _, it := range clearedOut {
		fmt.Fprintf(&d, "cleared   %s  from %s  age %s  desk %s at %s\n", it.ID, it.Sender, lookAge(time.Duration(it.AgeS)*time.Second), it.cleared.DeskID, it.cleared.At)
	}
	if requests > 0 {
		fmt.Fprintf(&d, "request   %d (not past sell-by)\n", requests)
	}
	if other > 0 {
		fmt.Fprintf(&d, "other     %d\n", other)
	}
	for _, o := range r.others {
		fmt.Fprintf(&d, "older session: %s\n", o)
	}
	for _, w := range r.warnings {
		fmt.Fprintf(&d, "warning: %s\n", w)
	}
	fmt.Fprintf(&d, "reader: %s\n", r.reader)

	hb := map[string]any{"at": now.Format(time.RFC3339), "global": "not read", "reader": r.reader}
	if failed {
		hb["local"] = nil
	} else {
		hb["local"] = r.local
	}
	hbJSON, _ := json.MarshalIndent(hb, "", "  ")

	werr := writeLookFile(o.State, "digest.txt", []byte(d.String()))
	if werr == nil {
		werr = writeLookFile(o.State, "heartbeat.json", append(hbJSON, '\n'))
	}
	if werr == nil && !failed {
		n := map[string]any{"as_of": now.Format(time.RFC3339), "urgent": nonNilItems(urgent), "cleared": nonNilCleared(clearedOut), "reader": r.reader}
		if len(r.warnings) > 0 {
			n["warnings"] = r.warnings
		}
		nj, _ := json.MarshalIndent(n, "", "  ")
		werr = writeLookFile(o.State, "notify.json", append(nj, '\n'))
	}
	if werr != nil {
		fmt.Fprintf(stderr, "look: writing state: %v\n", werr)
		return 2
	}
	if failed {
		fmt.Fprintf(stderr, "look: %s\n", r.reader)
		return 1
	}
	fmt.Fprintf(stdout, "look: local %d, urgent %d, cleared %d\n", r.local, len(urgent), len(clearedOut))
	return 0
}

func nonNilItems(in []lookItem) []lookItem {
	if in == nil {
		return []lookItem{}
	}
	return in
}

type lookClearedItem struct {
	lookItem
	DeskID    string `json:"desk_id"`
	ClearedAt string `json:"cleared_at"`
}

func nonNilCleared(in []lookItem) []lookClearedItem {
	out := []lookClearedItem{}
	for _, it := range in {
		out = append(out, lookClearedItem{lookItem: it, DeskID: it.cleared.DeskID, ClearedAt: it.cleared.At})
	}
	return out
}

// runLookMain resolves the look's defaults: the state directory is
// ~/.director/look, the seat is DIRECTOR_AGENT_ID, else MARVEL_SESSION.
func runLookMain(cli cliArgs) int {
	state := cli.lookState
	if state == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "look: no --state and no home directory: %v\n", err)
			return 2
		}
		state = filepath.Join(home, ".director", "look")
	}
	seat := cli.lookSeat
	if seat == "" {
		seat = os.Getenv("DIRECTOR_AGENT_ID")
	}
	if seat == "" {
		seat = os.Getenv("MARVEL_SESSION")
	}
	if seat == "" {
		fmt.Fprintf(os.Stderr, "look: no --seat, no DIRECTOR_AGENT_ID and no MARVEL_SESSION (usage: %s)\n", lookUsage)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return runLook(ctx, lookOpts{URL: env("NATS_URL", "nats://127.0.0.1:4222"), State: state, Seat: seat}, os.Stdout, os.Stderr)
}
