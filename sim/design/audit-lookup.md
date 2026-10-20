# Audit lookup: read one audit record by its number

Status: design for review, 2026-10-20. Owner: the architect role. Docs only;
nothing is built by this document.

When a send through director's tool is refused or fails, the tool tells the
sender "audit: recorded agent.audit seq N", and that record holds the
envelope and why it failed. Today nothing in the tool can read it back, so a
seat on another host that hits a failure has the record's number and no way to
see it. This design adds one read-only tool call that returns that record,
over the connection the tool already has. The operator also asked for the same
lookup in marvel, readable from the marvel CLI by cluster; that half is
designed in marvel's own repository (`docs/design/bus-audit.md`).

## 1. What was ruled

Desk card b5-agent-audit-read, ruled 2026-10-20. The operator, verbatim
except the flag, which the note shows with one dash and is written here as
`--cluster`: "do mcp-audit-lookup AND marvel-bus-audit, and the second should
be able to be read with marvel bus audit `--cluster` (target-cluster) from cli
also."

The option this half builds, as written on the card: "Add a read-only audit
lookup to director's MCP tool: given a record number, it returns that one
record from its own cluster's audit log, using the connection the tool already
has. It never sends, acks or deletes, and shows no message body beyond what
the record holds."

## 2. Premises (checked 2026-10-20, director main 935e0f5)

- **What a record is.** Every successful send mirrors its envelope to
  `agent.audit` (`auditMirror`, `cmd/director-mcp/bus.go`). A refused send
  writes the envelope as built, with headers `Director-Outcome: refused`,
  `Director-Stage` and `Director-Refusal` (at most 1024 bytes), and a
  `Nats-Msg-Id` ending in `-refused` (`auditRefusal`, same file). The stream
  is `AGENT_AUDIT`, kept 720 hours. Its declared config sets neither
  `DenyDelete` nor `DenyPurge` (marvel `internal/bus/declared.go`, the
  `AuditStream` object), so a message inside the stream's range can be
  missing: deleted or purged, not aged out.
- **A sequence number names a record only on one broker.** Each cluster has
  its own `AGENT_AUDIT`, numbered from 1, and a send's own result carries a
  sequence too: the inbox stream's, or the hub's for a global send
  (`sendResult`, `bus.go`). The same N on another cluster, or a send
  result's N, is a different record, and nothing in a bare number says
  which log it came from. The session's cluster name is `DIRECTOR_CLUSTER`
  (`global.go`, `globalConfig.Cluster`), empty when it is not set.
- **The read already exists in director, for another reader.** The ask reader
  reads `AGENT_AUDIT` by sequence with `Stream.GetMsg` and creates no consumer
  (`cmd/director-mcp/askreader.go`, `readAudit`). The lookup uses the same
  call, so it moves no one's ack floor.
- **The tools today** are `send_message`, `wait_for_message`,
  `inbox_summary`, `list_roster`, `set_presence`, `report_status` and
  `broadcast`, plus `cancel_held` and `mark_handled` behind their flags,
  dispatched by name in `dispatchTool` (`tools.go`). None takes a stream or a
  subject.
