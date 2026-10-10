package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// LR-3 slice M (sim/design/unread-slice-m.md, director#126): live rows first
// with dead durables collapsed, named states for a live seat whose durable is
// idle, an age threshold, the global tier and a role rollup. Every test checks
// that the reader changed nothing on the broker (design test 6).

// replyFrom publishes, on another seat's inbox, a reply whose sender names the
// given instance in sender.instance (empty for a sender that sets none) and
// whose in_reply_to names inReplyTo. It stands in for a seat that answers mail
// it read some other way than through its durable.
func replyFrom(t *testing.T, ctx context.Context, js jetstream.JetStream, agent, instance, inReplyTo, id string) {
	t.Helper()
	replyWith(t, ctx, js, Sender{AgentID: agent, Workspace: "aae-orc", Instance: instance}, inReplyTo, id)
}

// replyWith publishes the same reply with a caller-built sender, so a test can
// set sender.session, which is the harness session UUID and never evidence.
func replyWith(t *testing.T, ctx context.Context, js jetstream.JetStream, from Sender, inReplyTo, id string) {
	t.Helper()
	e := testEnv(id, from.AgentID, "INFORM", "answer")
	e.Sender = from
	e.InReplyTo = inReplyTo
	e.Recipient.Address = "agent://t/elsewhere"
	b, _ := json.Marshal(e)
	if _, err := js.Publish(ctx, "agent.w.t.elsewhere.inbox", b); err != nil {
		t.Fatal(err)
	}
}

// unchanged runs read and fails if the consumer count or the presence keys
// moved across it.
func unchanged(t *testing.T, ctx context.Context, js jetstream.JetStream, read func() unreadReport) unreadReport {
	t.Helper()
	kv, err := js.KeyValue(ctx, "AGENT_STATE")
	if err != nil {
		t.Fatal(err)
	}
	c1, k1 := counts(t, ctx, js, kv)
	r := read()
	if c2, k2 := counts(t, ctx, js, kv); c2 != c1 || k2 != k1 {
		t.Fatalf("the reader changed the broker: consumers %d->%d presence keys %d->%d", c1, c2, k1, k2)
	}
	return r
}

func rowFor(t *testing.T, r unreadReport, instance string) unreadDurable {
	t.Helper()
	for _, d := range r.Durables {
		if d.Instance == instance {
			return d
		}
	}
	t.Fatalf("no row for instance %s in %+v", instance, r.Durables)
	return unreadDurable{}
}

