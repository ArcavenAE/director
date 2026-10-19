package main

// The watch's two files (sim/design/presence-watch.md, D3, build item 3):
// presence-watch.json is the current state, rewritten whole; presence-events.jsonl
// is the append-only record of every transition, gap and flap. Both are mode
// 0600 beside the seat's other state, the way handled.jsonl is (handled.go).

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

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

// Save rewrites the state file atomically: the bytes go to a temp file in the
// same directory, are fsynced, and a rename makes them the file. A failure
// leaves the old state file as it was.
func (s *watchStore) Save(w *PresenceWatch) error {
	b, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, watchStateFile+".tmp*") // mode 0600
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.Write(append(b, '\n'))
	serr := s.syncFile(tmp)
	cerr := tmp.Close()
	if werr != nil || serr != nil || cerr != nil {
		_ = os.Remove(name)
		return errors.Join(werr, serr, cerr)
	}
	if err := os.Rename(name, filepath.Join(s.dir, watchStateFile)); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// Load reads the state file. A missing file is a fresh watch, not an error. A
// file that does not parse is an error, and is left in place for a person to read.
func (s *watchStore) Load(th watchThresholds) (*PresenceWatch, error) {
	b, err := os.ReadFile(filepath.Join(s.dir, watchStateFile))
	if errors.Is(err, os.ErrNotExist) {
		return newPresenceWatch(th), nil
	}
	if err != nil {
		return nil, err
	}
	w := newPresenceWatch(th)
	if err := json.Unmarshal(b, w); err != nil {
		return nil, fmt.Errorf("presence watch state %s: %w", watchStateFile, err)
	}
	if w.Units == nil {
		w.Units = map[string]*watchUnit{}
	}
	return w, nil
}

// Append adds events to the event file and fsyncs before it returns. The shim is
// the only writer. A torn last line from a crash is left as it is, and the new
// events start on their own line.
func (s *watchStore) Append(evs []watchEvent) error {
	if len(evs) == 0 {
		return nil
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(s.dir, watchEventsFile)
	var buf []byte
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 && b[len(b)-1] != '\n' {
		buf = append(buf, '\n')
	}
	for _, e := range evs {
		line, err := json.Marshal(e)
		if err != nil {
			return err
		}
		buf = append(append(buf, line...), '\n')
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("presence events: %w", err)
	}
	if _, err := f.Write(buf); err != nil {
		_ = f.Close()
		return fmt.Errorf("presence events: %w", err)
	}
	if err := s.syncFile(f); err != nil {
		_ = f.Close()
		return fmt.Errorf("presence events fsync: %w", err)
	}
	return f.Close()
}

// Events reads the event file, skipping a line that is not whole JSON.
func (s *watchStore) Events() ([]watchEvent, error) {
	b, err := os.ReadFile(filepath.Join(s.dir, watchEventsFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []watchEvent
	for _, line := range bytes.Split(b, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e watchEvent
		if json.Unmarshal(line, &e) == nil {
			out = append(out, e)
		}
	}
	return out, nil
}
