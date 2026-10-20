package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// cashp C3 (sim/design/cluster-gone-alert.md section 6): one parent for a hub
// move, and an hourly cap on lost and hub-moved notices.

func hubWatch(lostAfter time.Duration, cap int) (*PresenceWatch, *wclock) {
	th := defaultWatchThresholds()
	th.LostAfter, th.AlertCap = lostAfter, cap
	return newPresenceWatch(th), newWclock()
}

func rowsFor(clusters ...string) []watchRow {
	var r []watchRow
	for _, c := range clusters {
		r = append(r, supRow(c, "a"))
	}
	return r
}

// shown counts events of a kind that would reach the reader.
func shown(evs []watchEvent, kind string) int {
	n := 0
	for _, e := range evs {
		if e.Kind == kind && !e.Collapsed && !e.Capped {
			n++
		}
	}
	return n
}

func runPasses(w *PresenceWatch, c *wclock, n int, rows ...watchRow) []watchEvent {
	var all []watchEvent
	for i := 0; i < n; i++ {
		all = append(all, sighted(w, c, rows...)...)
	}
	return all
}

// threeUp establishes c1, c2, c3 present.
func threeUp(t *testing.T, w *PresenceWatch, c *wclock) {
	t.Helper()
	runPasses(w, c, 2, rowsFor("c1", "c2", "c3")...)
	for _, n := range []string{"c1", "c2", "c3"} {
		if u := w.Unit(n); u == nil || u.Status != unitPresent {
			t.Fatalf("CONTROL FAILED: %s should be present", n)
		}
	}
}

func TestHubMoveAllClustersGoingAbsentTogetherIsOneParent(t *testing.T) {
	w, c := hubWatch(30*time.Minute, 6)
	threeUp(t, w, c)
	all := runPasses(w, c, 3) // two misses open it
	if shown(all, "hub-moved") != 1 {
		t.Fatalf("one hub-moved parent expected, got %+v", all)
	}
	if shown(all, "absent") != 0 {
		t.Fatalf("per-cluster absent notices must be held while the parent is open, got %+v", all)
	}
	if count(all, "absent") == 0 {
		t.Fatalf("the absences are still recorded, collapsed: %+v", all)
	}
	for _, e := range all {
		if e.Kind == "hub-moved" && !(strings.Contains(e.Text, "c1") && strings.Contains(e.Text, "c2") && strings.Contains(e.Text, "c3")) {
			t.Fatalf("the parent should list the clusters: %q", e.Text)
		}
	}
}

func TestHubMoveControlOneClusterGoingAbsentIsPlain(t *testing.T) {
	w, c := hubWatch(30*time.Minute, 6)
	threeUp(t, w, c)
	all := runPasses(w, c, 3, rowsFor("c1", "c2")...) // c3 alone leaves
	if shown(all, "absent") == 0 || count(all, "hub-moved") != 0 {
		t.Fatalf("one cluster leaving while others stay is a plain absence, got %+v", all)
	}
}

func TestHubMoveBelowTheMinimumIsNoParent(t *testing.T) {
	w, c := hubWatch(30*time.Minute, 6)
	runPasses(w, c, 2, rowsFor("c1")...)
	all := runPasses(w, c, 3)
	if count(all, "hub-moved") != 0 || shown(all, "absent") == 0 {
		t.Fatalf("a single cluster is below the minimum of 2: %+v", all)
	}
}

func TestHubMoveReturnsAreNotCollapsedAndTheParentClosesOnce(t *testing.T) {
	w, c := hubWatchWin(30*time.Minute, 6, 30*time.Minute) // the window outlasts the test: it is not what ends the parent here
	threeUp(t, w, c)
	runPasses(w, c, 3)
	var all []watchEvent
	all = append(all, runPasses(w, c, 2, rowsFor("c1")...)...)
	if shown(all, "back") == 0 || count(all, "hub-moved-closed") != 0 {
		t.Fatalf("a return is shown and the parent stays open while others are out: %+v", all)
	}
	all = append(all, runPasses(w, c, 2, rowsFor("c1", "c2", "c3")...)...)
	if count(all, "hub-moved-closed") != 1 || shown(all, "back") < 3 {
		t.Fatalf("every return shown and one close, got %+v", all)
	}
}

