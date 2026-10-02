package main

// The unread reader (LR-3 slice S): for every seat durable on AGENT_INBOX,
// how much is waiting, how old the oldest waiting message is, and which shim
// revision the seat's live session runs. It is computed from the durables'
// ack floors and the stream's stored timestamps, so it touches no seat: it
// creates no consumer, acks nothing and writes no presence row.
//
// Diagnostic, not a gate: it always exits 0 once it has printed a report.
//
// Slice M (sim/design/unread-slice-m.md, director#126) adds: live rows first
// with dead durables collapsed (M1), named states for a live seat whose durable
// is idle (M2), the global tier (M3), an age threshold (M4) and a role rollup
// (M5). All of it is still read-only.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// shimRevision is the binary's own vcs stamp: vcs.revision, with +dirty when
// vcs.modified is true, or "unknown" when the binary was built without VCS
// stamping. It reads the build info and never guesses.
func shimRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	return revisionFrom(info.Settings)
}

func revisionFrom(settings []debug.BuildSetting) string {
	rev, dirty := "", false
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "unknown"
	}
	if dirty {
		rev += "+dirty"
	}
	return rev
}

// cliArgs is what the command line asked for. Anything not listed here is
// refused, rather than ignored and a live shim started (director#75).
type cliArgs struct {
	mode      string // serve, preflight, unread
	json      bool
	all       bool          // unread: one row per durable, dead ones included (M1)
	global    bool          // unread: the hub streams instead of AGENT_INBOX (M3)
	byRole    bool          // unread: one line per team and role (M5)
	olderThan time.Duration // unread: mark rows older than this; 0 is unset (M4)
}

const unreadUsage = "director-mcp unread [--json] [--all] [--global] [--by-role] [--older-than <duration>]"

func parseArgs(args []string) (cliArgs, error) {
	switch {
	case len(args) == 0:
		return cliArgs{mode: "serve"}, nil
	case len(args) == 1 && (args[0] == "--preflight" || args[0] == "-preflight"):
		return cliArgs{mode: "preflight"}, nil
	case args[0] == "unread":
		out := cliArgs{mode: "unread"}
		rest := args[1:]
		for i := 0; i < len(rest); i++ {
			a := rest[i]
			switch {
			case a == "--json":
				out.json = true
			case a == "--all":
				out.all = true
			case a == "--global":
				out.global = true
			case a == "--by-role":
				out.byRole = true
			case a == "--older-than" || strings.HasPrefix(a, "--older-than="):
				v, ok := strings.CutPrefix(a, "--older-than=")
				if !ok {
					if i+1 >= len(rest) {
						return cliArgs{}, fmt.Errorf("unread: --older-than needs a duration such as 1h (usage: %s)", unreadUsage)
					}
					i++
					v = rest[i]
				}
				d, err := time.ParseDuration(v)
				if err != nil || d <= 0 {
					return cliArgs{}, fmt.Errorf("unread: --older-than %q is not a positive duration such as 1h (usage: %s)", v, unreadUsage)
				}
				out.olderThan = d
			default:
				return cliArgs{}, fmt.Errorf("unread: unknown argument %q (usage: %s)", a, unreadUsage)
			}
		}
		return out, nil
	}
	return cliArgs{}, fmt.Errorf("unknown arguments %q (usage: director-mcp [--preflight] | %s); refused rather than ignored, so a typo does not start a live shim (director#75)", strings.Join(args, " "), unreadUsage)
}

type unreadPresence struct {
	Key   string `json:"key"`
	State string `json:"state"`
	TS    string `json:"ts"`
	Rev   string `json:"rev"`
}

