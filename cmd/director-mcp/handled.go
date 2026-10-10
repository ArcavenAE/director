package main

// The handled ledger and ack-after-handled (K4, director#278; design in
// sim/design/director-functions-redesign.md, "K4 in detail").
//
// Today every drain acks a message the moment it is read, so a message that
// was read and then lost before anyone acted on it reads as done (R-169, R-08).
// With the handled mode on, a read does not ack. The message stays ack-pending
// on the seat's durable until the session records what it did with it, through
// mark_handled: handled, forwarded (with the forward's message id) or parked
// (with a one-line reason). The ledger write comes first and the ack second. A
// crash between the two leaves the message pending; it is redelivered, the
// drain finds the disposition already in the ledger, acks it and does not
// return it, so there is no second record.
//
// The mode is per process: DIRECTOR_HANDLED=1, set for director's own seat and
// off by default. With it off nothing here runs and wait_for_message acks on
// read exactly as before, so no other seat's drain changes.
//
// The ledger is handled.jsonl beside the seat's other director state: append
// only, folded on read by the envelope's message_id, last event wins, one flock
// around fold, check and append. Each event also records the tier, stream and
// sequence, for the reader only: sequences restart after a stream is deleted,
// recreated or restored, so a sequence key would ack a new message that reused
// a ledgered sequence and never return it. message_id is the sender's id and
// the bus dedupe id, so it survives a reset. That is the shape of dws (skills/director/
// scripts/dws), so a reconciler can read it. There is no cursor and no
// high-water mark, which is the failure R-169 names.
//
// The ack wait for the mode is an entry in the operator-editable threshold
// table, thresholds.json in the same directory. The code carries the table's
// defaults, never a bare constant at the point of use.
//
// Diagnostic, not a gate (SOUL section 8, ADR-007): nothing here blocks a send
// or a merge, and nothing acts on a clock. An age is shown; a person or a
// reconciler proposal decides.
//
// An envelope that cannot be keyed or trusted is rejected, not held: one with no
// message_id, and one whose message_id is already in the ledger with different
// stored bytes (a reuse by a sender outside the shim). Holding it would
// redeliver it without bound (MaxDeliver is -1), so the drain records a
// rejected line keyed by tier, stream and sequence, reports it through
// inbox_summary, and Terms it, as the receive path does for undecodable mail.
// The original's disposition is left as recorded.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

const thresholdHandledAckWait = "handled_ack_wait"

// defaultThresholds is the threshold table's built-in rows. thresholds.json in
// the state directory overrides any of them by name. The handled ack wait is
// longer than the 30 s JetStream default so a message the session is still
// working on is not redelivered under it.
var defaultThresholds = map[string]time.Duration{
	thresholdHandledAckWait: 30 * time.Minute,
}

// ledgerLockTimeout bounds how long a writer waits for the flock. A holder that
// does not let go is a loud failure, not a wait forever.
const ledgerLockTimeout = 5 * time.Second

// stateDir is where the seat's director state lives: DIRECTOR_STATE, else
// ~/.director/state, the same default the dws script uses.
func stateDir() (string, error) {
	if d := os.Getenv("DIRECTOR_STATE"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("handled mode: no DIRECTOR_STATE and no home directory: %w", err)
	}
	return filepath.Join(home, ".director", "state"), nil
}

// loadThresholds returns the table: the defaults, overridden by the durations
// in thresholds.json when it exists. A value that is not a positive duration
// is an error, never a silent fall back to the default.
func loadThresholds(dir string) (map[string]time.Duration, error) {
	out := map[string]time.Duration{}
	for k, v := range defaultThresholds {
		out[k] = v
	}
	b, err := os.ReadFile(filepath.Join(dir, "thresholds.json"))
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("threshold table: %w", err)
	}
	var raw map[string]string
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("threshold table %s: %w", filepath.Join(dir, "thresholds.json"), err)
	}
	for k, v := range raw {
		if _, known := defaultThresholds[k]; !known {
			return nil, fmt.Errorf("threshold table: unknown key %q (a misspelled row would silently keep its default)", k)
		}
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("threshold table: %s = %q is not a positive duration", k, v)
		}
		out[k] = d
	}
	return out, nil
}

