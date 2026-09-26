package main

// The NATS binding. Address resolution (agent:// role:// broadcast://) to
// subjects per the envelope doc section 2.5, publish with dedupe on
// message_id, a durable per-agent consumer for the long-poll receive, and the
// presence KV. Everything here is the transport director will own; the MCP
// layer above it is deliberately thin.

import (
	"context"
	crand "crypto/rand"
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

	// The global tier (R-86), off unless the launcher set
	// DIRECTOR_GLOBAL_DOMAIN. globalCfg is the validated configuration and is
	// immutable; global is the attached hub context, which is built lazily so
	// a hub that is down at startup degrades to local-only and loud rather
	// than taking the session with it (brief 8 section 1: the local broker
	// keeps serving, a global publish fails loud). globalWarn holds the last
	// reported attach or presence failure so a hub outage logs on the
	// transition instead of every 30 seconds.
	globalCfg  *globalConfig
	globalMu   sync.Mutex
	global     *globalTier
	globalWarn string

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

	// globalFirst is which tier a call takes waiting mail from first. It
	// flips on every call, so a backlog on one tier cannot starve the other.
	turnMu      sync.Mutex
	globalFirst bool

	// resumed holds, per tier, where this instance's durable started and
	// what was waiting, reported once in the next wait_for_message result.
	resumedMu sync.Mutex
	resumed   []string
}

// localConsumerInactive is the local durable's inactive threshold. The inbox
// carries a 72h max age (docs/getting-started.md), so a durable cleaned up
// after a slightly longer idle window can only ever have replayed messages
// that already expired. It is what keeps superseded instances' durables from
// accumulating, the same reasoning as globalConsumerInactive.
const localConsumerInactive = 73 * time.Hour

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
// With the global tier configured it also verifies the hub half through the
// domain: the presence bucket and this session's own inbox stream. That is the
// same finding-166 argument one tier up. A supervisor cast with global mode on
// but no leaf link, or against a hub whose streams were never provisioned,
// would otherwise come up looking healthy and be unreachable from the director.
func preflight(ctx context.Context, url string, self Sender, gcfg *globalConfig) error {
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
	if gcfg == nil {
		return nil
	}
	return preflightGlobal(ctx, nc, *gcfg)
}

// instanceEntropy is the entropy behind the per-session instance id. It mirrors
// the shape of ulid's own package default, a monotonic reader behind a mutex,
// with one substitution that is the whole point: the source is crypto/rand
// rather than a math/rand stream seeded from the clock.
//
// ulid.Make() uses that package default, which seeds math/rand from
// time.Now().UnixNano() at package init (oklog/ulid/v2 v2.1.2, ulid.go:135-137).
// Two processes whose init lands in the same clock tick therefore draw the SAME
// stream and, called in the same millisecond, mint the SAME id. Measured on one
// host 2026-09-21: 47 collisions in 200 simultaneous pairs.
//
// The instance is not decorative, which is why this is worth a named source.
// The global presence key is presence.<cluster>.<role>.<instance> and carries
// no agent id, so two same-cluster same-role sessions that collide collapse
// into a single roster row; and the per-session durable is
// mcp_<agentID>_<instance>, which collapses with it, so the two sessions bind
// one durable and race each other's mail. That is the R-50 loss this naming
// exists to prevent.
//
// Staggering process starts also avoids the collision, and that is a
// workaround rather than a fix: it depends on timing nobody controls. See
// ArcavenAE/director#62 and finding-005-instance-ulid-collides-on-simultaneous-start.
var instanceEntropy = &ulid.LockedMonotonicReader{
	MonotonicReader: ulid.Monotonic(crand.Reader, 0),
}

// newInstanceID mints this session's instance id. Sortable by time, like
// ulid.Make(), without sharing a seed with any other process.
func newInstanceID() string {
	return ulid.MustNew(ulid.Now(), instanceEntropy).String()
}