type unreadDurable struct {
	Durable          string          `json:"durable"`
	AgentID          string          `json:"agent_id"`
	Instance         string          `json:"instance"`
	Filters          []string        `json:"filters"`
	Pending          uint64          `json:"pending"`
	AckPending       int             `json:"ack_pending"`
	AckFloor         uint64          `json:"ack_floor"`
	OldestSeq        uint64          `json:"oldest_unread_seq,omitempty"`
	OldestAt         string          `json:"oldest_unread_at,omitempty"`
	OldestAgeSeconds float64         `json:"oldest_unread_age_seconds,omitempty"`
	Presence         *unreadPresence `json:"presence"`
	Note             string          `json:"note,omitempty"`

	// Slice M. Live is presence present. State is set on live rows only:
	// reading, behind, durable-idle or reads-outside-durable (M2).
	Live          bool            `json:"live"`
	State         string          `json:"state,omitempty"`
	Created       string          `json:"created,omitempty"`
	Delivered     uint64          `json:"delivered"`
	Team          string          `json:"team,omitempty"`
	Role          string          `json:"role,omitempty"`
	Evidence      *unreadEvidence `json:"evidence,omitempty"`
	ScanTruncated bool            `json:"scan_truncated,omitempty"`
	OverThreshold bool            `json:"over_threshold,omitempty"`

	created    time.Time
	pendingIDs map[string]bool
}

// unreadEvidence is what reclassed a durable-idle row: the pending message ids
// this session answered under its own sender.instance, and its latest such send.
type unreadEvidence struct {
	InReplyTo  []string `json:"in_reply_to"`
	LatestSent string   `json:"latest_sent"`
	// Basis says what the evidence rests on: the sender wrote its own
	// sender.instance, and nothing verified it (design section 3).
	Basis string `json:"basis"`
}

// evidenceBasis is the Basis of every reads-outside-durable row. Any process
// holding the agent's bus credentials can write any sender.instance, so the
// match is the sender's own claim, not proof.
const evidenceBasis = "sender.instance is self-asserted by the sender, not verified"

// unreadRole is one line of the role rollup (M5).
type unreadRole struct {
	Team             string         `json:"team"`
	Role             string         `json:"role"`
	Holders          int            `json:"holders"`
	Pending          uint64         `json:"pending"`
	OldestAgeSeconds float64        `json:"oldest_unread_age_seconds,omitempty"`
	States           map[string]int `json:"states"`
}

type unreadReport struct {
	Stream             string          `json:"stream"`
	ReadAt             string          `json:"read_at"`
	Durables           []unreadDurable `json:"durables"`
	Warnings           []string        `json:"warnings,omitempty"`
	OlderThan          string          `json:"older_than,omitempty"`
	OverThresholdCount int             `json:"over_threshold_count,omitempty"`
	Roles              []unreadRole    `json:"roles,omitempty"`
}

// The live states (M2).
const (
	stateReading      = "reading"
	stateBehind       = "behind"
	stateDurableIdle  = "durable-idle"
	stateReadsOutside = "reads-outside-durable"
)

// unreadIdleWindow is how long a live session's durable may exist, from its
// Created, delivering nothing with mail waiting, before it is named
// durable-idle (ruling 3, default 10m). unreadScanCap bounds the reply scan:
// at most this many messages per stream per run, newest first. Variables so
// tests can shorten them.
var (
	unreadIdleWindow = 10 * time.Minute
	unreadScanCap    = 2000
)

// readUnread builds the local report. Its only calls are reads: consumer and
// stream info, stored-message gets, and presence key reads.
func readUnread(ctx context.Context, js jetstream.JetStream, now time.Time) (unreadReport, error) {
	r := unreadReport{Stream: "AGENT_INBOX", ReadAt: now.UTC().Format(time.RFC3339)}
	stream, err := js.Stream(ctx, "AGENT_INBOX")
	if err != nil {
		return r, fmt.Errorf("stream AGENT_INBOX: %w", err)
	}
	presence := map[string]unreadPresence{} // by agent id + "." + instance
	if kv, err := js.KeyValue(ctx, "AGENT_STATE"); err != nil {
		r.Warnings = append(r.Warnings, "presence bucket AGENT_STATE unreadable: "+err.Error()+"; presence below is NOT established")
	} else {
		keys, err := kv.Keys(ctx)
		if err != nil && !errors.Is(err, jetstream.ErrNoKeysFound) {
			r.Warnings = append(r.Warnings, "presence keys unreadable: "+err.Error()+"; presence below is NOT established")
		}
		for _, k := range keys {
			parts := strings.Split(k, ".") // presence.<team>.<id>.<instance>
			if len(parts) != 4 || parts[0] != "presence" {
				continue
			}
			p := unreadPresence{Key: k}
			if e, err := kv.Get(ctx, k); err == nil {
				var rec map[string]any
				if json.Unmarshal(e.Value(), &rec) == nil {
					p.State, _ = rec["state"].(string)
					p.TS, _ = rec["ts"].(string)
					p.Rev, _ = rec["rev"].(string)
				}
			}
			presence[parts[2]+"."+parts[3]] = p
		}
	}
	local := func(name string) (string, bool) {
		if !strings.HasPrefix(name, "mcp_") || strings.HasPrefix(name, "mcp_global_") {
			return "", false
		}
		return strings.TrimPrefix(name, "mcp_"), true
	}
	readDurables(ctx, stream, local, presence, true, now, &r)
	classify(ctx, []jetstream.Stream{stream}, now, &r)
	sortDurables(&r)
	return r, nil
}

