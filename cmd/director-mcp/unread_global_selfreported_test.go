package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// I-467 rec 2: when unread --global cannot read another cluster's stream (no
// observer grant, or the observer fails), it falls back to the presence rows
// each seat writes for itself, which carry oldest_pending_s. The fallback is
// marked self-reported, names its limits, and says "not reported" for a seat
// whose row has no age, never 0.
//
// The tests read the reports through their JSON form, so the names the
// fallback adds are part of the contract: a report carries "self_reported":
// true and "self_reported_seats", one object per seat with "agent_id",
// "instance", "reported" and, only when reported, "oldest_pending_s" and
// "over_bound".

// putPresence writes (or replaces) one seat's presence row as admin.
func putHubPresence(t *testing.T, ctx context.Context, adminURL, cluster, agent, inst string, fields map[string]any) {
	t.Helper()
	nc, _ := provision(t, ctx, adminURL)
	gjs, err := jetstream.NewWithDomain(nc, "global")
	if err != nil {
		t.Fatal(err)
	}
	kv, err := gjs.KeyValue(ctx, globalPresenceBucket)
	if err != nil {
		t.Fatal(err)
	}
	rec := map[string]any{"agent_id": agent, "instance": inst, "state": "online", "ts": time.Now().UTC().Format(time.RFC3339)}
	for k, v := range fields {
		rec[k] = v
	}
	b, _ := json.Marshal(rec)
	if _, err := kv.Put(ctx, "presence."+cluster+".supervisor."+inst, b); err != nil {
		t.Fatal(err)
	}
}

