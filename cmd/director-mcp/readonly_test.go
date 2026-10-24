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
}

// toolsListed is the tools/list result as a client reads it.
func toolsListed(t *testing.T, gcfg *globalConfig, cueOn bool) []map[string]any {
	t.Helper()
	var buf bytes.Buffer
	s := &Server{out: bufio.NewWriter(&buf), tools: toolCatalog(gcfg, cueOn), log: func(string, ...any) {}}
	s.dispatch(context.Background(), &rpcRequest{ID: json.RawMessage(`1`), Method: "tools/list"})
	var resp struct {
		Result struct {
			Tools []map[string]any `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("tools/list reply: %v: %s", err, buf.String())
	}
	return resp.Result.Tools
}

func TestEveryToolDeclaresReadOnlyHint(t *testing.T) {
	for _, gcfg := range []*globalConfig{nil, {Domain: "global", Cluster: "c1", Role: roleDirector}} {
		for _, cueOn := range []bool{false, true} {
			t.Run(fmt.Sprintf("global=%v cue=%v", gcfg != nil, cueOn), func(t *testing.T) {
				tools := toolsListed(t, gcfg, cueOn)
				if len(tools) != len(wantReadOnly) {
					t.Fatalf("%d tools listed, want %d", len(tools), len(wantReadOnly))
				}
				for _, tool := range tools {
					name, _ := tool["name"].(string)
					want, known := wantReadOnly[name]
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
					if got != want {
						t.Errorf("%s: readOnlyHint %v, want %v", name, got, want)
					}
				}
			})
		}
	}
}

// The exact tools/list result with the global tier and the cue off. Run with
// DIRECTOR_UPDATE_GOLDEN=1 to rewrite it after an intended change.
func TestToolsListMatchesTheGolden(t *testing.T) {
	var buf bytes.Buffer
	s := &Server{out: bufio.NewWriter(&buf), tools: toolCatalog(nil, false), log: func(string, ...any) {}}
	s.dispatch(context.Background(), &rpcRequest{ID: json.RawMessage(`1`), Method: "tools/list"})
	var resp struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, resp.Result, "", "  "); err != nil {
		t.Fatal(err)
	}
	pretty.WriteByte('\n')
	const path = "testdata/tools-list.golden.json"
	if os.Getenv("DIRECTOR_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, pretty.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pretty.Bytes(), want) {
		t.Fatalf("tools/list differs from %s:\n got %s\nwant %s", path, pretty.Bytes(), want)
	}
}
