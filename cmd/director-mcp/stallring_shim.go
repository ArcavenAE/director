package main

// The stall ring inside the shim (aae-orc director-stall-ring). It runs on the
// director seat's heartbeat tick, behind DIRECTOR_STALL_RING=1, and only on a
// shim whose global role is director, the way the presence watch does
// (presencewatch_shim.go). Off, nothing here runs and nothing is written.
//
// The core (stallring.go) decides; this file feeds it: the seats from the
// local presence bucket, the composer verdict from `marvel capture` piped to
// composer-check, and the inject from `marvel inject`. A pass runs on its own
// goroutine so a slow marvel call never holds the heartbeat past the presence
// TTL, and runs at most once a minute.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// stallRingEvery is how often a pass looks at the roster. The bounds that limit
// rings (10 minutes stalled, one ring an hour) live in the core.
const stallRingEvery = time.Minute

// stallRingCallTimeout bounds one marvel or composer-check call.
const stallRingCallTimeout = 20 * time.Second

type stallRingRunner struct {
	ringer  *stallRinger
	logf    func(string, ...any)
	last    atomic.Int64 // unix seconds of the last pass start
	running atomic.Bool
}

// stallSeatFromRow reads one presence record as a candidate seat. A row that is
// not fresh, has no cue state, is this session's own, or names no workspace is
// never a candidate: the ring needs the marvel session key (workspace/agent id).
func stallSeatFromRow(key string, rec map[string]any, now time.Time, selfInstance string) (stallSeat, bool) {
	row, _ := rosterRowFromKey(key, rec, now)
	if !row.Fresh || row.Cue == "" || row.Workspace == "" || row.AgentID == "" || row.Instance == selfInstance {
		return stallSeat{}, false
	}
	oldest, _ := rec["oldest_pending_s"].(float64)
	return stallSeat{AgentID: row.AgentID, Instance: row.Instance, Key: row.Workspace + "/" + row.AgentID,
		Cue: row.Cue, OldestS: int64(oldest)}, true
}

func runCombined(ctx context.Context, argv []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, stallRingCallTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
	return string(out), err
}

// composerVerdict captures the seat's pane with its escapes and asks
// composer-check. Anything but its one-word verdict is an error.
func composerVerdict(ctx context.Context, key string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, stallRingCallTimeout)
	defer cancel()
	capture, err := exec.CommandContext(ctx, "marvel", "capture", key, "-e").Output()
	if err != nil {
		return "", fmt.Errorf("marvel capture %s: %w", key, err)
	}
	check := exec.CommandContext(ctx, filepath.Join(directorHome(), "bin", "composer-check"), "--file", "/dev/stdin")
	check.Stdin = bytes.NewReader(capture)
	var out bytes.Buffer
	check.Stdout = &out
	// composer-check exits 1 for HAS-TEXT and 2 for MENU etc. with the verdict on stdout.
	_ = check.Run()
	v := strings.TrimSpace(out.String())
	if v == "" {
		return "", fmt.Errorf("composer-check printed no verdict for %s", key)
	}
	return v, nil
}

// startStallRing turns the ring on when DIRECTOR_STALL_RING is 1 and the
// session holds the global director role. Otherwise it does nothing.
func (b *Bus) startStallRing(logf func(string, ...any)) error {
	role := ""
	if b.globalCfg != nil {
		role = b.globalCfg.Role
	}
	if !stallRingEnabled(os.Getenv, role) {
		return nil
	}
	dir, err := stateDir()
	if err != nil {
		return err
	}
	readRow := func(ctx context.Context, key string) (stallSeat, bool) {
		e, err := b.kv.Get(ctx, key)
		if err != nil {
			return stallSeat{}, false
		}
		var rec map[string]any
		if json.Unmarshal(e.Value(), &rec) != nil {
			return stallSeat{}, false
		}
		return stallSeatFromRow(key, rec, time.Now(), b.instance)
	}
	r := &stallRinger{
		Now:    time.Now,
		Bound:  defaultPendingAgeBound(),
		MinGap: stallRingMinGap,
		List: func(ctx context.Context) ([]stallSeat, error) {
			keys, err := quietKeys(ctx, b.js, "AGENT_STATE", b.kv)
			if err != nil {
				return nil, err
			}
			var seats []stallSeat
			for _, k := range keys {
				if strings.HasPrefix(k, "presence.") && strings.Count(k, ".") == 3 {
					if s, ok := readRow(ctx, k); ok {
						seats = append(seats, s)
					}
				}
			}
			return seats, nil
		},
		Fresh: func(ctx context.Context, s stallSeat) (stallSeat, error) {
			cur, ok := readRow(ctx, "presence."+b.self.Team+"."+s.AgentID+"."+s.Instance)
			if !ok {
				return stallSeat{}, fmt.Errorf("presence row for %s is gone or stale", s.AgentID)
			}
			return cur, nil
		},
		Composer: func(ctx context.Context, s stallSeat) (string, error) { return composerVerdict(ctx, s.Key) },
		Inject:   newMarvelInject(runCombined),
		Log:      openRingLog(dir),
	}
	b.stall = &stallRingRunner{ringer: r, logf: logf}
	return nil
}

// stallTick is the heartbeat's one call into the ring.
func (b *Bus) stallTick(ctx context.Context) {
	s := b.stall
	if s == nil || !s.running.CompareAndSwap(false, true) {
		return
	}
	now := time.Now().Unix()
	if now-s.last.Load() < int64(stallRingEvery/time.Second) {
		s.running.Store(false)
		return
	}
	s.last.Store(now)
	go func() {
		defer s.running.Store(false)
		for _, row := range s.ringer.Tick(ctx) {
			if s.logf != nil {
				s.logf("stall ring: %s %s %s", row.Seat, row.Event, row.Result)
			}
		}
	}()
}
