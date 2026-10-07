package main

// The ask ledger (sim/design/ask-ledger.md, part A1): one row per REQUEST,
// derived from what the local AGENT_AUDIT stream recorded. This file is the
// pure core: it takes audit records and id observations and keeps rows. It
// opens no connection and reads no clock; the reader (askreader.go) feeds it.
//
// Diagnostic, not a gate (SOUL section 8, ADR-007): nothing here blocks a
// send, a merge or a dispatch.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
)

const (
	askPassInterval = 30 * time.Second // the reader runs a pass at least this often
	presenceTTL     = 90 * time.Second // AGENT_STATE's TTL
	// resolveMargin is how long after an expired id entry was last seen the
	// entry may still have been live: the reader sees it at most one pass
	// interval late, and presence outlives its last beat by the TTL.
	resolveMargin = askPassInterval + presenceTTL
	alarmAfter    = 15 * time.Minute // the one threshold table (design section 7)
	staleAfter    = 72 * time.Hour   // the inbox's own max age
	closedKeep    = 30 * 24 * time.Hour
	idKeep        = 30 * 24 * time.Hour
	lineMax       = 120
)

// Row states (design section 5).
const (
	askSent      = "sent"
	askAcked     = "acked"
	askWorking   = "working"
	askBlocked   = "blocked"
	askAnswered  = "answered"
	askRefused   = "refused"
	askFailed    = "failed"
	askCancelled = "cancelled"
)

// auditRec is one record read from a broker's AGENT_AUDIT. At is the stream's
// own timestamp, which the reader trusts over the sender's claimed sent_at.
type auditRec struct {
	Broker  string
	Seq     uint64
	At      time.Time
	Refused bool
	Env     Envelope
}

// askParty is one end of an ask, as sent and as resolved (design section 4a).
type askParty struct {
	Address   string `json:"address"`
	AgentID   string `json:"agent_id,omitempty"`
	Workspace string `json:"workspace,omitempty"`
	Team      string `json:"team"`
	Role      string `json:"role"`
}

type askRow struct {
	Ask         string   `json:"ask"`
	Asker       askParty `json:"asker"`
	Owner       askParty `json:"owner"`
	SentAt      string   `json:"sent_at"`
	SellBy      string   `json:"sell_by"`
	Line        string   `json:"line"`
	State       string   `json:"state"`
	StateAt     string   `json:"state_at"`
	BlockedOn   string   `json:"blocked_on,omitempty"`
	DeliveredAt string   `json:"delivered_at,omitempty"` // part A4; empty in A1
	Links       []string `json:"links"`
	Brokers     []string `json:"brokers"`

	// Kept for the reader's own bookkeeping, and persisted with the row.
	AskedAt     time.Time `json:"asked_at"`        // the REQUEST's stream timestamp
	LastMsgAt   time.Time `json:"last_msg_at"`     // the latest message of the thread, stream timestamp
	ClosedAt    time.Time `json:"closed_at"`       // set when a closing state is reached
	AskerRole   string    `json:"asker_wire_role"` // the sender.role the REQUEST carried, if any
	ThreadIDs   []string  `json:"thread_ids"`      // message ids that belong to this row
	ReplyRole   string    `json:"reply_role"`      // step 3: the owner's own declared role
	OwnerRoleBy string    `json:"owner_role_by"`   // wire, table, reply or empty
}

// idEntry is one (team, role) the reader saw an agent id under (step 2).
type idEntry struct {
	Agent     string    `json:"agent"`
	Team      string    `json:"team"`
	Role      string    `json:"role"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	Live      bool      `json:"live"`
}

// idObs is one thing a pass saw: a live presence key or a durable's filters.
type idObs struct {
	Agent string
	Team  string
	Role  string
}

// globalObs is one live principal in the hub's GLOBAL_PRESENCE: a cluster's
// supervisor, or the director (no cluster).
type globalObs struct {
	Cluster string
	Role    string
}

// globalEntry is a hub principal the reader saw, kept like an id table entry.
type globalEntry struct {
	Cluster   string    `json:"cluster"`
	Role      string    `json:"role"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	Live      bool      `json:"live"`
}

