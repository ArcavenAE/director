# Seat decisions in the ledger: record, edges, window, two paths

Status: design for review, 2026-10-20. Owner: the architect role. Docs only;
nothing is built by this document. The builds are split in section 9.

The operator's queue holds small, reversible choices next to serious ones, so
the serious ones wait and seats idle. The operator ruled that a seat may
adopt its own recommendation when it passes a written test, and that every
such decision is recorded, mapped to the work that builds on it, held to a
window, and confirmed or overridden by the operator by hand. This design puts
all of that in director's workstream ledger (`dws`) and its board page, so an
override arrives with the list of work it touches, and the daily pass is
ordered by what each answer frees.

## 1. What was ruled

Six Desk cards, `seat-decide-*`. The operator's chosen option is quoted where
it sets a rule this design must keep.

| card | ruled | what it fixes here |
|---|---|---|
| seat-decide-test | option 2: the eight-line test, for a two-week trial | which decisions exist at all |
| seat-decide-cost | option 1: an estimate first, at most 4 seat-hours in total including dependent work, no new spending | the estimate and actual fields |
| seat-decide-lifecycle | option 1: "The decision is a `dws` record: the option taken, the reason, how to undo it, the seat, the estimate and the actual cost. A Desk card mirrors it with the status decided-by-seat. Director's daily line lists every unconfirmed decision in card 4's order. ... A skipped decision stays unconfirmed. Nothing confirms on silence or on a clock." | section 3 |
| seat-decide-map | option 1: "a row may name several decisions it waits on, including decided-by-seat ones, not just one open card. Then a `board-html` view draws the map and lists decisions by how much work each one holds up." | sections 4 and 7 |
| seat-decide-window | option 1: "Dependent changes may be built and reviewed up to ready, but none is merged until you confirm. At most three may exist for one decision, and none may start more than five days after the decision. When the window is full, the seat stops, and the decision moves to the top of your list." Order: dependents waiting, then age, then the seat-hours an override would throw away | sections 5 and 6 |
| seat-decide-parallel | option 1: build both options only when all four parts hold; two paths at most; "The losing path is closed as a draft, and its lessons go into the winning PR's notes. You still confirm the winner under card 5." | section 8 |

The trial already runs as practice: a seat sends one `seat-decision@1`
message to director before it acts, and director builds a daily line from its
relay log. This design keeps that message as the input and gives it a home.

## 2. What the premises are (checked 2026-10-20, director main 935e0f5)

- **`dws` has one writer table, `{director, refresh}`** (`dws`, `ACTORS`).
  A seat does not write the ledger. So director writes every decision event,
  from the seat's message, and the seat stays the author of the content.
- **A row links to one Desk card today,** through `--blocked operator --ask
  <card>`, and only while that card is open in a fresh Desk snapshot (K13).
- **Links are typed** (`pr:`, `issue:`, `bd:`, `ask:`); refresh reads the
  first three and names every link it skips.
- **`merge-guard` is the read before every director merge** (R-152), and it
  already stops on operator exclusions read from `rulings.jsonl`. It is where
  "nothing merged until you confirm" can be enforced without a new gate.
- **The Desk page's statuses are open, relayed and closed,** and only a
  session can write its data (finding glst). The decided-by-seat mirror is a
  Desk page change director makes, not a repo change.
- **No daily line exists in the code.** The trial's line is typed by
  director from the relay log.

## 3. The decision record (card 5)

Decisions live in the same event file as the rows, `workstreams.jsonl`, under
the same lock, as events with `"event": "decision"`. One file and one lock
means a row's wait edge and the decision it names are checked in one fold:
`dws` can refuse an edge to a decision it does not hold.

**Fields**, all required unless marked, each passed through `checked()` so
no instance id is stored:

| field | from the `seat-decision@1` line | notes |
|---|---|---|
| `id` | `id:` | `<seat label>-<YYYYMMDD>-<n>`, lower case; refused if it carries an instance id |
| `seat` | `seat:` | the seat label, by role |
| `question` | `question:` | one sentence |
| `options` | `options:` | the options as written |
| `chosen`, `reason` | `chosen: <option> because <reason>` | split on the first ` because ` |
| `test` | `test:` | must read `1 ok` to `8 ok`; any other value is refused, since a failed line means the choice goes to the Desk |
| `estimate_h` | `estimate:` | a number; refused above 4 (card 2) |
| `undo` | `undo:` | how the same seat undoes it within a day |
| `changes` | `changes:` | typed links, as row links are |
| `actual_h` | `SEAT-DECISION-ACTUAL` | optional until the work is done; its source is kept |
| `state` | set by `dws` | below |