// readDurables appends one row per durable that name accepts, with its pending
// count, ack floor, oldest unread message and presence. name returns the
// <agent>_<instance> part of an accepted durable name. withRev says whether
// presence on this tier carries a rev at all (the hub's does not).
func readDurables(ctx context.Context, stream jetstream.Stream, name func(string) (string, bool), presence map[string]unreadPresence, withRev bool, now time.Time, r *unreadReport) {
	lister := stream.ListConsumers(ctx)
	for info := range lister.Info() {
		rest, ok := name(info.Name)
		if !ok {
			continue
		}
		d := unreadDurable{
			Durable:    info.Name,
			Pending:    info.NumPending + uint64(info.NumAckPending),
			AckPending: info.NumAckPending,
			AckFloor:   info.AckFloor.Stream,
			Filters:    info.Config.FilterSubjects,
			Delivered:  info.Delivered.Consumer,
			created:    info.Created,
		}
		if !info.Created.IsZero() {
			d.Created = info.Created.UTC().Format(time.RFC3339)
		}
		if len(d.Filters) == 0 && info.Config.FilterSubject != "" {
			d.Filters = []string{info.Config.FilterSubject}
		}
		// <agent>_<instance>: the instance is a ULID and carries no "_",
		// so the last "_" splits it from an agent id that may.
		if i := strings.LastIndex(rest, "_"); i > 0 {
			d.AgentID, d.Instance = rest[:i], rest[i+1:]
		} else {
			d.AgentID = rest
		}
		d.Team, d.Role = teamAndRole(d.AgentID, d.Filters)
		if p, ok := presence[d.AgentID+"."+d.Instance]; ok {
			pp := p
			d.Presence = &pp
			d.Live = true
			if withRev && pp.Rev == "" {
				d.Note = "presence carries no rev: the seat runs a shim older than LR-3"
			}
		} else {
			d.Note = "no presence: this durable belongs to a session that is not live"
		}
		if d.Pending > 0 {
			// The first message the durable has not acked: after its ack floor,
			// and never before where a resumed durable started.
			start := d.AckFloor + 1
			if info.Config.OptStartSeq > start {
				start = info.Config.OptStartSeq
			}
			for _, subj := range d.Filters {
				m, err := stream.GetMsg(ctx, start, jetstream.WithGetMsgSubject(subj))
				if err != nil {
					if !errors.Is(err, jetstream.ErrMsgNotFound) {
						r.Warnings = append(r.Warnings, fmt.Sprintf("%s: oldest unread on %s unreadable: %v", d.Durable, subj, err))
					}
					continue
				}
				if d.OldestSeq == 0 || m.Sequence < d.OldestSeq {
					d.OldestSeq = m.Sequence
					d.OldestAt = m.Time.UTC().Format(time.RFC3339)
					d.OldestAgeSeconds = now.Sub(m.Time).Seconds()
				}
			}
		}
		if d.Live {
			d.State = liveState(d, now)
			if d.State == stateDurableIdle {
				d.pendingIDs = pendingIDs(ctx, stream, d, info.Config.OptStartSeq, r)
			}
		}
		r.Durables = append(r.Durables, d)
	}
	if err := lister.Err(); err != nil {
		r.Warnings = append(r.Warnings, "consumer listing incomplete: "+err.Error())
	}
}

