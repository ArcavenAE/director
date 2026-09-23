package main

// The global tier (R-86, R-94, R-95; design brief 8, sim/design/global-bus-tier.md).
//
// One connection, two JetStream contexts. The shim keeps its single connection
// to the LOCAL broker and reaches the hub's stream and presence bucket by
// addressing the hub's JetStream domain over the broker's leaf link
// (jetstream.NewWithDomain, the client half of "$JS.<domain>.API.>"). There is
// no second connection and no hub credential in the session: the leaf link
// holds the cluster credential, the broker holds the leaf link, and the shim
// holds neither (brief 8 sections 1 and 4).
//
// Off by default. DIRECTOR_GLOBAL_DOMAIN unset means the shim behaves exactly
// as it did before this file existed: one local tier, no global addresses, no
// hub traffic.
//
// Everything above the connection is derivation, and the derivations are pure
// functions at the top of this file so they are testable without a broker.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// The two role words that exist at the global tier (R-94). A worker never
// holds a global address, so there is no third value and an unknown one is
// refused at start rather than defaulted.
const (
	roleSupervisor = "supervisor"
	roleDirector   = "director"
)

const (
	globalPresenceBucket = "GLOBAL_PRESENCE"
	globalDirectorStream = "GLOBAL_TO_DIRECTOR"
	globalStreamPrefix   = "GLOBAL_TO_"

	// The hub streams carry a 24h max age. A durable cleaned up after a
	// slightly longer idle window can therefore only ever have replayed
	// messages that have already expired, so the cleanup costs nothing while
	// still keeping dead sessions' consumers from accumulating on a shared hub
	// (R-50, and the orphan accumulation the local preflight comment names).
	globalConsumerInactive = 25 * time.Hour
)

// globalConfig is the launcher-assigned global identity. Like the local
// identity levers it is validated and rejected, never rewritten (R-76).
type globalConfig struct {
	Domain  string // the hub's JetStream domain, e.g. "global"
	Cluster string // this cluster's subject token, e.g. "mokuzai" (R-94)
	Role    string // supervisor | director
}

