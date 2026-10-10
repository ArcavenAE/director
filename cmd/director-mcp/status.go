package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// statusArgs are the report_status arguments. The status message is the
// ask ledger's D1(b): an INFORM signal in reply to the ask, whose data the
// reader's isStatus parses (working, or blocked-on <address or ref>).
type statusArgs struct {
	To        string
	InReplyTo string `json:"in_reply_to"`
	Status    string
	On        string
}

// buildStatusEnvelope validates report_status args and builds the INFORM the
// ask reader reads. It refuses what isStatus would silently ignore, so a
// status that cannot move a row never goes on the bus.
func buildStatusEnvelope(self Sender, raw json.RawMessage) (*Envelope, string, error) {
	var a statusArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(a.To) == "" {
		return nil, "", errors.New("to required: the sender of the ask")
	}
	if strings.TrimSpace(a.InReplyTo) == "" {
		return nil, "", errors.New("in_reply_to required: the message_id of the ask")
	}
	on := strings.TrimSpace(a.On)
	var data string
	switch a.Status {
	case "working":
		if on != "" {
			return nil, "", errors.New("on is for blocked-on only")
		}
		data = "working"
	case "blocked-on":
		if on == "" {
			return nil, "", errors.New("status blocked-on needs on: the address or ref you are waiting on")
		}
		if strings.ContainsAny(on, "\r\n") {
			return nil, "", errors.New("on must be one line")
		}
		data = "blocked-on " + on
	default:
		return nil, "", errors.New("status must be working or blocked-on")
	}
	e := newEnvelope(self)
	e.Recipient.Address = a.To
	e.Performative = "INFORM"
	e.Content = Content{Type: "signal", Data: data}
	e.InReplyTo = a.InReplyTo
	e.CorrelationID = a.InReplyTo
	if err := e.validate(); err != nil {
		return e, "", atStage("validate", err)
	}
	return e, "", nil
}

func toolReportStatus(ctx context.Context, bus *Bus, raw json.RawMessage) (any, error) {
	e, wsHint, err := buildStatusEnvelope(bus.self, raw)
	if err != nil {
		if e == nil {
			return nil, bus.refuseArgs(ctx, raw, err)
		}
		return nil, bus.refuseEnvelope(ctx, e, "validate", err)
	}
	res, err := bus.publish(ctx, e, wsHint)
	if err != nil {
		return nil, bus.refuseEnvelope(ctx, e, "publish", err)
	}
	return map[string]any{
		"status":     "accepted for delivery",
		"message_id": e.MessageID,
		"tier":       res.Tier,
		"note":       "accepted is not delivered or read; the ask ledger shows the row state once the reader has seen it (R-08)",
	}, nil
}
