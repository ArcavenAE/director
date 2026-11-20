package main

// The FOR filter. A hub stream is shared, so a seat's global inbox can hold
// mail whose first line says it is meant for another seat ("FOR
// alpha-supervisor"). With DIRECTOR_FOR_FILTER=1 the shim acks and logs such a
// message instead of handing it to the model. The default is off.
//
// The rule drops only when it can prove the mail is for someone else: every
// token on the leading FOR lines (one or several, in a row at the top of the
// body) must resolve, through the live presence rows, to a seat that is not this
// one, and none may name this seat or its team. A sibling seat of this seat's own
// team is another seat. A predecessor of this seat (the same id with another
// generation and the same replica index, on this team) is this seat; a parallel
// replica (another index) is not. Anything
// else delivers: no FOR line, a line that does not parse, an unknown name, a
// name on a stale row, a roster that cannot be read. A drop acks the message
// on the seat's durable, so a wrong drop loses mail, and every doubt goes to
// delivery for that reason.
//
// Where the names end. The FOR text is read as a comma-separated list, split on
// commas before any word splitting. The first element is always names, so
// "FOR a b" reads two names, as it always has. A later element made only of
// names and the joiners "and", "or" and "only" is names, and an element that is
// exactly one token is a name, so a mistyped seat-shaped name still reaches the
// roster and delivers. The first later element that holds more than one name
// between joiners ("on repo#1 at abc1234") is prose, and it ends the list along
// with everything after it. Prose therefore never spoils a line that names
// seats first, and it never turns into a name that could drop mail. The cut
// prose is still scanned for this seat's own full name (its agent id, or the id
// without the -gN-M suffix) as a whole token in any case; a hit delivers, so a
// seat named inside prose is not dropped. Role words, team names and prefixes
// do not count. A name written name@cluster counts when the name part matches
// by that rule and the cluster is this seat's cluster; name@other-cluster does
// not match, and a name with no cluster, or an empty one ("name@"), matches as
// before.

import (
	"context"
	"log"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// forRowFresh is how old a presence row may be and still prove a seat's team.
// It is AGENT_STATE's TTL (presenceTTL): an older row may belong to a seat
// that has since moved or gone, which is the stale-roster case that delivers.
const forRowFresh = presenceTTL

var (
	forLine   = regexp.MustCompile(`(?i)^FOR[ \t]+(.+)$`)
	forEnd    = regexp.MustCompile(`[.;:](\s|$)`)
	forSeat   = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*(@[a-z0-9-]+)?$`)
	forRole   = regexp.MustCompile(`^role://[a-z0-9-]+/[a-z0-9-]+$`)
	forGenSfx = regexp.MustCompile(`-g[0-9]+-[0-9]+$`)
	// forTicket is an intake id as written in prose ("I-578"), in any case. A
	// lowercase one is seat-shaped, so it is named here instead of left to forSeat.
	forTicket = regexp.MustCompile(`(?i)^i-[0-9]+$`)
	// forGenOnly matches the generation of an id and keeps its replica index,
	// so a successor (g2-1 for g1-1) matches and a parallel replica (g1-1 for
	// g1-0) does not.
	forGenOnly = regexp.MustCompile(`-g[0-9]+(-[0-9]+)$`)
)

// forFilter holds this seat's identity and the roster read. roster is a seam;
// production uses the global presence rows that list_roster reads.
type forFilter struct {
	agentID string
	team    string
	role    string
	cluster string
	// standsIn is the team this seat stands in for (standsin.go); empty when unset.
	standsIn string
	roster   func(ctx context.Context) ([]map[string]any, error)
	now      func() time.Time
	logf     func(format string, args ...any)
	first    sync.Once
	// note receives the one first-decision line; nil means the standard log.
	// It is separate from logf so a test that counts drop lines still sees one.
	note func(format string, args ...any)
}

// newForFilter builds the filter for one session: its identity from self, the
// cluster from the global config, and the roster read it is handed. bus.go
// builds the production filter here, so a test can build the same one.
//
// The role is the session's own (DIRECTOR_ROLE), which is what its presence row
// carries. The global role is only the address class, and every research
// supervisor holds it as "supervisor", so using it would make a research seat
// answer to <team>-supervisor, the team supervisor's own alias. A session with
// no local role falls back to the global role, as before.
func newForFilter(self Sender, gcfg globalConfig, roster func(ctx context.Context) ([]map[string]any, error)) *forFilter {
	role := self.Role
	if role == "" {
		role = gcfg.Role
	}
	return &forFilter{agentID: self.AgentID, team: self.Team, role: role, cluster: gcfg.Cluster, now: time.Now, logf: log.Printf, roster: roster}
}

// forFilterStatus is the one line printed when the global tier attaches, so a
// seat whose filter is on but drops nothing can be told from one where it is
// off. It carries the team the filter compares against and never the value of
// DIRECTOR_FOR_FILTER.
func forFilterStatus(env, team, cluster, role string) string {
	switch env {
	case "1":
		return "director-mcp: for-filter on, team=" + team + " cluster=" + cluster + " role=" + role
	case "":
		return "director-mcp: for-filter off"
	}
	return "director-mcp: for-filter off (DIRECTOR_FOR_FILTER is set but not 1)"
}

// forTexts returns the text after FOR on each leading FOR line of data. Blank
// lines before the first FOR line are skipped; the list ends at the first line
// that is not a FOR line, blank lines included, so a FOR line after other text
// is never read. The match is anchored and ignores case. Each line's text ends
// at the first ".", ";" or ":" that is followed by whitespace or the end of the
// line, so prose after the names is not read as names, while a role:// name and
// a dotted seat name stay whole. No FOR line gives nil.
func forTexts(data string) []string {
	var out []string
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimRight(line, "\r")
		if len(out) == 0 && strings.TrimSpace(line) == "" {
			continue
		}
		m := forLine.FindStringSubmatch(line)
		if m == nil {
			break
		}
		text := m[1]
		if loc := forEnd.FindStringIndex(text); loc != nil {
			text = text[:loc[0]]
		}
		out = append(out, text)
	}
	return out
}

