package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// aae-orc-z63a4: the shim's bus beat to marvel, against a fake marvel on a
// unix socket. Nothing here dials a real daemon.

type fakeMarvel struct {
	mu   sync.Mutex
	reqs []beatRequest
	raw  []string
	// reply decides each answer; nil means an empty success object.
	reply func(beatRequest) string
	sock  string
}

func newFakeMarvel(t *testing.T) *fakeMarvel {
	t.Helper()
	// a short path: unix socket paths are limited to about 104 bytes
	dir, err := os.MkdirTemp("/tmp", "bb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	f := &fakeMarvel{sock: filepath.Join(dir, "s")}
	ln, err := net.Listen("unix", f.sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				line, err := bufio.NewReader(c).ReadString('\n')
				if err != nil {
					return
				}
				var req beatRequest
				_ = json.Unmarshal([]byte(line), &req)
				f.mu.Lock()
				f.reqs = append(f.reqs, req)
				f.raw = append(f.raw, line)
				reply := f.reply
				f.mu.Unlock()
				out := "{}"
				if reply != nil {
					out = reply(req)
				}
				_, _ = c.Write([]byte(out + "\n"))
			}()
		}
	}()
	return f
}

func (f *fakeMarvel) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func beatEnv(f *fakeMarvel) map[string]string {
	return map[string]string{marvelSocketEnv: f.sock, marvelSessionEnv: "sess-1", heartbeatTokenEnv: "tok-SECRET-123"}
}

// a log that collects what the feed says.
func newBeatLog() (*lockedLog, func(string, ...any)) {
	l := &lockedLog{}
	return l, l.add
}

