package main

// The held store (sim/design/presence-watch.md, section 4, build item 5):
// held.jsonl, the director seat's own record of sends that presence refused and
// the shim kept for release. Append-only, mode 0600, written under a lock with
// an fsync as handled.jsonl is (handled.go). The shim is its only writer.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

// heldRec is one line of held.jsonl. The first line for an id carries the
// envelope, the address, when it was held, why and its sell-by; a later line
// carries the new state and what that change adds, and leaves the rest out. The
// current record is the fold of its lines (Current).
type heldRec struct {
	ID           string    `json:"id"`
	To           string    `json:"to,omitempty"`
	Envelope     *Envelope `json:"envelope,omitempty"`
	HeldAt       time.Time `json:"held_at,omitempty"`
	Reason       string    `json:"reason,omitempty"`
	SellBy       time.Time `json:"sell_by,omitempty"`
	State        string    `json:"state"`
	StateAt      time.Time `json:"state_at"`
	Stream       string    `json:"stream,omitempty"`
	Seq          uint64    `json:"seq,omitempty"`
	SupersededBy string    `json:"superseded_by,omitempty"`
}

type heldStore struct {
	path, lock string
	// syncFile is fsync; a field so a test can fail it.
	syncFile func(*os.File) error
	// appendHook, when set, runs before every append and can fail it; a test
	// uses it to stop the append that follows a release, as a crash would.
	appendHook func(heldRec) error
}

func newHeldStore(dir string) *heldStore {
	return &heldStore{
		path:     filepath.Join(dir, "held.jsonl"),
		lock:     filepath.Join(dir, "held.lock"),
		syncFile: func(f *os.File) error { return f.Sync() },
	}
}

// Append adds one line under the lock and fsyncs before it returns. A torn last
// line from a crash is left as it is and the new line starts on its own.
func (s *heldStore) Append(rec heldRec) error {
	if s.appendHook != nil {
		if err := s.appendHook(rec); err != nil {
			return err
		}
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	unlock, err := (&handledLedger{lock: s.lock}).lockLedger()
	if err != nil {
		return err
	}
	defer unlock()
	if b, err := os.ReadFile(s.path); err == nil && len(b) > 0 && b[len(b)-1] != '\n' {
		line = append([]byte{'\n'}, line...)
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("held store: %w", err)
	}
	if _, err := f.Write(line); err != nil {
		_ = f.Close()
		return fmt.Errorf("held store: %w", err)
	}
	if err := s.syncFile(f); err != nil {
		_ = f.Close()
		return fmt.Errorf("held store fsync: %w", err)
	}
	return f.Close()
}

// Current folds the file into one record per id, oldest held_at first. A line
// that is not whole JSON is skipped. It never writes.
func (s *heldStore) Current() ([]heldRec, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	byID := map[string]*heldRec{}
	var order []string
	for _, line := range bytes.Split(b, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var r heldRec
		if json.Unmarshal(line, &r) != nil || r.ID == "" {
			continue
		}
		cur := byID[r.ID]
		if cur == nil {
			c := r
			byID[r.ID] = &c
			order = append(order, r.ID)
			continue
		}
		cur.State, cur.StateAt = r.State, r.StateAt
		if r.Stream != "" {
			cur.Stream, cur.Seq = r.Stream, r.Seq
		}
		if r.SupersededBy != "" {
			cur.SupersededBy = r.SupersededBy
		}
		if r.Reason != "" && r.State == holdFailed {
			cur.Reason = r.Reason
		}
	}
	out := make([]heldRec, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].HeldAt.Before(out[j].HeldAt) })
	return out, nil
}

// find returns the current record for id.
func (s *heldStore) find(id string) (*heldRec, error) {
	recs, err := s.Current()
	if err != nil {
		return nil, err
	}
	for i := range recs {
		if recs[i].ID == id {
			return &recs[i], nil
		}
	}
	return nil, nil
}

// heldErr is what a held send returns in place of a refusal: the send was not
// delivered, and the shim kept it.
type heldErr struct{ rec heldRec }

func (e *heldErr) Error() string {
	return fmt.Sprintf("held, not delivered: %s (hold id %s; released when its recipient returns and beats, or proposed for your review after %s)", e.rec.Reason, e.rec.ID, e.rec.SellBy.Format(time.RFC3339))
}

// holdFailFastKey marks a send with fail_fast: true, which keeps today's refusal.
type holdFailFastKey struct{}

// presenceRefusalErr is a refusal because no live presence was established for
// the recipient (the four branches of noGlobalPresenceErr, Unreadable included).
// presenceReadErr is a refusal because the hub did not answer the presence read.
// These two, and only these, are held; every other failure is not.
type presenceRefusalErr struct {
	err error
	// unreadable: rows matched and none could be read, so absence was never
	// established and no claim of absence is added to the text.
	unreadable bool
}

func (e *presenceRefusalErr) Error() string { return e.err.Error() }
func (e *presenceRefusalErr) Unwrap() error { return e.err }

type presenceReadErr struct{ err error }

func (e *presenceReadErr) Error() string { return e.err.Error() }
func (e *presenceReadErr) Unwrap() error { return e.err }
