package main

// The tool catalog and dispatch. Six tools: the five from the spbc ticket
// (send_message, wait_for_message, list_roster, set_presence, broadcast) and
// inbox_summary. Each is request/response; wait_for_message is the long-poll
// that answers the push-vs-poll question, and its max argument drains a
// backlog in one call (drain.go).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/oklog/ulid/v2"
)

// toolCatalog describes the tools this session actually has. The global forms
// appear only when the launcher turned the global tier on, so a session with
// no hub is never told about addresses it cannot route (gcfg nil = off).
func toolCatalog(gcfg *globalConfig, cueOn bool) []toolDef {
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	toDesc := "recipient address: agent://{team}/{id}, role://{team}/{role}, or broadcast://{workspace}[/{team}]"
	sendDesc := "Send a director envelope to another session or role. Returns accepted-for-delivery with a message_id; this is a send acknowledgement, not a delivery or read receipt."
	waitDesc := "Block up to timeout_seconds for the next message addressed to this session, then return it. This is a poll: the model must call it. The server cannot push a message into context on its own. With max greater than 1 it returns every waiting message up to max in one call, oldest first, and consumes them as the single form does; run inbox_summary first to see what is waiting without consuming it."
	summaryDesc := "Summarize the messages waiting for this session without consuming any: counts by sender and by performative, the REQUEST, FAILURE and QUERY messages and any that set reply_by, say they hold custody, or await a reply, and the waiting sequence numbers oldest first. Acks nothing; drain in order afterwards with wait_for_message max=N."
	rosterDesc := "List the sessions currently present, from the presence store. Absence means silence, not a negative report."
	if gcfg != nil {
		toDesc += ", or across hosts global://director and global://{cluster}/supervisor. A global send is refused before publish when nobody is live at that address."
		sendDesc += " This session is " + gcfg.selfAddress() + " at the global tier: reply to a global message with global://director, and name a cluster (list_roster shows them) to reach its supervisor."
		waitDesc += " It polls the local inbox and this session's global inbox, and the result names the tier the message came from. A batch is listed local first, then global, each tier oldest first, and its budget is split by turn between the tiers, so the list order is not a delivery priority; sequence numbers order messages within a tier only."
		summaryDesc += " Covers both the local inbox and this session's global inbox."
		rosterDesc += " Rows from both tiers are merged and carry a tier column; global rows carry the cluster and role that address them."
	}
	tools := []toolDef{
		{
			Name:        "send_message",
			Description: sendDesc,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"to":           str(toDesc),
					"performative": str("one of INFORM REQUEST AGREE REFUSE FAILURE CFP PROPOSE ACCEPT-PROPOSAL REJECT-PROPOSAL CANCEL QUERY NOT-UNDERSTOOD"),
					"text":         str("the message body"),
					"refs":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "pointers: bd:, finding:, file:, url:, pr:"},
					"in_reply_to":  str("optional message_id being answered"),
					"reply_by":     str("optional RFC3339 deadline"),
					"workspace":    str("optional recipient workspace. Omit and it is resolved from the recipient's live presence; a send to a recipient with no live presence is then refused, not silently misdelivered. Set it to address a known cold mailbox (no live presence) verbatim. Ignored for a global:// address, whose subject carries no workspace."),
				},
				"required": []string{"to", "performative", "text"},
			},
		},
		{
			Name:        "wait_for_message",
			Description: waitDesc,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"timeout_seconds": map[string]any{"type": "integer", "description": "how long to wait when nothing is already waiting (default 30, max 120)"},
					"max":             map[string]any{"type": "integer", "description": "most messages to return in one call (default 1, max 50). Above 1, waiting messages return at once, oldest first."},
				},
			},
		},
		{
			Name:        "inbox_summary",
			Description: summaryDesc,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit": map[string]any{"type": "integer", "description": "most waiting messages to read per tier (default 200, max 500); totals past it still come from consumer state"},
				},
			},
		},
		{
			Name:        "list_roster",
			Description: rosterDesc,
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			Name:        "set_presence",
			Description: "Record this session's presence heartbeat (state, e.g. idle or busy). Must be repeated within the presence window or the entry expires.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"state": str("idle | busy | away")},
				"required":   []string{"state"},
			},
		},
		{
			Name:        "report_status",
			Description: "Tell the sender of an ask that you are working on it, or blocked on someone. One INFORM signal in reply to the ask, so the ask ledger can show working and blocked-on instead of silence. Send it when you start work on a request and again if you stop on someone else.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"to":          str("the address of whoever sent the ask"),
					"in_reply_to": str("the message_id of the ask"),
					"status":      str("working | blocked-on"),
					"on":          str("for blocked-on only: the agent://, role:// or global:// address, or the ref (pr:, bd:, finding:), you are waiting on, as written on the ask"),
				},
				"required": []string{"to", "in_reply_to", "status"},
			},
		},
		{
			Name:        "broadcast",
			Description: "Send an INFORM to every seat live in the workspace or a team: one durable send per seat found in presence, excluding you. Returns recipients:N and refuses when N is 0. A seat that joins after the call is not included.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"team":      str("optional team; omit for whole workspace"),
					"text":      str("the message body"),
					"workspace": str("optional target workspace; omit to broadcast to your own"),
				},
				"required": []string{"text"},
			},
		},
	}
	if cueOn {
		// The self-test proof (channel-cue.md 3.3): only a model that received a
		// cue knows its nonce. Offered only to a seat that opted in.
		for i := range tools {
			if tools[i].Name == "inbox_summary" {
				tools[i].InputSchema["properties"].(map[string]any)["cue_ack"] = map[string]any{"type": "string", "description": "the nonce from a director cue notice, echoed to prove the cue reached you"}
			}
		}
	}
	return tools
}

