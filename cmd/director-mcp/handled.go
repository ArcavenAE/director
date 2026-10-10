package main

// The handled ledger (K4, director#278). Stub: the red commit carries the
// types and signatures the tests use, and every behavior is unbuilt.

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

var errHandledUnbuilt = errors.New("handled ledger: not built")

const thresholdHandledAckWait = "handled_ack_wait"

type handledKey struct {
	Tier   string
	Stream string
	Seq    uint64
}

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

type handledLedger struct{ path, lock string }

func newHandledLedger(dir string) *handledLedger { return &handledLedger{} }

func (l *handledLedger) fold() (map[handledKey]handledEvent, error) {
	return nil, errHandledUnbuilt
}

func (l *handledLedger) record(ev handledEvent) (bool, error) { return false, errHandledUnbuilt }

type unhandledRow struct {
	Tier      string `json:"tier"`
	Stream    string `json:"stream"`
	Seq       uint64 `json:"sequence"`
	MessageID string `json:"message_id"`
	Sender    string `json:"sender"`
	Age       string `json:"age"`
}

type handledState struct {
	mu      sync.Mutex
	ackWait time.Duration
}

func handledFromEnv() (*handledState, error) { return nil, errHandledUnbuilt }

func loadThresholds(dir string) (map[string]time.Duration, error) { return nil, errHandledUnbuilt }

func (h *handledState) settle(ctx context.Context, m jetstream.Msg, tier, stream string, e *Envelope, seq uint64) (bool, error) {
	return true, nil
}

func (h *handledState) unhandled() []unhandledRow { return nil }

func (b *Bus) markHandled(ctx context.Context, tier string, seq uint64, disposition, detail string) (map[string]any, error) {
	return nil, errHandledUnbuilt
}

func toolCatalogHandled(gcfg *globalConfig, cueOn, handledOn bool) []toolDef {
	return toolCatalog(gcfg, cueOn)
}

type markArgs struct {
	Tier        string `json:"tier"`
	Seq         uint64 `json:"sequence"`
	Disposition string `json:"disposition"`
	ForwardID   string `json:"forward_id"`
	Reason      string `json:"reason"`
}

func parseMarkArgs(raw json.RawMessage) (markArgs, error) { return markArgs{}, nil }
