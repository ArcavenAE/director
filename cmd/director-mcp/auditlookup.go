package main

// audit_lookup: read one AGENT_AUDIT record by its sequence number
// (sim/design/audit-lookup.md). Stub for the red commit.

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/nats-io/nats.go/jetstream"
)

func sessionCluster() string { return "" }

func auditLookup(ctx context.Context, js jetstream.JetStream, own string, raw json.RawMessage) (map[string]any, error) {
	return nil, errors.New("audit_lookup: not built")
}