func TestTheBeatRequestMatchesThePinnedFixture(t *testing.T) {
	want, err := os.ReadFile("testdata/bus-beat-request.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(beatRequest{Method: busBeatMethod, Params: beatParams{SessionKey: "fixture-session", SessionToken: "fixture-token", Source: busBeatSource}})
	if err != nil {
		t.Fatal(err)
	}
	if string(got)+"\n" != string(want) {
		t.Fatalf("the request is not byte-equal to the fixture:\n got %s\nwant %s", got, want)
	}
}

func TestABeatCarriesTheSessionTheTokenAndTheSource(t *testing.T) {
	f := newFakeMarvel(t)
	_, logf := newBeatLog()
	bb := newBusBeat(envOf(beatEnv(f)), logf)
	bb.send(context.Background())
	if f.count() != 1 {
		t.Fatalf("requests = %d, want 1", f.count())
	}
	r := f.reqs[0]
	if r.Method != "bus.beat" || r.Params.SessionKey != "sess-1" || r.Params.SessionToken != "tok-SECRET-123" || r.Params.Source != "director-shim" {
		t.Fatalf("request = %+v", r)
	}
}

func TestNoSocketMeansNoDialAndOneLogLineNamingTheKey(t *testing.T) {
	logs, logf := newBeatLog()
	env := map[string]string{marvelSessionEnv: "sess-1", heartbeatTokenEnv: "tok-SECRET-123"}
	if bb := newBusBeat(envOf(env), logf); bb != nil {
		t.Fatal("the feed must be off without MARVEL_SOCKET")
	}
	all := logs.all()
	if len(all) != 1 || !strings.Contains(all[0], "MARVEL_SOCKET") || strings.Contains(all[0], "tok-SECRET-123") || strings.Contains(all[0], "sess-1") {
		t.Fatalf("log = %v, want one line naming MARVEL_SOCKET and no value", all)
	}
	var none *busBeat
	none.send(context.Background()) // a nil feed does nothing
}

func TestTheTokenFileWinsOverTheVariable(t *testing.T) {
	f := newFakeMarvel(t)
	dir := t.TempDir()
	tf := filepath.Join(dir, "tok")
	if err := os.WriteFile(tf, []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := beatEnv(f)
	env[heartbeatTokenFile] = tf
	_, logf := newBeatLog()
	bb := newBusBeat(envOf(env), logf)
	bb.send(context.Background())
	if f.count() != 1 || f.reqs[0].Params.SessionToken != "from-file" {
		t.Fatalf("token = %+v, want from-file", f.reqs)
	}
}

func TestAnUnreadableTokenFileGivesNoTokenNotTheVariable(t *testing.T) {
	f := newFakeMarvel(t)
	env := beatEnv(f)
	env[heartbeatTokenFile] = filepath.Join(t.TempDir(), "missing")
	logs, logf := newBeatLog()
	if bb := newBusBeat(envOf(env), logf); bb != nil {
		t.Fatal("an unreadable token file must turn the feed off, not fall back to the variable")
	}
	if all := logs.all(); len(all) != 1 || !strings.Contains(all[0], heartbeatTokenFile) || strings.Contains(all[0], "tok-SECRET-123") {
		t.Fatalf("log = %v", all)
	}
	if f.count() != 0 {
		t.Fatalf("a dial happened with no token: %d", f.count())
	}
}

func TestUnknownMethodStopsTheFeedWithOneLogLine(t *testing.T) {
	f := newFakeMarvel(t)
	f.reply = func(beatRequest) string { return `{"error":"unknown method: bus.beat"}` }
	logs, logf := newBeatLog()
	bb := newBusBeat(envOf(beatEnv(f)), logf)
	bb.send(context.Background())
	bb.send(context.Background())
	bb.send(context.Background())
	if f.count() != 1 {
		t.Fatalf("requests = %d after unknown method, want 1 (the feed stops)", f.count())
	}
	if all := logs.all(); len(all) != 1 || !strings.Contains(all[0], "does not know bus.beat") {
		t.Fatalf("log = %v, want one line", all)
	}
}

func TestAFailureLogsOnTheTransitionAndRecoveryOnce(t *testing.T) {
	f := newFakeMarvel(t)
	var mu sync.Mutex
	fail := true
	f.reply = func(beatRequest) string {
		mu.Lock()
		defer mu.Unlock()
		if fail {
			return `{"error":"refused: token does not match"}`
		}
		return `{}`
	}
	logs, logf := newBeatLog()
	bb := newBusBeat(envOf(beatEnv(f)), logf)
	for i := 0; i < 3; i++ {
		bb.send(context.Background())
	}
	if n := len(logs.all()); n != 1 {
		t.Fatalf("log lines after three failures = %d, want 1: %v", n, logs.all())
	}
	mu.Lock()
	fail = false
	mu.Unlock()
	bb.send(context.Background())
	bb.send(context.Background())
	if all := logs.all(); len(all) != 2 || !strings.Contains(all[1], "recovered") {
		t.Fatalf("log = %v, want the failure then one recovery", all)
	}
}

func TestADaemonStillAdoptingIsNotAFailure(t *testing.T) {
	f := newFakeMarvel(t)
	f.reply = func(beatRequest) string { return `{"error":"report deferred: daemon starting","starting":true}` }
	logs, logf := newBeatLog()
	bb := newBusBeat(envOf(beatEnv(f)), logf)
	bb.send(context.Background())
	bb.send(context.Background())
	if f.count() != 2 || len(logs.all()) != 0 {
		t.Fatalf("requests = %d, log = %v; a deferral is retried silently", f.count(), logs.all())
	}
}

func TestNoLogLineCarriesTheToken(t *testing.T) {
	f := newFakeMarvel(t)
	f.reply = func(r beatRequest) string {
		return `{"error":"refused: bad token ` + r.Params.SessionToken + `"}`
	}
	logs, logf := newBeatLog()
	env := beatEnv(f)
	bb := newBusBeat(envOf(env), logf)
	bb.send(context.Background())
	// a dial failure too
	env[marvelSocketEnv] = filepath.Join(t.TempDir(), "nobody")
	bb2 := newBusBeat(envOf(env), logf)
	bb2.send(context.Background())
	if len(logs.all()) == 0 {
		t.Fatal("expected failure lines")
	}
	for _, l := range logs.all() {
		if strings.Contains(l, "tok-SECRET-123") {
			t.Fatalf("a log line carries the token: %q", l)
		}
	}
}

// ---- against the bus: when the beat is sent ----

func newBeatBus(t *testing.T, f *fakeMarvel) (*Bus, context.Context) {
	t.Helper()
	r := newPipeRig(t, false)
	_, logf := newBeatLog()
	r.bus.beat = newBusBeat(envOf(beatEnv(f)), logf)
	return r.bus, r.ctx
}

func TestAnnounceSendsOneBeatAfterThePresenceWrite(t *testing.T) {
	f := newFakeMarvel(t)
	b, ctx := newBeatBus(t, f)
	if _, err := b.announce(ctx); err != nil {
		t.Fatal(err)
	}
	if f.count() != 1 {
		t.Fatalf("beats at connect = %d, want 1", f.count())
	}
}

func TestTheTimerBeatsOnceAfterEachSuccessfulPutAndNotAfterAFailedOne(t *testing.T) {
	f := newFakeMarvel(t)
	b, ctx := newBeatBus(t, f)
	run := func(d time.Duration) {
		hctx, cancel := context.WithTimeout(ctx, d)
		defer cancel()
		b.heartbeat(hctx, 40*time.Millisecond, func(string, ...any) {})
	}
	run(330 * time.Millisecond)
	if n := f.count(); n < 4 {
		t.Fatalf("beats after ~8 ticks with a working bucket = %d, want at least 4", n)
	}
	// Break the presence Put and keep ticking: a missing bucket would stall the
	// whole tick, which proves nothing about the beat.
	puts := &putCounter{}
	b.kv = &failingPutKV{KeyValue: b.kv, puts: puts}
	before := f.count()
	run(330 * time.Millisecond)
	if n := puts.n(); n < 3 {
		t.Fatalf("only %d failing Puts: the timer did not keep ticking, so the check below proves nothing", n)
	}
	if after := f.count(); after != before {
		t.Fatalf("beats grew from %d to %d while the presence Put was failing", before, after)
	}
}

type putCounter struct {
	mu sync.Mutex
	c  int
}

func (p *putCounter) add() { p.mu.Lock(); p.c++; p.mu.Unlock() }
func (p *putCounter) n() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.c
}

// failingPutKV is the presence bucket with a Put that fails.
type failingPutKV struct {
	jetstream.KeyValue
	puts *putCounter
}

func (k *failingPutKV) Put(context.Context, string, []byte) (uint64, error) {
	k.puts.add()
	return 0, errors.New("presence put failed")
}

func TestThePresenceTimerIsTheOnlyClock(t *testing.T) {
	f := newFakeMarvel(t)
	b, ctx := newBeatBus(t, f)
	hctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { b.heartbeat(hctx, time.Hour, func(string, ...any) {}); close(done) }()
	time.Sleep(300 * time.Millisecond)
	cancel()
	<-done
	if n := f.count(); n != 0 {
		t.Fatalf("beats with no tick = %d, want 0: the beat has no clock of its own", n)
	}
}

// ---- F1 of the review: no test can reach a real marvel ----

func TestShimEnvDropsTheFourMarvelKeysAndKeepsTheRest(t *testing.T) {
	t.Setenv(marvelSocketEnv, "/real/socket")
	t.Setenv(marvelSessionEnv, "real-session")
	t.Setenv(heartbeatTokenEnv, "real-token")
	t.Setenv(heartbeatTokenFile, "/real/token")
	t.Setenv("DIRECTOR_TEST_KEEP", "1")
	env := shimEnv("EXTRA=1")
	have := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		have[k] = v
	}
	for _, k := range []string{marvelSocketEnv, marvelSessionEnv, heartbeatTokenEnv, heartbeatTokenFile} {
		if _, ok := have[k]; ok {
			t.Fatalf("%s reached the child environment", k)
		}
	}
	if have["DIRECTOR_TEST_KEEP"] != "1" || have["EXTRA"] != "1" {
		t.Fatalf("the rest of the environment was not kept: %v", have)
	}
}

