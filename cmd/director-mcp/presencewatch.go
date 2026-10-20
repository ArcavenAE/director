package main

// The presence watch core (sim/design/presence-watch.md, build item 1, issue
// #319). It takes one scan of GLOBAL_PRESENCE and the times of the pass, and
// keeps per-unit state: a supervisor instance, and a cluster made of its
// supervisor instances. It opens no connection and reads no clock; the caller
// supplies both.
//
// Diagnostic, not a gate (SOUL section 8, ADR-007, diagnostic-not-gate.md):
// nothing here blocks a send, a merge or a dispatch.

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// watchRec is the part of a presence record the watch reads.
type watchRec struct {
	State string    `json:"state"`
	TS    time.Time `json:"ts"`
	// LostAfter is the optional lost_after field of the record, as written: a
	// duration string the cluster's own admin preference carries (cashp design
	// section 5). It is judged in Observe, never trusted as it stands.
	LostAfter string `json:"lost_after,omitempty"`
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
	// SelfCheck says the scan carries the evidence that the hub read is the hub
	// this director writes to (cashp design section 4): SelfSeen is whether this
	// director's own row was in the bucket, SelfTS its ts, and SelfWant the ts of
	// the row this session last wrote. A scan with SelfCheck false makes no claim.
	SelfCheck bool
	SelfSeen  bool
	SelfTS    time.Time
	SelfWant  time.Time
}

// watchThresholds are the JUDGMENT numbers (design section 3, D4). A later
// item reads them from thresholds.json beside handled_ack_wait.
type watchThresholds struct {
	FlapCount  int
	FlapWindow time.Duration
	FlapQuiet  time.Duration
	// LostAfter is the away-to-lost bound when a cluster carries none, and
	// LostMax the most a cluster's own value may set (cashp design section 5).
	LostAfter time.Duration
	LostMax   time.Duration
	// HubMoveMin clusters going absent within HubMoveWindow, when no other
	// cluster is present, open one hub-moved parent; AlertCap is the most lost
	// and hub-moved notices shown per hour (cashp design section 6).
	HubMoveMin    int
	HubMoveWindow time.Duration
	AlertCap      int
}

func defaultWatchThresholds() watchThresholds {
	return watchThresholds{FlapCount: 4, FlapWindow: time.Hour, FlapQuiet: 15 * time.Minute,
		LostAfter: 30 * time.Minute, LostMax: 24 * time.Hour,
		HubMoveMin: 2, HubMoveWindow: 2 * time.Minute, AlertCap: 6}
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
	Kind        string    `json:"kind"` // "supervisor" or "cluster"
	Cluster     string    `json:"cluster"`
	Instance    string    `json:"instance,omitempty"`
	Status      string    `json:"status"`
	Unknown     bool      `json:"unknown,omitempty"`
	Since       time.Time `json:"since"`     // first seen if present, last seen if absent or asleep
	LastSeen    time.Time `json:"last_seen"` // the last pass that read a row for it
	LastState   string    `json:"last_state,omitempty"`
	AsleepUntil time.Time `json:"asleep_until,omitempty"`
	// FirstTS is the ts of this row on the watch's first sighting of it, and
	// Beating says a later sighting carried a later ts: the row is being renewed
	// (design section 4). Both reset when the row is unseen and on a gap.
	FirstTS     time.Time   `json:"first_ts,omitempty"`
	Beating     bool        `json:"beating,omitempty"`
	SeenRun     int         `json:"seen_run"`
	MissRun     int         `json:"miss_run"`
	Transitions []time.Time `json:"transitions,omitempty"`
	Flapping    bool        `json:"flapping,omitempty"`
	// Lost is set on a cluster unit once its absence has raised lost, so one
	// absence raises it once; a return clears it.
	Lost bool `json:"lost,omitempty"`
	// AbsentAt is when a cluster unit last turned absent, for the hub-move check.
	AbsentAt time.Time `json:"absent_at,omitempty"`
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
	// Capped marks a lost or hub-moved event held back by the hourly cap: it is
	// recorded and not shown.
	Capped bool `json:"capped,omitempty"`
}