// liveState names a live row (M2). The anchor is the durable's Created: the
// per-session durable is made at shim start, while presence is renewed on a
// timer and says nothing about whether the seat reads (R-56). With mail
// waiting and nothing delivered since a Created older than the window, the
// row is durable-idle, the alarming default; only evidence lifts it (classify).
func liveState(d unreadDurable, now time.Time) string {
	switch {
	case d.Pending == 0:
		return stateReading
	case d.Delivered == 0 && !d.created.IsZero() && now.Sub(d.created) >= unreadIdleWindow:
		return stateDurableIdle
	default:
		return stateBehind
	}
}

// pendingIDs walks the durable's filter subjects from its first unacked
// sequence to the end of the stream and returns the message ids found. The set
// comes from the floor, not from Created, so a resumed durable's older pending
// mail is in it.
func pendingIDs(ctx context.Context, stream jetstream.Stream, d unreadDurable, optStart uint64, r *unreadReport) map[string]bool {
	ids := map[string]bool{}
	start := d.AckFloor + 1
	if optStart > start {
		start = optStart
	}
	for _, subj := range d.Filters {
		seq := start
		for n := uint64(0); n <= d.Pending; n++ {
			m, err := stream.GetMsg(ctx, seq, jetstream.WithGetMsgSubject(subj))
			if err != nil {
				if !errors.Is(err, jetstream.ErrMsgNotFound) {
					r.Warnings = append(r.Warnings, fmt.Sprintf("%s: pending set on %s unreadable: %v", d.Durable, subj, err))
				}
				break
			}
			var e Envelope
			if json.Unmarshal(m.Data, &e) == nil && e.MessageID != "" {
				ids[e.MessageID] = true
			}
			seq = m.Sequence + 1
		}
	}
	return ids
}

// sentReply is one envelope a session sent that answers another message.
type sentReply struct {
	inReplyTo string
	at        time.Time
}

// replyScan is one capped, newest-first pass over a stream: the replies found,
// keyed by sender.instance, how far back it reached, and whether the cap cut it
// short.
type replyScan struct {
	byInstance map[string][]sentReply
	oldest     time.Time
	truncated  bool
}

// scanReplies reads a stream newest first, by sequence, until a message older
// than since, the start of the stream, or the cap. Read-only: GetMsg by
// sequence. A message with no sender.instance is never kept, so a sender that
// names only the agent (a shim before part E1, a sibling, a send-only process)
// can never stand as evidence for a session. sender.session is the harness
// session UUID, a different identifier, and is never read here (director#196).
func scanReplies(ctx context.Context, stream jetstream.Stream, since time.Time) (replyScan, error) {
	out := replyScan{byInstance: map[string][]sentReply{}}
	info, err := stream.Info(ctx)
	if err != nil {
		return out, err
	}
	first, last := info.State.FirstSeq, info.State.LastSeq
	n := 0
	for seq := last; seq >= first && seq > 0; seq-- {
		if n >= unreadScanCap {
			out.truncated = true
			break
		}
		n++
		m, err := stream.GetMsg(ctx, seq)
		if err != nil {
			if errors.Is(err, jetstream.ErrMsgNotFound) {
				continue // deleted or expired: a gap, not an end
			}
			return out, err
		}
		if m.Time.Before(since) {
			break
		}
		out.oldest = m.Time
		var e Envelope
		if json.Unmarshal(m.Data, &e) != nil || e.Sender.Instance == "" || e.InReplyTo == "" {
			continue
		}
		out.byInstance[e.Sender.Instance] = append(out.byInstance[e.Sender.Instance], sentReply{inReplyTo: e.InReplyTo, at: m.Time})
	}
	return out, nil
}

