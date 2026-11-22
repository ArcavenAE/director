package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Claude Code gives an MCP server about 30 seconds to answer initialize
// (MCP_TIMEOUT), so a bus connect that retries for minutes must not hold the
// answer back (I-685 2b, review of director#740). Built shim, dead broker, a
// long retry bound: initialize and tools/list are answered within a short bound,
// and a tool call in that window returns the named "bus connecting" error.
func TestShimAnswersInitializeWhileTheBusIsStillConnecting(t *testing.T) {
	bin := buildShim(t, t.TempDir(), true)
	cmd := exec.Command(bin)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir(), "NATS_URL=nats://127.0.0.1:1", "DIRECTOR_BUS_CONNECT_RETRY=10m", "DIRECTOR_AGENT_ID=seat-a", "DIRECTOR_TEAM=ops", "DIRECTOR_WORKSPACE=w"}
	cmd.Stderr = io.Discard
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	defer func() { _ = cmd.Process.Kill() }()

	lines := make(chan string, 8)
	go func() {
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	ask := func(req string) map[string]any {
		t.Helper()
		if _, err := io.WriteString(in, req+"\n"); err != nil {
			t.Fatal(err)
		}
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatalf("the shim closed its output before answering %s", req)
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(l), &m); err != nil {
				t.Fatalf("reply %q: %v", l, err)
			}
			return m
		case <-time.After(5 * time.Second):
			t.Fatalf("no reply within 5s to %s: the shim is holding its answer back while the bus connects", req)
		}
		return nil
	}

	r := ask(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	if res, _ := r["result"].(map[string]any); res == nil || res["protocolVersion"] == nil {
		t.Fatalf("initialize reply: %v", r)
	}
	r = ask(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if res, _ := r["result"].(map[string]any); res == nil || len(res["tools"].([]any)) == 0 {
		t.Fatalf("tools/list reply has no tools: %v", r)
	}
	r = ask(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"inbox_summary","arguments":{}}}`)
	res, _ := r["result"].(map[string]any)
	if res == nil || res["isError"] != true {
		t.Fatalf("a tool call while connecting: want an isError result, got %v", r)
	}
	text := ""
	if c, ok := res["content"].([]any); ok && len(c) > 0 {
		text, _ = c[0].(map[string]any)["text"].(string)
	}
	if !strings.Contains(text, "bus connecting (attempt ") || !strings.Contains(text, "last error") {
		t.Errorf("the tool error does not name the connect state: %q", text)
	}
	// Closing stdin before the bus came up ends the shim without waiting out the retry.
	_ = in.Close()
	select {
	case <-exited:
	case <-time.After(15 * time.Second):
		t.Error("the shim kept running 15s after its stdin closed, with the bus still down")
	}
}
