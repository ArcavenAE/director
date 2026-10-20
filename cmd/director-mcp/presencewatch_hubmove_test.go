package main

import (
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
	w, c := hubWatch(30*time.Minute, 6)
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
