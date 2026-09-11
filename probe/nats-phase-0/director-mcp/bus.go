package main

// The NATS binding. Address resolution (agent:// role:// broadcast://) to
// subjects per the envelope doc section 2.5, publish with dedupe on
// message_id, a durable per-agent consumer for the long-poll receive, and the
// presence KV. Everything here is the transport director will own; the MCP
// layer above it is deliberately thin.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

type Bus struct {
	nc       *nats.Conn
	js       jetstream.JetStream
	kv       jetstream.KeyValue
	self     Sender
	consumer jetstream.Consumer
}

func connect(ctx context.Context, url string, self Sender) (*Bus, error) {
	nc, err := nats.Connect(url, nats.Name("director-mcp/"+self.AgentID))
	if err != nil {
		return nil, err
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, err
	}
	kv, err := js.KeyValue(ctx, "AGENT_STATE")
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("presence KV AGENT_STATE: %w (run the broker setup first)", err)
	}
	b := &Bus{nc: nc, js: js, kv: kv, self: self}
	// Durable per-agent consumer on this agent's own inbox subject. This is
	// the store-and-forward receive: messages sent while we were offline
	// replay from the stream when we first pull.
	cons, err := js.CreateOrUpdateConsumer(ctx, "AGENT_INBOX", jetstream.ConsumerConfig{
		Durable:       "mcp_" + sanitize(self.AgentID),
		FilterSubject: b.inboxSubject(self.Team, self.AgentID),
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		MaxDeliver:    -1,
	})
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("durable consumer: %w", err)
	}
	b.consumer = cons
	return b, nil
}

func sanitize(s string) string {
	return strings.NewReplacer(".", "_", "*", "_", ">", "_", " ", "_").Replace(s)
}

func (b *Bus) inboxSubject(team, id string) string {
	return fmt.Sprintf("agent.%s.%s.%s.inbox", b.self.Workspace, team, id)
}

func (b *Bus) roleSubject(team, role string) string {
	return fmt.Sprintf("agent.%s.%s.role.%s.inbox", b.self.Workspace, team, role)
}

func (b *Bus) broadcastSubject(team string) string {
	if team == "" {
		return fmt.Sprintf("agent.%s.broadcast", b.self.Workspace)
	}
	return fmt.Sprintf("agent.%s.%s.broadcast", b.self.Workspace, team)
}

// resolveSubject turns a director address into a NATS subject. role:// binding
// without marvel is a static lookup; in the probe it maps to the role inbox
// subject and any holder consuming that subject receives it.
func (b *Bus) resolveSubject(addr string) (subject string, durable bool, err error) {
	switch {
	case strings.HasPrefix(addr, "agent://"):
		rest := strings.TrimPrefix(addr, "agent://")
		team, id, ok := strings.Cut(rest, "/")
		if !ok {
			return "", false, errors.New("agent address must be agent://{team}/{id}")
		}
		return b.inboxSubject(team, id), true, nil
	case strings.HasPrefix(addr, "role://"):
		rest := strings.TrimPrefix(addr, "role://")
		team, role, ok := strings.Cut(rest, "/")
		if !ok {
			return "", false, errors.New("role address must be role://{team}/{role}")
		}
		return b.roleSubject(team, role), true, nil
	case strings.HasPrefix(addr, "broadcast://"):
		rest := strings.TrimPrefix(addr, "broadcast://")
		_, team, _ := strings.Cut(rest, "/") // broadcast://{ws}[/{team}]
		return b.broadcastSubject(team), false, nil
	}
	return "", false, errors.New("unroutable address: " + addr)
}

// publish sends the envelope to its recipient subject and mirrors it to the
// audit stream. Dedupe rides on Nats-Msg-Id = message_id (R-13, verified at
// transport in sub-probe 2). Returns "accepted for delivery" semantics only:
// this is a send acknowledgement, never a delivery or read one (R-08).
func (b *Bus) publish(ctx context.Context, e *Envelope) error {
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if len(body) > maxEnvelopeBytes {
		return fmt.Errorf("envelope %d bytes exceeds 64 KiB; use content.refs pointers", len(body))
	}
	subject, durable, err := b.resolveSubject(e.Recipient.Address)
	if err != nil {
		return err
	}
	msg := &nats.Msg{Subject: subject, Data: body, Header: nats.Header{}}
	msg.Header.Set(jetstream.MsgIDHeader, e.MessageID)
	if durable {
		if _, err := b.js.PublishMsg(ctx, msg); err != nil {
			return fmt.Errorf("publish to %s: %w", subject, err)
		}
	} else {
		// broadcast is core NATS, no durable queue (section 2.5)
		if err := b.nc.PublishMsg(msg); err != nil {
			return err
		}
	}
	// audit: every envelope lands on the append-only stream (section 2.3)
	amsg := &nats.Msg{Subject: "agent.audit", Data: body, Header: nats.Header{}}
	amsg.Header.Set(jetstream.MsgIDHeader, e.MessageID)
	_, _ = b.js.PublishMsg(ctx, amsg) // audit is best-effort in the probe
	return nil
}

// receive is the long-poll. It pulls the next message for this agent from its
// durable consumer, waiting up to timeout, and acks it. This is the shape the
// probe set out to measure: the agent CALLS to receive, because MCP cannot
// push into the model's context (see PROGRESS.md sub-probe 3).
func (b *Bus) receive(ctx context.Context, timeout time.Duration) (*Envelope, error) {
	msgs, err := b.consumer.Fetch(1, jetstream.FetchMaxWait(timeout))
	if err != nil {
		return nil, err
	}
	for m := range msgs.Messages() {
		var e Envelope
		if err := json.Unmarshal(m.Data(), &e); err != nil {
			_ = m.Term() // poison message: do not redeliver a thing we cannot parse
			return nil, fmt.Errorf("undecodable message on inbox: %w", err)
		}
		_ = m.Ack()
		return &e, nil
	}
	if err := msgs.Error(); err != nil {
		return nil, err
	}
	return nil, nil // timed out, no message: a clean empty, not an error
}

// setPresence writes this agent's heartbeat into the presence KV. The bucket
// TTL expires the key if no heartbeat lands within the window, so absence is
// silence, not a message (section 2.1: presence is a transport concern).
func (b *Bus) setPresence(ctx context.Context, state string) error {
	rec := map[string]any{
		"agent_id":  b.self.AgentID,
		"team":      b.self.Team,
		"workspace": b.self.Workspace,
		"state":     state,
		"ts":        time.Now().UTC().Format(time.RFC3339),
	}
	body, _ := json.Marshal(rec)
	_, err := b.kv.Put(ctx, "presence."+sanitize(b.self.Team)+"."+sanitize(b.self.AgentID), body)
	return err
}

// roster reads the presence KV and returns every live entry.
func (b *Bus) roster(ctx context.Context) ([]map[string]any, error) {
	keys, err := b.kv.Keys(ctx)
	if err != nil {
		if errors.Is(err, jetstream.ErrNoKeysFound) {
			return nil, nil
		}
		return nil, err
	}
	var out []map[string]any
	for _, k := range keys {
		entry, err := b.kv.Get(ctx, k)
		if err != nil {
			continue
		}
		var rec map[string]any
		if json.Unmarshal(entry.Value(), &rec) == nil {
			out = append(out, rec)
		}
	}
	return out, nil
}

func (b *Bus) close() {
	if b.nc != nil {
		b.nc.Drain()
	}
}
