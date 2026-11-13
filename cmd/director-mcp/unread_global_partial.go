package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// readUnreadGlobalPartial is the read a cluster credential can make when the leaf
// answers named reads and leaves the list verbs without a responder
// (aae-orc-e6otm, part B). It reads one stream, GLOBAL_TO_<own cluster>, and that
// cluster's presence, by name only:
//
//   - STREAM.INFO of the stream and of the presence bucket, the latter with a
//     subject filter on this cluster's presence keys, which names the instances;
//   - an exact-key get of each presence.<cluster>.supervisor.<instance>, which
//     gives the agent id the durable name needs;
//   - CONSUMER.INFO of each durable, named mcp_global_<agent>_<instance>.
//
// No key listing, watch, wildcard or write. A get or a consumer that is refused
// is named in the warnings with the leaf's error, never counted as zero. The
// report is marked Partial and gives no total. extra holds durable names an
// operator passed by --durable, which are read too, and are the way in when the
// leaf will not name the presence instances.
func readUnreadGlobalPartial(ctx context.Context, gjs jetstream.JetStream, cluster string, extra []string, now time.Time) ([]unreadReport, error) {
	name := globalStreamPrefix + cluster
	stream, err := gjs.Stream(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("hub stream %s: %w", name, err)
	}
	r := unreadReport{Stream: name, ReadAt: now.UTC().Format(time.RFC3339), Partial: true}
	keyPrefix := "presence." + cluster + ".supervisor."
	r.Warnings = append(r.Warnings, fmt.Sprintf("partial: this credential reaches the hub through a leaf that does not carry the list verbs, so %s was read by name (stream and consumer info, and an exact-key get of %s<instance>). Other clusters' streams are not read, and a cluster with no presence row is not reported; durables with no live presence are not listed; the oldest-unread age, the live state, the reply scan and any total are not read", name, keyPrefix))

	presence := map[string]unreadPresence{}
	have := map[string]bool{}
	var durables []string
	add := func(d string) {
		if !have[d] {
			have[d] = true
			durables = append(durables, d)
		}
	}
	var reads, otherReads []keyRead
	kv, err := gjs.KeyValue(ctx, globalPresenceBucket)
	if err == nil {
		var all []keyRead
		all, err = readKeysUnder(ctx, gjs, globalPresenceBucket, kv, "presence.")
		for _, kr := range all {
			switch {
			case strings.HasPrefix(kr.key, keyPrefix):
				reads = append(reads, kr)
			case !strings.HasPrefix(kr.key, "presence."+cluster+"."):
				otherReads = append(otherReads, kr)
			}
		}
	}
	if err != nil {
		r.Warnings = append(r.Warnings, fmt.Sprintf("GAP presence instances not listed: %v; pass --durable NAME for each durable to read", err))
	}
	for _, kr := range reads {
		k, e, err := kr.key, kr.entry, kr.err
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			r.Warnings = append(r.Warnings, fmt.Sprintf("unseen (not found): %s", k))
			continue
		}
		if err != nil {
			r.Warnings = append(r.Warnings, fmt.Sprintf("unseen: %s: %v", k, err))
			continue
		}
		var rec map[string]any
		agent, inst := "", ""
		if json.Unmarshal(e.Value(), &rec) == nil {
			agent, _ = rec["agent_id"].(string)
			inst, _ = rec["instance"].(string)
		}
		if agent == "" || inst == "" {
			r.Warnings = append(r.Warnings, fmt.Sprintf("unseen: %s: the presence record names no agent and instance", k))
			continue
		}
		p := unreadPresence{Key: k}
		p.State, _ = rec["state"].(string)
		p.TS, _ = rec["ts"].(string)
		presence[agent+"."+inst] = p
		add(globalDurable(agent, inst))
	}
	for _, d := range extra {
		add(d)
	}
	sort.Strings(durables)
	for _, d := range durables {
		rest, ok := strings.CutPrefix(d, "mcp_global_")
		if !ok {
			r.Warnings = append(r.Warnings, fmt.Sprintf("unseen: %s: not a hub durable name (mcp_global_<agent>_<instance>)", d))
			continue
		}
		c, err := stream.Consumer(ctx, d)
		if err != nil {
			r.Warnings = append(r.Warnings, fmt.Sprintf("unseen: durable %s: %v", d, err))
			continue
		}
		r.Durables = append(r.Durables, durableRow(c.CachedInfo(), rest, presence, false))
	}
	sortDurables(&r)
	out := []unreadReport{r}
	out = append(out, selfReportedReports(otherReads, now)...)
	return out, nil
}

// readUnreadGlobalPresenceOnly is the read when the leaf will not list streams and
// no cluster is named: no stream is read by name, and every cluster's seats come
// from their own presence rows, self-reported (I-467). It returns nothing when
// the presence rows cannot be listed or none exists, so the caller keeps the
// list-verb error rather than print an empty answer.
func readUnreadGlobalPresenceOnly(ctx context.Context, gjs jetstream.JetStream, now time.Time) []unreadReport {
	kv, err := gjs.KeyValue(ctx, globalPresenceBucket)
	if err != nil {
		return nil
	}
	reads, err := readKeysUnder(ctx, gjs, globalPresenceBucket, kv, "presence.")
	if err != nil {
		return nil
	}
	reports := selfReportedReports(reads, now)
	note := "DIRECTOR_CLUSTER is unset, so no stream was read by name; every cluster, this one included, is self-reported"
	for i := range reports {
		reports[i].Warnings = append([]string{note}, reports[i].Warnings...)
	}
	return reports
}

