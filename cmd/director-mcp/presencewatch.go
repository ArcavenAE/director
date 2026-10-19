package main

// The presence watch core (sim/design/presence-watch.md, build item 1, issue
// #319). It takes one scan of GLOBAL_PRESENCE and the times of the pass, and
// keeps per-unit state: a supervisor instance, and a cluster made of its
// supervisor instances. It opens no connection and reads no clock; the caller
// supplies both.
//
// Diagnostic, not a gate (SOUL section 8, ADR-007, diagnostic-not-gate.md):
// nothing here blocks a send, a merge or a dispatch.

import "time"

// watchRec is the part of a presence record the watch reads.
type watchRec struct {
	State string    `json:"state"`
	TS    time.Time `json:"ts"`
}

// watchRow is one key of the bucket in one pass. Unfetched is a key whose
// value could not be fetched. Rec is nil, with Unfetched false, when the value
// was fetched and did not parse as a presence record.
type watchRow struct {
	Key       string
	Unfetched bool
	Rec       *watchRec
}

// watchScan is one pass over the bucket. KeysErr is a failed keys call.
type watchScan struct {
	KeysErr bool
	Rows    []watchRow
}

// watchThresholds are the JUDGMENT numbers (design section 3, D4). A later
// item reads them from thresholds.json beside handled_ack_wait.
type watchThresholds struct {
	FlapCount  int
	FlapWindow time.Duration
	FlapQuiet  time.Duration
}

func defaultWatchThresholds() watchThresholds {
	return watchThresholds{FlapCount: 4, FlapWindow: time.Hour, FlapQuiet: 15 * time.Minute}
}

// Unit statuses.
const (
	unitPresent = "present"
	unitAbsent  = "absent"
	unitAsleep  = "asleep"
)

// watchUnit is the state of one supervisor instance ("cluster/instance") or one
// cluster ("cluster").
type watchUnit struct {
	Kind        string      `json:"kind"` // "supervisor" or "cluster"
	Cluster     string      `json:"cluster"`
	Instance    string      `json:"instance,omitempty"`
	Status      string      `json:"status"`
	Unknown     bool        `json:"unknown,omitempty"`
	Since       time.Time   `json:"since"`     // first seen if present, last seen if absent or asleep
	LastSeen    time.Time   `json:"last_seen"` // the last pass that read a row for it
	LastState   string      `json:"last_state,omitempty"`
	AsleepUntil time.Time   `json:"asleep_until,omitempty"`
	SeenRun     int         `json:"seen_run"`
	MissRun     int         `json:"miss_run"`
	Transitions []time.Time `json:"transitions,omitempty"`
	Flapping    bool        `json:"flapping,omitempty"`
}

// watchGap is a span the watch did not observe.
type watchGap struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// watchEvent is one line of the event file.
type watchEvent struct {
	At   time.Time `json:"at"`
	Kind string    `json:"kind"`
	Unit string    `json:"unit,omitempty"`
	Text string    `json:"text,omitempty"`
	From time.Time `json:"from,omitempty"`
	To   time.Time `json:"to,omitempty"`
	// Collapsed marks a transition inside a flapping spell: it is recorded in
	// the file and left out of the notice.
	Collapsed bool `json:"collapsed,omitempty"`
}

// PresenceWatch is the whole watch state.
type PresenceWatch struct {
	Units    map[string]*watchUnit `json:"units"`
	LastPass time.Time             `json:"last_pass"`
	Sleeps   []watchGap            `json:"sleeps,omitempty"`
	Downs    []watchGap            `json:"downs,omitempty"`
	Foreign  []string              `json:"foreign,omitempty"`
	Blind    bool                  `json:"blind,omitempty"`

	th       watchThresholds
	lastMono time.Duration
	haveMono bool
}

func newPresenceWatch(th watchThresholds) *PresenceWatch {
	return &PresenceWatch{Units: map[string]*watchUnit{}, th: th}
}

// Unit returns the unit with this name ("cluster" or "cluster/instance"), or nil.
func (w *PresenceWatch) Unit(name string) *watchUnit { return w.Units[name] }

// Observe records one pass at wall time now and monotonic reading mono (the
// time since the process started, which stops while the host sleeps), and
// returns the events it produced.
func (w *PresenceWatch) Observe(now time.Time, mono time.Duration, scan watchScan) []watchEvent {
	return nil
}

// AbsenceDuration is how long the unit has been absent at now, less any host
// sleep inside that span (R-209).
func (w *PresenceWatch) AbsenceDuration(unit string, now time.Time) time.Duration {
	return 0
}
