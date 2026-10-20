package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// R-92 refusal texts with a known-clusters list (cluster-address rulings O3):
// when no presence row sits under the named cluster, the text says only what
// this director's ask ledger saw. It never says "never"; it names the record
// start, lists the clusters seen, and says nothing was sent.
func TestWrongClusterRefusalSaysWhatTheLedgerSaw(t *testing.T) {
	const addr = "global://mokuzai/supervisor"
	const prefix = "presence.mokuzai.supervisor."
	scan := presenceScan{Keys: 5}
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	first := now.Add(-48 * time.Hour)
	ledger := map[string]globalEntry{
		"cluster-b/supervisor": {Cluster: "cluster-b", Role: "supervisor", FirstSeen: first, LastSeen: now.Add(-time.Hour)},
		"cluster-a/supervisor": {Cluster: "cluster-a", Role: "supervisor", FirstSeen: first.Add(time.Hour), LastSeen: now.Add(-2 * time.Hour)},
		"mokuzai/supervisor":   {Cluster: "mokuzai", Role: "supervisor", FirstSeen: first, LastSeen: now.Add(-90 * time.Minute)},
	}
	tests := []struct {
		name string
		seen *seenView
		want []string
		not  []string
	}{
		{"cluster not in the ledger lists the known clusters", &seenView{state: seenLedger, entries: map[string]globalEntry{
			"cluster-b/supervisor": ledger["cluster-b/supervisor"], "cluster-a/supervisor": ledger["cluster-a/supervisor"]}, now: now},
			[]string{"not seen by this director since 2026-10-18T12:00:00Z", "clusters seen by this director: cluster-a, cluster-b", "nothing was sent", "R-92"},
			[]string{"never"}},
		{"cluster in the ledger gives last seen and age", &seenView{state: seenLedger, entries: ledger, now: now},
			[]string{"last saw cluster \"mokuzai\" at 2026-10-20T10:30:00Z (1h30m ago)", "name is probably right", "nothing was sent", "R-92"},
			[]string{"likely wrong", "never"}},
		{"no ledger points at marvel config", &seenView{state: seenNoLedger, now: now},
			[]string{"keeps no record of the clusters it has seen", "marvel config", "nothing was sent", "R-92"},
			[]string{"never", "clusters seen by this director"}},
		{"an empty ledger reads as no record", &seenView{state: seenLedger, entries: map[string]globalEntry{}, now: now},
			[]string{"keeps no record of the clusters it has seen", "marvel config"},
			[]string{"since 0001"}},
		{"an unreadable ledger says so", &seenView{err: errors.New("bucket gone"), now: now},
			[]string{"ask ledger could not be read (bucket gone)", "NOT established", "marvel config", "R-92"},
			[]string{"probably right", "clusters seen by this director"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := noGlobalPresenceErr(addr, prefix, scan, tt.seen).Error()
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("want %q in:\n%s", w, got)
				}
			}
			for _, n := range tt.not {
				if strings.Contains(got, n) {
					t.Errorf("did not want %q in:\n%s", n, got)
				}
			}
		})
	}
}

// The list is capped, and the director's own row and the reserved names are not
// clusters.
func TestWrongClusterRefusalCapsTheList(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	entries := map[string]globalEntry{"director/director": {Cluster: "director", Role: "director", FirstSeen: now, LastSeen: now}}
	for _, c := range []string{"c01", "c02", "c03", "c04", "c05", "c06", "c07", "c08", "c09", "c10", "c11", "c12", "c13", "c14", "c15", "c16", "c17", "c18", "c19", "c20", "c21", "c22"} {
		entries[c+"/supervisor"] = globalEntry{Cluster: c, Role: "supervisor", FirstSeen: now.Add(-time.Hour), LastSeen: now}
	}
	got := noGlobalPresenceErr("global://zz/supervisor", "presence.zz.supervisor.", presenceScan{Keys: 3},
		&seenView{state: seenLedger, entries: entries, now: now}).Error()
	if !strings.Contains(got, "c01, c02") || !strings.Contains(got, "c20 and 2 more") {
		t.Errorf("want the first 20 and a count of the rest, got:\n%s", got)
	}
	if strings.Contains(got, "director,") || strings.Contains(got, "director and") {
		t.Errorf("the director row is not a cluster:\n%s", got)
	}
}

// The other refusal cases keep their text whether or not the ledger is read.
func TestOtherRefusalCasesIgnoreTheLedger(t *testing.T) {
	seen := &seenView{state: seenLedger, entries: map[string]globalEntry{"cluster-b/supervisor": {Cluster: "cluster-b", Role: "supervisor"}}}
	for _, scan := range []presenceScan{{}, {Keys: 5, TeamMatched: 2}, {Keys: 5, TeamMatched: 2, Matched: 2, Unreadable: 2}} {
		a := noGlobalPresenceErr("global://mokuzai/supervisor", "presence.mokuzai.supervisor.", scan, nil).Error()
		b := noGlobalPresenceErr("global://mokuzai/supervisor", "presence.mokuzai.supervisor.", scan, seen).Error()
		if a != b {
			t.Errorf("scan %+v changed with the ledger:\n%s\n%s", scan, a, b)
		}
	}
}

// The send path reads the ledger: a publish to a cluster nobody registered under
// refuses with the clusters this director has seen, and sends nothing.
func TestPublishToUnknownClusterListsKnownClusters(t *testing.T) {
	ctx, js, gjs := seenFixture(t)
	gkv, err := gjs.KeyValue(ctx, globalPresenceBucket)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gkv.Put(ctx, "presence.cluster-b.supervisor.01ABC", []byte(`{"cluster":"cluster-b","role":"supervisor","agent_id":"sup-b","state":"idle"}`)); err != nil {
		t.Fatal(err)
	}
	r := newAskReader(js, askReaderCfg{Broker: "local", File: filepath.Join(t.TempDir(), "a.json"), Hub: gjs})
	if err := r.Pass(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	g := &globalTier{cfg: globalConfig{Domain: "global", Cluster: "c1", Role: roleSupervisor}, js: gjs, local: js, kv: gkv}
	env := &Envelope{Recipient: Recipient{Address: "global://typo/supervisor"}}
	_, err = g.publish(ctx, env, []byte(`{}`))
	if err == nil {
		t.Fatal("a send to an unregistered cluster must refuse")
	}
	for _, w := range []string{"not seen by this director since", "clusters seen by this director: cluster-b", "nothing was sent"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("want %q in:\n%s", w, err)
		}
	}
}
