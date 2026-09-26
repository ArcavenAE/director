# director-mcp shim reference

The shim is a Go program in `probe/nats-phase-0/director-mcp/`. A harness
launches one per session as an MCP stdio server; the shim connects to a NATS
broker and exposes six tools that carry director envelopes. It is the Phase
0 probe cut, kept small on purpose: it proves the transport and the receive
shape. It is not the director software.

Values below are read from the source on `main` as of 2026-09-15
(`main.go`, `tools.go`, `envelope.go`, `bus.go`, `global.go`).

## Environment

Identity, read at start. The three identity values are subject tokens and
must match `[A-Za-z0-9_-]`; anything else is refused before a connection is
made, never rewritten (R-76).

| Variable | Default | Meaning |
|---|---|---|
| `DIRECTOR_AGENT_ID` | required | the session's id; the `id` in `agent://team/id` |
| `DIRECTOR_TEAM` | `default` | the team |
| `DIRECTOR_WORKSPACE` | `default` | the workspace |
| `NATS_URL` | `nats://127.0.0.1:4222` | the local broker |

Broker credentials, all optional. Unset means an anonymous connection.

| Variable | Meaning |
|---|---|
| `DIRECTOR_NATS_USER`, `DIRECTOR_NATS_PASS` | user and password for a broker running the authorization block |
| `DIRECTOR_NATS_CREDS` | path to a `.creds` file (NKey or JWT); the forward path, not wired at the Phase 0 broker |

The global tier (R-86). Off unless the first is set; with it set the other
two are required. The role is one of exactly two words.

| Variable | Meaning |
|---|---|
| `DIRECTOR_GLOBAL_DOMAIN` | the hub's JetStream domain, reached over the local broker's leaf link (for example `global`) |
| `DIRECTOR_CLUSTER` | this cluster's subject token (for example `kinu`, `mokuzai`) |
| `DIRECTOR_GLOBAL_ROLE` | `supervisor` or `director` |

Under marvel, `DIRECTOR_AGENT_ID`, `NATS_URL`, `DIRECTOR_NATS_USER`, and
`DIRECTOR_NATS_PASS` are stamped into the session environment by the
daemon; a launcher supplies the rest.

## Command line

| Invocation | Behaviour |
|---|---|
| `director-mcp` | serve MCP on stdin and stdout, log to stderr |
| `director-mcp --preflight` | connect, verify the broker is provisioned (and the hub through the domain when global mode is on), print `preflight: ok`, exit. Creates no consumer, writes no presence. |

Exit codes: 2 for a configuration refusal (missing or malformed identity,
bad global levers), 1 for a failed connection or preflight.

## Startup behaviour

1. Validate identity and global levers; refuse on failure.
2. Connect to `NATS_URL`; create or bind the durable inbox consumer
   `mcp_<id>_<instance>` on `AGENT_INBOX`, filtered to this session's inbox
   subject. `<instance>` is a ULID minted per process, so two shims with one
   id hold two durables and each receives its own copy (R-50). A new
   instance starts after the highest ack floor among the seat's departed
   durables (same name prefix and same filter subject, and no live presence
   row for that instance), so a reconnect does not replay the inbox. A live
   sibling's position is not taken: a session joining a live seat reads the
   inbox from the start, its own copy. With no departed durable the inbox is
   read from the start, so mail sent to a cold mailbox is delivered. A
   crashed session's presence row can linger up to the bucket's 90s TTL; a
   reconnect inside that window replays rather than skips. The
   durable carries a 73h inactive threshold, one hour above the inbox's 72h
   max age, so a superseded instance's durable is cleaned up rather than
   left behind.
3. Write presence as `idle`; warn on stderr if another instance of the same
   id is present (R-49).
4. Start the heartbeat: presence renewed every 30 seconds on the shim's own
   timer, independent of the model (R-56). The same tick carries the global
   presence row and re-attaches a hub that was down at start.
