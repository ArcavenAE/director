package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// A shim that exits on its first connect error stays down after a loaded host
// times out a JetStream call (I-685 2b). These pin the retry loop with a clock
// that does not sleep, and the half-finished connect against a scratch broker.

type fakeClock struct {
	t     time.Time
	waits []time.Duration
}

func (f *fakeClock) clock() retryClock {
	return retryClock{
		now: func() time.Time { return f.t },
		wait: func(ctx context.Context, d time.Duration) bool {
			if ctx.Err() != nil {
				return false
			}
			f.waits = append(f.waits, d)
			f.t = f.t.Add(d)
			return true
		},
	}
}

func discardLog(string, ...any) {}

func TestConnectRetryServesAfterNTimeouts(t *testing.T) {
	fc := &fakeClock{t: time.Unix(0, 0)}
	want := &Bus{}
	calls := 0
	var logged []string
	logf := func(f string, a ...any) { logged = append(logged, f) }
	got, err := connectRetry(context.Background(), defaultConnectRetry, fc.clock(), logf, nil, func(context.Context) (*Bus, error) {
		calls++
		if calls <= 3 {
			return nil, errors.New("role durable consumer: " + context.DeadlineExceeded.Error())
		}
		return want, nil
	})
	if err != nil {
		t.Fatalf("connectRetry returned %v after 3 timeouts, want the bus", err)
	}
	if got != want || calls != 4 {
		t.Errorf("got bus %p after %d calls, want %p after 4", got, calls, want)
	}
	if len(logged) != 4 { // three failed attempts, then the success line
		t.Errorf("logged %d lines, want one per failed attempt and one for the success: %v", len(logged), logged)
	}
}

func TestConnectRetryBackoffDoublesToTheCap(t *testing.T) {
	fc := &fakeClock{t: time.Unix(0, 0)}
	calls := 0
	_, _ = connectRetry(context.Background(), defaultConnectRetry, fc.clock(), discardLog, nil, func(context.Context) (*Bus, error) {
		calls++
		if calls <= 8 {
			return nil, errors.New("durable consumer: timeout")
		}
		return &Bus{}, nil
	})
	want := []time.Duration{1, 2, 4, 8, 16, 30, 30, 30}
	if len(fc.waits) != len(want) {
		t.Fatalf("waits %v, want %d of them", fc.waits, len(want))
	}
	for i, w := range want {
		if fc.waits[i] != w*time.Second {
			t.Errorf("wait %d was %s, want %s", i, fc.waits[i], w*time.Second)
		}
	}
}

func TestConnectRetryGivesUpAfterTheBound(t *testing.T) {
	fc := &fakeClock{t: time.Unix(0, 0)}
	calls := 0
	_, err := connectRetry(context.Background(), defaultConnectRetry, fc.clock(), discardLog, nil, func(context.Context) (*Bus, error) {
		calls++
		return nil, errors.New("durable consumer: " + context.DeadlineExceeded.Error())
	})
	if err == nil {
		t.Fatal("connectRetry returned nil for a connect that never works")
	}
	if !strings.Contains(err.Error(), "gave up after") || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Errorf("error lost the give-up note or the last cause: %v", err)
	}
	if spent := fc.t.Sub(time.Unix(0, 0)); spent > connectRetryTotal {
		t.Errorf("waited %s, past the %s bound", spent, connectRetryTotal)
	}
	if calls < 10 {
		t.Errorf("only %d attempts inside the bound", calls)
	}
}

func TestConnectRetryReturnsAPermanentErrorAtOnce(t *testing.T) {
	fc := &fakeClock{t: time.Unix(0, 0)}
	calls := 0
	_, err := connectRetry(context.Background(), defaultConnectRetry, fc.clock(), discardLog, nil, func(context.Context) (*Bus, error) {
		calls++
		return nil, &permanentConnectError{errors.New("bad DIRECTOR_HANDLED setting")}
	})
	if err == nil || calls != 1 || len(fc.waits) != 0 {
		t.Errorf("permanent error: err=%v calls=%d waits=%v, want one call and no wait", err, calls, fc.waits)
	}
}

