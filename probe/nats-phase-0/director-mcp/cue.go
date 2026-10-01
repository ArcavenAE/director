package main

// The P1 channel cue (sim/design/channel-cue.md, #166). With DIRECTOR_CUE=1
// the shim sends its Claude Code one notifications/claude/channel notice when
// mail arrives, carrying the sender, performative and message id in meta and
// never the body. The model then drains with wait_for_message as before, so
// FIFO order, acks and R-08 are unchanged; the cue only replaces the human
// who types "read your inbox" into the pane. Off, the shim is unchanged.
//
// The core below takes its bus, clock, nonce and output as functions so the
// design's cases run without a broker (cue_test.go).

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"
)

const (
	cueContent      = "new message in your director inbox"
	cueAckAsk       = " (call inbox_summary with cue_ack set to the nonce in this notice)"
	cueSelfTest     = "director cue self-test: call inbox_summary with cue_ack set to the nonce in this notice"
	cueStateOff     = "off"
	cueStateUnver   = "unverified"
	cueStateLive    = "live"
	cueTodayVersion = "2025-06-18"
)

type cueConfig struct {
	Enabled bool
	// Versions are the client protocol versions the cue was measured on. P0
	// measured 2025-11-25; C-0 decides whether 2025-06-18 joins.
	Versions       []string
	Poll           time.Duration
	Floor          time.Duration
	Window         time.Duration
	SelfTestWindow time.Duration
}

func defaultCueConfig() cueConfig {
	return cueConfig{
		Versions:       []string{"2025-11-25"},
		Poll:           2 * time.Second,
		Floor:          5 * time.Second,
		Window:         300 * time.Second,
		SelfTestWindow: 120 * time.Second,
	}
}

// loadCueConfig reads the per-seat opt-in. Only the exact value 1 turns it on.
func loadCueConfig() cueConfig {
	cfg := defaultCueConfig()
	cfg.Enabled = os.Getenv("DIRECTOR_CUE") == "1"
	return cfg
}

// cueMeta is what a cue may say about a message: never the body, never text
// a sender chose (INJ-A..C).
type cueMeta struct {
	Sender, Performative, MessageID string
}

type cueSource interface {
	// count is the number of messages waiting across tiers, read from
	// consumer state; nothing is fetched or acked.
	count(ctx context.Context) (uint64, error)
	// oldestMeta reads the oldest waiting message without consuming it.
	oldestMeta(ctx context.Context) (*cueMeta, error)
}

type pendingCue struct {
	at      time.Time
	meta    *cueMeta
	count   uint64
	pending uint64 // messages waiting when the cue went out
	recued  bool
}

type cue struct {
	mu    sync.Mutex
	cfg   cueConfig
	src   cueSource
	emit  func(params map[string]any)
	warn  func(ids []string, state string)
	now   func() time.Time
	nonce func() string

	active        bool
	state, reason string
	selfNonce     string
	selfAt        time.Time
	valid         map[string]time.Time // nonces sent while unverified, with expiry
	openWaits     int
	lastSeen      uint64
	lastCueAt     time.Time
	fold          uint64
	out           *pendingCue
	reconnect     bool
	// readErr is set while the watcher cannot read inbox state. It shows in
	// the reason beside, never instead of, the self-test reason.
	readErr string
	logf    func(string, ...any)
}

func newCue(cfg cueConfig, src cueSource, emit func(map[string]any), warn func([]string, string), now func() time.Time, nonce func() string) *cue {
	return &cue{cfg: cfg, src: src, emit: emit, warn: warn, now: now, nonce: nonce,
		state: cueStateOff, valid: map[string]time.Time{}}
}

func randomNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// handshake answers initialize. A measured client version is echoed and the
// channel declared; any other version answers as the shim always has, and the
// cue stays off with a reason rather than dropping silently (section 3.2).
func (c *cue) handshake(clientVersion string) (version string, channel bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cfg.Enabled && slices.Contains(c.cfg.Versions, clientVersion) {
		c.active, c.state, c.reason = true, cueStateUnver, ""
		return clientVersion, true
	}
	c.active, c.state = false, cueStateOff
	c.reason = fmt.Sprintf("protocol %s not verified", clientVersion)
	return cueTodayVersion, false
}

// initialized sends the startup self-test (section 3.3). Only a model that
// received the notice can know its nonce.
func (c *cue) initialized() {
	c.mu.Lock()
	if !c.active {
		c.mu.Unlock()
		return
	}
	c.selfNonce, c.selfAt = c.nonce(), c.now()
	p := map[string]any{"content": cueSelfTest, "meta": map[string]any{"kind": "self-test", "nonce": c.selfNonce}}
	c.mu.Unlock()
	c.emit(p)
}

