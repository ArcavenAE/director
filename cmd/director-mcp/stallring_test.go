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
		if r.Event == "ring" && r.Result == "sent" && strings.Contains(r.Seat, id) && r.TS.After(last) {
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

func (h *ringHarness) ringer() *stallRinger {
	return &stallRinger{
		Now:    func() time.Time { return h.now },
		Bound:  defaultPendingAgeBound(),
		MinGap: stallRingMinGap,
		List:   func(context.Context) ([]stallSeat, error) { return h.seats, nil },
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
	for _, v := range []string{composerHasText, composerQueued, composerMenu, composerUnclear} {
		h := newRingHarness()
		h.verdict = v
		rows := h.tick()
		if len(h.calls) != 0 {
			t.Fatalf("composer %s: nothing may be typed, got %d injects", v, len(h.calls))
		}
		if len(rows) != 1 || rows[0].Event != "ring-refused" || rows[0].Result != "refused-composer" {
			t.Fatalf("composer %s: want one refused-composer row, got %+v", v, rows)
		}
	}
	h := newRingHarness()
	h.compErr = errors.New("capture failed")
	h.tick()
	if len(h.calls) != 0 {
		t.Fatalf("a composer check that errors must not ring, got %d injects", len(h.calls))
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
	if stallRingEnabled(env(""), roleDirector) {
		t.Fatalf("an unset flag must leave the ring off")
	}
	if stallRingEnabled(env("1"), roleSupervisor) {
		t.Fatalf("a seat that is not the director must never ring")
	}
}

func TestRingLogFileCountsHandRowsAndSurvivesATornLine(t *testing.T) {
	dir := t.TempDir()
	hand := `{"ts":"2026-11-28T00:30:00Z","actor":"director","event":"ring","seat":"arcaven-kinu-builder-g35-1 swept by hand","idle_minutes":10,"mail":1}` + "\n" +
		`{"ts":"2026-11-28T00:40:00Z","actor":"director-mcp","event":"ring-refused","seat":"arcaven-kinu-builder-g35-1","result":"refused-composer"}` + "\n" +
		`{"ts":"2026-11-28T00:5`
	if err := os.WriteFile(filepath.Join(dir, "rings.jsonl"), []byte(hand), 0o600); err != nil {
		t.Fatal(err)
	}
	l := openRingLog(dir)
	last, err := l.LastRing("arcaven-kinu-builder-g35-1")
	if err != nil {
		t.Fatalf("a torn last line must be skipped, not fatal: %v", err)
	}
	want := time.Date(2026, 11, 28, 0, 30, 0, 0, time.UTC)
	if !last.Equal(want) {
		t.Fatalf("the hand ring at 00:30 is the last counted ring (a refusal never counts), got %v", last)
	}
	if err := l.Append(ringRow{TS: ringT0, Actor: "director-mcp", Event: "ring", Seat: "arcaven-kinu-builder-g35-1", Result: "sent", Line: stallRingLine}); err != nil {
		t.Fatalf("append must succeed on a writable directory: %v", err)
	}
	last, err = l.LastRing("arcaven-kinu-builder-g35-1")
	if err != nil || !last.Equal(ringT0) {
		t.Fatalf("a sent row appended by director-mcp must be the last counted ring, got %v, %v", last, err)
	}
}
