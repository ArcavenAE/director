# director-mcp shim reference

The shim is a Go program in `probe/nats-phase-0/director-mcp/`. A harness
launches one per session as an MCP stdio server; the shim connects to a NATS
broker and exposes five tools that carry director envelopes. It is the Phase
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
   id hold two durables and each receives its own copy (R-50).
3. Write presence as `idle`; warn on stderr if another instance of the same
   id is present (R-49).
4. Start the heartbeat: presence renewed every 30 seconds on the shim's own
   timer, independent of the model (R-56). The same tick carries the global
   presence row and re-attaches a hub that was down at start.
5. Log `connected to <url> as agent://<team>/<id> instance <ulid> in
   workspace <ws>`, then serve.

## The five tools

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
| `timeout_seconds` | 30 (max 120) | how long to block for the next message |

Result with a message:

```json
{ "message": { "...the envelope..." }, "tier": "local" }
```

Result without one:

```json
{ "message": null, "note": "no message within the window; this is silence, not failure" }
```

With global mode on, the poll alternates between the local inbox and this
session's global inbox and names the tier. A hub that does not answer adds
`global_warning` beside the result rather than failing the poll; the local
tier keeps working through a hub outage. A raw line on the shared global
stream that is not an envelope is terminated and counted, not surfaced; on
the local inbox an undecodable message is surfaced at once.

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
| `AGENT_INBOX` | `agent.*.*.*.inbox`, `agent.*.*.role.*.inbox` | file storage, limits retention, 24h max age, 64 KiB max message, 2m dedupe window |
| `AGENT_AUDIT` | `agent.audit` | file, append-only, 30 days; every envelope is mirrored here |
| `AGENT_STATE` (KV) | `presence.<team>.<id>.<instance>` | 90s TTL on the bucket; a heartbeat rewrite resets the key's age |

Global hub (domain `global`):

| Object | Subjects or keys |
|---|---|
| `GLOBAL_TO_DIRECTOR` | `global.director.inbox`, `global.director.escalation` |
| `GLOBAL_TO_<cluster>` | `global.<cluster>.supervisor.inbox`, `global.<cluster>.escalation`; one stream per cluster so the hub can bind a leaf to its own stream by name |
| `GLOBAL_PRESENCE` (KV) | `presence.<cluster>.<role>.<instance>`, `presence.director.<instance>` |

A global durable is `mcp_global_<id>_<instance>` with an inactive threshold
one hour longer than the hub streams' 24h max age, so cleanup can only ever
discard a durable whose replay had already expired.

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