func dispatchTool(ctx context.Context, bus *Bus, name string, rawArgs json.RawMessage) (any, error) {
	switch name {
	case "send_message":
		return toolSend(ctx, bus, rawArgs)
	case "wait_for_message":
		return toolWait(ctx, bus, rawArgs)
	case "inbox_summary":
		return toolSummary(ctx, bus, rawArgs)
	case "list_roster":
		return toolRoster(ctx, bus)
	case "set_presence":
		return toolPresence(ctx, bus, rawArgs)
	case "broadcast":
		return toolBroadcast(ctx, bus, rawArgs)
	case "report_status":
		return toolReportStatus(ctx, bus, rawArgs)
	}
	return nil, errors.New("unknown tool: " + name)
}

func newEnvelope(self Sender) *Envelope {
	id := ulid.Make().String()
	e := &Envelope{
		SchemaVersion:  1,
		MessageID:      id,
		ConversationID: "cid-" + id,
		Sender:         self,
		SentAt:         time.Now().UTC().Format(time.RFC3339),
	}
	e.Sender.Principal = nil // Phase 0: envelope validates with principal null
	return e
}

// sendArgs are the send_message tool arguments. in_reply_to and reply_by carry
// json tags because Go's case-insensitive field match does not bridge the
// underscore in the schema key to the CamelCase field: without the tag both
// would silently drop, and a reply would lose the in_reply_to that R-88
// receipt correlation reads (in_reply_to -> CorrelationID below). to,
// performative, text and refs bind without a tag, having no underscore.
type sendArgs struct {
	To           string
	Performative string
	Text         string
	InReplyTo    string `json:"in_reply_to"`
	ReplyBy      string `json:"reply_by"`
	Workspace    string `json:"workspace"`
	Refs         []string
}

// buildSendEnvelope parses and validates send_message args into an envelope,
// returning the optional workspace hint separately. Split from toolSend so the
// arg binding, notably in_reply_to -> CorrelationID (R-88 receipt
// correlation), is testable without a broker.
func buildSendEnvelope(self Sender, raw json.RawMessage) (*Envelope, string, error) {
	var a sendArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, "", err
	}
	e := newEnvelope(self)
	e.Recipient.Address = a.To
	e.Performative = a.Performative
	e.Content = Content{Type: "text", Data: a.Text, Refs: a.Refs}
	e.InReplyTo = a.InReplyTo
	e.ReplyBy = a.ReplyBy
	if a.InReplyTo != "" {
		e.CorrelationID = a.InReplyTo
	}
	if err := e.validate(); err != nil {
		// The envelope is returned with its refusal so the caller can audit it
		// as built (LR-6).
		return e, "", atStage("validate", err)
	}
	return e, a.Workspace, nil
}

