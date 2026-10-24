package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// T1: tools/call runs beside the read loop, so a ping or a cancel is read
// while a wait blocks. These run the server over two pipes against a scratch
// nats-server on loopback (startScratchServer), never a live bus.

type pipeRig struct {
	t     *testing.T
	ctx   context.Context
	s     *Server
	bus   *Bus
	js    jetstream.JetStream
	in    *io.PipeWriter
	lines chan map[string]any
	done  chan error
	inbox string
}

func newPipeRig(t *testing.T, global bool) *pipeRig {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	self := Sender{AgentID: "operator", Workspace: "w", Team: "ops"}
	var gcfg *globalConfig
	if global {
		gcfg = &globalConfig{Domain: "global", Cluster: "c1", Role: roleDirector}
	}
	bus, err := connect(ctx, url, self, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bus.close)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := &Server{bus: bus, out: bufio.NewWriter(outW), tools: toolCatalog(gcfg, false), log: func(string, ...any) {}}
	r := &pipeRig{t: t, ctx: ctx, s: s, bus: bus, js: js, in: inW, lines: make(chan map[string]any, 64), done: make(chan error, 1), inbox: "agent.w.ops.operator.inbox"}
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
		for sc.Scan() {
			var m map[string]any
			if err := json.Unmarshal(sc.Bytes(), &m); err == nil {
				r.lines <- m
			}
		}
		close(r.lines)
	}()
	go func() { r.done <- s.serve(ctx, inR) }()
	t.Cleanup(func() {
		_ = inW.Close()
		select {
		case <-r.done:
		case <-time.After(5 * time.Second):
			t.Error("serve did not return after stdin closed")
		}
		_ = outW.Close()
	})
	return r
}

func (r *pipeRig) send(line string) {
	r.t.Helper()
	if _, err := io.WriteString(r.in, line+"\n"); err != nil {
		r.t.Fatal(err)
	}
}

func (r *pipeRig) wait(id string, timeout, max int) {
	r.send(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"method":"tools/call","params":{"name":"wait_for_message","arguments":{"timeout_seconds":%d,"max":%d}}}`, id, timeout, max))
}

func (r *pipeRig) cancel(id string) {
	r.send(fmt.Sprintf(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":%s,"reason":"test"}}`, id))
}

// next returns the next line the server wrote, or fails after d.
func (r *pipeRig) next(d time.Duration) map[string]any {
	r.t.Helper()
	select {
	case m, ok := <-r.lines:
		if !ok {
			r.t.Fatal("server output closed")
		}
		return m
	case <-time.After(d):
		r.t.Fatalf("no line from the server within %s", d)
	}
	return nil
}

func (r *pipeRig) publish(id string) {
	r.t.Helper()
	pubEnv(r.t, r.ctx, r.js, r.inbox, id, "INFORM", "hello "+id)
}

// untilInFlight waits until the server reports n calls in flight.
func (r *pipeRig) untilInFlight(n int, d time.Duration) bool {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if r.s.inFlight() == n {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return r.s.inFlight() == n
}

func idOf(m map[string]any) string {
	b, _ := json.Marshal(m["id"])
	return string(b)
}

// toolText is the text of a tools/call result.
func toolText(t *testing.T, m map[string]any) string {
	t.Helper()
	res, _ := m["result"].(map[string]any)
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("no tool content in %v", m)
	}
	c, _ := content[0].(map[string]any)
	s, _ := c["text"].(string)
	return s
}

func TestPingDuringABlockedWaitIsAnsweredFirst(t *testing.T) {
	for _, global := range []bool{false, true} {
		t.Run(fmt.Sprintf("global=%v", global), func(t *testing.T) {
			r := newPipeRig(t, global)
			r.wait(`"w1"`, 3, 1)
			if !r.untilInFlight(1, 2*time.Second) {
				t.Fatalf("the wait is not in flight: %d", r.s.inFlight())
			}
			start := time.Now()
			r.send(`{"jsonrpc":"2.0","id":"p1","method":"ping"}`)
			first := r.next(2 * time.Second)
			if got := idOf(first); got != `"p1"` {
				t.Fatalf("first line is %s, want the ping's answer: %v", got, first)
			}
			if el := time.Since(start); el > time.Second {
				t.Fatalf("ping answered after %s, behind the wait", el)
			}
			if got := idOf(r.next(5 * time.Second)); got != `"w1"` {
				t.Fatalf("second line is %s, want the wait's answer", got)
			}
		})
	}
}