**States.** `decided-by-seat`, then `confirmed` or `overridden`; an
overridden decision becomes `reworked` once every affected row is resolved.
No other move exists, and none happens on a clock.

| command (actor director) | effect | refuses when |
|---|---|---|
| `dws decision add --from FILE` | parses one `seat-decision@1` block and appends `decided-by-seat` | a field is missing, unknown or repeated; the id exists; the test is not eight oks; the estimate is over 4 |
| `dws decision actual <id> --hours H --source S` | records the actual figure | the id is unknown |
| `dws decision confirm <id> --words W --ref R` | `confirmed`; W is the operator's words, R the relay id | not `decided-by-seat`; W empty |
| `dws decision override <id> --words W --ref R --instead I` | `overridden`; prints the affected list (section 6) | not `decided-by-seat`; W empty |
| `dws decision rework <id>` | `reworked` | any affected row is still open (section 6) |
| `dws decisions [--json] [--unconfirmed] [--daily]` | lists decisions; `--daily` prints the daily line | never; it writes nothing |

A skip is no command at all: the decision stays `decided-by-seat`, and stays
on the list. `--words` keeps the operator's own text on every confirm and
override, so a reader can check the ruling (R-150). The actor claim is the
same as the rest of `dws`: believed, not verified (section 6 of the ledger
design).

**The daily line.** `dws decisions --daily` prints the trial's line from the
ledger instead of the relay log, plus the unconfirmed list in card 4's order:

```
Seat decisions (trial day <n> of 14): <k> new today, <total> total, <c> confirmed, <o> overridden, rework <h> seat-hours.
Unconfirmed, most work held first:
  <id>  <dependents> waiting, <age>d old, <hours>h at risk: <question>
```

The trial day and length come from a `--trial-start` date the command is
given; the ledger does not hold the trial's dates.

**The Desk mirror.** For each `decided-by-seat` record, director writes a
Desk card with status `decided-by-seat`, the record's fields as its context,
and three choices: confirm, override, skip. The page shows them in their own
section, in the daily line's order. A ready answer is relayed like any other:
confirm and override go to `dws decision confirm|override` with the answer's
note as `--words`; skip writes nothing. This is director's Desk page edit and
sweep step; no builder touches the Desk.

## 4. Wait edges (card 3)

A row may wait on several things at once:

```
dws set <slug> --waits decision:<id> [--waits ask:<card id> ...]
dws set <slug> --unwait decision:<id>
```

- `decision:<id>` is a new link type. `dws` refuses it unless the id is a
  decision in the same fold.
- `ask:<card>` keeps K13: the card must be open in a fresh Desk snapshot.
  `--blocked operator --ask` keeps working and means one `ask:` wait.
- Edges are stored as links, so `dws show --json` carries them and `refresh`
  names them as not read, as it does `ask:` today.
- A row stays waiting until the edge is removed or the decision is
  confirmed. A confirmed decision's edges are shown as met, not deleted, so
  the history of what waited on what stays in the ledger.

Rows whose owners do not link their work get no edges (the card's stated
cost). Nothing infers an edge from text.

## 5. The window (card 4)

A **dependent** of a decision is a row with a `decision:<id>` edge whose stage
is not `parked`. Each dependent names its change with a `pr:` link.

`dws set --waits decision:<id>` refuses, and prints why, when:

1. the decision is not `decided-by-seat` (a confirmed decision needs no
   window; an overridden one takes no new work);
2. it already has three dependents; or
3. the call comes more than five days (120 hours, by the ledger clock) after
   the decision's `add` event.

The refusal text ends with "window full: stop, and the decision goes to the
top of the operator's list", and `dws decisions --unconfirmed` lists a full
window first. This is the ruled limit, written down by the operator, so it
may refuse (SOUL section 8: a gate through a ratified, written decision). The
two-path case of section 8 counts each path as one dependent.

**Nothing merged until confirmed.** `merge-guard` gains one read, before any
`gh` call: if a ledger row links `pr:<repo>#<n>` and waits on a decision that
is not `confirmed`, it prints

```
STOP <repo>#<n>: waits on seat decision <id>, not confirmed (seat-decide-window)
```

and exits 1. A ledger it cannot read stops too, as every other read in
`merge-guard` does. A PR the ledger does not link is not stopped by this
read; that is the same limit as section 4, and the stacked-PR rule still
governs the order of the ready PRs.