func connect(ctx context.Context, url string, self Sender, gcfg *globalConfig) (*Bus, error) {
	nc, js, err := dial(url, self)
	if err != nil {
		return nil, err
	}
	kv, err := js.KeyValue(ctx, "AGENT_STATE")
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("presence KV AGENT_STATE: %w (run the broker setup first)", err)
	}
	b := &Bus{nc: nc, js: js, kv: kv, self: self, instance: newInstanceID(), pid: os.Getpid(), state: "idle", globalCfg: gcfg}
	// Durable per-SESSION consumer. The durable name includes the per-session
	// instance, so two sessions sharing one agent id do not bind one durable
	// and race each other's mail (R-50); each gets its own copy instead of a
	// silent loss. A new instance resumes after the last message a DEPARTED
	// instance of the same seat acked, so a reconnect does not replay the
	// whole inbox. A live sibling's position is not taken: a session joining
	// a live seat gets its own full copy (R-50 as implemented), not a start
	// after mail only the sibling read. A seat with no departed durable reads
	// everything the inbox still holds, so mail sent to a cold mailbox is
	// delivered.
	filter := b.inboxSubject(self.Workspace, self.Team, self.AgentID)
	cfg := jetstream.ConsumerConfig{
		Durable:           "mcp_" + self.AgentID + "_" + b.instance,
		FilterSubject:     filter,
		AckPolicy:         jetstream.AckExplicitPolicy,
		DeliverPolicy:     jetstream.DeliverAllPolicy,
		MaxDeliver:        -1,
		InactiveThreshold: localConsumerInactive,
	}
	liveLocal := func(instance string) bool {
		_, err := kv.Get(ctx, "presence."+self.Team+"."+self.AgentID+"."+instance)
		return err == nil
	}
	floor, _ := seatAckFloor(ctx, js, "AGENT_INBOX", "mcp_"+self.AgentID+"_", filter, liveLocal)
	if floor > 0 {
		cfg.DeliverPolicy = jetstream.DeliverByStartSequencePolicy
		cfg.OptStartSeq = floor + 1
	}
	cons, err := js.CreateOrUpdateConsumer(ctx, "AGENT_INBOX", cfg)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("durable consumer: %w", err)
	}
	b.consumer = cons
	b.noteResumed("local", floor, cons)
	return b, nil
}

// noteResumed records where a new durable started and how much was waiting,
// for the next wait_for_message result.
func (b *Bus) noteResumed(tier string, floor uint64, cons jetstream.Consumer) {
	var waiting uint64
	if info := cons.CachedInfo(); info != nil {
		waiting = info.NumPending
	}
	var msg string
	if floor > 0 {
		msg = fmt.Sprintf("%s inbox resumed after stream sequence %d, the seat's last ack, so mail already read is not replayed; %d message(s) waiting", tier, floor, waiting)
	} else {
		msg = fmt.Sprintf("%s inbox has no earlier durable for this seat, so it reads everything the stream still holds; %d message(s) waiting", tier, waiting)
	}
	b.resumedMu.Lock()
	b.resumed = append(b.resumed, msg)
	b.resumedMu.Unlock()
}

// takeResumed returns the pending resume notes once, then clears them.
func (b *Bus) takeResumed() []string {
	b.resumedMu.Lock()
	defer b.resumedMu.Unlock()
	r := b.resumed
	b.resumed = nil
	return r
}

// seatAckFloor returns the highest ack floor among the seat's departed
// durables on a stream: those named with the seat's prefix AND filtered on the
// seat's own subject, whose instance (the name after the prefix) has no live
// presence row. The subject check is what keeps "michael" from reading
// "michael-2"'s position; the name prefix alone would not. Skipping live
// instances keeps a joining session from starting after mail only its live
// sibling read. A crashed session's presence row lingers up to the bucket's
// 90s TTL, so a reconnect inside that window finds no departed floor and reads
// the whole inbox: a replay, never a loss. Zero means no departed durable, or
// none that acked anything. Best effort: an error means the caller falls back
// to delivering everything.
func seatAckFloor(ctx context.Context, js jetstream.JetStream, stream, prefix, filter string, live func(instance string) bool) (uint64, error) {
	st, err := js.Stream(ctx, stream)
	if err != nil {
		return 0, err
	}
	var floor uint64
	lister := st.ListConsumers(ctx)
	for info := range lister.Info() {
		if !strings.HasPrefix(info.Name, prefix) || info.Config.FilterSubject != filter {
			continue
		}
		if live != nil && live(strings.TrimPrefix(info.Name, prefix)) {
			continue
		}
		if info.AckFloor.Stream > floor {
			floor = info.AckFloor.Stream
		}
	}
	return floor, lister.Err()
}

