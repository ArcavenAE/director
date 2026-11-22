package main

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// A shim that exits on its first bus connect error stays down until someone
// reconnects it by hand: on a host under heavy load the JetStream calls in
// connect can time out while the broker is up, and the harness does not
// respawn an MCP server that exited. connectRetry keeps trying, with a capped
// backoff, for a bounded time, and exits only after the bound.

const (
	connectRetryFirst = time.Second
	connectRetryCap   = 30 * time.Second
	connectRetryTotal = 5 * time.Minute
)

// retryPolicy is the backoff shape: the first wait, the cap each wait doubles
// toward, and the total time after which the shim gives up.
type retryPolicy struct {
	first time.Duration
	max   time.Duration
	total time.Duration
}

var defaultConnectRetry = retryPolicy{first: connectRetryFirst, max: connectRetryCap, total: connectRetryTotal}

// retryClock is the time source of the loop, so a test runs it without waiting.
type retryClock struct {
	now func() time.Time
	// wait sleeps for d and reports false when ctx ended first.
	wait func(ctx context.Context, d time.Duration) bool
}

var realRetryClock = retryClock{
	now: time.Now,
	wait: func(ctx context.Context, d time.Duration) bool {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-t.C:
			return true
		case <-ctx.Done():
			return false
		}
	},
}

// connectRetry calls attempt until it succeeds, the policy's total time is
// spent, attempt returns a permanentConnectError, or ctx ends. Each failed
// attempt is logged with its error. A ctx that ends returns ctx.Err(), which
// the caller treats as an orderly stop and not as a connect failure.
func connectRetry(ctx context.Context, pol retryPolicy, clk retryClock, logf func(string, ...any), attempt func(ctx context.Context) (*Bus, error)) (*Bus, error) {
	start := clk.now()
	delay := pol.first
	for n := 1; ; n++ {
		b, err := attempt(ctx)
		if err == nil {
			if n > 1 {
				logf("bus connect succeeded on attempt %d after %s", n, clk.now().Sub(start).Round(time.Second))
			}
			return b, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var perm *permanentConnectError
		if errors.As(err, &perm) {
			return nil, err
		}
		spent := clk.now().Sub(start)
		if spent+delay > pol.total {
			return nil, fmt.Errorf("gave up after %d attempts over %s: %w", n, spent.Round(time.Second), err)
		}
		logf("bus connect attempt %d failed: %v; retrying in %s", n, err, delay)
		if !clk.wait(ctx, delay) {
			return nil, ctx.Err()
		}
		delay = min(delay*2, pol.max)
	}
}
