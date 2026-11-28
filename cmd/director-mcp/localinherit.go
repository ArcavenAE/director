package main

// A successor inherits its predecessor's unread local mail (director#220,
// sim/design/asks-die-unread.md section 3, part U1). A seat that is replaced
// leaves mail on its instance subject that no other session reads, and the
// stream drops it after max_age. The shim reads an optional lineage input, and
// once the predecessor is gone it creates one more durable on the predecessor's
// instance subject, starting after the predecessor's own ack floor.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// predecessorFromEnv is the lineage input and where it came from. Director's
// ruling reads DIRECTOR_PREDECESSOR first and MARVEL_PREDECESSOR, which marvel
// sets to "<workspace>/<name>" for a seat a shift or a wake started, as the
// fallback. One function serves the global tier, the local tier and the dossier,
// so they cannot disagree about who the predecessor is.
func predecessorFromEnv() (link, source string) {
	if v := os.Getenv("DIRECTOR_PREDECESSOR"); v != "" {
		return v, "env DIRECTOR_PREDECESSOR"
	}
	if v := os.Getenv("MARVEL_PREDECESSOR"); v != "" {
		return v, "env MARVEL_PREDECESSOR"
	}
	return "", ""
}

// predecessorName is the name after the last "/" of the lineage input, as
// given: empty when there is no input or the value has no name.
func predecessorName() string {
	link, _ := predecessorFromEnv()
	return link[strings.LastIndex(link, "/")+1:]
}

// predecessorID is the agent id of the session this one replaced. It is empty
// when the lineage input has no name, when the name is this seat's own id (a
// restart, which the ordinary lookup already covers), or when the name is not a
// valid identity, since it is built into a subject and a durable filter.
func predecessorID(self string) string {
	name := predecessorName()
	if name == "" || name == self || validToken("predecessor", name) != nil {
		return ""
	}
	return name
}

// lineageRead is what one read of the predecessor's presence says.
type lineageRead int

const (
	readLive    lineageRead = iota // a row exists: the predecessor is live
	readGone                       // the read succeeded and found no row
	readUnknown                    // the read failed: unknown, never "gone"
)

// lineageEmptyReads is how many successful empty reads in a row start the
// inheritance. At the 30 s heartbeat that is 60 to 90 seconds, longer than a
// shim restarting in place takes to write its new row.
const lineageEmptyReads = 3

// lineageCounter counts successful empty reads in a row. A live row or a failed
// read resets it, so empty, empty, failed, empty does not trigger.
type lineageCounter struct{ empties int }

// observe records one read and reports whether the run has reached the trigger.
func (c *lineageCounter) observe(r lineageRead) bool {
	if r != readGone {
		c.empties = 0
		return false
	}
	c.empties++
	return c.empties >= lineageEmptyReads
}

// inheritMaxAge is how long an arm lasts when the stream's max age cannot be
// read: the 72h of AGENT_INBOX.
const inheritMaxAge = 72 * time.Hour

// inheritState is the local tier's lineage state. pred empty means no lineage:
// nothing is read and nothing waits.
type inheritState struct {
	mu      sync.Mutex
	pred    string
	armedAt time.Time
	maxAge  time.Duration
	counter lineageCounter
	cons    jetstream.Consumer // set once the inherited durable exists
	durable string
	ended   string // why it ended, when it has
}

// armInherit sets the lineage at connect, from the input and never from timing.
// The predecessor of a shift is usually still live here, so it only arms.
func (b *Bus) armInherit(ctx context.Context) {
	pred := predecessorID(b.self.AgentID)
	if pred == "" {
		return
	}
	age := inheritMaxAge
	if st, err := b.js.Stream(ctx, "AGENT_INBOX"); err == nil {
		if ma := st.CachedInfo().Config.MaxAge; ma > 0 {
			age = ma
		}
	}
	b.inh.mu.Lock()
	b.inh.pred, b.inh.armedAt, b.inh.maxAge = pred, time.Now(), age
	b.inh.mu.Unlock()
}

// predecessorPresence reads whether the predecessor has a live presence row.
func (b *Bus) predecessorPresence(ctx context.Context, pred string) lineageRead {
	keys, err := b.kv.Keys(ctx)
	if err != nil {
		if errors.Is(err, jetstream.ErrNoKeysFound) {
			return readGone
		}
		return readUnknown
	}
	prefix := "presence." + b.self.Team + "." + pred + "."
	for _, k := range keys {
		if strings.HasPrefix(k, prefix) {
			return readLive
		}
	}
	return readGone
}