// loadGlobalConfig reads the three environment levers. A nil config with a nil
// error is the off case: DIRECTOR_GLOBAL_DOMAIN unset means the global tier is
// not configured, which is not an error. Anything else malformed is an error
// the caller turns into a refusal at start.
func loadGlobalConfig() (*globalConfig, error) {
	domain := os.Getenv("DIRECTOR_GLOBAL_DOMAIN")
	if domain == "" {
		return nil, nil
	}
	cfg := &globalConfig{
		Domain:  domain,
		Cluster: os.Getenv("DIRECTOR_CLUSTER"),
		Role:    os.Getenv("DIRECTOR_GLOBAL_ROLE"),
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// validate checks the three levers before any subject is built. The domain and
// the cluster both become subject tokens ("$JS.<domain>.API.>",
// "global.<cluster>.>"), so both take the identity class check; the role is a
// closed set of two words and an unrecognised one is refused with both listed,
// because a silently defaulted role would consume the wrong stream.
func (c *globalConfig) validate() error {
	if err := validToken("DIRECTOR_GLOBAL_DOMAIN", c.Domain); err != nil {
		return err
	}
	if c.Cluster == "" {
		return errors.New("DIRECTOR_CLUSTER is required when DIRECTOR_GLOBAL_DOMAIN is set; the cluster is this session's namespace at the global tier (R-94)")
	}
	if err := validToken("DIRECTOR_CLUSTER", c.Cluster); err != nil {
		return err
	}
	if c.Role != roleSupervisor && c.Role != roleDirector {
		return fmt.Errorf("DIRECTOR_GLOBAL_ROLE %q is not a global role; exactly two exist, %s and %s, and a worker never holds a global address (R-94)",
			c.Role, roleSupervisor, roleDirector)
	}
	return nil
}

// inboxSubject is the subject this session consumes at the global tier. The
// director reads one inbox for the whole fleet; a supervisor reads its own
// cluster's (brief 8 section 2).
func (c *globalConfig) inboxSubject() string {
	if c.Role == roleDirector {
		return "global.director.inbox"
	}
	return "global." + c.Cluster + ".supervisor.inbox"
}

// streamName is the hub stream that inbox lands in. One stream per direction
// per cluster is what lets the hub bind a leaf to its own stream by name
// (brief 8 sections 2 and 4).
func (c *globalConfig) streamName() string {
	if c.Role == roleDirector {
		return globalDirectorStream
	}
	return globalStreamPrefix + c.Cluster
}

// selfAddress is how the rest of the fleet addresses this session globally. A
// supervisor keeps its local id and gains a global address; nothing is renamed
// across tiers (R-06, R-79, R-94).
func (c *globalConfig) selfAddress() string {
	if c.Role == roleDirector {
		return "global://director"
	}
	return "global://" + c.Cluster + "/supervisor"
}

// presenceKey is this session's own key in GLOBAL_PRESENCE. It carries the
// instance, so two sessions cast under one role do not collapse into one row
// (the same R-49/R-50 reasoning as the local presence key).
func (c *globalConfig) presenceKey(instance string) string {
	return globalPresencePrefix(c.Cluster, c.Role) + instance
}

// globalPresencePrefix is the key prefix every live record for one global
// principal shares: "presence.director." for the director, and
// "presence.<cluster>.supervisor." for a cluster's supervisor (brief 8
// section 2). Reading it is the liveness half of R-92 at this tier.
func globalPresencePrefix(cluster, role string) string {
	if role == roleDirector {
		return "presence.director."
	}
	return "presence." + cluster + "." + role + "."
}

// globalSubject is the subject a global address resolves to. Unlike the local
// tier there is no workspace to resolve: the address fully determines the
// subject, which is why R-92 here is the liveness half only (brief 8 section 2).
func globalSubject(cluster, role string) string {
	if role == roleDirector {
		return "global.director.inbox"
	}
	return "global." + cluster + "." + role + ".inbox"
}

// globalDurable names the per-session durable on the hub stream. It carries
// the instance for the same reason the local one does: two sessions under one
// id must not bind one durable and race each other's mail (R-50).
func globalDurable(agentID, instance string) string {
	return "mcp_global_" + agentID + "_" + instance
}

// parseGlobalAddress resolves a global:// address to the cluster and role it
// names. Two forms exist and no others (R-94):
//
//	global://director                 the one director seat, no cluster token
//	global://<cluster>/supervisor     a cluster's supervisor
//
// A worker form, a third role word, a cluster-qualified director, or a stray
// path segment is refused here rather than turned into a subject nobody reads.
func parseGlobalAddress(addr string) (cluster, role string, err error) {
	if !strings.HasPrefix(addr, "global://") {
		return "", "", errors.New("not a global address: " + addr)
	}
	rest := strings.TrimPrefix(addr, "global://")
	if rest == "" {
		return "", "", errors.New("global address is empty; use global://director or global://{cluster}/supervisor")
	}
	head, tail, hasSlash := strings.Cut(rest, "/")
	if !hasSlash {
		if head != roleDirector {
			return "", "", fmt.Errorf("unroutable global address %q; a single-segment global address is only global://director, and a cluster needs its role (global://%s/supervisor)", addr, head)
		}
		return "", roleDirector, nil
	}
	if strings.Contains(tail, "/") {
		return "", "", fmt.Errorf("unroutable global address %q; the global forms are global://director and global://{cluster}/supervisor, nothing deeper", addr)
	}
	if err := validToken("global cluster", head); err != nil {
		return "", "", err
	}
	switch tail {
	case roleSupervisor:
		return head, roleSupervisor, nil
	case roleDirector:
		return "", "", fmt.Errorf("unroutable global address %q; the director is one seat for the fleet and is addressed as global://director, never per cluster (R-94)", addr)
	default:
		return "", "", fmt.Errorf("unroutable global address %q; %q is not a global role, exactly two exist (%s and %s) and a worker never holds a global address (R-94)",
			addr, tail, roleSupervisor, roleDirector)
	}
}

// noGlobalPresenceErr is the R-92 liveness refusal at the global tier. The
// subject here is fully determined by the address, so the only resolution
// question left is whether anyone is alive to consume it; zero live records
// refuses before publish rather than storing a message no session filters.
// noGlobalPresenceErr states the refusal and nothing beyond what the scan
// established. Every branch refuses; they differ only in what they claim.
func noGlobalPresenceErr(addr, prefix string, scan presenceScan) error {
	const refused = "no session would consume this send, so it is refused rather than stored on a subject nobody reads (R-92 liveness)"
	const refusedUnestablished = "refused rather than stored on a subject nobody reads (R-92 liveness)"
	switch {
	case scan.Keys == 0:
		return fmt.Errorf("no presence record exists at all in %s: the bucket is empty, so no session of any kind has registered. %s for %s", globalPresenceBucket, refused, addr)
	case scan.TeamMatched == 0:
		return fmt.Errorf("nothing is registered under %q in %s for %s, though %d other presence key(s) exist; the cluster name is likely wrong. %s", teamScope(prefix), globalPresenceBucket, addr, scan.Keys, refused)
	case scan.Matched == 0:
		return fmt.Errorf("the cluster is live (%d key(s) under %q in %s) but nothing matches %q for %s; the role is wrong, or that seat is down. %s", scan.TeamMatched, teamScope(prefix), globalPresenceBucket, prefix, addr, refused)
	case scan.Unreadable > 0:
		// The finding-188 case: rows were there and could not be used, which
		// is what a dropped leaf or an expiring credential looks like from
		// here. Not a report that the recipient is absent.
		return fmt.Errorf("%d presence record(s) match %q in %s for %s but none could be read (%d unreadable), so whether %s is live was NOT established; this is not a report that it is absent. %s",
			scan.Matched, prefix, globalPresenceBucket, addr, scan.Unreadable, addr, refusedUnestablished)
	default:
		return fmt.Errorf("no live presence for %s in %s; %s", addr, globalPresenceBucket, refused)
	}
}

// globalTier is the attached hub context: the domain-qualified JetStream
// handle, the presence bucket, and this session's durable on its own inbox
// stream.
type globalTier struct {
	cfg      globalConfig
	js       jetstream.JetStream
	kv       jetstream.KeyValue
	consumer jetstream.Consumer
}

// attachGlobal builds the second JetStream context over the SAME connection
// and binds this session's inbox durable. The hub stream and the bucket must
// already exist: provisioning is the hub operator's job, and a bare hub is the
// finding-166 failure at the global tier, so a missing one is reported as
// unprovisioned rather than created here.
func attachGlobal(ctx context.Context, nc *nats.Conn, cfg globalConfig, agentID, instance string) (*globalTier, error) {
	js, err := jetstream.NewWithDomain(nc, cfg.Domain)
	if err != nil {
		return nil, fmt.Errorf("global JetStream domain %q: %w", cfg.Domain, err)
	}
	kv, err := js.KeyValue(ctx, globalPresenceBucket)
	if err != nil {
		return nil, fmt.Errorf("global presence bucket %s in domain %q: %w (%s)", globalPresenceBucket, cfg.Domain, err, hubHint(cfg))
	}
	g := &globalTier{cfg: cfg, js: js, kv: kv}
	if err := g.ensureConsumer(ctx, agentID, instance); err != nil {
		return nil, err
	}
	return g, nil
}

// ensureConsumer creates or rebinds this session's durable on the hub stream.
// It is idempotent, so it doubles as the repair path when the durable has been
// cleaned up under it (globalConsumerInactive, or an operator removing it).
func (g *globalTier) ensureConsumer(ctx context.Context, agentID, instance string) error {
	cons, err := g.js.CreateOrUpdateConsumer(ctx, g.cfg.streamName(), jetstream.ConsumerConfig{
		Durable:           globalDurable(agentID, instance),
		FilterSubject:     g.cfg.inboxSubject(),
		AckPolicy:         jetstream.AckExplicitPolicy,
		DeliverPolicy:     jetstream.DeliverAllPolicy,
		MaxDeliver:        -1,
		InactiveThreshold: globalConsumerInactive,
	})
	if err != nil {
		return fmt.Errorf("global durable on %s: %w (%s)", g.cfg.streamName(), err, hubHint(g.cfg))
	}
	g.consumer = cons
	return nil
}

// hubHint is the one sentence worth saying whenever a hub operation fails: a
// leaf that is down and a credential that refuses the subject look identical
// from here (both are "no responders", by design, because the hub pushes the
// permission set down so the leaf refuses locally at once).
func hubHint(cfg globalConfig) string {
	return fmt.Sprintf("the hub is reached through the local broker's leaf link in domain %q; a down link and a credential that does not grant this cluster's stream both surface as no responders", cfg.Domain)
}

// receive is the global long-poll. Two things differ from the local one, and
// both are consequences of the hub stream being SHARED: the shim is not the
// only publisher into it, and its durable can be cleaned up under a live
// session.
//
// A message this shim cannot decode is terminated and the poll carries on
// within the same budget, returning the count rather than the error. On the
// local inbox an undecodable message is a defect worth surfacing at once; on a
// hub stream that operators and scripts also publish into, one raw line would
// otherwise consume a whole poll and hide the envelope behind it. The count
// still reaches the caller, so discarding stays visible.
//
// A durable that has been cleaned up (globalConsumerInactive, or an operator
// removing it) is rebuilt once and the fetch retried, so an idle session does
// not spend the rest of its life reporting a missing consumer.
func (g *globalTier) receive(ctx context.Context, timeout time.Duration, agentID, instance string) (env *Envelope, discarded int, err error) {
	deadline := time.Now().Add(timeout)
	rebuilt := false
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, discarded, nil
		}
		e, poison, err := g.fetchOne(remaining)
		switch {
		case err != nil && errors.Is(err, jetstream.ErrConsumerNotFound) && !rebuilt:
			rebuilt = true
			if rerr := g.ensureConsumer(ctx, agentID, instance); rerr != nil {
				return nil, discarded, rerr
			}
		case err != nil:
			return nil, discarded, err
		case poison:
			discarded++
		default:
			return e, discarded, nil
		}
	}
}