// classify reclasses a durable-idle row as reads-outside-durable when this
// session, since its durable's Created, sent a reply naming a message still
// pending on that durable (M2). The streams are scanned once per run, back to
// the earliest Created among the idle rows. A row whose window the capped scan
// did not reach stays durable-idle and says scan_truncated.
func classify(ctx context.Context, streams []jetstream.Stream, now time.Time, r *unreadReport) {
	var since time.Time
	for _, d := range r.Durables {
		if d.State == stateDurableIdle && (since.IsZero() || d.created.Before(since)) {
			since = d.created
		}
	}
	if since.IsZero() {
		return
	}
	var scans []replyScan
	for _, s := range streams {
		sc, err := scanReplies(ctx, s, since)
		if err != nil {
			r.Warnings = append(r.Warnings, fmt.Sprintf("reply scan of %s incomplete: %v; no idle row is reclassed from it", s.CachedInfo().Config.Name, err))
			sc.truncated = true
		}
		scans = append(scans, sc)
	}
	for i := range r.Durables {
		d := &r.Durables[i]
		if d.State != stateDurableIdle {
			continue
		}
		var matched []string
		var latest time.Time
		truncated := false
		for _, sc := range scans {
			if sc.truncated && (sc.oldest.IsZero() || !sc.oldest.Before(d.created)) {
				truncated = true
			}
			for _, rep := range sc.byInstance[d.Instance] {
				if rep.at.Before(d.created) || !d.pendingIDs[rep.inReplyTo] {
					continue
				}
				matched = append(matched, rep.inReplyTo)
				if rep.at.After(latest) {
					latest = rep.at
				}
			}
		}
		if truncated {
			d.ScanTruncated = true
			continue
		}
		if len(matched) == 0 {
			continue
		}
		sort.Strings(matched)
		matched = dedupe(matched)
		d.State = stateReadsOutside
		d.Evidence = &unreadEvidence{InReplyTo: matched, LatestSent: latest.UTC().Format(time.RFC3339), Basis: evidenceBasis}
	}
}

func dedupe(sorted []string) []string {
	out := sorted[:0]
	for i, s := range sorted {
		if i == 0 || s != sorted[i-1] {
			out = append(out, s)
		}
	}
	return out
}

// instanceSuffix is the replica suffix a cast adds to an agent id (-g5-2).
var instanceSuffix = regexp.MustCompile(`-g\d+-\d+$`)

// teamAndRole reads a row's team and role from its filter subjects: the team
// from agent.<ws>.<team>.<id>.inbox (or global.<cluster>.<role>.inbox, where
// the cluster stands in for the team), the role from a role.<role>.inbox
// filter. A durable with no role filter takes its role from the agent id, the
// replica suffix and a leading "<team>-" removed (M5).
func teamAndRole(agent string, filters []string) (team, role string) {
	for _, f := range filters {
		p := strings.Split(f, ".")
		switch {
		case len(p) == 6 && p[0] == "agent" && p[3] == "role":
			team, role = p[2], p[4]
		case len(p) == 5 && p[0] == "agent" && team == "":
			team = p[2]
		case len(p) == 4 && p[0] == "global":
			team, role = p[1], p[2]
		case len(p) == 3 && p[0] == "global" && p[1] == roleDirector:
			team, role = "global", roleDirector
		}
	}
	if role == "" {
		role = strings.TrimPrefix(instanceSuffix.ReplaceAllString(agent, ""), team+"-")
	}
	return team, role
}

// sortDurables puts oldest unread first; durables with nothing waiting after,
// by name.
func sortDurables(r *unreadReport) {
	sort.SliceStable(r.Durables, func(i, j int) bool {
		a, b := r.Durables[i], r.Durables[j]
		if (a.OldestSeq != 0) != (b.OldestSeq != 0) {
			return a.OldestSeq != 0
		}
		if a.OldestSeq != b.OldestSeq {
			return a.OldestSeq < b.OldestSeq
		}
		return a.Durable < b.Durable
	})
}

