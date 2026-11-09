package main

import (
	"context"

	"github.com/nats-io/nats.go/jetstream"
)

// adoptInboxMaxAge reads AGENT_INBOX's max_age from the live stream and makes
// it the lifetime inboxMaxAge() reports. A read that fails, or a stream with no
// max age, leaves the fallback in force and logs a warning; the warning is a
// diagnostic and never stops startup.
func adoptInboxMaxAge(ctx context.Context, js jetstream.JetStream, logf func(string, ...any)) {
}