// fetchOne pulls at most one message. poison reports a message that was
// terminated because it could not be decoded; a nil envelope with poison false
// is the clean empty of a spent wait.
func (g *globalTier) fetchOne(timeout time.Duration) (env *Envelope, poison bool, err error) {
	msgs, err := g.consumer.Fetch(1, jetstream.FetchMaxWait(timeout))
	if err != nil {
		return nil, false, err
	}
	for m := range msgs.Messages() {
		var e Envelope
		if err := json.Unmarshal(m.Data(), &e); err != nil {
			_ = m.Term() // do not redeliver a thing we cannot parse
			return nil, true, nil
		}
		_ = m.Ack()
		return &e, false, nil
	}
	if err := msgs.Error(); err != nil {
		return nil, false, err
	}
	return nil, false, nil
}

// writePresence puts this session's record into the hub bucket. It carries the
// cluster and the global role alongside the local identity, so both tiers name
// the same session and a reader can get from a global address back to the
// local id without a rename (R-06, R-79, R-94).
func (g *globalTier) writePresence(ctx context.Context, self Sender, instance string, pid int, state string) error {
	rec := map[string]any{
		"cluster":   g.cfg.Cluster,
		"role":      g.cfg.Role,
		"agent_id":  self.AgentID,
		"workspace": self.Workspace,
		"team":      self.Team,
		"instance":  instance,
		"pid":       pid,
		"state":     state,
		"ts":        time.Now().UTC().Format(time.RFC3339),
	}
	body, _ := json.Marshal(rec)
	_, err := g.kv.Put(ctx, g.cfg.presenceKey(instance), body)
	return err
}

