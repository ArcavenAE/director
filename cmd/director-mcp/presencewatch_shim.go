package main

// The presence watch inside the shim (sim/design/presence-watch.md, build item
// 4, D2 option (a), operator Desk ruling director-319-d2 "2 option 1 for now").
// It runs on the director seat's heartbeat tick, behind DIRECTOR_PRESENCE_WATCH=1,
// and only on a shim whose global role is director.
//
// One seam. presenceWatcher is all the shim knows of the watch: main starts it
// (startPresenceWatch), the heartbeat calls watchTick, and the tools drain
// presenceNotices. What feeds it is one function, scan, and what it keeps is a
// PresenceWatch and a watchStore. Moving the watch out of the shim means a new
// feeder for tick and a new drain for notices; the core and the files do not
// change.
//
// Where the heartbeat cadence bounds the watch. "For now" runs the watch on the
// shim's 30 second tick (main.go), and each of these follows from that:
//
//  1. Resolution. A pass is one tick, so absence is two passes, about 60 to 90
//     seconds after the last beat, and an absence or return shorter than that
//     is never seen (a unit that flaps faster than two ticks reads as present).
//  2. Existence. The watch looks only while a director session runs. A director
//     that is down records nothing, so the next start records a "watch-down"
//     gap and reports each unit as present or absent "after the gap", never as a
//     return (design D3). Absence history while no director runs is what a
//     separate loop (D2 option b) would add.
//  3. Lateness. A tick that runs more than 90 seconds after the last is itself a
//     gap (passLate in presencewatch.go), so a tick starved by a slow hub or a
//     busy host reads as the watcher's downtime, not as every cluster leaving.
//  4. Blocking. The pass runs on the heartbeat goroutine after the presence
//     writes, so a slow bucket read would delay the next renewal. The read is
//     bounded by scanTimeout, well under the 90 second presence TTL.
//  5. Clock. The monotonic reading is taken from the process start on each tick,
//     which is what separates a host sleep from lateness. It is only as good as
//     the tick: a sleep shorter than the 30 second slack is not seen.
//  6. Release. Held mail (item 5) goes out on a tick after the recipient is back
//     and beating, so a release is up to one tick late.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// scanTimeout bounds one bucket read, so a slow hub cannot hold the heartbeat
// goroutine past the next renewal (cadence bound 4).
const scanTimeout = 10 * time.Second

const (
	thresholdPresenceFlapWindow = "presence_flap_window"
	thresholdPresenceFlapQuiet  = "presence_flap_quiet"
)

// presenceWatcher is the watch as the shim holds it.
type presenceWatcher struct {
	mu    sync.Mutex
	w     *PresenceWatch
	store *watchStore
	scan  func(ctx context.Context) watchScan
	logf  func(string, ...any)
	now   func() time.Time
	mono  func() time.Duration

	pending []string // notices not yet reported
}

func newPresenceWatcher(store *watchStore, w *PresenceWatch, scan func(context.Context) watchScan, logf func(string, ...any)) *presenceWatcher {
	start := time.Now()
	return &presenceWatcher{
		w: w, store: store, scan: scan, logf: logf,
		now:  time.Now,
		mono: func() time.Duration { return time.Since(start) },
	}
}

// tick runs one pass: read the bucket, observe, write the files, queue notices.
// A failed file write is logged, never fatal: the watch is a diagnostic.
func (p *presenceWatcher) tick(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	sctx, cancel := context.WithTimeout(ctx, scanTimeout)
	sc := p.scan(sctx)
	cancel()
	evs := p.w.Observe(p.now(), p.mono(), sc)
	if err := p.store.Save(p.w); err != nil {
		p.log("presence watch: state file not written: %v", err)
	}
	if err := p.store.Append(evs); err != nil {
		p.log("presence watch: event file not written: %v", err)
	}
	for _, e := range evs {
		if !e.Collapsed {
			p.pending = append(p.pending, p.noticeText(e))
		}
	}
}

func (p *presenceWatcher) log(format string, a ...any) {
	if p.logf != nil {
		p.logf(format, a...)
	}
}