func TestHubMoveConvertsToPerClusterLostWhenNoneReturns(t *testing.T) {
	w, c := hubWatch(5*time.Minute, 6)
	threeUp(t, w, c)
	all := runPasses(w, c, 3)
	if shown(all, "hub-moved") != 1 {
		t.Fatalf("CONTROL FAILED: parent should be open: %+v", all)
	}
	later := runPasses(w, c, 20) // 10 minutes
	if count(later, "hub-moved-converted") != 1 {
		t.Fatalf("after the window with none back the parent converts once, got %+v", later)
	}
	if shown(later, "lost") != 3 {
		t.Fatalf("each cluster then raises its own lost at its bound, got %+v", later)
	}
}

func TestHubMoveLostIsRecordedCollapsedWhileOpen(t *testing.T) {
	w, c := hubWatch(30*time.Second, 6) // the bound is reached inside the window
	threeUp(t, w, c)
	all := runPasses(w, c, 3)
	if shown(all, "lost") != 0 {
		t.Fatalf("a lost under an open parent is not shown: %+v", all)
	}
}

func TestAlertCapHoldsBackTheExcessAndSaysSoOncePerHour(t *testing.T) {
	w, c := hubWatch(time.Minute, 2)
	names := []string{"c1", "c2", "c3", "c4"}
	up := rowsFor(append([]string{"keep"}, names...)...)
	runPasses(w, c, 2, up...)
	var all []watchEvent
	for i := range names { // one cluster leaves every 4 minutes, so no parent opens
		all = append(all, runPasses(w, c, 8, rowsFor(append([]string{"keep"}, names[i+1:]...)...)...)...)
	}
	if shown(all, "lost") != 2 {
		t.Fatalf("a cap of 2 shows two lost notices, got %d in %+v", shown(all, "lost"), all)
	}
	capped := 0
	for _, e := range all {
		if e.Kind == "lost" && e.Capped {
			capped++
		}
	}
	if capped != 2 {
		t.Fatalf("the other two are recorded as capped, got %d: %+v", capped, all)
	}
	if count(all, "alerts-capped") != 1 {
		t.Fatalf("one summary line for the hour, got %+v", all)
	}
}

func TestAlertCapNeverHoldsBackABack(t *testing.T) {
	w, c := hubWatch(time.Minute, 1)
	runPasses(w, c, 2, rowsFor("keep", "c1", "c2")...)
	runPasses(w, c, 8, rowsFor("keep", "c2")...)
	runPasses(w, c, 8, rowsFor("keep")...)
	back := runPasses(w, c, 2, rowsFor("keep", "c1", "c2")...)
	if shown(back, "back") < 2 {
		t.Fatalf("returns are never capped: %+v", back)
	}
}

