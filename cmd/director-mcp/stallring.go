package main

// The stall ring core (aae-orc director-stall-ring). A seat whose cue reads
// "stalled" (mail waiting, no wait open, oldest pending at least the pending
// age bound) is rung once through marvel inject --ring, and rung no more than
// once an hour. It is a reminder, inside SOUL section 8: it types one fixed
// line and Enter, and nothing else.
//
// The core opens no connection and starts no process. The caller supplies the
// seat list, the composer check, the inject and the ring log as functions, and
// the clock, so every case runs without a broker or a pane.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// stallRingLine is the only text the ring types. It carries nothing from any
// message: no sender, no id, no body.
const stallRingLine = "director inbox: new mail waiting, read it with wait_for_message"

// stallRingErrorBackoff is how long a seat is left alone after a ring that
// ended in an error, so a marvel outage does not write a row per tick.
const stallRingErrorBackoff = 15 * time.Minute

// stallRingMinGap is the most often a seat is rung: once in any hour, counting
// rings by hand as well.
const stallRingMinGap = time.Hour

// errRingClaimHeld is what the inject reports when marvel refuses the ring
// because another ring holds the seat's claim.
var errRingClaimHeld = errors.New("ring claim held by another ring")

// errRingUnconfirmed is what the inject reports when --verify could not see the
// line staged in the composer. The adapter stops before Enter.
var errRingUnconfirmed = errors.New("ring line not confirmed in the composer")

// Composer verdicts, as composer-check prints them. Only EMPTY and SUGGESTION let a ring go.
const (
	composerEmpty = "EMPTY"
	// composerSuggestion is an idle composer holding only a dim suggestion: typed
	// text replaces it, so composer-check calls it safe (exit 0).
	composerSuggestion = "SUGGESTION"
	composerHasText    = "HAS-TEXT"
	composerQueued     = "QUEUED"
	composerMenu       = "MENU"
	composerUnclear    = "UNCLEAR"
)

// stallSeat is one presence row, as far as the ring reads it.
type stallSeat struct {
	AgentID  string
	Team     string
	Instance string
	// Key is the director's guess at the marvel session key; the ring ignores it and resolves
	// the key from marvel's own session list.
	Key     string
	Cue     string
	OldestS int64
}

// ringRow is one line of the ring log (rings.jsonl). Result is "sent" for a
// ring that went in; a refusal or failure is its own event so it never counts
// toward the hour.
type ringRow struct {
	TS          time.Time
	Actor       string
	Event       string
	Seat        string
	IdleMinutes int
	Line        string
	Result      string
}

// ringLog is the ring log: the last ring of a seat by anyone, and an append.
type ringLog interface {
	// LastRing is the time of the latest counted ring of the seat, by any actor,
	// or the zero time when there is none.
	LastRing(agentID string) (time.Time, error)
	Append(row ringRow) error
}

type stallRinger struct {
	Now      func() time.Time
	Bound    time.Duration
	MinGap   time.Duration
	List     func(ctx context.Context) ([]stallSeat, error)
	Fresh    func(ctx context.Context, s stallSeat) (stallSeat, error)
	Composer func(ctx context.Context, s stallSeat) (string, error)
	Inject   func(ctx context.Context, key, text string, keys ...string) error
	Log      ringLog
	// Sessions lists marvel's sessions, read once per pass, to turn a seat's agent
	// id into the marvel session key (workspace/name).
	Sessions func(ctx context.Context) ([]marvelSession, error)
	// Backoff is how long a seat is left alone after an error result.
	Backoff time.Duration

	mu       sync.Mutex
	local    map[string]time.Time // rings this process sent, in case the log write after one fails
	errUntil map[string]time.Time // seats left alone after an error result, until then
}

// stalledEnough says whether a row still reads as a stalled seat at act time.
func (r *stallRinger) stalledEnough(s stallSeat) bool {
	return s.Cue == cueStateStalled && time.Duration(s.OldestS)*time.Second >= r.Bound
}

