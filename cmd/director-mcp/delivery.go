package main

import (
	"context"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// sendRec is one local send as the audit mirror recorded it: where the inbox
// stream stored it. Seq is the inbox stream's own sequence, not the audit's.
type sendRec struct {
	Seq     uint64    `json:"seq"`
	Subject string    `json:"subject"`
	Perf    string    `json:"performative"`
	At      time.Time `json:"at"`
}

// sendKeep is how long a send record is kept: the inbox's max age plus a day,
// so a send is still answerable for a day after the stream dropped it.
func sendKeep() time.Duration { return inboxMaxAge() + 24*time.Hour }

// sendsMax caps the records kept, oldest dropped first, so the stored value
// stays well inside a KV value's size limit.
const sendsMax = 8000

// Delivery states (the architect's spec for aae-orc-t77rr).
const (
	deliveryRead        = "read"
	deliveryNotYetRead  = "not_yet_read"
	deliveryExpiredUnrd = "expired_unread"
	deliveryNoReader    = "no_reader"
	deliveryUnknown     = "unknown"
)

// delivery is what the bus says became of one send.
type delivery struct {
	State string `json:"state"`
	Error string `json:"error,omitempty"`
	Note  string `json:"note,omitempty"`
}

// inboxView is one read of AGENT_INBOX: the stream's first sequence and the
// consumers on it. Err is a failed stream read; ConsumersErr a failed listing.
// Both leave the states that need them as unknown, never guessed.
type inboxView struct {
	First        uint64
	Consumers    []*jetstream.ConsumerInfo
	Err          error
	ConsumersErr error
}

// readsSubject reports whether a consumer is a durable reader of exactly this
// subject: a durable with the subject as its filter. An ephemeral consumer, a
// wildcard filter and no filter do not count, because none of them is a seat
// that will read the mail. This is the same test as the hinted-send refusal
// (director#488); it is carried here so the two PRs do not depend on each
// other.
func readsSubject(cfg jetstream.ConsumerConfig, subject string) bool {
	if cfg.Durable == "" {
		return false
	}
	for _, f := range filterSubjects(cfg) {
		if f == subject {
			return true
		}
	}
	return false
}

// deliveryOf computes the delivery state of one send. Order matters: a covering
// durable whose ack floor reached the send is a read, whatever the stream has
// since dropped; otherwise a send below the stream's first sequence is expired
// unread; otherwise no covering durable is no_reader and the rest is not yet
// read.
func deliveryOf(s sendRec, v inboxView) delivery {
	if v.Err != nil {
		return delivery{State: deliveryUnknown, Error: v.Err.Error()}
	}
	covering := false
	for _, c := range v.Consumers {
		if c == nil || !readsSubject(c.Config, s.Subject) {
			continue
		}
		covering = true
		if c.AckFloor.Stream >= s.Seq {
			return delivery{State: deliveryRead}
		}
	}
	if v.ConsumersErr != nil {
		return delivery{State: deliveryUnknown, Error: v.ConsumersErr.Error()}
	}
	switch {
	case s.Seq < v.First:
		return delivery{State: deliveryExpiredUnrd}
	case !covering:
		return delivery{State: deliveryNoReader}
	}
	return delivery{State: deliveryNotYetRead}
}

// readInboxView reads the stream and its consumers once.
func readInboxView(ctx context.Context, js jetstream.JetStream) inboxView {
	st, err := js.Stream(ctx, "AGENT_INBOX")
	if err != nil {
		return inboxView{Err: err}
	}
	info, err := st.Info(ctx)
	if err != nil {
		return inboxView{Err: err}
	}
	v := inboxView{First: info.State.FirstSeq}
	lister := st.ListConsumers(ctx)
	for ci := range lister.Info() {
		v.Consumers = append(v.Consumers, ci)
	}
	v.ConsumersErr = lister.Err()
	return v
}
