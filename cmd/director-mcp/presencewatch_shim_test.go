package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// Build item 4 (presence-watch.md, section 6): the core wired to the heartbeat
// tick, on the leaf rig. The tests drive the watcher's tick with an injected
// clock, so no test waits on the 30 second heartbeat, and the only real time in
// them is the scratch bucket's own TTL.

type watchFx struct {
	t   *testing.T
	ctx context.Context
	rig *leafRig
	kv  jetstream.KeyValue
	gjs jetstream.JetStream
	bus *Bus
	dir string
	clk *wclock
}

func newWatchFx(t *testing.T, ctx context.Context, ttl time.Duration, on bool) *watchFx {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DIRECTOR_STATE", dir)
	t.Setenv("DIRECTOR_HANDLED", "")
	if on {
		t.Setenv("DIRECTOR_PRESENCE_WATCH", "1")
	} else {
		t.Setenv("DIRECTOR_PRESENCE_WATCH", "")
	}
	rig := startLeafRig(t)
	rig.presenceTTL = ttl
	_, gjs := rig.provision(ctx)
	kv, err := gjs.KeyValue(ctx, globalPresenceBucket)
	if err != nil {
		t.Fatal(err)
	}
	bus, err := connect(ctx, rig.leafURL, Sender{AgentID: "operator", Workspace: "aae-orc", Team: "ops"}, &globalConfig{Domain: "global", Cluster: "kinu", Role: roleDirector})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bus.close)
	fx := &watchFx{t: t, ctx: ctx, rig: rig, kv: kv, gjs: gjs, bus: bus, dir: dir, clk: newWclock()}
	fx.wire()
	return fx
}

// wire starts the watch on the current bus and gives it the injected clock.
func (fx *watchFx) wire() {
	fx.t.Helper()
	if err := fx.bus.startPresenceWatch(nil); err != nil {
		fx.t.Fatal(err)
	}
	if fx.bus.watch != nil {
		fx.bus.watch.now = func() time.Time { return fx.clk.wall }
		fx.bus.watch.mono = func() time.Duration { return fx.clk.mono }
	}
}

// restart is a new shim process on the same state directory and the same hub:
// the old bus closes, a new one attaches, and the watch loads what the old one saved.
func (fx *watchFx) restart() {
	fx.t.Helper()
	fx.bus.close()
	bus, err := connect(fx.ctx, fx.rig.leafURL, Sender{AgentID: "operator", Workspace: "aae-orc", Team: "ops"}, &globalConfig{Domain: "global", Cluster: "kinu", Role: roleDirector})
	if err != nil {
		fx.t.Fatal(err)
	}
	fx.t.Cleanup(bus.close)
	fx.bus = bus
	fx.wire()
}

// tick is one heartbeat as the shim runs it: the director's own row is written
// (writeGlobalPresence), then the injected clock advances and the watch runs.
func (fx *watchFx) tick() {
	fx.t.Helper()
	if msg := fx.bus.writeGlobalPresence(fx.ctx, "working"); msg != "" {
		fx.t.Fatal(msg)
	}
	fx.tickNoBeat()
}

// tickNoBeat advances the injected clock and runs the watch without writing the
// director's own row, as a heartbeat that did not reach the hub would.
func (fx *watchFx) tickNoBeat() {
	fx.t.Helper()
	fx.clk.tick()
	fx.bus.watchTick(fx.ctx)
}

// put writes a supervisor row the way the shim does, stamped with the injected clock.
func (fx *watchFx) put(cluster, inst, state string) {
	fx.t.Helper()
	fx.putKey("presence."+cluster+".supervisor."+inst, map[string]any{
		"cluster": cluster, "role": "supervisor", "agent_id": "sup", "instance": inst,
		"state": state, "ts": fx.clk.wall.Format(time.RFC3339),
	})
}

func (fx *watchFx) putKey(key string, rec any) {
	fx.t.Helper()
	b, _ := json.Marshal(rec)
	if _, err := fx.kv.Put(fx.ctx, key, b); err != nil {
		fx.t.Fatal(err)
	}
}

func (fx *watchFx) events() []watchEvent {
	fx.t.Helper()
	ev, err := newWatchStore(fx.dir).Events()
	if err != nil {
		fx.t.Fatal(err)
	}
	return ev
}

