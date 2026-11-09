package main

import (
	"context"

	"github.com/nats-io/nats.go/jetstream"
)

// adoptInboxMaxAge reads AGENT_INBOX's max_age from the live stream and makes
// it the lifetime inboxMaxAge() reports, so the send records are kept as long
// as the stream keeps the mail whatever the stream was provisioned with. A read
// that fails, or a stream with no max age, leaves the fallback in force and logs
// a warning; the warning is a diagnostic and never stops startup. It reads only:
// the stream's configuration is not changed.
func adoptInboxMaxAge(ctx context.Context, js jetstream.JetStream, logf func(string, ...any)) {
	st, err := js.Stream(ctx, "AGENT_INBOX")
	if err != nil {
		logf("WARNING: could not read AGENT_INBOX to learn its max age (%v); using the fallback %v", err, inboxMaxAgeFallback)
		return
	}
	si, err := st.Info(ctx)
	if err != nil {
		logf("WARNING: could not read AGENT_INBOX info to learn its max age (%v); using the fallback %v", err, inboxMaxAgeFallback)
		return
	}
	if si.Config.MaxAge <= 0 {
		logf("WARNING: AGENT_INBOX has no max age; using the fallback %v", inboxMaxAgeFallback)
		return
	}
	liveInboxMaxAge.Store(int64(si.Config.MaxAge))
	logf("AGENT_INBOX max age %v read from the live stream (send records kept %v)", si.Config.MaxAge, sendKeep())
}