- **Not yet known:** whether every cluster's broker lets a seat's connection
  read `AGENT_AUDIT` by sequence. The card says so ("the builder finds that
  out in the first test, and if it is not, this card comes back to you"). The
  test in section 5 is that check.

## 3. The call

```
audit_lookup { "cluster": "<name>", "seq": <integer>, "message_id": "<optional>" }
```

- `seq` is required, a whole number of 1 or more. Anything else is refused
  before the broker is asked.
- **`cluster` is required and binds the number to a log.** It must equal
  this session's `DIRECTOR_CLUSTER`. A different name is refused, with the
  way to read that cluster's record instead: `marvel bus audit <seq>
  --cluster <name>`, the marvel half. A session with `DIRECTOR_CLUSTER`
  unset refuses every lookup, since it cannot say which log a number belongs
  to.
- **The refusal text carries the cluster.** `auditRefusal` adds it, so the
  number arrives already bound: `audit: recorded agent.audit seq N on
  cluster <name>`. Where `DIRECTOR_CLUSTER` is unset the text is unchanged.
  The existing pattern `audit: recorded agent\.audit seq (\d+)` still
  matches. The tool description says to use only the number from that text,
  never a sequence from a send result, which numbers a different stream.
- **`message_id` is an optional second binding.** When given, the record's
  `Nats-Msg-Id` must equal it, or it plus `-refused`; otherwise the call
  returns `audit_lookup: seq N on <cluster> holds a different message`, and
  not the record.
- The stream name is a constant, `AGENT_AUDIT`. The call takes no stream, no
  subject and no filter, so it cannot be pointed at an inbox.
- It reads the local tier only: the audit log of the cluster this session is
  on. There is no global audit stream, and a record from another cluster is
  read on that cluster (the marvel half reaches other clusters).
- It never publishes, acks, deletes or creates a consumer, and writes no file.

**Result**, one record, as stored:

| field | from |
|---|---|
| `cluster` | this session's `DIRECTOR_CLUSTER`, the value the call was bound to |
| `stream_created` | `AGENT_AUDIT`'s creation time from the stream's info, so a log that was replaced reads as a different log |
| `seq`, `time` | the stream's sequence and timestamp |
| `subject` | the message subject, `agent.audit` |
| `outcome` | `refused` when `Director-Outcome` says so, else `sent` |
| `stage`, `refusal` | the two headers, present only on a refusal |
| `msg_id` | `Nats-Msg-Id` |
| `envelope` | the stored body parsed as an envelope; when it does not parse, `raw` holds it as a string instead |

No field is added from anywhere but the record, except `cluster` and
`stream_created`, which say which log it was read from. The tool description says
plainly that a record holds the full envelope of a send, its text included,
and that any session on the cluster can read any record on that cluster's
audit log by number. That is the reach the card ruled; the description makes
it visible to the seat that calls it.

**Refusals and misses**, each returned as the tool's error text:

| case | text |
|---|---|
| `seq` missing, not a whole number, or below 1 | `audit_lookup: seq must be a whole number of 1 or more` |
| `cluster` missing or not this session's | `audit_lookup: this session is on cluster <own>, not <given>; read it with marvel bus audit <seq> --cluster <given>` |
| `DIRECTOR_CLUSTER` unset | `audit_lookup: this session names no cluster (DIRECTOR_CLUSTER is unset), so a number cannot be tied to a log` |
| no stream | `audit_lookup: cluster <c> has no AGENT_AUDIT stream` |
| the stream is empty | `audit_lookup: AGENT_AUDIT on <c> holds no records` |
| N below the first sequence (aged out after 720 hours) | `audit_lookup: seq N on <c> has aged out; the stream holds F to L` |
| N past the last sequence (not written yet, or a number from another log) | `audit_lookup: seq N on <c> is past the last record, L; check the number came from an audit line for this cluster` |
| N inside F to L with no message | `audit_lookup: seq N on <c> was deleted or purged; the stream holds F to L with gaps` |
| `message_id` given and not the record's | `audit_lookup: seq N on <c> holds a different message` |
| the broker refuses the read | `audit_lookup: the broker refused the read: <its error, verbatim>` |
| timeout | `audit_lookup: no answer from the broker in 10s` |

A broker refusal is reported as it came back and is not retried; it is the
answer the card asked to bring back to the operator.

## 4. Where it goes

- `cmd/director-mcp/auditlookup.go`: the call, one function over
  `jetstream.JetStream`, so a test can drive it against a scratch broker.
- `tools.go`: the tool definition and one `dispatchTool` case.
- `bus.go`, `auditRefusal`: the ` on cluster <name>` suffix when
  `DIRECTOR_CLUSTER` is set. No other change to sends, the audit writer, the
  ask reader or presence.

## 5. Tests (red first)

Against a scratch broker, as `audit_refusal_test.go` runs today (skipped
without `nats-server`):

1. A refused send, then `audit_lookup` on the seq its result names, returns
   `outcome: refused`, the stage, the refusal text and the envelope's
   `message_id`.
2. A successful send's mirror returns `outcome: sent`, and no `stage` or
   `refusal` field.
3. `seq` 0, -1, 1.5, a string and a missing `seq` are each refused without a
   broker read: the test runs them against a broker it has already stopped,
   and each must return the seq text, never a broker or timeout text.
4. A seq past the last returns the miss text with the stream's range.
5. No `AGENT_AUDIT` stream returns the no-stream text, and an empty stream
   the empty text.
5a. A record deleted from inside the range (`Stream.DeleteMsg` in the test)
   returns the deleted-or-purged text, never the aged-out or past-the-end
   text.
5b. `cluster` other than the session's, missing, or with `DIRECTOR_CLUSTER`
   unset, is refused before any broker read, each with its own text.
5c. A `message_id` that is not the record's returns the different-message
   text and none of the record's fields.
5d. A refused send's result text reads `seq N on cluster <name>` with
   `DIRECTOR_CLUSTER` set, and is unchanged with it unset; the existing
   `recordedSeq` pattern matches both.
6. The stream's consumer count and every inbox consumer's ack floor are the
   same before and after a lookup.
7. A body that is not an envelope comes back as `raw`, unchanged.
8. **On each cluster, after install** (the card's open question): the
   cluster's own seat runs `audit_lookup` on a known seq. The permission
   this tests is the JetStream message-get request on the stream, subject
   `$JS.API.STREAM.MSG.GET.AGENT_AUDIT`; the stream does not set
   `AllowDirect`, so the client sends that request and not a direct get
   (nats.go v1.53.1, `jetstream/stream.go:577` and `api.go:97`). On corporate that is
   the three records the card names, 518, 521 and 530, read by corporate's
   research supervisor. If the broker refuses, the refusal text goes to
   director as the card's answer; nothing is changed to get past it.

Every test that starts a broker runs under a scratch `HOME` and `TMPDIR` and
no `MARVEL_SOCKET`, as the existing broker tests do.

## 6. Build

One director PR: section 4's files and section 5's tests 1 to 7. Test 8 is a
read on each cluster after the release reaches it, done by that cluster's
seat, and reported to director. It shares no file with the seat-decision
chain (`seat-decisions.md`), so it may be open beside it.

Valid until 2026-10-27, or until the build PR opens, whichever comes first;
the architect role re-checks it then.
