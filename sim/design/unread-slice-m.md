# Unread reader, slice M: live rows first, the global tier, a threshold, a role rollup

Status: design for review, 2026-10-02. Owner: the architect role.
Tracks director#126 (LR-3). Slice S shipped in director#137 (`director-mcp
unread`, `rev` in presence). Design only; builders follow review.

## 1. Why

`director-mcp unread` answers "how long has mail sat unread at an address",
but on a live fleet the answer is buried. Run read-only on 2026-10-02:

- **Dead rows drown live ones.** The report is mostly durables of sessions
  that are no longer live (`no presence`), about two dozen for one address,
  each with its own pending count. The live seat's row is one line among
  them.
- **Some live rows are not unread mail.** A supervisor seat that reads its
  inbox with `nats stream get`, not through the shim, shows `pending 389,
  oldest 57h` on its live durable. The brief author's comment on #126 found
  the same for director: its durable has delivered nothing, so its floor
  never moves. So "reads outside its durable" is a class of seat, not one
  seat, and today the reader presents it as a 57-hour backlog.

Slice M fixes both, and adds the three items #126 left for later: the global
tier, an age threshold the diagnostics battery reads, and a role rollup.

## 2. Today (checked at main c0038b1)

- `probe/nats-phase-0/director-mcp/unread.go`: one row per `mcp_` durable on
  AGENT_INBOX: pending (`NumPending + NumAckPending`), ack floor, oldest
  unread age, and the presence row (state, ts, rev) or the note `no presence:
  this durable belongs to a session that is not live`. Exit 0 always. It
  creates no consumer and writes nothing.
- The global tier is separate: per-session durables `mcp_global_<agent>_<n>`
  on the hub streams (`GLOBAL_TO_DIRECTOR`, `GLOBAL_TO_<cluster>`), reached
  through the hub's JetStream domain over the leaf (`global.go`). `unread`
  does not read it.
- The diagnostics battery is designed (`sim/design/ops-diagnostics-battery.md`)
  and reads reports; it has no unread threshold.

## 3. Design

### M1. Live rows first; dead rows collapsed, never hidden

The default view lists, per address, the live row (presence present) and then
one summary line for that address's dead durables:

```
seat-a/2  pending 0  floor 812  rev 92d4eb6
  dead durables: 23, pending 41 in total, oldest 3d (--all to list)
