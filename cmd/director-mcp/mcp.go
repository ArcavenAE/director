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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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
	Name        string           `json:"name"`
	Description string           `json:"description"`
	InputSchema map[string]any   `json:"inputSchema"`
	Annotations *toolAnnotations `json:"annotations,omitempty"`
}

// toolAnnotations are the MCP tool hints (2025-06-18).
type toolAnnotations struct {
	ReadOnlyHint bool `json:"readOnlyHint"`
}

type Server struct {
	bus   *Bus
	out   *bufio.Writer
	outMu sync.Mutex
	tools []toolDef
	log   func(string, ...any)
	// cue is the P1 channel cue, nil unless the seat opted in (cue.go).
	cue *cue
	// running counts tools/call requests in flight; calls maps each one's
	// request id to the cancel that notifications/cancelled fires, and wg
	// lets serve wait for them on EOF.
	running atomic.Int32
	callsMu sync.Mutex
	calls   map[string]*callEntry
	// answered keeps the calls whose reply carried mail, for lateWindow.
	answered   map[string]*callEntry
	lateWindow time.Duration
	// testBeforeReply runs after a call's tool returns and before its reply is
	// written; tests use it to land a cancel there.
	testBeforeReply func(context.Context)
	wg              sync.WaitGroup
}

// callEntry is one tools/call. After its reply is written, items holds the mail
// the reply carried, so a notifications/cancelled that crosses the reply can
// give it back (ackafter.go). cancelled is set under callsMu by a cancel that
// reached the call while it ran.
type callEntry struct {
	cancel    context.CancelFunc
	cancelled bool
	replied   time.Time
	items     []drained
}

func newServer(bus *Bus, logf func(string, ...any), cueOn bool) *Server {
	s := &Server{bus: bus, out: bufio.NewWriter(os.Stdout), log: logf}
	if dir, err := stateDir(); err == nil {
		if table, notes, err := loadThresholdsNotes(dir); err == nil {
			s.lateWindow = table[thresholdLateCancelWindow]
			for _, n := range notes {
				logf("%s", n)
			}
		} else {
			logf("threshold table: %v; the late-cancel window keeps its default", err)
		}
	}
	s.tools = withHoldTools(toolCatalogHandled(bus.globalCfg, cueOn, bus.handled != nil), bus.watch != nil)
	return s
}

// inFlight reports how many tools/call requests are running.
func (s *Server) inFlight() int { return int(s.running.Load()) }

// serve reads JSON-RPC messages line by line and dispatches them. Requests
// carry an id and get a response; notifications (no id) are acted on silently.
//
// A tools/call with an id runs on its own goroutine, so the loop goes on
// reading while a wait blocks: a ping is answered at once and a
// notifications/cancelled reaches the call it names. Everything else stays
// inline, so the handshake keeps its order. On EOF serve cancels the calls in
// flight and waits for them before it returns.
func (s *Server) serve(ctx context.Context, in io.Reader) error {
	ctx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		s.wg.Wait()
	}()
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
		if req.Method == "tools/call" && len(req.ID) > 0 {
			s.startCall(ctx, &req)
			continue
		}
		s.dispatch(ctx, &req)
	}
	return sc.Err()
}

// startCall runs one tools/call on its own goroutine under a context that
// notifications/cancelled can end.
func (s *Server) startCall(ctx context.Context, req *rpcRequest) {
	cctx, cancel := context.WithCancel(ctx)
	key, entry := callKey(req.ID), &callEntry{cancel: cancel}
	s.callsMu.Lock()
	if s.calls == nil {
		s.calls = map[string]*callEntry{}
	}
	s.calls[key] = entry
	s.callsMu.Unlock()
	s.running.Add(1)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.running.Add(-1)
		defer func() {
			s.callsMu.Lock()
			if s.calls[key] == entry {
				delete(s.calls, key)
			}
			if len(entry.items) > 0 {
				s.rememberAnsweredLocked(key, entry)
			}
			s.callsMu.Unlock()
			cancel()
		}()
		s.callTool(cctx, req)
	}()
}

