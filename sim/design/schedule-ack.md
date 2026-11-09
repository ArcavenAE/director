# A scheduled firing with no acked result

A scheduled role (a headless role that marvel fires on a cron) can run and succeed while nobody ever reads what it
produced. marvel raises its own alarm when no run succeeds within `stale_after`; it cannot know whether a result
reached the supervisor and was acknowledged. This adds the sweep's reminder for that gap: one line under
"waiting on you" when the last due firing of a scheduled role has no succeeded run with an acked result by due
plus 2 hours. It reminds only. It never runs, respawns or gates anything, and it is never a required check.

**Status: the reader and its input contract, built against fixtures.** marvel already returns the describe block this
reads. Two inputs do not exist yet: the results file with its sha256 line (the sweep prompt step, ticket H-3) and the
supervisor's ack row, whose shape `sim/design/route-ledger.md` defines and this reader only reads. Nothing here is wired into a live sweep, and the contract below
changes only by a ruling.

## The inputs

1. **marvel's `describe team` JSON**, read from a file the caller writes
   (`marvel describe team WORKSPACE/TEAM > FILE`). The reader takes only the `Schedules` array and, from each
   scheduled role, these fields, as marvel's `scheduleDescription` and `runDescription` return them:
   - `role`, `stale` (marvel's own freshness alarm), and `firing`: the current firing's id;
   - `runs[]`, newest first, each with `firing`, `outcome` (`succeeded`, `failed` or `cancelled`) and the times marvel
     records. Only `firing` and `outcome` are read.
2. **The results directory**, per firing: `<firing>.md`, the results the sweep wrote, and `<firing>.md.sha256`, one
   `sha256sum` line (the hash, then the file name). The first word of that line is the results hash. The reader does
   not recompute the hash of the file; it compares the ack's hash with the recorded line.
3. **A route ledger**: JSON lines, where an ack is the scheduled-firing ack row
   `{scheduled_role, firing_id, results_sha256, acked_at}`. `sim/design/route-ledger.md` defines that row; this reader
   only reads it. The supervisor writes it; the sweep never does.

The firing id is marvel's `FiringID`: the nominal due time in UTC as `YYYYMMDDTHHMMSSZ`. The reader reads the due
time from the id, so it never evaluates cron. A schedule that has not fired has no `firing` and no line.

## The rule

For each scheduled role, with `now` supplied by the caller, take the **last due firing**: the newest firing past its
grace period (due plus 2 hours at or before `now`) among the current `firing` and the firings of the `runs`. A
schedule that fires more often than every 2 hours has its current firing inside grace, so checking only the current
one would never print a line. A firing the document does not name is not guessed, since cron is never evaluated: a
firing that left no run record and is no longer current cannot be seen. When any of these holds for that firing,
print one line.

| reason | when |
|---|---|
| `no succeeded run` | no run of that firing has outcome `succeeded` (this covers a firing still running or retrying, and failed or cancelled runs) |
| `no results file` | a succeeded run, but no `<firing>.md` |
| `results file has no sha256 line` | the file exists; its `.sha256` is missing or its first word is not a 64-digit hash |
| `no ack in the route ledger` | no ledger row for that role and firing carries `acked_at` |
| `the ack in the route ledger does not match the results` | such rows exist, and none has the recorded hash |

A line reads `scheduled: ROLE firing ID due TIME: REASON`, with ` (schedule is stale)` added while marvel's `stale`
flag is set (the check still runs: an unacked firing and a stale schedule are different facts). Several ledger
rows for one firing are fine: any matching row acks it.

An input the reader cannot read prints `scheduled: UNKNOWN (describe unreadable)`,
`scheduled: UNKNOWN (route ledger unreadable)` or `scheduled: UNKNOWN (ROLE: firing id unreadable)`, and never a
silent pass. It exits 0 whatever it finds; a usage error is exit 2.

## What waits on others

- The sweep prompt writing `<firing>.md` and `<firing>.md.sha256` (H-3), and the supervisor writing the ack row
  (the supervisor role definition names its four fields). Until both exist, a live run of this reader finds no
  results and no ack, which is the truth.
- Wiring `schedule-ack check` into the sweep's waiting-on-you block. That block's script is changed by other work
  in flight, so the wiring is a later change; `skills/director/SKILL.md` says how the sweep runs it.

## Proof

`verify-schedule-ack.sh` covers the ticket's four acceptance cases, the 2-hour edge (12:59:59Z, 13:00:00Z), failed
and cancelled runs, a missing results file or sha256 line, a ledger row for another role, firing or without
`acked_at`, two roles in one document, unreadable inputs, and that nothing is written and no hash or result text
reaches the output. Its fixtures are generated with placeholder values; `fixtures/schedule-ack.describe.example.json`
is a describe document in marvel's shape.
