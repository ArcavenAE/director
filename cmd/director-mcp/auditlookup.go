package main

// audit_lookup reads one record of this cluster's AGENT_AUDIT stream by its
// sequence number (sim/design/audit-lookup.md). A refused send reports
// "audit: recorded agent.audit seq N on cluster <name>"; this is the call
// that reads that record back. It uses Stream.GetMsg, as the ask reader does,
// so it creates no consumer and moves no ack floor. It never publishes, acks,
// deletes or writes a file.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// auditLookupStream is a constant: the call takes no stream, subject or
// filter, so it cannot be pointed at an inbox.
const auditLookupStream = "AGENT_AUDIT"

// auditLookupTimeout bounds the whole read.
const auditLookupTimeout = 10 * time.Second

// sessionCluster is this session's DIRECTOR_CLUSTER when it is a valid subject
// token, else "". It is read here, not from globalConfig, because a worker
// seat has no global config (loadGlobalConfig returns nil without
// DIRECTOR_GLOBAL_DOMAIN) and still has a cluster.
func sessionCluster() string {
	c := os.Getenv("DIRECTOR_CLUSTER")
	if validToken("DIRECTOR_CLUSTER", c) != nil {
		return ""
	}
	return c
}

func toolAuditLookup(ctx context.Context, bus *Bus, raw json.RawMessage) (any, error) {
	return auditLookup(ctx, bus.js, sessionCluster(), raw)
}

// auditLookup returns one AGENT_AUDIT record. own is this session's cluster,
// "" when it has none usable. Every argument is checked before the broker is
// asked.
func auditLookup(ctx context.Context, js jetstream.JetStream, own string, raw json.RawMessage) (map[string]any, error) {
	var args map[string]json.RawMessage
	if err := json.Unmarshal(raw, &args); err != nil {
		args = nil
	}
	seq, err := strconv.ParseUint(string(bytes.TrimSpace(args["seq"])), 10, 64)
	if err != nil || seq < 1 {
		return nil, errors.New("audit_lookup: seq must be a whole number of 1 or more")
	}
	if own == "" {
		return nil, errors.New("audit_lookup: this session names no usable cluster (DIRECTOR_CLUSTER is unset or not a valid token), so a number cannot be tied to a log")
	}
	var given string
	if c, ok := args["cluster"]; ok && json.Unmarshal(c, &given) != nil {
		given = string(c)
	}
	if given != own {
		return nil, fmt.Errorf("audit_lookup: this session is on cluster %s, not %s; read it with marvel bus audit %d --cluster %s", own, given, seq, given)
	}
	var msgID string
	if m, ok := args["message_id"]; ok {
		_ = json.Unmarshal(m, &msgID)
	}

	ctx, cancel := context.WithTimeout(ctx, auditLookupTimeout)
	defer cancel()
	brokerErr := func(err error) error {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, nats.ErrTimeout) {
			return fmt.Errorf("audit_lookup: no answer from the broker in %s", auditLookupTimeout)
		}
		return fmt.Errorf("audit_lookup: the broker refused the read: %v", err)
	}

	s, err := js.Stream(ctx, auditLookupStream)
	if errors.Is(err, jetstream.ErrStreamNotFound) {
		return nil, fmt.Errorf("audit_lookup: cluster %s has no AGENT_AUDIT stream", own)
	}
	if err != nil {
		return nil, brokerErr(err)
	}
	info := s.CachedInfo()
	st := info.State
	if st.Msgs == 0 {
		return nil, fmt.Errorf("audit_lookup: AGENT_AUDIT on %s holds no records", own)
	}
	if seq < st.FirstSeq {
		return nil, fmt.Errorf("audit_lookup: seq %d on %s has aged out; the stream holds %d to %d", seq, own, st.FirstSeq, st.LastSeq)
	}
	if seq > st.LastSeq {
		return nil, fmt.Errorf("audit_lookup: seq %d on %s is past the last record, %d; check the number came from an audit line for this cluster", seq, own, st.LastSeq)
	}
	msg, err := s.GetMsg(ctx, seq)
	if errors.Is(err, jetstream.ErrMsgNotFound) {
		return nil, fmt.Errorf("audit_lookup: seq %d on %s was deleted or purged; the stream holds %d to %d with gaps", seq, own, st.FirstSeq, st.LastSeq)
	}
	if err != nil {
		return nil, brokerErr(err)
	}
	got := msg.Header.Get(jetstream.MsgIDHeader)
	if msgID != "" && got != msgID && got != msgID+"-refused" {
		return nil, fmt.Errorf("audit_lookup: seq %d on %s holds a different message", seq, own)
	}

	out := map[string]any{
		"cluster":        own,
		"stream_created": info.Created.UTC().Format(time.RFC3339Nano),
		"seq":            msg.Sequence,
		"time":           msg.Time.UTC().Format(time.RFC3339Nano),
		"subject":        msg.Subject,
		"outcome":        "sent",
		"msg_id":         got,
	}
	if msg.Header.Get("Director-Outcome") == "refused" {
		out["outcome"] = "refused"
		out["stage"] = msg.Header.Get("Director-Stage")
		out["refusal"] = msg.Header.Get("Director-Refusal")
	}
	if isEnvelope(msg.Data) {
		out["envelope"] = json.RawMessage(msg.Data)
	} else {
		out["raw"] = string(msg.Data)
	}
	return out, nil
}

// isEnvelope reports whether body is one JSON object of the envelope's fields
// and no others, with a message_id. It is returned as stored, not re-encoded.
func isEnvelope(body []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var e Envelope
	if dec.Decode(&e) != nil || e.MessageID == "" {
		return false
	}
	_, err := dec.Token()
	return errors.Is(err, io.EOF)
}
