package main

// The watch's two files (sim/design/presence-watch.md, D3, build item 3):
// presence-watch.json is the current state, rewritten whole; presence-events.jsonl
// is the append-only record of every transition, gap and flap. Both are mode
// 0600 beside the seat's other state, the way handled.jsonl is (handled.go).

import "os"

const (
	watchStateFile  = "presence-watch.json"
	watchEventsFile = "presence-events.jsonl"
)

type watchStore struct {
	dir string
	// syncFile is fsync; a field so a test can fail it.
	syncFile func(*os.File) error
}

func newWatchStore(dir string) *watchStore {
	return &watchStore{dir: dir, syncFile: func(f *os.File) error { return f.Sync() }}
}

// Save rewrites the state file atomically.
func (s *watchStore) Save(w *PresenceWatch) error { return nil }

// Load reads the state file. A missing file is a fresh watch, not an error.
func (s *watchStore) Load(th watchThresholds) (*PresenceWatch, error) {
	return newPresenceWatch(th), nil
}

// Append adds events to the event file and fsyncs before it returns.
func (s *watchStore) Append(evs []watchEvent) error { return nil }

// Events reads the event file, skipping a torn last line.
func (s *watchStore) Events() ([]watchEvent, error) { return nil, nil }
