package main

// Recovery of a deleted local durable (director#269). The server deletes a
// durable that sat idle past its inactive threshold, and an operator may delete
// one, while the session is running. The local tier created its durable once at
// connect and never looked again, so the next summary failed with 404 consumer
// not found and the long poll with no responders, for as long as the session
// lived. The global tier already asks the hub whether its durable exists and
// recreates it (global.go restore); this is the same step for the local one.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// localState is what the local tier needs to rebuild its durable: the config it
// attached with, the highest stream sequence this session has acked, and when
// it last asked the broker whether the durable still exists.
type localState struct {
	cfg     jetstream.ConsumerConfig // without a start sequence
	acked   uint64
	checked time.Time
	// rebuilds counts completed rebuilds, so a restore that waited for another
	// one can tell the durable was rebuilt while it waited.
	rebuilds uint64
}

// cons returns the current local consumer. A rebuild replaces it, so readers on
// other goroutines (the cue watcher) take it here and not from the field.
func (b *Bus) cons() jetstream.Consumer {
	b.localMu.Lock()
	defer b.localMu.Unlock()
	return b.consumer
}

// consOf returns the own-subject consumer, or the role-subject one when role is
// true (nil for a seat with no role).
func (b *Bus) consOf(role bool) jetstream.Consumer {
	if role {
		b.localMu.Lock()
		defer b.localMu.Unlock()
		return b.roleCons
	}
	return b.cons()
}

// localConsumers lists every local consumer this session reads: its own
// subject's, then its role subject's when it holds a role. Every read that sums
// or peeks the local mail goes through it, so a role holder's two durables read
// as one source (director#220).
func (b *Bus) localConsumers() []jetstream.Consumer {
	b.localMu.Lock()
	defer b.localMu.Unlock()
	out := []jetstream.Consumer{b.consumer}
	if b.roleCons != nil {
		out = append(out, b.roleCons)
	}
	return out
}

// stateOf is the rebuild state of one local durable. The caller holds localMu.
func (b *Bus) stateOf(role bool) *localState {
	if role {
		return &b.roleLocal
	}
	return &b.local
}

// durableOf is the durable's name: the own one, or the same with "-role".
func (b *Bus) durableOf(role bool) string {
	d := "mcp_" + b.self.AgentID + "_" + b.instance
	if role {
		d += "-role"
	}
	return d
}

// noteRoleDelivered records the stream sequence of a role-subject message that
// was fetched and not yet acked. The ack is noted later by sequence alone (the
// reply path and the handled mode carry no subject), and this tells noteLocalAcked
// which durable's acked counter the sequence belongs to: the two durables'
// sequences interleave, so one shared maximum would make a rebuilt own durable
// skip own mail below a role sequence.
func (b *Bus) noteRoleDelivered(seq uint64) {
	b.localMu.Lock()
	if b.roleSeqs == nil {
		b.roleSeqs = map[uint64]struct{}{}
	}
	b.roleSeqs[seq] = struct{}{}
	b.localMu.Unlock()
}

// noteLocalAcked records an acked local stream sequence.
func (b *Bus) noteLocalAcked(seq uint64) {
	b.localMu.Lock()
	_, isRole := b.roleSeqs[seq]
	delete(b.roleSeqs, seq)
	st := b.stateOf(isRole)
	if seq > st.acked {
		st.acked = seq
	}
	b.localMu.Unlock()
}

// noteAckedOn records an ack made at the fetch itself, where the durable is known.
func (b *Bus) noteAckedOn(role bool, seq uint64) {
	b.localMu.Lock()
	st := b.stateOf(role)
	if seq > st.acked {
		st.acked = seq
	}
	b.localMu.Unlock()
}