// noticeText is one line for the model: what changed, and for how long when it
// is an absence (less any host sleep).
func (p *presenceWatcher) noticeText(e watchEvent) string {
	switch e.Kind {
	case "absent":
		return fmt.Sprintf("presence: %s absent, last seen %s (%s, host sleep not counted)", e.Unit, p.lastSeen(e.Unit), p.w.AbsenceDuration(e.Unit, e.At).Round(time.Second))
	case "back":
		return fmt.Sprintf("presence: %s back", e.Unit)
	case "appeared":
		return fmt.Sprintf("presence: %s seen", e.Unit)
	case "present-after-gap":
		return fmt.Sprintf("presence: %s present after a gap in the watch, not a return", e.Unit)
	case "absent-before-gap":
		return fmt.Sprintf("presence: %s absent before a gap in the watch", e.Unit)
	case "foreign":
		return fmt.Sprintf("presence: key %s does not have the shape a supervisor writes and is not counted", e.Text)
	case "flapping":
		return fmt.Sprintf("presence: %s is flapping (%s); further transitions are quiet until it settles", e.Unit, e.Text)
	}
	if e.Unit != "" {
		return fmt.Sprintf("presence: %s %s %s", e.Kind, e.Unit, e.Text)
	}
	return fmt.Sprintf("presence: %s", e.Text)
}

func (p *presenceWatcher) lastSeen(unit string) string {
	if u := p.w.Unit(unit); u != nil && !u.LastSeen.IsZero() {
		return u.LastSeen.Format(time.RFC3339)
	}
	return "unknown"
}

// takeNotices returns the pending notices once.
func (p *presenceWatcher) takeNotices() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.pending
	p.pending = nil
	return out
}

// startPresenceWatch turns the watch on when DIRECTOR_PRESENCE_WATCH=1 and the
// session holds the global director role. Otherwise it does nothing and
// writes nothing.
func (b *Bus) startPresenceWatch(logf func(string, ...any)) error {
	if os.Getenv("DIRECTOR_PRESENCE_WATCH") != "1" || b.globalCfg == nil || b.globalCfg.Role != roleDirector {
		return nil
	}
	dir, err := stateDir()
	if err != nil {
		return err
	}
	table, err := loadThresholds(dir)
	if err != nil {
		return err
	}
	th := defaultWatchThresholds()
	th.FlapWindow = table[thresholdPresenceFlapWindow]
	th.FlapQuiet = table[thresholdPresenceFlapQuiet]
	store := newWatchStore(dir)
	w, err := store.Load(th)
	if err != nil {
		return err
	}
	scan := func(ctx context.Context) watchScan {
		g, err := b.globalReady(ctx)
		if err != nil {
			return watchScan{KeysErr: true}
		}
		return g.watchScan(ctx)
	}
	b.watch = newPresenceWatcher(store, w, scan, logf)
	return nil
}

// watchTick is the heartbeat's one call into the watch.
func (b *Bus) watchTick(ctx context.Context) {
	if b.watch != nil {
		b.watch.tick(ctx)
	}
}

// presenceNotices returns the unreported notices once.
func (b *Bus) presenceNotices() []string {
	if b.watch == nil {
		return nil
	}
	return b.watch.takeNotices()
}

// watchScan reads GLOBAL_PRESENCE for the watch: every key under presence.
// except a director's own rows, with its value parsed. A failed keys call is
// KeysErr; a key whose value cannot be fetched is Unfetched; a value that does
// not parse as a record has no Rec.
func (g *globalTier) watchScan(ctx context.Context) watchScan {
	keys, err := g.kv.Keys(ctx)
	if err != nil {
		if errors.Is(err, jetstream.ErrNoKeysFound) {
			return watchScan{}
		}
		return watchScan{KeysErr: true}
	}
	var sc watchScan
	for _, k := range keys {
		if !strings.HasPrefix(k, "presence.") || isDirectorKey(k) {
			continue
		}
		row := watchRow{Key: k}
		entry, err := g.kv.Get(ctx, k)
		if err != nil {
			row.Unfetched = true
			sc.Rows = append(sc.Rows, row)
			continue
		}
		var rec struct {
			State string `json:"state"`
			TS    string `json:"ts"`
		}
		if json.Unmarshal(entry.Value(), &rec) == nil {
			ts, _ := time.Parse(time.RFC3339, rec.TS)
			row.Rec = &watchRec{State: rec.State, TS: ts}
		}
		sc.Rows = append(sc.Rows, row)
	}
	return sc
}
