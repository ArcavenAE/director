package main

// The FOR filter. A hub stream is shared, so a seat's global inbox can hold
// mail whose first line says it is meant for another team's seat ("FOR
// alpha-supervisor"). With DIRECTOR_FOR_FILTER=1 the shim acks and logs such a
// message instead of handing it to the model. The default is off.
//
// The rule drops only when it can prove the mail is for someone else: every
// token on the FOR line must resolve, through the live presence rows, to a
// seat of a different team, and none may name this seat or its team. Anything
// else delivers: no FOR line, a line that does not parse, an unknown name, a
// name on a stale row, a roster that cannot be read. A drop acks the message
// on the seat's durable, so a wrong drop loses mail, and every doubt goes to
// delivery for that reason.

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// forRowFresh is how old a presence row may be and still prove a seat's team.
// It is AGENT_STATE's TTL (presenceTTL): an older row may belong to a seat
// that has since moved or gone, which is the stale-roster case that delivers.
const forRowFresh = presenceTTL

var (
	forLine   = regexp.MustCompile(`^FOR[ \t]+(.+)$`)
	forSeat   = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*(@[a-z0-9-]+)?$`)
	forRole   = regexp.MustCompile(`^role://[a-z0-9-]+/[a-z0-9-]+$`)
	forGenSfx = regexp.MustCompile(`-g[0-9]+-[0-9]+$`)
)

// forFilter holds this seat's identity and the roster read. roster is a seam;
// production uses the global presence rows that list_roster reads.
type forFilter struct {
	agentID string
	team    string
	role    string
	cluster string
	roster  func(ctx context.Context) ([]map[string]any, error)
	now     func() time.Time
	logf    func(format string, args ...any)
}

// forText returns the text after FOR on the first non-blank line of data, and
// whether that line is a FOR line. The match is anchored and case-sensitive.
func forText(data string) (string, bool) {
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		m := forLine.FindStringSubmatch(line)
		if m == nil {
			return "", false
		}
		return m[1], true
	}
	return "", false
}

// forTokens splits the FOR text on commas and whitespace, drops the words
// "and" and "or", and trims trailing . ; : from each token. Seat names hold no
// spaces, so splitting on whitespace never cuts a name.
func forTokens(text string) []string {
	var out []string
	for _, f := range strings.Fields(strings.ReplaceAll(text, ",", " ")) {
		f = strings.TrimRight(f, ".;:")
		if f == "" || f == "and" || f == "or" {
			continue
		}
		out = append(out, f)
	}
	return out
}

// forSplit separates a token's name from its @cluster suffix. A role:// token
// carries no suffix.
func forSplit(tok string) (name, cluster string) {
	if forRole.MatchString(tok) {
		return tok, ""
	}
	if i := strings.LastIndex(tok, "@"); i >= 0 {
		return tok[:i], tok[i+1:]
	}
	return tok, ""
}

// forNames are the names one seat answers to: its agent id, the id without
// the -gN-M suffix, <team>-<role> and role://<team>/<role>.
func forNames(agentID, team, role string) []string {
	names := []string{agentID, forGenSfx.ReplaceAllString(agentID, "")}
	if team != "" && role != "" {
		names = append(names, team+"-"+role, "role://"+team+"/"+role)
	}
	return names
}

func forHas(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// namesSelf reports whether a token names this seat or its team.
func (f *forFilter) namesSelf(name, cluster string) bool {
	if cluster != "" && cluster != f.cluster {
		return false
	}
	return name == f.team || forHas(forNames(f.agentID, f.team, f.role), name)
}

// otherTeamSeat reports whether a token resolves, through fresh presence
// rows, to a seat of a different team, with no matching row on this seat's
// team.
func (f *forFilter) otherTeamSeat(name, cluster string, rows []map[string]any) bool {
	found := false
	for _, r := range rows {
		team, _ := r["team"].(string)
		agent, _ := r["agent_id"].(string)
		role, _ := r["role"].(string)
		rc, _ := r["cluster"].(string)
		if team == "" || agent == "" || !f.fresh(r) {
			continue
		}
		if cluster != "" && rc != cluster {
			continue
		}
		if !forHas(forNames(agent, team, role), name) {
			continue
		}
		if team == f.team {
			return false
		}
		found = true
	}
	return found
}

// fresh reports whether a presence row was written within forRowFresh. A row
// with no readable ts is not fresh.
func (f *forFilter) fresh(r map[string]any) bool {
	s, _ := r["ts"].(string)
	ts, err := time.Parse(time.RFC3339, s)
	return err == nil && f.now().Sub(ts) <= forRowFresh
}

// drops decides one envelope. It returns true and the FOR text only when
// every token resolves to a seat of another team.
func (f *forFilter) drops(ctx context.Context, e *Envelope) (bool, string) {
	text, ok := forText(e.Content.Data)
	if !ok {
		return false, ""
	}
	toks := forTokens(text)
	if len(toks) == 0 {
		return false, ""
	}
	for _, t := range toks {
		if !forSeat.MatchString(t) && !forRole.MatchString(t) {
			return false, "" // ambiguous: one bad token spoils the line
		}
	}
	for _, t := range toks {
		name, cluster := forSplit(t)
		if f.namesSelf(name, cluster) {
			return false, ""
		}
	}
	rows, err := f.roster(ctx)
	if err != nil {
		return false, ""
	}
	for _, t := range toks {
		name, cluster := forSplit(t)
		if !f.otherTeamSeat(name, cluster, rows) {
			return false, ""
		}
	}
	return true, text
}

// dropForeign is the one place a received message meets the filter. Every path
// that takes mail off the global durable calls it before it hands the message
// on: the blocking fetch (fetchOne) and the no-wait pull behind takeWaiting and
// receiveBatch (pullNoWait). A third receive path must call it too.
//
// It reports true when the message was for another team's seat; the message is
// then acked (confirmed) and logged with its id, and the caller does not return
// it. With the filter off it reports false and does nothing. A failed ack still
// reports true, so a message proven foreign is never delivered; the broker
// redelivers it and it is dropped again.
func (g *globalTier) dropForeign(ctx context.Context, m jetstream.Msg, e *Envelope, seq uint64) bool {
	if g.forFilter == nil {
		return false
	}
	drop, text := g.forFilter.drops(ctx, e)
	if !drop {
		return false
	}
	if err := m.DoubleAck(ctx); err != nil {
		g.forFilter.logf("director-mcp: global ack %s FOR %s not confirmed: %v", e.MessageID, text, err)
		return true
	}
	g.noteAcked(seq)
	g.forDrops.Add(1)
	g.forFilter.logf("director-mcp: global ack %s FOR %s", e.MessageID, text)
	return true
}
