package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

var statusSelf = Sender{AgentID: "builder-1", Team: "ops", Workspace: "w"}

func statusEnv(t *testing.T, args string) *Envelope {
	t.Helper()
	e, _, err := buildStatusEnvelope(statusSelf, json.RawMessage(args))
	if err != nil {
		t.Fatalf("buildStatusEnvelope(%s): %v", args, err)
	}
	return e
}

func TestReportStatusIsInCatalog(t *testing.T) {
	found := false
	for _, td := range toolCatalog(nil, false) {
		if td.Name == "report_status" {
			found = true
			req, _ := td.InputSchema["required"].([]string)
			want := map[string]bool{"to": true, "in_reply_to": true, "status": true}
			for _, k := range req {
				delete(want, k)
			}
			if len(want) != 0 {
				t.Fatalf("report_status does not require %v", want)
			}
		}
	}
	if !found {
		t.Fatal("report_status missing from toolCatalog")
	}
}

func TestReportStatusWorkingBuildsSignalInform(t *testing.T) {
	e := statusEnv(t, `{"to":"agent://ops/sup-1","in_reply_to":"m1","status":"working"}`)
	if e.Performative != "INFORM" || e.Content.Type != "signal" || e.Content.Data != "working" {
		t.Fatalf("envelope = %s %s %q, want INFORM signal working", e.Performative, e.Content.Type, e.Content.Data)
	}
	if e.InReplyTo != "m1" || e.CorrelationID != "m1" {
		t.Fatalf("in_reply_to %q correlation %q, want m1 both", e.InReplyTo, e.CorrelationID)
	}
	if e.Recipient.Address != "agent://ops/sup-1" {
		t.Fatalf("recipient = %q", e.Recipient.Address)
	}
}

func TestReportStatusBlockedOnCarriesTarget(t *testing.T) {
	e := statusEnv(t, `{"to":"agent://ops/sup-1","in_reply_to":"m1","status":"blocked-on","on":"agent://ops/architect-1"}`)
	if e.Content.Data != "blocked-on agent://ops/architect-1" {
		t.Fatalf("data = %q", e.Content.Data)
	}
}

func TestReportStatusRefusesBadInput(t *testing.T) {
	for name, args := range map[string]string{
		"no in_reply_to":        `{"to":"agent://ops/sup-1","status":"working"}`,
		"no to":                 `{"in_reply_to":"m1","status":"working"}`,
		"unknown status":        `{"to":"agent://ops/sup-1","in_reply_to":"m1","status":"done"}`,
		"empty status":          `{"to":"agent://ops/sup-1","in_reply_to":"m1","status":""}`,
		"blocked-on without on": `{"to":"agent://ops/sup-1","in_reply_to":"m1","status":"blocked-on"}`,
		"blocked-on blank on":   `{"to":"agent://ops/sup-1","in_reply_to":"m1","status":"blocked-on","on":"  "}`,
		"working with on":       `{"to":"agent://ops/sup-1","in_reply_to":"m1","status":"working","on":"x"}`,
		"on with a newline":     `{"to":"agent://ops/sup-1","in_reply_to":"m1","status":"blocked-on","on":"a\nb"}`,
	} {
		if _, _, err := buildStatusEnvelope(statusSelf, json.RawMessage(args)); err == nil {
			t.Errorf("%s: accepted, want refusal", name)
		}
	}
}

// What the tool sends is what the reader reads: the envelope built by the
// shim moves a row through working and blocked with no reader change.
func TestReportStatusEnvelopeMovesTheRow(t *testing.T) {
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	put := func(e *Envelope, id string, m int) auditRec {
		e.MessageID = id
		e.Sender = Sender{AgentID: "builder-1", Workspace: "w"}
		return auditRec{Broker: "local", Seq: uint64(100 + m), At: at(m), Env: *e}
	}
	l := ledgerWith(
		rec(t0, "m1", "REQUEST", "sup-1", owner1, "build x"),
		rec(at(1), "a1", "AGREE", "builder-1", "agent://ops/sup-1", "ok", inReplyTo("m1")),
	)
	l.Ingest(put(statusEnv(t, `{"to":"agent://ops/sup-1","in_reply_to":"m1","status":"working"}`), "s1", 2))
	resolved(l, at(3), builderObs())
	if got := rowOf(t, l, "m1").State; got != askWorking {
		t.Fatalf("after working status state = %s, want working", got)
	}
	l.Ingest(put(statusEnv(t, `{"to":"agent://ops/sup-1","in_reply_to":"m1","status":"blocked-on","on":"agent://ops/architect-1"}`), "s2", 4))
	resolved(l, at(5), builderObs())
	r := rowOf(t, l, "m1")
	if r.State != askBlocked || r.BlockedOn != "agent://ops/architect-1" {
		t.Fatalf("row = %s blocked_on %q, want blocked on the architect", r.State, r.BlockedOn)
	}
}

// The catalog lists the tool, but a seat only gets an answer if dispatchTool
// routes the name. This goes through dispatchTool on a scratch broker and
// reads the INFORM back from the asker's inbox.
func TestReportStatusDispatchesAndReachesTheAsker(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	provision(t, ctx, url)
	asker := roleBus(t, ctx, url, "sup-1", "supervisor")
	worker := roleBus(t, ctx, url, "builder-1", "builder")

	raw := json.RawMessage(`{"to":"agent://ops/sup-1","in_reply_to":"m1","status":"blocked-on","on":"global://director"}`)
	out, err := dispatchTool(ctx, worker, "report_status", raw)
	if err != nil {
		t.Fatalf("dispatchTool report_status: %v", err)
	}
	if res, _ := out.(map[string]any); res["message_id"] == nil {
		t.Fatalf("result has no message_id: %v", out)
	}
	e, _, err := asker.receive(ctx, 3*time.Second)
	if err != nil || e == nil {
		t.Fatalf("asker got %v, %v; want the status INFORM", e, err)
	}
	if e.Performative != "INFORM" || e.Content.Type != "signal" || e.Content.Data != "blocked-on global://director" || e.InReplyTo != "m1" {
		t.Fatalf("asker got %s %s %q in_reply_to %q", e.Performative, e.Content.Type, e.Content.Data, e.InReplyTo)
	}
}