5. Log `connected to <url> as agent://<team>/<id> instance <ulid> in
   workspace <ws>`, then serve.

## The six tools

Every tool is request and response. `wait_for_message` is the long poll
that stands in for a push the transport cannot make.

### `send_message`

| Argument | Required | Meaning |
|---|---|---|
| `to` | yes | `agent://{team}/{id}`, `role://{team}/{role}`, `broadcast://{workspace}[/{team}]`, and with global mode on, `global://director` or `global://{cluster}/supervisor` |
| `performative` | yes | one of the twelve verbs below |
| `text` | yes | the body; a body-bearing message is refused empty (R-87) |
| `refs` | no | pointers: `bd:`, `finding:`, `file:`, `url:`, `pr:` |
| `in_reply_to` | no | the `message_id` being answered; also sets `correlation_id` so receipts correlate (R-88) |
| `reply_by` | no | RFC 3339 deadline |
| `workspace` | no | the recipient's workspace. Omit and it is resolved from the recipient's live presence; with no live presence the send is refused rather than misdelivered (R-92). Set it to address a known cold mailbox verbatim. Ignored for `global://`. |

Result:

```json
{
  "status": "accepted for delivery",
  "message_id": "01M2K9Q2Q3WTRB0D6FRWAS0R0J",
  "tier": "local",
  "stream": "AGENT_INBOX",
  "sequence": 4127,
  "note": "accepted is not delivered or read; the recipient reports those (R-08)"
}
```

`stream` and `sequence` are the broker's own word that the bytes are
stored. A global send is refused before publish when nobody is live at the
address, so nothing is stored and the audit mirror stays empty.

### `wait_for_message`

| Argument | Default | Meaning |
|---|---|---|
| `timeout_seconds` | 30 (max 120) | how long to block when nothing is already waiting |
| `max` | 1 (max 50) | most messages to return in one call |

Result with a message:

```json
{ "message": { "...the envelope..." }, "tier": "local" }
```

Result without one:

```json
{ "message": null, "note": "no message within the window; this is silence, not failure" }
```

With global mode on, the poll first takes a message already waiting, from
the tier whose turn it is and then the other, flipping the turn every call,
so a backlog on one tier cannot starve the other. With nothing waiting it
alternates between the local inbox and this session's global inbox in slices
and names the tier. A hub that does not answer adds
`global_warning` beside the result rather than failing the poll; the local
tier keeps working through a hub outage. A raw line on the shared global
stream that is not an envelope is terminated and counted, not surfaced; on
the local inbox an undecodable message is surfaced at once.

**Batch drain (`max` above 1).** Every message already waiting comes back in
one call, up to `max`, oldest first within each tier. With both tiers on,
the budget is shared so a backlog on one cannot starve the other: the tier
whose turn it is takes up to half (rounded up), the other takes up to the
rest, and the first takes any budget left over. The result lists the local
items in stream order, then the global ones. Sequence numbers are per
stream, so they order messages within a tier only. When nothing is waiting the call blocks
as the single form does, then tops the batch up with anything else that
arrived. Messages are consumed exactly as the single form consumes them,
with the ack confirmed by the server before the batch returns, so a lost ack
cannot redeliver a message after the caller has read past it. An
undecodable message on either tier is terminated and counted in
`discarded` so the rest of the batch still returns. The single form keeps
its result shape.

**Failures partway through a batch.** Once any message has been acked in a
call, a later local failure (a failed fetch, a failed top-up after the
blocking wait) no longer fails the call: the messages already in hand come
back, and the failure is reported in `local_warning`. The call is an error
only when nothing was consumed. A confirmed ack that itself fails, or times
out after the server applied it, returns the message anyway marked
`ack_unconfirmed`, and the rest of that batch is still acked. The shim
prefers a possible duplicate to a silent loss: such a message may be
delivered once more later.