// takeTurn returns which tier this call reads first, and flips it for the next.
func (b *Bus) takeTurn() (globalFirst bool) {
	b.turnMu.Lock()
	defer b.turnMu.Unlock()
	globalFirst = b.globalFirst
	b.globalFirst = !b.globalFirst
	return globalFirst
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
			// An empty bucket is a real reading, not a failed one.
			return pickWorkspace(addr, prefix, presenceScan{})
		}
		return "", err
	}
	return pickWorkspace(addr, prefix, b.scanPresence(ctx, keys, prefix))
}

// presenceScan is what one pass over the presence bucket actually established.
//
// The counts are the whole point. The resolver used to keep only the
// workspaces it recovered, and a row it could not read left no trace at all:
// a failed Get, an unparseable value and a record with no workspace field all
// became the same empty slice, and the refusal then announced that the
// recipient had no live presence. That is a claim the resolver was in no
// position to make. It had not established absence; it had established that
// it read nothing usable, which under a dropped leaf, an expiring credential
// or a partial hub outage are very different things.
//
// So the unreadable state gets a representation and arrives by default
// (finding-188): Unreadable and Incomplete are zero only when nothing was
// skipped, and every caller that reports absence has to look past them first.
type presenceScan struct {
	// Keys is every key in the bucket, before any prefix filter.
	Keys int
	// TeamMatched is the keys under presence.<team>., derived from the
	// prefix already in hand. It separates a wrong team from a wrong id.
	TeamMatched int
	// Matched is the keys under the recipient's full prefix.
	Matched int
	// Unreadable is matched keys whose value could not be fetched or parsed.
	Unreadable int
	// Incomplete is matched keys that parsed but carried no workspace.
	Incomplete int
	// Workspaces is what was actually recovered, one per usable record.
	Workspaces []string
}

// scanPresence reads the matched rows once and records what it could not use
// alongside what it could. It performs no read the old code did not perform.
func (b *Bus) scanPresence(ctx context.Context, keys []string, prefix string) presenceScan {
	team := teamScope(prefix)
	s := presenceScan{Keys: len(keys)}
	for _, k := range keys {
		if strings.HasPrefix(k, team) {
			s.TeamMatched++
		}
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		s.Matched++
		entry, err := b.kv.Get(ctx, k)
		if err != nil {
			s.Unreadable++
			continue
		}
		var rec map[string]any
		if json.Unmarshal(entry.Value(), &rec) != nil {
			s.Unreadable++
			continue
		}
		ws, _ := rec["workspace"].(string)
		if ws == "" {
			s.Incomplete++
			continue
		}
		s.Workspaces = append(s.Workspaces, ws)
	}
	return s
}

// teamScope narrows a presence prefix to its team segment, presence.<team>.
// A presence key is presence.<team>.<id>.<instance>, so an agent address
// yields presence.<team>.<id>. and a role address yields presence.<team>.
// already. Derived from the prefix in hand; it reads nothing.
func teamScope(prefix string) string {
	parts := strings.SplitAfter(prefix, ".")
	if len(parts) < 3 {
		return prefix
	}
	return parts[0] + parts[1]
}

// pickWorkspace collapses the workspaces of a recipient's live presence records
// to the one that names its subject. Zero is a refusal (no live consumer), two
// or more is a refusal (ambiguous, and it lists them), both loud before publish
// (R-09); exactly one resolves. Split from the KV read so it is unit-testable
// without a broker.
//
// The refusal at zero is correct and stays. What changed is that it now says
// only what the scan established, and the four zero cases no longer share one
// sentence: an empty bucket, a team nothing matches, a team that is live
// without this seat, and rows that were there and could not be read are four
// different things to be told at 3am.
func pickWorkspace(addr, prefix string, scan presenceScan) (string, error) {
	seen := map[string]bool{}
	for _, ws := range scan.Workspaces {
		seen[ws] = true
	}
	switch len(seen) {
	case 1:
		for ws := range seen {
			return ws, nil
		}
	case 0:
		return "", noPresenceErr(addr, prefix, scan)
	}
	list := make([]string, 0, len(seen))
	for ws := range seen {
		list = append(list, ws)
	}
	sort.Strings(list)
	return "", fmt.Errorf("recipient %s is live in more than one workspace (%s); pass an explicit workspace to disambiguate rather than guess (R-92)", addr, strings.Join(list, ", "))
}

