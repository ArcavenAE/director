package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// cashp C2 (sim/design/cluster-gone-alert.md section 5): an absent cluster
// raises lost, once per absence, when its absence reaches its bound. The bound
// is the cluster's own lost_after when its record carries a good one, else the
// director's row; host sleep never counts; a cluster cannot silence its own
// lost with a long bound.

func lostWatch(after, max time.Duration) (*PresenceWatch, *wclock) {
	th := defaultWatchThresholds()
	th.LostAfter, th.LostMax = after, max
	return newPresenceWatch(th), newWclock()
}

func lostRow(cluster, inst, lostAfter string) watchRow {
	r := supRow(cluster, inst)
	r.Rec.LostAfter = lostAfter
	return r
}

func sighted(w *PresenceWatch, c *wclock, rows ...watchRow) []watchEvent {
	c.tick()
	return w.Observe(c.wall, c.mono, watchScan{Rows: rows, SelfCheck: true, SelfSeen: true, SelfTS: c.wall, SelfWant: c.wall})
}

// absentCluster establishes c1/a with the given lost_after and leaves it absent.
func absentCluster(t *testing.T, after, max time.Duration, lostAfter string) (*PresenceWatch, *wclock) {
	t.Helper()
	w, c := lostWatch(after, max)
	sighted(w, c, lostRow("c1", "a", lostAfter))
	sighted(w, c, lostRow("c1", "a", lostAfter))
	sighted(w, c)
	if !has(sighted(w, c), "c1", "absent") {
		t.Fatal("CONTROL FAILED: two missed sighted passes must report the cluster absent")
	}
	return w, c
}

func TestLostDefaultsAreThirtyMinutesAndTwentyFourHours(t *testing.T) {
	th := defaultWatchThresholds()
	if th.LostAfter != 30*time.Minute || th.LostMax != 24*time.Hour {
		t.Fatalf("defaults = %v, %v", th.LostAfter, th.LostMax)
	}
}

func TestLostFiresOnceAtTheBound(t *testing.T) {
	w, c := absentCluster(t, 5*time.Minute, 24*time.Hour, "")
	var all []watchEvent
	for i := 0; i < 20; i++ { // 10 minutes of passes
		all = append(all, sighted(w, c)...)
	}
	if count(all, "lost") != 1 || !has(all, "c1", "lost") {
		t.Fatalf("lost should fire exactly once for the cluster at its bound, got %+v", all)
	}
}

func TestLostNotBeforeTheBound(t *testing.T) {
	w, c := absentCluster(t, 5*time.Minute, 24*time.Hour, "")
	var all []watchEvent
	for i := 0; i < 4; i++ { // 2 minutes
		all = append(all, sighted(w, c)...)
	}
	if count(all, "lost") != 0 {
		t.Fatalf("lost fired before the bound: %+v", all)
	}
}

func TestLostClusterValueOverridesTheDefault(t *testing.T) {
	w, c := absentCluster(t, 30*time.Minute, 24*time.Hour, "1m")
	var all []watchEvent
	for i := 0; i < 6; i++ { // 3 minutes, well under the 30 minute row
		all = append(all, sighted(w, c)...)
	}
	if count(all, "lost") != 1 {
		t.Fatalf("a cluster's own 1m bound should raise lost, got %+v", all)
	}
}

func TestLostBadClusterValueFallsBackWithOneEvent(t *testing.T) {
	for _, bad := range []string{"soon", "-5m", "0s"} {
		w, c := lostWatch(2*time.Minute, 24*time.Hour)
		var all []watchEvent
		all = append(all, sighted(w, c, lostRow("c1", "a", bad))...)
		all = append(all, sighted(w, c, lostRow("c1", "a", bad))...)
		all = append(all, sighted(w, c, lostRow("c1", "a", bad))...)
		if count(all, "lost-after-ignored") != 1 {
			t.Fatalf("%q: one ignored event expected, got %+v", bad, all)
		}
		sighted(w, c)
		sighted(w, c)
		for i := 0; i < 8; i++ {
			all = append(all, sighted(w, c)...)
		}
		if count(all, "lost") != 1 {
			t.Fatalf("%q: the director's own row should still raise lost, got %+v", bad, all)
		}
	}
}