```json
{
  "messages": [ { "message": { "...": "..." }, "tier": "local", "sequence": 431 } ],
  "count": 1,
  "order": "oldest first within each tier; local before global; the budget is shared between tiers",
  "remaining": { "local": 0, "global": 0 }
}
```

There is no peek mode on this tool. A fetch without an ack leaves the
message pending on the session's durable, where it is redelivered after the
ack wait and holds up the FIFO cursor. Looking without consuming is
`inbox_summary`, which reads through a separate consumer.

The summary is a point-in-time read. Nothing locks it against a
`wait_for_message` running on the same session at the same moment, so a
drain can race it; `partial` and `maybe_consumed` say when the numbers do
not line up.

### `inbox_summary`

| Argument | Default | Meaning |
|---|---|---|
| `limit` | 200 (max 500) | most waiting messages to read per tier |

Summarizes what is waiting for this session on both tiers and acks nothing.
It reads each durable's filter and ack floor, then reads the stream from
just past the floor through a throwaway ephemeral pull consumer (memory
storage, no acks, deleted afterwards), so the durable's cursor does not move
and a repeat call returns the same answer. It reads to the end of the stream
(up to `limit`), not to the durable's count of what is waiting: the waiting
set is not always a prefix of the stream above the floor, and stopping at the
count would miss the newest mail.

```json
{
  "summary": {
    "total": 31,
    "by_sender": { "seat-a@aae-orc": 12, "director@aae-orc": 1 },
    "by_performative": { "INFORM": 28, "REQUEST": 2, "QUERY": 1 },
    "flagged": [
      { "tier": "local", "sequence": 433, "message_id": "01M3...", "sender": "seat-b@aae-orc",
        "performative": "INFORM", "reasons": ["awaits a reply"],
        "excerpt": "New seat up, holding for director instructions." }
    ],
    "sequences": { "local": [431, 432, 433], "global": [7] }
  },
  "waiting": { "local": 30, "global": 1 },
  "read": { "local": 30, "global": 1 },
  "note": "nothing was acked; drain in order with wait_for_message max=N"
}
```

A message is flagged when its performative is REQUEST, FAILURE or QUERY,
when it sets `reply_by`, or when its text says it holds custody or awaits a
reply or instructions. The text match errs toward flagging. `waiting` is the
durable's own count; `partial` appears when fewer were read than that
(the limit, or a drain racing the read). `maybe_consumed` appears when more
were read than that: some messages above the ack floor were acked out of
order (a lost fire-and-forget ack on the single form, or an unconfirmed ack
in a batch), and the durable does not expose which, so the counts, flags and
sequences may include them. An undecodable message is counted in
`undecodable` and listed in `sequences`.

### `list_roster`

No arguments. Result:

```json
{
  "count": 2,
  "present": [
    { "agent_id": "michael", "instance": "01M2JJ8R...", "pid": 42657,
      "state": "busy", "team": "ops", "workspace": "aae-orc", "ts": "2026-09-15T19:50:00Z" }
  ]
}
```

Rows come from the presence bucket. Absence means silence, not a negative
report: a session whose shim died leaves no row. With global mode on, rows
from both tiers are merged with a `tier` column, and global rows carry
`cluster` and `role`.

### `set_presence`

| Argument | Default | Meaning |
|---|---|---|
| `state` | `idle` | `idle`, `busy`, or `away` |

Result: `{ "status": "presence recorded", "state": "busy" }`. The state is
the last value the model set; the timestamp is renewed by the shim's timer
regardless.

### `broadcast`

| Argument | Required | Meaning |
|---|---|---|
| `text` | yes | the body |
| `team` | no | a team; omit for the whole workspace |
| `workspace` | no | a target workspace; omit for your own |

Sends an INFORM to `broadcast://{workspace}[/{team}]`. There is no durable
queue for broadcasts: late joiners do not replay them. Result:
`{ "status": "broadcast sent", "message_id": "..." }`.

