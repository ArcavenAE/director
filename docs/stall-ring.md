# The stall ring

A seat whose mail waits with no wait open does nothing until someone types at it. The stall ring reminds such a seat once, so nobody has to watch the roster for it. It is a reminder only (SOUL section 8): it types one fixed line and Enter, and decides nothing.

## When it runs

It is off. It runs only on the director seat's shim, and only with `DIRECTOR_STALL_RING=1` (exactly `1`). Turning it on anywhere live is the director's act.

## Rules

- A seat is a candidate when its published cue reads `stalled`: the cue is on, no wait is open, and the oldest pending message is at least ten minutes old. The bound is the cue's own (`defaultPendingAgeBound`), not a second number.
- A seat is rung at most once in any hour. Rings made by hand count, and so does a ring whose line `--verify` could not see.
- Before it types, it reads the seat's pane with `marvel capture` and `composer-check`. Only the verdict `EMPTY` lets a ring go. Any other verdict, or a failed check, is a logged refusal.
- It types the fixed line with `marvel inject <session-key> <line> --ring --verify`, then presses Enter with `marvel inject <session-key> '' --enter`. No other key is ever sent, and nothing is cleared. Marvel holds a ring claim for 120 seconds; a refused claim is logged and not retried.
- The line carries nothing from any message.

## The log

`rings.jsonl` under `rings.lock` in the director state directory, the file the hand path (`dws ring`) writes. One row per event:

| event | result | counts toward the hour |
|---|---|---|
| `ring-attempt` | none | no (written before the inject; if it cannot be written, nothing is sent) |
| `ring` | `sent` or `unverified`, or none for a hand ring | yes |
| `ring-refused` | `refused-composer`, `refused-claim` or `error` | no |

A hand row counts for a seat when its `seat` text names the seat's agent id as a whole token.
