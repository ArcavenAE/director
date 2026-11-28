package main

// Tests for the stall ring (aae-orc director-stall-ring). Fakes only: no
// broker, no pane, no marvel, no real state directory.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var ringT0 = time.Date(2026, 11, 28, 1, 0, 0, 0, time.UTC)

type ringCall struct {
	key  string
	text string
	keys []string
}

// ringFake is a memory ring log with the ring counted by seat id.
type ringFake struct {
	rows      []ringRow
	appendErr error
	hand      map[string]time.Time
}

func (f *ringFake) LastRing(id string) (time.Time, error) {
	var last time.Time
	for _, r := range f.rows {
		if r.Event == "ring" && (r.Result == "sent" || r.Result == "unverified" || r.Result == "") && seatNamed(r.Seat, id) && r.TS.After(last) {
			last = r.TS
		}
	}
	if t, ok := f.hand[id]; ok && t.After(last) {
		last = t
	}
	return last, nil
}

func (f *ringFake) Append(row ringRow) error {
	if f.appendErr != nil {
		return f.appendErr
	}
	f.rows = append(f.rows, row)
	return nil
}

type ringHarness struct {
	now      time.Time
	seats    []stallSeat
	fresh    map[string]stallSeat
	verdict  string
	compErr  error
	injectEr map[string]error
	sessions []marvelSession
	sessErr  error
	sessN    int
	cached   *stallRinger
	calls    []ringCall
	log      *ringFake
}

func newRingHarness() *ringHarness {
	return &ringHarness{now: ringT0, verdict: composerEmpty, log: &ringFake{hand: map[string]time.Time{}},
		fresh: map[string]stallSeat{}, injectEr: map[string]error{},
		seats: []stallSeat{stalledSeat("arcaven-kinu-builder-g35-1", 11*60)}}
}

func stalledSeat(id string, ageS int64) stallSeat {
	return stallSeat{AgentID: id, Instance: "i1", Key: "aae/" + id, Cue: cueStateStalled, OldestS: ageS}
}

// ringer is built once, as the shim holds one for the life of the process, so the
// error backoff and the local ring memory persist across ticks.
func (h *ringHarness) ringer() *stallRinger {
	if h.cached == nil {
		h.cached = h.newRinger()
	}
	return h.cached
}

func (h *ringHarness) newRinger() *stallRinger {
	return &stallRinger{
		Now:     func() time.Time { return h.now },
		Bound:   defaultPendingAgeBound(),
		MinGap:  stallRingMinGap,
		Backoff: stallRingErrorBackoff,
		List:    func(context.Context) ([]stallSeat, error) { return h.seats, nil },
		Fresh: func(_ context.Context, s stallSeat) (stallSeat, error) {
			if f, ok := h.fresh[s.AgentID]; ok {
				return f, nil
			}
			return s, nil
		},
		Composer: func(context.Context, stallSeat) (string, error) { return h.verdict, h.compErr },
		Inject: func(_ context.Context, key, text string, keys ...string) error {
			for _, k := range keys {
				if k != "Enter" {
					return errors.New("fake inject: refused key " + k)
				}
			}
			h.calls = append(h.calls, ringCall{key, text, keys})
			return h.injectEr[key]
		},
		Log: h.log,
		Sessions: func(context.Context) ([]marvelSession, error) {
			h.sessN++
			if h.sessErr != nil {
				return nil, h.sessErr
			}
			if h.sessions != nil {
				return h.sessions, nil
			}
			var out []marvelSession
			for _, s := range h.seats {
				out = append(out, marvelSession{Name: s.AgentID, Workspace: "aae"})
			}
			return out, nil
		},
	}
}

func (h *ringHarness) tick() []ringRow { return h.ringer().Tick(context.Background()) }

func TestStallRingSendsOneRingToAStalledSeatWithAnEmptyComposer(t *testing.T) {
	h := newRingHarness()
	rows := h.tick()
	if len(h.calls) != 1 {
		t.Fatalf("a seat stalled 11 minutes with an empty composer must get exactly one ring, got %d injects", len(h.calls))
	}
	if h.calls[0].key != "aae/arcaven-kinu-builder-g35-1" {
		t.Fatalf("the ring must address the seat's own session key, got %q", h.calls[0].key)
	}
	if len(rows) != 1 || rows[0].Event != "ring" || rows[0].Result != "sent" || rows[0].Actor != "director-mcp" {
		t.Fatalf("one ring must leave one sent row by director-mcp, got %+v", rows)
	}
	if rows[0].IdleMinutes != 11 || !strings.Contains(rows[0].Seat, "arcaven-kinu-builder-g35-1") {
		t.Fatalf("the row must name the seat and its 11 idle minutes, got %+v", rows[0])
	}
}

