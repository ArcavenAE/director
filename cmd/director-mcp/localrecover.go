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
}

// cons returns the current local consumer. A rebuild replaces it, so readers on
// other goroutines (the cue watcher) take it here and not from the field.
func (b *Bus) cons() jetstream.Consumer {
	b.localMu.Lock()
	defer b.localMu.Unlock()
	return b.consumer
}

// noteLocalAcked records an acked local stream sequence.
func (b *Bus) noteLocalAcked(seq uint64) {
	b.localMu.Lock()
	if seq > b.local.acked {
		b.local.acked = seq
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
func (b *Bus) localRestartSeq() (start uint64, acked uint64) {
	b.localMu.Lock()
	acked = b.local.acked
	b.localMu.Unlock()
	if acked > 0 {
		start = acked + 1
	}
	if b.handled != nil {
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
	if !possiblyLost(cause) {
		return false, nil
	}
	return b.restoreLocal(ctx)
}

// recheckLocal is recoverLocal for an empty pull: a pull on a durable the
// broker no longer has can look like an empty inbox, so an empty pull asks, at
// most once per durableCheckEvery.
func (b *Bus) recheckLocal(ctx context.Context) (bool, error) {
	b.localMu.Lock()
	due := time.Since(b.local.checked) >= durableCheckEvery
	b.localMu.Unlock()
	if !due {
		return false, nil
	}
	return b.restoreLocal(ctx)
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
	if b.js == nil {
		return false, nil // no broker context to ask (a bus built around a fake consumer)
	}
	durable := "mcp_" + b.self.AgentID + "_" + b.instance
	// The window starts when the broker is asked, whatever it answers: a broker
	// that cannot answer is not asked again on every empty poll. (The global
	// tier's restore, global.go, does the same.)
	b.localMu.Lock()
	b.local.checked = time.Now()
	b.localMu.Unlock()
	err := b.lookupLocal(ctx, durable)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, jetstream.ErrConsumerNotFound) {
		return false, nil
	}
	start, acked := b.localRestartSeq()
	b.localMu.Lock()
	cfg := b.local.cfg
	b.localMu.Unlock()
	if start > 0 {
		cfg.DeliverPolicy = jetstream.DeliverByStartSequencePolicy
		cfg.OptStartSeq = start
	}
	cons, err := b.js.CreateOrUpdateConsumer(ctx, "AGENT_INBOX", cfg)
	if err != nil {
		return false, fmt.Errorf("the broker has lost this session's local durable %s and recreating it failed: %w", durable, err)
	}
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
	b.consumer = cons
	b.local.checked = time.Now()
	b.localMu.Unlock()
	b.resumedMu.Lock()
	b.resumed = append(b.resumed, msg)
	b.resumedMu.Unlock()
	return true, nil
}

// pullLocalNoWait is pullNoWait on the local durable with the same one-time
// recreate the global tier's drainNoWait has, and it records the sequences it
// acked so a rebuild knows where to resume.
func (b *Bus) pullLocalNoWait(ctx context.Context, n int) ([]drained, int, error) {
	rebuilt := false
	for {
		out, disc, err := pullNoWait(ctx, b.cons(), n, "local", b.handled)
		if !rebuilt && len(out) == 0 && disc == 0 {
			rebuilt = true
			var ok bool
			var rerr error
			if err != nil {
				ok, rerr = b.recoverLocal(ctx, err)
			} else {
				ok, rerr = b.recheckLocal(ctx)
			}
			if rerr != nil {
				return nil, 0, rerr
			}
			if ok {
				continue
			}
		}
		if b.handled == nil {
			for _, it := range out {
				if !it.AckUnconfirmed {
					b.noteLocalAcked(it.Seq)
				}
			}
		}
		return out, disc, err
	}
}
