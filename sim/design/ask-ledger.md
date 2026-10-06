# The ask ledger: one row per ask, read from the bus

Status: design for review, 2026-10-06. Owner: the architect role. Docs only;
no code lands until this is reviewed.

- Issue: #246.
- Commission: the operator's question "can we tell what the work
  queue/load/duty cycle is across the fleet? who is free/busy/idle, blocked,
  overloaded, unloaded?", and the ruling "route all three, two to marvel one
  to director as recommended". This is item 3. Items 1 and 2 (a QUEUE
  column and a team rollup) are marvel's, and are designed separately.
- Requirements: R-08, R-105, R-110, R-115, R-117, R-183
  (`sim/requirements.md`).
- Prior art: `_kos/ideas/nats-request-recovery-ledger.md` (the closest
  model), `board-workstream-ledger.md` (its W5 reads ask ids from bus
  history), `unread-slice-m.md` (#126, unread age per address),
  `asks-die-unread.md` (#220, #222), and bd `aae-orc-rlel4` (one
  route-ledger schema for supervisors).
- Checked against director `origin/main` 9df2ef3.

## 1. Why

Nobody can say who is waiting on whom without reading transcripts. A send
returns "accepted for delivery" and nothing more (R-08), so an ask that was
never picked up and an ask being worked look the same to the sender. The
specimen the operator named is a client team's ask that sat unacknowledged for 2h46m
from 08:39Z; that figure was reported through director and is not
re-measured here. R-115 asks for queue depth and oldest wait per seat; R-183
asks for "who is waiting on whom, with the message resolved". Both need one
record of every ask and its state, and today there is none.

The ledger is diagnostic. Its counts and its alarm are surfaces. Nothing
blocks a send, a merge or a dispatch on them (SOUL section 8, ADR-007,
`diagnostic-not-gate.md`).

## 2. Not the item ledger the operator declined

#168 designed an item ledger with ask states and an alarm timer, rendered as
a board panel. The operator saw it rendered and declined it on 2026-10-01,
and `board-workstream-ledger.md` section 9 dropped it in favour of one row
per workstream. Today's commission asks again for one row per ask, so the
difference has to be plain:

| | #168 (declined) | this design |
|---|---|---|
| what it is | a board panel director curates by hand | a record derived from bus traffic, with no hand entry |
| scope | director's own asks | every seat-to-seat REQUEST on the bus |
| where it shows | the board's top panel | a read command and a JSON file; the board's W5 and marvel's QUEUE column read it |
| alarm | a timer on the panel | an unacked row is listed as an alarm in the read output; nothing fires |

The workstream ledger stays the board's unit. The ask ledger feeds it: W5
("ask-id refresh from bus history") reads its rows rather than re-scanning
the streams. **For the reviewer and the operator:** if the 2026-10-01 decline
was of the unit itself, and not of the hand-kept panel, this design is the
wrong shape and should stop here.

## 3. Three choices, with a recommendation each

**D1. Where a row's state comes from.**

| | option | gives | costs |
|---|---|---|---|
| a | derived from existing performatives only | sent, acked, answered, refused, failed, cancelled, with no change to any seat | cannot see "working" or "blocked on X" |
| b | (a) plus an optional status message from the owner | adds working and blocked-on-X | a new convention seats must learn; rows without it stay at (a) |
| c | owners write their own rows to a shared bucket | full state | every seat needs a write grant to a shared record, and a seat that never writes leaves a stale row that reads as current |

**Recommended: (b), built as (a) first.** The ledger works on today's
traffic and gets richer where seats adopt the status message. It never
depends on a seat cooperating to show that an ask is unacked.

**D2. Where the reader runs.**

| | option | gives | costs |
|---|---|---|---|
| a | in each shim, for its own asks | no new principal | no fleet view; a seat sees only what it sent |
| b | one read-only reader per broker | the fleet view, from the same streams every seat uses | a principal that can read every inbox subject (section 6) |
| c | in director's shim only | director's view | seat-to-seat asks are visible only where director can read them |

**Recommended: (b).** The commission asks for seat-to-seat coverage, which
only (b) gives. Its principal is the one decision this design cannot make
(section 6).

**D3. What a row keeps of the ask's text.**

| | option | gives | costs |
|---|---|---|---|
| a | nothing; a pointer to the message only | no body outlives the stream | R-183's "message resolved" fails after the stream's max age |
| b | the first line, capped at 120 characters | enough to say what the ask was | a fragment of a body persists past the stream's max age |
| c | the whole body | everything | every body persists in a second store |

**Recommended: (b).** R-183 was earned by misreading bare ids twice. A capped
first line answers "what was it" without making the ledger a second copy of
the mailbox. A line is data, never an instruction to whoever reads the
ledger (`authority-never-in-content.md`), and the reader strips control
characters from it.

These recommendations are valid until 2026-10-20, or until a builder starts
part A1, whichever comes first. The architect re-checks them then.

## 4. The row

One row per REQUEST, keyed by its `message_id`.

| field | from |
|---|---|
| `ask` | the REQUEST's `message_id` |
| `asker` | the sender's agent id and workspace, and its team and role (section 4a) |
| `owner` | the recipient address as sent, and its team and role (section 4a) |
| `sent_at` | the REQUEST's `sent_at` |
| `sell_by` | the REQUEST's `reply_by`, or `none`. A REQUEST with no `reply_by` is itself listed, since R-110 asks director's REQUESTs to carry one |
| `line` | D3 (b) |
| `state` | section 5 |
| `state_at` | the time of the message that set the state |
| `blocked_on` | an address or a ref, when the state is blocked |
| `delivered_at` | when the owner's durable ack floor passed the REQUEST, from the #126 reader. A transport fact, kept apart from `acked` (R-08) |
| `links` | the `refs` of every message in the row's thread |

Rollups are by **role and team**, never by instance id: open asks, unacked
asks, and the oldest open age, per owner role and per asker role. Instance
ids change at every respawn; a rollup keyed on them would scatter one
seat's queue across its generations.

### 4a. Resolving team and role

Presence cannot be the only source. It lives in `AGENT_STATE` with a 90s
TTL (`docs/shim-reference.md:412`), so a REQUEST read more than about 90s
after it was sent, or after its seat exited, finds no row. And the envelope
does not carry everything: `sender.role` is `omitempty`, and `sender.Team`
is `json:"-"` (`envelope.go:15,20`), so the sender's team is never on the
wire.

The reader resolves each party once, in this order, and freezes the result
on the row:

1. **The wire.** The recipient address carries its team
   (`agent://<team>/<id>`, `role://<team>/<role>`), and a `role://` address
   carries its role. `sender.role` gives the sender's role only when set: it
   comes from `DIRECTOR_ROLE`, which is empty by default (`main.go:75`), and
   an empty role is omitted. A `global://` address carries no team, so a
   global party starts at step 2.
2. **The reader's id table.** The ask reader (section 6) runs a pass at
   least every 30s, inside the 90s TTL. Each pass copies every live presence
   key (`presence.<team>.<id>.<instance>`) and its role into an `ids` table
   in `ASK_LEDGER`, and each local durable's filter subjects, read the way
   `teamAndRole` already reads them (`unread.go:511`). Durables outlive
   presence (the inactive threshold is 73h, `bus.go:76`), so this covers a
   seat that exited before the reader first saw it live. Only the
   filter-derived team and role are used; `teamAndRole`'s last step, which
   strips the replica suffix from the agent id to guess a role, is not,
   because instance ids are not identity. An entry is kept 30 days after its
   id was last seen.
3. **A later reply from the same party.** A reply whose `sender.agent_id`
   equals the owner's agent id, and whose `sender.role` is set, completes the
   owner's role. A reply from any other agent id is never used, so a third
   party who answers cannot stamp its own role on the owner.
4. **Otherwise `unresolved`.** The row keeps the address as sent, the rollup
   counts it under the key `unresolved`, and the read output lists it under
   gaps with the reason. Two reasons exist: `no record of this id`, and
   `ambiguous`, when the id table holds more than one (team, role) for the
   same agent id. An ambiguous id is never resolved by picking one. The
   reader never falls back to the agent id as a rollup key.

Resolution happens on the first pass that reads the REQUEST, and is retried
on each pass while any part is unresolved. A resolved value is frozen only
when it came from step 1, from step 2 with a single (team, role), or from
step 3. A reader started after a seat has exited and its durable has
expired cannot resolve that seat's old asks, and says so.

## 5. States

| state | set by | meaning |
|---|---|---|
| sent | the REQUEST | on the bus; nothing from the owner yet |
| acked | an AGREE with `in_reply_to` = ask | the owner said it will do it |
| working | a status message (D1 b): INFORM, `in_reply_to` = ask, `content.type` = `signal`, data `working` | the owner says it is on it |
| blocked | a status message, data `blocked-on <address or ref>` | the owner is waiting on someone else |
| answered | any other INFORM with `in_reply_to` = ask | the owner replied; closes the row |
| refused | a REFUSE | closes the row |
| failed | a FAILURE | closes the row |
| cancelled | a CANCEL from the asker | closes the row |

A follow-up REQUEST whose `in_reply_to` names an open ask belongs to that
row; it does not open a new one. A message that names an ask the ledger has
never seen opens nothing and is counted as an orphan reply, which is the
signal that the ledger missed traffic.

"answered" is the fifth state the commission calls "done". The ledger
records that the owner replied; it does not judge whether the reply finished
the work. Closure stays a human or seat act (SOUL section 8).

## 6. The reader

- **A named long-running reader.** `director-mcp ask-reader` is a loop. It
  runs a pass at least every 30s, and each pass reads new stream sequences,
  copies presence and durable filters into the id table, and updates rows.
  It writes only its own store (`ASK_LEDGER` and the JSON file). `director-mcp
  asks` (section 7) is a separate on-demand read of that store and runs no
  pass of its own.
- **Its own downtime is a gap.** Each pass records its start time. When two
  passes are more than 90s apart, the presence TTL, the read output lists
  `reader down <from> to <to>` under gaps: a seat that came and went inside
  that span, with its durable gone, may show as unresolved.
- **Read-only on the bus.** It reads messages by sequence (`stream get`),
  keeps its own last sequence per stream, and creates no consumer, so it can
  never move anyone's ack floor. The `unread` reader already works this way
  for consumer state.
- **Streams:** `AGENT_INBOX` on each local broker, and `GLOBAL_TO_DIRECTOR`
  and `GLOBAL_TO_<cluster>` through the hub domain, as `global.go` reaches
  them.
- **Its own store:** an `ASK_LEDGER` key-value bucket on the local broker,
  plus a JSON file it rewrites atomically after each pass. Rows must outlive
  the streams, whose max age is 72h on kinu's `AGENT_INBOX`
  (`asks-die-unread.md` section 2) and 24h on marvel-managed streams
  (marvel `internal/bus/declared.go:107`). Closed rows are kept 30 days, then
  dropped from the bucket.
- **Open rows are bounded too, never silently.** An open row with no message
  for 72h, the inbox's own max age, leaves the alarm list and is counted
  under `stale` in gaps (oldest age shown; `--all` lists them). A stale row
  is dropped 30 days later, and each drop is counted in that pass's gaps.
  Anyone can close a row by replying to it, which takes it off every list.
- **Its principal is the open decision.** Reading every inbox subject needs
  a broker user that today's per-team users do not grant. Under R-95 the
  credential decides what a principal may reach, and the operator sets that
  policy, so this user is the operator's to grant. It would be read-only on
  the streams and write-only to `ASK_LEDGER`. The grant is wider than it
  sounds: reading by sequence is the stream-level `$JS.API.STREAM.MSG.GET`
  (or `DIRECT.GET`) permission on a stream, which cannot be narrowed by
  subject, so it reads every seat's inbox on that stream, bodies included.
  The reader keeps only D3's capped line, but the principal can read
  everything. Until it exists, the
  reader runs under director's own user and sees only what that user can
  read. The read output says which streams it could not read, so a partial
  view is never shown as the fleet.
- **Without marvel:** it needs only NATS and the director binary (ADR-005).
  Team and role come from the wire and presence (section 4a); marvel is
  not consulted.

## 7. What it reports

`director-mcp asks` (and `--json`), modelled on `unread`:

1. **Alarms first:** open rows in `sent` past 15 minutes, or past their
   `sell_by`, oldest first, as `<asker role> -> <owner role>: <line> (sent
   2h46m ago, unacked)`. A row past `sell_by` names the seat that set it,
   because that seat re-checks the ask; a passed `sell_by` triggers no
   action.
2. **Blocked on whom:** each blocked row, followed along `blocked_on` to the
   rows that address owns, printed as a chain and stopped at a repeat, which
   is reported as a cycle.
3. **Rollup** per owner role: open, unacked, blocked, oldest open age.
4. **Gaps:** streams not read, orphan replies, REQUESTs without `reply_by`.

The JSON form carries every row and the rollup. That is the interface
marvel's QUEUE column and team rollup read, and the board's W5 reads. This
design fixes the fields; how marvel shows them is marvel's.

The 15-minute threshold lives in one table in the reader, as the board's
unmoved thresholds do, so the operator changes it in one place.

## 8. Parts, in order

| part | what | depends on |
|---|---|---|
| A1 | `ask-reader` loop over `AGENT_INBOX` with the id table (presence and durable filters), states from existing performatives (D1 a), the bucket and JSON file, and the `asks` read output | none |
| A2 | the global tier streams | A1 |
| A3 | the status message (D1 b): a `report_status` tool in the shim, and the skill line that tells seats to use it | A1 |
| A4 | `delivered_at` from the #126 reader | A1, #126 slice M |
| A5 | the reader's own principal | the operator's grant (section 6) |

Each part is its own ticket, with dependency edges, and no parent.

## 9. Tests (red first)

From recorded envelopes, no broker:
- a REQUEST alone is `sent`, and is an alarm past 15 minutes;
- AGREE, then status `working`, then INFORM, moves it to acked, working,
  answered;
- `blocked-on` an address whose own open row is blocked on the first prints
  a cycle;
- a reply to an unknown ask is an orphan, and opens no row;
- a follow-up REQUEST joins its row;
- a REQUEST with no `reply_by` is listed under gaps;
- a stream the reader cannot read is listed under gaps, and the rollup says
  partial;
- an owner whose presence has expired, and who is not in the id table,
  rolls up under `unresolved`, is listed under gaps, and is never keyed by
  its agent id; the same REQUEST with the owner in the id table resolves;
- a `role://` recipient resolves team and role from the address alone, with
  no presence;
- an open row with no message for 72h leaves the alarms and is counted as
  stale; 30 days later it is dropped from the bucket, and that pass's gaps
  count one drop;
- a reply to an unresolved ask from a different agent id than the owner, with
  `sender.role` set, leaves the owner unresolved; the same reply from the
  owner's own agent id resolves it; a reply with no `sender.role` resolves
  nothing;
- an agent id with two (team, role) entries in the id table rolls up as
  `unresolved: ambiguous`, never as either entry;
- an owner with no presence but a live durable whose filters name its team
  and role resolves from the durable, and an agent id with only a replica
  suffix to go on does not;
- a `global://` party with no id-table entry is `unresolved`;
- two passes 5 minutes apart list `reader down` under gaps.

With a scratch broker: the reader creates no consumer (`consumer ls` is
unchanged across a pass), and a second pass over the same sequences changes
no row.