// inheritTick runs on the heartbeat. Armed and not started, it reads the
// predecessor's presence and starts the inheritance on the third successful
// empty read. Started, it ends the inheritance when the predecessor's row comes
// back, when the durable has drained with the predecessor still absent, or when
// the arm has run out.
func (b *Bus) inheritTick(ctx context.Context, logf func(string, ...any)) {
	b.inh.mu.Lock()
	pred, armed, maxAge, ended := b.inh.pred, b.inh.armedAt, b.inh.maxAge, b.inh.ended
	started := b.inh.cons != nil
	b.inh.mu.Unlock()
	if pred == "" || ended != "" {
		return
	}
	if time.Since(armed) > maxAge {
		b.endInherit(ctx, "the arm ran out at the stream's max age", logf)
		return
	}
	read := b.predecessorPresence(ctx, pred)
	if started {
		switch read {
		case readLive:
			b.endInherit(ctx, fmt.Sprintf("predecessor %s is live again, so two readers of one mailbox would collide", pred), logf)
		case readGone:
			if b.inheritDrained(ctx) {
				b.endInherit(ctx, "the inherited durable drained with the predecessor still absent", logf)
			}
		}
		return
	}
	b.inh.mu.Lock()
	fire := b.inh.counter.observe(read)
	b.inh.mu.Unlock()
	if !fire {
		return
	}
	if err := b.startInherit(ctx, pred); err != nil {
		// Not started: the count stays at the trigger, so the next tick tries
		// again. A failure is logged once per change by the caller's logf.
		if logf != nil {
			logf("inherit: starting the inherited durable for %s failed: %v", pred, err)
		}
	}
}

// seatDurableInstance parses a seat durable's name against a prefix such as
// "mcp_<id>_". After the prefix comes a 26-character ULID and an optional
// known suffix. Anything else, including another id that merely starts with this
// one's name and an underscore, is not this seat's durable.
func seatDurableInstance(name, prefix string) (string, bool) {
	rest, ok := strings.CutPrefix(name, prefix)
	if !ok {
		return "", false
	}
	for _, suf := range []string{"-role", "-inherit"} {
		if r, cut := strings.CutSuffix(rest, suf); cut {
			rest = r
			break
		}
	}
	if len(rest) != 26 {
		return "", false
	}
	for _, c := range rest {
		if !strings.ContainsRune("0123456789ABCDEFGHJKMNPQRSTVWXYZ", c) {
			return "", false
		}
	}
	return rest, true
}

func containsSubject(set []string, subject string) bool {
	for _, s := range set {
		if s == subject {
			return true
		}
	}
	return false
}

// subjectFloor is the highest ack floor among one id's departed seat durables
// whose filter set contains the subject, by exact string membership. A durable
// that also filters a role subject still counts for the instance subject it
// reads: its floor is the stream sequence at or below which it acked everything
// it reads. found is false when no such durable exists, so a floor of 0 can be
// told from no floor.
func subjectFloor(ctx context.Context, js jetstream.JetStream, stream, prefix, subject string, live func(instance string) bool) (floor uint64, found bool, err error) {
	st, err := js.Stream(ctx, stream)
	if err != nil {
		return 0, false, err
	}
	lister := st.ListConsumers(ctx)
	for info := range lister.Info() {
		inst, ok := seatDurableInstance(info.Name, prefix)
		if !ok || !containsSubject(filterSubjects(info.Config), subject) {
			continue
		}
		if live != nil && live(inst) {
			continue
		}
		found = true
		if info.AckFloor.Stream > floor {
			floor = info.AckFloor.Stream
		}
	}
	return floor, found, lister.Err()
}