func drainSeat(t *testing.T, ctx context.Context, b *Bus) {
	t.Helper()
	for {
		e, _, err := b.receive(ctx, 500*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		if e == nil {
			return
		}
	}
}

func TestUnreadParseArgsSliceM(t *testing.T) {
	got, err := parseArgs([]string{"unread", "--all", "--global", "--by-role", "--older-than", "90m", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if !got.json || !got.all || !got.global || !got.byRole || got.olderThan != 90*time.Minute {
		t.Errorf("parsed %+v", got)
	}
	if got, err := parseArgs([]string{"unread", "--older-than=2h"}); err != nil || got.olderThan != 2*time.Hour {
		t.Errorf("--older-than=2h: %+v %v", got, err)
	}
	for _, bad := range [][]string{
		{"unread", "--older-than"},
		{"unread", "--older-than", "soon"},
		{"unread", "--older-than", "-1h"},
		{"unread", "--prune"},
	} {
		if got, err := parseArgs(bad); err == nil {
			t.Errorf("%v accepted as %+v, want a refusal", bad, got)
		}
	}
}

// Design test 1: one live session and three ended ones with mail. The default
// view shows the live row and one summary line; --all lists four; --json
// carries four with live set.
func TestUnreadCollapsesDeadDurables(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	self := Sender{AgentID: "seat", Workspace: "w", Team: "t"}
	for i := 0; i < 3; i++ {
		b, err := connect(ctx, url, self, nil)
		if err != nil {
			t.Fatal(err)
		}
		b.close()
	}
	live := liveSeat(t, ctx, url, self)
	pubEnv(t, ctx, js, "agent.w.t.seat.inbox", "m1", "INFORM", "one")
	pubEnv(t, ctx, js, "agent.w.t.seat.inbox", "m2", "INFORM", "two")
	drainSeat(t, ctx, live)
	time.Sleep(200 * time.Millisecond)

	r := unchanged(t, ctx, js, func() unreadReport { return mustRead(t, ctx, js, time.Now()) })
	if len(r.Durables) != 4 {
		t.Fatalf("json rows = %d, want 4", len(r.Durables))
	}
	nLive := 0
	for _, d := range r.Durables {
		if d.Live {
			nLive++
		}
	}
	if nLive != 1 {
		t.Errorf("live rows = %d, want 1", nLive)
	}
	if d := rowFor(t, r, live.instance); !d.Live || d.State != "reading" {
		t.Errorf("live row %+v, want live and reading", d)
	}

	var def bytes.Buffer
	printUnread(&def, r, unreadOpts{})
	out := def.String()
	if strings.Count(out, "mcp_seat_") != 1 || !strings.Contains(out, "mcp_seat_"+live.instance) {
		t.Errorf("default view should list only the live durable:\n%s", out)
	}
	if !strings.Contains(out, "dead durables: 3, pending 6 in total") || !strings.Contains(out, "--all to list") {
		t.Errorf("default view has no dead-durable summary:\n%s", out)
	}
	var all bytes.Buffer
	printUnread(&all, r, unreadOpts{All: true})
	if n := strings.Count(all.String(), "mcp_seat_"); n != 4 {
		t.Errorf("--all lists %d durables, want 4:\n%s", n, all.String())
	}
	var js1 bytes.Buffer
	printUnread(&js1, r, unreadOpts{JSON: true})
	var back unreadReport
	if err := json.Unmarshal(js1.Bytes(), &back); err != nil || len(back.Durables) != 4 {
		t.Errorf("--json: %v, %d rows", err, len(back.Durables))
	}
	if !strings.Contains(js1.String(), `"live": false`) || !strings.Contains(js1.String(), `"live": true`) {
		t.Errorf("--json does not carry live:\n%s", js1.String())
	}
}

func mustRead(t *testing.T, ctx context.Context, js jetstream.JetStream, now time.Time) unreadReport {
	t.Helper()
	r, err := readUnread(ctx, js, now)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Design test 2, deaf and anchor: a live session that reads nothing with mail
// waiting is durable-idle once its durable is past the window, never "not
// unread mail"; the window counts from the durable's Created, not presence.
func TestUnreadDeafSeatIsDurableIdle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	deaf := liveSeat(t, ctx, url, Sender{AgentID: "deaf", Workspace: "w", Team: "t"})
	for _, id := range []string{"d1", "d2", "d3"} {
		pubEnv(t, ctx, js, "agent.w.t.deaf.inbox", id, "REQUEST", "hello")
	}

	// Inside the window: not yet named idle.
	if d := rowFor(t, mustRead(t, ctx, js, time.Now()), deaf.instance); d.State == "durable-idle" {
		t.Errorf("a durable younger than the window is %s, want it not named idle yet", d.State)
	}
	// Presence rewritten now does not restart the window (anchor).
	if err := deaf.writePresence(ctx, "idle"); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(15 * time.Minute)
	r := unchanged(t, ctx, js, func() unreadReport { return mustRead(t, ctx, js, later) })
	d := rowFor(t, r, deaf.instance)
	if d.State != "durable-idle" || d.Pending != 3 || d.OldestSeq == 0 || d.Evidence != nil {
		t.Fatalf("deaf row %+v, want durable-idle with 3 pending, an age and no evidence", d)
	}
	applyThreshold(&r, 5*time.Minute)
	if d := rowFor(t, r, deaf.instance); !d.OverThreshold || r.OverThresholdCount != 1 {
		t.Errorf("--older-than 5m: row %+v count %d, want it over threshold", d, r.OverThresholdCount)
	}
	var out bytes.Buffer
	printUnread(&out, r, unreadOpts{})
	if !strings.Contains(out.String(), "durable-idle") || strings.Contains(out.String(), "not unread mail") {
		t.Errorf("text view:\n%s", out.String())
	}
}

// Design test 2, correlation: the session answers one of its pending
// messages under its own sender.instance, so it reads elsewhere.
func TestUnreadReplyFromTheSessionIsReadsOutside(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	seat := liveSeat(t, ctx, url, Sender{AgentID: "sup", Workspace: "w", Team: "t"})
	for _, id := range []string{"p1", "p2", "p3"} {
		pubEnv(t, ctx, js, "agent.w.t.sup.inbox", id, "REQUEST", "hello")
	}
	replyFrom(t, ctx, js, "sup", seat.instance, "p2", "r1")

	later := time.Now().Add(15 * time.Minute)
	r := unchanged(t, ctx, js, func() unreadReport { return mustRead(t, ctx, js, later) })
	d := rowFor(t, r, seat.instance)
	if d.State != "reads-outside-durable" || d.Evidence == nil || len(d.Evidence.InReplyTo) != 1 || d.Evidence.InReplyTo[0] != "p2" {
		t.Fatalf("row %+v evidence %+v, want reads-outside-durable on p2", d, d.Evidence)
	}
	applyThreshold(&r, time.Minute)
	if d := rowFor(t, r, seat.instance); d.OverThreshold || r.OverThresholdCount != 0 {
		t.Errorf("reads-outside-durable counted over threshold: %+v", d)
	}
	var out bytes.Buffer
	printUnread(&out, r, unreadOpts{})
	if !strings.Contains(out.String(), "not unread mail") {
		t.Errorf("text view does not say not unread mail:\n%s", out.String())
	}
}

// Design section 3: the instance is self-asserted. Any process holding the
// agent's bus credentials can write any value in sender.instance, so a
// reads-outside-durable row rests on the sender's own claim, and the reader
// says so in the JSON evidence and in both text views.
func TestUnreadEvidenceSaysTheInstanceIsSelfAsserted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	seat := liveSeat(t, ctx, url, Sender{AgentID: "sup", Workspace: "w", Team: "t"})
	pubEnv(t, ctx, js, "agent.w.t.sup.inbox", "p1", "REQUEST", "hello")
	replyFrom(t, ctx, js, "sup", seat.instance, "p1", "r1")

	later := time.Now().Add(15 * time.Minute)
	r := unchanged(t, ctx, js, func() unreadReport { return mustRead(t, ctx, js, later) })
	d := rowFor(t, r, seat.instance)
	if d.Evidence == nil {
		t.Fatalf("row %+v has no evidence", d)
	}
	for _, want := range []string{"self-asserted", "not verified"} {
		if !strings.Contains(d.Evidence.Basis, want) {
			t.Errorf("evidence basis %q does not say %q", d.Evidence.Basis, want)
		}
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"basis":"`) {
		t.Errorf("--json evidence carries no basis field: %s", raw)
	}
	var row, roll bytes.Buffer
	printUnread(&row, r, unreadOpts{})
	if !strings.Contains(row.String(), "self-asserted") {
		t.Errorf("default text view does not say the instance is self-asserted:\n%s", row.String())
	}
	printUnread(&roll, r, unreadOpts{All: true})
	if !strings.Contains(roll.String(), "self-asserted") {
		t.Errorf("--all text view does not say the instance is self-asserted:\n%s", roll.String())
	}
}

// Design test 2, siblings and senders: a reply by a sibling session, by a
// sender that names no instance, or by one that names the instance only in
// sender.session (the harness session UUID field), never reclasses a deaf
// session.
func TestUnreadSiblingOrSessionlessReplyLeavesIdle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	self := Sender{AgentID: "twin", Workspace: "w", Team: "t"}
	one := liveSeat(t, ctx, url, self)
	two := liveSeat(t, ctx, url, self)
	pubEnv(t, ctx, js, "agent.w.t.twin.inbox", "M", "REQUEST", "hello")
	drainSeat(t, ctx, one)
	replyFrom(t, ctx, js, "twin", one.instance, "M", "r-sibling")
	replyFrom(t, ctx, js, "twin", "", "M", "r-noinstance")
	replyWith(t, ctx, js, Sender{AgentID: "twin", Workspace: "aae-orc", Session: two.instance}, "M", "r-session-only")

	later := time.Now().Add(15 * time.Minute)
	r := unchanged(t, ctx, js, func() unreadReport { return mustRead(t, ctx, js, later) })
	if d := rowFor(t, r, one.instance); d.State != "reading" {
		t.Errorf("session 1 %+v, want reading", d)
	}
	if d := rowFor(t, r, two.instance); d.State != "durable-idle" || d.Evidence != nil {
		t.Errorf("session 2 %+v, want durable-idle with no evidence", d)
	}
}

// Design test 2, truncated: a matching reply inside the capped scan does not
// upgrade a row when the scan stopped short of the durable's Created.
func TestUnreadTruncatedScanNeverUpgrades(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	old := unreadScanCap
	unreadScanCap = 10
	defer func() { unreadScanCap = old }()

	seat := liveSeat(t, ctx, url, Sender{AgentID: "sup", Workspace: "w", Team: "t"})
	for _, id := range []string{"p1", "p2", "p3"} {
		pubEnv(t, ctx, js, "agent.w.t.sup.inbox", id, "REQUEST", "hello")
	}
	for i := 0; i < 6; i++ {
		pubEnv(t, ctx, js, "agent.w.t.other.inbox", "f"+string(rune('a'+i)), "INFORM", "filler")
	}
	replyFrom(t, ctx, js, "sup", seat.instance, "p1", "r1")
	for i := 0; i < 5; i++ {
		pubEnv(t, ctx, js, "agent.w.t.other.inbox", "g"+string(rune('a'+i)), "INFORM", "filler")
	}

	later := time.Now().Add(15 * time.Minute)
	r := unchanged(t, ctx, js, func() unreadReport { return mustRead(t, ctx, js, later) })
	d := rowFor(t, r, seat.instance)
	if d.State != "durable-idle" || !d.ScanTruncated {
		t.Fatalf("row %+v, want durable-idle with scan_truncated", d)
	}
	unreadScanCap = 100
	if d := rowFor(t, mustRead(t, ctx, js, later), seat.instance); d.State != "reads-outside-durable" || d.ScanTruncated {
		t.Errorf("with the whole window scanned: %+v, want reads-outside-durable", d)
	}
}

// Design tests 2 (reading, behind) and 3 (threshold): a session reading through
// its durable with mail waiting is behind with an age, a drained one is
// reading, and the threshold marks behind and durable-idle rows only.
func TestUnreadBehindReadingAndThreshold(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	slow := liveSeat(t, ctx, url, Sender{AgentID: "slow", Workspace: "w", Team: "t"})
	deaf := liveSeat(t, ctx, url, Sender{AgentID: "deaf", Workspace: "w", Team: "t"})
	outside := liveSeat(t, ctx, url, Sender{AgentID: "out", Workspace: "w", Team: "t"})
	pubEnv(t, ctx, js, "agent.w.t.slow.inbox", "s1", "INFORM", "one")
	pubEnv(t, ctx, js, "agent.w.t.slow.inbox", "s2", "INFORM", "two")
	if e, _, err := slow.receive(ctx, 2*time.Second); err != nil || e == nil {
		t.Fatalf("slow read nothing: %v", err)
	}
	pubEnv(t, ctx, js, "agent.w.t.deaf.inbox", "d1", "INFORM", "one")
	pubEnv(t, ctx, js, "agent.w.t.out.inbox", "o1", "INFORM", "one")
	replyFrom(t, ctx, js, "out", outside.instance, "o1", "r1")

	later := time.Now().Add(2 * time.Hour)
	r := unchanged(t, ctx, js, func() unreadReport { return mustRead(t, ctx, js, later) })
	if d := rowFor(t, r, slow.instance); d.State != "behind" || d.OldestSeq == 0 {
		t.Errorf("slow %+v, want behind with an age", d)
	}
	applyThreshold(&r, time.Hour)
	if r.OverThresholdCount != 2 {
		t.Errorf("over threshold = %d, want 2 (behind and durable-idle)", r.OverThresholdCount)
	}
	for inst, want := range map[string]bool{slow.instance: true, deaf.instance: true, outside.instance: false} {
		if d := rowFor(t, r, inst); d.OverThreshold != want {
			t.Errorf("%s (%s) over_threshold = %v, want %v", d.AgentID, d.State, d.OverThreshold, want)
		}
	}
	var out bytes.Buffer
	printUnread(&out, r, unreadOpts{})
	if !strings.Contains(out.String(), "! mcp_slow_") || !strings.Contains(out.String(), "over threshold") {
		t.Errorf("text view does not mark over-threshold rows:\n%s", out.String())
	}

	drainSeat(t, ctx, slow)
	time.Sleep(200 * time.Millisecond)
	if d := rowFor(t, mustRead(t, ctx, js, later), slow.instance); d.State != "reading" {
		t.Errorf("after drain %+v, want reading", d)
	}
}

// Design test 5: two live holders of one role, one behind and one reading
// outside: one role line, two holders, the age from the behind holder only.
func TestUnreadRoleRollup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	a := liveSeat(t, ctx, url, Sender{AgentID: "rev-a", Workspace: "w", Team: "t", Role: "reviewer"})
	b := liveSeat(t, ctx, url, Sender{AgentID: "rev-b", Workspace: "w", Team: "t", Role: "reviewer"})
	pubEnv(t, ctx, js, "agent.w.t.rev-a.inbox", "a1", "INFORM", "one")
	pubEnv(t, ctx, js, "agent.w.t.rev-a.inbox", "a2", "INFORM", "two")
	if e, _, err := a.receive(ctx, 2*time.Second); err != nil || e == nil {
		t.Fatalf("a read nothing: %v", err)
	}
	pubEnv(t, ctx, js, "agent.w.t.rev-b.inbox", "b1", "INFORM", "one")
	replyFrom(t, ctx, js, "rev-b", b.instance, "b1", "rb")

	later := time.Now().Add(3 * time.Hour)
	r := unchanged(t, ctx, js, func() unreadReport { return mustRead(t, ctx, js, later) })
	rollupRoles(&r)
	if len(r.Roles) != 1 {
		t.Fatalf("roles = %+v, want one line", r.Roles)
	}
	role := r.Roles[0]
	behind := rowFor(t, r, a.instance)
	if role.Team != "t" || role.Role != "reviewer" || role.Holders != 2 || role.States["reads-outside-durable"] != 1 || role.States["behind"] != 1 {
		t.Errorf("role line %+v", role)
	}
	if role.OldestAgeSeconds != behind.OldestAgeSeconds {
		t.Errorf("role oldest %.0fs, want the behind holder's %.0fs", role.OldestAgeSeconds, behind.OldestAgeSeconds)
	}
	var out bytes.Buffer
	printUnread(&out, r, unreadOpts{ByRole: true})
	if !strings.Contains(out.String(), "t/reviewer") || !strings.Contains(out.String(), "holders 2") {
		t.Errorf("--by-role text:\n%s", out.String())
	}
}

