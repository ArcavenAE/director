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
  is `AGENT_AUDIT`, kept 720 hours.
- **The read already exists in director, for another reader.** The ask reader
  reads `AGENT_AUDIT` by sequence with `Stream.GetMsg` and creates no consumer
  (`cmd/director-mcp/askreader.go`, `readAudit`). The lookup uses the same
  call, so it moves no one's ack floor.
- **The tools today** are `send_message`, `wait_for_message`,
  `inbox_summary`, `list_roster`, `set_presence`, `report_status` and
  `broadcast`, dispatched by name in `dispatchTool` (`tools.go`). None takes a
  stream or a subject.
- **Not yet known:** whether every cluster's broker lets a seat's connection
  read `AGENT_AUDIT` by sequence. The card says so ("the builder finds that
  out in the first test, and if it is not, this card comes back to you"). The
  test in section 5 is that check.

## 3. The call

```
audit_lookup { "seq": <integer> }
```

- `seq` is required, a whole number of 1 or more. Anything else is refused
  before the broker is asked.
- The stream name is a constant, `AGENT_AUDIT`. The call takes no stream, no
  subject and no filter, so it cannot be pointed at an inbox.
- It reads the local tier only: the audit log of the cluster this session is
  on. There is no global audit stream, and a record from another cluster is
  read on that cluster (the marvel half reaches other clusters).
- It never publishes, acks, deletes or creates a consumer, and writes no file.

**Result**, one record, as stored:

| field | from |
|---|---|
| `seq`, `time` | the stream's sequence and timestamp |
| `subject` | the message subject, `agent.audit` |
| `outcome` | `refused` when `Director-Outcome` says so, else `sent` |
| `stage`, `refusal` | the two headers, present only on a refusal |
| `msg_id` | `Nats-Msg-Id` |
| `envelope` | the stored body parsed as an envelope; when it does not parse, `raw` holds it as a string instead |

No field is added from anywhere but the record. The tool description says
plainly that a record holds the full envelope of a send, its text included,
and that any session on the cluster can read any record on that cluster's
audit log by number. That is the reach the card ruled; the description makes
it visible to the seat that calls it.

**Refusals and misses**, each returned as the tool's error text:

| case | text |
|---|---|
| `seq` missing, not a whole number, or below 1 | `audit_lookup: seq must be a whole number of 1 or more` |
| no stream | `audit_lookup: this cluster has no AGENT_AUDIT stream` |
| no record at that number (aged out, or never written) | `audit_lookup: no record at seq N; the stream holds F to L` |
| the broker refuses the read | `audit_lookup: the broker refused the read: <its error, verbatim>` |
| timeout | `audit_lookup: no answer from the broker in 10s` |

A broker refusal is reported as it came back and is not retried; it is the
answer the card asked to bring back to the operator.

## 4. Where it goes

- `cmd/director-mcp/auditlookup.go`: the call, one function over
  `jetstream.JetStream`, so a test can drive it against a scratch broker.
- `tools.go`: the tool definition and one `dispatchTool` case.
- No change to sends, the audit writer, the ask reader or presence.

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
5. No `AGENT_AUDIT` stream returns the no-stream text.
6. The stream's consumer count and every inbox consumer's ack floor are the
   same before and after a lookup.
7. A body that is not an envelope comes back as `raw`, unchanged.
8. **On each cluster, after install** (the card's open question): the
   cluster's own seat runs `audit_lookup` on a known seq. On corporate that is
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
