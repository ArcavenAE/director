package main

// The class-aware cue (aae-orc-bykbv, T3 of the director-cue plan). With
// DIRECTOR_CUE_CLASS=1 the cue reads each new waiting message's class from the
// receiver classifier (classify.go): act mail cues at once, under the usual
// Floor, and the cue names the waking message; routine mail is held, and one
// cue covers it when a fixed window D (the cue's Window) from the first held
// message ends. A seat that reads its mail before then gets no cue for it.
//
// Where this code is unsure it fails open to act: an unreadable waiting set,
// a message the peek did not return, an undecodable message. A held message
// is never dropped, only delayed by at most D. With the switch off none of
// this runs.

import (
	"context"
	"encoding/json"
	"time"
)

// cueWaitingLimit bounds one peek of the waiting set. A waiting count past it
// leaves the rest unclassified, and those count as act.
const cueWaitingLimit = 500

type classState struct {
	floorsSet  bool
	considered map[classKey]struct{}
	held       map[classKey]*cueMeta
	order      []classKey // held keys in arrival order
	holdStart  time.Time  // when the first held message arrived; zero when none
	wake       *cueMeta   // the first act message since the last cue
}

func newClassState() *classState {
	return &classState{considered: map[classKey]struct{}{}, held: map[classKey]*cueMeta{}}
}

func (s *classState) clear() {
	s.considered = map[classKey]struct{}{}
	s.clearHeld()
	s.wake = nil
}

func (s *classState) clearHeld() {
	s.held = map[classKey]*cueMeta{}
	s.order = nil
	s.holdStart = time.Time{}
}

// classRead is what one tick read for the class-aware cue, outside the lock.
type classRead struct {
	items  []summaryItem
	err    error
	floors map[string]uint64
	ferr   error
}

// readClasses decides whether this tick needs the waiting set, and reads it.
// It returns nil when the class-aware cue is off, when the source cannot
// show waiting messages, or when nothing needs reading.
func (c *cue) readClasses(ctx context.Context, n uint64, now time.Time) *classRead {
	ws, ok := c.src.(waitingSource)
	if !ok {
		return nil
	}
	c.mu.Lock()
	if !c.cfg.ClassAware || c.cls == nil {
		c.mu.Unlock()
		return nil
	}
	if c.cs == nil {
		c.cs = newClassState()
	}
	cs := c.cs
	needFloors := !cs.floorsSet
	timerDue := len(cs.held) > 0 && !cs.holdStart.IsZero() && !now.Before(cs.holdStart.Add(c.cfg.Window))
	// While anything is held the waiting set is read on every tick: the count
	// alone cannot show that the seat read the held mail and a new message
	// arrived in the same tick, and that message must not wait for a window.
	need := n > c.lastSeen || c.reconnect || timerDue || len(cs.held) > 0
	c.mu.Unlock()
	if n == 0 {
		need = false // nothing is waiting, so there is nothing to classify
	}
	if !need && !needFloors {
		return nil
	}
	cr := &classRead{}
	if needFloors {
		// Read on the first tick whatever is waiting, so the boundary is the
		// stream position at cue start and a message that arrives later is
		// never backlog.
		cr.floors, cr.ferr = ws.floors(ctx)
	}
	if need {
		cr.items, cr.err = ws.waiting(ctx)
	}
	return cr
}