// localRestartSeq is where a rebuilt durable starts: after the highest sequence
// this session acked, so mail already read does not replay and mail sent while
// the durable was gone is delivered. A session that acked nothing, and attached
// with no seat floor, starts from the beginning of the inbox. With the handled
// mode on, a message held ack-pending can sit below a later one acked out of
// order, so the start is the lowest held sequence when that is lower: the new
// durable redelivers it, the handled state replaces its dead ack handle, and
// mark_handled still acks it.
func (b *Bus) localRestartSeq(role bool) (start uint64, acked uint64) {
	b.localMu.Lock()
	acked = b.stateOf(role).acked
	b.localMu.Unlock()
	if acked > 0 {
		start = acked + 1
	}
	// lowestPending is per tier, so it speaks for the own durable only; the role
	// durable's handled-mode rebuild resumes after its acks (a stated limit).
	if b.handled != nil && !role {
		if low := b.handled.lowestPending("local"); low > 0 && (start == 0 || low < start) {
			start = low
		}
	}
	return start, acked
}

// recoverLocal is called after a local pull or summary error. When the error
// can mean the durable is gone (possiblyLost) it asks the broker, and rebuilds
// the durable if the broker says it is not there. It returns true when it
// rebuilt, so the caller retries. Any other answer, including a broker that
// cannot be asked, returns false and the caller reports the original error.
func (b *Bus) recoverLocal(ctx context.Context, cause error) (bool, error) {
	return b.recoverLocalOn(ctx, false, cause)
}

func (b *Bus) recoverLocalOn(ctx context.Context, role bool, cause error) (bool, error) {
	if !possiblyLost(cause) {
		return false, nil
	}
	return b.restoreDurable(ctx, role)
}

// recheckLocal is recoverLocal for an empty pull: a pull on a durable the
// broker no longer has can look like an empty inbox, so an empty pull asks, at
// most once per durableCheckEvery.
func (b *Bus) recheckLocal(ctx context.Context) (bool, error) {
	return b.recheckLocalOn(ctx, false)
}

func (b *Bus) recheckLocalOn(ctx context.Context, role bool) (bool, error) {
	b.localMu.Lock()
	due := time.Since(b.stateOf(role).checked) >= durableCheckEvery
	b.localMu.Unlock()
	if !due {
		return false, nil
	}
	return b.restoreDurable(ctx, role)
}

// lookupLocal asks the broker whether the durable exists. b.lookup replaces the
// call in a test, so a test can count the calls and make one fail.
func (b *Bus) lookupLocal(ctx context.Context, durable string) error {
	if b.lookup != nil {
		return b.lookup(ctx, durable)
	}
	_, err := b.js.Consumer(ctx, "AGENT_INBOX", durable)
	return err
}

func (b *Bus) restoreLocal(ctx context.Context) (bool, error) {
	return b.restoreDurable(ctx, false)
}