// globalTeam stands in for the team of the director, which has no cluster.
const globalTeam = "global"

type readerGap struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

type askLedger struct {
	Rows     map[string]*askRow   `json:"rows"`
	Seen     map[string]time.Time `json:"seen"`       // message ids seen on the audit stream
	IDs      []idEntry            `json:"ids"`        // the id table, with expired entries kept 30 days
	Tombs    []idEntry            `json:"tombs"`      // dropped entries, kept while a row resolved through the id is kept
	GlobalID []globalEntry        `json:"global_ids"` // hub principals seen in GLOBAL_PRESENCE (part A2)
	Orphans  map[string]int       `json:"orphans"`    // per broker
	LastSeq  map[string]uint64    `json:"last_seq"`
	LastPass time.Time            `json:"last_pass"`
	Down     []readerGap          `json:"down"`
	Dropped  int                  `json:"dropped"` // rows dropped in the latest pass

	threads map[string]string // message id -> ask id, rebuilt by reindex
}

const seenMax = 20000

func newAskLedger() *askLedger {
	return &askLedger{
		Rows:    map[string]*askRow{},
		Seen:    map[string]time.Time{},
		Orphans: map[string]int{},
		LastSeq: map[string]uint64{},
		threads: map[string]string{},
	}
}

// reindex rebuilds the message-to-row index after a load.
func (l *askLedger) reindex() {
	l.threads = map[string]string{}
	for ask, r := range l.Rows {
		l.threads[ask] = ask
		for _, id := range r.ThreadIDs {
			l.threads[id] = ask
		}
	}
	if l.Rows == nil {
		l.Rows = map[string]*askRow{}
	}
	if l.Seen == nil {
		l.Seen = map[string]time.Time{}
	}
	if l.Orphans == nil {
		l.Orphans = map[string]int{}
	}
	if l.LastSeq == nil {
		l.LastSeq = map[string]uint64{}
	}
}

const (
	teamUnresolved = "unresolved"
	roleUnresolved = "unresolved"
	roleAmbiguous  = "unresolved: ambiguous"
	roleNoneDecl   = "unresolved: no role declared"
)

func isOpen(state string) bool {
	switch state {
	case askSent, askAcked, askWorking, askBlocked:
		return true
	}
	return false
}

// firstLine is D3 (b): the first line of a body, control characters removed,
// capped at lineMax runes. It is data, never an instruction.
func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > lineMax {
		s = string(r[:lineMax])
	}
	return s
}

func parseAddress(a string) (scheme, team, rest string) {
	scheme, after, ok := strings.Cut(a, "://")
	if !ok {
		return "", "", ""
	}
	team, rest, _ = strings.Cut(after, "/")
	return scheme, team, rest
}