// deletePresence removes this session's row from GLOBAL_PRESENCE on an orderly
// exit (BEAT-C, sim/design/shim-timer-heartbeat.md). It mirrors writePresence;
// a failure here, or a crash that never reaches it, falls to the bucket TTL,
// which vacates a dead holder without cooperation.
func (g *globalTier) deletePresence(ctx context.Context, instance string) error {
	return g.kv.Delete(ctx, g.cfg.presenceKey(instance))
}

// records reads GLOBAL_PRESENCE and returns every record whose key starts with
// prefix. An empty prefix reads the whole bucket (the roster); a principal's
// prefix reads its live records (the liveness check).
// records returns the matched presence records and, alongside them, what the
// pass could not use. The second return is the same fix as the local tier's:
// a row that could not be fetched or parsed used to vanish, and its absence
// was then reported as the recipient's absence (finding-188). A caller that
// reports absence has to look at the scan first.
func (g *globalTier) records(ctx context.Context, prefix string) ([]map[string]any, presenceScan, error) {
	keys, err := g.kv.Keys(ctx)
	if err != nil {
		if errors.Is(err, jetstream.ErrNoKeysFound) {
			return nil, presenceScan{}, nil
		}
		return nil, presenceScan{}, err
	}
	team := teamScope(prefix)
	scan := presenceScan{Keys: len(keys)}
	var out []map[string]any
	for _, k := range keys {
		if prefix != "" && strings.HasPrefix(k, team) {
			scan.TeamMatched++
		}
		if prefix != "" && !strings.HasPrefix(k, prefix) {
			continue
		}
		scan.Matched++
		entry, err := g.kv.Get(ctx, k)
		if err != nil {
			scan.Unreadable++
			continue
		}
		var rec map[string]any
		if json.Unmarshal(entry.Value(), &rec) != nil {
			scan.Unreadable++
			continue
		}
		out = append(out, rec)
	}
	return out, scan, nil
}