func TestStallRingWritesTheAttemptRowFirstAndItNeverCountsTowardTheHour(t *testing.T) {
	h := newRingHarness()
	h.injectEr["aae/arcaven-kinu-builder-g35-1"] = errors.New("marvel unreachable")
	rows := h.tick()
	if len(h.log.rows) < 2 || h.log.rows[0].Event != "ring-attempt" {
		t.Fatalf("the attempt row (event ring-attempt) must be written before the inject, got %+v", h.log.rows)
	}
	if len(rows) != 1 || rows[0].Event != "ring-refused" || rows[0].Result != "error" {
		t.Fatalf("a failed inject must leave one ring-refused row with result error, got %+v", rows)
	}
	if last, _ := h.log.LastRing("arcaven-kinu-builder-g35-1"); !last.IsZero() {
		t.Fatalf("an attempt or a failed ring must not count toward the hour")
	}
}

func TestSeatNamedMatchesWholeIdsOnly(t *testing.T) {
	cases := []struct {
		text, id string
		want     bool
	}{
		{"arcaven-kinu-builder-g35-1", "arcaven-kinu-builder-g35-1", true},
		{"arcaven-kinu-builder-g35-1 swept by hand", "arcaven-kinu-builder-g35-1", true},
		{"swept arcaven-kinu-builder-g35-1, stalled", "arcaven-kinu-builder-g35-1", true},
		{"arcaven-kinu-builder-g35-10", "arcaven-kinu-builder-g35-1", false},
		{"xarcaven-kinu-builder-g35-1", "arcaven-kinu-builder-g35-1", false},
		{"arcaven-kinu-builder-g35-1x", "arcaven-kinu-builder-g35-1", false},
	}
	for _, c := range cases {
		if got := seatNamed(c.text, c.id); got != c.want {
			t.Fatalf("seatNamed(%q, %q) = %v, want %v", c.text, c.id, got, c.want)
		}
	}
}

func TestStallRingProbesTheBoundAtExactly600Seconds(t *testing.T) {
	h := newRingHarness()
	h.seats = []stallSeat{stalledSeat("arcaven-kinu-builder-g35-1", 599)}
	h.tick()
	if len(h.calls) != 0 {
		t.Fatalf("599 seconds is under the bound, got %d injects", len(h.calls))
	}
	h.seats = []stallSeat{stalledSeat("arcaven-kinu-builder-g35-1", 600)}
	h.tick()
	if len(h.calls) != 1 {
		t.Fatalf("exactly 600 seconds is at the bound and must ring, got %d injects", len(h.calls))
	}
}

func TestMarvelInjectArgvIsTheLineWithRingAndVerifyThenEnter(t *testing.T) {
	var argvs [][]string
	run := func(_ context.Context, argv []string) (string, error) {
		argvs = append(argvs, argv)
		return "", nil
	}
	inj := newMarvelInject(run)
	if err := inj(context.Background(), "aae/seat-0", stallRingLine, "Enter"); err != nil {
		t.Fatalf("a good inject must succeed: %v", err)
	}
	want := [][]string{
		{"marvel", "inject", "aae/seat-0", stallRingLine, "--ring", "--verify"},
		{"marvel", "inject", "aae/seat-0", "", "--enter"},
	}
	if len(argvs) != 2 {
		t.Fatalf("want two marvel calls (the line, then Enter), got %v", argvs)
	}
	for i := range want {
		if strings.Join(argvs[i], "\x00") != strings.Join(want[i], "\x00") {
			t.Fatalf("call %d: want %q, got %q", i, want[i], argvs[i])
		}
	}
}

func TestMarvelInjectRefusesAnyOtherKeyBeforeRunningAnything(t *testing.T) {
	ran := 0
	inj := newMarvelInject(func(context.Context, []string) (string, error) { ran++; return "", nil })
	for _, k := range []string{"Escape", "C-c", "Tab", "enter"} {
		if err := inj(context.Background(), "aae/seat-0", stallRingLine, k); err == nil {
			t.Fatalf("key %q must be refused", k)
		}
	}
	if err := inj(context.Background(), "aae/seat-0", stallRingLine, "Enter", "Enter"); err == nil {
		t.Fatalf("more than one key must be refused")
	}
	if ran != 0 {
		t.Fatalf("a refused key must run nothing, ran %d", ran)
	}
}

