package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// audit_lookup (sim/design/audit-lookup.md, section 5, tests 1 to 7). These
// run against a scratch broker, as audit_refusal_test.go does, and skip
// without nats-server.

const lookupSeqText = "audit_lookup: seq must be a whole number of 1 or more"

const lookupUnsetText = "audit_lookup: this session names no usable cluster (DIRECTOR_CLUSTER is unset or not a valid token), so a number cannot be tied to a log"

func lookupArgs(t *testing.T, args map[string]any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// closedJS is a JetStream handle whose connection is already closed, so any
// broker read through it fails with a connection error, never with a text the
// lookup builds before reading.
func closedJS(t *testing.T, url string) jetstream.JetStream {
	t.Helper()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	nc.Close()
	return js
}

func publishAudit(t *testing.T, ctx context.Context, js jetstream.JetStream, msgID string, body []byte) uint64 {
	t.Helper()
	m := &nats.Msg{Subject: "agent.audit", Data: body, Header: nats.Header{}}
	if msgID != "" {
		m.Header.Set(jetstream.MsgIDHeader, msgID)
	}
	ack, err := js.PublishMsg(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	return ack.Sequence
}

// unsetEnv unsets key for the test and restores it after.
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
}

// Test 1: a refused send, then the lookup on the seq its result names.
func TestAuditLookupReturnsARefusedSend(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	t.Setenv("DIRECTOR_CLUSTER", "c1")
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	createAudit(t, ctx, js)
	bus, err := connect(ctx, url, Sender{AgentID: "sup", Workspace: "w", Team: "t"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bus.close()

	raw, _ := json.Marshal(map[string]any{"to": "agent://t/nobody", "performative": "REQUEST", "text": "are you there"})
	_, err = toolSend(ctx, bus, raw)
	if err == nil {
		t.Fatal("send to a seat with no presence was accepted")
	}
	m := recordedSeq.FindStringSubmatch(err.Error())
	if m == nil {
		t.Fatalf("refusal does not report the audit record: %v", err)
	}
	seq, _ := strconv.ParseUint(m[1], 10, 64)

	out, err := auditLookup(ctx, js, "c1", lookupArgs(t, map[string]any{"cluster": "c1", "seq": seq}))
	if err != nil {
		t.Fatalf("lookup of seq %d: %v", seq, err)
	}
	if out["outcome"] != "refused" {
		t.Errorf("outcome = %v, want refused", out["outcome"])
	}
	if out["stage"] != "resolve" {
		t.Errorf("stage = %v, want resolve", out["stage"])
	}
	if r, _ := out["refusal"].(string); !strings.Contains(r, "R-92") {
		t.Errorf("refusal = %v, want the R-92 text", out["refusal"])
	}
	if out["cluster"] != "c1" || out["subject"] != "agent.audit" || out["seq"] != seq {
		t.Errorf("cluster, subject, seq = %v, %v, %v", out["cluster"], out["subject"], out["seq"])
	}
	if s, _ := out["stream_created"].(string); s == "" {
		t.Errorf("stream_created missing: %v", out)
	}
	if s, _ := out["time"].(string); s == "" {
		t.Errorf("time missing: %v", out)
	}
	envRaw, ok := out["envelope"].(json.RawMessage)
	if !ok {
		t.Fatalf("no envelope in %v", out)
	}
	var e Envelope
	if err := json.Unmarshal(envRaw, &e); err != nil {
		t.Fatal(err)
	}
	if e.MessageID == "" || e.Recipient.Address != "agent://t/nobody" || e.Content.Data != "are you there" {
		t.Errorf("envelope incomplete: %s", envRaw)
	}
	if out["msg_id"] != e.MessageID+"-refused" {
		t.Errorf("msg_id = %v, want %s-refused", out["msg_id"], e.MessageID)
	}
	// The envelope's message_id binds the refused record, with or without the suffix.
	for _, id := range []string{e.MessageID, e.MessageID + "-refused"} {
		if _, err := auditLookup(ctx, js, "c1", lookupArgs(t, map[string]any{"cluster": "c1", "seq": seq, "message_id": id})); err != nil {
			t.Errorf("message_id %s: %v", id, err)
		}
	}
}

// Test 2: a successful send's mirror reads as sent, with no stage or refusal.
func TestAuditLookupReturnsASentMirror(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	audit := createAudit(t, ctx, js)
	sender := liveSeatNoCleanup(t, ctx, url, Sender{AgentID: "sup", Workspace: "w", Team: "t"})
	liveSeatNoCleanup(t, ctx, url, Sender{AgentID: "a", Workspace: "w", Team: "t"})

	raw, _ := json.Marshal(map[string]any{"to": "agent://t/a", "performative": "INFORM", "text": "hi"})
	res, err := toolSend(ctx, sender, raw)
	if err != nil {
		t.Fatal(err)
	}
	mid := res.(map[string]any)["message_id"].(string)
	info, _ := audit.Info(ctx)
	seq := info.State.LastSeq

	out, err := auditLookup(ctx, js, "c1", lookupArgs(t, map[string]any{"cluster": "c1", "seq": seq, "message_id": mid}))
	if err != nil {
		t.Fatal(err)
	}
	if out["outcome"] != "sent" {
		t.Errorf("outcome = %v, want sent", out["outcome"])
	}
	for _, k := range []string{"stage", "refusal"} {
		if _, ok := out[k]; ok {
			t.Errorf("a sent record carries %s: %v", k, out[k])
		}
	}
	if out["msg_id"] != mid {
		t.Errorf("msg_id = %v, want %s", out["msg_id"], mid)
	}
}

// Test 3: a bad seq is refused before any broker read.
func TestAuditLookupRefusesABadSeqBeforeReading(t *testing.T) {
	url := startScratchServer(t)
	js := closedJS(t, url)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, args := range []string{
		`{"cluster":"c1","seq":0}`,
		`{"cluster":"c1","seq":-1}`,
		`{"cluster":"c1","seq":1.5}`,
		`{"cluster":"c1","seq":"5"}`,
		`{"cluster":"c1"}`,
		`{"cluster":"c1","seq":null}`,
		`[]`,
	} {
		out, err := auditLookup(ctx, js, "c1", json.RawMessage(args))
		if err == nil || err.Error() != lookupSeqText {
			t.Errorf("%s: got %v, %v; want %q", args, out, err, lookupSeqText)
		}
	}
}

// Test 4: a seq past the last, and one below the first, name the stream's range.
func TestAuditLookupOutsideTheRange(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	audit := createAudit(t, ctx, js)
	for i := 0; i < 4; i++ {
		publishAudit(t, ctx, js, fmt.Sprintf("m%d", i), []byte(`{}`))
	}
	_, err := auditLookup(ctx, js, "c1", lookupArgs(t, map[string]any{"cluster": "c1", "seq": 9}))
	want := "audit_lookup: seq 9 on c1 is past the last record, 4; check the number came from an audit line for this cluster"
	if err == nil || err.Error() != want {
		t.Errorf("past the end: got %v, want %q", err, want)
	}
	if err := audit.Purge(ctx, jetstream.WithPurgeSequence(3)); err != nil {
		t.Fatal(err)
	}
	_, err = auditLookup(ctx, js, "c1", lookupArgs(t, map[string]any{"cluster": "c1", "seq": 1}))
	want = "audit_lookup: seq 1 on c1 has aged out; the stream holds 3 to 4"
	if err == nil || err.Error() != want {
		t.Errorf("below the first: got %v, want %q", err, want)
	}
}

// Test 5: no stream, and an empty stream.
func TestAuditLookupNoStreamAndEmptyStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	args := lookupArgs(t, map[string]any{"cluster": "c1", "seq": 1})
	_, err := auditLookup(ctx, js, "c1", args)
	want := "audit_lookup: cluster c1 has no AGENT_AUDIT stream"
	if err == nil || err.Error() != want {
		t.Errorf("no stream: got %v, want %q", err, want)
	}
	createAudit(t, ctx, js)
	_, err = auditLookup(ctx, js, "c1", args)
	want = "audit_lookup: AGENT_AUDIT on c1 holds no records"
	if err == nil || err.Error() != want {
		t.Errorf("empty stream: got %v, want %q", err, want)
	}
}

// Test 5a: a record deleted from inside the range reads as deleted or purged.
func TestAuditLookupDeletedInsideTheRange(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	audit := createAudit(t, ctx, js)
	for i := 0; i < 3; i++ {
		publishAudit(t, ctx, js, fmt.Sprintf("m%d", i), []byte(`{}`))
	}
	if err := audit.DeleteMsg(ctx, 2); err != nil {
		t.Fatal(err)
	}
	_, err := auditLookup(ctx, js, "c1", lookupArgs(t, map[string]any{"cluster": "c1", "seq": 2}))
	want := "audit_lookup: seq 2 on c1 was deleted or purged; the stream holds 1 to 3 with gaps"
	if err == nil || err.Error() != want {
		t.Errorf("deleted: got %v, want %q", err, want)
	}
}

// Test 5b: the cluster binding, before any broker read, and where the
// session's cluster comes from.
func TestAuditLookupClusterBinding(t *testing.T) {
	url := startScratchServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dead := closedJS(t, url)
	for _, c := range []struct {
		own, args, want string
	}{
		{"c1", `{"cluster":"c2","seq":7}`, "audit_lookup: this session is on cluster c1, not c2; read it with marvel bus audit 7 --cluster c2"},
		{"c1", `{"seq":7}`, "audit_lookup: this session is on cluster c1, not ; read it with marvel bus audit 7 --cluster "},
		{"", `{"cluster":"c1","seq":7}`, lookupUnsetText},
	} {
		out, err := auditLookup(ctx, dead, c.own, json.RawMessage(c.args))
		if err == nil || err.Error() != c.want {
			t.Errorf("own %q %s: got %v, %v; want %q", c.own, c.args, out, err, c.want)
		}
	}

	// A worker seat: no global tier, DIRECTOR_CLUSTER set. The tool reads the
	// variable itself and answers.
	_, js := provision(t, ctx, url)
	createAudit(t, ctx, js)
	publishAudit(t, ctx, js, "m0", []byte(`{}`))
	unsetEnv(t, "DIRECTOR_GLOBAL_DOMAIN")
	t.Setenv("DIRECTOR_CLUSTER", "c1")
	bus, err := connect(ctx, url, Sender{AgentID: "w1", Workspace: "w", Team: "t"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bus.close()
	args := lookupArgs(t, map[string]any{"cluster": "c1", "seq": 1})
	out, err := dispatchTool(ctx, bus, "audit_lookup", args)
	if err != nil {
		t.Fatalf("worker seat lookup: %v", err)
	}
	if got := out.(map[string]any)["cluster"]; got != "c1" {
		t.Errorf("cluster = %v, want c1", got)
	}
	for _, bad := range []string{"c.1", "c*", "c 1"} {
		t.Setenv("DIRECTOR_CLUSTER", bad)
		_, err := dispatchTool(ctx, bus, "audit_lookup", lookupArgs(t, map[string]any{"cluster": bad, "seq": 1}))
		if err == nil || err.Error() != lookupUnsetText {
			t.Errorf("DIRECTOR_CLUSTER %q: got %v, want the unset text", bad, err)
		}
	}
	unsetEnv(t, "DIRECTOR_CLUSTER")
	_, err = dispatchTool(ctx, bus, "audit_lookup", args)
	if err == nil || err.Error() != lookupUnsetText {
		t.Errorf("DIRECTOR_CLUSTER unset: got %v, want the unset text", err)
	}
}

// Test 5c: a message_id that is not the record's returns no record fields.
func TestAuditLookupMessageIDMismatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	createAudit(t, ctx, js)
	seq := publishAudit(t, ctx, js, "m-right", []byte(`{}`))
	for _, id := range []string{"m-wrong", "m-righ", "m-right-refused-x"} {
		out, err := auditLookup(ctx, js, "c1", lookupArgs(t, map[string]any{"cluster": "c1", "seq": seq, "message_id": id}))
		want := fmt.Sprintf("audit_lookup: seq %d on c1 holds a different message", seq)
		if err == nil || err.Error() != want {
			t.Errorf("message_id %s: got %v, want %q", id, err, want)
		}
		if out != nil {
			t.Errorf("message_id %s: record fields returned: %v", id, out)
		}
	}
}

// Test 5d: the refusal text carries the cluster when DIRECTOR_CLUSTER is set.
func TestAuditRefusalNamesTheCluster(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	createAudit(t, ctx, js)
	bus, err := connect(ctx, url, Sender{AgentID: "sup", Workspace: "w", Team: "t"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bus.close()
	refuse := func() string {
		raw, _ := json.Marshal(map[string]any{"to": "agent://t/nobody", "performative": "INFORM", "text": "x"})
		_, err := toolSend(ctx, bus, raw)
		if err == nil {
			t.Fatal("send to a seat with no presence was accepted")
		}
		if recordedSeq.FindStringSubmatch(err.Error()) == nil {
			t.Fatalf("recordedSeq does not match: %v", err)
		}
		return err.Error()
	}
	t.Setenv("DIRECTOR_CLUSTER", "c1")
	if msg := refuse(); !strings.HasSuffix(msg, " on cluster c1") {
		t.Errorf("with DIRECTOR_CLUSTER set: %q", msg)
	}
	for _, bad := range []string{"c.1", ""} {
		t.Setenv("DIRECTOR_CLUSTER", bad)
		if msg := refuse(); strings.Contains(msg, "on cluster") {
			t.Errorf("DIRECTOR_CLUSTER %q should leave the text unchanged: %q", bad, msg)
		}
	}
	unsetEnv(t, "DIRECTOR_CLUSTER")
	if msg := refuse(); strings.Contains(msg, "on cluster") {
		t.Errorf("with DIRECTOR_CLUSTER unset: %q", msg)
	}
}

// Test 6: a lookup moves no consumer and no ack floor.
func TestAuditLookupMovesNoConsumer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	audit := createAudit(t, ctx, js)
	sender := liveSeatNoCleanup(t, ctx, url, Sender{AgentID: "sup", Workspace: "w", Team: "t"})
	recv := liveSeatNoCleanup(t, ctx, url, Sender{AgentID: "a", Workspace: "w", Team: "t"})
	for _, text := range []string{"one", "two"} {
		raw, _ := json.Marshal(map[string]any{"to": "agent://t/a", "performative": "INFORM", "text": text})
		if _, err := toolSend(ctx, sender, raw); err != nil {
			t.Fatal(err)
		}
	}
	// One of the two is received and acked, so the floor sits between them.
	if _, err := toolWait(ctx, recv, json.RawMessage(`{"timeout_seconds":5}`)); err != nil {
		t.Fatal(err)
	}

	type snap struct {
		auditConsumers int
		auditMsgs      uint64
		floors         map[string]string
	}
	take := func() snap {
		ai, err := audit.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		s := snap{auditConsumers: ai.State.Consumers, auditMsgs: ai.State.Msgs, floors: map[string]string{}}
		inbox, err := js.Stream(ctx, "AGENT_INBOX")
		if err != nil {
			t.Fatal(err)
		}
		l := inbox.ListConsumers(ctx)
		for ci := range l.Info() {
			s.floors[ci.Name] = fmt.Sprintf("ack %d/%d delivered %d/%d pending %d acking %d",
				ci.AckFloor.Consumer, ci.AckFloor.Stream, ci.Delivered.Consumer, ci.Delivered.Stream, ci.NumPending, ci.NumAckPending)
		}
		if l.Err() != nil {
			t.Fatal(l.Err())
		}
		return s
	}
	before := take()
	if len(before.floors) == 0 {
		t.Fatal("no inbox consumer to compare")
	}
	ai, _ := audit.Info(ctx)
	for seq := ai.State.FirstSeq; seq <= ai.State.LastSeq; seq++ {
		if _, err := auditLookup(ctx, js, "c1", lookupArgs(t, map[string]any{"cluster": "c1", "seq": seq})); err != nil {
			t.Fatal(err)
		}
	}
	after := take()
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Errorf("a lookup changed consumer state:\nbefore %v\n after %v", before, after)
	}
}

// Test 7: a body that is not an envelope comes back as raw, unchanged.
func TestAuditLookupReturnsANonEnvelopeBodyAsRaw(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	url := startScratchServer(t)
	_, js := provision(t, ctx, url)
	createAudit(t, ctx, js)
	for _, body := range []string{
		"not json {",
		`{"sender":{"agent_id":"sup","workspace":"w","principal":null},"raw_arguments":"{\"to\": 7}"}`,
		`[1,2]`,
		`{"schema_version":1,"message_id":"m1","not_an_envelope_field":true}`,
		`{"schema_version":1,"message_id":"m1"} trailing`,
	} {
		seq := publishAudit(t, ctx, js, "", []byte(body))
		out, err := auditLookup(ctx, js, "c1", lookupArgs(t, map[string]any{"cluster": "c1", "seq": seq}))
		if err != nil {
			t.Fatal(err)
		}
		if out["raw"] != body {
			t.Errorf("raw = %#v, want %#v", out["raw"], body)
		}
		if _, ok := out["envelope"]; ok {
			t.Errorf("%q came back as an envelope", body)
		}
	}
}