// noPresenceErr states the refusal and nothing beyond what the scan
// established. Every branch refuses; they differ only in what they claim.
func noPresenceErr(addr, prefix string, scan presenceScan) error {
	// The established cases keep the original claim, because in each of them
	// it is true and was shown. The unreadable case does not get it: "no
	// session would consume this send" is precisely what was not established.
	const refused = "no session would consume this send, so it is refused rather than sent to a subject nobody filters (R-92)"
	const refusedUnestablished = "refused rather than sent to a subject nobody filters (R-92)"
	switch {
	case scan.Keys == 0:
		return fmt.Errorf("no presence record exists at all: the presence bucket is empty, so no session of any kind has registered. %s for %s", refused, addr)
	case scan.TeamMatched == 0:
		return fmt.Errorf("nothing is registered under %q for %s, though %d other presence key(s) exist; the team name is likely wrong. %s", teamScope(prefix), addr, scan.Keys, refused)
	case scan.Matched == 0:
		return fmt.Errorf("the team is live (%d key(s) under %q) but nothing matches %q for %s; the recipient id is wrong, or that seat is down. %s", scan.TeamMatched, teamScope(prefix), prefix, addr, refused)
	case scan.Unreadable > 0 || scan.Incomplete > 0:
		// The finding-188 case. This is NOT a statement that the recipient is
		// absent: rows were there and could not be used, which is what a
		// dropped leaf or an expiring credential looks like from here.
		return fmt.Errorf("%d presence record(s) match %q for %s but none could be used (%d unreadable, %d missing a workspace), so whether %s is live was NOT established; this is not a report that it is absent. %s",
			scan.Matched, prefix, addr, scan.Unreadable, scan.Incomplete, addr, refusedUnestablished)
	default:
		return fmt.Errorf("no live presence for %s; %s", addr, refused)
	}
}

// sendResult is what a send is able to say about itself: which tier carried
// it, and, when the transport gave one, the stream and sequence the message
// was stored under. It remains "accepted for delivery" and nothing more
// (R-08); a stream sequence is the transport's word that the bytes are
// stored, not the recipient's word that they were read.
type sendResult struct {
	Tier     string
	Stream   string
	Sequence uint64
}

// publish sends the envelope to its recipient subject and mirrors it to the
// audit stream. Dedupe rides on Nats-Msg-Id = message_id (R-13, verified at
// transport in sub-probe 2). Returns "accepted for delivery" semantics only:
// this is a send acknowledgement, never a delivery or read one (R-08). wsHint
// is the tools' optional workspace argument, passed through to resolveSubject
// for a cold mailbox; empty means resolve the recipient's workspace from the
// roster. A resolution refusal (R-92) returns here before any subject is built,
// so nothing lands on the inbox or the audit stream.
//
// A global:// address routes to the hub instead (R-86). The workspace hint has
// no meaning there: a global subject carries no workspace token, so the
// address alone determines it and R-92 reduces to the liveness half.
func (b *Bus) publish(ctx context.Context, e *Envelope, wsHint string) (*sendResult, error) {
	body, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	if len(body) > maxEnvelopeBytes {
		return nil, fmt.Errorf("envelope %d bytes exceeds 64 KiB; use content.refs pointers", len(body))
	}
	if strings.HasPrefix(e.Recipient.Address, "global://") {
		return b.publishGlobal(ctx, e, body)
	}
	subject, durable, err := b.resolveSubject(ctx, e.Recipient.Address, wsHint)
	if err != nil {
		return nil, err
	}
	msg := &nats.Msg{Subject: subject, Data: body, Header: nats.Header{}}
	msg.Header.Set(jetstream.MsgIDHeader, e.MessageID)
	res := &sendResult{Tier: "local"}
	if durable {
		ack, err := b.js.PublishMsg(ctx, msg)
		if err != nil {
			return nil, fmt.Errorf("publish to %s: %w", subject, err)
		}
		res.Stream, res.Sequence = ack.Stream, ack.Sequence
	} else {
		// broadcast is core NATS, no durable queue (section 2.5)
		if err := b.nc.PublishMsg(msg); err != nil {
			return nil, err
		}
	}
	b.auditMirror(ctx, e, body)
	return res, nil
}

