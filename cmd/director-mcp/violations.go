package main

import (
	"errors"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

// A publish the broker denies is never acked, so a JetStream publish returns
// "context deadline exceeded" and says nothing of why. The reason arrives on
// the connection's async error callback instead, as
// nats.ErrPermissionViolation wrapping "Permissions Violation for Publish to
// <subject>". violationRing keeps the last few of those, keyed by subject, so a
// publish that times out can name the violation for its own subject. nc.LastError
// is only one slot and is not keyed by subject, so it is not used for this.

const violationRingSize = 16

var publishViolationRe = regexp.MustCompile(`(?i)permissions violation for publish to "([^"]+)"`)

type violation struct {
	subject string
	text    string
	at      time.Time
}

type violationRing struct {
	mu    sync.Mutex
	items []violation // oldest first, at most violationRingSize
}

// note records err if it is a publish permissions violation. Anything else is
// ignored. A nil ring is a no-op, so callers that do not keep one stay valid.
func (r *violationRing) note(err error) {
	if r == nil || err == nil || !errors.Is(err, nats.ErrPermissionViolation) {
		return
	}
	m := publishViolationRe.FindStringSubmatch(err.Error())
	if m == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, violation{subject: m[1], text: err.Error(), at: time.Now()})
	if len(r.items) > violationRingSize {
		r.items = r.items[len(r.items)-violationRingSize:]
	}
}

// since returns the text of the newest violation for subject noted at or after
// start, so a violation from an earlier publish never explains a later one.
func (r *violationRing) since(subject string, start time.Time) (string, bool) {
	if r == nil {
		return "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.items) - 1; i >= 0; i-- {
		v := r.items[i]
		if v.subject == subject && !v.at.Before(start) {
			return v.text, true
		}
	}
	return "", false
}

// publishViolationError carries the original publish error and the broker's
// stated reason. It unwraps to the original, so errors.Is on the deadline
// still holds.
type publishViolationError struct {
	err    error
	reason string
}

func (e *publishViolationError) Error() string {
	return fmt.Sprintf("%v: broker denied the publish: %s", e.err, e.reason)
}
func (e *publishViolationError) Unwrap() error { return e.err }

// explain wraps err with the broker's violation for subject, if one was noted
// since start. With none noted, or err nil, it returns err unchanged.
func (r *violationRing) explain(subject string, start time.Time, err error) error {
	if err == nil {
		return nil
	}
	if text, ok := r.since(subject, start); ok {
		return &publishViolationError{err: err, reason: text}
	}
	return err
}