// The check that matters: a seat's own MARVEL_* environment, set here, must not
// make a shim started by a test beat anything.
func TestAShimStartedByATestNeverBeatsTheMarvelOfItsParent(t *testing.T) {
	f := newFakeMarvel(t)
	t.Setenv(marvelSocketEnv, f.sock)
	t.Setenv(marvelSessionEnv, "real-session")
	t.Setenv(heartbeatTokenEnv, "real-token")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	kv, _ := js.KeyValue(ctx, "AGENT_STATE")
	bin := buildShim(t, t.TempDir(), true)
	startShim(t, ctx, bin, url, Sender{AgentID: "nobeat", Workspace: "w", Team: "t"}, kv)
	time.Sleep(1500 * time.Millisecond)
	if n := f.count(); n != 0 {
		t.Fatalf("a shim started by a test sent %d bus.beat request(s) to its parent's marvel", n)
	}
}

// ---- F2 of the review: mutants the first tests let live ----

// the presence bucket passes Put through and counts the ones that landed.
type countingPutKV struct {
	jetstream.KeyValue
	puts *putCounter
}

func (k *countingPutKV) Put(ctx context.Context, key string, v []byte) (uint64, error) {
	rev, err := k.KeyValue.Put(ctx, key, v)
	if err == nil {
		k.puts.add()
	}
	return rev, err
}

