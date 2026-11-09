package marry

import (
	"encoding/json"
	"os"
	"testing"
)

// TestAgentIDPatternMatchesEnvelopeSchema fails when the agent_id class in this
// package and the one in the envelope schema drift apart.
func TestAgentIDPatternMatchesEnvelopeSchema(t *testing.T) {
	b, err := os.ReadFile("../testdata/director-envelope.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Properties map[string]struct {
			Properties map[string]struct {
				Pattern string `json:"pattern"`
			} `json:"properties"`
		} `json:"properties"`
		Defs map[string]struct {
			Properties map[string]struct {
				Pattern string `json:"pattern"`
			} `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	want := "^" + agentIDPattern + "$"
	var found []string
	for _, p := range s.Properties {
		if v, ok := p.Properties["agent_id"]; ok {
			found = append(found, v.Pattern)
		}
	}
	for _, d := range s.Defs {
		if v, ok := d.Properties["agent_id"]; ok {
			found = append(found, v.Pattern)
		}
	}
	if len(found) == 0 {
		t.Fatal("no agent_id pattern found in the envelope schema")
	}
	for _, got := range found {
		if got != want {
			t.Fatalf("schema agent_id pattern %q, marry.agentIDPattern wants %q", got, want)
		}
	}
}