// forTokens reads the FOR text as a comma-separated list and returns its names.
// See forList.
func forTokens(text string) []string {
	names, _ := forList(text)
	return names
}

// forList reads the FOR text as a comma-separated list and returns its names
// and the prose that ended the list. The first element is always names. A later
// element is names when, with the joiners "and", "or" and "only" removed, each
// run of words between joiners is one token; otherwise it is prose, and it ends
// the list along with everything after it. A lone ticket id (forTicket, "I-578"
// in any case) after a comma ("FOR a-supervisor, I-578:") is prose too, so it is
// never a name that would spoil the line (aae-orc-c93bj). The names read before
// the ticket id in the same element are kept. Any other lone token is a name,
// and one that is not seat-shaped leaves the line ambiguous, which delivers. Trailing . ; : are trimmed from each
// name. Seat names hold no spaces, so splitting on whitespace never cuts a name.
// The prose words come back whole, for the this-seat scan in drops.
func forList(text string) (names, prose []string) {
	els := strings.Split(text, ",")
	for i, el := range els {
		var got []string
		run, isProse := 0, false
		for _, f := range strings.Fields(el) {
			f = strings.TrimRight(f, ".;:")
			if f == "" {
				continue
			}
			if f == "and" || f == "or" || f == "only" {
				run = 0
				continue
			}
			run++
			if i > 0 && run > 1 {
				isProse = true
				break
			}
			if i > 0 && forTicket.MatchString(f) {
				names = append(names, got...)
				isProse = true
				break
			}
			got = append(got, f)
		}
		if isProse {
			for _, rest := range els[i:] {
				prose = append(prose, strings.Fields(rest)...)
			}
			break
		}
		names = append(names, got...)
	}
	return names, prose
}