// ack records an inbox_summary cue_ack. Only this start's self-test nonce
// inside its window, or a nonce a cue carried while unverified inside W,
// makes the cue live. A drain never does.
func (c *cue) ack(n string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.active || c.state == cueStateLive {
		return
	}
	now := c.now()
	if n == c.selfNonce && now.Sub(c.selfAt) <= c.cfg.SelfTestWindow {
		c.promote()
		return
	}
	if exp, ok := c.valid[n]; ok && !now.After(exp) {
		c.promote()
		return
	}
	c.reason = "wrong nonce"
}

func (c *cue) promote() {
	c.state, c.reason = cueStateLive, ""
	c.valid = map[string]time.Time{}
}

// answered is any wait_for_message or inbox_summary call: it settles the
// outstanding cue for receipt. It never changes the cue state.
func (c *cue) answered() {
	c.mu.Lock()
	c.out = nil
	c.mu.Unlock()
}

// waitBegin and waitEnd bracket a wait_for_message. An arrival seen while a
// wait is open is delivered by that wait, so it is not cued (section 3.4).
func (c *cue) waitBegin() {
	c.mu.Lock()
	c.openWaits++
	c.out = nil
	c.mu.Unlock()
}

func (c *cue) waitEnd() {
	c.mu.Lock()
	if c.openWaits > 0 {
		c.openWaits--
	}
	c.mu.Unlock()
}

// reconnected makes the next tick treat whatever is waiting as an arrival, so
// a seat cut off by a broker reconnect is cued once.
func (c *cue) reconnected() {
	c.mu.Lock()
	c.reconnect = true
	c.mu.Unlock()
}

func (c *cue) status() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	reason := c.reason
	if c.readErr != "" {
		if reason != "" {
			reason += "; "
		}
		reason += c.readErr
	}
	return map[string]string{"state": c.state, "reason": reason}
}

// params builds one cue. While unverified, each cue carries a fresh nonce and
// asks for its echo, so any cue can prove the channel (section 3.3).
func (c *cue) params(m *cueMeta, count uint64, now time.Time) map[string]any {
	meta := map[string]any{"count": strconv.FormatUint(count, 10)}
	if m != nil {
		meta["sender"], meta["performative"], meta["message_id"] = m.Sender, m.Performative, m.MessageID
	}
	content := cueContent
	if c.state != cueStateLive {
		for k, exp := range c.valid {
			if now.After(exp) {
				delete(c.valid, k) // expired: it can no longer promote
			}
		}
		n := c.nonce()
		c.valid[n] = now.Add(c.cfg.Window)
		meta["nonce"] = n
		content += cueAckAsk
	}
	return map[string]any{"content": content, "meta": meta}
}

// tick is one watcher pass: read the pending count, cue new arrivals with
// coalescing, and run the receipt window (sections 3.4 and 3.5).
func (c *cue) tick(ctx context.Context) {
	c.mu.Lock()
	if !c.active {
		c.mu.Unlock()
		return
	}
	now := c.now()
	if c.state == cueStateUnver && c.reason == "" && c.selfNonce != "" && now.Sub(c.selfAt) > c.cfg.SelfTestWindow {
		c.reason = "no nonce echo after self-test"
	}
	c.mu.Unlock()

	n, err := c.src.count(ctx)
	c.mu.Lock()
	if err != nil {
		// Skip the tick, so a transient error cannot read as a drop. Show it
		// on the first failure, so a permanent one (a deleted durable, a lost
		// permission) is visible rather than a seat that looks live and is
		// never cued.
		if c.readErr == "" {
			c.readErr = "watcher: cannot read inbox state: " + err.Error()
			c.logOnce("channel cue %s", c.readErr)
		}
		c.mu.Unlock()
		return
	}
	if c.readErr != "" {
		c.readErr = ""
		c.logOnce("channel cue watcher: inbox state readable again")
	}
	if c.reconnect {
		c.reconnect = false
		if n > 0 && c.openWaits == 0 {
			c.lastSeen = 0
		}
	}
	if c.out != nil && n < c.out.pending {
		c.out = nil // the cued messages left the durable: answered
	}
	if n < c.lastSeen {
		c.lastSeen = n
	}
	if n > c.lastSeen {
		if c.openWaits == 0 {
			c.fold += n - c.lastSeen
		}
		c.lastSeen = n
	}
	sendNew := c.fold > 0 && (c.lastCueAt.IsZero() || now.Sub(c.lastCueAt) >= c.cfg.Floor)
	var recue, warnOut *pendingCue
	if c.out != nil && now.Sub(c.out.at) >= c.cfg.Window {
		warnOut = c.out
		if !c.out.recued {
			recue = c.out
		} else {
			c.out = nil // twice unanswered: back to the manual wake for these
		}
	}
	state := c.state
	c.mu.Unlock()

	var emits []map[string]any
	if sendNew {
		m, _ := c.src.oldestMeta(ctx)
		c.mu.Lock()
		p := c.params(m, c.fold, now)
		c.out = &pendingCue{at: now, meta: m, count: c.fold, pending: n}
		c.lastCueAt, c.fold = now, 0
		c.mu.Unlock()
		emits = append(emits, p)
	}
	if warnOut != nil {
		var ids []string
		if warnOut.meta != nil {
			ids = append(ids, warnOut.meta.MessageID)
		}
		c.warn(ids, state)
	}
	if recue != nil && !sendNew {
		c.mu.Lock()
		p := c.params(recue.meta, recue.count, now)
		recue.at, recue.recued = now, true
		c.lastCueAt = now
		c.mu.Unlock()
		emits = append(emits, p)
	}
	for _, p := range emits {
		c.emit(p)
	}
}