// PresenceWatch is the whole watch state.
type PresenceWatch struct {
	Units    map[string]*watchUnit `json:"units"`
	LastPass time.Time             `json:"last_pass"`
	Sleeps   []watchGap            `json:"sleeps,omitempty"`
	Downs    []watchGap            `json:"downs,omitempty"`
	Blind    bool                  `json:"blind,omitempty"`
	// SelfUnseen is set while the passes do not contain this director's own
	// current row, so the hub read is not known to be the hub it writes to.
	SelfUnseen bool `json:"self_unseen,omitempty"`
	// Bounds is the last good lost_after each cluster's presence record carried,
	// clamped, kept because the row expires when the cluster leaves. BoundNotes is
	// the raw value already reported as ignored or clamped, so each is said once.
	// Parent is the open hub-moved parent, if any. Shown, CapSaid and CapHeld keep
	// the hourly cap across a restart (cashp design section 6).
	Parent  *hubParent  `json:"parent,omitempty"`
	Shown   []time.Time `json:"alerts_shown,omitempty"`
	CapSaid time.Time   `json:"cap_said,omitempty"`
	CapHeld int         `json:"cap_held,omitempty"`

	Bounds     map[string]time.Duration `json:"bounds,omitempty"`
	BoundNotes map[string]string        `json:"bound_notes,omitempty"`
	// Foreign lists each key under presence. that is not the shape the shim
	// writes (D4), once, so it is reported once.
	Foreign []string `json:"foreign,omitempty"`

	th       watchThresholds
	lastMono time.Duration
	haveMono bool
}

func newPresenceWatch(th watchThresholds) *PresenceWatch {
	return &PresenceWatch{Units: map[string]*watchUnit{}, th: th}
}

// Unit returns the unit with this name ("cluster" or "cluster/instance"), or nil.
func (w *PresenceWatch) Unit(name string) *watchUnit { return w.Units[name] }

// parseSupervisorKey reports the cluster and instance of a key of the shape the
// shim writes: presence.<cluster>.supervisor.<instance>, one instance segment.
func parseSupervisorKey(key string) (cluster, instance string, ok bool) {
	p := strings.Split(key, ".")
	if len(p) != 4 || p[0] != "presence" || p[2] != "supervisor" || p[1] == "" || p[3] == "" {
		return "", "", false
	}
	return p[1], p[3], true
}

// asleepUntil reads the declared-sleep convention of set_presence: "asleep" or
// "asleep until <RFC3339>". until is zero when no time was given or it did not
// parse.
func asleepUntil(state string) (asleep bool, until time.Time) {
	s := strings.TrimSpace(state)
	if s != "asleep" && !strings.HasPrefix(s, "asleep until ") {
		return false, time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(strings.TrimPrefix(s, "asleep until"))); err == nil {
		return true, t
	}
	return true, time.Time{}
}

// maxForeign bounds the foreign list kept in the state file.
const maxForeign = 200

func (w *PresenceWatch) noteForeign(key string, now time.Time, ev *[]watchEvent) {
	for _, k := range w.Foreign {
		if k == key {
			return
		}
	}
	if len(w.Foreign) < maxForeign {
		w.Foreign = append(w.Foreign, key)
	}
	*ev = append(*ev, watchEvent{At: now, Kind: "foreign", Text: key})
}

func (w *PresenceWatch) unit(kind, cluster, instance string) (*watchUnit, string) {
	name := cluster
	if kind == "supervisor" {
		name = cluster + "/" + instance
	}
	return w.Units[name], name
}