// restoreDurable rebuilds the own-subject durable, or the role-subject one.
func (b *Bus) restoreDurable(ctx context.Context, role bool) (bool, error) {
	if b.js == nil {
		return false, nil // no broker context to ask (a bus built around a fake consumer)
	}
	durable := b.durableOf(role)
	// The poll and the heartbeat can both get here for one loss. One rebuilds;
	// the other waits, and when the durable came back while it waited it reports
	// that, so its caller retries on the new durable.
	b.localMu.Lock()
	seen := b.stateOf(role).rebuilds
	b.localMu.Unlock()
	b.restoreMu.Lock()
	defer b.restoreMu.Unlock()
	b.localMu.Lock()
	rebuiltMeanwhile := b.stateOf(role).rebuilds != seen
	b.localMu.Unlock()
	if rebuiltMeanwhile {
		return true, nil
	}
	// The window starts when the broker is asked, whatever it answers: a broker
	// that cannot answer is not asked again on every empty poll. (The global
	// tier's restore, global.go, does the same.)
	b.localMu.Lock()
	b.stateOf(role).checked = time.Now()
	b.localMu.Unlock()
	err := b.lookupLocal(ctx, durable)
	if err == nil {
		b.dh.lookupAnswered()
		return false, nil
	}
	if !errors.Is(err, jetstream.ErrConsumerNotFound) {
		// The broker did not answer (no responders, a timeout): say so in
		// presence, rebuild nothing, and fail nothing. A canceled call is the
		// caller leaving, not a broker that is silent.
		if ctx.Err() == nil {
			b.dh.lookupUnanswered(err)
		}
		return false, nil
	}
	start, acked := b.localRestartSeq(role)
	b.localMu.Lock()
	cfg := b.stateOf(role).cfg
	b.localMu.Unlock()
	if start > 0 {
		cfg.DeliverPolicy = jetstream.DeliverByStartSequencePolicy
		cfg.OptStartSeq = start
	}
	cons, err := b.js.CreateOrUpdateConsumer(ctx, "AGENT_INBOX", cfg)
	if err != nil {
		ferr := fmt.Errorf("the broker has lost this session's local durable %s and recreating it failed: %w", durable, err)
		b.dh.rebuildFailed(ferr)
		return false, ferr
	}
	b.dh.rebuilt()
	msg := fmt.Sprintf("the broker had lost this session's local durable %s; it was recreated", durable)
	if start > 0 {
		msg += fmt.Sprintf(" to resume at stream sequence %d, so mail already read does not replay and mail sent while it was missing is delivered", start)
		if acked > 0 && start <= acked {
			msg += "; a message held and not yet marked below that sequence is delivered again"
		}
	} else {
		msg += " from the start of the inbox, because this session had acked nothing on it and attached with no seat floor"
	}
	b.localMu.Lock()
	if role {
		b.roleCons = cons
	} else {
		b.consumer = cons
	}
	st := b.stateOf(role)
	st.checked = time.Now()
	st.rebuilds++
	b.localMu.Unlock()
	b.resumedMu.Lock()
	b.resumed = append(b.resumed, msg)
	b.resumedMu.Unlock()
	return true, nil
}

// pullLocalNoWait is pullNoWait on the local durables with the same one-time
// recreate the global tier's drainNoWait has, and it records the sequences it
// acked so a rebuild knows where to resume. A role holder has two durables: it
// takes from them in alternating order, so a backlog on one subject cannot
// starve the other.
func (b *Bus) pullLocalNoWait(ctx context.Context, n int) ([]drained, int, error) {
	if b.consOf(true) == nil {
		return b.pullLocalOn(ctx, false, n)
	}
	order := []bool{false, true}
	if b.roleTurn.Add(1)%2 == 0 {
		order = []bool{true, false}
	}
	var (
		out      []drained
		disc     int
		firstErr error
	)
	for _, role := range order {
		if len(out) >= n {
			break
		}
		items, d, err := b.pullLocalOn(ctx, role, n-len(out))
		out = append(out, items...)
		disc += d
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return out, disc, firstErr
}

// pullLocalOn is one durable's no-wait pull, own-subject or role-subject.
func (b *Bus) pullLocalOn(ctx context.Context, role bool, n int) ([]drained, int, error) {
	rebuilt := false
	for {
		out, disc, err := pullNoWait(ctx, b.consOf(role), n, "local", b.handled, nil)
		if !rebuilt && len(out) == 0 && disc == 0 {
			rebuilt = true
			var ok bool
			var rerr error
			if err != nil {
				ok, rerr = b.recoverLocalOn(ctx, role, err)
			} else {
				ok, rerr = b.recheckLocalOn(ctx, role)
			}
			if rerr != nil {
				return nil, 0, rerr
			}
			if ok {
				continue
			}
		}
		if b.handled == nil && ackLaterFrom(ctx) == nil {
			for _, it := range out {
				if !it.AckUnconfirmed {
					b.noteAckedOn(role, it.Seq)
				}
			}
		} else if role {
			// Acked later, by sequence alone: say which durable it came from.
			for _, it := range out {
				b.noteRoleDelivered(it.Seq)
			}
		}
		return out, disc, err
	}
}