// logOnce logs a watcher transition; callers hold mu and call it only when
// the transition happens, so it cannot repeat per tick.
func (c *cue) logOnce(format string, a ...any) {
	if c.logf != nil {
		c.logf(format, a...)
	}
}

// run polls on the configured interval until ctx ends.
func (c *cue) run(ctx context.Context) {
	t := time.NewTicker(c.cfg.Poll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.tick(ctx)
		}
	}
}

// cueAckArg reads the optional cue_ack argument of inbox_summary.
func cueAckArg(raw json.RawMessage) string {
	var a struct {
		CueAck string `json:"cue_ack"`
	}
	_ = json.Unmarshal(raw, &a)
	return a.CueAck
}

// busCueSource reads arrival from consumer state and the oldest waiting
// message by sequence, through the same paths inbox_summary uses.
type busCueSource struct{ b *Bus }

// count fails when a tier's state cannot be read, so the watcher skips that
// tick: a tier left out would read as a drop and the next good read as an
// arrival, one spurious cue. A hub not yet attached is left out, as in
// remaining().
func (s busCueSource) count(ctx context.Context) (uint64, error) {
	info, err := s.b.consumer.Info(ctx)
	if err != nil {
		return 0, err
	}
	n := info.NumPending + uint64(info.NumAckPending)
	if s.b.globalCfg != nil {
		s.b.globalMu.Lock()
		g := s.b.global
		s.b.globalMu.Unlock()
		if g != nil {
			gi, err := g.consumer.Info(ctx)
			if err != nil {
				return 0, err
			}
			n += gi.NumPending + uint64(gi.NumAckPending)
		}
	}
	return n, nil
}

func (s busCueSource) oldestMeta(ctx context.Context) (*cueMeta, error) {
	items, _, _, err := peekWaiting(ctx, s.b.js, s.b.consumer, "local", 1)
	if err == nil && len(items) > 0 && items[0].Env != nil {
		return metaOf(items[0].Env), nil
	}
	if s.b.globalCfg != nil {
		if g, gerr := s.b.globalReady(ctx); gerr == nil {
			gitems, _, _, gerr := peekWaiting(ctx, g.js, g.consumer, "global", 1)
			if gerr == nil && len(gitems) > 0 && gitems[0].Env != nil {
				return metaOf(gitems[0].Env), nil
			}
		}
	}
	return nil, err
}

func metaOf(e *Envelope) *cueMeta {
	return &cueMeta{Sender: senderLabel(e), Performative: e.Performative, MessageID: e.MessageID}
}

// warnUnanswered is the fail-loud half of R-89 for a cue (section 3.5): a
// warning, never a death or loss report. It always logs, and with the global
// tier on it also tells director.
func warnUnanswered(ctx context.Context, bus *Bus, logf func(string, ...any), ids []string, state string) {
	text := fmt.Sprintf("cue.unanswered: seat %s, messages %v still waiting after the receipt window, cue %s", bus.self.AgentID, ids, state)
	logf("WARNING %s", text)
	if bus.globalCfg == nil {
		return
	}
	raw, _ := json.Marshal(map[string]any{"to": "global://director", "performative": "INFORM", "text": text})
	env, ws, err := buildSendEnvelope(bus.self, raw)
	if err == nil {
		_, err = bus.publish(ctx, env, ws)
	}
	if err != nil {
		logf("cue.unanswered not delivered to director: %v", err)
	}
}
