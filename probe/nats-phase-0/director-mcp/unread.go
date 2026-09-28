package main

// The unread reader (LR-3 slice S): for every seat durable on AGENT_INBOX,
// how much is waiting, how old the oldest waiting message is, and which shim
// revision the seat's live session runs. It is computed from the durables'
// ack floors and the stream's stored timestamps, so it touches no seat: it
// creates no consumer, acks nothing and writes no presence row.
//
// Diagnostic, not a gate: it always exits 0 once it has printed a report.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime/debug"
	"sort"
	"strings"
	"time"

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
	mode string // serve, preflight, unread
	json bool
}

func parseArgs(args []string) (cliArgs, error) {
	switch {
	case len(args) == 0:
		return cliArgs{mode: "serve"}, nil
	case len(args) == 1 && (args[0] == "--preflight" || args[0] == "-preflight"):
		return cliArgs{mode: "preflight"}, nil
	case args[0] == "unread":
		out := cliArgs{mode: "unread"}
		for _, a := range args[1:] {
			if a != "--json" {
				return cliArgs{}, fmt.Errorf("unread: unknown argument %q (usage: director-mcp unread [--json])", a)
			}
			out.json = true
		}
		return out, nil
	}
	return cliArgs{}, fmt.Errorf("unknown arguments %q (usage: director-mcp [--preflight] | director-mcp unread [--json]); refused rather than ignored, so a typo does not start a live shim (director#75)", strings.Join(args, " "))
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
}

type unreadReport struct {
	Stream   string          `json:"stream"`
	ReadAt   string          `json:"read_at"`
	Durables []unreadDurable `json:"durables"`
	Warnings []string        `json:"warnings,omitempty"`
}

// readUnread builds the report. Its only calls are reads: consumer and stream
// info, stored-message gets, and presence key reads.
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

	lister := stream.ListConsumers(ctx)
	for info := range lister.Info() {
		if !strings.HasPrefix(info.Name, "mcp_") || strings.HasPrefix(info.Name, "mcp_global_") {
			continue
		}
		d := unreadDurable{
			Durable:    info.Name,
			Pending:    info.NumPending + uint64(info.NumAckPending),
			AckPending: info.NumAckPending,
			AckFloor:   info.AckFloor.Stream,
			Filters:    info.Config.FilterSubjects,
		}
		if len(d.Filters) == 0 && info.Config.FilterSubject != "" {
			d.Filters = []string{info.Config.FilterSubject}
		}
		// mcp_<agent>_<instance>: the instance is a ULID and carries no "_",
		// so the last "_" splits it from an agent id that may.
		rest := strings.TrimPrefix(info.Name, "mcp_")
		if i := strings.LastIndex(rest, "_"); i > 0 {
			d.AgentID, d.Instance = rest[:i], rest[i+1:]
		} else {
			d.AgentID = rest
		}
		if p, ok := presence[d.AgentID+"."+d.Instance]; ok {
			pp := p
			d.Presence = &pp
			if pp.Rev == "" {
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
		r.Durables = append(r.Durables, d)
	}
	if err := lister.Err(); err != nil {
		r.Warnings = append(r.Warnings, "consumer listing incomplete: "+err.Error())
	}
	// Oldest unread first; durables with nothing waiting after, by name.
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
	return r, nil
}

func printUnread(w io.Writer, r unreadReport, asJSON bool) {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(r)
		return
	}
	_, _ = fmt.Fprintf(w, "%s unread, read %s\n", r.Stream, r.ReadAt)
	for _, d := range r.Durables {
		age := "no age"
		if d.OldestSeq != 0 {
			age = fmt.Sprintf("oldest %s (seq %d, %s)", time.Duration(d.OldestAgeSeconds*float64(time.Second)).Round(time.Second), d.OldestSeq, d.OldestAt)
		}
		pres := "no presence"
		if d.Presence != nil {
			rev := d.Presence.Rev
			if rev == "" {
				rev = "(none)"
			}
			pres = fmt.Sprintf("%s at %s rev %s", d.Presence.State, d.Presence.TS, rev)
		}
		_, _ = fmt.Fprintf(w, "%s pending %d ack_pending %d floor %d %s; %s\n", d.Durable, d.Pending, d.AckPending, d.AckFloor, age, pres)
	}
	for _, warn := range r.Warnings {
		_, _ = fmt.Fprintf(w, "WARNING: %s\n", warn)
	}
}

// runUnreadCmd connects, reads and exits. It uses dial, not connect: connect
// creates the session's durable, which is the one thing a reader must not do.
// A failure to reach the broker is reported and still exits 0 (diagnostic).
func runUnreadCmd(ctx context.Context, url string, asJSON bool, out, errw io.Writer) int {
	nc, js, err := dial(url, Sender{AgentID: "unread-reader"})
	if err != nil {
		_, _ = fmt.Fprintf(errw, "director-mcp unread: connect %s: %v\n", url, err)
		return 0
	}
	defer nc.Close()
	r, err := readUnread(ctx, js, time.Now())
	if err != nil {
		_, _ = fmt.Fprintf(errw, "director-mcp unread: %v\n", err)
		return 0
	}
	printUnread(out, r, asJSON)
	return 0
}