// waitGone waits for the bucket to expire a key.
func (fx *watchFx) waitGone(key string) {
	fx.t.Helper()
	for i := 0; i < 80; i++ {
		if _, err := fx.kv.Get(fx.ctx, key); err != nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	fx.t.Fatalf("%s did not expire", key)
}

func TestWatchSeesSupervisorExpireAndReturn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	fx := newWatchFx(t, ctx, 2*time.Second, true)

	// control: a row that keeps being written reports appeared and never absent.
	fx.put("c1", "s1", "working")
	fx.tick()
	if !has(fx.events(), "c1/s1", "appeared") {
		t.Fatalf("CONTROL FAILED: a first sighting should be recorded, got %+v", fx.events())
	}
	fx.put("c1", "s1", "working")
	fx.tick()
	if count(fx.events(), "absent") != 0 {
		t.Fatalf("CONTROL FAILED: a renewed row reported absent: %+v", fx.events())
	}

	// stop beating: the bucket expires it, and two passes later it is absent.
	fx.waitGone("presence.c1.supervisor.s1")
	fx.tick()
	if count(fx.events(), "absent") != 0 {
		t.Fatal("one missed pass reported absent")
	}
	fx.tick()
	if !has(fx.events(), "c1/s1", "absent") || !has(fx.events(), "c1", "absent") {
		t.Fatalf("an expired row should read absent after two passes, got %+v", fx.events())
	}

	// write it again: two sightings later it is back.
	fx.put("c1", "s1", "working")
	fx.tick()
	if has(fx.events(), "c1/s1", "back") {
		t.Fatal("one sighting reported back")
	}
	fx.put("c1", "s1", "working")
	fx.tick()
	if !has(fx.events(), "c1/s1", "back") || !has(fx.events(), "c1", "back") {
		t.Fatalf("a row written again should read back after two sightings, got %+v", fx.events())
	}
}

func dirDigest(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() {
			lines = append(lines, "d "+rel)
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		lines = append(lines, "f "+rel+" "+string(b))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return string(sum[:])
}

func TestWatchOnWritesStateOffWritesNothing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// control: with the flag on, one pass leaves a state file and an event.
	on := newWatchFx(t, ctx, 0, true)
	on.put("c1", "s1", "working")
	on.tick()
	if _, err := os.Stat(filepath.Join(on.dir, watchStateFile)); err != nil {
		t.Fatalf("CONTROL FAILED: the flag on wrote no state file: %v", err)
	}
	if len(on.events()) == 0 {
		t.Fatal("CONTROL FAILED: the flag on recorded no event")
	}

	// the same pass with the flag off, in a state dir that already holds the
	// seat's other files, changes nothing there.
	off := newWatchFx(t, ctx, 0, false)
	for name, body := range map[string]string{"handled.jsonl": "{\"x\":1}\n", "thresholds.json": "{\"handled_ack_wait\":\"10m\"}\n"} {
		if err := os.WriteFile(filepath.Join(off.dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	off.put("c1", "s1", "working")
	before := dirDigest(t, off.dir)
	off.tick()
	off.tick()
	if n := off.bus.presenceNotices(); len(n) != 0 {
		t.Fatalf("the flag off reported notices: %v", n)
	}
	if after := dirDigest(t, off.dir); after != before {
		t.Fatal("the flag off changed the state directory")
	}
	if off.bus.watch != nil {
		t.Fatal("the flag off built a watcher")
	}
}

func TestWatchHubRestartIsBlindNotAbsent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	fx := newWatchFx(t, ctx, 0, true)
	fx.put("c1", "s1", "working")
	fx.tick()
	fx.tick()

	fx.rig.stopHub()
	fx.tickNoBeat()
	fx.tickNoBeat()
	fx.rig.startHub()
	fx.put("c1", "s1", "working")
	fx.tick()
	ev := fx.events()
	if count(ev, "blind") != 1 {
		t.Fatalf("a hub that cannot be read should give exactly one blind event, got %+v", ev)
	}
	if count(ev, "absent") != 0 || count(ev, "back") != 0 {
		t.Fatalf("a dropped hub must not read as a cluster leaving or returning, got %+v", ev)
	}
	if count(ev, "sighted") != 1 {
		t.Fatalf("the hub reading again should be recorded once, got %+v", ev)
	}
}

func TestWatchForeignKeyNeverCountsAsPresent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	fx := newWatchFx(t, ctx, 0, true)
	// control: a well-formed row on another cluster is counted, so the scan reads rows.
	fx.put("c2", "z", "working")
	fx.putKey("presence.c1.worker.x", map[string]any{"state": "working", "ts": fx.clk.wall.Format(time.RFC3339)})
	if _, err := fx.kv.Put(ctx, "presence.c1.supervisor.y", []byte("not json")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		fx.tick()
	}
	if fx.bus.watch == nil {
		t.Fatal("the flag is on and the session holds the director role, but no watcher was built")
	}
	w := fx.bus.watch.w
	if u := w.Unit("c2/z"); u == nil || u.Status != unitPresent {
		t.Fatalf("CONTROL FAILED: the well-formed row should be counted present, got %+v", u)
	}
	for _, name := range []string{"c1", "c1/x", "c1/y", "c1/worker.x"} {
		if u := w.Unit(name); u != nil {
			t.Fatalf("a foreign key made %s a unit: %+v", name, u)
		}
	}
	var foreign []string
	for _, e := range fx.events() {
		if e.Kind == "foreign" {
			foreign = append(foreign, e.Text)
		}
	}
	sort.Strings(foreign)
	want := "presence.c1.supervisor.y,presence.c1.worker.x"
	if strings.Join(foreign, ",") != want {
		t.Fatalf("foreign keys listed %v, want each of %s once", foreign, want)
	}
}

func TestWatchNoticeReachesWaitAndSummaryOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	fx := newWatchFx(t, ctx, 0, true)
	notice := func(out any) []string {
		m, ok := out.(map[string]any)
		if !ok {
			t.Fatalf("tool result is %T", out)
		}
		n, _ := m["presence_notice"].([]string)
		return n
	}
	// control: nothing to report yet, so no field.
	out, err := toolWait(ctx, fx.bus, json.RawMessage(`{"timeout_seconds":1}`))
	if err != nil || len(notice(out)) != 0 {
		t.Fatalf("CONTROL FAILED: a quiet watch reported %v, %v", notice(out), err)
	}
	fx.put("c1", "s1", "working")
	fx.tick()
	out, err = toolWait(ctx, fx.bus, json.RawMessage(`{"timeout_seconds":1}`))
	if err != nil || len(notice(out)) == 0 {
		t.Fatalf("wait_for_message should carry the appeared notice, got %v, %v", notice(out), err)
	}
	out, _ = toolWait(ctx, fx.bus, json.RawMessage(`{"timeout_seconds":1}`))
	if len(notice(out)) != 0 {
		t.Fatalf("a notice repeated: %v", notice(out))
	}
	fx.put("c3", "q", "working")
	fx.tick()
	out, err = toolSummary(ctx, fx.bus, json.RawMessage(`{}`))
	if err != nil || len(notice(out)) == 0 {
		t.Fatalf("inbox_summary should carry the new notice, got %v, %v", notice(out), err)
	}
}