// applyClasses classifies the messages not seen before and returns how many
// of the add new arrivals the cue should count now: act messages, backlog
// (one cue, never one by one), and any arrival the peek did not show. Routine
// messages are held. The caller holds mu.
func (c *cue) applyClasses(cr *classRead, add uint64, now time.Time) uint64 {
	cs := c.cs
	if cr.err != nil {
		return add // fail open: count every arrival as act
	}
	if !cs.floorsSet && cr.ferr == nil && cr.floors != nil {
		for tier, seq := range cr.floors {
			c.cls.setBacklogFloor(tier, seq)
		}
		cs.floorsSet = true
	} else if !cs.floorsSet && cr.ferr == nil {
		cs.floorsSet = true
	}
	cur := make(map[classKey]bool, len(cr.items))
	for _, it := range cr.items {
		cur[classKey{it.Tier, it.Seq}] = true
	}
	for k := range cs.considered {
		if !cur[k] {
			delete(cs.considered, k)
		}
	}
	if len(cs.held) > 0 {
		kept := cs.order[:0]
		for _, k := range cs.order {
			if cur[k] {
				kept = append(kept, k)
			} else {
				delete(cs.held, k)
			}
		}
		cs.order = kept
		if len(cs.held) == 0 {
			cs.holdStart = time.Time{}
		}
	}
	var seen, counted uint64
	for _, it := range cr.items {
		k := classKey{it.Tier, it.Seq}
		if _, ok := cs.considered[k]; ok {
			continue
		}
		cs.considered[k] = struct{}{}
		seen++
		var data []byte
		var m *cueMeta
		if it.Env != nil {
			data, _ = json.Marshal(it.Env)
			m = metaOf(it.Env)
		}
		class := c.cls.classify(it.Tier, it.Seq, data, now)
		switch {
		case c.cls.isBacklog(it.Tier, it.Seq):
			counted++
		case class == classAct || c.openWaits > 0:
			// An open wait delivers what arrives, so nothing is held for it.
			counted++
			if cs.wake == nil && class == classAct {
				cs.wake = m
			}
		default:
			cs.held[k] = m
			cs.order = append(cs.order, k)
			if cs.holdStart.IsZero() {
				cs.holdStart = now
			}
		}
	}
	if add > seen {
		counted += add - seen // arrivals the peek did not show are act
	}
	return counted
}

// fireHeld ends the hold when its window is up: the held messages still
// waiting are counted, and the oldest is the one the cue names unless an act
// message already is. The caller holds mu.
func (c *cue) fireHeld(now time.Time) {
	cs := c.cs
	if cs == nil || len(cs.held) == 0 || cs.holdStart.IsZero() || now.Before(cs.holdStart.Add(c.cfg.Window)) || c.openWaits > 0 {
		return
	}
	c.fold += uint64(len(cs.held))
	if cs.wake == nil {
		cs.wake = cs.held[cs.order[0]]
	}
	cs.clearHeld()
}

// takeWake returns the message the next cue names, once. The caller holds mu.
func (c *cue) takeWake() *cueMeta {
	if c.cs == nil || c.cs.wake == nil {
		return nil
	}
	m := c.cs.wake
	c.cs.wake = nil
	return m
}

// waiting reads the waiting messages of both tiers without acking.
func (s busCueSource) waiting(ctx context.Context) ([]summaryItem, error) {
	items, _, _, err := peekWaiting(ctx, s.b.js, s.b.cons(), "local", cueWaitingLimit)
	if err != nil {
		return nil, err
	}
	if s.b.globalCfg != nil {
		g, gerr := s.b.globalReady(ctx)
		if gerr != nil {
			return nil, gerr
		}
		gitems, _, _, gerr := peekWaiting(ctx, g.js, g.cons(), "global", cueWaitingLimit)
		if gerr != nil {
			return nil, gerr
		}
		items = append(items, gitems...)
	}
	return items, nil
}

// floors reads each tier's last stream sequence now: a message at or below it
// was already there when the cue started, so it is backlog.
func (s busCueSource) floors(ctx context.Context) (map[string]uint64, error) {
	out := map[string]uint64{}
	info, err := s.b.cons().Info(ctx)
	if err != nil {
		return nil, err
	}
	st, err := s.b.js.Stream(ctx, info.Stream)
	if err != nil {
		return nil, err
	}
	si, err := st.Info(ctx)
	if err != nil {
		return nil, err
	}
	out["local"] = si.State.LastSeq
	if s.b.globalCfg != nil {
		g, gerr := s.b.globalReady(ctx)
		if gerr != nil {
			return nil, gerr
		}
		ginfo, gerr := g.cons().Info(ctx)
		if gerr != nil {
			return nil, gerr
		}
		gst, gerr := g.js.Stream(ctx, ginfo.Stream)
		if gerr != nil {
			return nil, gerr
		}
		gsi, gerr := gst.Info(ctx)
		if gerr != nil {
			return nil, gerr
		}
		out["global"] = gsi.State.LastSeq
	}
	return out, nil
}
