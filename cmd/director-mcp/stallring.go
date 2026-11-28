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
	"context"
	"errors"
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

// Composer verdicts, as composer-check prints them. Only EMPTY lets a ring go.
const (
	composerEmpty   = "EMPTY"
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
}

// Tick rings every seat that is stalled, due and has an empty composer, and
// returns the outcome rows it wrote.
func (r *stallRinger) Tick(ctx context.Context) []ringRow {
	return nil
}

// stallRingEnabled is true only on the director seat with DIRECTOR_STALL_RING=1.
func stallRingEnabled(getenv func(string) string, role string) bool {
	return false
}

// fileRingLog is the ring log on disk, rings.jsonl under rings.lock.
type fileRingLog struct{ dir string }

func openRingLog(dir string) *fileRingLog { return &fileRingLog{dir: dir} }

func (l *fileRingLog) LastRing(agentID string) (time.Time, error) {
	return time.Time{}, errors.New("not implemented")
}

func (l *fileRingLog) Append(row ringRow) error { return errors.New("not implemented") }