## 6. The order, and the affected list on override

**Order** (card 4), for every `decided-by-seat` decision:

1. a full window first (three dependents, or past five days with one or
   more waiting);
2. then the number of dependents, most first;
3. then age, oldest first;
4. then the seat-hours an override would throw away, most first: the
   decision's `actual_h` where recorded, else its `estimate_h`.

Ties keep id order, so two runs on the same ledger print the same list.

**Affected list.** `dws decision override` prints, and `dws decisions --json`
carries, every row with an edge to the decision, each with its stage and its
`pr:` links, plus the decision's own `changes`. `rework` refuses while any of
those rows still has its edge to this decision and is not `parked`. A seat
removes the edge (`--unwait`) when it has redone the row on the new choice,
and parks the row when it closed the work. None of them can be merged, since
section 5 stops every merge until a confirm. So "reworked" means every
affected row was closed or redone, as card 5 defines it.

## 7. The map view (`board-html`)

A "Seat decisions" section on the board page, drawn from `dws decisions
--json`:

- one block per unconfirmed decision, in section 6's order, with its id,
  seat, question, state, age, dependents waiting and hours at risk, as text;
- under each block, its dependent rows, each with its stage and PR link;
- below, confirmed, overridden and reworked decisions of the last 14 days as
  one line each, so the trial's counts can be checked against the ledger.

It uses the board's existing rules: no colour alone, no red, no alarm sort,
and it gates nothing. A ledger with no decision events renders "No seat
decisions recorded" and nothing else. This is a section of `board-html`, not
of the Desk's operations tab, which shows the Desk queue only for now
(card dash-sequence).

## 8. Two paths at once (card 6)

The card covers a choice that waits on the operator: an open Desk card, not
a seat decision. Its record is a decision event of a second kind:

```
dws decision paths --card <card id> --evidence E --estimate H --row-a SLUG --row-b SLUG \
                   --why-waiting W --test-a "1 ok ... 8 ok" --test-b "1 ok ... 8 ok"
```

It refuses unless all four parts hold as recorded: the two estimates add to
4 seat-hours or less (part 1); the card is open in a fresh snapshot, so it
waits on the operator (part 2; `--why-waiting` says why the seats would
otherwise idle); `--evidence` names the evidence before either path starts
(part 3); and each path's test reads eight oks (part 4). A third path is
refused. Each row gets a `decision:<id>` edge.

When the evidence arrives, `dws decision pick <id> --winner a|b --result R`
records it. The loser's row must already be `parked` with its PR closed as a
draft (refresh reads that), and `--result` must name where the loser's lessons
went in the winner's PR notes. The winner then becomes an ordinary
`decided-by-seat` record and goes through section 3: the operator still
confirms it.

## 9. Build split (stacked-PR rule d: one chain)

All three builds edit `dws` or read its output, so they land as one chain,
each based on the one before, merged in order:

| link | repo | contents | tests, red first |
|---|---|---|---|
| D1 | director | section 3: decision events, `add --from`, `actual`, `confirm`, `override`, `rework` (state checks only), `decisions --json/--unconfirmed/--daily` | `verify-dws-decisions.sh`: one fixture per refusal in section 3's table; a skip leaves the state unchanged; no command moves a state on a clock (fixed `DWS_NOW` a year later, state unchanged) |
| D2 | director, based on D1 | sections 4, 5, 6 and 8: `--waits`/`--unwait`, the window refusals, the order, the affected list, `rework`'s row check, `paths` and `pick`, and the `merge-guard` read | each window refusal; the order with a tie; `merge-guard` STOP on a waiting PR and on an unreadable ledger, PROCEED once confirmed (extends `verify-merge-guard.sh`) |
| D3 | director, based on D2 | section 7: the `board-html` section | `verify-board-html.sh`: the empty ledger line; order matches `dws decisions --unconfirmed`; no colour-only state |

The Desk mirror (section 3) is director's own step and goes with D1's
merge. The trial's `seat-decision@1` messages recorded before D1 are entered
once with `add --from`, so the trial's counts come from one place.

## 10. Not covered

- Who may write the Desk's confirm or override is honour-only, as for every
  Desk answer: the answer row carries no author.
- A seat that acts without sending the record is not seen by any of this.
  The test and the record are practice; the ledger only keeps what it is told.
- Where the test, record and window are written into the role files is a
  separate change, outside this repository.

Valid until 2026-10-27, or until D1 opens, whichever comes first; the
architect role re-checks it then.