// cancelCall ends the call a notifications/cancelled names. An id that is not
// in flight (already answered, or never sent) is ignored, as MCP allows.
func (s *Server) cancelCall(params json.RawMessage) {
	var p struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if err := json.Unmarshal(params, &p); err != nil || len(p.RequestID) == 0 {
		return
	}
	key := callKey(p.RequestID)
	s.callsMu.Lock()
	e := s.calls[key]
	if e != nil {
		e.cancelled = true
		if len(e.items) > 0 {
			// The reply is written: the client may have sent this cancel before
			// it read it. The mail goes back to the next wait.
			items := e.items
			e.items = nil
			s.callsMu.Unlock()
			s.bus.returnMail(items, "late cancel of call "+key, s.log)
			return
		}
		s.callsMu.Unlock()
		e.cancel()
		return
	}
	a := s.answered[key]
	if a != nil {
		delete(s.answered, key)
	}
	s.callsMu.Unlock()
	if a == nil {
		return
	}
	if time.Since(a.replied) <= s.lateCancelWindow() {
		s.bus.returnMail(a.items, "late cancel of call "+key, s.log)
		return
	}
	s.log("late cancel for call %s arrived %s after its reply, past the %s window; nothing changed (message_ids: %s)", key, time.Since(a.replied).Round(time.Second), s.lateCancelWindow(), idsOf(a.items))
}

// callKey is a request id in compact form, so 9 and "w1" match however the
// client spaced them.
func callKey(id json.RawMessage) string {
	var b bytes.Buffer
	if err := json.Compact(&b, id); err != nil {
		return string(id)
	}
	return b.String()
}

func (s *Server) dispatch(ctx context.Context, req *rpcRequest) {
	switch req.Method {
	case "initialize":
		version, caps := "2025-06-18", map[string]any{"tools": map[string]any{}}
		if s.cue != nil {
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(req.Params, &p)
			var channel bool
			if version, channel = s.cue.handshake(p.ProtocolVersion); channel {
				caps["experimental"] = map[string]any{"claude/channel": map[string]any{}}
			}
		}
		s.reply(req.ID, map[string]any{
			"protocolVersion": version,
			"capabilities":    caps,
			"serverInfo":      map[string]any{"name": "director-mcp", "version": "0.1-probe"},
		})
	case "notifications/initialized":
		if s.cue != nil {
			s.cue.initialized()
		}
	case "notifications/cancelled":
		// notifications: no response
		s.cancelCall(req.Params)
	case "tools/list":
		s.reply(req.ID, map[string]any{"tools": s.tools})
	case "tools/call":
		s.callTool(ctx, req)
	case "ping":
		s.reply(req.ID, map[string]any{})
	default:
		// server/discover lands here on purpose: -32601 makes Claude Code fall
		// back to the legacy initialize, the handshake the cue was measured on
		// (channel-cue.md 3.2). Do not implement it without re-measuring.
		if len(req.ID) > 0 {
			s.replyErr(req.ID, -32601, "method not found: "+req.Method)
		}
	}
}

// pushOnlyNote is what a push-only wait adds to its result.
const pushOnlyNote = "live: this wait did not block. End your turn now; a director cue notice wakes you when mail arrives."

// pushOnlyWaitArgs rewrites a wait's timeout to the shortest the tool takes,
// keeping max, so a seat whose cue is live drains what is waiting and returns
// at once instead of polling. Unparseable arguments are left for toolWait.
func pushOnlyWaitArgs(raw json.RawMessage) json.RawMessage {
	m := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &m); err != nil {
			return raw
		}
	}
	m["timeout_seconds"] = 1
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
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
	pushOnly := p.Name == "wait_for_message" && s.cue != nil && s.cue.pushes()
	if pushOnly {
		p.Args = pushOnlyWaitArgs(p.Args)
		s.cue.notePushOnly()
	}
	done := s.cueBefore(p.Name, p.Args)
	// A wait holds what it fetches and acks only after its reply is written
	// (ackafter.go); the other tools ack nothing.
	dctx, al := ctx, (*ackLater)(nil)
	if p.Name == "wait_for_message" {
		dctx, al = withAckLater(ctx)
	}
	res, err := dispatchTool(dctx, s.bus, p.Name, p.Args)
	if m, ok := res.(map[string]any); ok && pushOnly {
		m["push"] = pushOnlyNote
	}
	done(res)
	if s.testBeforeReply != nil {
		s.testBeforeReply(ctx)
	}
	if al != nil && ctx.Err() != nil {
		// Cancelled before the reply was written: MCP says a cancelled request
		// gets none, so the mail it fetched goes back unread and is redelivered.
		s.giveBack(al, "call "+callKey(req.ID)+" cancelled before its reply")
		return
	}
	if err != nil && ctx.Err() != nil {
		// Cancelled: MCP 2025-06-18 says a cancelled request gets no response.
		s.log("call %s cancelled: %v", callKey(req.ID), err)
		return
	}
	if err != nil {
		if al != nil {
			s.giveBack(al, "call "+callKey(req.ID)+" failed")
		}
		// A tool-level failure is reported inside the result with isError,
		// per MCP, so the model sees the failure rather than an RPC fault.
		s.reply(req.ID, map[string]any{
			"content": []map[string]any{{"type": "text", "text": err.Error()}},
			"isError": true,
		})
		return
	}
	blob, _ := json.MarshalIndent(res, "", "  ")
	werr := s.reply(req.ID, map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(blob)}},
	})
	if werr != nil && al != nil {
		// The reply did not leave, so nothing it carried is acked: the mail goes
		// back unread, as for a cancelled call, and the broker redelivers it.
		s.giveBack(al, "call "+callKey(req.ID)+" reply write failed ("+werr.Error()+")")
		return
	}
	if al != nil {
		items := al.items()
		al.ack(s.bus, s.log)
		s.afterReply(callKey(req.ID), items)
	}
}