// transition records an absent or back transition on u and says whether it
// lands inside a flapping spell. The fourth transition inside the window starts
// the spell and is itself reported; later ones are collapsed.
func (w *PresenceWatch) transition(u *watchUnit, name string, now time.Time, ev *[]watchEvent, kind, text string) {
	cut := now.Add(-w.th.FlapWindow)
	keep := u.Transitions[:0]
	for _, t := range u.Transitions {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	u.Transitions = append(keep, now)
	*ev = append(*ev, watchEvent{At: now, Kind: kind, Unit: name, Text: text, Collapsed: u.Flapping})
	if !u.Flapping && len(u.Transitions) >= w.th.FlapCount {
		u.Flapping = true
		*ev = append(*ev, watchEvent{At: now, Kind: "flapping", Unit: name, Text: fmt.Sprintf("%d transitions within %s", len(u.Transitions), w.th.FlapWindow)})
	}
}

// maxGaps bounds the gap lists kept in the state file.
const maxGaps = 100

const (
	// hostSleepSlack is how far wall time may run ahead of the monotonic clock
	// between passes before the difference is called a host sleep.
	hostSleepSlack = 30 * time.Second
	// passLate is how long after the last pass the next one counts as late: the
	// presence TTL, as the ask reader's own gap rule has it (askledger.go).
	passLate = 90 * time.Second
)

// noteGap records a span the watch did not observe and says whether one began
// since the last pass. A host sleep is wall time that the monotonic clock did not
// see (R-209). Otherwise a pass more than 90 seconds after the last, or the first
// pass of a process that loaded earlier state, is the watch's own downtime. On
// gaps the run counters reset, so no transition is inferred across the gap.
func (w *PresenceWatch) noteGap(now time.Time, mono time.Duration, ev *[]watchEvent) bool {
	if w.LastPass.IsZero() {
		return false
	}
	wall := now.Sub(w.LastPass)
	var kind string
	switch {
	case w.haveMono && wall-(mono-w.lastMono) > hostSleepSlack:
		kind = "host-sleep"
		w.Sleeps = append(w.Sleeps, watchGap{From: w.LastPass, To: now})
		if len(w.Sleeps) > maxGaps {
			w.Sleeps = w.Sleeps[len(w.Sleeps)-maxGaps:]
		}
	case !w.haveMono || wall > passLate:
		kind = "watch-down"
		w.Downs = append(w.Downs, watchGap{From: w.LastPass, To: now})
		if len(w.Downs) > maxGaps {
			w.Downs = w.Downs[len(w.Downs)-maxGaps:]
		}
	default:
		return false
	}
	*ev = append(*ev, watchEvent{At: now, Kind: kind, From: w.LastPass, To: now, Text: fmt.Sprintf("%s %s to %s", kind, w.LastPass.Format(time.RFC3339), now.Format(time.RFC3339))})
	for _, u := range w.Units {
		u.SeenRun, u.MissRun = 0, 0
		u.FirstTS, u.Beating = time.Time{}, false
	}
	return true
}

// Observe records one pass at wall time now and monotonic reading mono (the
// time since the process started, which stops while the host sleeps), and
// returns the events it produced.
func (w *PresenceWatch) Observe(now time.Time, mono time.Duration, scan watchScan) []watchEvent {
	var ev []watchEvent
	gap := w.noteGap(now, mono, &ev)
	w.LastPass, w.lastMono, w.haveMono = now, mono, true

	if scan.KeysErr {
		if !w.Blind {
			w.Blind = true
			ev = append(ev, watchEvent{At: now, Kind: "blind", Text: "watch blind: hub unreadable from this host"})
		}
		for _, u := range w.Units {
			u.Unknown = true
		}
		return ev
	}
	if w.Blind {
		w.Blind = false
		ev = append(ev, watchEvent{At: now, Kind: "sighted", Text: "hub readable again"})
	}
	if scan.SelfCheck && (!scan.SelfSeen || scan.SelfTS.Before(scan.SelfWant)) {
		// Not sighted (cashp design section 4): this pass cannot show that the hub
		// read is the hub this director writes to, so it infers nothing, as a
		// blind pass does.
		if !w.SelfUnseen {
			w.SelfUnseen = true
			ev = append(ev, watchEvent{At: now, Kind: "self-unseen", Text: "this director's own presence row is not in the hub read; no absence is inferred from it"})
		}
		for _, u := range w.Units {
			u.Unknown = true
		}
		return ev
	}
	if w.SelfUnseen {
		w.SelfUnseen = false
		ev = append(ev, watchEvent{At: now, Kind: "self-seen", Text: "this director's own row is in the hub read again"})
	}

	seen := map[string]*watchRec{}
	unread := map[string]bool{}
	for _, r := range scan.Rows {
		cl, inst, ok := parseSupervisorKey(r.Key)
		switch {
		case ok && r.Unfetched:
			unread[cl+"/"+inst] = true
		case ok && r.Rec != nil:
			seen[cl+"/"+inst] = r.Rec
		case strings.HasPrefix(r.Key, "presence.") && !isDirectorKey(r.Key):
			// Not shim-shaped, or shim-shaped with a value that is not a presence
			// record: never counts toward a unit, listed once.
			w.noteForeign(r.Key, now, &ev)
		}
	}

	names := map[string]bool{}
	for n, u := range w.Units {
		if u.Kind == "supervisor" {
			names[n] = true
		}
	}
	for n := range seen {
		names[n] = true
	}
	for n := range unread {
		names[n] = true
	}
	order := make([]string, 0, len(names))
	for n := range names {
		order = append(order, n)
	}
	sort.Strings(order)

	for _, name := range order {
		cl, inst, _ := strings.Cut(name, "/")
		u := w.Units[name]
		rec, isSeen := seen[name]
		switch {
		case unread[name] && !isSeen:
			if u != nil && !u.Unknown {
				ev = append(ev, watchEvent{At: now, Kind: "unknown", Unit: name, Text: "a presence row could not be read"})
			}
			if u != nil {
				u.Unknown = true
			}
		case isSeen:
			isNew := u == nil
			if u == nil {
				u = &watchUnit{Kind: "supervisor", Cluster: cl, Instance: inst, Status: unitPresent, Since: now}
				if c := w.Units[cl]; c != nil && c.Status != unitPresent && gap {
					// first sighted on the pass after a gap, under a cluster that was
					// absent before it: present after the gap, as an old instance
					// returning would read. No return is inferred across the gap.
					ev = append(ev, watchEvent{At: now, Kind: "present-after-gap", Unit: name})
				} else if c != nil && c.Status != unitPresent {
					u.Status = unitAbsent // a new instance under an absent cluster needs two sightings
				} else {
					ev = append(ev, watchEvent{At: now, Kind: "appeared", Unit: name})
				}
				w.Units[name] = u
			}
			w.noteBound(cl, rec.LostAfter, now, &ev)
			u.Unknown = false
			u.MissRun = 0
			u.SeenRun++
			u.LastSeen, u.LastState = now, rec.State
			if !rec.TS.IsZero() {
				if u.FirstTS.IsZero() {
					u.FirstTS = rec.TS
				} else if rec.TS.After(u.FirstTS) {
					u.Beating = true
				}
			}
			switch {
			case gap && !isNew:
				// seen on the first pass after a gap: present after it, as observed
				// at this end, and never a return.
				u.Status, u.Since = unitPresent, now
				ev = append(ev, watchEvent{At: now, Kind: "present-after-gap", Unit: name})
			case u.Status != unitPresent && u.SeenRun >= 2:
				from := u.Status
				u.Status, u.Since = unitPresent, now
				if from == unitAbsent {
					w.transition(u, name, now, &ev, "back", "")
				} else {
					ev = append(ev, watchEvent{At: now, Kind: "back", Unit: name})
				}
			}
		default: // known, not seen, not unread
			u.Unknown = false
			u.SeenRun = 0
			u.MissRun++
			u.FirstTS, u.Beating = time.Time{}, false
			switch {
			case gap && u.Status == unitAbsent:
				ev = append(ev, watchEvent{At: now, Kind: "absent-before-gap", Unit: name})
			case u.Status == unitPresent && u.MissRun >= 2:
				if asleep, until := asleepUntil(u.LastState); asleep {
					u.Status, u.Since, u.AsleepUntil = unitAsleep, u.LastSeen, until
					ev = append(ev, watchEvent{At: now, Kind: "asleep", Unit: name, Text: u.LastState})
				} else {
					u.Status, u.Since = unitAbsent, u.LastSeen
					w.transition(u, name, now, &ev, "absent", "")
				}
			case u.Status == unitAsleep && !u.AsleepUntil.IsZero() && now.After(u.AsleepUntil.Add(15*time.Minute)):
				u.Status = unitAbsent
				w.transition(u, name, now, &ev, "absent", "declared sleep ended 15 minutes ago")
			}
		}
	}
	w.deriveClusters(now, gap, &ev)
	w.hubMove(now, &ev)
	w.raiseLost(now, &ev)
	w.collapseUnderParent(&ev)
	w.capAlerts(now, &ev)
	for _, u := range w.Units {
		if u.Flapping && len(u.Transitions) > 0 && now.Sub(u.Transitions[len(u.Transitions)-1]) >= w.th.FlapQuiet {
			u.Flapping = false
		}
	}
	return ev
}

// deriveClusters keeps one unit per cluster from its supervisor instances: present
// when any instance is, else absent when any instance is, else asleep.
func (w *PresenceWatch) deriveClusters(now time.Time, gap bool, ev *[]watchEvent) {
	type agg struct{ present, absent, asleep, unknown, n int }
	by := map[string]*agg{}
	var last = map[string]time.Time{}
	for _, u := range w.Units {
		if u.Kind != "supervisor" {
			continue
		}
		a := by[u.Cluster]
		if a == nil {
			a = &agg{}
			by[u.Cluster] = a
		}
		a.n++
		switch u.Status {
		case unitPresent:
			a.present++
		case unitAbsent:
			a.absent++
		case unitAsleep:
			a.asleep++
		}
		if u.Unknown {
			a.unknown++
		}
		if u.LastSeen.After(last[u.Cluster]) {
			last[u.Cluster] = u.LastSeen
		}
	}
	clusters := make([]string, 0, len(by))
	for c := range by {
		clusters = append(clusters, c)
	}
	sort.Strings(clusters)
	for _, cl := range clusters {
		a := by[cl]
		status := unitAsleep
		switch {
		case a.present > 0:
			status = unitPresent
		case a.absent > 0:
			status = unitAbsent
		}
		u := w.Units[cl]
		if u == nil {
			u = &watchUnit{Kind: "cluster", Cluster: cl, Status: status, Since: now}
			w.Units[cl] = u
			*ev = append(*ev, watchEvent{At: now, Kind: "appeared", Unit: cl})
		}
		u.Unknown = a.unknown == a.n
		u.LastSeen = last[cl]
		if u.Status == status {
			continue
		}
		from := u.Status
		u.Status = status
		switch status {
		case unitPresent:
			u.Since = now
			if gap {
				*ev = append(*ev, watchEvent{At: now, Kind: "present-after-gap", Unit: cl})
			} else if from == unitAbsent {
				w.transition(u, cl, now, ev, "back", "")
			} else {
				*ev = append(*ev, watchEvent{At: now, Kind: "back", Unit: cl})
			}
		case unitAbsent:
			u.Since, u.AbsentAt = last[cl], now
			w.transition(u, cl, now, ev, "absent", "")
		default:
			u.Since = last[cl]
			*ev = append(*ev, watchEvent{At: now, Kind: "asleep", Unit: cl})
		}
	}
}

// Releasable reports whether mail held for the cluster may go out: one of its
// supervisor instances is present and its row is beating, that is, its ts has
// moved since the watch first saw that row (design section 4).
func (w *PresenceWatch) Releasable(cluster string) bool {
	for _, u := range w.Units {
		if u.Kind == "supervisor" && u.Cluster == cluster && u.Status == unitPresent && !u.Unknown && u.Beating {
			return true
		}
	}
	return false
}

// AbsenceDuration is how long the unit has been absent at now, less any host
// sleep inside that span (R-209). It is zero for a unit that is not absent.
func (w *PresenceWatch) AbsenceDuration(unit string, now time.Time) time.Duration {
	u := w.Units[unit]
	if u == nil || u.Status != unitAbsent {
		return 0
	}
	d := now.Sub(u.Since)
	for _, g := range w.Sleeps {
		from, to := g.From, g.To
		if from.Before(u.Since) {
			from = u.Since
		}
		if to.After(now) {
			to = now
		}
		if to.After(from) {
			d -= to.Sub(from)
		}
	}
	if d < 0 {
		d = 0
	}
	return d
}

// noteBound takes the lost_after a cluster's presence record carries (cashp
// design section 5). A value that does not parse as a positive duration is
// ignored for that cluster, with one event; one above the director's maximum is
// clamped to it, with one event. Neither fails the watch.
func (w *PresenceWatch) noteBound(cluster, raw string, now time.Time, ev *[]watchEvent) {
	if raw == "" {
		delete(w.Bounds, cluster)
		delete(w.BoundNotes, cluster)
		return
	}
	note := func(kind, text string) {
		if w.BoundNotes[cluster] == raw {
			return
		}
		if w.BoundNotes == nil {
			w.BoundNotes = map[string]string{}
		}
		w.BoundNotes[cluster] = raw
		*ev = append(*ev, watchEvent{At: now, Kind: kind, Unit: cluster, Text: text})
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		delete(w.Bounds, cluster)
		note("lost-after-ignored", fmt.Sprintf("lost_after %q is not a positive duration; the director's own bound is used", raw))
		return
	}
	if w.th.LostMax > 0 && d > w.th.LostMax {
		note("lost-after-clamped", fmt.Sprintf("lost_after %s is above the maximum %s; %s is used", raw, w.th.LostMax, w.th.LostMax))
		d = w.th.LostMax
	} else {
		delete(w.BoundNotes, cluster)
	}
	if w.Bounds == nil {
		w.Bounds = map[string]time.Duration{}
	}
	w.Bounds[cluster] = d
}

// bound is the away-to-lost bound for a cluster: its own good lost_after, else
// the director's row. Zero means no bound is set, and nothing is raised.
func (w *PresenceWatch) bound(cluster string) time.Duration {
	if d, ok := w.Bounds[cluster]; ok {
		return d
	}
	return w.th.LostAfter
}

// raiseLost raises lost, once per absence, for each cluster whose absence
// (less host sleep) has reached its bound. It runs only on a sighted pass: the
// blind and self-unseen passes return before it.
func (w *PresenceWatch) raiseLost(now time.Time, ev *[]watchEvent) {
	names := make([]string, 0, len(w.Units))
	for n, u := range w.Units {
		if u.Kind == "cluster" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		u := w.Units[n]
		if u.Status != unitAbsent {
			u.Lost = false
			continue
		}
		b := w.bound(n)
		if u.Lost || u.Unknown || b <= 0 || w.AbsenceDuration(n, now) < b {
			continue
		}
		u.Lost = true
		*ev = append(*ev, watchEvent{At: now, Kind: "lost", Unit: n, Text: b.String(), Collapsed: u.Flapping})
	}
}

// hubParent is one open hub-moved parent: the clusters it lists, which went
// absent together while no other cluster was present.
type hubParent struct {
	Opened   time.Time `json:"opened"`
	Clusters []string  `json:"clusters"`
}

// hubMove opens, closes or converts the hub-moved parent (cashp design section
// 6). It runs only on a sighted pass. At least HubMoveMin clusters going absent
// within HubMoveWindow, with no cluster left present, is a hub move and not N
// separate losses. The parent closes when every listed cluster is back. When the
// window has passed with none of the listed clusters back, it converts: the
// per-cluster lost alerts take over at their own bounds, so a real multi-cluster
// loss is not hidden behind it.
func (w *PresenceWatch) hubMove(now time.Time, ev *[]watchEvent) {
	if w.th.HubMoveMin <= 0 || w.th.HubMoveWindow <= 0 {
		return
	}
	if p := w.Parent; p != nil {
		var out []string
		for _, n := range p.Clusters {
			if u := w.Units[n]; u == nil || u.Status != unitPresent {
				out = append(out, n)
			}
		}
		switch {
		case len(out) == 0:
			*ev = append(*ev, watchEvent{At: now, Kind: "hub-moved-closed", Text: "every cluster the hub-moved parent listed is back"})
			w.Parent = nil
		case len(out) == len(p.Clusters) && now.Sub(p.Opened) >= w.th.HubMoveWindow:
			*ev = append(*ev, watchEvent{At: now, Kind: "hub-moved-converted", Text: fmt.Sprintf("none back after %s: %s; each is reported on its own lost bound", w.th.HubMoveWindow, strings.Join(out, ", "))})
			for _, n := range p.Clusters {
				if u := w.Units[n]; u != nil {
					u.Lost = false
				}
			}
			w.Parent = nil
		}
		return
	}
	var gone []string
	for n, u := range w.Units {
		if u.Kind != "cluster" {
			continue
		}
		if u.Status == unitPresent {
			return // another cluster is still here: not a hub move
		}
		if u.Status == unitAbsent && !u.AbsentAt.IsZero() && now.Sub(u.AbsentAt) < w.th.HubMoveWindow {
			gone = append(gone, n)
		}
	}
	if len(gone) < w.th.HubMoveMin {
		return
	}
	sort.Strings(gone)
	w.Parent = &hubParent{Opened: now, Clusters: gone}
	*ev = append(*ev, watchEvent{At: now, Kind: "hub-moved", Text: fmt.Sprintf("clusters %s went absent together; per-cluster absent and lost notices are held while this is open", strings.Join(gone, ", "))})
}

// collapseUnderParent marks the pass's absent and lost events collapsed while a
// parent is open: they stay in the event file and are left out of the notices.
func (w *PresenceWatch) collapseUnderParent(ev *[]watchEvent) {
	if w.Parent == nil {
		return
	}
	for i := range *ev {
		if k := (*ev)[i].Kind; k == "absent" || k == "lost" {
			(*ev)[i].Collapsed = true
		}
	}
}

// capAlerts holds back lost and hub-moved notices past AlertCap per hour: the
// event stays in the file, marked capped, and one line per hour says how many
// were held. back and every other kind are never capped.
func (w *PresenceWatch) capAlerts(now time.Time, ev *[]watchEvent) {
	if w.th.AlertCap <= 0 {
		return
	}
	cut := now.Add(-time.Hour)
	keep := w.Shown[:0]
	for _, t := range w.Shown {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	w.Shown = keep
	for i := range *ev {
		e := &(*ev)[i]
		if (e.Kind != "lost" && e.Kind != "hub-moved") || e.Collapsed {
			continue
		}
		if len(w.Shown) < w.th.AlertCap {
			w.Shown = append(w.Shown, now)
			continue
		}
		e.Capped = true
		w.CapHeld++
	}
	if w.CapHeld > 0 && (w.CapSaid.IsZero() || now.Sub(w.CapSaid) >= time.Hour) {
		*ev = append(*ev, watchEvent{At: now, Kind: "alerts-capped", Text: fmt.Sprintf("%d lost or hub-moved notice(s) recorded and not shown (cap %d an hour); see %s", w.CapHeld, w.th.AlertCap, watchEventsFile)})
		w.CapSaid, w.CapHeld = now, 0
	}
}