```

`--all` restores today's one-row-per-durable view; `--json` always carries
every durable, with a `live` boolean, so nothing is lost to a consumer.
Collapsing is presentation only: pending mail on a dead durable is real (it
was addressed to an instance that will never read it), so the count stays
on the summary line.

**Pruning is out of this slice.** Deleting a dead durable is a write to the
broker, and the reader's contract is that it writes nothing. A prune belongs
in a separate verb, with its own design and an operator ruling, because it
discards the record of undelivered mail. Recommended later: `director-mcp
prune-durables --older-than 72h --dry-run` by default.

### M2. Named states for a seat whose durable is idle

A live seat's durable can deliver nothing while mail waits for two reasons
the durable alone cannot tell apart: the seat reads its inbox some other way
(director today; a seat that drains with `nats stream get`), or the seat
reads nothing at all (idle, stuck in a turn, never told to poll). Presence
does not separate them either: the shim renews it on its own 30-second
timer, never on a model call (R-56). So the default must be the alarming
reading, and the reassuring one needs positive evidence.

**The anchor is the durable's `Created`.** The per-session durable
`mcp_<agent>_<instance>` is created at shim start, so its consumer-info
`Created` is the session's start. The presence `ts` is rewritten on every
write and gives no start time; it is not used for this.

**`durable-idle` (the default).** A live row whose durable is at least 10
minutes old (from `Created`), has delivered nothing since (`Delivered.Consumer`
is 0), and has pending mail. The row keeps its age:

```
seat-b/1  durable-idle  pending 389  oldest 57h  (this seat reads elsewhere, or not at all)
```

M4 counts it over threshold like `behind`, and lists it under its own
heading so a reader sees the class.

**`reads-outside-durable` (only with evidence).** A `durable-idle` row is
reclassed when **this session** has sent at least one message, since its
durable's `Created`, whose `in_reply_to` names a message still pending on
that durable. A session that answers mail it never acked read it somewhere
else.

**The evidence must name the session, not the agent.** `sender.agent_id`
names the agent, and two live sessions of one agent share it, so live
session 1 replying to message M would otherwise reclass deaf session 2. A
process that only sends under the agent id would do the same. The envelope's
`sender.session` is not the field for this: the canonical schema defines it
as the harness session UUID (R-71, R-73), which marvel sets, and the shim's
instance is a ULID naming one shim process. A new optional field,
`sender.instance`, carries the instance (`sim/design/envelope-sender-instance.md`).
Part E1 fills it on every send. Evidence then counts only when
`sender.instance` equals this durable's instance. A message with no
`sender.instance` (any shim before E1, a send-only process, a sibling) never
counts. So until E1 ships, no row is upgraded and every idle durable stays
`durable-idle`, which is the alarming default.

**The instance is self-asserted.** Any process holding the agent's bus
credentials can write any value in `sender.instance`. So
`reads-outside-durable` rests on the sender's own claim, not on proof, and
the reader says so in its `evidence`. Whenever the claim is absent, the row
keeps the default (`durable-idle`, with its age).

**How the pending set is enumerated.** The pending ids are the messages on
the durable's filter subjects from its ack floor plus one (or its
`OptStartSeq`, when later) to the stream's last sequence, walked with
`GetMsg` as slice S already does for the oldest age. A resumed durable can
hold pending messages older than its `Created`. They are in the pending set
all the same, because the set comes from the floor, not from `Created`.

**Finding the replies.** Subjects are keyed by recipient
(`agent.<ws>.<team>.<id>.inbox`), so there is no per-sender subject to
filter. The reader scans AGENT_INBOX by sequence from the first message at
or after the durable's `Created`, decodes each envelope, and keeps those
whose `sender.instance` is this durable's instance and whose `in_reply_to` is
in the pending set. It is read-only (`GetMsg` by sequence), runs only for
`durable-idle` rows, and is capped at the last 2,000 messages per run. With
`--global` it scans the hub streams the same way.

**A truncated scan never upgrades a row.** When the cap cuts the scan short
of `Created`, the row stays `durable-idle` even if a matching reply was found
in the part scanned, and `--json` carries `scan_truncated: true`. A partial
scan proves less than the rule asks for, so the default holds.

It records the evidence:

```
seat-c/1  reads-outside-durable  pending 41 (answered 3 of them, latest 12m ago; not unread mail)
```

`--json` carries `state`, `evidence` (`in_reply_to` ids matched, the
latest send time) and `scan_truncated`. This row shows no age and M4 does not count it. The fix
for such a seat is to read through `wait_for_message` or `inbox_summary`,
which ack and advance the floor; the reader names the class, it does not
change the seat.

An operator-ruled allow-list of addresses known to read outside (ruling 4)
can sit on top of this; without it, nothing is reassured by default.

The other live states are `reading` (pending 0, or delivered since `Created`
within the window) and `behind` (pending > 0, reading through the durable,
oldest unread age shown).

### M3. The global tier

`unread --global` runs the same report against the hub: every
`mcp_global_` durable on each `GLOBAL_TO_*` stream, through the hub's
JetStream domain, with presence from `GLOBAL_PRESENCE`. The output is one
section per stream. It reuses M1 and M2 unchanged. Default is local only, so
a host with no hub configured behaves as today; `--global` with no hub
reachable prints the reason and exits 0.

### M4. An age threshold for the battery

`--older-than <dur>` (default unset) marks each `behind` or `durable-idle` row whose oldest
unread message is older than the threshold, in text with a leading `!` and in
JSON as `over_threshold: true`, and adds a summary count. Exit stays 0: this
is a diagnostic the battery reads and reports, not a gate (ADR-007). The
battery's live check (a new D-row, read-only) runs `unread --json
--older-than 1h` on both tiers and reports the over-threshold rows by
address, with `durable-idle` rows under their own heading.
`reads-outside-durable` rows are listed separately, with their evidence, and
never counted over threshold.

### M5. A role rollup

`--by-role` groups live rows by team and role (from the agent id, and the
`role.<role>.inbox` filter where a durable carries one) and prints one line
per role: live holders, total pending, the oldest unread age across holders
in `behind` or `durable-idle`, and how many holders are in each named state. A message to a
role address is read by any holder, so the role line answers "is anyone in
this role reading" without the reader judging which instance should have.

## 4. Pairing with aae-orc-ldteb (worktree builds report the wrong rev)

`rev` in presence is the shim's own VCS stamp. A shim built inside a git
worktree stamps the enclosing repository's revision (finding-018, in
director#184), so the reader can show a confident, wrong rev. Slice M does
not fix the stamp; ldteb does (an `-ldflags` revision from `git rev-parse` at
build, or a refusal to stamp from a VCS root outside this repo). Until it
lands, the reader marks a rev it cannot trust: when the binary's module path
is not under the stamped VCS root, the row shows `rev <sha> (stamp from an
enclosing repo, see ldteb)`. When ldteb lands, that mark never fires. #68's
release (a pinned, reproducible build) removes the case for installed seats.

## 4a. Parts

| part | what | depends on |
|---|---|---|
| E1 | the shim sets `sender.instance` to its instance on every send | the schema field in marvel, then beadle re-pinned and deployed (`envelope-sender-instance.md` section 4) |
| M1 to M5 | the reader changes above | none; M2's upgrade path only fires after E1 |

## 5. Tests (red first)

1. **Collapse.** Scratch broker: one live session plus three durables of
   ended sessions with mail. Default view shows one live row and one summary
   line (`dead durables: 3, pending N`); `--all` shows four rows; `--json` has
   four entries with `live` set.
2. **Idle durable and reads outside.** Clock injected; durable `Created`
   used as the start.
   - **Deaf:** a live session that reads nothing, with three messages waiting
     past 10 minutes and no outbound replies, is `durable-idle` with an age,
     never "not unread mail"; with `--older-than 5m` it appears in the
     over-threshold output.
   - **Correlation:** the same session sends a reply whose `in_reply_to`
     names one of the pending messages; it becomes `reads-outside-durable`
     with that id as evidence and no age, and is not counted over threshold.
   - **Siblings and senders (negative):** session 1 of an agent is live and
     reading; session 2 of the same agent is deaf with message M pending.
     Session 1 replies to M. Session 2 stays `durable-idle`. A process that
     publishes a reply to M with the agent id and no `sender.instance` leaves
     it `durable-idle` too. A reply that carries M's agent id and a
     `sender.session` equal to the instance, but no `sender.instance`, also
     leaves it `durable-idle`. Before E1, the correlation case stays
     `durable-idle` (no instance on the envelope).
   - **Truncated:** with the cap set to 10 and the matching reply within the
     last 10 messages but `Created` earlier, the row stays `durable-idle`
     and `--json` shows `scan_truncated: true`.
   - **Reading:** after it drains through the shim it is `reading`. A session
     reading through the durable with mail waiting is `behind` with an age.
   - **Anchor:** rewriting the presence `ts` does not change the state; the
     10-minute window counts from the durable's `Created`.
3. **Threshold.** With `--older-than 1h`, a `behind` row and a `durable-idle`
   row each 2h old are marked and counted; a `reads-outside-durable` row with
   older mail is not; exit 0 in every case.
4. **Global.** Scratch hub and leaf: a `GLOBAL_TO_<cluster>` durable with
   pending mail appears under `--global` and not without it; with no hub
   reachable, `--global` prints the reason and exits 0.
5. **Role rollup.** Two live holders of one role, one `behind` and one
   `reads-outside-durable` with evidence: one role line, holders 2, the oldest
   age from the `behind` holder only, outside count 1.
6. **Still read-only.** In every test, the consumer count and the presence
   bucket are unchanged after the run.
7. **Rev mark.** A binary whose stamp comes from an enclosing repo carries the
   ldteb mark; a plain-clone build does not.

## 6. Rulings needed (operator, via director)

| # | question | default |
|---|---|---|
| 1 | Collapse dead durables by default, `--all` to list (M1) | yes |
| 2 | Pruning dead durables is a separate, later verb, not part of the reader (M1) | yes |
| 3 | The 10-minute window, from the durable's `Created`, before a live seat's idle durable is named (M2) | 10m |
| 4 | An allow-list of addresses known to read outside their durable, on top of the evidence rule (M2) | none; director's row is named by evidence or stays `durable-idle` |