// applyThreshold marks each behind or durable-idle row whose oldest unread
// message is older than d (M4). reads-outside-durable rows are never counted.
// It changes the report only; the exit code stays 0 (ADR-007).
func applyThreshold(r *unreadReport, d time.Duration) {
	if d <= 0 {
		return
	}
	r.OlderThan = d.String()
	r.OverThresholdCount = 0
	for i := range r.Durables {
		row := &r.Durables[i]
		row.OverThreshold = (row.State == stateBehind || row.State == stateDurableIdle) &&
			row.OldestSeq != 0 && row.OldestAgeSeconds > d.Seconds()
		if row.OverThreshold {
			r.OverThresholdCount++
		}
	}
}

// rollupRoles groups live rows by team and role (M5). The oldest age counts
// holders in behind or durable-idle only: a role reads its mail if anyone in
// it does, and the reader does not judge which instance should have.
func rollupRoles(r *unreadReport) {
	byKey := map[string]*unreadRole{}
	var order []string
	for _, d := range r.Durables {
		if !d.Live {
			continue
		}
		k := d.Team + "/" + d.Role
		role, ok := byKey[k]
		if !ok {
			role = &unreadRole{Team: d.Team, Role: d.Role, States: map[string]int{}}
			byKey[k] = role
			order = append(order, k)
		}
		role.Holders++
		role.Pending += d.Pending
		role.States[d.State]++
		if (d.State == stateBehind || d.State == stateDurableIdle) && d.OldestAgeSeconds > role.OldestAgeSeconds {
			role.OldestAgeSeconds = d.OldestAgeSeconds
		}
	}
	sort.Strings(order)
	r.Roles = nil
	for _, k := range order {
		r.Roles = append(r.Roles, *byKey[k])
	}
}

// unreadOpts is how a report is printed.
type unreadOpts struct {
	JSON   bool
	All    bool
	ByRole bool
}

func ageText(seconds float64) string {
	return time.Duration(seconds * float64(time.Second)).Round(time.Second).String()
}

func printUnread(w io.Writer, r unreadReport, o unreadOpts) {
	if o.JSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(r)
		return
	}
	_, _ = fmt.Fprintf(w, "%s unread, read %s\n", r.Stream, r.ReadAt)
	switch {
	case o.ByRole:
		for _, role := range r.Roles {
			var states []string
			for _, s := range []string{stateReading, stateBehind, stateDurableIdle, stateReadsOutside} {
				if n := role.States[s]; n > 0 {
					states = append(states, fmt.Sprintf("%s %d", s, n))
				}
			}
			oldest := "no age"
			if role.OldestAgeSeconds > 0 {
				oldest = "oldest " + ageText(role.OldestAgeSeconds)
			}
			_, _ = fmt.Fprintf(w, "%s/%s  holders %d  pending %d  %s  (%s)\n", role.Team, role.Role, role.Holders, role.Pending, oldest, strings.Join(states, ", "))
		}
	case o.All:
		for _, d := range r.Durables {
			printRow(w, d)
		}
	default:
		printCollapsed(w, r)
	}
	printTrailer(w, r)
	for _, warn := range r.Warnings {
		_, _ = fmt.Fprintf(w, "WARNING: %s\n", warn)
	}
}

// printCollapsed is the default view (M1): per address, its live rows, then
// one summary line for its dead durables. Nothing is hidden: the count and
// pending total stay on the summary, and --all or --json list every row.
func printCollapsed(w io.Writer, r unreadReport) {
	type group struct {
		live           []unreadDurable
		dead           int
		deadPending    uint64
		deadOldestSecs float64
	}
	groups := map[string]*group{}
	var order []string
	for _, d := range r.Durables {
		g, ok := groups[d.AgentID]
		if !ok {
			g = &group{}
			groups[d.AgentID] = g
			order = append(order, d.AgentID)
		}
		if d.Live {
			g.live = append(g.live, d)
			continue
		}
		g.dead++
		g.deadPending += d.Pending
		if d.OldestAgeSeconds > g.deadOldestSecs {
			g.deadOldestSecs = d.OldestAgeSeconds
		}
	}
	for _, a := range order {
		g := groups[a]
		if len(g.live) == 0 {
			_, _ = fmt.Fprintf(w, "%s  no live session\n", a)
		}
		for _, d := range g.live {
			printRow(w, d)
		}
		if g.dead > 0 {
			oldest := ""
			if g.deadOldestSecs > 0 {
				oldest = ", oldest " + ageText(g.deadOldestSecs)
			}
			_, _ = fmt.Fprintf(w, "  dead durables: %d, pending %d in total%s (--all to list)\n", g.dead, g.deadPending, oldest)
		}
	}
}

