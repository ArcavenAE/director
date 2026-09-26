package main

// Batch drain and unread summary (the turn-based inbox defect, 2026-09-24).
//
// wait_for_message used to return one message per call, so a session with a
// backlog spent one model turn per message and read fresh mail only after the
// whole backlog: about 30 roll-call replies held about 25 newer reports behind
// them for hours. Two tools answer the part the shim can answer without push:
//
//   - wait_for_message with max > 1 returns every waiting message up to max in
//     one call, oldest first. It consumes (acks) exactly as the single-message
//     form does.
//   - inbox_summary counts what is waiting, by sender and by performative,
//     flags what needs an answer, and lists the sequence numbers. It reads
//     through a throwaway ephemeral consumer and acks nothing, so the session's
//     own durable and its FIFO cursor are untouched.
//
// Receipt without polling (a notifier or doorbell) is out of scope here: MCP
// cannot push into a session's context. That half belongs to the MCP
// modernization and the leaf fabric (director#77 section 2.7).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

const (
	// maxDrain caps one batch. An envelope may be up to 64 KiB, and a batch
	// lands whole in the caller's context, so the cap bounds the worst case
	// at a few MiB rather than a whole backlog.
	maxDrain = 50
	// summaryDefault and summaryMax bound how many waiting messages one
	// summary reads. The totals past the cap still come from consumer state.
	summaryDefault = 200
	summaryMax     = 500
)

// drained is one message returned by a batch drain. Sequence is the stream
// sequence on its tier's stream; sequences are per stream, so they order
// messages within a tier and say nothing across tiers.
type drained struct {
	Env  *Envelope `json:"message"`
	Tier string    `json:"tier"`
	Seq  uint64    `json:"sequence"`
	// AckUnconfirmed marks a message whose confirmed ack failed. The server
	// may or may not have applied it, so the message is returned rather than
	// dropped, and may be delivered once more later.
	AckUnconfirmed bool `json:"ack_unconfirmed,omitempty"`
}

// tierRank puts local before global. The two tiers share no clock the shim
// can trust (sent_at is the sender's), so a batch is grouped by tier and each
// group is in stream order.
func tierRank(t string) int {
	if t == "global" {
		return 1
	}
	return 0
}

// orderDrained sorts a batch oldest first within each tier. A single durable
// delivers in stream order already; a redelivery can arrive out of place, and
// the sort puts it back.
func orderDrained(items []drained) {
	sort.SliceStable(items, func(i, j int) bool {
		if ri, rj := tierRank(items[i].Tier), tierRank(items[j].Tier); ri != rj {
			return ri < rj
		}
		return items[i].Seq < items[j].Seq
	})
}

// pullNoWait takes up to n messages that are already waiting on a durable,
// without blocking. Each decoded message is acked before it is returned, as
// the single-message poll acks. The ack here is confirmed by the server
// (DoubleAck), not fire-and-forget: a batch hands the caller many messages at
// once, and a lost ack would redeliver one later, out of order, after the
// caller had already read past it. An undecodable message is terminated and
// counted rather than returned as an error: failing the batch after acking the
// messages before it would lose them.
//
// A confirmed ack can itself fail, or time out after the server applied it.
// The shim prefers a possible duplicate to a silent loss: the message is
// returned flagged AckUnconfirmed, the rest of the batch is still acked (so it
// does not sit ack-pending and redeliver behind newer mail), and the failure
// comes back as an error beside the items for the caller to report.
func pullNoWait(ctx context.Context, cons jetstream.Consumer, n int, tier string) ([]drained, int, error) {
	if n <= 0 {
		return nil, 0, nil
	}
	batch, err := cons.FetchNoWait(n)
	if err != nil {
		return nil, 0, err
	}
	var out []drained
	var ackErrs []error
	discarded := 0
	for m := range batch.Messages() {
		var e Envelope
		if err := json.Unmarshal(m.Data(), &e); err != nil {
			_ = m.Term()
			discarded++
			continue
		}
		var seq uint64
		if md, err := m.Metadata(); err == nil {
			seq = md.Sequence.Stream
		}
		d := drained{Env: &e, Tier: tier, Seq: seq}
		if err := m.DoubleAck(ctx); err != nil {
			d.AckUnconfirmed = true
			ackErrs = append(ackErrs, fmt.Errorf("ack %s sequence %d: %w", tier, seq, err))
		}
		out = append(out, d)
	}
	if err := batch.Error(); err != nil && !errors.Is(err, jetstream.ErrNoMessages) {
		ackErrs = append(ackErrs, err)
	}
	return out, discarded, errors.Join(ackErrs...)
}