func TestMarvelInjectMapsAHeldRingClaimAndStopsBeforeEnter(t *testing.T) {
	var n int
	// The text is marvel's own RingRefusal.Error (internal/api/ring_claim.go at
	// marvel b379ca65): "ring refused: <session> was rung by <ringer> at
	// <RFC3339> (<N>s ago, window <W>s)", which the daemon wraps as
	// "inject <key>: ring refused: ..." (daemon.go). The match is on the
	// substring "ring refused:". The claim window there is 120 s.
	inj := newMarvelInject(func(context.Context, []string) (string, error) {
		n++
		return "inject aae/seat-0: ring refused: aae/seat-0 was rung by director at 2026-11-28T01:00:00Z (30s ago, window 120s)", errors.New("exit status 1")
	})
	err := inj(context.Background(), "aae/seat-0", stallRingLine, "Enter")
	if !errors.Is(err, errRingClaimHeld) {
		t.Fatalf("a held claim must map to errRingClaimHeld, got %v", err)
	}
	if n != 1 {
		t.Fatalf("Enter must not be sent after a refused ring, ran %d calls", n)
	}
	var m int
	inj = newMarvelInject(func(context.Context, []string) (string, error) {
		m++
		return "marvel: ring claim held by another ring", errors.New("exit status 1")
	})
	err = inj(context.Background(), "aae/seat-0", stallRingLine, "Enter")
	if err == nil || errors.Is(err, errRingClaimHeld) || m != 1 {
		t.Fatalf("any other failure is a plain error and stops before Enter, got %v after %d calls", err, m)
	}
}

func TestMarvelInjectStopsBeforeEnterWhenVerifyCannotSeeTheLine(t *testing.T) {
	var n int
	// Marvel's own output (cmd/marvel/inject.go at marvel b379ca65): stderr
	// "verify: unconfirmed (composer <STATE>)", then the error "injected into
	// <key> but the effect was not confirmed: the composer reads <STATE>".
	inj := newMarvelInject(func(context.Context, []string) (string, error) {
		n++
		return "verify: unconfirmed (composer empty)\nError: injected into aae/seat-0 but the effect was not confirmed: the composer reads empty", errors.New("exit status 1")
	})
	err := inj(context.Background(), "aae/seat-0", stallRingLine, "Enter")
	if !errors.Is(err, errRingUnconfirmed) {
		t.Fatalf("a --verify that cannot see the line must map to errRingUnconfirmed, got %v", err)
	}
	if n != 1 {
		t.Fatalf("Enter must not be sent when the line was not confirmed, ran %d calls", n)
	}
	var m int
	inj = newMarvelInject(func(context.Context, []string) (string, error) { m++; return "boom", errors.New("exit status 1") })
	err = inj(context.Background(), "aae/seat-0", stallRingLine, "Enter")
	if err == nil || errors.Is(err, errRingUnconfirmed) || errors.Is(err, errRingClaimHeld) || m != 1 {
		t.Fatalf("any other failure is a plain error and stops before Enter, got %v after %d calls", err, m)
	}
}

func TestStallRingCountsAnUnverifiedRingTowardTheHourAndDoesNotRetry(t *testing.T) {
	h := newRingHarness()
	h.injectEr["aae/arcaven-kinu-builder-g35-1"] = errRingUnconfirmed
	rows := h.tick()
	if len(rows) != 1 || rows[0].Event != "ring" || rows[0].Result != "unverified" {
		t.Fatalf("an unverified ring must leave one row with event ring and result unverified, got %+v", rows)
	}
	for _, c := range h.calls {
		for _, k := range c.keys {
			if k != "Enter" {
				t.Fatalf("no key but Enter is ever sent, got %v", c.keys)
			}
		}
	}
	if last, _ := h.log.LastRing("arcaven-kinu-builder-g35-1"); !last.Equal(ringT0) {
		t.Fatalf("an unverified ring counts toward the hour, so the seat is not retried every tick; last counted ring %v", last)
	}
	// The composer reads empty again (the line never landed): still no retry inside the hour.
	h.injectEr = map[string]error{}
	h.now = ringT0.Add(5 * time.Minute)
	h.tick()
	if len(h.calls) != 1 {
		t.Fatalf("an unverified ring must not be retried inside the hour, got %d injects in all", len(h.calls))
	}
}

func TestStallRingRingsAComposerThatHoldsOnlyADimSuggestion(t *testing.T) {
	h := newRingHarness()
	h.verdict = composerSuggestion
	rows := h.tick()
	if len(h.calls) != 1 {
		t.Fatalf("composer-check calls a dim suggestion safe (typed text replaces it), so the seat must be rung, got %d injects", len(h.calls))
	}
	if len(rows) != 1 || rows[0].Event != "ring" || rows[0].Result != "sent" {
		t.Fatalf("a ring over a suggestion is a sent ring, got %+v", rows)
	}
}