// startInherit creates the inherited durable on the predecessor's instance
// subject, after the predecessor's floor, or from the start of what the stream
// holds when it left no durable.
func (b *Bus) startInherit(ctx context.Context, pred string) error {
	subject := b.inboxSubject(b.self.Workspace, b.self.Team, pred)
	livePred := func(instance string) bool {
		_, err := b.kv.Get(ctx, "presence."+b.self.Team+"."+pred+"."+instance)
		return err == nil
	}
	floor, found, ferr := subjectFloor(ctx, b.js, "AGENT_INBOX", "mcp_"+pred+"_", subject, livePred)
	if ferr != nil && !found {
		return fmt.Errorf("reading %s's durables: %w", pred, ferr)
	}
	cfg := jetstream.ConsumerConfig{
		Durable:           "mcp_" + b.self.AgentID + "_" + b.instance + "-inherit",
		FilterSubject:     subject,
		AckPolicy:         jetstream.AckExplicitPolicy,
		DeliverPolicy:     jetstream.DeliverAllPolicy,
		MaxDeliver:        -1,
		InactiveThreshold: localConsumerInactive,
	}
	if b.handled != nil {
		cfg.AckWait = b.handled.ackWait
	}
	if floor > 0 {
		cfg.DeliverPolicy = jetstream.DeliverByStartSequencePolicy
		cfg.OptStartSeq = floor + 1
	}
	cons, err := b.js.CreateOrUpdateConsumer(ctx, "AGENT_INBOX", cfg)
	if err != nil {
		return err
	}
	var waiting uint64
	if info := cons.CachedInfo(); info != nil {
		waiting = info.NumPending
	}
	var msg string
	switch {
	case floor > 0:
		msg = fmt.Sprintf("inherited the unread local mail of predecessor %s, after its last ack at stream sequence %d; %d message(s) waiting, each marked inherited_from. Check %s's handoff and replies before acting on them: delivery is at least once", pred, floor, waiting, pred)
	case found:
		msg = fmt.Sprintf("inherited the local mail of predecessor %s, which acked nothing, from the start of what the stream holds; %d message(s) waiting, each marked inherited_from. Check %s's handoff and replies before acting on them: delivery is at least once", pred, waiting, pred)
	default:
		msg = fmt.Sprintf("inherited the local mail of predecessor %s, which left no durable, from the start of what the stream holds; %d message(s) waiting, each marked inherited_from. Check %s's handoff and replies before acting on them: delivery is at least once", pred, waiting, pred)
	}
	b.inh.mu.Lock()
	b.inh.cons, b.inh.durable = cons, cfg.Durable
	b.inh.mu.Unlock()
	b.resumedMu.Lock()
	b.resumed = append(b.resumed, msg)
	b.resumedMu.Unlock()
	return nil
}

// inheritCons returns the inherited consumer, nil before it starts and after it
// ends, and the predecessor it names.
func (b *Bus) inheritCons() (jetstream.Consumer, string) {
	b.inh.mu.Lock()
	defer b.inh.mu.Unlock()
	return b.inh.cons, b.inh.pred
}

// inheritDrained reports whether the inherited durable holds nothing: no mail
// waiting and none fetched and not acked.
func (b *Bus) inheritDrained(ctx context.Context) bool {
	cons, _ := b.inheritCons()
	if cons == nil {
		return false
	}
	info, err := cons.Info(ctx)
	if err != nil {
		return false
	}
	return info.NumPending == 0 && info.NumAckPending == 0
}

// endInherit deletes the inherited durable and says why in the next resume note.
// It runs once: a later tick sees ended and does nothing.
func (b *Bus) endInherit(ctx context.Context, why string, logf func(string, ...any)) {
	b.inh.mu.Lock()
	if b.inh.ended != "" {
		b.inh.mu.Unlock()
		return
	}
	durable, pred := b.inh.durable, b.inh.pred
	b.inh.ended, b.inh.cons = why, nil
	b.inh.mu.Unlock()
	// The note goes first: a reader that sees the durable gone has the reason.
	b.resumedMu.Lock()
	b.resumed = append(b.resumed, fmt.Sprintf("the inherited mail of predecessor %s ended: %s", pred, why))
	b.resumedMu.Unlock()
	if durable != "" {
		if err := b.js.DeleteConsumer(ctx, "AGENT_INBOX", durable); err != nil && !errors.Is(err, jetstream.ErrConsumerNotFound) && logf != nil {
			logf("inherit: deleting %s failed: %v", durable, err)
		}
	}
}

// inheritedOne takes one waiting inherited message without blocking, for the
// single-message wait. A failure is returned as a warning, never as an error: the
// seat's own wait goes on.
func (b *Bus) inheritedOne(ctx context.Context) (*drained, string) {
	var res batchResult
	b.drainInherited(ctx, 1, &res)
	if len(res.Items) == 0 {
		return nil, res.LocalWarn
	}
	return &res.Items[0], res.LocalWarn
}

// ownWaiting takes one message of the seat's own that is already waiting,
// without blocking: local first, then the global tier when it is attached.
func (b *Bus) ownWaiting(ctx context.Context, res *pollResult) *drained {
	if b.globalCfg != nil {
		var notices string
		w := b.takeWaiting(ctx, res, &notices)
		res.GlobalWarn = joinWarn(res.GlobalWarn, notices)
		return w
	}
	if items, _, _ := b.pullLocalNoWait(ctx, 1); len(items) > 0 {
		return &items[0]
	}
	return nil
}

