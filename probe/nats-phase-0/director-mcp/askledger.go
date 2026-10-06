package main

// The ask ledger (sim/design/ask-ledger.md, part A1): one row per REQUEST,
// derived from what the local AGENT_AUDIT stream recorded. This file is the
// pure core: it takes audit records and id observations and keeps rows. It
// opens no connection and reads no clock; the reader (askreader.go) feeds it.
//
// Diagnostic, not a gate (SOUL section 8, ADR-007): nothing here blocks a
// send, a merge or a dispatch.

import (
	"time"
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
	AskedAt     time.Time `json:"asked_at"`      // the REQUEST's stream timestamp
	LastMsgAt   time.Time `json:"last_msg_at"`   // the latest message of the thread, stream timestamp
	ClosedAt    time.Time `json:"closed_at"`     // set when a closing state is reached
	ThreadIDs   []string  `json:"thread_ids"`    // message ids that belong to this row
	ReplyRole   string    `json:"reply_role"`    // step 3: the owner's own declared role
	OwnerRoleBy string    `json:"owner_role_by"` // wire, table, reply or empty
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

type readerGap struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

type askLedger struct {
	Rows     map[string]*askRow   `json:"rows"`
	Seen     map[string]time.Time `json:"seen"`    // message ids seen on the audit stream
	IDs      []idEntry            `json:"ids"`     // the id table, with expired entries kept 30 days
	Tombs    []idEntry            `json:"tombs"`   // dropped entries, kept while a row resolved through the id is kept
	Orphans  map[string]int       `json:"orphans"` // per broker
	LastSeq  map[string]uint64    `json:"last_seq"`
	LastPass time.Time            `json:"last_pass"`
	Down     []readerGap          `json:"down"`
	Dropped  int                  `json:"dropped"` // rows dropped in the latest pass
}

func newAskLedger() *askLedger {
	return &askLedger{
		Rows:    map[string]*askRow{},
		Seen:    map[string]time.Time{},
		Orphans: map[string]int{},
		LastSeq: map[string]uint64{},
	}
}

const (
	teamUnresolved = "unresolved"
	roleUnresolved = "unresolved"
	roleAmbiguous  = "unresolved: ambiguous"
	roleNoneDecl   = "unresolved: no role declared"
)

// Ingest applies one audit record. Stubbed until the green change.
func (l *askLedger) Ingest(rec auditRec) {}

// ObserveIDs records one pass's live id observations at now. Stubbed.
func (l *askLedger) ObserveIDs(now time.Time, live []idObs) {}

// NotePass records that a pass started at now, listing a gap when the last
// pass was more than the presence TTL ago. Stubbed.
func (l *askLedger) NotePass(now time.Time) {}

// Resolve (re)resolves every row's parties at now. Stubbed.
func (l *askLedger) Resolve(now time.Time) {}

// Sweep drops what has aged out and counts the drops. Stubbed.
func (l *askLedger) Sweep(now time.Time) {}

// idObsFromPresence reads a presence key and value. Stubbed.
func idObsFromPresence(key string, value []byte) (idObs, bool) { return idObs{}, false }

// idObsFromDurable reads a durable's name and filter subjects. Stubbed.
func idObsFromDurable(name string, filters []string) (idObs, bool) { return idObs{}, false }

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

// Report builds the read output at now. notRead names the streams the reader
// could not read. Stubbed.
func (l *askLedger) Report(now time.Time, notRead []string, all bool) askReport {
	return askReport{}
}

// asksUsage and asksHelp are the read command's usage and help text.
const asksUsage = "director-mcp asks [--json] [--all]"

func asksHelp() string { return "" }