func TestStallRingTypesOnlyTheFixedLineAndEnter(t *testing.T) {
	h := newRingHarness()
	h.tick()
	if len(h.calls) != 1 {
		t.Fatalf("want one inject to inspect, got %d", len(h.calls))
	}
	c := h.calls[0]
	if c.text != stallRingLine {
		t.Fatalf("the ring must type the fixed line, got %q", c.text)
	}
	if len(c.keys) != 1 || c.keys[0] != "Enter" {
		t.Fatalf("the only key beside the line is Enter, got %v", c.keys)
	}
	for _, r := range c.text {
		if r > 126 || r < 32 {
			t.Fatalf("the ring line must be plain ASCII, got %q", c.text)
		}
	}
}

func TestStallRingRingsAtMostOnceAnHour(t *testing.T) {
	h := newRingHarness()
	h.tick()
	if last, _ := h.log.LastRing("arcaven-kinu-builder-g35-1"); !last.Equal(ringT0) {
		t.Fatalf("the sent ring must reach the log, so a restart still sees it; last counted ring %v", last)
	}
	h.cached = nil // a shim restart: the in-memory ring memory is gone, the log is not
	h.now = ringT0.Add(59 * time.Minute)
	h.tick()
	if len(h.calls) != 1 {
		t.Fatalf("59 minutes after a ring the seat must not be rung again, got %d injects", len(h.calls))
	}
	h.now = ringT0.Add(61 * time.Minute)
	h.tick()
	if len(h.calls) != 2 {
		t.Fatalf("61 minutes after a ring the seat is due again, got %d injects", len(h.calls))
	}
}

func TestStallRingCountsARingMadeByHand(t *testing.T) {
	h := newRingHarness()
	h.log.hand["arcaven-kinu-builder-g35-1"] = ringT0.Add(-30 * time.Minute)
	h.tick()
	if len(h.calls) != 0 {
		t.Fatalf("a seat rung by hand 30 minutes ago must not be rung again, got %d injects", len(h.calls))
	}
	h.now = ringT0.Add(31 * time.Minute)
	h.tick()
	if len(h.calls) != 1 {
		t.Fatalf("a hand ring that is 61 minutes old no longer holds the seat, got %d injects", len(h.calls))
	}
}

func TestStallRingNeverTypesOverAComposerThatIsNotEmpty(t *testing.T) {
	for _, v := range []string{composerHasText, composerQueued, composerMenu, composerUnclear, "", "empty", "EMPTY\n", " EMPTY", "BOGUS", "suggestion", "SUGGESTION\n", " SUGGESTION", "SUGGESTIONS"} {
		h := newRingHarness()
		h.verdict = v
		rows := h.tick()
		if len(h.calls) != 0 {
			t.Fatalf("composer %q: only the exact verdicts EMPTY and SUGGESTION may ring, got %d injects", v, len(h.calls))
		}
		if len(rows) != 1 || rows[0].Event != "ring-refused" || rows[0].Result != "refused-composer" {
			t.Fatalf("composer %q: want one ring-refused row with result refused-composer, got %+v", v, rows)
		}
	}
	h := newRingHarness()
	h.compErr = errors.New("capture failed")
	rows := h.tick()
	if len(h.calls) != 0 {
		t.Fatalf("a composer check that errors must not ring, got %d injects", len(h.calls))
	}
	if len(rows) != 1 || rows[0].Event != "ring-refused" || rows[0].Result != "refused-composer" {
		t.Fatalf("a composer error must leave one ring-refused row, got %+v", rows)
	}
}

func TestStallRingSkipsASeatThatIsNotStalledAtActTime(t *testing.T) {
	cases := map[string]stallSeat{
		"live":       {AgentID: "a", Key: "k", Cue: cueStateLive, OldestS: 700},
		"off":        {AgentID: "a", Key: "k", Cue: cueStateOff, OldestS: 700},
		"unverified": {AgentID: "a", Key: "k", Cue: cueStateUnver, OldestS: 700},
		"nine min":   {AgentID: "a", Key: "k", Cue: cueStateStalled, OldestS: 540},
	}
	for name, s := range cases {
		h := newRingHarness()
		h.seats = []stallSeat{s}
		h.tick()
		if len(h.calls) != 0 {
			t.Fatalf("%s: a seat that is not stalled for the bound must not be rung, got %d injects", name, len(h.calls))
		}
		h.seats = []stallSeat{stalledSeat("a", 601)}
		h.tick()
		if len(h.calls) != 1 {
			t.Fatalf("%s control: a seat stalled 601 seconds must be rung, got %d injects", name, len(h.calls))
		}
	}
	h := newRingHarness()
	h.fresh["arcaven-kinu-builder-g35-1"] = stallSeat{AgentID: "arcaven-kinu-builder-g35-1", Key: "k", Cue: cueStateLive, OldestS: 0}
	h.tick()
	if len(h.calls) != 0 {
		t.Fatalf("a seat that woke between the scan and the ring must not be rung, got %d injects", len(h.calls))
	}
	delete(h.fresh, "arcaven-kinu-builder-g35-1")
	h.tick()
	if len(h.calls) != 1 {
		t.Fatalf("control: the same seat, still stalled at act time, must be rung, got %d injects", len(h.calls))
	}
}