// selfReportedReports turns the presence rows of other clusters' seats into one
// self-reported report per cluster, the fallback for a stream this credential
// cannot read (I-467 rec 2). It makes no call: the rows were read already, each
// by an exact-key get. A cluster with no presence row has no report, since a
// report would read as a quiet cluster; the own cluster's warning says so.
func selfReportedReports(reads []keyRead, now time.Time) []unreadReport {
	byCluster := map[string][]selfReportedSeat{}
	unreadable := map[string]int{}
	for _, kr := range reads {
		parts := strings.SplitN(kr.key, ".", 4) // presence.<cluster>.<role>.<instance>
		if len(parts) < 4 {
			continue
		}
		cluster := parts[1]
		if errors.Is(kr.err, jetstream.ErrKeyNotFound) {
			continue // deleted since the listing: the row is gone, not unreadable
		}
		if kr.err != nil {
			unreadable[cluster]++
			continue
		}
		var rec map[string]any
		if json.Unmarshal(kr.entry.Value(), &rec) != nil {
			unreadable[cluster]++
			continue
		}
		agent, _ := rec["agent_id"].(string)
		inst, _ := rec["instance"].(string)
		if agent == "" || inst == "" {
			unreadable[cluster]++
			continue
		}
		seat := selfReportedSeat{AgentID: agent, Instance: inst}
		seat.PresenceTS, _ = rec["ts"].(string)
		if v, ok := rec["oldest_pending_s"]; ok {
			if age, ok := ageSeconds(v); ok {
				n := int64(age)
				seat.Reported = true
				seat.OldestPendingS = &n
				seat.OverBound = age > pendingAgeBound.Seconds()
			}
		}
		byCluster[cluster] = append(byCluster[cluster], seat)
	}
	seen := map[string]bool{}
	var clusters []string
	for _, m := range []map[string]bool{boolKeys(byCluster), boolKeysInt(unreadable)} {
		for c := range m {
			if !seen[c] {
				seen[c] = true
				clusters = append(clusters, c)
			}
		}
	}
	sort.Strings(clusters)
	var out []unreadReport
	for _, c := range clusters {
		seats := byCluster[c]
		sort.Slice(seats, func(i, j int) bool { return seats[i].AgentID < seats[j].AgentID })
		name := globalStreamPrefix + c
		r := unreadReport{
			Stream: name, ReadAt: now.UTC().Format(time.RFC3339), Partial: true, SelfReported: true, Seats: seats, UnreadableRows: unreadable[c],
			Warnings: []string{fmt.Sprintf("self-reported: %s was not read; each seat's own presence row was. oldest_pending_s covers both tiers (the oldest message waiting on either), it is an age, not a count, and it is what the seat wrote about itself. A seat with no row, or a row with no age, is not reported, never 0", name)},
		}
		if n := unreadable[c]; n > 0 {
			noun := "rows"
			if n == 1 {
				noun = "row"
			}
			r.Warnings = append(r.Warnings, fmt.Sprintf("%d presence %s unreadable for %s (the get failed, the value is not JSON, or it names no agent and instance), so %s not counted as a seat and not read as 0", n, noun, name, map[bool]string{true: "it is", false: "they are"}[n == 1]))
		}
		out = append(out, r)
	}
	return out
}

func boolKeys(m map[string][]selfReportedSeat) map[string]bool {
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}

func boolKeysInt(m map[string]int) map[string]bool {
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}

// printSelfReportedUnread prints a self-reported report: a header that says it
// is not a stream read, one line per seat, and the warning that names the limits.
func printSelfReportedUnread(w io.Writer, r unreadReport) {
	_, _ = fmt.Fprintf(w, "%s SELF-REPORTED by presence, not read from the stream, read %s\n", r.Stream, r.ReadAt)
	for _, s := range r.Seats {
		switch {
		case !s.Reported:
			_, _ = fmt.Fprintf(w, "%s  not reported (the presence row carries no age)\n", s.AgentID)
		case s.OverBound:
			_, _ = fmt.Fprintf(w, "%s  oldest pending %ds (self-reported)  OVER BOUND\n", s.AgentID, *s.OldestPendingS)
		default:
			_, _ = fmt.Fprintf(w, "%s  oldest pending %ds (self-reported)\n", s.AgentID, *s.OldestPendingS)
		}
	}
	for _, warn := range r.Warnings {
		_, _ = fmt.Fprintf(w, "WARNING: %s\n", warn)
	}
}

// printPartialUnread prints a partial report: a header that says it is partial,
// one row per durable that was read, and the warnings that name what was not.
// It gives no collapsed summary, no trailer and no total, since each would read
// as a count of the stream.
func printPartialUnread(w io.Writer, r unreadReport) {
	_, _ = fmt.Fprintf(w, "%s unread, PARTIAL read by name, read %s\n", r.Stream, r.ReadAt)
	for _, d := range r.Durables {
		printRow(w, d)
	}
	for _, warn := range r.Warnings {
		_, _ = fmt.Fprintf(w, "WARNING: %s\n", warn)
	}
}