// drainAll pulls from one durable until it is empty or n messages are in
// hand. A poll can come back short (an undecodable message terminated, or a
// server batch boundary), so it loops rather than trusting one fetch.
func drainAll(pull func(int) ([]drained, int, error), n int) ([]drained, int, error) {
	var out []drained
	discarded := 0
	for len(out) < n {
		items, disc, err := pull(n - len(out))
		out = append(out, items...)
		discarded += disc
		if err != nil {
			return out, discarded, err
		}
		if len(items) == 0 && disc == 0 {
			break
		}
	}
	return out, discarded, nil
}

// drainNoWait is the global tier's pull, with the same one-time recreate the
// long poll does when the hub no longer has the durable (recover).
func (g *globalTier) drainNoWait(ctx context.Context, n int, agentID, instance string) ([]drained, int, error) {
	rebuilt := false
	out, disc, err := drainAll(func(k int) ([]drained, int, error) {
		out, disc, err := pullNoWait(ctx, g.consumer, k, "global")
		if len(out) == 0 && disc == 0 && !rebuilt {
			rebuilt = true
			var ok bool
			var rerr error
			if err != nil {
				ok, rerr = g.recover(ctx, err, agentID, instance)
			} else {
				ok, rerr = g.recheck(ctx, agentID, instance)
			}
			if rerr != nil {
				return nil, 0, rerr
			}
			if ok {
				return pullNoWait(ctx, g.consumer, k, "global")
			}
		}
		return out, disc, err
	}, n)
	for _, it := range out {
		g.noteAcked(it.Seq)
	}
	return out, disc, err
}

// joinWarn appends a second warning to a first, either of which may be empty.
func joinWarn(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "; " + b
}

// batchResult is one wait_for_message call with max > 1.
type batchResult struct {
	Items      []drained
	Discarded  int // undecodable messages terminated during the drain
	LocalWarn  string
	GlobalWarn string
}

// drainWaiting takes what is already waiting, up to n: the local inbox until
// it is empty, then the global one. It never blocks.
//
// A local failure is an error only while nothing has been consumed. Once any
// message is in hand (from this drain or an earlier step of the same call) it
// has been acked, so the failure becomes LocalWarn and the items are kept:
// failing the call would drop acked mail. The global tier already works this
// way through GlobalWarn.
func (b *Bus) drainWaiting(ctx context.Context, n int, res *batchResult) error {
	items, disc, err := drainAll(func(k int) ([]drained, int, error) {
		return pullNoWait(ctx, b.consumer, k, "local")
	}, n)
	res.Items = append(res.Items, items...)
	res.Discarded += disc
	if err != nil {
		if len(res.Items) == 0 {
			return err
		}
		res.LocalWarn = fmt.Sprintf("local inbox drain stopped early: %v", err)
		return nil
	}
	left := n - len(items)
	if b.globalCfg == nil || left <= 0 {
		return nil
	}
	g, err := b.globalReady(ctx)
	if err != nil {
		res.GlobalWarn = err.Error()
		return nil
	}
	gitems, gdisc, err := g.drainNoWait(ctx, left, b.self.AgentID, b.instance)
	res.Items = append(res.Items, gitems...)
	res.Discarded += gdisc
	if err != nil {
		res.GlobalWarn = joinWarn(res.GlobalWarn, fmt.Sprintf("global inbox drain failed: %v", err))
	}
	res.GlobalWarn = joinWarn(res.GlobalWarn, g.takeNotice())
	return nil
}

// receiveBatch returns up to max messages in one call. What is already
// waiting comes back at once. When nothing is waiting it blocks exactly as the
// single-message poll does, then tops the batch up with whatever else arrived.
func (b *Bus) receiveBatch(ctx context.Context, timeout time.Duration, max int) (batchResult, error) {
	var res batchResult
	if err := b.drainWaiting(ctx, max, &res); err != nil {
		return res, err
	}
	if len(res.Items) == 0 {
		first, err := b.receiveTiered(ctx, timeout)
		if err != nil && !errors.Is(err, jetstream.ErrNoMessages) {
			return res, err
		}
		res.GlobalWarn = joinWarn(res.GlobalWarn, first.GlobalWarn)
		if first.Env == nil {
			return res, nil
		}
		res.Items = append(res.Items, drained{Env: first.Env, Tier: first.Tier, Seq: first.Seq})
		if err := b.drainWaiting(ctx, max-1, &res); err != nil {
			return res, err
		}
	}
	orderDrained(res.Items)
	return res, nil
}

// remaining reports how many messages are still waiting after a drain, per
// tier, from consumer state. Best effort: a tier whose state cannot be read
// is left out rather than guessed.
func (b *Bus) remaining(ctx context.Context) map[string]uint64 {
	out := map[string]uint64{}
	if info, err := b.consumer.Info(ctx); err == nil {
		out["local"] = info.NumPending + uint64(info.NumAckPending)
	}
	if b.globalCfg != nil {
		b.globalMu.Lock()
		g := b.global
		b.globalMu.Unlock()
		if g != nil {
			if info, err := g.consumer.Info(ctx); err == nil {
				out["global"] = info.NumPending + uint64(info.NumAckPending)
			}
		}
	}
	return out
}