func TestStallRingDoesNotRingWhenItCannotLogTheAttempt(t *testing.T) {
	h := newRingHarness()
	h.log.appendErr = errors.New("disk full")
	h.tick()
	if len(h.calls) != 0 {
		t.Fatalf("a ring that cannot be logged must not be sent, got %d injects", len(h.calls))
	}
	h.log.appendErr = nil
	h.tick()
	if len(h.calls) != 1 {
		t.Fatalf("control: with the log writable the same seat must be rung, got %d injects", len(h.calls))
	}
}

func TestStallRingLogsAClaimRefusalAndDoesNotRetryInTheTick(t *testing.T) {
	h := newRingHarness()
	h.seats = []stallSeat{stalledSeat("arcaven-kinu-builder-g35-1", 700), stalledSeat("arcaven-kinu-author-g36-0", 700)}
	h.injectEr["aae/arcaven-kinu-builder-g35-1"] = errRingClaimHeld
	rows := h.tick()
	n := 0
	for _, c := range h.calls {
		if c.key == "aae/arcaven-kinu-builder-g35-1" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("a refused claim must not be retried in the same tick, got %d injects for the seat", n)
	}
	var refused, sent int
	for _, r := range rows {
		switch r.Result {
		case "refused-claim":
			if r.Event != "ring-refused" {
				t.Fatalf("a claim refusal is event ring-refused, got %q", r.Event)
			}
			refused++
		case "sent":
			sent++
		}
	}
	if refused != 1 || sent != 1 {
		t.Fatalf("want one refused-claim and one sent row, got %+v", rows)
	}
	if last, _ := h.log.LastRing("arcaven-kinu-builder-g35-1"); !last.IsZero() {
		t.Fatalf("a refused ring must not count toward the hour")
	}
}

func TestStallRingIsOffUnlessTheFlagIsOnAndTheRoleIsDirector(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == "DIRECTOR_STALL_RING" {
				return v
			}
			return ""
		}
	}
	if !stallRingEnabled(env("1"), roleDirector) {
		t.Fatalf("DIRECTOR_STALL_RING=1 on the director role must turn the ring on")
	}
	for _, v := range []string{"", "0", "true", "yes", " 1", "1 ", "on"} {
		if stallRingEnabled(env(v), roleDirector) {
			t.Fatalf("DIRECTOR_STALL_RING=%q must leave the ring off: the flag means exactly 1", v)
		}
	}
	if stallRingEnabled(env("1"), roleSupervisor) {
		t.Fatalf("a seat that is not the director must never ring")
	}
}

func TestRingLogFileCountsHandRowsAndSurvivesATornLine(t *testing.T) {
	dir := t.TempDir()
	hand := `{"ts":"2026-11-28T00:30:00Z","actor":"director","event":"ring","seat":"arcaven-kinu-builder-g35-1 swept by hand","idle_minutes":10,"mail":1}` + "\n" +
		`{"ts":"2026-11-28T00:40:00Z","actor":"director-mcp","event":"ring-refused","seat":"arcaven-kinu-builder-g35-1","result":"refused-composer"}` + "\n" +
		`{"ts":"2026-11-28T00:41:00Z","actor":"director-mcp","event":"ring-refused","seat":"arcaven-kinu-builder-g35-1","result":"refused-claim"}` + "\n" +
		`{"ts":"2026-11-28T00:42:00Z","actor":"director-mcp","event":"ring-attempt","seat":"arcaven-kinu-builder-g35-1"}` + "\n" +
		`{"ts":"2026-11-28T00:45:00Z","actor":"director-mcp","event":"ring","seat":"arcaven-kinu-builder-g35-1","result":"unverified"}` + "\n" +
		`{"ts":"2026-11-28T00:50:00Z","actor":"director","event":"ring","seat":"arcaven-kinu-builder-g35-10"}` + "\n" +
		`{"ts":"2026-11-28T00:5`
	if err := os.WriteFile(filepath.Join(dir, "rings.jsonl"), []byte(hand), 0o600); err != nil {
		t.Fatal(err)
	}
	l := openRingLog(dir)
	last, err := l.LastRing("arcaven-kinu-builder-g35-1")
	if err != nil {
		t.Fatalf("a torn last line must be skipped, not fatal: %v", err)
	}
	want := time.Date(2026, 11, 28, 0, 45, 0, 0, time.UTC)
	if !last.Equal(want) {
		t.Fatalf("the unverified ring at 00:45 is the last counted ring (it counts; a refusal, an attempt and a ring of ...-g35-10 never count for ...-g35-1), got %v", last)
	}
	if err := l.Append(ringRow{TS: ringT0, Actor: "director-mcp", Event: "ring", Seat: "arcaven-kinu-builder-g35-1", Result: "sent", Line: stallRingLine}); err != nil {
		t.Fatalf("append must succeed on a writable directory: %v", err)
	}
	last, err = l.LastRing("arcaven-kinu-builder-g35-1")
	if err != nil || !last.Equal(ringT0) {
		t.Fatalf("a sent row appended by director-mcp must be the last counted ring, got %v, %v", last, err)
	}
}

