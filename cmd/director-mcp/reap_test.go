package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// reapedChildPid returns the pid of a child that has run and been waited for.
func reapedChildPid(t *testing.T) int {
	t.Helper()
	c := exec.Command("true")
	if err := c.Run(); err != nil {
		t.Fatal(err)
	}
	return c.Process.Pid
}

// liveChildPid returns the pid of a running child, killed at cleanup by pid.
func liveChildPid(t *testing.T) int {
	t.Helper()
	c := exec.Command("sleep", "60")
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Process.Kill(); _, _ = c.Process.Wait() })
	return c.Process.Pid
}

func TestPidGone(t *testing.T) {
	dead, live := reapedChildPid(t), liveChildPid(t)
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"reaped child", fmt.Sprint(dead), true},
		{"running child", fmt.Sprint(live), false},
		{"zero", "0", false},
		{"one", "1", false},
		{"negative", "-5", false},
		{"above 32 bits, low bits a live pid", fmt.Sprint(int64(live) + 1<<32), false},
		{"above 32 bits, low bits a dead pid", fmt.Sprint(int64(dead) + 1<<32), false},
		{"fraction", "12.5", false},
		{"text", `"abc"`, false},
		{"empty", ``, false},
	}
	for _, c := range cases {
		if _, got := pidGone([]byte(c.raw)); got != c.want {
			t.Errorf("%s: pidGone(%s) = %v, want %v", c.name, c.raw, got, c.want)
		}
	}
}

// Another user's live process answers EPERM, which must read as alive. Pid 1 is
// rejected earlier, so this probes the syscall directly on the darwin/linux
// behavior the design rests on.
func TestKillProbeAnswers(t *testing.T) {
	if err := syscall.Kill(reapedChildPid(t), 0); err != syscall.ESRCH {
		t.Fatalf("kill(reaped child, 0) = %v, want ESRCH", err)
	}
	if err := syscall.Kill(1, 0); err != nil && err != syscall.EPERM {
		t.Fatalf("kill(1, 0) = %v, want nil or EPERM", err)
	}
}

type reapRig struct {
	ctx  context.Context
	url  string
	self Sender
	kv   jetstream.KeyValue
}

func newReapRig(t *testing.T) *reapRig {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	kv, err := js.KeyValue(ctx, "AGENT_STATE")
	if err != nil {
		t.Fatal(err)
	}
	return &reapRig{ctx: ctx, url: url, self: Sender{AgentID: "arcaven-supervisor-g58-0", Workspace: "aae", Team: "arcaven"}, kv: kv}
}

func (r *reapRig) bus(t *testing.T, gcfg *globalConfig) *Bus {
	t.Helper()
	b, err := connect(r.ctx, r.url, r.self, gcfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.close)
	return b
}

func (r *reapRig) seed(t *testing.T, kv jetstream.KeyValue, key string, rec map[string]any) {
	t.Helper()
	body, _ := json.Marshal(rec)
	if _, err := kv.Put(r.ctx, key, body); err != nil {
		t.Fatal(err)
	}
}

func (r *reapRig) has(t *testing.T, kv jetstream.KeyValue, key string) bool {
	t.Helper()
	_, err := kv.Get(r.ctx, key)
	return err == nil
}

func localKey(self Sender, inst string) string {
	return "presence." + self.Team + "." + self.AgentID + "." + inst
}

func seedRec(self Sender, inst, pidspace string, pid any) map[string]any {
	rec := map[string]any{"agent_id": self.AgentID, "team": self.Team, "instance": inst, "state": "idle"}
	if pidspace != "" {
		rec["pidspace"] = pidspace
	}
	if pid != nil {
		rec["pid"] = pid
	}
	return rec
}

