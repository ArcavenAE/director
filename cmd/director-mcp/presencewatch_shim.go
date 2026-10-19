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
	thresholdPresenceHoldSellBy = "presence_hold_sell_by"
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

	hold    *heldStore
	sellBy  time.Duration // a hold's sell-by when the send names no reply_by
	publish func(ctx context.Context, e *Envelope) (*sendResult, error)
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
	if p.hold != nil {
		p.release(ctx, p.now())
	}
}

// release goes through the held records oldest first. A record past its sell-by
// is proposed, never sent (nothing goes out on a clock past what the sender
// meant). One whose cluster is back and beating is published through the normal
// global send, under the record's own id as the message id, so a crash between
// the publish and the record is repeated and the hub's dedupe window drops the
// copy. A refusal again leaves it held. Any other failure marks it failed once.
func (p *presenceWatcher) release(ctx context.Context, now time.Time) {
	recs, err := p.hold.Current()
	if err != nil {
		p.log("presence watch: held store not read: %v", err)
		return
	}
	for _, r := range recs {
		if r.State != holdHeld {
			continue
		}
		if now.After(r.SellBy) {
			if err := p.hold.Append(heldRec{ID: r.ID, State: holdProposed, StateAt: now}); err != nil {
				p.log("presence watch: held store not written: %v", err)
				continue
			}
			p.pending = append(p.pending, fmt.Sprintf("presence: held send %s to %s is past sell-by (%s): release, revise or cancel", r.ID, r.To, r.SellBy.Format(time.RFC3339)))
			continue
		}
		cluster, role, err := parseGlobalAddress(r.To)
		if err != nil || role != roleSupervisor || !p.w.Releasable(cluster) {
			continue
		}
		// A cancel may have landed since the fold above.
		if cur, err := p.hold.find(r.ID); err != nil || cur == nil || cur.State != holdHeld {
			continue
		}
		res, err := p.publish(ctx, r.Envelope)
		var refused *presenceRefusalErr
		var unread *presenceReadErr
		switch {
		case err == nil:
			if aerr := p.hold.Append(heldRec{ID: r.ID, State: holdReleased, StateAt: now, Stream: res.Stream, Seq: res.Sequence}); aerr != nil {
				p.log("presence watch: release of %s published but not recorded, it will be published again and deduped: %v", r.ID, aerr)
				continue
			}
			p.pending = append(p.pending, fmt.Sprintf("presence: held send %s released to %s after %s (stream %s, sequence %d); delivered to the hub, not read", r.ID, r.To, now.Sub(r.HeldAt).Round(time.Second), res.Stream, res.Sequence))
		case errors.As(err, &refused) || errors.As(err, &unread):
			// the unit flapped out again, or the hub did not answer: still held
		default:
			if aerr := p.hold.Append(heldRec{ID: r.ID, State: holdFailed, StateAt: now, Reason: err.Error()}); aerr != nil {
				p.log("presence watch: held store not written: %v", aerr)
				continue
			}
			p.pending = append(p.pending, fmt.Sprintf("presence: held send %s to %s failed on release and will not be retried: %v", r.ID, r.To, err))
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
	sellBy := table[thresholdPresenceHoldSellBy]
	scan := func(ctx context.Context) watchScan {
		g, err := b.globalReady(ctx)
		if err != nil {
			return watchScan{KeysErr: true}
		}
		return g.watchScan(ctx)
	}
	b.watch = newPresenceWatcher(store, w, scan, logf)
	b.watch.hold = newHeldStore(dir)
	b.watch.sellBy = sellBy
	b.watch.publish = func(ctx context.Context, e *Envelope) (*sendResult, error) {
		body, err := json.Marshal(e)
		if err != nil {
			return nil, err
		}
		return b.publishGlobalOnce(ctx, e, body)
	}
	return nil
}

// namedRefusal, with the watch on, says when the cluster was last seen in a
// refusal for absence (R-210, #88): "no live presence for <address>, last seen
// <t>", from the watch's state. A cluster the watch has never seen, one it reads
// as present, and a refusal where the rows could not be read keep their text.
func (b *Bus) namedRefusal(e *Envelope, err error) error {
	p := b.watch
	var refused *presenceRefusalErr
	if p == nil || !errors.As(err, &refused) || refused.unreadable {
		return err
	}
	cluster, role, perr := parseGlobalAddress(e.Recipient.Address)
	if perr != nil || role != roleSupervisor {
		return err
	}
	p.mu.Lock()
	u := p.w.Unit(cluster)
	var status string
	var last, since time.Time
	if u != nil {
		status, last, since = u.Status, u.LastSeen, u.Since
	}
	p.mu.Unlock()
	if u == nil || last.IsZero() || status == unitPresent {
		return err
	}
	return &presenceRefusalErr{err: fmt.Errorf("no live presence for %s, last seen %s; the presence watch reads it %s since %s. no session would consume this send, so it is refused rather than stored on a subject nobody reads (R-92 liveness)",
		e.Recipient.Address, last.Format(time.RFC3339), status, since.Format(time.RFC3339))}
}

// holdRefused keeps a send that presence refused, when the watch is on and the
// refusal is for absence or for a hub that did not answer the presence read. It
// returns the heldErr to hand back in place of the refusal, or nil to refuse as
// before: the watch off, fail_fast, or any other failure (a publish that failed
// after presence passed, an R-78 refusal, a validation error).
func (b *Bus) holdRefused(ctx context.Context, e *Envelope, cause error) error {
	p := b.watch
	if p == nil || p.hold == nil {
		return nil
	}
	if ff, _ := ctx.Value(holdFailFastKey{}).(bool); ff {
		return nil
	}
	var refused *presenceRefusalErr
	var unread *presenceReadErr
	if !errors.As(cause, &refused) && !errors.As(cause, &unread) {
		return nil
	}
	now := p.now()
	rec := heldRec{ID: e.MessageID, To: e.Recipient.Address, Envelope: e, HeldAt: now, Reason: cause.Error(), State: holdHeld, StateAt: now, SellBy: now.Add(p.sellBy)}
	if t, err := time.Parse(time.RFC3339, e.ReplyBy); err == nil {
		rec.SellBy = t
	}
	if err := p.hold.Append(rec); err != nil {
		// A send that cannot be kept is refused as before, with the reason added.
		return fmt.Errorf("%w; it could not be held either: %v", cause, err)
	}
	return &heldErr{rec: rec}
}

// heldWhileOff counts the held records a shim with the watch off leaves in the
// store: a shim started with the flag off while held records exist sends none
// of them, and says so in each presence notice, so turning the watch off cannot
// strand mail silently. Read only. A director shim only.
func (b *Bus) heldWhileOff() int {
	if b.watch != nil || b.globalCfg == nil || b.globalCfg.Role != roleDirector {
		return 0
	}
	dir, err := stateDir()
	if err != nil {
		return 0
	}
	recs, err := newHeldStore(dir).Current()
	if err != nil {
		return 0
	}
	n := 0
	for _, r := range recs {
		if r.State == holdHeld {
			n++
		}
	}
	return n
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
		if n := b.heldWhileOff(); n > 0 {
			return []string{fmt.Sprintf("presence: %d held while the watch is off; none is sent until DIRECTOR_PRESENCE_WATCH=1 is back on", n)}
		}
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

// toolCancelHeld withdraws a held send, or marks it superseded by a later one.
// Only a record still held or proposed can be cancelled.
func toolCancelHeld(ctx context.Context, bus *Bus, raw json.RawMessage) (any, error) {
	if bus.watch == nil || bus.watch.hold == nil {
		return nil, errors.New("cancel_held: the presence watch is off for this session (DIRECTOR_PRESENCE_WATCH is not 1), so nothing is held")
	}
	var a struct {
		ID          string `json:"id"`
		Replacement string `json:"replacement"`
	}
	if err := json.Unmarshal(raw, &a); err != nil || a.ID == "" {
		return nil, errors.New("cancel_held: give the id of the held send")
	}
	p := bus.watch
	p.mu.Lock()
	defer p.mu.Unlock()
	cur, err := p.hold.find(a.ID)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		return nil, fmt.Errorf("cancel_held: no held send has the id %s", a.ID)
	}
	if cur.State != holdHeld && cur.State != holdProposed {
		return nil, fmt.Errorf("cancel_held: %s is %s, and only a held or proposed send can be cancelled", a.ID, cur.State)
	}
	if a.Replacement != "" {
		if rep, err := p.hold.find(a.Replacement); err != nil || rep == nil {
			return nil, fmt.Errorf("cancel_held: the replacement %s is not a held send", a.Replacement)
		}
	}
	if err := p.hold.Append(heldRec{ID: a.ID, State: holdCancelled, StateAt: p.now(), SupersededBy: a.Replacement}); err != nil {
		return nil, err
	}
	return map[string]any{"status": "cancelled", "id": a.ID, "superseded_by": a.Replacement, "note": "it will not be sent"}, nil
}

// withHoldTools adds what the held store needs to the tool catalog, only when
// the watch is on: fail_fast on send_message and the cancel_held tool.
func withHoldTools(tools []toolDef, on bool) []toolDef {
	if !on {
		return tools
	}
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	for i := range tools {
		if tools[i].Name == "send_message" {
			if props, ok := tools[i].InputSchema["properties"].(map[string]any); ok {
				props["fail_fast"] = map[string]any{"type": "boolean", "description": "refuse at once when the recipient has no live presence, as without the presence watch, instead of holding the send for release when it returns"}
			}
			tools[i].Description += " With the presence watch on, a send to a global address that presence refuses is held, not delivered, and released when the recipient returns; the result says so. Pass fail_fast to refuse instead."
		}
	}
	return append(tools, toolDef{
		Name:        "cancel_held",
		Description: "Withdraw a send the presence watch is holding (status held or proposed), optionally naming the later send that replaces it. A cancelled send is never released. Offered only when the presence watch is on.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":          str("the hold id the send_message result gave"),
				"replacement": str("optional hold id of the later send that supersedes this one"),
			},
			"required": []string{"id"},
		},
	})
}