func TestStallSeatFromRowReadsAFreshStalledRowAndSkipsOwnAndStaleRows(t *testing.T) {
	now := time.Date(2026, 11, 28, 1, 0, 0, 0, time.UTC)
	rec := map[string]any{"agent_id": "arcaven-kinu-builder-g35-1", "workspace": "aae", "team": "arcaven-kinu", "instance": "i1",
		"cue": "stalled", "oldest_pending_s": float64(700), "ts": now.Add(-10 * time.Second).Format(time.RFC3339)}
	key := "presence.arcaven-kinu.arcaven-kinu-builder-g35-1.i1"
	s, ok := stallSeatFromRow(key, rec, now, "self", "director-self")
	if !ok || s.Key != "aae/arcaven-kinu-builder-g35-1" || s.OldestS != 700 || s.Cue != "stalled" {
		t.Fatalf("a fresh stalled row must read as a candidate with the session key workspace/id, got %+v, %v", s, ok)
	}
	if _, ok := stallSeatFromRow(key, rec, now, "i1", "director-self"); ok {
		t.Fatalf("this session's own row must never be a candidate")
	}
	rec["ts"] = now.Add(-5 * time.Minute).Format(time.RFC3339)
	if _, ok := stallSeatFromRow(key, rec, now, "self", "director-self"); ok {
		t.Fatalf("a stale row must never be a candidate")
	}
}