func TestSoftCountRowsKeepDefaultsAndSayWhy(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "thresholds.json"), []byte(`{"presence_hub_move_min":"many","presence_alert_cap":"0","presence_hub_move_window":"soon"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	counts, cnotes := loadCountRows(dir)
	if counts[thresholdPresenceHubMoveMin] != 2 || counts[thresholdPresenceAlertCap] != 6 {
		t.Fatalf("bad count rows keep defaults, got %v", counts)
	}
	table, notes, err := loadThresholdsNotes(dir)
	if err != nil {
		t.Fatalf("a bad soft row must not fail the table: %v", err)
	}
	if table[thresholdPresenceHubMoveWindow] != 2*time.Minute {
		t.Fatalf("a bad window keeps 2m, got %v", table[thresholdPresenceHubMoveWindow])
	}
	if len(cnotes)+len(notes) != 3 {
		t.Fatalf("one note per bad row, got %q %q", cnotes, notes)
	}
	if err := os.WriteFile(filepath.Join(dir, "thresholds.json"), []byte(`{"presence_hub_move_min":"3","presence_alert_cap":"9"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	counts, _ = loadCountRows(dir)
	if counts[thresholdPresenceHubMoveMin] != 3 || counts[thresholdPresenceAlertCap] != 9 {
		t.Fatalf("good count rows apply, got %v", counts)
	}
	if _, _, err := loadThresholdsNotes(dir); err != nil {
		t.Fatalf("count keys are known keys: %v", err)
	}
}

func TestCountRowsWithANonStringValueKeepTheDefaultAndSayWhy(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "thresholds.json"), []byte(`{"presence_hub_move_min":3,"presence_alert_cap":"9"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	counts, notes := loadCountRows(dir)
	if counts[thresholdPresenceHubMoveMin] != 2 || counts[thresholdPresenceAlertCap] != 9 {
		t.Fatalf("the bad row keeps its default and the good row applies, got %v", counts)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "presence_hub_move_min") {
		t.Fatalf("one note naming the row, got %q", notes)
	}
}

// cashp C3 review 5586449061 item 1: a capped notice is recorded in the events
// file and never queued for the operator. The cap lives in the watch; the shim
// decides what it queues, so this runs the shim's own tick.
func TestShimQueuesNoCappedNotice(t *testing.T) {
	w, c := hubWatch(time.Minute, 2)
	var rows []watchRow
	scan := func(context.Context) watchScan {
		return watchScan{Rows: rows, SelfCheck: true, SelfSeen: true, SelfTS: c.wall, SelfWant: c.wall}
	}
	store := newWatchStore(t.TempDir())
	p := newPresenceWatcher(store, w, scan, func(string, ...any) {})
	p.now = func() time.Time { return c.wall }
	p.mono = func() time.Duration { return c.mono }
	pass := func(n int, clusters ...string) {
		rows = rowsFor(clusters...)
		for i := 0; i < n; i++ {
			c.tick()
			p.tick(context.Background())
		}
	}
	pass(2, "keep", "c1", "c2", "c3", "c4")
	for _, left := range [][]string{{"keep", "c2", "c3", "c4"}, {"keep", "c3", "c4"}, {"keep", "c4"}, {"keep"}} {
		pass(8, left...) // one cluster leaves every 4 minutes, so no hub-moved parent opens
	}
	evs, err := store.Events()
	if err != nil {
		t.Fatal(err)
	}
	capped, queued := 0, 0
	for _, e := range evs {
		if e.Capped {
			capped++
		}
		if !e.Collapsed && !e.Capped {
			queued++
		}
	}
	if capped == 0 {
		t.Fatalf("CONTROL FAILED: a cap of 2 over four lost clusters should hold some back: %+v", evs)
	}
	if len(p.pending) != queued {
		t.Fatalf("the shim queued %d notices, want %d (the %d capped must stay in the file only): %q", len(p.pending), queued, capped, p.pending)
	}
}

// cashp C3 amended 2026-10-20 (review 5586449061 item 2, design section 6 and 8):
// the window bounds the parent's life, the collapse covers only listed clusters
// still out, and each listed back names those still out.

// hubWatchWin is hubWatch with the hub-move window set, so a test can hold a
// parent open as long as it needs.
func hubWatchWin(lostAfter time.Duration, cap int, window time.Duration) (*PresenceWatch, *wclock) {
	th := defaultWatchThresholds()
	th.LostAfter, th.AlertCap, th.HubMoveWindow = lostAfter, cap, window
	return newPresenceWatch(th), newWclock()
}

// shownFor counts events of a kind for one unit that would reach the reader.
func shownFor(evs []watchEvent, unit, kind string) int {
	n := 0
	for _, e := range evs {
		if e.Unit == unit && e.Kind == kind && !e.Collapsed && !e.Capped {
			n++
		}
	}
	return n
}

func TestHubMovePartialReturnNamesTheClustersStillOutAndConvertsAtTheWindow(t *testing.T) {
	w, c := hubWatch(time.Minute, 6)
	threeUp(t, w, c)
	open := runPasses(w, c, 3)
	if shown(open, "hub-moved") != 1 {
		t.Fatalf("CONTROL FAILED: the parent should be open: %+v", open)
	}
	back := runPasses(w, c, 2, rowsFor("c1")...)
	named := false
	for _, e := range back {
		if e.Kind == "back" && e.Unit == "c1" && strings.Contains(e.Text, "c2") && strings.Contains(e.Text, "c3") {
			named = true
		}
	}
	if !named {
		t.Fatalf("c1's back should name c2 and c3 as still out: %+v", back)
	}
	later := runPasses(w, c, 6, rowsFor("c1")...) // past the 2 minute window with c1 back
	if count(later, "hub-moved-converted") != 1 {
		t.Fatalf("the window bounds the parent: it converts once with c2 and c3 out, got %+v", later)
	}
	if shownFor(later, "c2", "lost") != 1 || shownFor(later, "c3", "lost") != 1 {
		t.Fatalf("c2 and c3 each raise a shown lost at their own bound, got %+v", later)
	}
}

func TestHubMoveAClusterOutsideTheParentIsNeverCollapsed(t *testing.T) {
	w, c := hubWatchWin(time.Minute, 6, 30*time.Minute)
	runPasses(w, c, 2, rowsFor("c1", "c2")...)
	open := runPasses(w, c, 3)
	if shown(open, "hub-moved") != 1 {
		t.Fatalf("CONTROL FAILED: the parent listing c1 and c2 should be open: %+v", open)
	}
	runPasses(w, c, 2, rowsFor("c4")...) // c4 appears while the parent is open
	if u := w.Unit("c4"); u == nil || u.Status != unitPresent {
		t.Fatal("CONTROL FAILED: c4 should be present")
	}
	gone := runPasses(w, c, 5)
	if shownFor(gone, "c4", "absent") != 1 || shownFor(gone, "c4", "lost") != 1 {
		t.Fatalf("c4 is not listed, so its absent and lost are shown, not collapsed: %+v", gone)
	}
}

func TestHubMoveAListedClusterThatReturnsAndLeavesAgainIsShown(t *testing.T) {
	w, c := hubWatchWin(time.Minute, 6, 30*time.Minute)
	runPasses(w, c, 2, rowsFor("c1", "c2")...)
	runPasses(w, c, 3)
	back := runPasses(w, c, 2, rowsFor("c1")...)
	if shownFor(back, "c1", "back") != 1 {
		t.Fatalf("CONTROL FAILED: c1 should be back: %+v", back)
	}
	again := runPasses(w, c, 5)
	if shownFor(again, "c1", "absent") != 1 {
		t.Fatalf("c1 came back and left again while the parent is open: its absent is shown, got %+v", again)
	}
	if shownFor(again, "c2", "absent") != 0 {
		t.Fatalf("c2 is still out and listed: its events stay collapsed, got %+v", again)
	}
}

func TestAlertCapHourIsWallClockSoHostSleepCountsTowardIt(t *testing.T) {
	w, c := hubWatch(time.Minute, 1)
	up := rowsFor("keep", "c1", "c2", "c3")
	runPasses(w, c, 2, up...)
	first := runPasses(w, c, 5, rowsFor("keep", "c2", "c3")...)
	if shownFor(first, "c1", "lost") != 1 {
		t.Fatalf("CONTROL FAILED: the first lost uses the cap of 1: %+v", first)
	}
	c.wall = c.wall.Add(40 * time.Minute) // the host sleeps: wall time moves, the monotonic clock does not
	second := runPasses(w, c, 5, rowsFor("keep", "c3")...)
	if shownFor(second, "c2", "lost") != 0 {
		t.Fatalf("40 minutes of sleep still sit inside the cap's hour: c2's lost is held, got %+v", second)
	}
	c.wall = c.wall.Add(30 * time.Minute)
	third := runPasses(w, c, 5, rowsFor("keep")...)
	if shownFor(third, "c3", "lost") != 1 {
		t.Fatalf("the hour has passed on the wall clock: c3's lost is shown, got %+v", third)
	}
}

// cashp C3, director#359 section 6 and 8 (review 5586988630): a listed cluster
// leaves the still-out set when the watch first sees it present on a sighted
// pass, by `back` or by `present-after-gap`.

func textOf(evs []watchEvent, unit, kind string) string {
	for _, e := range evs {
		if e.Unit == unit && e.Kind == kind {
			return e.Text
		}
	}
	return ""
}

func TestHubMoveSeenAfterAGapLeavesTheStillOutSetAndNamesTheRest(t *testing.T) {
	w, c := hubWatchWin(30*time.Minute, 6, 30*time.Minute)
	threeUp(t, w, c)
	runPasses(w, c, 3)
	runPasses(w, c, 2, rowsFor("c1")...) // c1 back by a normal return, c2 and c3 still out
	gapped := c.skip(w, 5*time.Minute, 5*time.Minute, rowsFor("c1", "c2")...)
	if count(gapped, "watch-down") != 1 || !has(gapped, "c2", "present-after-gap") || has(gapped, "c2", "back") {
		t.Fatalf("CONTROL FAILED: c2 is first seen after a gap, with no back: %+v", gapped)
	}
	txt := textOf(gapped, "c2", "present-after-gap")
	if !strings.Contains(txt, "c3") || strings.Contains(txt, "c1") {
		t.Fatalf("the present-after-gap that takes c2 out of the set names c3 only, got %q in %+v", txt, gapped)
	}
	last := runPasses(w, c, 3, rowsFor("c1", "c2", "c3")...)
	closed := textOf(last, "", "hub-moved-closed")
	if count(last, "hub-moved-closed") != 1 || !strings.Contains(closed, "seen present") || strings.Contains(closed, "back") || !strings.Contains(closed, "c2") {
		t.Fatalf("the closing notice says seen present and names c2, seen only after a gap, got %q in %+v", closed, last)
	}
}

func TestHubMoveAnAsleepStragglerGetsNoLostAtConversionAndIsNamedAsleep(t *testing.T) {
	w, c := hubWatch(time.Minute, 6)
	runPasses(w, c, 2, rowsFor("c1", "c2", "c3")...)
	runPasses(w, c, 3)
	if w.Parent == nil {
		t.Fatal("CONTROL FAILED: the parent should be open")
	}
	// c3 is asleep and not present; the unit is set directly, since a unit that
	// is absent cannot become asleep by a transition
	for _, n := range []string{"c3", "c3/a"} {
		u := w.Unit(n)
		u.Status, u.AsleepUntil = unitAsleep, c.wall.Add(24*time.Hour)
	}
	later := runPasses(w, c, 8)
	conv := textOf(later, "", "hub-moved-converted")
	if count(later, "hub-moved-converted") != 1 || !strings.Contains(conv, "c3 (asleep)") {
		t.Fatalf("the conversion notice names c3 as asleep, got %q in %+v", conv, later)
	}
	if shownFor(later, "c3", "lost") != 0 || shownFor(later, "c2", "lost") != 1 {
		t.Fatalf("c2 raises its lost and the asleep c3 does not, got %+v", later)
	}
}

func TestHubMoveABackInAFlapSpellIsShownAndNamesTheClustersStillOut(t *testing.T) {
	w, c := hubWatchWin(30*time.Minute, 6, 30*time.Minute)
	runPasses(w, c, 2, rowsFor("c1", "c2")...)
	for i := 0; i < 2; i++ { // c1 leaves and returns twice: four transitions, so it is flapping
		runPasses(w, c, 3, rowsFor("c2")...)
		runPasses(w, c, 2, rowsFor("c1", "c2")...)
	}
	if u := w.Unit("c1"); u == nil || !u.Flapping {
		t.Fatal("CONTROL FAILED: c1 should be in a flap spell")
	}
	runPasses(w, c, 3) // both leave: the parent lists c1 and c2
	if w.Parent == nil {
		t.Fatal("CONTROL FAILED: the parent should be open")
	}
	back := runPasses(w, c, 2, rowsFor("c1")...)
	if shownFor(back, "c1", "back") != 1 || !strings.Contains(textOf(back, "c1", "back"), "c2") {
		t.Fatalf("c1's back during its flap spell is shown and names c2, got %+v", back)
	}
}