func toolSend(ctx context.Context, bus *Bus, raw json.RawMessage) (any, error) {
	e, wsHint, err := buildSendEnvelope(bus.self, raw)
	if err != nil {
		if e == nil {
			return nil, bus.refuseArgs(ctx, raw, err)
		}
		return nil, bus.refuseEnvelope(ctx, e, "validate", err)
	}
	// A broadcast address takes the broadcast path, so both entry points reach
	// the same fan-out, refuse alike, and audit alike (director#121).
	if rest, ok := strings.CutPrefix(e.Recipient.Address, "broadcast://"); ok {
		ws, team, _ := strings.Cut(rest, "/")
		if err := validToken("broadcast workspace", ws); err != nil {
			return nil, bus.refuseEnvelope(ctx, e, "validate", err)
		}
		if team != "" {
			if err := validToken("broadcast team", team); err != nil {
				return nil, bus.refuseEnvelope(ctx, e, "validate", err)
			}
		}
		return broadcastFanout(ctx, bus, ws, team, e)
	}
	res, err := bus.publish(ctx, e, wsHint)
	if err != nil {
		return nil, bus.refuseEnvelope(ctx, e, "publish", err)
	}
	out := map[string]any{
		"status":     "accepted for delivery",
		"message_id": e.MessageID,
		"tier":       res.Tier,
		"note":       "accepted is not delivered or read; the recipient reports those (R-08)",
	}
	// The stream and sequence are the hub's (or the local broker's) own word
	// that the bytes are stored. It matters most at the global tier, where a
	// leaf-side sender is not permitted to read the director's stream back.
	if res.Stream != "" {
		out["stream"] = res.Stream
		out["sequence"] = res.Sequence
	}
	return out, nil
}

// waitArgs are the wait_for_message arguments after defaults and caps.
type waitArgs struct {
	TimeoutSeconds int `json:"timeout_seconds"`
	Max            int `json:"max"`
}

func parseWaitArgs(raw json.RawMessage) waitArgs {
	var a waitArgs
	_ = json.Unmarshal(raw, &a)
	if a.TimeoutSeconds <= 0 {
		a.TimeoutSeconds = 30
	}
	if a.TimeoutSeconds > 120 {
		a.TimeoutSeconds = 120
	}
	if a.Max <= 0 {
		a.Max = 1
	}
	if a.Max > maxDrain {
		a.Max = maxDrain
	}
	return a
}

func toolWait(ctx context.Context, bus *Bus, raw json.RawMessage) (any, error) {
	a := parseWaitArgs(raw)
	if a.Max > 1 {
		return toolWaitBatch(ctx, bus, a)
	}
	res, err := bus.receiveTiered(ctx, time.Duration(a.TimeoutSeconds)*time.Second)
	if err != nil && !errors.Is(err, jetstream.ErrNoMessages) {
		return nil, err
	}
	out := map[string]any{"message": res.Env}
	if res.Env == nil {
		out["note"] = "no message within the window; this is silence, not failure"
	} else {
		out["tier"] = res.Tier
	}
	// A global-tier failure is reported beside the answer rather than instead
	// of it: the local tier keeps working through a hub outage, so the poll
	// does too, and the caller still hears that the hub did not answer.
	if res.GlobalWarn != "" {
		out["global_warning"] = res.GlobalWarn
	}
	if r := bus.takeResumed(); len(r) > 0 {
		out["resumed"] = r
	}
	return out, nil
}

// toolWaitBatch is wait_for_message with max > 1. The single form keeps its
// result shape exactly, so existing callers see no change.
func toolWaitBatch(ctx context.Context, bus *Bus, a waitArgs) (any, error) {
	res, err := bus.receiveBatch(ctx, time.Duration(a.TimeoutSeconds)*time.Second, a.Max)
	if err != nil {
		return nil, err
	}
	items := res.Items
	if items == nil {
		items = []drained{}
	}
	out := map[string]any{
		"messages": items,
		"count":    len(items),
		"order":    batchOrderNote,
	}
	if len(items) == 0 {
		out["note"] = "no message within the window; this is silence, not failure"
	} else {
		out["remaining"] = bus.remaining(ctx)
	}
	if res.Discarded > 0 {
		out["discarded"] = res.Discarded
		out["discarded_note"] = "undecodable messages were terminated and skipped so the rest of the batch could return"
	}
	if res.LocalWarn != "" {
		out["local_warning"] = res.LocalWarn
	}
	for _, it := range items {
		if it.AckUnconfirmed {
			out["ack_note"] = "a message marked ack_unconfirmed may be delivered once more later; it is returned rather than risk losing it"
			break
		}
	}
	if res.GlobalWarn != "" {
		out["global_warning"] = res.GlobalWarn
	}
	if r := bus.takeResumed(); len(r) > 0 {
		out["resumed"] = r
	}
	return out, nil
}