// Design test 4: a hub durable with mail appears under the global report and
// never in the local one; with no hub reachable the reason is printed and the
// command still exits 0.
func TestUnreadGlobalTier(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	nc, js := provision(t, ctx, url)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	if _, err := gjs.CreateStream(ctx, jetstream.StreamConfig{Name: "GLOBAL_TO_kinu", Subjects: []string{"global.kinu.>"}}); err != nil {
		t.Fatal(err)
	}
	sup, err := connect(ctx, url, Sender{AgentID: "sup", Workspace: "w", Team: "t"}, &globalConfig{Domain: "global", Cluster: "kinu", Role: roleSupervisor})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sup.close)
	g, err := sup.globalReady(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.writePresence(ctx, sup.self, sup.instance, sup.pid, "idle"); err != nil {
		t.Fatal(err)
	}
	pubEnv(t, ctx, gjs, "global.kinu.supervisor.inbox", "g1", "REQUEST", "from director")

	for _, d := range mustRead(t, ctx, js, time.Now()).Durables {
		if strings.HasPrefix(d.Durable, "mcp_global_") {
			t.Errorf("local report carries a hub durable: %+v", d)
		}
	}
	reports, err := readUnreadGlobal(ctx, nc, "global", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range reports {
		for _, d := range r.Durables {
			if r.Stream == "GLOBAL_TO_kinu" && d.Instance == sup.instance {
				found = true
				if d.Pending != 1 || !d.Live || d.Presence == nil {
					t.Errorf("hub row %+v, want 1 pending and live", d)
				}
			}
		}
	}
	if !found {
		t.Fatalf("no hub row for the supervisor in %+v", reports)
	}

	short, cancel2 := context.WithTimeout(ctx, 3*time.Second)
	defer cancel2()
	if _, err := readUnreadGlobal(short, nc, "nohub", time.Now()); err == nil {
		t.Error("a domain with no hub answered, want an error")
	}
	var out, errw bytes.Buffer
	if code := runUnreadCmd(ctx, url, cliArgs{mode: "unread", global: true}, "nohub", &out, &errw); code != 0 {
		t.Errorf("exit %d with no hub, want 0", code)
	}
	if !strings.Contains(out.String()+errw.String(), "nohub") {
		t.Errorf("no reason printed for the unreachable hub: %q %q", out.String(), errw.String())
	}
}
