package main

import "testing"

// The key forms docs/shim-reference.md lists under "Rollup keys". A consumer
// that parses the rollup reads these, so the forms are pinned here.
func TestRollupKeyFormsAreTheDocumentedOnes(t *testing.T) {
	for name, c := range map[string]struct {
		p    askParty
		want string
	}{
		"local":             {askParty{Address: "agent://ops/builder-1", Team: "ops", Role: "builder"}, "ops/builder"},
		"global director":   {askParty{Address: "global://director", Team: globalTeam, Role: roleDirector}, "global:global/director"},
		"cluster":           {askParty{Address: "global://cluster-b/supervisor", Team: "cluster-b", Role: "supervisor"}, "global:cluster-b/supervisor"},
		"unresolved":        {askParty{Team: teamUnresolved, Role: roleUnresolved}, "unresolved"},
		"ambiguous":         {askParty{Team: "ops", Role: roleAmbiguous}, "unresolved: ambiguous"},
		"no role declared":  {askParty{Team: "ops", Role: roleNoneDecl}, "ops/unresolved: no role declared"},
		"asker, no address": {askParty{AgentID: "x", Team: "ops", Role: "builder"}, "ops/builder"},
	} {
		if got := rollupKey(c.p); got != c.want {
			t.Errorf("%s: key = %q, want %q", name, got, c.want)
		}
	}
}