func TestConnectRetryStopsOnASignalDuringTheAttempt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fc := &fakeClock{t: time.Unix(0, 0)}
	calls := 0
	_, err := connectRetry(ctx, defaultConnectRetry, fc.clock(), discardLog, nil, func(context.Context) (*Bus, error) {
		calls++
		cancel() // SIGTERM arrives while the attempt runs
		return nil, errors.New("durable consumer: timeout")
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Errorf("err=%v calls=%d, want context.Canceled after one call", err, calls)
	}
}

func TestConnectRetryRealClockWaitEndsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if realRetryClock.wait(ctx, time.Hour) {
		t.Error("wait reported a full sleep on a cancelled context")
	}
	if time.Since(start) > 5*time.Second {
		t.Error("wait did not return at once on a cancelled context")
	}
}

// The role durable create times out after it landed on the broker, with the own
// durable already created and a departed seat's durables holding nonzero floors.
// The retry must reuse the instance (no stray durable), must not move a start
// floor (a durable that exists cannot be given a different start sequence), and
// must end with both durables starting where the first attempt put them, even
// though the departed seat acked more mail in between.
func TestConnectRetryAfterHalfFinishedConnectLeavesNoStrayDurable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)

	self := Sender{AgentID: "ops-builder-g1-0", Workspace: "w", Team: "t", Role: "builder"}
	ownSubject := fmt.Sprintf("agent.%s.%s.%s.inbox", self.Workspace, self.Team, self.AgentID)
	roleSubject := fmt.Sprintf("agent.%s.%s.role.%s.inbox", self.Workspace, self.Team, self.Role)

	// A departed predecessor of the seat and a departed holder of the role, each with
	// mail acked: both start floors are above 0.
	for _, subj := range []string{ownSubject, roleSubject} {
		for i := 0; i < 5; i++ {
			if _, err := js.Publish(ctx, subj, []byte(fmt.Sprintf(`{"n":%d}`, i))); err != nil {
				t.Fatal(err)
			}
		}
	}
	ackSome := func(name, subj string, n int) {
		t.Helper()
		cons, err := js.CreateOrUpdateConsumer(ctx, "AGENT_INBOX", jetstream.ConsumerConfig{Durable: name, FilterSubject: subj, AckPolicy: jetstream.AckExplicitPolicy})
		if err != nil {
			t.Fatal(err)
		}
		batch, err := cons.Fetch(n, jetstream.FetchMaxWait(5*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		for m := range batch.Messages() {
			if err := m.DoubleAck(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	predecessor := "mcp_" + self.AgentID + "_01JAAAAAAAAAAAAAAAAAAAAAAA"
	roleHolder := "mcp_other-seat-g1-0_01JBBBBBBBBBBBBBBBBBBBBBBB"
	ackSome(predecessor, ownSubject, 3)
	ackSome(roleHolder, roleSubject, 3)
	wantOwn, err := ownSubjectFloor(ctx, js, "AGENT_INBOX", "mcp_"+self.AgentID+"_", ownSubject, func(string) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	wantRole, _, err := roleSubjectFloor(ctx, js, "AGENT_INBOX", roleSubject)
	if err != nil {
		t.Fatal(err)
	}
	if wantOwn == 0 || wantRole == 0 {
		t.Fatalf("seeded floors own=%d role=%d, want both above 0", wantOwn, wantRole)
	}

	// The role create lands and then reports a timeout; before the retry the
	// role holder acks more mail, so a floor read again would be higher.
	failedRole := 0
	old := createConsumer
	createConsumer = func(ctx context.Context, js jetstream.JetStream, stream string, cfg jetstream.ConsumerConfig) (jetstream.Consumer, error) {
		cons, err := old(ctx, js, stream, cfg)
		if err == nil && strings.HasSuffix(cfg.Durable, "-role") && failedRole == 0 {
			failedRole++
			ackSome(roleHolder, roleSubject, 2)
			return nil, context.DeadlineExceeded
		}
		return cons, err
	}
	defer func() { createConsumer = old }()

	plan := newConnectPlan()
	fc := &fakeClock{t: time.Unix(0, 0)}
	bus, err := connectRetry(ctx, defaultConnectRetry, fc.clock(), discardLog, nil, func(ctx context.Context) (*Bus, error) {
		return connectPlanned(ctx, url, self, nil, plan)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer bus.close()
	if failedRole != 1 {
		t.Fatalf("the hook failed the role create %d times, want 1", failedRole)
	}
	if now, _, _ := roleSubjectFloor(ctx, js, "AGENT_INBOX", roleSubject); now <= wantRole {
		t.Fatalf("the role floor read again is %d, not above %d: the retry could not have moved it, so the test proves nothing", now, wantRole)
	}
	if !plan.floorsSet || plan.floor != wantOwn || plan.roleFloor != wantRole {
		t.Errorf("plan floors own=%d role=%d set=%v, want %d and %d from the first attempt", plan.floor, plan.roleFloor, plan.floorsSet, wantOwn, wantRole)
	}
	st, err := js.Stream(ctx, "AGENT_INBOX")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	starts := map[string]uint64{}
	lister := st.ListConsumers(ctx)
	for info := range lister.Info() {
		if strings.HasPrefix(info.Name, "mcp_"+self.AgentID+"_") && strings.Contains(info.Name, plan.instance) {
			names = append(names, info.Name)
			starts[info.Name] = info.Config.OptStartSeq
		}
	}
	if len(names) != 2 {
		t.Errorf("durables of this startup after the retry: %v, want its own and its role durable only", names)
	}
	if got := starts["mcp_"+self.AgentID+"_"+plan.instance]; got != wantOwn+1 {
		t.Errorf("own durable starts at %d, want %d (the first attempt's floor plus one)", got, wantOwn+1)
	}
	if got := starts["mcp_"+self.AgentID+"_"+plan.instance+"-role"]; got != wantRole+1 {
		t.Errorf("role durable starts at %d, want %d (the first attempt's floor plus one, not the floor after the holder acked more)", got, wantRole+1)
	}
	// No durable of this seat from another instance, other than the seeded departed one.
	lister2 := st.ListConsumers(ctx)
	for info := range lister2.Info() {
		if strings.HasPrefix(info.Name, "mcp_"+self.AgentID+"_") && !strings.Contains(info.Name, plan.instance) && info.Name != predecessor {
			t.Errorf("stray durable %s left by an attempt", info.Name)
		}
	}
}

func TestConnectRetryPolicyReadsTheOverride(t *testing.T) {
	var notes []string
	note := func(f string, a ...any) { notes = append(notes, f) }
	get := func(v string) func(string) string {
		return func(k string) string {
			if k == connectRetryEnv {
				return v
			}
			return ""
		}
	}
	if p := connectRetryPolicy(get(""), note); p != defaultConnectRetry || len(notes) != 0 {
		t.Errorf("unset: got %+v with %d notes, want the default", p, len(notes))
	}
	if p := connectRetryPolicy(get("90s"), note); p.total != 90*time.Second || p.first != connectRetryFirst {
		t.Errorf("90s: got %+v", p)
	}
	if p := connectRetryPolicy(get("0"), note); p.total != 0 {
		t.Errorf("0: got %+v, want a zero bound", p)
	}
	for _, bad := range []string{"soon", "-5s", "5"} {
		notes = nil
		if p := connectRetryPolicy(get(bad), note); p != defaultConnectRetry || len(notes) != 1 {
			t.Errorf("%q: got %+v with %d notes, want the default and one note", bad, p, len(notes))
		}
	}
	// With a zero bound the first failure is final: one attempt, no wait.
	fc := &fakeClock{t: time.Unix(0, 0)}
	calls := 0
	_, err := connectRetry(context.Background(), connectRetryPolicy(get("0s"), note), fc.clock(), discardLog, nil, func(context.Context) (*Bus, error) {
		calls++
		return nil, errors.New("durable consumer: timeout")
	})
	if err == nil || calls != 1 || len(fc.waits) != 0 {
		t.Errorf("zero bound: err=%v calls=%d waits=%v, want one call, no wait", err, calls, fc.waits)
	}
}
