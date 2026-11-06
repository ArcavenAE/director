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
	r.Warnings = append(r.Warnings, fmt.Sprintf("partial: this credential reaches the hub through a leaf that does not carry the list verbs, so %s was read by name (stream and consumer info, and an exact-key get of %s<instance>). Other clusters' streams are not read; durables with no live presence are not listed; the oldest-unread age, the live state, the reply scan and any total are not read", name, keyPrefix))

	presence := map[string]unreadPresence{}
	have := map[string]bool{}
	var durables []string
	add := func(d string) {
		if !have[d] {
			have[d] = true
			durables = append(durables, d)
		}
	}
	var keys []string
	kv, err := gjs.KeyValue(ctx, globalPresenceBucket)
	if err == nil {
		keys, err = quietKeysUnder(ctx, gjs, globalPresenceBucket, kv, keyPrefix)
	}
	if err != nil {
		r.Warnings = append(r.Warnings, fmt.Sprintf("GAP presence instances not listed: %v; pass --durable NAME for each durable to read", err))
	}
	for _, k := range keys {
		e, err := kv.Get(ctx, k)
		if errors.Is(err, jetstream.ErrKeyNotFound) {
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
	return []unreadReport{r}, nil
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
