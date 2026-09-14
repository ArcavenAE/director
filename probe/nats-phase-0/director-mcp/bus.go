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
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/oklog/ulid/v2"
)

type Bus struct {
	nc       *nats.Conn
	js       jetstream.JetStream
	kv       jetstream.KeyValue
	self     Sender
	consumer jetstream.Consumer

	// instance is a per-session id minted at startup. Two sessions launched
	// with the same DIRECTOR_AGENT_ID (the michael collision, R-49) still get
	// distinct instances, so their durable consumers and presence keys do not
	// collapse into one (R-50). pid is recorded for the roster.
	instance string
	pid      int

	// state is the last presence state the model set; the heartbeat re-writes
	// it on the shim's own timer (R-56), never on a model tool call.
	stateMu sync.Mutex
	state   string
}

// dial opens the NATS connection and JetStream context, presenting the R-77
// credential the launcher supplied (if any). Shared by connect() and
// preflight() so both reach the broker the same way with the same credential.
//
//	DIRECTOR_NATS_CREDS  path to a .creds file (NKey/JWT), wins when set
//	DIRECTOR_NATS_USER   user name, with DIRECTOR_NATS_PASS, when no creds file
//
// director#4 (R-77): the broker's authorization block binds a credential to
// the subjects it may use, so a process holding the ops credential cannot act
// as another team. Backward-compatible: with neither variable set the shim
// connects anonymously, so this lands without a flag day. Activation is the
// coordinated relaunch that flips the broker to require auth with every
// session presenting a credential at once; the broker is not hot-reloaded with
// auth under a live fleet.
func dial(url string, self Sender) (*nats.Conn, jetstream.JetStream, error) {
	opts := []nats.Option{nats.Name("director-mcp/" + self.AgentID)}
	if creds := os.Getenv("DIRECTOR_NATS_CREDS"); creds != "" {
		opts = append(opts, nats.UserCredentials(creds))
	} else if user := os.Getenv("DIRECTOR_NATS_USER"); user != "" {
		opts = append(opts, nats.UserInfo(user, os.Getenv("DIRECTOR_NATS_PASS")))
	}
	nc, err := nats.Connect(url, opts...)
	if err != nil {
		return nil, nil, err
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, nil, err
	}
	return nc, js, nil
}

// preflight verifies the broker is reachable and provisioned before the
// harness starts: the credential connects, the AGENT_STATE presence bucket
// exists, and the AGENT_INBOX stream exists. It creates NO durable consumer,
// so a repeated preflight leaves nothing behind (unlike connect, so it does
// not feed the orphaned-consumer accumulation of R-50 and aae-orc-8mcnf).
// cast-launch runs it before exec claude and exits nonzero on failure, so an
// unprovisioned or unreachable bus crashes the pane and marvel backs off
// loudly, rather than the harness starting shimless under --strict-mcp-config
// with the session reporting running and no presence (finding-166, candidate
// R-93).
func preflight(ctx context.Context, url string, self Sender) error {
	nc, js, err := dial(url, self)
	if err != nil {
		return fmt.Errorf("broker unreachable at %s: %w", url, err)
	}
	defer nc.Close()
	if _, err := js.KeyValue(ctx, "AGENT_STATE"); err != nil {
		return fmt.Errorf("presence KV AGENT_STATE missing: %w (provision the broker first; the streams and KV come from the broker setup, not the shim)", err)
	}
	if _, err := js.Stream(ctx, "AGENT_INBOX"); err != nil {
		return fmt.Errorf("stream AGENT_INBOX missing: %w (provision the broker first)", err)
	}
	return nil
}

