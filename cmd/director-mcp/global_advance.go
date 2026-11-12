package main

import (
	"context"
	"errors"
)

// advanceResult is what one advance did, for the printout.
type advanceResult struct {
	Durable       string
	Stream        string
	BeforeFloor   uint64
	AfterFloor    uint64
	BeforePending uint64
	AfterPending  uint64
	Acked         uint64
	Head          uint64
	HeadKnown     bool
}

// advanceGlobalDurable is not built yet.
func advanceGlobalDurable(ctx context.Context, g *globalTier, agentID string, to uint64) (advanceResult, error) {
	return advanceResult{}, errors.New("not built")
}

func formatAdvance(r advanceResult) string { return "" }

func runGlobalAdvanceCmd(ctx context.Context, cli cliArgs, out, errw interface {
	Write([]byte) (int, error)
}) int {
	return 0
}

const globalAdvanceUsage = "director-mcp global-advance [--seat AGENT-ID] [--to-head | --to SEQ]"