// drainInherited takes up to n waiting messages from the inherited durable into
// res, each marked with the predecessor. It never fails the call: the seat's own
// mail is already in hand, so a failure here becomes a warning.
func (b *Bus) drainInherited(ctx context.Context, n int, res *batchResult) {
	cons, pred := b.inheritCons()
	if cons == nil || n <= 0 {
		return
	}
	// Noted before settle can ack: in the handled mode a message already in the
	// ledger is acked on the pull and never returned, so noting only returned
	// items would let that ack credit the own counter (director#684, review of
	// #688). The pull's drop hook runs first and drops nothing.
	noteInherited := func(_ context.Context, _ jetstream.Msg, _ *Envelope, seq uint64) bool {
		if b.handled != nil || ackLaterFrom(ctx) != nil {
			b.noteInheritedDelivered(seq) // acked later by sequence alone
		}
		return false
	}
	items, disc, err := drainAll(func(k int) ([]drained, int, error) {
		return pullNoWait(ctx, cons, k, "local", b.handled, noteInherited)
	}, n)
	for i := range items {
		items[i].InheritedFrom = pred
	}
	res.Items = append(res.Items, items...)
	res.Discarded += disc
	if err != nil {
		res.LocalWarn = joinWarn(res.LocalWarn, fmt.Sprintf("inherited inbox drain stopped early: %v", err))
	}
}

// anySeatDurable parses a seat durable's name for its instance without knowing
// the agent id: it is `mcp_<id>_<ULID>` with an optional -role or -inherit
// suffix, so the ULID is the 26 characters at the right, preceded by `_`.
func anySeatDurable(name string) (string, bool) {
	rest, ok := strings.CutPrefix(name, "mcp_")
	if !ok {
		return "", false
	}
	for _, suf := range []string{"-role", "-inherit"} {
		if r, cut := strings.CutSuffix(rest, suf); cut {
			rest = r
			break
		}
	}
	if len(rest) < 27 || rest[len(rest)-27] != '_' {
		return "", false
	}
	inst := rest[len(rest)-26:]
	for _, c := range inst {
		if !strings.ContainsRune("0123456789ABCDEFGHJKMNPQRSTVWXYZ", c) {
			return "", false
		}
	}
	return inst, true
}

// roleSubjectFloor is the highest ack floor among every seat durable of any id,
// live or departed, whose filter set contains the role subject: the sequence at
// or below which some holder of the role acked everything it reads. A new
// holder starts after it, so mail a holder already read is not replayed. found
// is false when no durable ever filtered the subject, so the first holder reads
// everything the stream holds.
func roleSubjectFloor(ctx context.Context, js jetstream.JetStream, stream, subject string) (floor uint64, found bool, err error) {
	st, err := js.Stream(ctx, stream)
	if err != nil {
		return 0, false, err
	}
	lister := st.ListConsumers(ctx)
	for info := range lister.Info() {
		if _, ok := anySeatDurable(info.Name); !ok || !containsSubject(filterSubjects(info.Config), subject) {
			continue
		}
		found = true
		if info.AckFloor.Stream > floor {
			floor = info.AckFloor.Stream
		}
	}
	return floor, found, lister.Err()
}

// ownSubjectFloor is the floor a restarting seat's own-subject durable starts
// after. The base is subjectFloor: the highest floor among this id's departed
// durables that read the subject. A seat that read its role subject too moved
// on past that: its role durable's floor is further along, and a departed
// combined durable (own and role subjects in one) carried a single floor for both.
// So the base is raised to the highest floor of any of this id's departed
// durables, but only when no message on the own subject sits between the two,
// so no unread own mail is skipped. The check is the stream's next message on
// the subject after the base.
func ownSubjectFloor(ctx context.Context, js jetstream.JetStream, stream, prefix, subject string, live func(instance string) bool) (uint64, error) {
	st, err := js.Stream(ctx, stream)
	if err != nil {
		return 0, err
	}
	var base, reach uint64
	lister := st.ListConsumers(ctx)
	for info := range lister.Info() {
		inst, ok := seatDurableInstance(info.Name, prefix)
		if !ok || (live != nil && live(inst)) {
			continue
		}
		reach = max(reach, info.AckFloor.Stream)
		if containsSubject(filterSubjects(info.Config), subject) {
			base = max(base, info.AckFloor.Stream)
		}
	}
	if err := lister.Err(); err != nil {
		return base, err
	}
	if reach <= base {
		return base, nil
	}
	next, err := st.GetMsg(ctx, base+1, jetstream.WithGetMsgSubject(subject))
	if err != nil && !errors.Is(err, jetstream.ErrMsgNotFound) {
		return base, err
	}
	if err == nil && next.Sequence <= reach {
		return base, nil // own mail sits in the gap: it is unread, so start at the base
	}
	return reach, nil
}