func toolSummary(ctx context.Context, bus *Bus, raw json.RawMessage) (any, error) {
	var a struct {
		Limit int `json:"limit"`
	}
	_ = json.Unmarshal(raw, &a)
	if a.Limit <= 0 {
		a.Limit = summaryDefault
	}
	if a.Limit > summaryMax {
		a.Limit = summaryMax
	}
	res, err := bus.summarizeInbox(ctx, a.Limit)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"summary": res.Summary,
		"waiting": res.Expected,
		"read":    res.Read,
		"note":    "nothing was acked; drain in order with wait_for_message max=N",
	}
	for tier, exp := range res.Expected {
		if uint64(res.Read[tier]) < exp {
			out["partial"] = "fewer messages were read than the consumer reports waiting (limit reached, or read raced a drain); the counts cover what was read"
		}
	}
	if len(res.MaybeConsumed) > 0 {
		out["maybe_consumed"] = res.MaybeConsumed
		out["maybe_consumed_note"] = "more messages were listed than the consumer reports waiting: some above the ack floor were already consumed out of order, and the summary cannot tell which, so counts and flags may include them"
	}
	if res.GlobalWarn != "" {
		out["global_warning"] = res.GlobalWarn
	}
	return out, nil
}

func toolRoster(ctx context.Context, bus *Bus) (any, error) {
	rows, globalWarn, err := bus.rosterMerged(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"present": rows, "count": len(rows)}
	if globalWarn != "" {
		out["global_warning"] = globalWarn
	}
	return out, nil
}

func toolPresence(ctx context.Context, bus *Bus, raw json.RawMessage) (any, error) {
	var a struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if a.State == "" {
		a.State = "idle"
	}
	globalWarn, err := bus.setPresence(ctx, a.State)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"status": "presence recorded", "state": a.State}
	if globalWarn != "" {
		out["global_warning"] = globalWarn
	}
	return out, nil
}