// ---- unread summary ----

// summaryItem is one waiting message as the summary sees it. Env is nil for a
// message the shim cannot decode; it is counted, never touched.
type summaryItem struct {
	Tier string
	Seq  uint64
	Env  *Envelope
}

// flagged is a waiting message that likely needs an answer.
type flagged struct {
	Tier         string   `json:"tier"`
	Sequence     uint64   `json:"sequence"`
	MessageID    string   `json:"message_id"`
	Sender       string   `json:"sender"`
	Performative string   `json:"performative"`
	Reasons      []string `json:"reasons"`
	Excerpt      string   `json:"excerpt"`
}

type inboxSummary struct {
	Total          int                 `json:"total"`
	BySender       map[string]int      `json:"by_sender"`
	ByPerformative map[string]int      `json:"by_performative"`
	Flagged        []flagged           `json:"flagged"`
	Sequences      map[string][]uint64 `json:"sequences"`
	Undecodable    int                 `json:"undecodable,omitempty"`
}

// answerPerformatives are the verbs that ask the reader for something.
var answerPerformatives = map[string]bool{"REQUEST": true, "FAILURE": true, "QUERY": true}

// custodyRE and awaitsRE are deliberately plain: they catch the phrasings the
// fleet actually uses ("holding for director instructions", "I hold custody of
// the findings") and err toward flagging. A false flag costs one read; a
// missed one is the defect this tool exists for.
var (
	custodyRE = regexp.MustCompile(`(?i)\bcustody\b`)
	awaitsRE  = regexp.MustCompile(`(?i)\b(await(s|ing)?|waiting (on|for)|holding for|blocked on|need(s)? (your|a) (reply|answer|decision|go-ahead))\b[^.\n]{0,40}\b(reply|replies|response|answer|instructions?|decision|go-ahead|director|you|operator)\b`)
)

func senderLabel(e *Envelope) string {
	id := e.Sender.AgentID
	if id == "" {
		id = "(unknown)"
	}
	if e.Sender.Workspace != "" {
		return id + "@" + e.Sender.Workspace
	}
	return id
}