func (r *stallRinger) lastRing(id string) (time.Time, error) {
	last, err := r.Log.LastRing(id)
	if err != nil {
		return time.Time{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if t := r.local[id]; t.After(last) {
		last = t
	}
	return last, nil
}

func (r *stallRinger) noteLocal(id string, t time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.local == nil {
		r.local = map[string]time.Time{}
	}
	r.local[id] = t
}

// resolveSession turns an agent id into marvel's session key. The name must
// match exactly; the workspace is marvel's, not the director's.
func resolveSession(list []marvelSession, agentID string) (key, refusal string) {
	var found []marvelSession
	for _, m := range list {
		if m.Name == agentID {
			found = append(found, m)
		}
	}
	switch len(found) {
	case 0:
		return "", "refused-unknown-session"
	case 1:
		return found[0].Workspace + "/" + found[0].Name, ""
	}
	return "", "refused-ambiguous-session"
}

// Tick rings every seat that is stalled, due and has a composer that is empty
// or holds only a dim suggestion, and returns the outcome rows it wrote. A seat
// is skipped, and nothing is typed, whenever anything it needs cannot be read or
// written. The session list is read once, when the first seat needs it.
func (r *stallRinger) Tick(ctx context.Context) []ringRow {
	seats, err := r.List(ctx)
	if err != nil {
		return nil
	}
	var out []ringRow
	var sessions []marvelSession
	sessionsRead := false
	for _, s := range seats {
		if !r.stalledEnough(s) {
			continue
		}
		cur, err := r.Fresh(ctx, s)
		if err != nil || !r.stalledEnough(cur) {
			continue
		}
		now := r.Now()
		last, err := r.lastRing(cur.AgentID)
		if err != nil || (!last.IsZero() && now.Sub(last) < r.MinGap) || r.backedOff(cur.AgentID, now) {
			continue
		}
		row := ringRow{TS: now, Actor: "director-mcp", Seat: cur.AgentID, IdleMinutes: int(cur.OldestS / 60), Line: stallRingLine}
		if !sessionsRead {
			sessionsRead = true
			if sessions, err = r.Sessions(ctx); err != nil {
				sessions = nil
				row.Seat, row.Event, row.Result, row.IdleMinutes = "*", "ring-refused", "refused-sessions", 0
				_ = r.Log.Append(row)
				return append(out, row)
			}
		} else if sessions == nil {
			return out
		}
		key, refusal := resolveSession(sessions, cur.AgentID)
		if refusal != "" {
			row.Event, row.Result = "ring-refused", refusal
			_ = r.Log.Append(row)
			out = append(out, row)
			continue
		}
		cur.Key = key
		verdict, err := r.Composer(ctx, cur)
		if err != nil || (verdict != composerEmpty && verdict != composerSuggestion) {
			row.Event, row.Result = "ring-refused", "refused-composer"
			_ = r.Log.Append(row)
			out = append(out, row)
			continue
		}
		attempt := row
		attempt.Event = "ring-attempt"
		if err := r.Log.Append(attempt); err != nil {
			continue
		}
		switch err := r.Inject(ctx, cur.Key, stallRingLine, "Enter"); {
		case err == nil:
			row.Event, row.Result = "ring", "sent"
		case errors.Is(err, errRingUnconfirmed):
			row.Event, row.Result = "ring", "unverified"
		case errors.Is(err, errRingClaimHeld):
			row.Event, row.Result = "ring-refused", "refused-claim"
		default:
			row.Event, row.Result = "ring-refused", "error"
			r.noteBackoff(cur.AgentID, now)
		}
		if row.Event == "ring" {
			r.noteLocal(cur.AgentID, now)
		}
		_ = r.Log.Append(row)
		out = append(out, row)
	}
	return out
}

// backedOff says whether a seat is being left alone after an error result.
func (r *stallRinger) backedOff(id string, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return now.Before(r.errUntil[id])
}

func (r *stallRinger) noteBackoff(id string, now time.Time) {
	if r.Backoff <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.errUntil == nil {
		r.errUntil = map[string]time.Time{}
	}
	r.errUntil[id] = now.Add(r.Backoff)
}

// stallRingEnabled is true only on the director seat with DIRECTOR_STALL_RING=1.
func stallRingEnabled(getenv func(string) string, role string) bool {
	return role == roleDirector && getenv("DIRECTOR_STALL_RING") == "1"
}

// fileRingLog is the ring log on disk: rings.jsonl under rings.lock, the pair
// the hand path (dws ring) writes.
type fileRingLog struct{ dir string }

func openRingLog(dir string) *fileRingLog { return &fileRingLog{dir: dir} }

type ringLine struct {
	TS          string `json:"ts"`
	Actor       string `json:"actor"`
	Event       string `json:"event"`
	Seat        string `json:"seat"`
	IdleMinutes int    `json:"idle_minutes,omitempty"`
	Line        string `json:"line,omitempty"`
	Result      string `json:"result,omitempty"`
}

// LastRing reads the whole file and skips any line that does not parse, a torn
// last line included. Only event ring rows count, and only with no result (the
// hand path), sent or unverified.
func (l *fileRingLog) LastRing(agentID string) (time.Time, error) {
	f, err := os.Open(filepath.Join(l.dir, "rings.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	defer f.Close()
	var last time.Time
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var e ringLine
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Event != "ring" {
			continue
		}
		if e.Result != "" && e.Result != "sent" && e.Result != "unverified" {
			continue
		}
		if !seatNamed(e.Seat, agentID) {
			continue
		}
		t, err := time.Parse(time.RFC3339, e.TS)
		if err != nil {
			continue
		}
		if t.After(last) {
			last = t
		}
	}
	return last, sc.Err()
}

// Append writes one line under an exclusive lock on rings.lock.
func (l *fileRingLog) Append(row ringRow) error {
	if err := os.MkdirAll(l.dir, 0o700); err != nil {
		return err
	}
	lk, err := os.OpenFile(filepath.Join(l.dir, "rings.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lk.Close()
	if err := syscall.Flock(int(lk.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(lk.Fd()), syscall.LOCK_UN) }()
	b, err := json.Marshal(ringLine{TS: row.TS.UTC().Format(time.RFC3339), Actor: row.Actor, Event: row.Event,
		Seat: row.Seat, IdleMinutes: row.IdleMinutes, Line: row.Line, Result: row.Result})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(l.dir, "rings.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	// A torn last line (no newline) must not swallow this row: start a fresh line.
	if st, err := f.Stat(); err == nil && st.Size() > 0 {
		last := make([]byte, 1)
		if r, err := os.Open(filepath.Join(l.dir, "rings.jsonl")); err == nil {
			_, rerr := r.ReadAt(last, st.Size()-1)
			_ = r.Close()
			if rerr == nil && last[0] != '\n' {
				b = append([]byte{'\n'}, b...)
			}
		}
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

func isSeatIDChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.'
}

// seatNamed says whether a ring row's free-text seat field names the agent id:
// the id as a whole token, so "...-g35-10" does not name "...-g35-1".
func seatNamed(text, id string) bool {
	if id == "" {
		return false
	}
	for from := 0; ; {
		i := strings.Index(text[from:], id)
		if i < 0 {
			return false
		}
		i += from
		end := i + len(id)
		if (i == 0 || !isSeatIDChar(text[i-1])) && (end == len(text) || !isSeatIDChar(text[end])) {
			return true
		}
		from = i + 1
	}
}

// marvelExec runs one command and returns its combined output.
type marvelExec func(ctx context.Context, argv []string) (string, error)

// newMarvelInject is the production inject: it types the line with
// `marvel inject <key> <line> --ring --verify`, then submits it with
// `marvel inject <key> (empty text) --enter`. Any other key is refused before
// anything runs. The refusal texts are marvel's own (internal/api/ring_claim.go
// and cmd/marvel/inject.go at marvel b379ca65).
func newMarvelInject(run marvelExec) func(ctx context.Context, key, text string, keys ...string) error {
	return func(ctx context.Context, key, text string, keys ...string) error {
		if len(keys) != 1 || keys[0] != "Enter" {
			return fmt.Errorf("stall ring: the only key it presses is Enter, got %q", keys)
		}
		out, err := run(ctx, []string{"marvel", "inject", key, text, "--ring", "--verify"})
		if err != nil {
			switch {
			case strings.Contains(out, "ring refused:"):
				return fmt.Errorf("%w: %s", errRingClaimHeld, strings.TrimSpace(out))
			case strings.Contains(out, "the effect was not confirmed") || strings.Contains(out, "verify: unconfirmed"):
				return fmt.Errorf("%w: %s", errRingUnconfirmed, strings.TrimSpace(out))
			}
			return fmt.Errorf("marvel inject: %w: %s", err, strings.TrimSpace(out))
		}
		if out, err := run(ctx, []string{"marvel", "inject", key, "", "--enter"}); err != nil {
			// The line is staged and confirmed but not known to be sent.
			return fmt.Errorf("%w: marvel inject --enter: %v: %s", errRingUnconfirmed, err, strings.TrimSpace(out))
		}
		return nil
	}
}

// stallRowKey is the presence key of a seat's row: its own team, not the
// director's.
func stallRowKey(s stallSeat) string {
	return "presence." + s.Team + "." + s.AgentID + "." + s.Instance
}

// marvelSession is the part of one `marvel get sessions -o json` entry the ring reads.
type marvelSession struct {
	Name      string `json:"Name"`
	Workspace string `json:"Workspace"`
}

// marvelSessions reads marvel's session list through run. Plain `get sessions
// -o json` prints a list with a "Name" key (the object form appears only with
// --cluster or --all-clusters), so anything that is not a list is an error.
func marvelSessions(run marvelExec) func(ctx context.Context) ([]marvelSession, error) {
	return func(ctx context.Context) ([]marvelSession, error) {
		out, err := run(ctx, []string{"marvel", "get", "sessions", "-o", "json"})
		if err != nil {
			return nil, fmt.Errorf("marvel get sessions: %w", err)
		}
		var list []marvelSession
		if err := json.Unmarshal([]byte(out), &list); err != nil {
			return nil, fmt.Errorf("marvel get sessions: output is not a session list: %w", err)
		}
		return list, nil
	}
}