func printRow(w io.Writer, d unreadDurable) {
	mark := ""
	if d.OverThreshold {
		mark = "! "
	}
	state := d.State
	if !d.Live {
		state = "not live"
	}
	age := "no age"
	if d.OldestSeq != 0 {
		age = fmt.Sprintf("oldest %s (seq %d, %s)", ageText(d.OldestAgeSeconds), d.OldestSeq, d.OldestAt)
	}
	rev := ""
	if d.Presence != nil {
		r := d.Presence.Rev
		if r == "" {
			r = "(none)"
		}
		rev = fmt.Sprintf("  %s at %s rev %s", d.Presence.State, d.Presence.TS, r)
	}
	switch d.State {
	case stateReadsOutside:
		latest := d.Evidence.LatestSent
		_, _ = fmt.Fprintf(w, "%s%s  %s  pending %d (answered %d of them, latest %s; not unread mail; instance self-asserted, not verified)%s\n", mark, d.Durable, state, d.Pending, len(d.Evidence.InReplyTo), latest, rev)
	case stateDurableIdle:
		trunc := ""
		if d.ScanTruncated {
			trunc = "; reply scan truncated"
		}
		_, _ = fmt.Fprintf(w, "%s%s  %s  pending %d  %s  (this seat reads elsewhere, or not at all%s)%s\n", mark, d.Durable, state, d.Pending, age, trunc, rev)
	default:
		_, _ = fmt.Fprintf(w, "%s%s  %s  pending %d ack_pending %d floor %d  %s%s\n", mark, d.Durable, state, d.Pending, d.AckPending, d.AckFloor, age, rev)
	}
}

// printTrailer lists the two idle classes under their own headings, and the
// threshold count when one was asked for (M2, M4).
func printTrailer(w io.Writer, r unreadReport) {
	var idle, outside []string
	behind, idleOver := 0, 0
	for _, d := range r.Durables {
		switch d.State {
		case stateDurableIdle:
			idle = append(idle, d.Durable)
			if d.OverThreshold {
				idleOver++
			}
		case stateReadsOutside:
			outside = append(outside, fmt.Sprintf("%s (answered %s)", d.Durable, strings.Join(d.Evidence.InReplyTo, " ")))
		case stateBehind:
			if d.OverThreshold {
				behind++
			}
		}
	}
	if len(idle) > 0 {
		_, _ = fmt.Fprintf(w, "durable-idle, reads elsewhere or not at all: %s\n", strings.Join(idle, ", "))
	}
	if len(outside) > 0 {
		_, _ = fmt.Fprintf(w, "reads-outside-durable, not unread mail (instance self-asserted, not verified): %s\n", strings.Join(outside, ", "))
	}
	if r.OlderThan != "" {
		_, _ = fmt.Fprintf(w, "over threshold %s: %d (behind %d, durable-idle %d)\n", r.OlderThan, r.OverThresholdCount, behind, idleOver)
	}
}