## Addresses and subjects

| Address | Subject | Guarantee |
|---|---|---|
| `agent://{team}/{id}` | `agent.{ws}.{team}.{id}.inbox` | durable, at least once, deduplicated on `message_id` within a 2 minute window (R-13) |
| `role://{team}/{role}` | `agent.{ws}.{team}.role.{role}.inbox` | resolved to the current holder at delivery; no holder is a NOT-UNDERSTOOD back to the sender |
| `broadcast://{ws}[/{team}]` | `agent.{ws}.broadcast` or `agent.{ws}.{team}.broadcast` | fan-out, no replay |
| `global://director` | `global.director.inbox` in stream `GLOBAL_TO_DIRECTOR` | durable at the hub; refused before publish when no director is live |
| `global://{cluster}/supervisor` | `global.{cluster}.supervisor.inbox` in stream `GLOBAL_TO_{cluster}` | durable at the hub; refused when no supervisor of that cluster is live |

`{ws}` for a local address is the recipient's workspace, resolved from
live presence unless the `workspace` argument names it (R-92). A global
address carries no workspace; `recipient.team` is empty for it.

## Streams and buckets

Local broker:

| Object | Subjects | Shape |
|---|---|---|
| `AGENT_INBOX` | `agent.*.*.*.inbox`, `agent.*.*.role.*.inbox` | file storage, limits retention, 72h max age, 64 KiB max message, 2m dedupe window |
| `AGENT_AUDIT` | `agent.audit` | file, append-only, 30 days; every envelope is mirrored here |
| `AGENT_STATE` (KV) | `presence.<team>.<id>.<instance>` | 90s TTL on the bucket; a heartbeat rewrite resets the key's age |

Global hub (domain `global`):

| Object | Subjects or keys |
|---|---|
| `GLOBAL_TO_DIRECTOR` | `global.director.inbox`, `global.director.escalation` |
| `GLOBAL_TO_<cluster>` | `global.<cluster>.supervisor.inbox`, `global.<cluster>.escalation`; one stream per cluster so the hub can bind a leaf to its own stream by name |
| `GLOBAL_PRESENCE` (KV) | `presence.<cluster>.<role>.<instance>`, `presence.director.<instance>` |

A global durable is `mcp_global_<id>_<instance>` with an inactive threshold
one hour longer than the hub streams' 72h max age, so cleanup can only ever
discard a durable whose replay had already expired.

A new global durable resumes the way the local one does: after the highest
ack floor among the seat's departed `mcp_global_<id>_` durables (filtered on
the same inbox subject, no live hub presence row for the instance), or from
the start of the stream when there are none.
Every supervisor of a cluster filters on the same role inbox, so the name
prefix is what keeps one seat's position from moving another's; each
session still receives every message (fan-out, not a work queue).

The first `wait_for_message` result after a start (or after the global tier
attaches) carries `resumed`: one line per tier saying where the durable
started and how many messages were waiting. It is reported once.