// giveBack hands a call's mail back when its reply will not be written: what it
// fetched is Nak'd and the broker redelivers it, and what it took from the
// returned queue goes back on it.
func (s *Server) giveBack(al *ackLater, why string) {
	n := al.nak()
	al.mu.Lock()
	carried := al.carried
	al.carried = nil
	al.mu.Unlock()
	s.bus.returnMail(carried, why, s.log)
	if n > 0 {
		s.log("%s: %d fetched message(s) given back unacked", why, n)
	}
}

// afterReply records what a written reply carried. A cancel that reached the
// call after its pre-write check is honoured here: the client may have dropped
// the reply, so the mail goes back to the next wait.
func (s *Server) afterReply(key string, items []drained) {
	if len(items) == 0 {
		return
	}
	s.callsMu.Lock()
	e := s.calls[key]
	if e == nil {
		s.callsMu.Unlock()
		return
	}
	e.replied = time.Now()
	if e.cancelled {
		s.callsMu.Unlock()
		s.bus.returnMail(items, "cancel crossed the reply of call "+key, s.log)
		return
	}
	e.items = items
	s.callsMu.Unlock()
}

// rememberAnsweredLocked keeps a replied call's entry for the late-cancel
// window and drops the ones past it. callsMu is held.
func (s *Server) rememberAnsweredLocked(key string, e *callEntry) {
	if s.answered == nil {
		s.answered = map[string]*callEntry{}
	}
	w := s.lateCancelWindow()
	for k, a := range s.answered {
		if time.Since(a.replied) > w {
			delete(s.answered, k)
		}
	}
	s.answered[key] = e
}

// lateCancelWindow is the late_cancel_window row of the threshold table.
func (s *Server) lateCancelWindow() time.Duration {
	if s.lateWindow > 0 {
		return s.lateWindow
	}
	return defaultThresholds[thresholdLateCancelWindow]
}

func idsOf(items []drained) string {
	var ids []string
	for _, it := range items {
		ids = append(ids, messageIDOf(it.Env))
	}
	return strings.Join(ids, ", ")
}

// cueBefore tells the cue about a tool call before it runs and returns what to
// do after: a wait brackets the open-wait count, an inbox_summary settles the
// receipt, reads any cue_ack, and carries the cue state in its result.
func (s *Server) cueBefore(name string, args json.RawMessage) func(any) {
	if s.cue == nil {
		return func(any) {}
	}
	switch name {
	case "wait_for_message":
		if s.bus != nil && s.bus.waitIsOpen() {
			// toolWait refuses this call and reads nothing: it answers the
			// cue's receipt and does not count as an open wait.
			s.cue.answered()
			return func(any) {}
		}
		s.cue.waitBegin()
		return func(any) { s.cue.waitEnd() }
	case "inbox_summary":
		s.cue.answered()
		if n := cueAckArg(args); n != "" {
			s.cue.ack(n)
		}
		return func(res any) {
			if m, ok := res.(map[string]any); ok {
				m["cue"] = s.cue.status()
			}
		}
	}
	return func(any) {}
}

// notifyCue writes one notifications/claude/channel line under outMu.
func (s *Server) notifyCue(params map[string]any) {
	s.write(map[string]any{"jsonrpc": "2.0", "method": "notifications/claude/channel", "params": params})
}

func (s *Server) reply(id json.RawMessage, result any) error {
	if len(id) == 0 {
		return nil
	}
	return s.write(rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *Server) replyErr(id json.RawMessage, code int, msg string) {
	s.write(rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
}

// write sends one line and reports whether it left: a write or flush error
// means the client did not get it, which callTool needs before it acks mail.
func (s *Server) write(v any) error {
	s.outMu.Lock()
	defer s.outMu.Unlock()
	b, _ := json.Marshal(v)
	if _, err := fmt.Fprintf(s.out, "%s\n", b); err != nil {
		return err
	}
	return s.out.Flush()
}