func excerpt(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

// flagReasons says why a message likely needs an answer, or nothing.
func flagReasons(e *Envelope) []string {
	var why []string
	if answerPerformatives[e.Performative] {
		why = append(why, "performative "+e.Performative)
	}
	if e.ReplyBy != "" {
		why = append(why, "reply_by "+e.ReplyBy)
	}
	if custodyRE.MatchString(e.Content.Data) {
		why = append(why, "holds custody")
	}
	if awaitsRE.MatchString(e.Content.Data) {
		why = append(why, "awaits a reply")
	}
	return why
}

// summarize is the pure half of inbox_summary: counts, flags, and sequences
// from the waiting messages. Sequences come out oldest first per tier.
func summarize(items []summaryItem) inboxSummary {
	s := inboxSummary{
		BySender:       map[string]int{},
		ByPerformative: map[string]int{},
		Flagged:        []flagged{},
		Sequences:      map[string][]uint64{},
	}
	sort.SliceStable(items, func(i, j int) bool {
		if ri, rj := tierRank(items[i].Tier), tierRank(items[j].Tier); ri != rj {
			return ri < rj
		}
		return items[i].Seq < items[j].Seq
	})
	for _, it := range items {
		s.Total++
		s.Sequences[it.Tier] = append(s.Sequences[it.Tier], it.Seq)
		if it.Env == nil {
			s.Undecodable++
			continue
		}
		s.BySender[senderLabel(it.Env)]++
		s.ByPerformative[it.Env.Performative]++
		if why := flagReasons(it.Env); len(why) > 0 {
			s.Flagged = append(s.Flagged, flagged{
				Tier: it.Tier, Sequence: it.Seq, MessageID: it.Env.MessageID,
				Sender: senderLabel(it.Env), Performative: it.Env.Performative,
				Reasons: why, Excerpt: excerpt(it.Env.Content.Data, 160),
			})
		}
	}
	return s
}

// peekWaiting reads the messages waiting on a durable without consuming them.
// It takes the durable's filter and ack floor from its state, then reads the
// stream from just past the floor through a throwaway ephemeral pull consumer
// (no acks, memory storage, deleted afterwards). The durable itself is only
// inspected, so its FIFO cursor and its ack-pending set do not move. An
// ordered consumer is not used: its no-wait fetch starts over on every call,
// so a paging loop reads the same messages again.
//
// It reads to the end of the stream (bounded by limit), not to the durable's
// count of what is waiting. The waiting set is not always a prefix of the
// stream above the floor: a message acked out of order above the floor is
// consumed, and stopping at the count would list it and leave out the newest
// waiting mail. Reading to the end lists every waiting message; any excess
// over the count is reported as maybeConsumed, because the durable does not
// expose which listed messages those are.
func peekWaiting(ctx context.Context, js jetstream.JetStream, cons jetstream.Consumer, tier string, limit int) (items []summaryItem, expected uint64, maybeConsumed int, err error) {
	info, err := cons.Info(ctx)
	if err != nil {
		return nil, 0, 0, err
	}
	expected = info.NumPending + uint64(info.NumAckPending)
	if expected == 0 {
		return nil, 0, 0, nil
	}
	filters := info.Config.FilterSubjects
	if info.Config.FilterSubject != "" {
		filters = []string{info.Config.FilterSubject}
	}
	cfg := jetstream.ConsumerConfig{
		Name:              "peek_" + newInstanceID(),
		DeliverPolicy:     jetstream.DeliverByStartSequencePolicy,
		OptStartSeq:       info.AckFloor.Stream + 1,
		AckPolicy:         jetstream.AckNonePolicy,
		MemoryStorage:     true,
		InactiveThreshold: 30 * time.Second,
	}
	if len(filters) == 1 {
		cfg.FilterSubject = filters[0]
	} else {
		cfg.FilterSubjects = filters
	}
	oc, err := js.CreateConsumer(ctx, info.Stream, cfg)
	if err != nil {
		return nil, expected, 0, err
	}
	defer func() {
		dctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = js.DeleteConsumer(dctx, info.Stream, cfg.Name)
		cancel()
	}()
	var last uint64
	for len(items) < limit {
		batch, err := oc.FetchNoWait(limit - len(items))
		if err != nil {
			return items, expected, 0, err
		}
		added := 0
		for m := range batch.Messages() {
			var seq uint64
			if md, err := m.Metadata(); err == nil {
				seq = md.Sequence.Stream
			}
			if seq != 0 && seq <= last {
				continue // a repeat, never a new waiting message
			}
			last = seq
			var e Envelope
			it := summaryItem{Tier: tier, Seq: seq}
			if json.Unmarshal(m.Data(), &e) == nil {
				it.Env = &e
			}
			items = append(items, it)
			added++
		}
		if err := batch.Error(); err != nil && !errors.Is(err, jetstream.ErrNoMessages) {
			return items, expected, 0, err
		}
		if added == 0 {
			break // the end of the stream, or only repeats
		}
	}
	if uint64(len(items)) > expected {
		maybeConsumed = len(items) - int(expected)
	}
	return items, expected, maybeConsumed, nil
}

// summaryResult is inbox_summary's answer before shaping into JSON.
type summaryResult struct {
	Summary  inboxSummary
	Expected map[string]uint64
	Read     map[string]int
	// MaybeConsumed counts, per tier, listed messages beyond the durable's
	// own count of what is waiting: messages acked out of order above the
	// ack floor, which the summary cannot tell apart from waiting ones.
	MaybeConsumed map[string]int
	GlobalWarn    string
}

// summarizeInbox reads what is waiting on both tiers and summarizes it. It
// acks nothing.
func (b *Bus) summarizeInbox(ctx context.Context, limit int) (summaryResult, error) {
	res := summaryResult{Expected: map[string]uint64{}, Read: map[string]int{}, MaybeConsumed: map[string]int{}}
	items, exp, maybe, err := peekWaiting(ctx, b.js, b.consumer, "local", limit)
	if err != nil {
		return res, fmt.Errorf("local inbox summary: %w", err)
	}
	res.Expected["local"], res.Read["local"] = exp, len(items)
	if maybe > 0 {
		res.MaybeConsumed["local"] = maybe
	}
	all := items
	if b.globalCfg != nil {
		g, gerr := b.globalReady(ctx)
		if gerr != nil {
			res.GlobalWarn = gerr.Error()
		} else {
			gitems, gexp, gmaybe, gerr := peekWaiting(ctx, g.js, g.consumer, "global", limit)
			if gerr != nil {
				ok, rerr := g.recover(ctx, gerr, b.self.AgentID, b.instance)
				if rerr != nil {
					gerr = rerr
				} else if ok {
					gitems, gexp, gmaybe, gerr = peekWaiting(ctx, g.js, g.consumer, "global", limit)
				}
			}
			if gerr != nil {
				res.GlobalWarn = fmt.Sprintf("global inbox summary failed: %v", gerr)
			}
			res.GlobalWarn = joinWarn(res.GlobalWarn, g.takeNotice())
			res.Expected["global"], res.Read["global"] = gexp, len(gitems)
			if gmaybe > 0 {
				res.MaybeConsumed["global"] = gmaybe
			}
			all = append(all, gitems...)
		}
	}
	res.Summary = summarize(all)
	return res, nil
}