// (a) the connect beat follows a presence write that landed, and only that.
func TestTheConnectBeatComesAfterThePutAndOnlyWhenItLanded(t *testing.T) {
	f := newFakeMarvel(t)
	b, ctx := newBeatBus(t, f)
	key := "presence." + b.self.Team + "." + b.self.AgentID + "." + b.instance
	if _, err := b.kv.Get(ctx, key); err == nil {
		t.Fatal("the presence row exists before announce: the order check below would prove nothing")
	}
	var mu sync.Mutex
	rowAtBeat := []bool{}
	f.reply = func(beatRequest) string {
		_, err := b.kv.Get(ctx, key)
		mu.Lock()
		rowAtBeat = append(rowAtBeat, err == nil)
		mu.Unlock()
		return `{}`
	}
	if _, err := b.announce(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := append([]bool(nil), rowAtBeat...)
	mu.Unlock()
	if len(got) != 1 || !got[0] {
		t.Fatalf("presence row seen at the connect beat = %v, want one beat with the row already written", got)
	}

	// A Put that fails sends nothing at connect.
	f2 := newFakeMarvel(t)
	b2, ctx2 := newBeatBus(t, f2)
	b2.kv = &failingPutKV{KeyValue: b2.kv, puts: &putCounter{}}
	if _, err := b2.announce(ctx2); err == nil {
		t.Fatal("announce did not report the failed Put")
	}
	if n := f2.count(); n != 0 {
		t.Fatalf("a connect beat went out after a failed Put: %d", n)
	}
}

// (b) exactly one beat per successful tick, not two.
func TestExactlyOneBeatPerSuccessfulTick(t *testing.T) {
	f := newFakeMarvel(t)
	b, ctx := newBeatBus(t, f)
	puts := &putCounter{}
	b.kv = &countingPutKV{KeyValue: b.kv, puts: puts}
	hctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { b.heartbeat(hctx, 200*time.Millisecond, func(string, ...any) {}); close(done) }()
	end := time.Now().Add(10 * time.Second)
	for f.count() < 3 && time.Now().Before(end) {
		time.Sleep(10 * time.Millisecond)
	}
	// The next tick is about 200 ms away; a second beat of the same tick, if
	// there were one, lands well inside this pause.
	time.Sleep(80 * time.Millisecond)
	cancel()
	<-done
	if beats, p := f.count(), puts.n(); p < 3 || beats != p {
		t.Fatalf("beats = %d, successful presence writes = %d; want equal and at least 3", beats, p)
	}
}

// (c) a marvel that takes the request and never answers cannot hold the
// heartbeat goroutine past the beat's own limit.
func TestAHungMarvelCannotHoldTheHeartbeatPastTheBeatLimit(t *testing.T) {
	f := newFakeMarvel(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	f.reply = func(beatRequest) string { <-release; return `{}` }
	b, ctx := newBeatBus(t, f)
	puts := &putCounter{}
	b.kv = &countingPutKV{KeyValue: b.kv, puts: puts}
	hctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { b.heartbeat(hctx, 40*time.Millisecond, func(string, ...any) {}); close(done) }()
	for i := 0; i < 100 && f.count() == 0; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if f.count() == 0 {
		t.Fatal("the beat never reached the hung marvel")
	}
	if puts.n() < 1 {
		t.Fatal("presence was not written before the beat")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(busBeatTimeout + time.Second):
		t.Fatal("the heartbeat goroutine is still held by a marvel that never answers")
	}
}