// The ticket's sequence: shim A writes presence and dies without deregistering,
// then shim B starts under the same agent id. After B's start the roster holds
// one row for the seat, and it is B's.
func TestReapRemovesCrashedPredecessorRow(t *testing.T) {
	r := newReapRig(t)
	a := r.bus(t, nil)
	a.pid = reapedChildPid(t)
	if _, err := a.setPresence(r.ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	a.close()

	b := r.bus(t, nil)
	var logs []string
	b.reapDeadPredecessors(r.ctx, func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) })
	if _, err := b.setPresence(r.ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	rows, err := b.roster(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["instance"] != b.instance {
		t.Fatalf("roster = %v, want one row, instance %s", rows, b.instance)
	}
	if len(logs) != 1 || !strings.Contains(logs[0], a.instance) || !strings.Contains(logs[0], fmt.Sprint(a.pid)) {
		t.Fatalf("reap log = %q, want one line naming instance %s and pid %d", logs, a.instance, a.pid)
	}
}

func TestReapKeepsLiveRivalAndCollisionStillWarns(t *testing.T) {
	r := newReapRig(t)
	a := r.bus(t, nil)
	a.pid = liveChildPid(t)
	if _, err := a.setPresence(r.ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	b := r.bus(t, nil)
	b.reapDeadPredecessors(r.ctx, nil)
	if !r.has(t, r.kv, localKey(r.self, a.instance)) {
		t.Fatal("a row whose pid is alive was reaped")
	}
	if _, err := b.setPresence(r.ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	if b.checkCollision(r.ctx) == "" {
		t.Fatal("checkCollision did not warn about the live rival")
	}
}

func TestReapKeepsRowsItCannotProve(t *testing.T) {
	r := newReapRig(t)
	b := r.bus(t, nil)
	dead := reapedChildPid(t)
	rows := map[string]map[string]any{
		"other pidspace":                  seedRec(r.self, "I-OTHERPS", "some-other-host|boot-x", dead),
		"same host name, other pid space": seedRec(r.self, "I-SAMEHOST", hostPart(b.pidspace)+"|container-ns", dead),
		"no pidspace field":               seedRec(r.self, "I-NOPS", "", dead),
		"pid zero":                        seedRec(r.self, "I-ZERO", b.pidspace, 0),
		"pid one":                         seedRec(r.self, "I-ONE", b.pidspace, 1),
		"pid above 32 bit":                seedRec(r.self, "I-WIDE", b.pidspace, int64(dead)+1<<32),
		"no pid":                          seedRec(r.self, "I-NOPID", b.pidspace, nil),
		"other agent id":                  {"agent_id": "someone-else", "instance": "I-OTHERID", "pidspace": b.pidspace, "pid": dead},
	}
	for name, rec := range rows {
		inst, _ := rec["instance"].(string)
		r.seed(t, r.kv, localKey(r.self, inst), rec)
		_ = name
	}
	b.reapDeadPredecessors(r.ctx, nil)
	for name, rec := range rows {
		inst, _ := rec["instance"].(string)
		if !r.has(t, r.kv, localKey(r.self, inst)) {
			t.Errorf("%s: row was reaped", name)
		}
	}
}

// raceKV moves the row's revision between the reap's read and its delete.
type raceKV struct {
	jetstream.KeyValue
	done bool
}

func (k *raceKV) Get(ctx context.Context, key string) (jetstream.KeyValueEntry, error) {
	e, err := k.KeyValue.Get(ctx, key)
	if err == nil && !k.done && strings.Contains(key, "I-RACE") {
		k.done = true
		if _, perr := k.KeyValue.Put(ctx, key, e.Value()); perr != nil {
			return nil, perr
		}
	}
	return e, err
}

func TestReapKeepsRowThatBeatsBetweenReadAndDelete(t *testing.T) {
	r := newReapRig(t)
	b := r.bus(t, nil)
	dead := reapedChildPid(t)
	key := localKey(r.self, "I-RACE")
	r.seed(t, r.kv, key, seedRec(r.self, "I-RACE", b.pidspace, dead))
	n := b.reapDeadRows(r.ctx, &raceKV{KeyValue: r.kv}, "presence."+r.self.Team+"."+r.self.AgentID+".", nil)
	if n != 0 || !r.has(t, r.kv, key) {
		t.Fatalf("reaped %d, row present %v; a row whose revision moved must stay", n, r.has(t, r.kv, key))
	}
	// With no race the same row goes, so the keep above is the revision, not the data.
	if n := b.reapDeadRows(r.ctx, r.kv, "presence."+r.self.Team+"."+r.self.AgentID+".", nil); n != 1 {
		t.Fatalf("second pass reaped %d, want 1", n)
	}
}

func TestReapGlobalRowFollowsTheSameRules(t *testing.T) {
	r := newReapRig(t)
	gcfg := &globalConfig{Domain: "global", Cluster: "kinu", Role: roleDirector}
	b := r.bus(t, gcfg)
	g, err := b.globalReady(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	dead, live := reapedChildPid(t), liveChildPid(t)
	rows := map[string]struct {
		rec  map[string]any
		gone bool
	}{
		"I-GDEAD":  {seedRec(r.self, "I-GDEAD", b.pidspace, dead), true},
		"I-GLIVE":  {seedRec(r.self, "I-GLIVE", b.pidspace, live), false},
		"I-GOTHER": {seedRec(r.self, "I-GOTHER", "some-other-host|boot-x", dead), false},
		"I-GSAME":  {seedRec(r.self, "I-GSAME", hostPart(b.pidspace)+"|container-ns", dead), false},
		"I-GNOPS":  {seedRec(r.self, "I-GNOPS", "", dead), false},
	}
	for inst, c := range rows {
		r.seed(t, g.kv, g.cfg.presenceKey(inst), c.rec)
	}
	b.reapDeadPredecessors(r.ctx, nil)
	for inst, c := range rows {
		if present := r.has(t, g.kv, g.cfg.presenceKey(inst)); present == c.gone {
			t.Errorf("%s: present=%v, want %v", inst, present, !c.gone)
		}
	}
}

func TestPresenceRowsCarryPidspace(t *testing.T) {
	r := newReapRig(t)
	b := r.bus(t, nil)
	if _, err := b.setPresence(r.ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	rows, _ := b.roster(r.ctx)
	if len(rows) != 1 || rows[0]["pidspace"] != b.pidspace || b.pidspace == "" {
		t.Fatalf("roster = %v, want one row with pidspace %q", rows, b.pidspace)
	}
}

// hostPart is the host name inside a pidspace.
func hostPart(ps string) string { return strings.SplitN(ps, "|", 2)[0] }

// A shim whose own pid space is unreadable reaps nothing, even a provably dead row.
func TestReapWithAnEmptyOwnPidspaceReapsNothing(t *testing.T) {
	r := newReapRig(t)
	b := r.bus(t, nil)
	key := localKey(r.self, "I-DEADROW")
	r.seed(t, r.kv, key, seedRec(r.self, "I-DEADROW", b.pidspace, reapedChildPid(t)))
	b.pidspace = ""
	if b.reapDeadRows(r.ctx, r.kv, "presence."+r.self.Team+"."+r.self.AgentID+".", nil) != 0 || !r.has(t, r.kv, key) {
		t.Fatal("a shim with an empty pidspace reaped a row")
	}
}

func TestPidspaceNeedsBothParts(t *testing.T) {
	if got := pidspaceOf("kinu", "boot-1"); got != "kinu|boot-1" {
		t.Fatalf("pidspaceOf = %q", got)
	}
	if pidspaceOf("", "boot-1") != "" || pidspaceOf("kinu", "") != "" {
		t.Fatal("a missing part must give an empty pidspace")
	}
}

// The linux reader runs against a fixture link, so it is tested on darwin too.
func TestLinuxPidNamespaceReadsTheLinkTarget(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "pid")
	if err := os.Symlink("pid:[4026531836]", link); err != nil {
		t.Fatal(err)
	}
	if got := linuxPidNamespace(link); got != "pid:[4026531836]" {
		t.Fatalf("linuxPidNamespace = %q", got)
	}
	if got := linuxPidNamespace(filepath.Join(dir, "absent")); got != "" {
		t.Fatalf("an unreadable link gave %q, want empty", got)
	}
}