func TestCancelEndsAWaitWithinOneSecondAndConsumesNothing(t *testing.T) {
	for _, global := range []bool{false, true} {
		t.Run(fmt.Sprintf("global=%v", global), func(t *testing.T) {
			r := newPipeRig(t, global)
			r.wait(`9`, 30, 1)
			if !r.untilInFlight(1, 2*time.Second) {
				t.Fatalf("the wait is not in flight: %d", r.s.inFlight())
			}
			r.cancel(`9`)
			if !r.untilInFlight(0, time.Second) {
				t.Fatalf("the cancelled wait is still in flight after 1 s: %d", r.s.inFlight())
			}
			// No answer for the cancelled id: the ping's answer is the next line.
			r.send(`{"jsonrpc":"2.0","id":"p2","method":"ping"}`)
			if got := idOf(r.next(2 * time.Second)); got != `"p2"` {
				t.Fatalf("a cancelled call got an answer (%s); MCP says a cancelled request gets none", got)
			}
			// Nothing was consumed: a message published now is the next wait's.
			r.publish("after-cancel")
			r.wait(`10`, 5, 1)
			m := r.next(6 * time.Second)
			if idOf(m) != `10` || !strings.Contains(toolText(t, m), "after-cancel") {
				t.Fatalf("the next wait did not get the message: %v", m)
			}
		})
	}
}

func TestResponseIDsAreKeptUnderInterleaving(t *testing.T) {
	r := newPipeRig(t, false)
	r.wait(`"a"`, 2, 1)
	r.wait(`7`, 1, 1)
	if !r.untilInFlight(2, 2*time.Second) {
		t.Fatalf("two waits are not in flight: %d", r.s.inFlight())
	}
	r.send(`{"jsonrpc":"2.0","id":"p","method":"ping"}`)
	r.send(`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`)
	got := map[string]int{}
	var order []string
	for i := 0; i < 4; i++ {
		m := r.next(5 * time.Second)
		id := idOf(m)
		got[id]++
		order = append(order, id)
	}
	for _, id := range []string{`"a"`, `7`, `"p"`, `3`} {
		if got[id] != 1 {
			t.Fatalf("id %s answered %d times; order %v", id, got[id], order)
		}
	}
	if order[0] != `"p"` {
		t.Fatalf("ping not answered first: %v", order)
	}
}

// A cancel that lands after the fetch and before the ack: the message is
// Nak'd and redelivered, never lost.
func TestCancelBeforeTheAckRedelivers(t *testing.T) {
	r := newPipeRig(t, false)
	var once sync.Once
	r.bus.testAfterFetch = func(ctx context.Context) {
		fired := false
		once.Do(func() { fired = true })
		if !fired {
			return
		}
		r.cancel(`11`)
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
			t.Error("the cancel did not reach the call")
		}
	}
	r.wait(`11`, 10, 1)
	if !r.untilInFlight(1, 2*time.Second) {
		t.Fatal("the wait is not in flight")
	}
	r.publish("nak-me")
	if !r.untilInFlight(0, 3*time.Second) {
		t.Fatalf("the cancelled wait is still in flight: %d", r.s.inFlight())
	}
	r.send(`{"jsonrpc":"2.0","id":"p3","method":"ping"}`)
	if got := idOf(r.next(2 * time.Second)); got != `"p3"` {
		t.Fatalf("a call cancelled before its ack got an answer (%s)", got)
	}
	r.wait(`12`, 5, 1)
	m := r.next(6 * time.Second)
	if idOf(m) != `12` || !strings.Contains(toolText(t, m), "nak-me") {
		t.Fatalf("the message was not redelivered to the next wait: %v", m)
	}
}

// A cancel that lands after the ack: the bus already took the message, so the
// answer goes out anyway (R-08: a message the bus took must not vanish).
func TestCancelAfterTheAckStillAnswers(t *testing.T) {
	r := newPipeRig(t, false)
	var once sync.Once
	r.bus.testAfterAck = func(ctx context.Context) {
		fired := false
		once.Do(func() { fired = true })
		if !fired {
			return
		}
		r.cancel(`13`)
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
			t.Error("the cancel did not reach the call")
		}
	}
	r.wait(`13`, 10, 1)
	if !r.untilInFlight(1, 2*time.Second) {
		t.Fatal("the wait is not in flight")
	}
	r.publish("keep-me")
	m := r.next(5 * time.Second)
	if idOf(m) != `13` || !strings.Contains(toolText(t, m), "keep-me") {
		t.Fatalf("a call cancelled after its ack must still answer with the message: %v", m)
	}
}

// On stdin EOF, serve cancels what is in flight and waits for it.
func TestServeWaitsForCallsInFlightOnEOF(t *testing.T) {
	r := newPipeRig(t, false)
	r.wait(`14`, 30, 1)
	if !r.untilInFlight(1, 2*time.Second) {
		t.Fatal("the wait is not in flight")
	}
	_ = r.in.Close()
	select {
	case <-r.done:
	case <-time.After(2 * time.Second):
		t.Fatal("serve did not return within 2 s of EOF with a 30 s wait open")
	}
	r.done <- nil // for the cleanup
	if n := r.s.inFlight(); n != 0 {
		t.Fatalf("serve returned with %d calls in flight", n)
	}
}