func reportJSON(t *testing.T, r unreadReport) map[string]any {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// selfReportedReport finds the report for stream among reports and returns its
// JSON form and the report, or fails the test.
func selfReportedReport(t *testing.T, reports []unreadReport, stream string) (map[string]any, unreadReport) {
	t.Helper()
	var names []string
	for _, r := range reports {
		names = append(names, r.Stream)
		if r.Stream == stream {
			return reportJSON(t, r), r
		}
	}
	t.Fatalf("no report for %s (reports: %v)", stream, names)
	return nil, unreadReport{}
}

func seatsOf(t *testing.T, m map[string]any) []map[string]any {
	t.Helper()
	raw, _ := m["self_reported_seats"].([]any)
	var out []map[string]any
	for _, s := range raw {
		if sm, ok := s.(map[string]any); ok {
			out = append(out, sm)
		}
	}
	return out
}

func TestSelfReportedFallbackReadsAnotherClustersPresenceAge(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminURL, leafURL := startLeafLikeServer(t, false)
	seedPartialHub(t, ctx, adminURL)
	putHubPresence(t, ctx, adminURL, "other", "other-supervisor", partialInstB, map[string]any{"oldest_pending_s": 900})
	t.Setenv("DIRECTOR_CLUSTER", "mokuzai")

	reports, err := readUnreadGlobal(ctx, dialLeaf(t, leafURL), "global", time.Now())
	if err != nil {
		t.Fatalf("a read that can fall back got an error: %v", err)
	}
	m, r := selfReportedReport(t, reports, "GLOBAL_TO_other")
	if m["self_reported"] != true {
		t.Errorf("the other cluster's report is not marked self_reported: %v", m)
	}
	if !r.Partial {
		t.Errorf("the fallback report is not marked partial")
	}
	seats := seatsOf(t, m)
	if len(seats) != 1 {
		t.Fatalf("self_reported_seats = %v, want the one other-supervisor seat", seats)
	}
	s := seats[0]
	if s["agent_id"] != "other-supervisor" || s["reported"] != true || s["oldest_pending_s"] != float64(900) {
		t.Errorf("seat = %v, want other-supervisor reported with oldest_pending_s 900", s)
	}
	if s["over_bound"] != true {
		t.Errorf("a 900 s age is over the %v bound but the seat is not over_bound: %v", pendingAgeBound, s)
	}
	if len(r.Durables) != 0 {
		t.Errorf("the fallback listed durables %+v; it read none", r.Durables)
	}
}

func TestSelfReportedFallbackNeverReadsAnAbsentAgeAsZero(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminURL, leafURL := startLeafLikeServer(t, false)
	seedPartialHub(t, ctx, adminURL) // other-supervisor's presence row has no oldest_pending_s
	t.Setenv("DIRECTOR_CLUSTER", "mokuzai")

	reports, err := readUnreadGlobal(ctx, dialLeaf(t, leafURL), "global", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	m, _ := selfReportedReport(t, reports, "GLOBAL_TO_other")
	seats := seatsOf(t, m)
	if len(seats) != 1 {
		t.Fatalf("self_reported_seats = %v, want the one other-supervisor seat", seats)
	}
	s := seats[0]
	if s["reported"] != false {
		t.Errorf("a row with no oldest_pending_s reads reported %v, want false", s["reported"])
	}
	if _, has := s["oldest_pending_s"]; has {
		t.Errorf("an unreported seat carries oldest_pending_s %v, which reads as 0", s["oldest_pending_s"])
	}
	if s["over_bound"] == true {
		t.Errorf("an unreported seat is over_bound: %v", s)
	}
	var out strings.Builder
	printUnread(&out, reportOf(t, reports, "GLOBAL_TO_other"), unreadOpts{})
	// the seat's own line says so; the warning's wording about "not reported"
	// would satisfy a check on the whole text
	seatLine := ""
	for _, l := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(l, "other-supervisor") {
			seatLine = l
		}
	}
	if !strings.Contains(seatLine, "not reported") || strings.Contains(seatLine, "0s") {
		t.Errorf("the seat's printed line does not say not reported, or prints an age: %q (in %q)", seatLine, out.String())
	}
}

func reportOf(t *testing.T, reports []unreadReport, stream string) unreadReport {
	t.Helper()
	_, r := selfReportedReport(t, reports, stream)
	return r
}

func TestSelfReportedFallbackNamesItsLimits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminURL, leafURL := startLeafLikeServer(t, false)
	seedPartialHub(t, ctx, adminURL)
	putHubPresence(t, ctx, adminURL, "other", "other-supervisor", partialInstB, map[string]any{"oldest_pending_s": 30})
	t.Setenv("DIRECTOR_CLUSTER", "mokuzai")

	reports, err := readUnreadGlobal(ctx, dialLeaf(t, leafURL), "global", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	r := reportOf(t, reports, "GLOBAL_TO_other")
	var out strings.Builder
	printUnread(&out, r, unreadOpts{})
	text := strings.Join(r.Warnings, "\n") + "\n" + out.String()
	for _, want := range []string{"self-reported", "both tiers", "an age, not a count", "not reported"} {
		if !strings.Contains(text, want) {
			t.Errorf("the fallback report does not say %q:\n%s", want, text)
		}
	}
	if strings.Contains(out.String(), "pending 5") {
		t.Errorf("the fallback printed a count of the stream: %q", out.String())
	}
	if !strings.Contains(out.String(), "other-supervisor") || !strings.Contains(out.String(), "30") {
		t.Errorf("the printed fallback does not show the seat and its age: %q", out.String())
	}
}

func TestSelfReportedFallbackLeavesOwnClusterReadByName(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminURL, leafURL := startLeafLikeServer(t, false)
	seedPartialHub(t, ctx, adminURL)
	t.Setenv("DIRECTOR_CLUSTER", "mokuzai")

	reports, err := readUnreadGlobal(ctx, dialLeaf(t, leafURL), "global", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	m, r := selfReportedReport(t, reports, "GLOBAL_TO_mokuzai")
	if m["self_reported"] == true {
		t.Errorf("the caller's own cluster is marked self-reported though its stream was read by name")
	}
	if len(r.Durables) != 1 || r.Durables[0].Pending != 3 {
		t.Errorf("the own cluster's durables = %+v, want the one seat with 3 pending", r.Durables)
	}
	if len(reports) != 2 {
		t.Errorf("reports = %d, want the own cluster's and one fallback for the other cluster", len(reports))
	}
}

func TestSelfReportedFallbackSaysSoWhenAClusterHasNoPresence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminURL, leafURL := startLeafLikeServer(t, false)
	seedPartialHub(t, ctx, adminURL)
	// the other cluster's seat stopped writing and its row aged out
	nc, _ := provision(t, ctx, adminURL)
	gjs, _ := jetstream.NewWithDomain(nc, "global")
	kv, _ := gjs.KeyValue(ctx, globalPresenceBucket)
	if err := kv.Purge(ctx, "presence.other.supervisor."+partialInstB); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DIRECTOR_CLUSTER", "mokuzai")

	reports, err := readUnreadGlobal(ctx, dialLeaf(t, leafURL), "global", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range reports {
		if r.Stream == "GLOBAL_TO_other" {
			t.Errorf("a cluster with no presence row has a report %+v, which would read as a quiet cluster", r)
		}
	}
	own := reportOf(t, reports, "GLOBAL_TO_mokuzai")
	if !strings.Contains(strings.Join(own.Warnings, "\n"), "not reported") {
		t.Errorf("the read does not say other clusters with no presence row are not reported: %v", own.Warnings)
	}
}

func TestSelfReportedFallbackFollowsAFailedObserver(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	h := startObserverHub(t)
	seedPartialHub(t, ctx, h.adminURL)
	putHubPresence(t, ctx, h.adminURL, "other", "other-supervisor", partialInstB, map[string]any{"oldest_pending_s": 700})
	setObserverEnv(t, h)
	t.Setenv(observerEnvNKey, h.unknownSeed) // the hub refuses this key

	reports, err := readUnreadGlobalObserved(ctx, dialLeaf(t, h.leafURL), "global", time.Now(), nil)
	if err != nil {
		t.Fatalf("a refused observer with a working leaf got an error: %v", err)
	}
	m, r := selfReportedReport(t, reports, "GLOBAL_TO_other")
	if m["self_reported"] != true || len(seatsOf(t, m)) != 1 {
		t.Errorf("after a failed observer the other cluster is not covered by the self-reported fallback: %v", m)
	}
	if !strings.Contains(strings.Join(r.Warnings, "\n"), "GAP observer") {
		t.Errorf("the fallback report does not carry the observer GAP: %v", r.Warnings)
	}
}

func TestSelfReportedFallbackIsNotUsedWhenTheObserverReads(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	h := startObserverHub(t)
	seedPartialHub(t, ctx, h.adminURL)
	putHubPresence(t, ctx, h.adminURL, "other", "other-supervisor", partialInstB, map[string]any{"oldest_pending_s": 700})
	setObserverEnv(t, h)

	reports, err := readUnreadGlobalObserved(ctx, dialLeaf(t, h.leafURL), "global", time.Now(), nil)
	if err != nil {
		t.Fatal(err)
	}
	perStream := map[string]int{}
	for _, rep := range reports {
		perStream[rep.Stream]++
		if rep.SelfReported || len(rep.Seats) != 0 {
			t.Errorf("report %s is self-reported though the observer read every stream in full", rep.Stream)
		}
	}
	for stream, n := range perStream {
		if n != 1 {
			t.Errorf("%d reports for %s, want exactly one (a fallback must not sit beside the observer's read)", n, stream)
		}
	}
	m, r := selfReportedReport(t, reports, "GLOBAL_TO_other")
	if m["self_reported"] == true || r.Partial {
		t.Errorf("a read the observer made in full is marked self-reported or partial: %v", m)
	}
	if len(r.Durables) != 1 || r.Durables[0].Pending != 5 {
		t.Errorf("the observer's read of GLOBAL_TO_other = %+v, want the one seat with 5 pending", r.Durables)
	}
}
