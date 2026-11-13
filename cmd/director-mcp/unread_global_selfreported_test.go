package main

import (
	"context"
	"encoding/json"
	"os"
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

// Presence rows the fallback cannot read are counted per cluster and named, so a
// cluster whose rows all fail does not vanish as "no presence row" (a false
// reason). The report carries "unreadable_rows": N, and a warning that says
// "N presence row(s) unreadable". Tests read it through the JSON form.

// putHubPresenceRaw writes raw bytes at a presence key as admin.
func putHubPresenceRaw(t *testing.T, ctx context.Context, adminURL, key string, raw []byte) {
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
	if _, err := kv.Put(ctx, key, raw); err != nil {
		t.Fatal(err)
	}
}

const otherKeyB = "presence.other.supervisor." + partialInstB

func unreadableRowsOf(m map[string]any) float64 {
	n, _ := m["unreadable_rows"].(float64)
	return n
}

func warningsText(r unreadReport) string { return strings.Join(r.Warnings, "\n") }

func TestSelfReportedUnreadableRowFromAReadFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminURL, leafURL := startLeafLikeServer(t, true) // key gets are refused
	seedPartialHub(t, ctx, adminURL)
	t.Setenv("DIRECTOR_CLUSTER", "mokuzai")

	reports, err := readUnreadGlobal(ctx, dialLeaf(t, leafURL), "global", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	m, r := selfReportedReport(t, reports, "GLOBAL_TO_other")
	if unreadableRowsOf(m) != 1 {
		t.Errorf("unreadable_rows = %v, want 1 for the row whose get failed: %v", m["unreadable_rows"], m)
	}
	if !strings.Contains(warningsText(r), "1 presence row unreadable") {
		t.Errorf("the warnings do not say 1 presence row unreadable: %v", r.Warnings)
	}
}

func TestSelfReportedUnreadableRowNotJSON(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminURL, leafURL := startLeafLikeServer(t, false)
	seedPartialHub(t, ctx, adminURL)
	putHubPresenceRaw(t, ctx, adminURL, otherKeyB, []byte("this is not json"))
	t.Setenv("DIRECTOR_CLUSTER", "mokuzai")

	reports, err := readUnreadGlobal(ctx, dialLeaf(t, leafURL), "global", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	m, r := selfReportedReport(t, reports, "GLOBAL_TO_other")
	if unreadableRowsOf(m) != 1 || !strings.Contains(warningsText(r), "1 presence row unreadable") {
		t.Errorf("a row that is not JSON is not counted and named: unreadable_rows %v, warnings %v", m["unreadable_rows"], r.Warnings)
	}
}

func TestSelfReportedUnreadableRowMissingAField(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminURL, leafURL := startLeafLikeServer(t, false)
	seedPartialHub(t, ctx, adminURL)
	for name, body := range map[string]string{"no instance": `{"agent_id":"other-supervisor","oldest_pending_s":900}`, "no agent_id": `{"instance":"` + partialInstB + `","oldest_pending_s":900}`} {
		putHubPresenceRaw(t, ctx, adminURL, otherKeyB, []byte(body))
		t.Setenv("DIRECTOR_CLUSTER", "mokuzai")
		reports, err := readUnreadGlobal(ctx, dialLeaf(t, leafURL), "global", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		m, r := selfReportedReport(t, reports, "GLOBAL_TO_other")
		if unreadableRowsOf(m) != 1 || !strings.Contains(warningsText(r), "1 presence row unreadable") {
			t.Errorf("%s: the row is not counted and named: unreadable_rows %v, warnings %v", name, m["unreadable_rows"], r.Warnings)
		}
		if len(seatsOf(t, m)) != 0 {
			t.Errorf("%s: a row without an identity became a seat: %v", name, seatsOf(t, m))
		}
	}
}

func TestSelfReportedAClusterWhoseRowsAllFailIsNotNoPresenceRow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminURL, leafURL := startLeafLikeServer(t, false)
	seedPartialHub(t, ctx, adminURL)
	putHubPresenceRaw(t, ctx, adminURL, otherKeyB, []byte("garbage"))
	putHubPresenceRaw(t, ctx, adminURL, "presence.other.supervisor.01M6CCCCCCCCCCCCCCCCCCCCCC", []byte(`{"agent_id":"x"}`))
	t.Setenv("DIRECTOR_CLUSTER", "mokuzai")

	reports, err := readUnreadGlobal(ctx, dialLeaf(t, leafURL), "global", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	m, r := selfReportedReport(t, reports, "GLOBAL_TO_other")
	if unreadableRowsOf(m) != 2 || !strings.Contains(warningsText(r), "2 presence rows unreadable") {
		t.Errorf("both rows are bad but unreadable_rows is %v, warnings %v", m["unreadable_rows"], r.Warnings)
	}
	var out strings.Builder
	printUnread(&out, r, unreadOpts{})
	if !strings.Contains(out.String(), "2 presence rows unreadable") {
		t.Errorf("the printed report does not say 2 presence rows unreadable: %q", out.String())
	}
	if strings.Contains(out.String(), "no presence row") {
		t.Errorf("a cluster whose rows all fail is printed as having no presence row: %q", out.String())
	}
}

func TestSelfReportedOneBadRowBesideAGoodOne(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminURL, leafURL := startLeafLikeServer(t, false)
	seedPartialHub(t, ctx, adminURL)
	putHubPresence(t, ctx, adminURL, "other", "other-supervisor", partialInstB, map[string]any{"oldest_pending_s": 40})
	putHubPresenceRaw(t, ctx, adminURL, "presence.other.supervisor.01M6CCCCCCCCCCCCCCCCCCCCCC", []byte("garbage"))
	t.Setenv("DIRECTOR_CLUSTER", "mokuzai")

	reports, err := readUnreadGlobal(ctx, dialLeaf(t, leafURL), "global", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	m, r := selfReportedReport(t, reports, "GLOBAL_TO_other")
	if len(seatsOf(t, m)) != 1 {
		t.Errorf("the good row is not a seat: %v", m)
	}
	if unreadableRowsOf(m) != 1 || !strings.Contains(warningsText(r), "1 presence row unreadable") {
		t.Errorf("the bad row beside it is not counted and named: unreadable_rows %v, warnings %v", m["unreadable_rows"], r.Warnings)
	}
}

func TestSelfReportedAllRowsReadableCountsNoneUnreadable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminURL, leafURL := startLeafLikeServer(t, false)
	seedPartialHub(t, ctx, adminURL)
	t.Setenv("DIRECTOR_CLUSTER", "mokuzai")

	reports, err := readUnreadGlobal(ctx, dialLeaf(t, leafURL), "global", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	m, r := selfReportedReport(t, reports, "GLOBAL_TO_other")
	if _, has := m["unreadable_rows"]; has || strings.Contains(warningsText(r), "unreadable") {
		t.Errorf("a cluster whose rows all read is reported with unreadable rows: %v %v", m, r.Warnings)
	}
}

// I-467 live fallback: the way a script runs the command. freebusy calls
// `director-mcp unread --json --global` from the caller's shell, where the
// session's DIRECTOR_CLUSTER (and the other DIRECTOR_GLOBAL_* variables) are not
// set: director-session hands them to the MCP server's env, not to the shell. The
// leaf has no list verbs. These tests go through runUnreadCmd, which main calls,
// with those variables empty, and set nothing else for the read.

func clearGlobalEnv(t *testing.T) {
	t.Helper()
	// freebusy's shell carries none of a seat's variables, so a test run from a seat
	// must not see them either: every DIRECTOR_* and MARVEL_* variable and NATS_URL
	// is unset (restored when the test ends).
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "DIRECTOR_") || strings.HasPrefix(k, "MARVEL_") || k == "NATS_URL" {
			t.Setenv(k, "") // registers the restore
			_ = os.Unsetenv(k)
		}
	}
}

func TestUnreadGlobalAsAScriptRunsItFallsBackToPresence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminURL, leafURL := startLeafLikeServer(t, false)
	seedPartialHub(t, ctx, adminURL)
	putHubPresence(t, ctx, adminURL, "other", "other-supervisor", partialInstB, map[string]any{"oldest_pending_s": 900})
	putHubPresence(t, ctx, adminURL, "mokuzai", "mokuzai-supervisor", partialInstA, map[string]any{"oldest_pending_s": 12})
	clearGlobalEnv(t)

	var out, errw strings.Builder
	runUnreadCmd(ctx, leafURL, cliArgs{global: true, json: true}, "global", &out, &errw)
	var g struct {
		Error   string           `json:"error"`
		Reports []map[string]any `json:"reports"`
	}
	if err := json.Unmarshal([]byte(out.String()), &g); err != nil {
		t.Fatalf("the output is not JSON: %v: %q", err, out.String())
	}
	if g.Error != "" {
		t.Fatalf("unread --json --global reported an error where the presence fallback can answer: %s", g.Error)
	}
	byStream := map[string]map[string]any{}
	for _, r := range g.Reports {
		byStream[r["stream"].(string)] = r
	}
	for _, c := range []string{"GLOBAL_TO_other", "GLOBAL_TO_mokuzai"} {
		r, ok := byStream[c]
		if !ok {
			t.Errorf("no report for %s (reports: %v)", c, g.Reports)
			continue
		}
		if r["self_reported"] != true {
			t.Errorf("%s is not marked self-reported though nothing was read from its stream: %v", c, r)
		}
	}
	for _, c := range []string{"GLOBAL_TO_other", "GLOBAL_TO_mokuzai"} {
		if w := strings.Join(strSlice(byStream[c]["warnings"]), "\n"); !strings.Contains(w, "DIRECTOR_CLUSTER is unset, so no stream was read by name") || !strings.Contains(w, "every cluster, this one included, is self-reported") {
			t.Errorf("%s does not say in plain words that no stream was read by name because DIRECTOR_CLUSTER is unset: %q", c, w)
		}
	}
	seats, _ := byStream["GLOBAL_TO_other"]["self_reported_seats"].([]any)
	if len(seats) != 1 {
		t.Fatalf("GLOBAL_TO_other seats = %v, want other-supervisor with its 900 s age", seats)
	}
	if seat, _ := seats[0].(map[string]any); seat["oldest_pending_s"] != float64(900) || seat["over_bound"] != true {
		t.Errorf("the other cluster's seat = %v, want oldest_pending_s 900 and over_bound", seat)
	}
	mseats, _ := byStream["GLOBAL_TO_mokuzai"]["self_reported_seats"].([]any)
	if seat := firstSeat(mseats); seat["oldest_pending_s"] != float64(12) {
		t.Errorf("the own cluster's seat = %v, want oldest_pending_s 12 (self-reported like any other)", seat)
	}
}

func firstSeat(seats []any) map[string]any {
	if len(seats) == 0 {
		return map[string]any{}
	}
	m, _ := seats[0].(map[string]any)
	return m
}

func strSlice(v any) []string {
	var out []string
	raw, _ := v.([]any)
	for _, x := range raw {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func TestUnreadGlobalAsAScriptRunsItPrintsTheFallbackNotAnError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminURL, leafURL := startLeafLikeServer(t, false)
	seedPartialHub(t, ctx, adminURL)
	clearGlobalEnv(t)

	var out, errw strings.Builder
	runUnreadCmd(ctx, leafURL, cliArgs{global: true}, "global", &out, &errw)
	if strings.Contains(out.String(), "global unread not read") {
		t.Errorf("the text read printed the error where the fallback can answer: %q", out.String())
	}
	for _, c := range []string{"GLOBAL_TO_other SELF-REPORTED", "GLOBAL_TO_mokuzai SELF-REPORTED"} {
		if !strings.Contains(out.String(), c) {
			t.Errorf("the text read did not print %q: %q", c, out.String())
		}
	}
	if !strings.Contains(out.String(), "DIRECTOR_CLUSTER is unset, so no stream was read by name") {
		t.Errorf("the text read does not say no stream was read by name because DIRECTOR_CLUSTER is unset: %q", out.String())
	}
}

// With no cluster named and no presence row readable at all, the read still says
// the credential may not list streams: the fallback does not hide that.
func TestUnreadGlobalAsAScriptRunsItKeepsTheErrorWhenPresenceIsEmpty(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminURL, leafURL := startLeafLikeServer(t, false)
	nc, _ := provision(t, ctx, adminURL) // the hub and its buckets, with no seat and no presence row
	_ = nc
	clearGlobalEnv(t)

	var out, errw strings.Builder
	runUnreadCmd(ctx, leafURL, cliArgs{global: true}, "global", &out, &errw)
	if !strings.Contains(out.String(), "may not list streams") {
		t.Errorf("with no presence row the read should keep the list-verb error, got %q", out.String())
	}
}
