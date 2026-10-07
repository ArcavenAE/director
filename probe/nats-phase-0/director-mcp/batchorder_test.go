package main

import (
	"strings"
	"testing"
)

// The batch result's order note and the tool description say the same thing,
// and neither reads as delivery priority: the list is local first, the budget
// is split by turn (director#260).
func TestBatchOrderNoteSaysListedAndSplitByTurn(t *testing.T) {
	for _, want := range []string{"listed local first, then global", "oldest first", "split by turn"} {
		if !strings.Contains(batchOrderNote, want) {
			t.Errorf("batchOrderNote %q lacks %q", batchOrderNote, want)
		}
	}
	for _, bad := range []string{"local before global", "budget is shared"} {
		if strings.Contains(batchOrderNote, bad) {
			t.Errorf("batchOrderNote %q still says %q", batchOrderNote, bad)
		}
	}
	var desc string
	for _, td := range toolCatalog(&globalConfig{Domain: "global", Cluster: "c", Role: roleSupervisor}, false) {
		if td.Name == "wait_for_message" {
			desc = td.Description
		}
	}
	for _, want := range []string{"listed local first, then global", "split by turn"} {
		if !strings.Contains(desc, want) {
			t.Errorf("wait_for_message description lacks %q: %s", want, desc)
		}
	}
}
