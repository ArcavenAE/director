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
	Pidspace string          `json:"pidspace"`
	Pid      json.RawMessage `json:"pid"`
}

// pidspaceOf names the pid space a shim runs in: the host name plus a platform
// part that tells two pid spaces under one host name apart (a container that
// takes its host's name has its own pid namespace, so a live pid there reads as
// ESRCH here). Either part unreadable gives "", and an empty pidspace never
// reaps and is never reaped.
func pidspaceOf(host, part string) string {
	if host == "" || part == "" {
		return ""
	}
	return host + "|" + part
}

// thisPidspace is this shim's pid space, written beside its pid.
func thisPidspace() string {
	h, _ := os.Hostname()
	return pidspaceOf(h, platformPidspace())
}

// linuxPidNamespace reads the pid namespace link, for example "pid:[4026531836]".
// It differs per pid namespace; the boot id does not, because a container shares
// it with its host. The path is a parameter so a test can point it at a fixture.
func linuxPidNamespace(path string) string {
	t, err := os.Readlink(path)
	if err != nil {
		return ""
	}
	return t
}

// linuxPlatformPidspace is the linux platform part: the random boot id, which
// tells two hosts apart, and the pid namespace link, which tells a container
// from its host (a container shares its host's boot id). Neither alone covers
// both. Either unreadable or empty gives "", so the whole pidspace is empty.
// The paths are parameters so a test can point them at fixtures.
func linuxPlatformPidspace(bootIDPath, nsPath string) string {
	b, err := os.ReadFile(bootIDPath)
	if err != nil {
		return ""
	}
	boot := strings.TrimSpace(string(b))
	ns := linuxPidNamespace(nsPath)
	if boot == "" || ns == "" {
		return ""
	}
	return boot + "|" + ns
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
// id, in this pid space, its pid is provably gone, and the delete is conditional on
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
		if b.pidspace == "" || row.Pidspace != b.pidspace {
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