// handledKey is a message's position on a stream. It keys only the rejected
// lines, which have no usable message_id. Sequences are per stream and restart
// on a reset, so it never keys a handled message.
type handledKey struct {
	Tier   string
	Stream string
	Seq    uint64
}

// handledEvent is one ledger line. Tier, Stream and Seq say where the message
// was read, for the reader. Hash is over the envelope's stored data bytes, not
// the headers (a migration may add a header, and that is not a reuse).
type handledEvent struct {
	At          string `json:"at"`
	Tier        string `json:"tier"`
	Stream      string `json:"stream"`
	Seq         uint64 `json:"sequence"`
	MessageID   string `json:"message_id,omitempty"`
	Hash        string `json:"hash,omitempty"`
	Disposition string `json:"disposition"`
	ForwardID   string `json:"forward_id,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

const dispRejected = "rejected"

func (e handledEvent) pos() handledKey { return handledKey{e.Tier, e.Stream, e.Seq} }

// handledFold is the ledger folded: handled messages by message_id, and the
// rejected lines by position.
type handledFold struct {
	ByID     map[string]handledEvent
	Rejected map[handledKey]handledEvent
}

// envelopeHash is the hash a ledger event records: sha256 over the stored data
// bytes.
func envelopeHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type handledLedger struct {
	path, lock string
	// syncFile is fsync; a field so a test can fail it.
	syncFile func(*os.File) error
}

func newHandledLedger(dir string) *handledLedger {
	return &handledLedger{path: filepath.Join(dir, "handled.jsonl"), lock: filepath.Join(dir, "handled.lock"), syncFile: func(f *os.File) error { return f.Sync() }}
}

// foldLines folds the ledger bytes: last event per message_id wins. A line that
// is not whole JSON (a torn last line) is skipped, not fatal.
func foldLines(b []byte) *handledFold {
	out := &handledFold{ByID: map[string]handledEvent{}, Rejected: map[handledKey]handledEvent{}}
	start := 0
	for i := 0; i <= len(b); i++ {
		if i < len(b) && b[i] != '\n' {
			continue
		}
		line := b[start:i]
		start = i + 1
		if len(line) == 0 {
			continue
		}
		var e handledEvent
		if json.Unmarshal(line, &e) != nil || e.Tier == "" || e.Disposition == "" {
			continue
		}
		if e.Disposition == dispRejected {
			if e.Seq != 0 {
				out.Rejected[e.pos()] = e
			}
			continue
		}
		if e.MessageID == "" {
			continue
		}
		out.ByID[e.MessageID] = e
	}
	return out
}

func (l *handledLedger) fold() (*handledFold, error) {
	b, err := os.ReadFile(l.path)
	if errors.Is(err, os.ErrNotExist) {
		return foldLines(nil), nil
	}
	if err != nil {
		return nil, fmt.Errorf("handled ledger: %w", err)
	}
	return foldLines(b), nil
}

// lockLedger takes the exclusive flock, waiting up to ledgerLockTimeout.
func (l *handledLedger) lockLedger() (func(), error) {
	if err := os.MkdirAll(filepath.Dir(l.lock), 0o700); err != nil {
		return nil, fmt.Errorf("handled ledger: %w", err)
	}
	f, err := os.OpenFile(l.lock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("handled ledger lock: %w", err)
	}
	deadline := time.Now().Add(ledgerLockTimeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = f.Close()
			return nil, fmt.Errorf("handled ledger lock: %w", err)
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("handled ledger lock %s held for more than %s; nothing written", l.lock, ledgerLockTimeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// syncExisting fsyncs the ledger file. A line already present may have been
// written by an attempt whose fsync failed, so a repeat is not called recorded
// until it has been synced.
func (l *handledLedger) syncExisting() error {
	f, err := os.OpenFile(l.path, os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("handled ledger: %w", err)
	}
	defer f.Close()
	if err := l.syncFile(f); err != nil {
		return fmt.Errorf("handled ledger fsync: %w", err)
	}
	return nil
}

// record appends ev under the lock, after folding and checking, and fsyncs
// before it returns. It reports false, and writes nothing new, when the ledger
// already holds this event (the existing line is synced), so a redelivery
// after a crash adds no second record. A failed append or fsync is an error.
func (l *handledLedger) record(ev handledEvent) (bool, error) {
	unlock, err := l.lockLedger()
	if err != nil {
		return false, err
	}
	defer unlock()
	cur, err := l.fold()
	if err != nil {
		return false, err
	}
	if ev.Disposition == dispRejected {
		if _, ok := cur.Rejected[ev.pos()]; ok {
			return false, l.syncExisting()
		}
	} else if old, ok := cur.ByID[ev.MessageID]; ok && old.Disposition == ev.Disposition && old.ForwardID == ev.ForwardID && old.Reason == ev.Reason {
		return false, l.syncExisting()
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return false, err
	}
	line = append(line, '\n')
	// A torn last line is left as it is; the new event starts on its own line.
	if b, err := os.ReadFile(l.path); err == nil && len(b) > 0 && b[len(b)-1] != '\n' {
		line = append([]byte{'\n'}, line...)
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return false, fmt.Errorf("handled ledger: %w", err)
	}
	if _, err := f.Write(line); err != nil {
		_ = f.Close()
		return false, fmt.Errorf("handled ledger: %w", err)
	}
	if err := l.syncFile(f); err != nil {
		_ = f.Close()
		return false, fmt.Errorf("handled ledger fsync: %w", err)
	}
	return true, f.Close()
}

// heldMsg is a delivered, unacked message this session holds.
type heldMsg struct {
	msg       jetstream.Msg
	tier      string
	stream    string
	seq       uint64
	messageID string
	hash      string
	sender    string
	at        time.Time
}

// unhandledRow is one delivered message not yet in the ledger, for
// inbox_summary's read-unhandled section.
type unhandledRow struct {
	Tier      string `json:"tier"`
	Stream    string `json:"stream"`
	Seq       uint64 `json:"sequence"`
	MessageID string `json:"message_id"`
	Sender    string `json:"sender"`
	Age       string `json:"age"`
}

// handledState is the handled mode for one process. A nil *handledState is the
// mode off.
type handledState struct {
	ledger  *handledLedger
	ackWait time.Duration
	now     func() time.Time

	// streamOf names the stream behind a tier; onAck tells the tier an ack
	// landed (the global tier records its acked floor from it).
	streamOf func(tier string) string
	onAck    func(tier string, seq uint64)

	mu      sync.Mutex
	pending map[string]*heldMsg // by message_id

	stopOnce sync.Once
	stopCh   chan struct{}
}

// handledFromEnv returns the handled state when DIRECTOR_HANDLED=1, and nil
// (the mode off) otherwise.
func handledFromEnv() (*handledState, error) {
	if os.Getenv("DIRECTOR_HANDLED") != "1" {
		return nil, nil
	}
	dir, err := stateDir()
	if err != nil {
		return nil, err
	}
	table, err := loadThresholds(dir)
	if err != nil {
		return nil, err
	}
	return &handledState{
		ledger:  newHandledLedger(dir),
		ackWait: table[thresholdHandledAckWait],
		now:     time.Now,
		pending: map[string]*heldMsg{},
		stopCh:  make(chan struct{}),
	}, nil
}

// startKeepalive sends InProgress on every delivered, unrecorded message
// before its ack wait runs out, so long handling does not cause a redelivery.
// The ack wait then bounds only a session that died. It runs until stop.
func (h *handledState) startKeepalive() {
	every := h.ackWait / 3
	if every < 20*time.Millisecond {
		every = 20 * time.Millisecond
	}
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-h.stopCh:
				return
			case <-t.C:
				h.mu.Lock()
				msgs := make([]jetstream.Msg, 0, len(h.pending))
				for _, v := range h.pending {
					msgs = append(msgs, v.msg)
				}
				h.mu.Unlock()
				for _, m := range msgs {
					_ = m.InProgress()
				}
			}
		}
	}()
}

// stop ends the keepalive.
func (h *handledState) stop() { h.stopOnce.Do(func() { close(h.stopCh) }) }

// simulateCrash drops what a dead session would lose: the held set and the
// keepalive. For tests of the redelivery path.
func (h *handledState) simulateCrash() {
	h.stop()
	h.mu.Lock()
	h.pending = map[string]*heldMsg{}
	h.mu.Unlock()
}

func (h *handledState) ack(ctx context.Context, m jetstream.Msg, tier string, seq uint64) error {
	if err := m.DoubleAck(ctx); err != nil {
		return err
	}
	if h.onAck != nil {
		h.onAck(tier, seq)
	}
	return nil
}

// settle decides what happens to a message the drain just pulled. It reports
// whether to return the message to the caller.
//
//   - No message_id: it cannot be keyed or marked. Rejected: a rejected line,
//     then Term. Never held, since a held message with MaxDeliver -1 redelivers
//     without bound.
//   - Its message_id is in the ledger with different stored bytes: a reuse by a
//     sender outside the shim. Rejected the same way; the original's record
//     stands.
//   - Its message_id is in the ledger (a redelivery after a crash between the
//     write and the ack): acked here, not returned, and dropped from the held
//     set if this session still held it.
//   - Already held by this session: the held message is replaced so the later
//     ack lands, and it is not returned again; it stays in read_unhandled.
//   - Otherwise it is held ack-pending and returned.
func (h *handledState) settle(ctx context.Context, m jetstream.Msg, tier string, e *Envelope, seq uint64) (bool, error) {
	stream := h.streamOf(tier)
	hash := envelopeHash(m.Data())
	if e.MessageID == "" {
		return false, h.reject(m, tier, stream, seq, hash, "", "no message_id")
	}
	folded, ferr := h.ledger.fold()
	if ferr == nil {
		if old, ok := folded.ByID[e.MessageID]; ok {
			if old.Hash != "" && old.Hash != hash {
				return false, h.reject(m, tier, stream, seq, hash, e.MessageID, "message_id already recorded with different bytes (a reuse); the original's record stands")
			}
			h.mu.Lock()
			delete(h.pending, e.MessageID)
			h.mu.Unlock()
			return false, h.ack(ctx, m, tier, seq)
		}
	}
	h.mu.Lock()
	if held, ok := h.pending[e.MessageID]; ok {
		held.msg = m
		h.mu.Unlock()
		return false, nil
	}
	h.pending[e.MessageID] = &heldMsg{msg: m, tier: tier, stream: stream, seq: seq, messageID: e.MessageID, hash: hash, sender: senderLabel(e), at: h.now()}
	h.mu.Unlock()
	// With the ledger unreadable, deliver rather than lose the message; the
	// message is held, so the later mark can still ack it.
	return true, ferr
}

// reject records a rejected line and Terms the message. If the line cannot be
// written the message is left pending, never Termed unrecorded.
func (h *handledState) reject(m jetstream.Msg, tier, stream string, seq uint64, hash, messageID, why string) error {
	ev := handledEvent{
		At: h.now().UTC().Format(time.RFC3339), Tier: tier, Stream: stream, Seq: seq,
		MessageID: messageID, Hash: hash, Disposition: dispRejected, Reason: why,
	}
	if _, err := h.ledger.record(ev); err != nil {
		return fmt.Errorf("rejecting %s sequence %d (%s): %w", tier, seq, why, err)
	}
	log.Printf("director-mcp: handled mode rejected %s sequence %d: %s", tier, seq, why)
	return m.Term()
}

// unhandled lists what this session holds and has not recorded, local first,
// oldest first.
func (h *handledState) unhandled() []unhandledRow {
	h.mu.Lock()
	rows := make([]unhandledRow, 0, len(h.pending))
	for _, v := range h.pending {
		rows = append(rows, unhandledRow{
			Tier: v.tier, Stream: v.stream, Seq: v.seq, MessageID: v.messageID, Sender: v.sender,
			Age: h.now().Sub(v.at).Round(time.Second).String(),
		})
	}
	h.mu.Unlock()
	sort.SliceStable(rows, func(i, j int) bool {
		if ri, rj := tierRank(rows[i].Tier), tierRank(rows[j].Tier); ri != rj {
			return ri < rj
		}
		return rows[i].Seq < rows[j].Seq
	})
	return rows
}

// rejected lists the ledger's rejected lines, local first, oldest first.
func (h *handledState) rejected() []handledEvent {
	f, err := h.ledger.fold()
	if err != nil {
		return nil
	}
	rows := make([]handledEvent, 0, len(f.Rejected))
	for _, e := range f.Rejected {
		rows = append(rows, e)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if ri, rj := tierRank(rows[i].Tier), tierRank(rows[j].Tier); ri != rj {
			return ri < rj
		}
		return rows[i].Seq < rows[j].Seq
	})
	return rows
}

// mark records a disposition: append, fsync, and only then ack. A failed append
// or fsync never acks; the error comes back and the message stays pending. A
// message this session does not hold is refused and nothing is written: after a
// restart the redelivery comes back to the new session, which then holds it.
func (h *handledState) mark(ctx context.Context, tier, messageID, disposition, forwardID, reason string) (map[string]any, error) {
	h.mu.Lock()
	held := h.pending[messageID]
	h.mu.Unlock()
	if held == nil {
		// Only a message this session holds can be recorded. A mark on an id it
		// has not been delivered would make that message vanish unread when it
		// arrives, and would skip the reuse check (no hash was recorded). After
		// a restart the redelivery comes back to the new session, which then
		// holds it and can mark it.
		return nil, fmt.Errorf("mark_handled: this session does not hold message_id %q (not delivered here and unrecorded, or already recorded); nothing was written. Read the message with wait_for_message, then mark it", messageID)
	}
	if held.tier != tier {
		return nil, fmt.Errorf("mark_handled: %s was delivered on the %s tier, not %s", messageID, held.tier, tier)
	}
	ev := handledEvent{
		At: h.now().UTC().Format(time.RFC3339), Tier: tier, Stream: h.streamOf(tier),
		MessageID: messageID, Disposition: disposition, ForwardID: forwardID, Reason: reason,
	}
	ev.Stream, ev.Seq, ev.Hash = held.stream, held.seq, held.hash
	wrote, err := h.ledger.record(ev)
	if err != nil {
		return nil, err // nothing acked: the message stays pending
	}
	out := map[string]any{
		"status": "recorded", "message_id": messageID, "tier": tier, "stream": ev.Stream,
		"disposition": disposition, "acked": false,
	}
	if ev.Seq != 0 {
		out["sequence"] = ev.Seq
	}
	if !wrote {
		out["status"] = "already_recorded"
	}
	h.mu.Lock()
	delete(h.pending, messageID)
	h.mu.Unlock()
	if err := h.ack(ctx, held.msg, tier, held.seq); err != nil {
		out["ack_warning"] = fmt.Sprintf("recorded, but the ack was not confirmed (%v); the message may be redelivered, and the drain acks it without returning it", err)
		return out, nil
	}
	out["acked"] = true
	return out, nil
}

// markArgs are the mark_handled tool arguments.
type markArgs struct {
	MessageID   string `json:"message_id"`
	Tier        string `json:"tier"`
	Disposition string `json:"disposition"`
	ForwardID   string `json:"forward_id"`
	Reason      string `json:"reason"`
}

func parseMarkArgs(raw json.RawMessage) (markArgs, error) {
	var a markArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return a, fmt.Errorf("mark_handled arguments: %w", err)
	}
	if a.MessageID == "" {
		return a, errors.New("mark_handled: message_id is required (the envelope's message_id)")
	}
	if a.Tier != "local" && a.Tier != "global" {
		return a, fmt.Errorf("mark_handled: tier must be local or global, got %q", a.Tier)
	}
	switch a.Disposition {
	case "handled":
	case "forwarded":
		if a.ForwardID == "" {
			return a, errors.New("mark_handled: forwarded needs forward_id, the message id of the forward")
		}
	case "parked":
		if a.Reason == "" {
			return a, errors.New("mark_handled: parked needs reason, one line on why it waits")
		}
	default:
		return a, fmt.Errorf("mark_handled: disposition must be handled, forwarded or parked, got %q", a.Disposition)
	}
	return a, nil
}

// streamFor names the stream behind a tier for this bus.
func (b *Bus) streamFor(tier string) string {
	if tier == "global" && b.globalCfg != nil {
		return b.globalCfg.streamName()
	}
	return "AGENT_INBOX"
}

// markHandled is the mark_handled tool's work.
func (b *Bus) markHandled(ctx context.Context, tier, messageID, disposition, detail string) (map[string]any, error) {
	if b.handled == nil {
		return nil, errors.New("mark_handled: the handled mode is off for this session (DIRECTOR_HANDLED is not 1), so messages are acked on read and there is nothing to record")
	}
	if tier == "global" && b.globalCfg == nil {
		return nil, errors.New("mark_handled: the global tier is off for this session")
	}
	var forwardID, reason string
	switch disposition {
	case "forwarded":
		forwardID = detail
	case "parked":
		reason = detail
	}
	return b.handled.mark(ctx, tier, messageID, disposition, forwardID, reason)
}

func toolMarkHandled(ctx context.Context, bus *Bus, raw json.RawMessage) (any, error) {
	a, err := parseMarkArgs(raw)
	if err != nil {
		return nil, err
	}
	detail := a.ForwardID
	if a.Disposition == "parked" {
		detail = a.Reason
	}
	return bus.markHandled(ctx, a.Tier, a.MessageID, a.Disposition, detail)
}

// toolCatalogHandled is toolCatalog plus the handled mode's changes: the
// mark_handled tool, and descriptions that say a read no longer acks.
func toolCatalogHandled(gcfg *globalConfig, cueOn, handledOn bool) []toolDef {
	tools := toolCatalog(gcfg, cueOn)
	if !handledOn {
		return tools
	}
	for i := range tools {
		switch tools[i].Name {
		case "wait_for_message":
			tools[i].Description += " The handled mode is on for this session: a read does NOT ack. Each message stays pending until you record it with mark_handled, using its message_id and the tier this result gives."
		case "inbox_summary":
			tools[i].Description += " The handled mode is on: messages you have read and not yet recorded are listed in their own read_unhandled section, oldest first with their age, and are not counted as waiting."
		}
	}
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	return append(tools, toolDef{
		Name:        "mark_handled",
		Description: "Record what you did with a message you read, then ack it. The record is written first and the ack second. handled: you acted. forwarded: you passed it on (give the forward's message_id). parked: it waits (give a one-line reason); a parked message is acked and stays visible through the ledger. Offered only when the handled mode is on.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message_id":  str("the envelope's message_id"),
				"tier":        str("local or global: the tier wait_for_message named"),
				"disposition": str("handled | forwarded | parked"),
				"forward_id":  str("for forwarded: the message_id of the forward"),
				"reason":      str("for parked: one line on why it waits"),
			},
			"required": []string{"message_id", "tier", "disposition"},
		},
	})
}