// publish sends one envelope to a global address. The liveness check runs
// first, so a refusal costs nothing and stores nothing; the publish then goes
// through the domain-qualified context with Nats-Msg-Id for dedupe, exactly as
// the local tier does (R-13). The returned PubAck is the hub's own word that
// the message is stored, and the tools surface its stream and sequence,
// because a leaf-side sender cannot read the director's stream back to check.
func (g *globalTier) publish(ctx context.Context, e *Envelope, body []byte) (*jetstream.PubAck, error) {
	cluster, role, err := parseGlobalAddress(e.Recipient.Address)
	if err != nil {
		return nil, err
	}
	prefix := globalPresencePrefix(cluster, role)
	live, scan, err := g.records(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w (%s)", globalPresenceBucket, err, hubHint(g.cfg))
	}
	if len(live) == 0 {
		return nil, noGlobalPresenceErr(e.Recipient.Address, prefix, scan)
	}
	msg := &nats.Msg{Subject: globalSubject(cluster, role), Data: body, Header: nats.Header{}}
	msg.Header.Set(jetstream.MsgIDHeader, e.MessageID)
	ack, err := g.js.PublishMsg(ctx, msg)
	if err != nil {
		// No responders and a timeout are the two shapes a refused or
		// unreachable hub takes, and both are a tool error: the send is not
		// queued, not retried, and must not read as accepted (brief 8
		// sections 1 and 4.1).
		return nil, fmt.Errorf("global publish to %s (%s) failed: %w (%s)", e.Recipient.Address, msg.Subject, err, hubHint(g.cfg))
	}
	return ack, nil
}

// preflightGlobal verifies the hub half before the harness starts: the domain
// answers, the bucket exists, and this session's own inbox stream exists. It
// creates NO consumer, so a repeated preflight leaves nothing on the hub.
func preflightGlobal(ctx context.Context, nc *nats.Conn, cfg globalConfig) error {
	js, err := jetstream.NewWithDomain(nc, cfg.Domain)
	if err != nil {
		return fmt.Errorf("global JetStream domain %q: %w", cfg.Domain, err)
	}
	if _, err := js.KeyValue(ctx, globalPresenceBucket); err != nil {
		return fmt.Errorf("presence bucket %s missing in domain %q: %w (%s)", globalPresenceBucket, cfg.Domain, err, hubHint(cfg))
	}
	if _, err := js.Stream(ctx, cfg.streamName()); err != nil {
		return fmt.Errorf("stream %s missing in domain %q: %w (%s)", cfg.streamName(), cfg.Domain, err, hubHint(cfg))
	}
	return nil
}