func toolBroadcast(ctx context.Context, bus *Bus, raw json.RawMessage) (any, error) {
	var a struct {
		Team, Text string
		Workspace  string `json:"workspace"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, bus.refuseArgs(ctx, raw, err)
	}
	ws := a.Workspace
	if ws == "" {
		ws = bus.self.Workspace
	}
	// The envelope as the caller addressed it, so a refusal of the whole
	// broadcast is audited with what was asked for (LR-6).
	asked := newEnvelope(bus.self)
	if a.Team == "" {
		asked.Recipient.Address = "broadcast://" + ws
	} else {
		asked.Recipient.Address = "broadcast://" + ws + "/" + a.Team
	}
	asked.Recipient.Team = a.Team
	asked.Performative = "INFORM"
	asked.Content = Content{Type: "text", Data: a.Text}
	if err := validToken("broadcast workspace", ws); err != nil {
		return nil, bus.refuseEnvelope(ctx, asked, "validate", err)
	}
	if a.Team != "" {
		if err := validToken("broadcast team", a.Team); err != nil {
			return nil, bus.refuseEnvelope(ctx, asked, "validate", err)
		}
	}
	return broadcastFanout(ctx, bus, ws, a.Team, asked)
}

// broadcastFanout is the one path to a broadcast, from the broadcast tool and
// from send_message to a broadcast:// address alike (director#121). It
// resolves the scope from live presence, refuses at zero before any publish,
// and otherwise sends the template's performative, content and reply fields
// to each seat as its own durable agent:// message. A broadcast is a fan-out
// of durable directed sends; it used to publish core NATS to a subject no
// stream captures and answer "broadcast sent" to a message that reached no
// one. Every refusal is audited (LR-6): a refusal of the whole broadcast with
// tmpl, the envelope as the caller addressed it, and each refused copy with
// its own envelope.
func broadcastFanout(ctx context.Context, bus *Bus, ws, team string, tmpl *Envelope) (any, error) {
	targets, scan, err := bus.broadcastRecipients(ctx, ws, team)
	if err != nil {
		return nil, bus.refuseEnvelope(ctx, tmpl, "resolve", err)
	}
	if len(targets) == 0 {
		return nil, bus.refuseEnvelope(ctx, tmpl, "resolve", noBroadcastRecipientsErr(ws, team, scan))
	}
	// One conversation for the whole fan-out; one message id per recipient, so
	// each inbox dedupes and each audit record stands alone.
	conv := tmpl.ConversationID
	sent := []string{}
	failed := []map[string]string{}
	for _, t := range targets {
		e := newEnvelope(bus.self)
		e.ConversationID = conv
		e.CorrelationID = tmpl.CorrelationID
		e.InReplyTo = tmpl.InReplyTo
		e.ReplyBy = tmpl.ReplyBy
		e.Recipient.Address = "agent://" + t.Team + "/" + t.ID
		e.Performative = tmpl.Performative
		e.Content = tmpl.Content
		if err := e.validate(); err != nil {
			return nil, bus.refuseEnvelope(ctx, e, "validate", err)
		}
		if _, err := bus.publish(ctx, e, ws); err != nil {
			// One recipient refused is not the broadcast refused: the others
			// still go. The refused copy is audited like any refused send.
			body, _ := json.Marshal(e)
			failed = append(failed, map[string]string{
				"to":    e.Recipient.Address,
				"error": err.Error(),
				"audit": bus.auditRefusal(ctx, e.MessageID, refusalStage(err, "publish"), body, err),
			})
			continue
		}
		sent = append(sent, e.MessageID)
	}
	// Nothing was accepted for delivery when every copy failed, so the
	// broadcast is a refusal, not a success with an empty sent list. Each
	// copy's own refusal is already audited above; the error names them all.
	if len(sent) == 0 {
		return nil, allFailedErr(ws, team, failed)
	}
	return map[string]any{
		"status":             "accepted for delivery",
		"recipients":         len(targets),
		"conversation_id":    conv,
		"sent":               sent,
		"failed":             failed,
		"skipped_unreadable": scan.Unreadable,
		"note":               "accepted is not delivered or read; each recipient reports those (R-08)",
	}, nil
}

// allFailedErr refuses a broadcast whose every per-seat send failed. Live
// seats were found, so the refusal is not a claim that no one is live; it
// names each seat, its error, and where its refused copy was recorded.
func allFailedErr(ws, team string, failed []map[string]string) error {
	scope := fmt.Sprintf("workspace %q", ws)
	if team != "" {
		scope = fmt.Sprintf("workspace %q team %q", ws, team)
	}
	parts := make([]string, 0, len(failed))
	for _, f := range failed {
		parts = append(parts, fmt.Sprintf("%s: %s (audit: %s)", f["to"], f["error"], f["audit"]))
	}
	return fmt.Errorf("broadcast to %s reached no one: 0 of %d per-seat sends were accepted, so nothing was sent and the broadcast is refused rather than reported as accepted (R-92). %s", scope, len(failed), strings.Join(parts, "; "))
}

// noBroadcastRecipientsErr refuses a broadcast no live seat would receive,
// before any publish, and says only what the scan established (finding-188).
func noBroadcastRecipientsErr(ws, team string, scan presenceScan) error {
	scope := fmt.Sprintf("workspace %q", ws)
	if team != "" {
		scope = fmt.Sprintf("workspace %q team %q", ws, team)
	}
	if scan.Unreadable > 0 || scan.Incomplete > 0 {
		return fmt.Errorf("no usable live presence in %s other than the sender, and %d record(s) could not be used (%d unreadable, %d incomplete), so whether anyone is live was NOT established; this is not a report that no one is. Broadcast refused before publish rather than sent to no one (R-92)", scope, scan.Unreadable+scan.Incomplete, scan.Unreadable, scan.Incomplete)
	}
	return fmt.Errorf("no live presence in %s other than the sender (%d presence key(s) read, %d in scope); no session would receive this broadcast, so it is refused before publish (R-92)", scope, scan.Keys, scan.Matched)
}

// batchOrderNote says what a batch result's order means. The list is grouped
// local first, then global; the budget is split by turn (drain.go), so the
// order of the list is not a priority (director#260).
const batchOrderNote = "listed local first, then global; each tier oldest first; the budget is split by turn between the tiers, so the list order is not a delivery priority"