func addUnique(list []string, vals ...string) []string {
	for _, v := range vals {
		found := false
		for _, x := range list {
			if x == v {
				found = true
				break
			}
		}
		if !found {
			list = append(list, v)
		}
	}
	return list
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// Ingest applies one audit record. A record is applied once: a message id
// already seen only adds its broker to the row it belongs to (design section
// 6: rows from each broker's reader merge by message_id).
func (l *askLedger) Ingest(rec auditRec) {
	if rec.Seq > l.LastSeq[rec.Broker] {
		l.LastSeq[rec.Broker] = rec.Seq
	}
	if rec.Refused {
		return
	}
	e := rec.Env
	if e.MessageID == "" {
		return
	}
	if _, dup := l.Seen[e.MessageID]; dup {
		if ask, ok := l.threads[e.MessageID]; ok {
			r := l.Rows[ask]
			r.Brokers = addUnique(r.Brokers, rec.Broker)
		}
		return
	}
	l.Seen[e.MessageID] = rec.At
	if e.Performative == "REQUEST" {
		if e.InReplyTo != "" {
			if ask, ok := l.threads[e.InReplyTo]; ok && isOpen(l.Rows[ask].State) {
				l.join(l.Rows[ask], rec)
				return
			}
		}
		l.open(rec)
		return
	}
	if e.InReplyTo == "" {
		return
	}
	ask, ok := l.threads[e.InReplyTo]
	if !ok {
		if _, known := l.Seen[e.InReplyTo]; !known {
			l.Orphans[rec.Broker]++
		}
		return
	}
	r := l.Rows[ask]
	l.join(r, rec)
	r.apply(e, rec.At)
}

func (l *askLedger) open(rec auditRec) {
	e := rec.Env
	r := &askRow{
		Ask:       e.MessageID,
		SentAt:    e.SentAt,
		SellBy:    "none",
		Line:      firstLine(e.Content.Data),
		State:     askSent,
		StateAt:   stamp(rec.At),
		AskedAt:   rec.At,
		LastMsgAt: rec.At,
		Links:     []string{},
		Brokers:   []string{rec.Broker},
		ThreadIDs: []string{e.MessageID},
		AskerRole: e.Sender.Role,
		Asker:     askParty{AgentID: e.Sender.AgentID, Workspace: e.Sender.Workspace, Team: teamUnresolved, Role: roleUnresolved},
		Owner:     askParty{Address: e.Recipient.Address, Team: teamUnresolved, Role: roleUnresolved},
	}
	if e.ReplyBy != "" {
		r.SellBy = e.ReplyBy
	}
	if scheme, _, rest := parseAddress(e.Recipient.Address); scheme == "agent" {
		r.Owner.AgentID = rest
	}
	r.Links = addUnique(r.Links, e.Content.Refs...)
	l.Rows[r.Ask] = r
	l.threads[r.Ask] = r.Ask
}

// join adds a message of the thread to a row: its id, its refs, its time.
func (l *askLedger) join(r *askRow, rec auditRec) {
	e := rec.Env
	r.ThreadIDs = addUnique(r.ThreadIDs, e.MessageID)
	l.threads[e.MessageID] = r.Ask
	r.Links = addUnique(r.Links, e.Content.Refs...)
	r.Brokers = addUnique(r.Brokers, rec.Broker)
	if rec.At.After(r.LastMsgAt) {
		r.LastMsgAt = rec.At
	}
	if e.Performative != "REQUEST" && r.ReplyRole == "" && e.Sender.Role != "" && e.Sender.AgentID == r.Owner.AgentID && r.Owner.AgentID != "" {
		r.ReplyRole = e.Sender.Role // step 3: the owner's own declaration
	}
}

func isStatus(e Envelope) (kind, on string) {
	if e.Performative != "INFORM" || e.Content.Type != "signal" {
		return "", ""
	}
	d := strings.TrimSpace(e.Content.Data)
	if d == "working" {
		return "working", ""
	}
	if rest, ok := strings.CutPrefix(d, "blocked-on "); ok && strings.TrimSpace(rest) != "" {
		return "blocked", strings.TrimSpace(rest)
	}
	return "", ""
}

// apply moves the row's state for one reply (design section 5). A closed row
// stays closed.
func (r *askRow) apply(e Envelope, at time.Time) {
	if !isOpen(r.State) {
		return
	}
	set := func(state string) {
		r.State = state
		r.StateAt = stamp(at)
		if !isOpen(state) {
			r.ClosedAt = at
		}
		if state != askBlocked {
			r.BlockedOn = ""
		}
	}
	switch e.Performative {
	case "AGREE":
		if r.State == askSent {
			set(askAcked)
		}
	case "INFORM":
		switch kind, on := isStatus(e); kind {
		case "working":
			set(askWorking)
		case "blocked":
			set(askBlocked)
			r.BlockedOn = on
		default:
			set(askAnswered)
		}
	case "REFUSE":
		set(askRefused)
	case "FAILURE":
		set(askFailed)
	case "CANCEL":
		if e.Sender.AgentID == r.Asker.AgentID {
			set(askCancelled)
		}
	}
}

// ObserveIDs records one pass's live id observations at now (step 2).
func (l *askLedger) ObserveIDs(now time.Time, live []idObs) {
	for i := range l.IDs {
		l.IDs[i].Live = false
	}
	for _, o := range live {
		if o.Agent == "" || o.Team == "" {
			continue
		}
		matched := false
		for i := range l.IDs {
			e := &l.IDs[i]
			if e.Agent != o.Agent || e.Team != o.Team {
				continue
			}
			if e.Role != o.Role && e.Role != "" && o.Role != "" {
				continue
			}
			if e.Role == "" {
				e.Role = o.Role
			}
			e.LastSeen, e.Live, matched = now, true, true
			break
		}
		if !matched {
			l.IDs = append(l.IDs, idEntry{Agent: o.Agent, Team: o.Team, Role: o.Role, FirstSeen: now, LastSeen: now, Live: true})
		}
	}
}

// ObserveGlobal records one pass's live GLOBAL_PRESENCE principals at now
// (step 2 at the hub tier, part A2). A global address names a cluster and a
// role and no agent id, so the entry is the principal, not an id.
func (l *askLedger) ObserveGlobal(now time.Time, live []globalObs) {
	for i := range l.GlobalID {
		l.GlobalID[i].Live = false
	}
	for _, o := range live {
		matched := false
		for i := range l.GlobalID {
			e := &l.GlobalID[i]
			if e.Cluster == o.Cluster && e.Role == o.Role {
				e.LastSeen, e.Live, matched = now, true, true
				break
			}
		}
		if !matched {
			l.GlobalID = append(l.GlobalID, globalEntry{Cluster: o.Cluster, Role: o.Role, FirstSeen: now, LastSeen: now, Live: true})
		}
	}
}

// globalObsFromKey reads a GLOBAL_PRESENCE key: presence.director.<instance>
// for the director, presence.<cluster>.supervisor.<instance> for a cluster's
// supervisor (global.go globalPresencePrefix). Anything else is not a global
// principal.
func globalObsFromKey(key string) (globalObs, bool) {
	p := strings.Split(key, ".")
	if len(p) < 3 || p[0] != "presence" {
		return globalObs{}, false
	}
	if p[1] == roleDirector {
		if len(p) != 3 {
			return globalObs{}, false
		}
		return globalObs{Role: roleDirector}, true
	}
	if len(p) != 4 || p[2] != roleSupervisor {
		return globalObs{}, false
	}
	return globalObs{Cluster: p[1], Role: roleSupervisor}, true
}

// NotePass records that a pass started at now, listing a gap when the last
// pass was more than the presence TTL ago.
func (l *askLedger) NotePass(now time.Time) {
	if !l.LastPass.IsZero() && now.Sub(l.LastPass) > presenceTTL {
		l.Down = append(l.Down, readerGap{From: l.LastPass, To: now})
	}
	l.LastPass = now
}

type lookupStatus int

const (
	lkNone lookupStatus = iota
	lkAmbiguous
	lkFound
)

// lookup is step 2 for one agent id at the time an ask was sent. A single
// (team, role) wins. With more than one, the live entry wins only when every
// other entry was last seen before the ask was sent, by more than the margin.
func (l *askLedger) lookup(agent, wantTeam, wantRole string, at time.Time) (idEntry, lookupStatus) {
	var cands []idEntry
	for _, set := range [][]idEntry{l.IDs, l.Tombs} {
		for _, e := range set {
			if e.Agent != agent || (wantTeam != "" && e.Team != wantTeam) {
				continue
			}
			if wantRole != "" && e.Role != "" && e.Role != wantRole {
				continue
			}
			cands = append(cands, e)
		}
	}
	switch len(cands) {
	case 0:
		return idEntry{}, lkNone
	case 1:
		return cands[0], lkFound
	}
	var live []idEntry
	for _, e := range cands {
		if e.Live {
			live = append(live, e)
		}
	}
	if len(live) != 1 {
		return idEntry{}, lkAmbiguous
	}
	for _, e := range cands {
		if e.Live {
			continue
		}
		if !at.After(e.LastSeen.Add(resolveMargin)) {
			return idEntry{}, lkAmbiguous
		}
	}
	return live[0], lkFound
}

// Resolve (re)resolves every row's parties. A value from the wire or from the
// owner's own reply is not revisited; a value from the id table is recomputed
// each pass, so a second entry makes it ambiguous again (design section 4a).
func (l *askLedger) Resolve(now time.Time) {
	for _, r := range l.Rows {
		l.resolveOwner(r)
		l.resolveAsker(r)
	}
}

func (l *askLedger) resolveOwner(r *askRow) {
	p := &r.Owner
	scheme, team, rest := parseAddress(p.Address)
	switch scheme {
	case "role":
		p.Team, p.Role, p.AgentID = team, rest, ""
		return
	case "agent":
		p.AgentID = rest
		p.Team = team
	case "global":
		p.Team, p.Role = teamUnresolved, roleUnresolved
		cluster, role, err := parseGlobalAddress(p.Address)
		if err != nil {
			return
		}
		for _, g := range l.GlobalID {
			if g.Cluster == cluster && g.Role == role {
				p.Team, p.Role = cluster, role
				if cluster == "" {
					p.Team = globalTeam
				}
				return
			}
		}
		return
	default: // broadcast: no team on the wire, and nothing to read it from
		p.Team, p.Role = teamUnresolved, roleUnresolved
		return
	}
	e, st := l.lookup(p.AgentID, team, "", r.AskedAt)
	switch {
	case st == lkFound && e.Role != "":
		p.Role = e.Role
	case r.ReplyRole != "":
		p.Role = r.ReplyRole
	case st == lkFound:
		p.Role = roleNoneDecl
	case st == lkAmbiguous:
		p.Role = roleAmbiguous
	default:
		p.Role = roleUnresolved
	}
}

func (l *askLedger) resolveAsker(r *askRow) {
	p := &r.Asker
	e, st := l.lookup(p.AgentID, "", r.AskerRole, r.AskedAt)
	switch st {
	case lkFound:
		p.Team = e.Team
		switch {
		case r.AskerRole != "":
			p.Role = r.AskerRole
		case e.Role != "":
			p.Role = e.Role
		default:
			p.Role = roleNoneDecl
		}
	case lkAmbiguous:
		p.Team, p.Role = roleAmbiguous, roleAmbiguous
	default:
		p.Team = teamUnresolved
		p.Role = roleUnresolved
		if r.AskerRole != "" {
			p.Role = r.AskerRole
		}
	}
}

// Sweep drops what has aged out and counts the drops (design section 6).
func (l *askLedger) Sweep(now time.Time) {
	l.Dropped = 0
	for ask, r := range l.Rows {
		var gone bool
		if isOpen(r.State) {
			gone = now.Sub(r.LastMsgAt) > staleAfter+closedKeep
		} else {
			gone = now.Sub(r.ClosedAt) > closedKeep
		}
		if gone {
			for _, id := range r.ThreadIDs {
				delete(l.threads, id)
			}
			delete(l.Rows, ask)
			l.Dropped++
		}
	}
	kept := l.IDs[:0]
	for _, e := range l.IDs {
		if !e.Live && now.Sub(e.LastSeen) > idKeep {
			l.Tombs = append(l.Tombs, e)
			continue
		}
		kept = append(kept, e)
	}
	l.IDs = kept
	// A tomb is kept only while a row resolved through its id is kept.
	used := map[string]bool{}
	for _, r := range l.Rows {
		used[r.Owner.AgentID] = true
		used[r.Asker.AgentID] = true
	}
	tk := l.Tombs[:0]
	for _, e := range l.Tombs {
		if used[e.Agent] {
			tk = append(tk, e)
		}
	}
	l.Tombs = tk
	// A hub principal is kept while it is live, or recently seen, or while a
	// kept row's owner names it (so a resolved row does not flap to
	// unresolved when the presence entry expires).
	named := map[string]bool{}
	for _, r := range l.Rows {
		if c, ro, err := parseGlobalAddress(r.Owner.Address); err == nil {
			named[c+"/"+ro] = true
		}
	}
	gk := l.GlobalID[:0]
	for _, g := range l.GlobalID {
		if g.Live || now.Sub(g.LastSeen) <= idKeep || named[g.Cluster+"/"+g.Role] {
			gk = append(gk, g)
		}
	}
	l.GlobalID = gk
	for id, at := range l.Seen {
		if now.Sub(at) > staleAfter {
			delete(l.Seen, id)
		}
	}
	if len(l.Seen) > seenMax {
		type kv struct {
			id string
			at time.Time
		}
		all := make([]kv, 0, len(l.Seen))
		for id, at := range l.Seen {
			all = append(all, kv{id, at})
		}
		sort.Slice(all, func(i, j int) bool { return all[i].at.Before(all[j].at) })
		for _, x := range all[:len(all)-seenMax] {
			delete(l.Seen, x.id)
		}
	}
	downKept := l.Down[:0]
	for _, g := range l.Down {
		if now.Sub(g.To) <= staleAfter {
			downKept = append(downKept, g)
		}
	}
	l.Down = downKept
}

// idObsFromPresence reads a presence key (presence.<team>.<id>.<instance>)
// and its value.
func idObsFromPresence(key string, value []byte) (idObs, bool) {
	parts := strings.Split(key, ".")
	if len(parts) != 4 || parts[0] != "presence" {
		return idObs{}, false
	}
	var rec struct {
		Role string `json:"role"`
	}
	_ = json.Unmarshal(value, &rec)
	return idObs{Agent: parts[2], Team: parts[1], Role: rec.Role}, true
}

// idObsFromDurable reads a seat durable (mcp_<agent>_<instance>) and its
// filter subjects the way teamAndRole does, without its last step: a role is
// never guessed from the agent id, because instance ids are not identity.
func idObsFromDurable(name string, filters []string) (idObs, bool) {
	rest, ok := strings.CutPrefix(name, "mcp_")
	if !ok || strings.HasPrefix(name, "mcp_global_") {
		return idObs{}, false
	}
	i := strings.LastIndex(rest, "_")
	if i <= 0 {
		return idObs{}, false
	}
	o := idObs{Agent: rest[:i]}
	for _, f := range filters {
		p := strings.Split(f, ".")
		switch {
		case len(p) == 6 && p[0] == "agent" && p[3] == "role":
			o.Team, o.Role = p[2], p[4]
		case len(p) == 5 && p[0] == "agent" && o.Team == "":
			o.Team = p[2]
		}
	}
	if o.Team == "" {
		return idObs{}, false
	}
	return o, true
}

type askAlarm struct {
	Ask    string `json:"ask"`
	Text   string `json:"text"`
	AgeSec int64  `json:"age_seconds"`
}

type askChain struct {
	Asks  []string `json:"asks"`
	Cycle bool     `json:"cycle"`
}

type rollupRow struct {
	Key           string `json:"key"`
	Open          int    `json:"open"`
	Unacked       int    `json:"unacked"`
	Blocked       int    `json:"blocked"`
	OldestOpenSec int64  `json:"oldest_open_seconds"`
}

type unresolvedGap struct {
	Ask    string `json:"ask"`
	Party  string `json:"party"`
	Reason string `json:"reason"`
}

type askGaps struct {
	StreamsNotRead []string        `json:"streams_not_read"`
	Orphans        map[string]int  `json:"orphans"`
	NoReplyBy      []string        `json:"no_reply_by"`
	Unresolved     []unresolvedGap `json:"unresolved"`
	Stale          int             `json:"stale"`
	StaleOldestSec int64           `json:"stale_oldest_seconds"`
	Dropped        int             `json:"dropped"`
	ReaderDown     []string        `json:"reader_down"`
	NamesSB4       bool            `json:"names_sb4"`
}

type askReport struct {
	GeneratedAt string      `json:"generated_at"`
	Partial     bool        `json:"partial"`
	Alarms      []askAlarm  `json:"alarms"`
	Chains      []askChain  `json:"chains"`
	ByOwner     []rollupRow `json:"rollup_by_owner"`
	ByAsker     []rollupRow `json:"rollup_by_asker"`
	Gaps        askGaps     `json:"gaps"`
	Rows        []*askRow   `json:"rows"`
}

func humanAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

func rollupKey(p askParty) string {
	switch {
	case p.Role == roleAmbiguous || p.Team == roleAmbiguous:
		return roleAmbiguous
	case p.Role == roleUnresolved || p.Team == teamUnresolved:
		return "unresolved"
	}
	return p.Team + "/" + p.Role
}

func pastSellBy(sellBy string, now time.Time) bool {
	if sellBy == "" || sellBy == "none" {
		return false
	}
	t, err := time.Parse(time.RFC3339, sellBy)
	return err == nil && now.After(t)
}

func (l *askLedger) isStale(r *askRow, now time.Time) bool {
	return isOpen(r.State) && now.Sub(r.LastMsgAt) > staleAfter
}

func partyLabel(p askParty) string {
	if p.Address != "" {
		return p.Address
	}
	return p.AgentID
}

// Report builds the read output at now. notRead names the streams the reader
// could not read. all lists stale rows among the alarms too.
func (l *askLedger) Report(now time.Time, notRead []string, all bool) askReport {
	rep := askReport{
		GeneratedAt: stamp(now),
		Partial:     len(notRead) > 0,
		Alarms:      []askAlarm{},
		Chains:      []askChain{},
		ByOwner:     []rollupRow{},
		ByAsker:     []rollupRow{},
		Rows:        []*askRow{},
	}
	rep.Gaps = askGaps{
		StreamsNotRead: append([]string{}, notRead...),
		Orphans:        map[string]int{},
		NoReplyBy:      []string{},
		Unresolved:     []unresolvedGap{},
		ReaderDown:     []string{},
		Dropped:        l.Dropped,
	}
	for b, n := range l.Orphans {
		rep.Gaps.Orphans[b] = n
	}
	for _, g := range l.Down {
		rep.Gaps.ReaderDown = append(rep.Gaps.ReaderDown, fmt.Sprintf("reader down %s to %s", stamp(g.From), stamp(g.To)))
	}
	switch {
	case l.LastPass.IsZero():
		rep.Gaps.ReaderDown = append(rep.Gaps.ReaderDown, "reader has not run: no pass is recorded")
	case now.Sub(l.LastPass) > presenceTTL:
		rep.Gaps.ReaderDown = append(rep.Gaps.ReaderDown, fmt.Sprintf("reader down %s to now", stamp(l.LastPass)))
	}

	rows := make([]*askRow, 0, len(l.Rows))
	for _, r := range l.Rows {
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].AskedAt.Equal(rows[j].AskedAt) {
			return rows[i].AskedAt.Before(rows[j].AskedAt)
		}
		return rows[i].Ask < rows[j].Ask
	})
	rep.Rows = rows

	owner := map[string]*rollupRow{}
	asker := map[string]*rollupRow{}
	bump := func(m map[string]*rollupRow, key string, r *askRow) {
		g := m[key]
		if g == nil {
			g = &rollupRow{Key: key}
			m[key] = g
		}
		g.Open++
		if r.State == askSent {
			g.Unacked++
		}
		if r.State == askBlocked {
			g.Blocked++
		}
		if age := int64(now.Sub(r.AskedAt).Seconds()); age > g.OldestOpenSec {
			g.OldestOpenSec = age
		}
	}
	var staleOldest time.Duration
	for _, r := range rows {
		if !isOpen(r.State) {
			continue
		}
		if l.isStale(r, now) {
			rep.Gaps.Stale++
			if age := now.Sub(r.LastMsgAt); age > staleOldest {
				staleOldest = age
			}
			if all {
				rep.Alarms = append(rep.Alarms, l.alarm(r, now, true))
			}
			continue
		}
		bump(owner, rollupKey(r.Owner), r)
		bump(asker, rollupKey(r.Asker), r)
		if r.SellBy == "none" {
			rep.Gaps.NoReplyBy = append(rep.Gaps.NoReplyBy, r.Ask)
		}
		if (r.State == askSent && now.Sub(r.AskedAt) > alarmAfter) || pastSellBy(r.SellBy, now) {
			rep.Alarms = append(rep.Alarms, l.alarm(r, now, false))
		}
		for _, side := range []struct {
			who string
			p   askParty
		}{{"owner", r.Owner}, {"asker", r.Asker}} {
			if side.p.Role == roleUnresolved || side.p.Role == roleAmbiguous || side.p.Role == roleNoneDecl ||
				side.p.Team == teamUnresolved || side.p.Team == roleAmbiguous {
				reason := "no record of this id"
				switch {
				case side.p.Role == roleAmbiguous || side.p.Team == roleAmbiguous:
					reason = "ambiguous"
				case side.p.Role == roleNoneDecl:
					reason = "no role declared"
					rep.Gaps.NamesSB4 = true
				}
				rep.Gaps.Unresolved = append(rep.Gaps.Unresolved, unresolvedGap{Ask: r.Ask, Party: side.who + " " + partyLabel(side.p), Reason: reason})
			}
		}
	}
	rep.Gaps.StaleOldestSec = int64(staleOldest.Seconds())
	sort.SliceStable(rep.Alarms, func(i, j int) bool { return rep.Alarms[i].AgeSec > rep.Alarms[j].AgeSec })
	for _, m := range []struct {
		src map[string]*rollupRow
		dst *[]rollupRow
	}{{owner, &rep.ByOwner}, {asker, &rep.ByAsker}} {
		for _, g := range m.src {
			*m.dst = append(*m.dst, *g)
		}
		sort.Slice(*m.dst, func(i, j int) bool { return (*m.dst)[i].Key < (*m.dst)[j].Key })
	}
	rep.Chains = l.chains(rows, now)
	return rep
}