// cashp C1: a hub that lost the director's own row (replaced or flushed) is not
// the hub this director writes to, so no cluster is called gone from its reads.
func TestWatchFlushedHubIsSelfUnseenNotEveryClusterGone(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	fx := newWatchFx(t, ctx, 0, true)
	fx.put("c1", "s1", "working")
	fx.tick()
	fx.put("c1", "s1", "working")
	fx.tick()
	if u := newWatchStoreUnit(t, fx, "c1/s1"); u == nil || u.Status != unitPresent {
		t.Fatalf("CONTROL FAILED: c1/s1 should be present on sighted passes, got %+v", u)
	}
	if count(fx.events(), "self-unseen") != 0 {
		t.Fatalf("CONTROL FAILED: sighted passes raised self-unseen: %+v", fx.events())
	}

	// the hub is flushed: every row is gone, including the director's own.
	own := fx.bus.globalCfg.presenceKey(fx.bus.instance)
	if err := fx.kv.Delete(ctx, "presence.c1.supervisor.s1"); err != nil {
		t.Fatal(err)
	}
	if err := fx.kv.Delete(ctx, own); err != nil {
		t.Fatal(err)
	}
	fx.tickNoBeat()
	fx.tickNoBeat()
	fx.tickNoBeat()
	ev := fx.events()
	if count(ev, "absent") != 0 {
		t.Fatalf("a flushed hub must not read as every cluster leaving, got %+v", ev)
	}
	if count(ev, "self-unseen") != 1 {
		t.Fatalf("self-unseen should be recorded once, got %+v", ev)
	}

	// the director writes its row again: sighted, and a missing cluster counts again.
	fx.tick()
	if count(fx.events(), "self-seen") != 1 {
		t.Fatalf("the own row back should be recorded once, got %+v", fx.events())
	}
}

func newWatchStoreUnit(t *testing.T, fx *watchFx, name string) *watchUnit {
	t.Helper()
	w, err := newWatchStore(fx.dir).Load(defaultWatchThresholds())
	if err != nil {
		t.Fatal(err)
	}
	return w.Unit(name)
}