func TestRingLogFileCountsAResultlessHandRowOnItsOwn(t *testing.T) {
	dir := t.TempDir()
	row := `{"ts":"2026-11-28T00:30:00Z","actor":"director","event":"ring","seat":"arcaven-kinu-builder-g35-1 swept by hand","idle_minutes":10,"mail":1}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "rings.jsonl"), []byte(row), 0o600); err != nil {
		t.Fatal(err)
	}
	last, err := openRingLog(dir).LastRing("arcaven-kinu-builder-g35-1")
	want := time.Date(2026, 11, 28, 0, 30, 0, 0, time.UTC)
	if err != nil || !last.Equal(want) {
		t.Fatalf("a hand ring with no result field counts on its own, got %v, %v", last, err)
	}
	if last, _ := openRingLog(dir).LastRing("arcaven-kinu-builder-g35-10"); !last.IsZero() {
		t.Fatalf("a hand ring of ...-g35-1 must not count for ...-g35-10, got %v", last)
	}
	if last, err := openRingLog(t.TempDir()).LastRing("arcaven-kinu-builder-g35-1"); err != nil || !last.IsZero() {
		t.Fatalf("a missing log is an empty history, not an error, got %v, %v", last, err)
	}
}

func TestStallSeatCarriesItsOwnTeamAndBuildsItsRowKeyFromIt(t *testing.T) {
	now := time.Date(2026, 11, 28, 1, 0, 0, 0, time.UTC)
	rec := map[string]any{"agent_id": "other-builder-g1-0", "workspace": "aae", "team": "other-team", "instance": "i9",
		"cue": "stalled", "oldest_pending_s": float64(700), "ts": now.Add(-10 * time.Second).Format(time.RFC3339)}
	s, ok := stallSeatFromRow("presence.other-team.other-builder-g1-0.i9", rec, now, "self", "director-self")
	if !ok || s.Team != "other-team" {
		t.Fatalf("a seat outside the director's team must be a candidate carrying its own team, got %+v, %v", s, ok)
	}
	if got, want := stallRowKey(s), "presence.other-team.other-builder-g1-0.i9"; got != want {
		t.Fatalf("the re-read key must use the seat's own team, got %q, want %q", got, want)
	}
}

func TestStallSeatSkipsTheDirectorsOwnAgentIdUnderAnotherInstance(t *testing.T) {
	now := time.Date(2026, 11, 28, 1, 0, 0, 0, time.UTC)
	rec := map[string]any{"agent_id": "director-self", "workspace": "aae", "team": "arcaven-kinu", "instance": "stale-old-instance",
		"cue": "stalled", "oldest_pending_s": float64(700), "ts": now.Add(-10 * time.Second).Format(time.RFC3339)}
	if s, ok := stallSeatFromRow("presence.arcaven-kinu.director-self.stale-old-instance", rec, now, "live-instance", "director-self"); ok {
		t.Fatalf("a stale row of the director's own agent id under another instance must never be rung, got %+v", s)
	}
}

func TestStallRingLeavesASeatAloneFor15MinutesAfterAnError(t *testing.T) {
	h := newRingHarness()
	h.injectEr["aae/arcaven-kinu-builder-g35-1"] = errors.New("marvel unreachable")
	h.tick()
	attempts := func() int {
		n := 0
		for _, r := range h.log.rows {
			if r.Event == "ring-attempt" {
				n++
			}
		}
		return n
	}
	if len(h.calls) != 1 || attempts() != 1 {
		t.Fatalf("the first tick must try once, got %d injects and %d attempt rows", len(h.calls), attempts())
	}
	h.now = ringT0.Add(14 * time.Minute)
	h.tick()
	if len(h.calls) != 1 || attempts() != 1 {
		t.Fatalf("14 minutes after an error the seat must be left alone, got %d injects and %d attempt rows", len(h.calls), attempts())
	}
	h.now = ringT0.Add(16 * time.Minute)
	h.tick()
	if len(h.calls) != 2 {
		t.Fatalf("16 minutes after an error the seat may be tried again, got %d injects", len(h.calls))
	}
}

func TestMarvelInjectLogsAnEnterThatFailsAfterAConfirmedLineAsUnconfirmed(t *testing.T) {
	var n int
	inj := newMarvelInject(func(_ context.Context, argv []string) (string, error) {
		n++
		if n == 2 {
			return "marvel: session gone", errors.New("exit status 1")
		}
		return "", nil
	})
	err := inj(context.Background(), "aae/seat-0", stallRingLine, "Enter")
	if !errors.Is(err, errRingUnconfirmed) {
		t.Fatalf("an Enter that fails after a staged, confirmed line leaves the line unsent, so it maps to errRingUnconfirmed, got %v", err)
	}
	if n != 2 {
		t.Fatalf("want the line call and the Enter call, got %d", n)
	}
}

func TestStallRingAddressesTheMarvelWorkspaceFromTheSessionList(t *testing.T) {
	h := newRingHarness()
	// the director's workspace for the seat is dir-ws; marvel's is aae
	h.seats = []stallSeat{{AgentID: "arcaven-kinu-builder-g35-1", Instance: "i1", Key: "dir-ws/arcaven-kinu-builder-g35-1", Cue: cueStateStalled, OldestS: 700}}
	h.sessions = []marvelSession{{Name: "arcaven-kinu-builder-g35-1", Workspace: "aae"}}
	h.tick()
	if len(h.calls) != 1 || h.calls[0].key != "aae/arcaven-kinu-builder-g35-1" {
		t.Fatalf("the ring must address the workspace marvel reports for the session, got %+v", h.calls)
	}
}

func TestStallRingRingsNothingForAnUnknownOrAmbiguousSessionName(t *testing.T) {
	cases := map[string]struct {
		sessions []marvelSession
		result   string
	}{
		"unknown":   {[]marvelSession{{Name: "someone-else", Workspace: "aae"}}, "refused-unknown-session"},
		"ambiguous": {[]marvelSession{{Name: "arcaven-kinu-builder-g35-1", Workspace: "aae"}, {Name: "arcaven-kinu-builder-g35-1", Workspace: "other"}}, "refused-ambiguous-session"},
	}
	for name, c := range cases {
		c := c
		t.Run(name, func(t *testing.T) {
			h := newRingHarness()
			h.sessions = c.sessions
			rows := h.tick()
			if len(h.calls) != 0 {
				t.Fatalf("%s: nothing may be typed, got %d injects", name, len(h.calls))
			}
			if len(rows) != 1 || rows[0].Event != "ring-refused" || rows[0].Result != c.result {
				t.Fatalf("%s: want one ring-refused row with result %s, got %+v", name, c.result, rows)
			}
			for _, r := range h.log.rows {
				if r.Event == "ring-attempt" {
					t.Fatalf("%s: no attempt row may be written for a seat that cannot be addressed", name)
				}
			}
		})
	}
}

func TestStallRingDoesNotMatchANearSessionName(t *testing.T) {
	h := newRingHarness()
	h.sessions = []marvelSession{{Name: "arcaven-kinu-builder-g35-10", Workspace: "aae"}}
	rows := h.tick()
	if len(h.calls) != 0 {
		t.Fatalf("a session named ...-g35-10 must not take the ring meant for ...-g35-1, got %+v", h.calls)
	}
	if len(rows) != 1 || rows[0].Result != "refused-unknown-session" {
		t.Fatalf("a near name is an unknown session, got %+v", rows)
	}
}

func TestStallRingPicksTheExactNameWhenANearNameIsListed(t *testing.T) {
	h := newRingHarness()
	h.seats = []stallSeat{{AgentID: "arcaven-kinu-builder-g35-1", Instance: "i1", Key: "dir-ws/arcaven-kinu-builder-g35-1", Cue: cueStateStalled, OldestS: 700}}
	h.sessions = []marvelSession{{Name: "arcaven-kinu-builder-g35-10", Workspace: "wrong"}, {Name: "arcaven-kinu-builder-g35-1", Workspace: "aae"}}
	h.tick()
	if len(h.calls) != 1 || h.calls[0].key != "aae/arcaven-kinu-builder-g35-1" {
		t.Fatalf("the ring must use the workspace of the exactly named session, got %+v", h.calls)
	}
}

func TestStallRingARefusedComposerDoesNotStartTheErrorBackoff(t *testing.T) {
	h := newRingHarness()
	h.verdict = composerHasText
	h.tick()
	if len(h.calls) != 0 {
		t.Fatalf("a composer holding text must not be rung over, got %d injects", len(h.calls))
	}
	h.verdict = composerEmpty
	h.now = ringT0.Add(time.Minute)
	h.tick()
	if len(h.calls) != 1 {
		t.Fatalf("a refused composer is not an error: the next minute's empty composer must be rung, got %d injects", len(h.calls))
	}
}

func TestStallRingReadsTheSessionsOnceAPassAndLogsOneRefusalWhenItFails(t *testing.T) {
	h := newRingHarness()
	h.seats = []stallSeat{stalledSeat("arcaven-kinu-builder-g35-1", 700), stalledSeat("arcaven-kinu-author-g36-0", 700)}
	h.tick()
	if h.sessN != 1 || len(h.calls) != 2 {
		t.Fatalf("two stalled seats must cost one sessions read and two rings, got %d reads and %d injects", h.sessN, len(h.calls))
	}
}

func TestStallRingLogsOneRefusalWhenTheSessionsReadFails(t *testing.T) {
	h2 := newRingHarness()
	h2.seats = []stallSeat{stalledSeat("arcaven-kinu-builder-g35-1", 700), stalledSeat("arcaven-kinu-author-g36-0", 700)}
	h2.sessErr = errors.New("marvel unreachable")
	rows := h2.tick()
	if len(h2.calls) != 0 {
		t.Fatalf("a failed sessions read must ring nothing, got %d injects", len(h2.calls))
	}
	if len(rows) != 1 || rows[0].Event != "ring-refused" || rows[0].Result != "refused-sessions" {
		t.Fatalf("a failed sessions read must log one ring-refused row (not one per seat), got %+v", rows)
	}
}

func TestMarvelSessionsReadsTheListOnceThroughMarvelGetSessions(t *testing.T) {
	var argvs [][]string
	list := marvelSessions(func(_ context.Context, argv []string) (string, error) {
		argvs = append(argvs, argv)
		return `[{"Name":"arcaven-kinu-builder-g35-1","Workspace":"aae","Team":"arcaven-kinu","Generation":35},{"Name":"x","Workspace":"w"}]`, nil
	})
	got, err := list(context.Background())
	if err != nil || len(got) != 2 || got[0].Name != "arcaven-kinu-builder-g35-1" || got[0].Workspace != "aae" {
		t.Fatalf("the session list must parse Name and Workspace, got %+v, %v", got, err)
	}
	if len(argvs) != 1 || strings.Join(argvs[0], " ") != "marvel get sessions -o json" {
		t.Fatalf("want one call to `marvel get sessions -o json`, got %v", argvs)
	}
	bad := marvelSessions(func(context.Context, []string) (string, error) { return "not json", nil })
	if _, err := bad(context.Background()); err == nil {
		t.Fatalf("output that is not JSON must be an error")
	}
	fail := marvelSessions(func(context.Context, []string) (string, error) { return "", errors.New("exit status 1") })
	if _, err := fail(context.Background()); err == nil {
		t.Fatalf("a failed marvel call must be an error")
	}
}

func TestStallRingARefusedClaimDoesNotBackOffTheSeat(t *testing.T) {
	h := newRingHarness()
	h.injectEr["aae/arcaven-kinu-builder-g35-1"] = errRingClaimHeld
	h.tick()
	if len(h.calls) != 1 {
		t.Fatalf("the first tick must try once, got %d injects", len(h.calls))
	}
	// past marvel's 120 s claim window the claim is free again
	h.injectEr = map[string]error{}
	h.now = ringT0.Add(3 * time.Minute)
	h.tick()
	if len(h.calls) != 2 {
		t.Fatalf("a held claim is not an error: 3 minutes later the seat must be rung, got %d injects", len(h.calls))
	}
}
