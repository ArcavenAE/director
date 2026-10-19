package main

// The presence watch inside the shim (sim/design/presence-watch.md, build item
// 4, D2 option (a), operator Desk ruling director-319-d2 "2 option 1 for now").

import (
	"context"
	"time"
)

// presenceWatcher is the one seam between the shim and the watch core. Nothing
// else in the shim knows the watch exists: the heartbeat calls Bus.watchTick,
// the tools call Bus.presenceNotices, and main starts it. Moving the watch out
// of the shim later means replacing what feeds tick and what drains notices.
type presenceWatcher struct {
	w    *PresenceWatch
	now  func() time.Time
	mono func() time.Duration
}

func (p *presenceWatcher) tick(ctx context.Context) {}

// startPresenceWatch turns the watch on when DIRECTOR_PRESENCE_WATCH=1 and the
// session holds the global director role. Otherwise it does nothing and
// writes nothing.
func (b *Bus) startPresenceWatch(logf func(string, ...any)) error { return nil }

// watchTick is the heartbeat's one call into the watch.
func (b *Bus) watchTick(ctx context.Context) {}

// presenceNotices returns the unreported notices once.
func (b *Bus) presenceNotices() []string { return nil }