When the hub no longer has a session's global durable, a pull does not say
"consumer not found". On a single server it fails with no responders, the
same answer a down leaf link gives; across a leaf link, the production shape,
it is not answered at all and looks exactly like an empty inbox
(director#66). So the shim asks the hub whether the durable exists after a
pull, batch or summary fails that way, and after an empty pull at most once
every 30 seconds. If the hub answers that it does not, the shim recreates it
to resume after the later of the last stream sequence this session acked and
the seat floor from departed durables above (so read mail does not replay and
mail sent meanwhile is delivered), retries, and reports
the recreate once in `global_warning`. If the hub cannot be asked, nothing is
recreated and the original error, if any, is the warning.

## Envelope v1

The wire format is the director envelope from
`aae-orc/docs/design/director-envelope-and-adapter-events.md` section 2,
validated in the shim with `sender.principal` null (the identity plane
attaches it later). The JSON Schema marvel generates its Go types from is
the same contract (`schema.arcaven.com`).

```json
{
  "schema_version": 1,
  "message_id": "01JZWIRE0001TESTMSGREQUEST01",
  "correlation_id": "task:aae-orc-spbc",
  "conversation_id": "cid-01JZWIRE0001TESTMSGREQUEST01",
  "in_reply_to": null,
  "sender": { "agent_id": "reviewer-a", "role": "reviewer", "workspace": "aae-orc",
              "session": "uuid-abc", "principal": null },
  "recipient": { "address": "agent://ops/michael", "team": "ops" },
  "performative": "REQUEST",
  "content": { "type": "text", "data": "please review PR #12", "refs": ["bd:aae-orc-spbc"] },
  "reply_by": null,
  "expires_at": null,
  "sent_at": "2026-09-10T23:37:00Z",
  "trace": { "otel_traceparent": null }
}
```

| Field | Notes |
|---|---|
| `schema_version` | must be 1 |
| `message_id` | ULID, minted by the shim; also the broker's `Nats-Msg-Id` for deduplication |
| `correlation_id` | free text; set to `in_reply_to` when replying so receipts correlate (R-88) |
| `conversation_id` | `cid-<message_id>` on a new conversation |
| `in_reply_to` | the message being answered |
| `sender` | `agent_id`, `workspace`, optional `role` and `session`; `principal` reserved and null |
| `recipient` | `address` (required, one of the five schemes) and `team` (empty for global) |
| `performative` | one of the twelve verbs |
| `content` | `type`, `data`, optional `refs` |
| `reply_by`, `expires_at` | RFC 3339, optional |
| `sent_at` | RFC 3339, UTC |
| `trace.otel_traceparent` | reserved |

The envelope is capped at 64 KiB; anything larger travels as a `pointer`.

### Performatives

A FIPA-ACL subset. The verb is the state of a commitment, not a message
type.

| Verb | Use |
|---|---|
| `REQUEST` | ask the recipient to do something; answered by AGREE or REFUSE |
| `AGREE` | the recipient will do it |
| `REFUSE` | the recipient will not |
| `FAILURE` | agreed, then could not (distinct from a refusal and from partial compliance, R-24, R-25) |
| `INFORM` | a result, a status, a notice; also the verb of every broadcast |
| `QUERY` | ask for information |
| `CFP` | call for proposals |
| `PROPOSE`, `ACCEPT-PROPOSAL`, `REJECT-PROPOSAL` | the negotiation triple |
| `CANCEL` | withdraw a request |
| `NOT-UNDERSTOOD` | protocol rejection; also what a role address with no holder returns |

### Content types

| Type | Body rule |
|---|---|
| `text`, `task`, `result` | body-bearing: `data` must be non-empty or the send is refused at the emit boundary (R-87) |
| `signal` | a bare notification; may carry neither data nor refs |
| `pointer` | must carry at least one ref; the body is elsewhere |

## Presence record

```json
{ "agent_id": "fleet-envoy-g1-0", "instance": "01M2GQ1KMEDB39RAPCGA8CK5HM", "pid": 92392,
  "state": "idle", "team": "fleet", "workspace": "ops2", "ts": "2026-09-15T19:50:00Z" }
```

Global rows add `cluster` and `role`, and a `tier` column in the merged
roster. The record carries the harness's own view of nothing: `state` is
the last value the model set, `ts` is the shim's timer. A record whose pid
no longer answers `kill -0` is a shim that died inside the TTL.

## What the shim does not do

- It does not push. There is no path from the bus into a running model's
  context except the model's own `wait_for_message`.
- It does not attach a principal or sign anything. `sender.principal` is
  null; a name on an inbound message is display text, never an
  authorization input (R-82).
- It does not hold a hub credential. In global mode the leaf link holds the
  cluster credential and the broker holds the leaf link.
- It does not rename. An id is validated and used as given, or refused.
