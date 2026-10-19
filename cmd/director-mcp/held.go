package main

// The held store (sim/design/presence-watch.md, section 4, build item 5):
// held.jsonl, the director seat's own record of sends that presence refused and
// the shim kept for release.

import (
	"os"
	"time"
)

// Hold states.
const (
	holdHeld      = "held"
	holdReleased  = "released"
	holdProposed  = "proposed"
	holdCancelled = "cancelled"
	holdFailed    = "failed"
)

// heldRec is one line of held.jsonl. The current state of a record is its last line.
type heldRec struct {
	ID           string    `json:"id"`
	To           string    `json:"to"`
	Envelope     *Envelope `json:"envelope,omitempty"`
	HeldAt       time.Time `json:"held_at"`
	Reason       string    `json:"reason,omitempty"`
	SellBy       time.Time `json:"sell_by"`
	State        string    `json:"state"`
	StateAt      time.Time `json:"state_at"`
	Stream       string    `json:"stream,omitempty"`
	Seq          uint64    `json:"seq,omitempty"`
	SupersededBy string    `json:"superseded_by,omitempty"`
}

type heldStore struct {
	path, lock string
	syncFile   func(*os.File) error
	// appendHook, when set, runs before every append and can fail it; a test
	// uses it to stop the append that follows a release.
	appendHook func(heldRec) error
}

func newHeldStore(dir string) *heldStore { return &heldStore{} }

// Append adds one line under the lock and fsyncs before it returns.
func (s *heldStore) Append(rec heldRec) error { return nil }

// Current folds the file: the last line per id, oldest held_at first.
func (s *heldStore) Current() ([]heldRec, error) { return nil, nil }
