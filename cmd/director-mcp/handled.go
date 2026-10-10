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
// only, folded on read by tier, stream and sequence, last event wins, one flock
// around fold, check and append. That is the shape of dws (skills/director/
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

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("threshold table: %s = %q is not a positive duration", k, v)
		}
		out[k] = d
	}
	return out, nil
}

// handledKey names one inbound message. The stream is part of the key because
// sequences are per stream, and the tier because two tiers share no sequence.
type handledKey struct {
	Tier   string
	Stream string
	Seq    uint64
}

// handledEvent is one ledger line.
type handledEvent struct {
	At          string `json:"at"`
	Tier        string `json:"tier"`
	Stream      string `json:"stream"`
	Seq         uint64 `json:"sequence"`
	MessageID   string `json:"message_id,omitempty"`
	Disposition string `json:"disposition"`
	ForwardID   string `json:"forward_id,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

func (e handledEvent) key() handledKey { return handledKey{e.Tier, e.Stream, e.Seq} }

type handledLedger struct{ path, lock string }

func newHandledLedger(dir string) *handledLedger {
	return &handledLedger{path: filepath.Join(dir, "handled.jsonl"), lock: filepath.Join(dir, "handled.lock")}
}

// foldLines folds the ledger bytes: last event per key wins. A line that is not
// whole JSON (a torn last line) is skipped, not fatal.
func foldLines(b []byte) map[handledKey]handledEvent {
	out := map[handledKey]handledEvent{}
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
		if json.Unmarshal(line, &e) != nil || e.Tier == "" || e.Seq == 0 || e.Disposition == "" {
			continue
		}
		out[e.key()] = e
	}
	return out
}

func (l *handledLedger) fold() (map[handledKey]handledEvent, error) {
	b, err := os.ReadFile(l.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[handledKey]handledEvent{}, nil
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

// record appends ev under the lock, after folding and checking. It reports
// false, and writes nothing, when the ledger already holds this disposition for
// the key, so a redelivery after a crash adds no second record.
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
	if old, ok := cur[ev.key()]; ok && old.Disposition == ev.Disposition && old.ForwardID == ev.ForwardID && old.Reason == ev.Reason {
		return false, nil
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
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return false, fmt.Errorf("handled ledger: %w", err)
	}
	return true, f.Close()
}

// heldMsg is a delivered, unacked message this session holds.
type heldMsg struct {
	msg       jetstream.Msg
	messageID string
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
	pending map[handledKey]*heldMsg
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
		pending: map[handledKey]*heldMsg{},
	}, nil
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
//   - Already in the ledger: a redelivery after a crash between the ledger
//     write and the ack. It is acked here and not returned (and dropped from
//     the held set if this session still held it).
//   - Already held by this session: a redelivery after the ack wait. The held
//     message is replaced so the later ack lands, and it is not returned again;
//     it stays visible in the read-unhandled section.
//   - Otherwise it is held ack-pending and returned.
func (h *handledState) settle(ctx context.Context, m jetstream.Msg, tier string, e *Envelope, seq uint64) (bool, error) {
	key := handledKey{tier, h.streamOf(tier), seq}
	folded, ferr := h.ledger.fold()
	if ferr == nil {
		if _, ok := folded[key]; ok {
			h.mu.Lock()
			delete(h.pending, key)
			h.mu.Unlock()
			return false, h.ack(ctx, m, tier, seq)
		}
	}
	h.mu.Lock()
	if held, ok := h.pending[key]; ok {
		held.msg = m
		h.mu.Unlock()
		return false, nil
	}
	h.mu.Unlock()
	// With the ledger unreadable, deliver rather than lose the message, and hold
	// it so the later mark can still ack it.
	h.hold(key, m, e)
	return true, ferr
}

func (h *handledState) hold(key handledKey, m jetstream.Msg, e *Envelope) {
	h.mu.Lock()
	h.pending[key] = &heldMsg{msg: m, messageID: e.MessageID, sender: senderLabel(e), at: h.now()}
	h.mu.Unlock()
}

// unhandled lists what this session holds and has not recorded, local first,
// oldest first.
func (h *handledState) unhandled() []unhandledRow {
	h.mu.Lock()
	rows := make([]unhandledRow, 0, len(h.pending))
	for k, v := range h.pending {
		rows = append(rows, unhandledRow{
			Tier: k.Tier, Stream: k.Stream, Seq: k.Seq, MessageID: v.messageID, Sender: v.sender,
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

// mark records a disposition, ledger first and ack second. A held message is
// acked after the write; one this session does not hold (a restart lost it)
// is recorded and left for its redelivery to be acked by the drain.
func (h *handledState) mark(ctx context.Context, tier string, seq uint64, disposition, forwardID, reason string) (map[string]any, error) {
	stream := h.streamOf(tier)
	key := handledKey{tier, stream, seq}
	h.mu.Lock()
	held := h.pending[key]
	h.mu.Unlock()
	ev := handledEvent{
		At: h.now().UTC().Format(time.RFC3339), Tier: tier, Stream: stream, Seq: seq,
		Disposition: disposition, ForwardID: forwardID, Reason: reason,
	}
	if held != nil {
		ev.MessageID = held.messageID
	}
	wrote, err := h.ledger.record(ev)
	if err != nil {
		return nil, err // nothing written, nothing acked: the message stays pending
	}
	out := map[string]any{
		"status": "recorded", "tier": tier, "stream": stream, "sequence": seq,
		"disposition": disposition, "acked": false,
	}
	if !wrote {
		out["status"] = "already_recorded"
	}
	if held == nil {
		out["note"] = "this session does not hold that message; it is recorded, and the drain acks it if it is redelivered"
		return out, nil
	}
	h.mu.Lock()
	delete(h.pending, key)
	h.mu.Unlock()
	if err := h.ack(ctx, held.msg, tier, seq); err != nil {
		out["ack_warning"] = fmt.Sprintf("recorded, but the ack was not confirmed (%v); the message may be redelivered, and the drain acks it without returning it", err)
		return out, nil
	}
	out["acked"] = true
	return out, nil
}

// markArgs are the mark_handled tool arguments.
type markArgs struct {
	Tier        string `json:"tier"`
	Seq         uint64 `json:"sequence"`
	Disposition string `json:"disposition"`
	ForwardID   string `json:"forward_id"`
	Reason      string `json:"reason"`
}

func parseMarkArgs(raw json.RawMessage) (markArgs, error) {
	var a markArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return a, fmt.Errorf("mark_handled arguments: %w", err)
	}
	if a.Tier != "local" && a.Tier != "global" {
		return a, fmt.Errorf("mark_handled: tier must be local or global, got %q", a.Tier)
	}
	if a.Seq == 0 {
		return a, errors.New("mark_handled: sequence is required (the sequence wait_for_message returned)")
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
func (b *Bus) markHandled(ctx context.Context, tier string, seq uint64, disposition, detail string) (map[string]any, error) {
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
	return b.handled.mark(ctx, tier, seq, disposition, forwardID, reason)
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
	return bus.markHandled(ctx, a.Tier, a.Seq, a.Disposition, detail)
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
			tools[i].Description += " The handled mode is on for this session: a read does NOT ack. Each message stays pending until you record it with mark_handled, using the tier and sequence this result gives."
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
				"tier":        str("local or global: the tier wait_for_message named"),
				"sequence":    map[string]any{"type": "integer", "description": "the stream sequence wait_for_message returned"},
				"disposition": str("handled | forwarded | parked"),
				"forward_id":  str("for forwarded: the message_id of the forward"),
				"reason":      str("for parked: one line on why it waits"),
			},
			"required": []string{"tier", "sequence", "disposition"},
		},
	})
}
