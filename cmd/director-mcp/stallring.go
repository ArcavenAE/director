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

// stallRingMinGap is the most often a seat is rung: once in any hour, counting
// rings by hand as well.
const stallRingMinGap = time.Hour

// errRingClaimHeld is what the inject reports when marvel refuses the ring
// because another ring holds the seat's claim.
var errRingClaimHeld = errors.New("ring claim held by another ring")

// errRingUnconfirmed is what the inject reports when --verify could not see the
// line staged in the composer. The adapter stops before Enter.
var errRingUnconfirmed = errors.New("ring line not confirmed in the composer")

// Composer verdicts, as composer-check prints them. Only EMPTY lets a ring go.
const (
	composerEmpty   = "EMPTY"
	// composerSuggestion is an idle composer holding only a dim suggestion: typed
	// text replaces it, so composer-check calls it safe (exit 0).
	composerSuggestion = "SUGGESTION"
	composerHasText = "HAS-TEXT"
	composerQueued  = "QUEUED"
	composerMenu    = "MENU"
	composerUnclear = "UNCLEAR"
)

// stallSeat is one presence row, as far as the ring reads it.
type stallSeat struct {
	AgentID  string
	Instance string
	// Key is the marvel session key the inject addresses.
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

	mu    sync.Mutex
	local map[string]time.Time // rings this process sent, in case the log write after one fails
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

// Tick rings every seat that is stalled, due and has an empty composer, and
// returns the outcome rows it wrote. A seat is skipped, and nothing is typed,
// whenever anything it needs cannot be read or written.
func (r *stallRinger) Tick(ctx context.Context) []ringRow {
	seats, err := r.List(ctx)
	if err != nil {
		return nil
	}
	var out []ringRow
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
		if err != nil || (!last.IsZero() && now.Sub(last) < r.MinGap) {
			continue
		}
		row := ringRow{TS: now, Actor: "director-mcp", Seat: cur.AgentID, IdleMinutes: int(cur.OldestS / 60), Line: stallRingLine}
		verdict, err := r.Composer(ctx, cur)
		if err != nil || verdict != composerEmpty {
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
		}
		if row.Event == "ring" {
			r.noteLocal(cur.AgentID, now)
		}
		_ = r.Log.Append(row)
		out = append(out, row)
	}
	return out
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
			return fmt.Errorf("marvel inject --enter: %w: %s", err, strings.TrimSpace(out))
		}
		return nil
	}
}