func connect(ctx context.Context, url string, self Sender) (*Bus, error) {
	nc, js, err := dial(url, self)
	if err != nil {
		return nil, err
	}
	kv, err := js.KeyValue(ctx, "AGENT_STATE")
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("presence KV AGENT_STATE: %w (run the broker setup first)", err)
	}
	b := &Bus{nc: nc, js: js, kv: kv, self: self, instance: ulid.Make().String(), pid: os.Getpid(), state: "idle"}
	// Durable per-SESSION consumer. The durable name includes the per-session
	// instance, so two sessions sharing one agent id do not bind one durable
	// and race each other's mail (R-50); each gets its own copy instead of a
	// silent loss. Cost, accepted for the probe: a fresh instance replays the
	// stream under DeliverAll, so a restart re-reads history.
	cons, err := js.CreateOrUpdateConsumer(ctx, "AGENT_INBOX", jetstream.ConsumerConfig{
		Durable:       "mcp_" + self.AgentID + "_" + b.instance,
		FilterSubject: b.inboxSubject(self.Workspace, self.Team, self.AgentID),
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

// validToken enforces the closed identity character class, [A-Za-z0-9_-], on
// every string that becomes a NATS subject token or a presence key segment.
// It validates and rejects; it never rewrites (director#3, R-76).
//
// The class is exactly what is safe as ONE subject token: no "." (the token
// separator), no "*" or ">" (wildcards), no whitespace. The prior sanitize()
// rewrote those characters to "_", but only in the durable consumer name and
// the presence key, not in the three subject builders below. That split let
// an id of "*" build the filter subject agent.<ws>.<team>.*.inbox and read
// every inbox on the team, and let "ops.planner" and "ops_planner" share one
// presence row while routing to two inboxes. A token that is legal everywhere
// or rejected before any subject is built removes the split.
func validToken(kind, s string) error {
	if s == "" {
		return fmt.Errorf("%s is empty; an identity is assigned at spawn, never blank (director#3, R-78)", kind)
	}
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-'
		if !ok {
			return fmt.Errorf("%s %q contains %q; the identity class is [A-Za-z0-9_-] and a value is rejected, not rewritten, because a dot, star or right angle bracket would build a subject that is not yours (director#3, R-76)", kind, s, string(r))
		}
	}
	return nil
}

// validateIdentity checks the launcher-assigned identity levers before any
// subject is built, so a malformed id fails loud at spawn (R-78) instead of
// reaching the bus.
func validateIdentity(self Sender) error {
	if err := validToken("DIRECTOR_WORKSPACE", self.Workspace); err != nil {
		return err
	}
	if err := validToken("DIRECTOR_TEAM", self.Team); err != nil {
		return err
	}
	return validToken("DIRECTOR_AGENT_ID", self.AgentID)
}

// The subject builders take the workspace explicitly rather than reading
// b.self.Workspace, because a directed subject names the RECIPIENT's workspace,
// not the sender's (R-92). For the self-subscribe in connect() that workspace
// is b.self.Workspace; for a send it is the recipient's, resolved from the
// roster (or supplied verbatim by the tools' optional workspace argument).
func (b *Bus) inboxSubject(workspace, team, id string) string {
	return fmt.Sprintf("agent.%s.%s.%s.inbox", workspace, team, id)
}

func (b *Bus) roleSubject(workspace, team, role string) string {
	return fmt.Sprintf("agent.%s.%s.role.%s.inbox", workspace, team, role)
}

func (b *Bus) broadcastSubject(workspace, team string) string {
	if team == "" {
		return fmt.Sprintf("agent.%s.broadcast", workspace)
	}
	return fmt.Sprintf("agent.%s.%s.broadcast", workspace, team)
}

// resolveSubject turns a director address into a NATS subject. A directed
// subject (agent://, role://) is built from the RECIPIENT's workspace, not the
// sender's (R-92): the address carries only {team}/{id} or {team}/{role}, so
// the workspace is resolved from the recipient's live presence, or supplied
// verbatim by wsHint (the tools' optional workspace argument, for a cold
// mailbox with no live presence). A recipient no live session would consume,
// or one live in more than one workspace, is refused here before publish
// rather than sent to a subject nobody filters (R-09). wsHint is ignored for
// broadcast, whose address already carries the workspace.
func (b *Bus) resolveSubject(ctx context.Context, addr, wsHint string) (subject string, durable bool, err error) {
	switch {
	case strings.HasPrefix(addr, "agent://"):
		rest := strings.TrimPrefix(addr, "agent://")
		team, id, ok := strings.Cut(rest, "/")
		if !ok {
			return "", false, errors.New("agent address must be agent://{team}/{id}")
		}
		// The send target is validated too (director#3): a "." or "*" in a
		// target id would add subject tokens or a wildcard, so it is refused
		// here rather than published to a subject the sender did not name. This
		// runs before any workspace resolution, so a wildcard target is refused
		// whether or not the roster has a match.
		if err := validToken("agent team", team); err != nil {
			return "", false, err
		}
		if err := validToken("agent id", id); err != nil {
			return "", false, err
		}
		ws, err := b.subjectWorkspace(ctx, wsHint, "presence."+team+"."+id+".", addr)
		if err != nil {
			return "", false, err
		}
		return b.inboxSubject(ws, team, id), true, nil
	case strings.HasPrefix(addr, "role://"):
		rest := strings.TrimPrefix(addr, "role://")
		team, role, ok := strings.Cut(rest, "/")
		if !ok {
			return "", false, errors.New("role address must be role://{team}/{role}")
		}
		if err := validToken("role team", team); err != nil {
			return "", false, err
		}
		if err := validToken("role", role); err != nil {
			return "", false, err
		}
		// A role has no id in the presence key, so the workspace is resolved
		// over the team's live members (presence.<team>.*): they share one
		// workspace, or the send is ambiguous and refused.
		ws, err := b.subjectWorkspace(ctx, wsHint, "presence."+team+".", addr)
		if err != nil {
			return "", false, err
		}
		return b.roleSubject(ws, team, role), true, nil
	case strings.HasPrefix(addr, "broadcast://"):
		rest := strings.TrimPrefix(addr, "broadcast://")
		ws, team, _ := strings.Cut(rest, "/") // broadcast://{ws}[/{team}]
		// The broadcast workspace comes from the address, not b.self.Workspace,
		// so a sender in one workspace can broadcast into another; the old
		// builder ignored the address's workspace, the same defect class R-92
		// fixes for directed sends.
		if err := validToken("broadcast workspace", ws); err != nil {
			return "", false, err
		}
		if team != "" {
			if err := validToken("broadcast team", team); err != nil {
				return "", false, err
			}
		}
		return b.broadcastSubject(ws, team), false, nil
	}
	return "", false, errors.New("unroutable address: " + addr)
}

// subjectWorkspace picks the workspace a directed subject is built from. An
// explicit hint (the tools' optional workspace argument) wins and is used
// verbatim, so a cold mailbox with no live presence can still be addressed;
// with no hint the workspace is resolved from the recipient's live presence
// (R-92). The chosen workspace is validated as a subject token either way, so
// neither an untrusted hint nor a roster record with a malformed workspace can
// build a stray subject.
func (b *Bus) subjectWorkspace(ctx context.Context, hint, prefix, addr string) (string, error) {
	ws := hint
	if ws == "" {
		resolved, err := b.resolveRecipientWorkspace(ctx, prefix, addr)
		if err != nil {
			return "", err
		}
		ws = resolved
	}
	if err := validToken("workspace", ws); err != nil {
		return "", err
	}
	return ws, nil
}

// resolveRecipientWorkspace reads the presence keys under prefix and returns
// the single workspace the recipient is live in. This is the R-92 fix: the
// recipient's own presence record names its inbox workspace, so a
// cross-workspace send lands where a consumer actually filters, instead of on
// a subject built from the sender's workspace that nobody reads.
func (b *Bus) resolveRecipientWorkspace(ctx context.Context, prefix, addr string) (string, error) {
	keys, err := b.kv.Keys(ctx)
	if err != nil {
		if errors.Is(err, jetstream.ErrNoKeysFound) {
			return "", noPresenceErr(addr)
		}
		return "", err
	}
	var workspaces []string
	for _, k := range keys {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		entry, err := b.kv.Get(ctx, k)
		if err != nil {
			continue
		}
		var rec map[string]any
		if json.Unmarshal(entry.Value(), &rec) != nil {
			continue
		}
		if ws, _ := rec["workspace"].(string); ws != "" {
			workspaces = append(workspaces, ws)
		}
	}
	return pickWorkspace(addr, workspaces)
}

func noPresenceErr(addr string) error {
	return fmt.Errorf("no live presence for %s; no session would consume this send, so it is refused rather than sent to a subject nobody filters (R-92)", addr)
}

// pickWorkspace collapses the workspaces of a recipient's live presence records
// to the one that names its subject. Zero is a refusal (no live consumer), two
// or more is a refusal (ambiguous, and it lists them), both loud before publish
// (R-09); exactly one resolves. Split from the KV read so it is unit-testable
// without a broker.
func pickWorkspace(addr string, workspaces []string) (string, error) {
	seen := map[string]bool{}
	for _, ws := range workspaces {
		seen[ws] = true
	}
	switch len(seen) {
	case 1:
		for ws := range seen {
			return ws, nil
		}
	case 0:
		return "", noPresenceErr(addr)
	}
	list := make([]string, 0, len(seen))
	for ws := range seen {
		list = append(list, ws)
	}
	sort.Strings(list)
	return "", fmt.Errorf("recipient %s is live in more than one workspace (%s); pass an explicit workspace to disambiguate rather than guess (R-92)", addr, strings.Join(list, ", "))
}

// publish sends the envelope to its recipient subject and mirrors it to the
// audit stream. Dedupe rides on Nats-Msg-Id = message_id (R-13, verified at
// transport in sub-probe 2). Returns "accepted for delivery" semantics only:
// this is a send acknowledgement, never a delivery or read one (R-08). wsHint
// is the tools' optional workspace argument, passed through to resolveSubject
// for a cold mailbox; empty means resolve the recipient's workspace from the
// roster. A resolution refusal (R-92) returns here before any subject is built,
// so nothing lands on the inbox or the audit stream.
func (b *Bus) publish(ctx context.Context, e *Envelope, wsHint string) error {
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if len(body) > maxEnvelopeBytes {
		return fmt.Errorf("envelope %d bytes exceeds 64 KiB; use content.refs pointers", len(body))
	}
	subject, durable, err := b.resolveSubject(ctx, e.Recipient.Address, wsHint)
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
	b.stateMu.Lock()
	b.state = state
	b.stateMu.Unlock()
	return b.writePresence(ctx, state)
}

// writePresence puts the presence record under a per-session key
// (presence.<team>.<id>.<instance>), so N concurrent sessions under one id show
// as N roster entries instead of last-writer-wins collapsing them to one.
func (b *Bus) writePresence(ctx context.Context, state string) error {
	rec := map[string]any{
		"agent_id":  b.self.AgentID,
		"team":      b.self.Team,
		"workspace": b.self.Workspace,
		"instance":  b.instance,
		"pid":       b.pid,
		"state":     state,
		"ts":        time.Now().UTC().Format(time.RFC3339),
	}
	body, _ := json.Marshal(rec)
	key := "presence." + b.self.Team + "." + b.self.AgentID + "." + b.instance
	_, err := b.kv.Put(ctx, key, body)
	return err
}

// heartbeat renews presence on the shim's own timer, never on a model tool
// call (R-56). A model turn can run for minutes; if renewal rode the model's
// poll, a long turn would let the presence key expire while the session is
// alive, which is exactly what an empty roster beside a live shim showed.
func (b *Bus) heartbeat(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b.stateMu.Lock()
			s := b.state
			b.stateMu.Unlock()
			_ = b.writePresence(ctx, s)
		}
	}
}

// checkCollision looks for another live session already present under this same
// team and agent id but a different instance. It returns a human-readable
// warning when it finds one, so an accidental duplicate id (R-49) is loud
// rather than silent. It does not refuse; the probe observes rather than blocks.
func (b *Bus) checkCollision(ctx context.Context) string {
	keys, err := b.kv.Keys(ctx)
	if err != nil {
		return ""
	}
	prefix := "presence." + b.self.Team + "." + b.self.AgentID + "."
	for _, k := range keys {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		entry, err := b.kv.Get(ctx, k)
		if err != nil {
			continue
		}
		var rec map[string]any
		if json.Unmarshal(entry.Value(), &rec) != nil {
			continue
		}
		if inst, _ := rec["instance"].(string); inst != "" && inst != b.instance {
			return fmt.Sprintf("another session is live under agent://%s/%s (instance %v, pid %v); addresses collide, assign a distinct id at spawn (R-49)",
				b.self.Team, b.self.AgentID, rec["instance"], rec["pid"])
		}
	}
	return ""
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
