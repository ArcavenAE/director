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

### M2. A named state for a seat that reads outside its durable

A live row is classed `reads-outside-durable` when both hold:
- presence has been live for at least 10 minutes, and
- the durable has delivered nothing since that presence began (its
  `Delivered.Consumer` is 0, or `Delivered.Last` is older than the presence
  start), while it has pending mail.

Such a row shows no age:

```
seat-b/1  reads-outside-durable  pending 389 (not unread mail: this seat reads its inbox without the shim's durable)
```

`--json` carries `state: "reads-outside-durable"` and omits
`oldest_unread_age_seconds`, so the threshold in M4 never fires on it.
Director's own row and any seat that drains with `nats stream get` fall in
this class. The fix for such a seat is to read through `wait_for_message` or
`inbox_summary`, which ack and advance the floor; the reader names the class,
it does not change the seat.

The other live states are `reading` (pending 0, or delivered within the
window) and `behind` (pending > 0, reading through the durable, oldest
unread age shown). Only `behind` carries an age.

### M3. The global tier

`unread --global` runs the same report against the hub: every
`mcp_global_` durable on each `GLOBAL_TO_*` stream, through the hub's
JetStream domain, with presence from `GLOBAL_PRESENCE`. The output is one
section per stream. It reuses M1 and M2 unchanged. Default is local only, so
a host with no hub configured behaves as today; `--global` with no hub
reachable prints the reason and exits 0.

### M4. An age threshold for the battery

`--older-than <dur>` (default unset) marks each `behind` row whose oldest
unread message is older than the threshold, in text with a leading `!` and in
JSON as `over_threshold: true`, and adds a summary count. Exit stays 0: this
is a diagnostic the battery reads and reports, not a gate (ADR-007). The
battery's live check (a new D-row, read-only) runs `unread --json
--older-than 1h` on both tiers and reports the over-threshold rows by
address. `reads-outside-durable` rows are listed separately and never
counted over threshold.

### M5. A role rollup

`--by-role` groups live rows by team and role (from the agent id, and the
`role.<role>.inbox` filter where a durable carries one) and prints one line
per role: live holders, total pending, the oldest unread age across holders
in `behind`, and how many holders are `reads-outside-durable`. A message to a
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

## 5. Tests (red first)

1. **Collapse.** Scratch broker: one live session plus three durables of
   ended sessions with mail. Default view shows one live row and one summary
   line (`dead durables: 3, pending N`); `--all` shows four rows; `--json` has
   four entries with `live` set.
2. **Reads outside.** A live session whose durable delivers nothing for 10
   minutes (clock injected) while three messages arrive is
   `reads-outside-durable` with no age; after it drains through the shim it
   is `reading`. A session reading through the durable with mail waiting is
   `behind` with an age.
3. **Threshold.** With `--older-than 1h`, a `behind` row 2h old is marked and
   counted; a `reads-outside-durable` row with older mail is not; exit 0 in
   every case.
4. **Global.** Scratch hub and leaf: a `GLOBAL_TO_<cluster>` durable with
   pending mail appears under `--global` and not without it; with no hub
   reachable, `--global` prints the reason and exits 0.
5. **Role rollup.** Two live holders of one role, one `behind` and one
   `reads-outside-durable`: one role line, holders 2, the oldest age from the
   `behind` holder only, outside count 1.
6. **Still read-only.** In every test, the consumer count and the presence
   bucket are unchanged after the run.
7. **Rev mark.** A binary whose stamp comes from an enclosing repo carries the
   ldteb mark; a plain-clone build does not.

## 6. Rulings needed (operator, via director)

| # | question | default |
|---|---|---|
| 1 | Collapse dead durables by default, `--all` to list (M1) | yes |
| 2 | Pruning dead durables is a separate, later verb, not part of the reader (M1) | yes |
| 3 | The 10-minute window that classes a live seat as reading outside its durable (M2) | 10m |
