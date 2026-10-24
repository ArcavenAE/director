package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// Each tool declares readOnlyHint, so a harness that lets read-only tools
// through plan mode can let a plan-mode cast poll the bus (aae-orc-r675b).
// The three that only read the bus are true; the four that write are false.
var wantReadOnly = map[string]bool{
	"wait_for_message": true,
	"inbox_summary":    true,
	"list_roster":      true,
	"send_message":     false,
	"broadcast":        false,
	"report_status":    false,
	"set_presence":     false,
	"mark_handled":     false,
	"cancel_held":      false,
}

// servedList is the raw tools/list result of the server main builds, through
// newServer, for a seat with the given levers. newServer reads only these
// fields of the bus, so no broker is needed.
func servedList(t *testing.T, gcfg *globalConfig, cueOn, handledOn, watchOn bool) json.RawMessage {
	t.Helper()
	bus := &Bus{globalCfg: gcfg}
	if handledOn {
		bus.handled = &handledState{}
	}
	if watchOn {
		bus.watch = &presenceWatcher{}
	}
	s := newServer(bus, func(string, ...any) {}, cueOn)
	var buf bytes.Buffer
	s.out = bufio.NewWriter(&buf)
	s.dispatch(context.Background(), &rpcRequest{ID: json.RawMessage(`1`), Method: "tools/list"})
	var resp struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("tools/list reply: %v: %s", err, buf.String())
	}
	return resp.Result
}

// servedTools is servedList decoded, one map per tool.
func servedTools(t *testing.T, gcfg *globalConfig, cueOn, handledOn, watchOn bool) []map[string]any {
	t.Helper()
	var res struct {
		Tools []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal(servedList(t, gcfg, cueOn, handledOn, watchOn), &res); err != nil {
		t.Fatal(err)
	}
	return res.Tools
}

// Every tool the server serves declares readOnlyHint, in all sixteen
// combinations of global tier, cue, handled mode and presence watch.
func TestEveryToolDeclaresReadOnlyHint(t *testing.T) {
	for _, gcfg := range []*globalConfig{nil, {Domain: "global", Cluster: "c1", Role: roleDirector}} {
		for _, cueOn := range []bool{false, true} {
			for _, handledOn := range []bool{false, true} {
				for _, watchOn := range []bool{false, true} {
					name := fmt.Sprintf("global=%v cue=%v handled=%v watch=%v", gcfg != nil, cueOn, handledOn, watchOn)
					t.Run(name, func(t *testing.T) {
						tools := servedTools(t, gcfg, cueOn, handledOn, watchOn)
						want := 7
						if handledOn {
							want++
						}
						if watchOn {
							want++
						}
						if len(tools) != want {
							t.Fatalf("%d tools listed, want %d", len(tools), want)
						}
						for _, tool := range tools {
							name, _ := tool["name"].(string)
							wantHint, known := wantReadOnly[name]
							if !known {
								t.Errorf("tool %q has no expected hint", name)
								continue
							}
							ann, _ := tool["annotations"].(map[string]any)
							got, ok := ann["readOnlyHint"].(bool)
							if !ok {
								t.Errorf("%s: no readOnlyHint in %v", name, tool["annotations"])
								continue
							}
							if got != wantHint {
								t.Errorf("%s: readOnlyHint %v, want %v", name, got, wantHint)
							}
						}
					})
				}
			}
		}
	}
}

// The exact tools/list result the server serves, with everything off, and with
// handled mode and the presence watch on (all nine tools). Run with
// DIRECTOR_UPDATE_GOLDEN=1 to rewrite them after an intended change.
func TestToolsListMatchesTheGolden(t *testing.T) {
	for _, c := range []struct {
		path               string
		handledOn, watchOn bool
	}{
		{"testdata/tools-list.golden.json", false, false},
		{"testdata/tools-list-handled-watch.golden.json", true, true},
	} {
		t.Run(c.path, func(t *testing.T) {
			raw := servedList(t, nil, false, c.handledOn, c.watchOn)
			var pretty bytes.Buffer
			if err := json.Indent(&pretty, raw, "", "  "); err != nil {
				t.Fatal(err)
			}
			pretty.WriteByte('\n')
			if os.Getenv("DIRECTOR_UPDATE_GOLDEN") == "1" {
				if err := os.WriteFile(c.path, pretty.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(c.path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(pretty.Bytes(), want) {
				t.Fatalf("tools/list differs from %s:\n got %s\nwant %s", c.path, pretty.Bytes(), want)
			}
		})
	}
}