// publishGlobal carries one envelope over the hub. The audit mirror stays on
// the LOCAL audit stream: the sending session's own record of what it sent is
// a local concern, and the hub carries the director channel only (brief 8
// section 2).
func (b *Bus) publishGlobal(ctx context.Context, e *Envelope, body []byte) (*sendResult, error) {
	g, err := b.globalReady(ctx)
	if err != nil {
		return nil, err
	}
	ack, err := g.publish(ctx, e, body)
	if err != nil {
		return nil, err
	}
	b.auditMirror(ctx, e, body)
	return &sendResult{Tier: "global", Stream: ack.Stream, Sequence: ack.Sequence}, nil
}

// auditMirror puts a copy on the append-only local stream (section 2.3). Best
// effort in the probe: a missing audit stream must not fail a send that the
// recipient's inbox already accepted.
func (b *Bus) auditMirror(ctx context.Context, e *Envelope, body []byte) {
	amsg := &nats.Msg{Subject: "agent.audit", Data: body, Header: nats.Header{}}
	amsg.Header.Set(jetstream.MsgIDHeader, e.MessageID)
	_, _ = b.js.PublishMsg(ctx, amsg)
}

// receive is the long-poll. It pulls the next message for this agent from its
// durable consumer, waiting up to timeout, and acks it. This is the shape the
// probe set out to measure: the agent CALLS to receive, because MCP cannot
// push into the model's context (see PROGRESS.md sub-probe 3).
func (b *Bus) receive(ctx context.Context, timeout time.Duration) (*Envelope, uint64, error) {
	msgs, err := b.consumer.Fetch(1, jetstream.FetchMaxWait(timeout))
	if err != nil {
		return nil, 0, err
	}
	for m := range msgs.Messages() {
		var e Envelope
		if err := json.Unmarshal(m.Data(), &e); err != nil {
			_ = m.Term() // poison message: do not redeliver a thing we cannot parse
			return nil, 0, fmt.Errorf("undecodable message on inbox: %w", err)
		}
		var seq uint64
		if md, err := m.Metadata(); err == nil {
			seq = md.Sequence.Stream
		}
		_ = m.Ack()
		return &e, seq, nil
	}
	if err := msgs.Error(); err != nil {
		return nil, 0, err
	}
	return nil, 0, nil // timed out, no message: a clean empty, not an error
}

// globalReady returns the attached hub context, attaching it on first use and
// after a failed attempt. Attaching lazily is what lets a session start while
// the hub is down: the local tier comes up, the global one reports its refusal
// per call, and the next heartbeat or tool call picks the hub up when it is
// back, with no restart (brief 8 section 1, and the brief's success signal 4).
func (b *Bus) globalReady(ctx context.Context) (*globalTier, error) {
	if b.globalCfg == nil {
		return nil, errors.New("the global tier is off for this session: DIRECTOR_GLOBAL_DOMAIN is unset, so it holds no global address and reaches no hub (R-86)")
	}
	b.globalMu.Lock()
	defer b.globalMu.Unlock()
	if b.global != nil {
		return b.global, nil
	}
	g, err := attachGlobal(ctx, b.nc, *b.globalCfg, b.self.AgentID, b.instance)
	if err != nil {
		return nil, err
	}
	b.global = g
	b.noteResumed("global", g.resumedFrom, g.consumer)
	return g, nil
}

// tierPollSlice is how long one tier is polled before the other gets a turn.
// The alternative, splitting the caller's budget in half, makes a message on
// the second tier wait out the first tier's whole share; slicing bounds the
// added latency on either tier at roughly one slice while keeping the count of
// pull requests low (a JetStream fetch is a server-side long poll, not a spin).
const tierPollSlice = 5 * time.Second

// pollResult is one wait_for_message outcome: the envelope if one arrived, the
// tier it came from, and any global-tier trouble worth telling the caller
// about. A global failure is a warning, not an error: the local tier keeps
// working through a hub outage and the poll must keep working with it.
type pollResult struct {
	Env        *Envelope
	Tier       string
	Seq        uint64 // stream sequence on the tier's stream; orders a batch
	GlobalWarn string
}

