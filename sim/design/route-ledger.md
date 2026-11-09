# Route ledger: one shape for every supervisor

Supervisors keep route ledgers in different shapes, so fleet queue depth, work-item age and closure cannot be
read from them the same way. The resource-map brief (`_kos/probes/brief-resource-map.yaml`, recommendation 2)
asks every supervisor to record routed, acked and done times and the queue depth at route in one shape, which
unblocks R-115 and R-116. This document defines that shape and a read-only checker for it. It moves no
supervisor onto the shape; that is a separate step per supervisor.

## Where the shapes come from

- **route-record@1** is the supervisor team-supervision process's route record
  (`supervisor/route-record@1`, in the private wardrobe role library). Its fields are restated here, since this
  repository cannot link a private one.
- **route-event@1 has no written definition that this work could find** in wardrobe, director or marvel. The
  brief names it only by name. The definition below is **derived from rows**: it is read off the event, close and
  note rows in the aae-orc supervisor lineage's ledger, the best-kept ledger readable at the time of writing. The
  brief's own well-kept ledger (604 routed, 485 acked) was not located and is **unverified**; it was probably a
  ledger on another team's host.

## route-record@1: one row per routing decision

| field | type | rule |
|---|---|---|
| `ask_id` | text | required; the ask the routing answers |
| `routed_to` | text | required; a role address with the epoch, or `none` for a no-route |
| `basis` | text | required; why this seat, in one line |
| `principal` | text | required; who asked |
| `supervisor_epoch` | text | required; the supervisor generation that routed it |
| `routed_at` | time | required; UTC, `YYYY-MM-DDTHH:MM:SSZ` |
| `alternatives` | list of text | optional; the seats considered |
| `depth_at_route` | integer, 0 or more, or null | optional; the destination's queue depth when routed |
| `depth_observed` | integer, 0 or more, or null | optional; the depth read at ack, if read |
| `acked_at` | time | optional; when the destination took it |
| `cancelled_at` | time | optional; when the routing was withdrawn |
| `no_route` | object or text | optional; the reason when `routed_to` is `none` |

A record with no `depth_at_route` is **depth unknown**, never zero. Fields a local ledger adds beyond these are
allowed and ignored.

## route-event@1: one row per later event on an ask

| field | type | rule |
|---|---|---|
| `ask_id` | text | required |
| `supervisor_epoch` | text | required |
| `at` | time | required; UTC |
| `kind` | `ack`, `relay`, `note`, `close` or `cancel` | required |
| `text` | text | required for every kind except `close` |
| `acked_at` | time | required for `ack` |
| `closed_at` | time | required for `close` |
| `result` | text | required for `close`; what finished the ask (a merged PR, a ruling) |

Closure is a `close` event, so "done" is a time and a result and not an absence. An ask is **open** when it was
routed and has neither a `close` nor a `cancel`.

## Legacy rows

Ledgers held these shapes before this one; the checker accepts and counts them, and a supervisor moving onto
the shape stops writing them:

- `{ask_id, event, supervisor_epoch[, at | acked_at]}`: free-text `event`; with `acked_at` it counts as an ack;
- `{ask_id, result}` and `{ask_id, closed_at, result}`: a close, with or without a time;
- `{[ask_id,] at, note}`: a note.

## What reads from it

- **Queue depth** is `depth_at_route` of the latest record per destination; unknown counts are reported, never
  filled in.
- **Work-item age** is `now - routed_at` for an open ask.
- **Closure** is the `close` events (and legacy results) over the routed asks.
- **Duty cycle** needs acked and closed times together, which the shape now carries.

## The checker

`skills/director/scripts/route-ledger check FILE [--json]` reads one file and writes nothing. It prints

```
route-ledger: rows N, asks N, routed N, acked N, closed N, cancelled N, open N, depth known K of R, event@1 N, legacy N, refused N
```

A refused row prints `route-ledger: refused: row <line>: <field>:<reason>` on stderr, in fixed words and never the
value, and the other rows are still read. Exit 0 when nothing was refused, 1 when a row was, 2 for a usage error
or an unreadable file. `verify-route-ledger.sh` proves it against `fixtures/route-ledger.example.jsonl`
(placeholder ids only), red first, with a mutant for each count and refusal.

## Not here

Moving each supervisor onto the shape, and any change to a supervisor role file, are separate steps (a
role-library change goes by its own pull request). The ledger's home (a file per seat, or marvel's store) is
undecided and out of scope.