func TestLostClusterValueIsClampedToTheMaximumWithOneEvent(t *testing.T) {
	w, c := lostWatch(time.Minute, 4*time.Minute)
	var all []watchEvent
	for i := 0; i < 3; i++ {
		all = append(all, sighted(w, c, lostRow("c1", "a", "720h"))...)
	}
	if count(all, "lost-after-clamped") != 1 {
		t.Fatalf("one clamped event expected, got %+v", all)
	}
	sighted(w, c)
	sighted(w, c)
	var lost []watchEvent
	for i := 0; i < 20; i++ { // 10 minutes
		lost = append(lost, sighted(w, c)...)
	}
	if count(lost, "lost") != 1 {
		t.Fatalf("a 720h value must not silence lost: it is clamped to 4m, got %+v", lost)
	}
}

func TestLostHostSleepDoesNotCountTowardTheBound(t *testing.T) {
	w, c := absentCluster(t, 10*time.Minute, 24*time.Hour, "")
	// the host slept for the whole span since the cluster went absent, then 4 minutes passed awake
	w.Sleeps = append(w.Sleeps, watchGap{From: w.Units["c1"].Since, To: c.wall.Add(20 * time.Minute)})
	c.wall = c.wall.Add(25 * time.Minute)
	var all []watchEvent
	all = append(all, w.Observe(c.wall, c.mono, watchScan{SelfCheck: true, SelfSeen: true, SelfTS: c.wall, SelfWant: c.wall})...)
	if count(all, "lost") != 0 {
		t.Fatalf("25 minutes of wall time that were mostly host sleep must not reach a 10 minute bound: %+v", all)
	}
}

func TestLostIsOncePerAbsenceAndAReturnClosesIt(t *testing.T) {
	w, c := absentCluster(t, 2*time.Minute, 24*time.Hour, "")
	var all []watchEvent
	for i := 0; i < 8; i++ {
		all = append(all, sighted(w, c)...)
	}
	if count(all, "lost") != 1 {
		t.Fatalf("first absence: %+v", all)
	}
	sighted(w, c, supRow("c1", "a"))
	if !has(sighted(w, c, supRow("c1", "a")), "c1", "back") {
		t.Fatal("CONTROL FAILED: two sightings should read back")
	}
	sighted(w, c)
	sighted(w, c)
	var second []watchEvent
	for i := 0; i < 8; i++ {
		second = append(second, sighted(w, c)...)
	}
	if count(second, "lost") != 1 {
		t.Fatalf("a second absence should raise its own lost, got %+v", second)
	}
}

func TestLostNeverFromASelfUnseenPass(t *testing.T) {
	w, c := absentCluster(t, 2*time.Minute, 24*time.Hour, "")
	var all []watchEvent
	for i := 0; i < 8; i++ {
		c.tick()
		all = append(all, w.Observe(c.wall, c.mono, watchScan{SelfCheck: true})...)
	}
	if count(all, "lost") != 0 {
		t.Fatalf("an unsighted pass raised lost: %+v", all)
	}
}

func TestThresholdSoftRowsKeepTheDefaultAndSayWhy(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "thresholds.json"), []byte(`{"presence_lost_after":"never","presence_lost_after_max":"-1h","presence_flap_quiet":"20m"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	table, notes, err := loadThresholdsNotes(dir)
	if err != nil {
		t.Fatalf("a bad soft row must not fail the table: %v", err)
	}
	if table[thresholdPresenceLostAfter] != 30*time.Minute || table[thresholdPresenceLostAfterMax] != 24*time.Hour {
		t.Fatalf("bad rows should keep defaults, got %v", table)
	}
	if table[thresholdPresenceFlapQuiet] != 20*time.Minute {
		t.Fatalf("a good row still overrides: %v", table)
	}
	if len(notes) != 2 || !strings.Contains(strings.Join(notes, "|"), "presence_lost_after") {
		t.Fatalf("one note per bad soft row expected, got %q", notes)
	}
}

func TestThresholdHardRowsStillFail(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "thresholds.json"), []byte(`{"presence_flap_quiet":"never"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadThresholdsNotes(dir); err == nil {
		t.Fatal("an existing row keeps today's behavior: a bad value is an error")
	}
}