// receiveTiered polls local first, then global, in slices until the budget is
// spent. With the global tier off it is exactly the old single-tier poll.
func (b *Bus) receiveTiered(ctx context.Context, timeout time.Duration) (pollResult, error) {
	if b.globalCfg == nil {
		e, seq, err := b.receive(ctx, timeout)
		if err != nil {
			return pollResult{}, err
		}
		return pollResult{Env: e, Tier: tierOf(e, "local"), Seq: seq}, nil
	}
	var res pollResult
	// Mail already waiting is taken in alternating tier order first, so a
	// backlog on one tier cannot starve the other (the loop below always
	// tries local first and would return a local message every call).
	if w := b.takeWaiting(ctx, &res); w != nil {
		res.Env, res.Tier, res.Seq = w.Env, w.Tier, w.Seq
		return res, nil
	}
	deadline := time.Now().Add(timeout)
	for {
		slice, ok := sliceLeft(deadline)
		if !ok {
			return res, nil
		}
		e, seq, err := b.receive(ctx, slice)
		if err != nil && !errors.Is(err, jetstream.ErrNoMessages) {
			return res, err
		}
		if e != nil {
			res.Env, res.Tier, res.Seq = e, "local", seq
			return res, nil
		}
		slice, ok = sliceLeft(deadline)
		if !ok {
			return res, nil
		}
		g, err := b.globalReady(ctx)
		if err != nil {
			// The hub is unreachable or refuses. Say so, keep polling local
			// for the rest of the budget rather than failing the whole call.
			res.GlobalWarn = err.Error()
			if _, ok := sliceLeft(deadline); !ok {
				return res, nil
			}
			continue
		}
		e, gseq, discarded, err := g.receive(ctx, slice, b.self.AgentID, b.instance)
		if err != nil && !errors.Is(err, jetstream.ErrNoMessages) {
			res.GlobalWarn = fmt.Sprintf("global inbox poll failed: %v", err)
			continue
		}
		res.GlobalWarn = ""
		if discarded > 0 {
			res.GlobalWarn = fmt.Sprintf("discarded %d undecodable message(s) on the global inbox; the hub stream is shared and not everything on it is a director envelope", discarded)
		}
		if e != nil {
			res.Env, res.Tier, res.Seq = e, "global", gseq
			return res, nil
		}
	}
}

// takeWaiting takes one message that is already waiting, without blocking,
// from the tier whose turn it is and then the other. A global failure is
// recorded as a warning; the blocking poll that follows reports it again if
// it persists.
func (b *Bus) takeWaiting(ctx context.Context, res *pollResult) *drained {
	order := []string{"local", "global"}
	if b.takeTurn() {
		order = []string{"global", "local"}
	}
	for _, tier := range order {
		var items []drained
		if tier == "local" {
			items, _, _ = pullNoWait(ctx, b.consumer, 1, "local")
		} else {
			g, err := b.globalReady(ctx)
			if err != nil {
				res.GlobalWarn = err.Error()
				continue
			}
			items, _, _ = g.drainNoWait(ctx, 1, b.self.AgentID, b.instance)
		}
		if len(items) > 0 {
			return &items[0]
		}
	}
	return nil
}

// sliceLeft returns the next poll slice and whether any budget remains.
func sliceLeft(deadline time.Time) (time.Duration, bool) {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0, false
	}
	if remaining < tierPollSlice {
		return remaining, true
	}
	return tierPollSlice, true
}

func tierOf(e *Envelope, tier string) string {
	if e == nil {
		return ""
	}
	return tier
}

// setPresence writes this agent's heartbeat into the presence KV. The bucket
// TTL expires the key if no heartbeat lands within the window, so absence is
// silence, not a message (section 2.1: presence is a transport concern).
//
// With the global tier on it writes both rows. The local write is the one that
// can fail the call: a hub that is down must not stop a session recording
// presence on its own broker, so the global failure comes back as a warning
// for the caller to see rather than an error that hides the local success.
func (b *Bus) setPresence(ctx context.Context, state string) (globalWarn string, err error) {
	b.stateMu.Lock()
	b.state = state
	b.stateMu.Unlock()
	if err := b.writePresence(ctx, state); err != nil {
		return "", err
	}
	return b.writeGlobalPresence(ctx, state), nil
}

