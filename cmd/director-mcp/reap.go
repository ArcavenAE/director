package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/nats-io/nats.go/jetstream"
)

// reapRow is the part of a presence record the reap reads. pid stays raw so
// a string, a fraction or a number wider than 32 bits is kept, not coerced.
type reapRow struct {
	AgentID  string          `json:"agent_id"`
	Instance string          `json:"instance"`
	Host     string          `json:"host"`
	Pid      json.RawMessage `json:"pid"`
}

// thisHost is the host name a shim writes beside its pid, so a later shim can
// tell a pid it may probe from one that means nothing here.
func thisHost() string {
	h, _ := os.Hostname()
	return h
}

// pidGone reports whether the process that wrote a presence row is provably
// gone on this host. Every doubt answers false, so the row stays: the pid must
// parse as a 32-bit integer above 1 (a wider number acts on its low 32 bits on
// darwin, and 0 and 1 are not seats), and only ESRCH counts. EPERM is another
// user's live process.
func pidGone(raw json.RawMessage) (int, bool) {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil || n <= 1 {
		return 0, false
	}
	return int(n), errors.Is(syscall.Kill(int(n), 0), syscall.ESRCH)
}

// reapDeadRows deletes, from one presence bucket, the rows an earlier shim of
// this same agent left behind when it died without deregistering (aae-orc-4unyk).
// A row goes only when all four hold: another instance wrote it for this agent
// id, on this host, its pid is provably gone, and the delete is conditional on
// the revision read here, so a row that beat in between survives. Anything else
// is left to the bucket TTL and to checkCollision, which still warns. It returns
// the number of rows it removed.
func (b *Bus) reapDeadRows(ctx context.Context, kv jetstream.KeyValue, prefix string, logf func(string, ...any)) int {
	keys, err := kv.Keys(ctx)
	if err != nil {
		return 0
	}
	n := 0
	for _, k := range keys {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		entry, err := kv.Get(ctx, k)
		if err != nil {
			continue
		}
		var row reapRow
		if json.Unmarshal(entry.Value(), &row) != nil {
			continue
		}
		if row.AgentID != b.self.AgentID || row.Instance == "" || row.Instance == b.instance {
			continue
		}
		if b.host == "" || row.Host != b.host {
			continue
		}
		pid, gone := pidGone(row.Pid)
		if !gone {
			continue
		}
		if err := kv.Delete(ctx, k, jetstream.LastRevision(entry.Revision())); err != nil {
			continue
		}
		n++
		if logf != nil {
			logf("reaped presence row %s: instance %s, pid %d is gone, revision %d", k, row.Instance, pid, entry.Revision())
		}
	}
	return n
}

// reapDeadPredecessors runs before this shim's first presence write, on the
// local bucket and on the global one when that tier is on, so the successor's
// first roster read never lists its own dead predecessor.
func (b *Bus) reapDeadPredecessors(ctx context.Context, logf func(string, ...any)) {
	b.reapDeadRows(ctx, b.kv, "presence."+b.self.Team+"."+b.self.AgentID+".", logf)
	if b.globalCfg == nil {
		return
	}
	g, err := b.globalReady(ctx)
	if err != nil {
		return
	}
	b.reapDeadRows(ctx, g.kv, globalPresencePrefix(g.cfg.Cluster, g.cfg.Role), logf)
}
