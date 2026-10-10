package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// The batch result carries batchOrderNote as sent by the tool, not a literal
// that drifted from it (director#260, follow-up to #261).
func TestBatchResultCarriesBatchOrderNote(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	provision(t, ctx, url)
	reader := roleBus(t, ctx, url, "rev-1", "reviewer")
	sender := roleBus(t, ctx, url, "sender", "")
	if err := sendRole(ctx, sender, "agent://ops/rev-1", "a1", ""); err != nil {
		t.Fatal(err)
	}
	out, err := dispatchTool(ctx, reader, "wait_for_message", json.RawMessage(`{"max":2,"timeout_seconds":3}`))
	if err != nil {
		t.Fatal(err)
	}
	res, _ := out.(map[string]any)
	if res["order"] != batchOrderNote {
		t.Fatalf("batch order = %v, want batchOrderNote %q", res["order"], batchOrderNote)
	}
}

// The shim reference shows the same note in its batch example.
func TestShimReferenceBatchExampleCarriesBatchOrderNote(t *testing.T) {
	b, err := os.ReadFile("../../docs/shim-reference.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"order": "`+batchOrderNote+`"`) {
		t.Fatalf("docs/shim-reference.md batch example does not carry batchOrderNote %q", batchOrderNote)
	}
}