// writeGlobalPresence puts this session's row in GLOBAL_PRESENCE, returning a
// warning string rather than an error because every caller wants to carry on
// without it. Off (no domain configured) returns an empty warning: nothing is
// wrong, there is simply no global row to write.
func (b *Bus) writeGlobalPresence(ctx context.Context, state string) string {
	if b.globalCfg == nil {
		return ""
	}
	g, err := b.globalReady(ctx)
	if err != nil {
		return err.Error()
	}
	if err := g.writePresence(ctx, b.self, b.instance, b.pid, state); err != nil {
		return fmt.Sprintf("global presence write failed: %v", err)
	}
	return ""
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
//
// The same tick carries the global row when the global tier is on, so one
// timer keeps both presences alive and the hub bucket's TTL measures the same
// liveness the local one does. It also makes the tick the re-attach path: a
// hub that was down at startup is picked up within one beat, with no restart.
// Failures log on the transition, not every beat, so an outage is loud once
// and quiet thereafter.
func (b *Bus) heartbeat(ctx context.Context, every time.Duration, logf func(string, ...any)) {
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
			b.reportGlobalWarn(b.writeGlobalPresence(ctx, s), logf)
		}
	}
}

// deregister deletes this session's presence rows on an orderly exit (BEAT-C,
// sim/design/shim-timer-heartbeat.md). Presence membership is not a liveness
// signal (R-93), so the graceful path removes the key at once rather than
// leaving a live-looking row until the TTL lapses; a crash that skips this path
// falls to that TTL. Best-effort by design: every failure is logged and none is
// fatal, because a shim that cannot delete its own key is still exiting. Local
// presence always; the global row too when the global tier is on. There is no
// lease to release here: the party pulled the seat lease back to the architect,
// so deregister acts on presence only.
func (b *Bus) deregister(ctx context.Context, logf func(string, ...any)) {
	key := "presence." + b.self.Team + "." + b.self.AgentID + "." + b.instance
	if err := b.kv.Delete(ctx, key); err != nil && logf != nil {
		logf("deregister: local presence delete failed, falls to TTL: %v", err)
	}
	if b.globalCfg == nil {
		return
	}
	g, err := b.globalReady(ctx)
	if err != nil {
		if logf != nil {
			logf("deregister: global tier not ready, global presence falls to TTL: %v", err)
		}
		return
	}
	if err := g.deletePresence(ctx, b.instance); err != nil && logf != nil {
		logf("deregister: global presence delete failed, falls to TTL: %v", err)
	}
}

// reportGlobalWarn logs a change in the global tier's health: the first
// failure, a different failure, and the recovery. Repeats stay silent.
func (b *Bus) reportGlobalWarn(warn string, logf func(string, ...any)) {
	b.globalMu.Lock()
	prev := b.globalWarn
	b.globalWarn = warn
	b.globalMu.Unlock()
	if warn == prev || logf == nil {
		return
	}
	if warn == "" {
		logf("global tier recovered: presence is writing to %s again", globalPresenceBucket)
		return
	}
	logf("WARNING: global tier unavailable, local tier unaffected: %s", warn)
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

// rosterMerged reads both presence stores and returns one list with a tier
// column. The global rows are the fleet's other clusters, which is the whole
// point of listing them here: a director picks the cluster to address from
// this list. A hub that will not answer degrades to the local rows plus a
// warning, never an empty roster and never an error.
func (b *Bus) rosterMerged(ctx context.Context) (rows []map[string]any, globalWarn string, err error) {
	local, err := b.roster(ctx)
	if err != nil {
		return nil, "", err
	}
	for _, r := range local {
		r["tier"] = "local"
		rows = append(rows, r)
	}
	if b.globalCfg == nil {
		return rows, "", nil
	}
	g, gerr := b.globalReady(ctx)
	if gerr != nil {
		return rows, gerr.Error(), nil
	}
	global, _, gerr := g.records(ctx, "")
	if gerr != nil {
		return rows, fmt.Sprintf("global roster read failed: %v", gerr), nil
	}
	for _, r := range global {
		r["tier"] = "global"
		rows = append(rows, r)
	}
	return rows, "", nil
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