// readUnreadGlobal builds one report per hub stream (GLOBAL_TO_*), through the
// hub's JetStream domain over the leaf, with presence from GLOBAL_PRESENCE
// (M3). It reuses the local rules unchanged; the reply scan covers every hub
// stream, since a supervisor's answer to director lands on GLOBAL_TO_DIRECTOR,
// not on the stream its own durable reads.
func readUnreadGlobal(ctx context.Context, nc *nats.Conn, domain string, now time.Time) ([]unreadReport, error) {
	gjs, err := jetstream.NewWithDomain(nc, domain)
	if err != nil {
		return nil, fmt.Errorf("global JetStream domain %q: %w", domain, err)
	}
	var names []string
	lister := gjs.StreamNames(ctx)
	for n := range lister.Name() {
		if strings.HasPrefix(n, globalStreamPrefix) {
			names = append(names, n)
		}
	}
	if err := lister.Err(); err != nil {
		return nil, fmt.Errorf("hub in domain %q not reachable: %w", domain, err)
	}
	sort.Strings(names)
	presence := map[string]unreadPresence{}
	var presenceWarn string
	if kv, err := gjs.KeyValue(ctx, globalPresenceBucket); err != nil {
		presenceWarn = "presence bucket " + globalPresenceBucket + " unreadable: " + err.Error() + "; presence below is NOT established"
	} else {
		keys, err := kv.Keys(ctx)
		if err != nil && !errors.Is(err, jetstream.ErrNoKeysFound) {
			presenceWarn = "presence keys unreadable: " + err.Error() + "; presence below is NOT established"
		}
		for _, k := range keys {
			e, err := kv.Get(ctx, k)
			if err != nil {
				continue
			}
			var rec map[string]any
			if json.Unmarshal(e.Value(), &rec) != nil {
				continue
			}
			agent, _ := rec["agent_id"].(string)
			inst, _ := rec["instance"].(string)
			if agent == "" || inst == "" {
				continue
			}
			p := unreadPresence{Key: k}
			p.State, _ = rec["state"].(string)
			p.TS, _ = rec["ts"].(string)
			presence[agent+"."+inst] = p
		}
	}
	var streams []jetstream.Stream
	for _, n := range names {
		s, err := gjs.Stream(ctx, n)
		if err != nil {
			return nil, fmt.Errorf("hub stream %s: %w", n, err)
		}
		streams = append(streams, s)
	}
	hub := func(name string) (string, bool) {
		return strings.CutPrefix(name, "mcp_global_")
	}
	var out []unreadReport
	for i, s := range streams {
		r := unreadReport{Stream: names[i], ReadAt: now.UTC().Format(time.RFC3339)}
		if presenceWarn != "" {
			r.Warnings = append(r.Warnings, presenceWarn)
		}
		readDurables(ctx, s, hub, presence, false, now, &r)
		classify(ctx, streams, now, &r)
		sortDurables(&r)
		out = append(out, r)
	}
	return out, nil
}

// globalUnread is the --global --json shape: one report per hub stream, or the
// reason the hub was not read.
type globalUnread struct {
	Domain  string         `json:"domain"`
	Reports []unreadReport `json:"reports"`
	Error   string         `json:"error,omitempty"`
}

// runUnreadCmd connects, reads and exits. It uses dial, not connect: connect
// creates the session's durable, which is the one thing a reader must not do.
// A failure to reach the broker or the hub is reported and still exits 0
// (diagnostic). domain is the hub's JetStream domain for --global.
func runUnreadCmd(ctx context.Context, url string, cli cliArgs, domain string, out, errw io.Writer) int {
	nc, js, err := dial(url, Sender{AgentID: "unread-reader"})
	if err != nil {
		_, _ = fmt.Fprintf(errw, "director-mcp unread: connect %s: %v\n", url, err)
		return 0
	}
	defer nc.Close()
	opts := unreadOpts{JSON: cli.json, All: cli.all, ByRole: cli.byRole}
	finish := func(r *unreadReport) {
		applyThreshold(r, cli.olderThan)
		if cli.byRole {
			rollupRoles(r)
		}
	}
	if cli.global {
		g := globalUnread{Domain: domain}
		reports, err := readUnreadGlobal(ctx, nc, domain, time.Now())
		if err != nil {
			g.Error = err.Error()
		}
		for i := range reports {
			finish(&reports[i])
		}
		g.Reports = reports
		if cli.json {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			_ = enc.Encode(g)
			return 0
		}
		if g.Error != "" {
			_, _ = fmt.Fprintf(out, "global unread not read: %s\n", g.Error)
			return 0
		}
		for _, r := range reports {
			printUnread(out, r, opts)
		}
		return 0
	}
	r, err := readUnread(ctx, js, time.Now())
	if err != nil {
		_, _ = fmt.Fprintf(errw, "director-mcp unread: %v\n", err)
		return 0
	}
	finish(&r)
	printUnread(out, r, opts)
	return 0
}