// forProseNamesSelf reports whether the cut prose holds this seat's own full
// name as a whole token, in any case: its agent id, or the id without the -gN-M
// suffix. Role words, team names and prefixes do not count, so ordinary prose
// about other seats does not deliver everywhere. A token written name@cluster
// counts when the cluster is this seat's cluster; name@other-cluster does not.
func (f *forFilter) forProseNamesSelf(prose []string) bool {
	own := []string{f.agentID, forGenSfx.ReplaceAllString(f.agentID, "")}
	for _, w := range prose {
		w = strings.Trim(w, ".,;:!?()[]\"'`")
		if i := strings.LastIndex(w, "@"); i >= 0 {
			if c := w[i+1:]; c != "" && c != f.cluster {
				continue // name@other-cluster is not this seat
			}
			w = w[:i]
		}
		for _, n := range own {
			if n != "" && strings.EqualFold(w, n) {
				return true
			}
		}
	}
	return false
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

// otherSeat reports whether a token resolves, through fresh presence rows on
// the cluster it names (any cluster when it names none, as a remote seat's name
// is written without one in the fan-out), only to seats that are not this seat. It is seat-granular: a sibling seat of this seat's
// own team is another seat. Two kinds of row are this seat and deliver: its own
// row, and a predecessor's, a row of this team whose id differs only by its
// generation, with the same replica index, so mail for a seat reaches its successor during a shift. A name
// that rows of different teams answer to is a doubt and delivers, as does a
// name no fresh row answers to.
func (f *forFilter) otherSeat(name, cluster string, rows []map[string]any) bool {
	own := forGenOnly.ReplaceAllString(f.agentID, "$1")
	found, team0 := false, ""
	for _, r := range rows {
		team, _ := r["team"].(string)
		agent, _ := r["agent_id"].(string)
		role, _ := r["role"].(string)
		rc, _ := r["cluster"].(string)
		if team == "" || agent == "" || !f.fresh(r) || (cluster != "" && rc != cluster) {
			continue
		}
		if !forHas(forNames(agent, team, role), name) {
			continue
		}
		if agent == f.agentID || (team == f.team && forGenOnly.ReplaceAllString(agent, "$1") == own) {
			return false
		}
		if found && team != team0 {
			return false
		}
		found, team0 = true, team
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
// every token on the leading FOR lines resolves to a seat other than this one.
func (f *forFilter) drops(ctx context.Context, e *Envelope) (bool, string) {
	texts := forTexts(e.Content.Data)
	if len(texts) == 0 {
		return false, ""
	}
	var toks, prose []string
	for _, tx := range texts {
		n, p := forList(tx)
		toks = append(toks, n...)
		prose = append(prose, p...)
	}
	text := strings.Join(texts, ", ")
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
	if f.forProseNamesSelf(prose) {
		return false, "" // this seat is named in the prose: deliver
	}
	rows, err := f.roster(ctx)
	if err != nil {
		return false, ""
	}
	f.first.Do(func() {
		note := f.note
		if note == nil {
			note = log.Printf
		}
		note("director-mcp: for-filter first decision read %d roster rows (roster rows are the global presence rows)", len(rows))
	})
	for _, t := range toks {
		name, cluster := forSplit(t)
		if !f.otherSeat(name, cluster, rows) {
			return false, ""
		}
	}
	return true, text
}

// foreign is the one decision whether the filter drops a message: false with
// the filter off, otherwise drops(), with every doubt going to delivery. It
// acks and counts nothing. dropForeign acts on it by acking, and the cue's
// count (cue_filter.go) leaves out what it reports true, so the two cannot
// drift (director#572).
func (g *globalTier) foreign(ctx context.Context, e *Envelope) (bool, string) {
	if g.forFilter == nil {
		return false, ""
	}
	return g.forFilter.drops(ctx, e)
}

// dropForeign is the one place a received message meets the filter. Every path
// that takes mail off the global durable calls it before it hands the message
// on: the blocking fetch (fetchOne) and the no-wait pull behind takeWaiting and
// receiveBatch (pullNoWait). A third receive path must call it too.
//
// It reports true when the message was for another seat; the message is
// then acked (confirmed) and logged with its id, and the caller does not return
// it. With the filter off it reports false and does nothing. A failed ack still
// reports true, so a message proven foreign is never delivered; the broker
// redelivers it and it is dropped again.
func (g *globalTier) dropForeign(ctx context.Context, m jetstream.Msg, e *Envelope, seq uint64) bool {
	drop, text := g.foreign(ctx, e)
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

// forFilterSummary is the for_filter field of inbox_summary: the filter's state
// and how many messages it has dropped since this session attached the global
// tier. It reads a counter and the environment's shape, never a value, and
// acks nothing. A session with no global tier has no such field.
func (b *Bus) forFilterSummary() map[string]any {
	if b.globalCfg == nil {
		return nil
	}
	b.globalMu.Lock()
	g := b.global
	b.globalMu.Unlock()
	env := os.Getenv("DIRECTOR_FOR_FILTER")
	state := "off"
	var dropped int64
	switch {
	case g != nil && g.forFilter != nil, g == nil && env == "1":
		state = "on"
	case env != "" && env != "1":
		state = "set but not 1"
	}
	if g != nil {
		dropped = g.forDrops.Load()
	}
	return map[string]any{"state": state, "dropped": dropped}
}
