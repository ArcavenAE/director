# The stall ring

A seat whose mail waits with no wait open does nothing until someone types at it. The stall ring reminds such a seat once, so nobody has to watch the roster for it. It is a reminder only (SOUL section 8): it types one fixed line and Enter, and decides nothing.

## When it runs

It is off. It runs only on the director seat's shim, and only with `DIRECTOR_STALL_RING=1` (exactly `1`). Turning it on anywhere live is the director's act.

## Rules

- A seat is a candidate when its published cue reads `stalled`: the cue is on, no wait is open, and the oldest pending message is at least ten minutes old. The bound is the cue's own (`defaultPendingAgeBound`), not a second number.
- A seat is rung at most once in any hour. Rings made by hand count, and so does a ring whose line `--verify` could not see.
- Before it types, it reads the seat's pane with `marvel capture` and `composer-check`. Only the verdicts `EMPTY` and `SUGGESTION` (an idle composer holding only a dim suggestion, which typed text replaces) let a ring go. Any other verdict, or a failed check, is a logged refusal.
- The marvel session key comes from `marvel get sessions -o json`, read once a pass: the session whose `Name` equals the seat's agent id exactly, in the workspace marvel reports. An unknown or ambiguous name, or a failed read, rings nothing and logs a refusal (one row for a failed read, not one per seat).
- A seat whose ring ended in an error result is left alone for 15 minutes. A refusal (composer, claim, session) starts no backoff. The backoff is in memory, so a restart of the shim clears it.
- Rows the ring writes on every pass, with no backoff, by design in this version: a seat with a draft in its composer writes one `refused-composer` row (about one a minute, and the sign that the seat is stuck); a stalled seat that is not a marvel session (an interactive seat whose shim publishes presence) writes `refused-unknown-session`; and while marvel is down the sessions read fails first, so an outage writes one `refused-sessions` row a pass, about 1,440 a day. Backing these off is a possible follow-up.
- It types the fixed line with `marvel inject <session-key> <line> --ring --verify`, then presses Enter with `marvel inject <session-key> '' --enter`. No other key is ever sent, and nothing is cleared. Marvel holds a ring claim for 120 seconds; a refused claim is logged and not retried.
- The line carries nothing from any message.

## The log

`rings.jsonl` under `rings.lock` in the director state directory, the file the hand path (`dws ring`) writes. One row per event:

| event | result | counts toward the hour |
|---|---|---|
| `ring-attempt` | none | no (written before the inject; if it cannot be written, nothing is sent) |
| `ring` | `sent` or `unverified`, or none for a hand ring | yes |
| `ring-refused` | `refused-composer`, `refused-claim`, `refused-unknown-session`, `refused-ambiguous-session`, `refused-sessions` or `error` | no |

A seat that keeps a draft in its composer writes one `refused-composer` row a pass, about one a minute; that row is the visible sign that a seat is stuck, and it is not backed off.

A hand row counts for a seat when its `seat` text names the seat's agent id as a whole token.
