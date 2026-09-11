package main

// A minimal MCP server over stdio. MCP is JSON-RPC 2.0, newline-delimited on
// stdio, and the probe needs exactly three methods: initialize, tools/list,
// tools/call. Implemented directly rather than via a library so the probe
// owns the wire and does not depend on a library's evolving API.
//
// The shape here IS the finding: every tool is request/response. The server
// cannot originate a message into the model's context. wait_for_message is the
// long-poll that stands in for the push the harness keeps to itself.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type Server struct {
	bus   *Bus
	out   *bufio.Writer
	outMu sync.Mutex
	tools []toolDef
	log   func(string, ...any)
}

func newServer(bus *Bus, logf func(string, ...any)) *Server {
	s := &Server{bus: bus, out: bufio.NewWriter(os.Stdout), log: logf}
	s.tools = toolCatalog()
	return s
}

// serve reads JSON-RPC messages line by line and dispatches them. Requests
// carry an id and get a response; notifications (no id) are acted on silently.
func (s *Server) serve(ctx context.Context, in io.Reader) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			s.log("parse error: %v", err)
			continue
		}
		s.dispatch(ctx, &req)
	}
	return sc.Err()
}

func (s *Server) dispatch(ctx context.Context, req *rpcRequest) {
	switch req.Method {
	case "initialize":
		s.reply(req.ID, map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "director-mcp", "version": "0.1-probe"},
		})
	case "notifications/initialized", "notifications/cancelled":
		// notifications: no response
	case "tools/list":
		s.reply(req.ID, map[string]any{"tools": s.tools})
	case "tools/call":
		s.callTool(ctx, req)
	case "ping":
		s.reply(req.ID, map[string]any{})
	default:
		if len(req.ID) > 0 {
			s.replyErr(req.ID, -32601, "method not found: "+req.Method)
		}
	}
}

// callTool routes a tools/call to the bus and returns an MCP tool result. The
// result content is text (a JSON string), which is what the model reads back.
func (s *Server) callTool(ctx context.Context, req *rpcRequest) {
	var p struct {
		Name string          `json:"name"`
		Args json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		s.replyErr(req.ID, -32602, "bad params: "+err.Error())
		return
	}
	res, err := dispatchTool(ctx, s.bus, p.Name, p.Args)
	if err != nil {
		// A tool-level failure is reported inside the result with isError,
		// per MCP, so the model sees the failure rather than an RPC fault.
		s.reply(req.ID, map[string]any{
			"content": []map[string]any{{"type": "text", "text": err.Error()}},
			"isError": true,
		})
		return
	}
	blob, _ := json.MarshalIndent(res, "", "  ")
	s.reply(req.ID, map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(blob)}},
	})
}

func (s *Server) reply(id json.RawMessage, result any) {
	if len(id) == 0 {
		return
	}
	s.write(rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *Server) replyErr(id json.RawMessage, code int, msg string) {
	s.write(rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
}

func (s *Server) write(v any) {
	s.outMu.Lock()
	defer s.outMu.Unlock()
	b, _ := json.Marshal(v)
	fmt.Fprintf(s.out, "%s\n", b)
	s.out.Flush()
}