func (l *askLedger) alarm(r *askRow, now time.Time, stale bool) askAlarm {
	age := now.Sub(r.AskedAt)
	why := "unacked"
	if r.State != askSent {
		why = r.State
	}
	text := fmt.Sprintf("%s -> %s: %s (sent %s ago, %s", r.Asker.Role, r.Owner.Role, r.Line, humanAge(age), why)
	if pastSellBy(r.SellBy, now) {
		text += fmt.Sprintf(", past sell_by %s, set by %s", r.SellBy, r.Asker.Role)
	}
	if stale {
		text += ", stale"
	}
	return askAlarm{Ask: r.Ask, Text: text + ")", AgeSec: int64(age.Seconds())}
}

// chains follows each blocked row along blocked_on to the rows that address
// owns, and stops at a repeat, which it reports as a cycle.
func (l *askLedger) chains(rows []*askRow, now time.Time) []askChain {
	out := []askChain{}
	seenChain := map[string]bool{}
	for _, start := range rows {
		if start.State != askBlocked || l.isStale(start, now) {
			continue
		}
		chain := askChain{Asks: []string{start.Ask}}
		visited := map[string]bool{start.Ask: true}
		cur := start
		for cur.State == askBlocked {
			var next *askRow
			for _, r := range rows {
				if isOpen(r.State) && r.Owner.Address == cur.BlockedOn && r.Owner.Address != "" {
					next = r
					break
				}
			}
			if next == nil {
				break
			}
			if visited[next.Ask] {
				chain.Cycle = true
				break
			}
			visited[next.Ask] = true
			chain.Asks = append(chain.Asks, next.Ask)
			cur = next
		}
		key := strings.Join(chain.Asks, ",")
		if chain.Cycle {
			ids := append([]string{}, chain.Asks...)
			sort.Strings(ids)
			key = "cycle:" + strings.Join(ids, ",")
		}
		if !seenChain[key] {
			seenChain[key] = true
			out = append(out, chain)
		}
	}
	return out
}

// asksUsage and asksHelp are the read command's usage and help text.
const asksUsage = "director-mcp asks [--json] [--all] [--help]"

func asksHelp() string {
	return asksUsage + `

Reads the ask ledger the ask-reader keeps and prints, in order: alarms (open
rows still unacked past 15 minutes, or past their sell_by), blocked-on chains,
a rollup per owner role and per asker role, and gaps. It runs no pass of its
own. It is a diagnostic: it always exits 0 and nothing waits on it.

Gaps names everything the ledger could not see: streams the reader could not
read (the view is then partial), orphan replies per broker, open asks with no
reply_by, parties it could not resolve, stale and dropped rows, and the spans
when the reader was down.

An orphan reply is a reply that names an ask the ledger never saw. The orphan
count is how a reader knows the ledger missed traffic, and it covers one case
worth naming: when the hub stores a message but its ack times out, the send
is recorded as refused (Director-Outcome: refused), the ledger opens no row
for it, and the owner's reply then shows as an orphan. That is an ack timeout,
not lost mail.

--json carries every row and the rollups; --all also lists stale rows among
the alarms.
`
}
