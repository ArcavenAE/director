package main

// The ask reader (design section 6): a loop over a broker's AGENT_AUDIT that
// feeds the ledger in askledger.go, and the on-demand `asks` read of its store.

import (
	"context"
	"io"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

type askReaderCfg struct {
	Broker string
	File   string
}

type askReader struct {
	js      jetstream.JetStream
	cfg     askReaderCfg
	ledger  *askLedger
	NotRead []string
}

func newAskReader(js jetstream.JetStream, cfg askReaderCfg) *askReader {
	return &askReader{js: js, cfg: cfg, ledger: newAskLedger()}
}

// Pass runs one pass at now. Stubbed until the green change.
func (r *askReader) Pass(ctx context.Context, now time.Time) error { return nil }

// readAskReport reads the ASK_LEDGER bucket and builds the report. Stubbed.
func readAskReport(ctx context.Context, js jetstream.JetStream, now time.Time, all bool) (askReport, error) {
	return askReport{}, nil
}

func runAsksCmd(ctx context.Context, url string, cli cliArgs, out, errw io.Writer) int { return 0 }

func runAskReaderCmd(ctx context.Context, url string, cli cliArgs, errw io.Writer) int { return 0 }
