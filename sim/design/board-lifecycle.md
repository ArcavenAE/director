# Board lifecycle: decided work stays on the board until it is done

Status: design for review, 2026-10-25. Owner: the architect role. Docs only;
nothing is built by this document. Commission: the operator, through
director and the team supervisor.

The operator's questions, verbatim: "how do we keep track of
decided-but-not-compelete items on theese dashboards? when do they drop off?
when decided or when complete? how would they return to the board if
dispatching them failed or the work split, expanded, or was otherwise not
complete? how do new work types in future states appear ie, designs become
plans, plans become tests, tests become PRs, but perhaps stall out there?"

Builds on `board-workstream-ledger.md` (the ledger, its stages and writer
table), `ledger-fill.md` (rows move from records director already writes)
and `stalled-big-ideas.md` (the stalled-ideas scan, `dbi`). Section 6 answers
each question with a recommendation; section 7 lists the rulings.

## 1. Why

Today a decision leaves the board when the operator makes it, and nothing
picks it up again. So "decided" reads as "done", and work that was dispatched
and then stopped is invisible until someone asks for it by name.

The numbers, measured read-only on 2026-10-25 (director main a7ce948;
`dws show --json` over `$DIRECTOR_STATE/workstreams.jsonl`, and
`$DIRECTOR_STATE/desk-snapshot.json`):

| measure | value |
|---|---|
| ledger rows | 151 |
| rows opened after 2026-10-08 | 0 |
| Desk cards | 809, of which 778 relayed |
| ledger rows that link a Desk card (`ask:`) | 0 |
| rows with no link at all | 135 |
| rows at `merged` | 17, every one last moved on 2026-10-05 or 10-06 |
| stalled-ideas scan rows (2026-10-24) | 171: 125 no pointer, 46 pointers all closed |

So two trackers exist and neither holds decided work. The Desk holds the
decision and closes when it is relayed. The ledger holds streams seeded once,
by hand, on 2026-10-05 to 10-08, and no ruling since has opened a row. The
stalled-ideas scan sees idea files, not dispatched work.

One instance from today: aae-orc-ld16k was closed on S2 of
`stalled-big-ideas.md` while its own notes said S3 remained. No view shows
that S3 is undone. The fleet's own measurement says this is the common case:
partial completion is the largest cause of "ticket outlives its fix"
(finding-152), and bd has no partial state.

A second observation, mechanism not confirmed: the standing hook recorded a
stalled-ideas scan at 2026-10-25T15:15Z, while the saved scan output
(`dbi-last.json`) was last written at 2026-10-24T01:59Z. A triage page built
from the saved file would look unrefreshed even though the scan ran. Section
6.5 covers it.

## 2. What already holds (no change needed)

- **The page is rendered from the ledger.** `board-html` renders the header,
  Blocked on you, Uncaptured and the ledger from `dws show --json` (W3), and
  runs `dws refresh --local` before it reads (LF5). The operations dashboard
  (`dash-writer`) reads the same fold. Only board.md's history is prose.
- **A closed-unmerged PR is already a hint.** W2 drops it from the row's
  agreement set and shows `PR #n closed unmerged` on the row.
- **Ideas already return when their pointers close.** `dbi` lists an idea
  whose tickets and PRs are all closed as `pointers all closed`, unless the
  file itself says it is done.
- **Per-stage thresholds exist.** `board-html` holds one table: `building`
  and `in review` 24h; `designed`, `merged`, `released`, `deployed` 48h;
  `defined` 72h; `idea`, `accepted` and `parked` never flag.
- **The writer table.** Refresh may append observations, the two factual PR
  moves and, once LF4 lands, the operator-block clear. Opening, closing,
  accepting, parking and re-opening a row stay director's (LF-F).

## 3. Director's starting position, tested

| point | verdict | why |
|---|---|---|
| A call opens a ledger row | holds, narrowed | Only a call that starts work. A ruling that sets a value (a threshold, a name) has nothing to track. The open stays director's act, made in the same step as the relay (section 6.1) |
| A row leaves only on evidence of completion (merged PR and closed ticket, or an explicit drop), never on the call alone | holds, with one change | "Merged" is not done for a tool that ships to hosts: 17 rows have sat at `merged` for 19 days. The row declares its last stage when it opens (section 6.2), and evidence proposes the close; director closes |
| Each stage has its own quiet threshold | holds | Already built. Two gaps: `idea` never flags, and no stage means "planned" (section 6.4) |
| A missing ack, a PR closed unmerged, a reopened ticket or new open children put the row back, as stalled at that stage | holds, as a view | Putting a row back is a re-open, which is director's act. The board can show a "Returned" block computed from the facts at render time, with no write (section 6.3) |
| The pages are rendered from the ledger, not hand-published | holds | Already true for both pages (section 2). The stalled-ideas list must be rendered from the scan run, not a saved copy (section 6.5) |

## 4. The row lifecycle

```
  ruling relayed ──open──▶ [stage] ──facts──▶ [stage] ... ──▶ [done_at stage]
                             ▲                                      │
                             │                        close proposed│
                             │                                      ▼
                       re-open (director)◀──Returned◀── closed (director)
                                              ▲
                                    a fact contradicts the close
```

A row is open from the call until director closes it on evidence. Closed rows
fold out of the ledger table into a "Done, last 14 days" list, and stay in
the event file forever (append-only). A closed row whose evidence is later
contradicted shows in Returned until director re-opens it or confirms the
close.

## 5. What a row gains

Two fields, both set by director at open and changeable with `dws set`:

| field | values | default |
|---|---|---|
| `done_at` | a stage: `merged`, `released`, `deployed` or `accepted` | `merged` for docs and designs; `deployed` for anything installed on a host |
| `from` | `ask:<card>` (the ruling), `idea:<repo>/<path>` (a dispatched idea), or empty | the card or idea that started the row |

`from` is an explicit id written by director, never matched from text
(LF-B). The ledger already accepts `ask:` links; `idea:` is a new link type
that `dbi` reads to know an idea has a row (section 6.5).

## 6. The answers

### 6.1 How decided-but-not-complete items are tracked

**Recommendation: every relayed ruling that starts work opens a ledger row in
the same step, linked to its card; a reconciler line lists the relayed cards
that started work and have no row.**

- **When:** director relays a ruling with `relay-log append --ws <slug>`
  (LF1). If the slug has no row, director runs `dws open <slug> --from
  ask:<card>` first. One step, one id, nothing guessed.
- **Which rulings:** a ruling that dispatches work to a seat. Director knows
  this when it relays, because it names the seat. A ruling that only records
  a value opens nothing.
- **The catch for a missed open:** `dws reconcile` (K11) already reads the
  Desk snapshot and the ledger. It gains one proposal: a card relayed in the
  last 14 days whose relay entry carries no `--ws`. That is listed under
  Uncaptured, never opened by the script, since opening is director's act.
- **Not recommended:** refresh opening rows from the Desk snapshot. It would
  need a writer-table change, and the snapshot cannot tell a ruling that
  starts work from one that sets a value (it holds ids and statuses, no card
  text).

### 6.2 When items drop off: decided, or complete

**Recommendation: neither alone. A decided item leaves "Blocked on you" when
it is decided, and leaves the board when it is complete.**

- **Decided:** the operator block clears when the card reaches a closing
  status. That is LF4 (ruled 2026-10-19, build ticket aae-orc-506xb). The
  row stays on the board with director or a seat as the next actor.
- **Complete:** the row reaches its `done_at` stage, every linked PR is
  merged or was unlinked, and every linked bd ticket is closed. When that
  holds, `dws reconcile` proposes `close <slug>`, and director closes it in
  the sweep, in one batch. No Desk card, no operator step.
- **Explicit drop:** `dws park` stays the way to stop work that will not be
  finished; parked rows fold out the same way as closed ones, with their
  reason kept.
- **Why not close on evidence alone:** closing a workstream is a judgment
  (SOUL section 8, LF-F), and a closed ticket is weak evidence: today's
  ld16k instance closed with work remaining. A proposal costs director one
  line per row; an automatic close would hide exactly the rows this design
  exists to show.

### 6.3 How items return to the board

**Recommendation: a "Returned" block, computed at render time from facts,
with no write. Director then re-opens the row or confirms the close.**

A closed row is listed in Returned, with its reason, when any of these is
observed after its close:

| fact | source | reason shown |
|---|---|---|
| a linked PR closed unmerged | `gh`, as W2 reads it | `PR #n closed unmerged` |
| a linked bd ticket reopened | `bd show --json` | `bd <id> reopened` |
| a linked bd ticket closed while its notes still carry a non-empty `REMAINING:` line | `bd show --json` | `bd <id> closed with REMAINING` |
| an open bd ticket or PR names the row's `from` path, or a linked ticket gains an open dependent, after the close | `bd sql`, as `dbi` already searches ticket text for a path | `new work on <id>` (split or expanded) |

An open row whose dispatch was never acknowledged is shown in place with
`dispatch unacked`, not moved, because it never left. That needs the
ask-ledger source (W5, `msg:` links) and is shown as "not checked" until the
ask reader's file exists on the host.

- **Why a view and not a write:** a re-open is director's move (LF-F), and a
  view needs no writer-table change. The fact stays visible until director
  acts on it, so nothing is lost by not writing.
- **Splits:** work that splits is filed as flat tickets with dependency
  edges, not under a parent ticket. On the ledger, one row per feature or per PR chain
  (stacked-PR rule d). A split that becomes a separate feature gets its own
  row with `from` set to the parent row's card or idea; the parent closes on
  its own evidence. Typed row-to-row edges are #337 and are not needed for
  this.

### 6.4 How new work types appear as work moves forward

**Recommendation: one row carries a stream through every stage. Each new
artifact is a link on the same row, never a new row. Add one stage,
`planned`, and give `idea` a threshold.**

The operator's sequence, mapped to stages:

| work | stage | the fact that moves it | moved by |
|---|---|---|---|
| idea dispatched | `idea` | the row opens with `from idea:` | director |
| design written | `designed` | the linked design PR merges | director; refresh shows a hint, since its two moves cover build PRs only |
| plan filed | `planned` (new) | bd tickets linked to the row | director, or a refresh proposal |
| tests written | `building` | a draft PR with red tests, linked | director (as today) |
| PR in review | `in review` | every linked PR left draft | refresh (existing move) |
| merged | `merged` | every linked PR merged | refresh (existing move) |
| installed | `deployed` | the host runs it | director |

Thresholds, one table in `board-html`, diagnostic only (ADR-007):

| stage | today | recommended |
|---|---|---|
| `idea` | never | 4 days, the value already ruled for the stalled-ideas scan |
| `defined` | 72h | 72h |
| `designed` | 48h | 48h |
| `planned` | (new) | 48h: a plan with no build started |
| `building`, `in review` | 24h | 24h |
| `merged`, `released` | 48h | 48h, and only when the row's `done_at` is later than this stage |
| `deployed` | 48h | 48h |

A stream that stalls at any stage shows as `unmoved <age>` on its row, in
text, as today. The ledger sort (stage order, oldest first within a stage)
already puts the longest-waiting row of each stage on top.

**Not recommended:** a separate tracker per work type (a plans board, a tests
board). Each new board starts empty, and nothing would carry a stream from
one to the next, which is the failure section 1 measured.

### 6.5 One list, not two, and freshness

- **An idea with a row belongs to the ledger.** `dbi` skips any idea whose
  path is the `from idea:` of an open ledger row. When the row closes, the
  idea is judged by `dbi`'s own rules again. So a dispatched idea appears
  once: on the ledger while in flight, and on the stalled list only if the
  row closes and the idea is still not done. This replaces S3's plan to list
  ledger rows inside the stalled-ideas block (aae-orc-h4d34 is adjusted to
  match if this is ruled).
- **The triage is rendered from a scan run, with its time.** Any page or
  answer that shows the stalled list renders from the `dbi --json` output of
  that sweep and prints the scan's own `read_at`. A saved copy, if kept,
  carries its `read_at` too, and a reader compares it with the standing
  hook's scan time. A list older than the last scan is shown as stale, not
  as current.

## 7. Rulings needed

Each recommendation below is valid until 2026-11-01; the architect role
re-checks it then. Nothing applies on silence.

| # | question | options | recommendation |
|---|---|---|---|
| 1 | Who opens a row for a ruling that starts work | (a) director, in the relay step, with a reconcile line for any missed; (b) refresh, from the Desk snapshot | (a): no writer-table change, and the snapshot cannot tell which rulings start work |
| 2 | When a row leaves the board | (a) director closes on a reconcile proposal once `done_at` and the links agree; (b) refresh closes on the same evidence; (c) on the ruling | (a): closing is a judgment, and closed tickets are weak evidence |
| 3 | How a closed row returns | (a) a Returned block computed at render, director re-opens; (b) refresh re-opens on the facts in section 6.3 | (a): no writer-table change, nothing is lost |
| 4 | Stages and thresholds | (a) add `planned`, flag `idea` at 4 days, flag `merged` and `released` only before `done_at`; (b) keep today's table | (a) |
| 5 | Ideas and the ledger | (a) an idea with a row is the ledger's, and S3 is replaced by the skip in 6.5; (b) S3 as designed, both lists | (a): one place per item |

## 8. Build, after the rulings

One feature, so one chain (stacked-PR rule d), each part red first against a
scratch `$DIRECTOR_STATE`, stubbed `gh` and `bd`, and `DWS_TEST_CLOCK=1`.
Tickets are filed once the rulings land.

| part | what | depends on |
|---|---|---|
| BL1 | `done_at` and `from` on `open` and `set`; the `idea:` link type; the `planned` stage; close and fold-out of closed and parked rows into "Done, last 14 days" | none |
| BL2 | reconcile proposals: relayed card with no `--ws`; `close <slug>` when `done_at` and links agree | BL1, LF4 |
| BL3 | the Returned block in `board-html` from section 6.3's facts; `dispatch unacked` as "not checked" until W5 | BL1 |
| BL4 | `board-html` threshold table per section 6.4 | BL1 |
| BL5 | `dbi` skips ideas with an open row's `from idea:`; the stalled list prints its scan `read_at` | BL1 |
| BL6 | skill text: the relay step opens a row; the sweep closes proposed rows | BL1 to BL5 |

Tests, named:

1. Positive control: a row opened with `--from ask:c1 --done-at merged`, one
   linked PR merged and one bd ticket closed: reconcile proposes `close`;
   after director closes it, the row is in "Done, last 14 days" and not in
   the table.
2. No close on partial evidence: the same row with the bd ticket open, or at
   `merged` with `done_at deployed`: no proposal.
3. Returned: the closed row from test 1, then the bd ticket reopened: the row
   shows in Returned with `bd <id> reopened`; nothing is appended to the
   ledger.
4. REMAINING: a linked ticket closed with `REMAINING: S3` in its notes: the
   row shows in Returned with `closed with REMAINING`, and a ticket with an
   empty `REMAINING:` does not.
5. Split: an open ticket whose description names the row's `from` path,
   filed after the close: Returned with `new work on <id>`.
6. Missed open: a card relayed with no `--ws`: reconcile lists it under
   Uncaptured and opens no row.
7. Thresholds: an `idea` row 5 days unmoved shows `unmoved 5d`; a `merged` row
   with `done_at merged` never flags; with `done_at deployed` it flags at 48h.
8. dbi skip: an idea that is an open row's `from` is not listed; after the row
   closes it is judged again.
9. Refresh writes none of `open`, `close` or a re-open, from any fact
   (regression pin on the writer table).

## 9. Candidate requirements (provisional)

- **BL-A (OBSERVED):** a ruling that starts work opens a workstream row in
  the step that relays it. Earned by: 778 relayed cards and 0 rows linking a
  card, 2026-10-25.
- **BL-B (JUDGMENT):** a row leaves the board on evidence that its declared
  last stage is reached, closed by director on a proposal; the ruling alone
  never removes it. Extends LF-F.
- **BL-C (OBSERVED):** a closed row returns to view when a fact after its
  close contradicts it, including a ticket closed with work still listed as
  remaining. Earned by: aae-orc-ld16k, closed with S3 remaining; finding-152.
- **BL-D (JUDGMENT):** one row carries a stream through every stage; a new
  artifact is a link on it, never a new tracker.
- **BL-E (JUDGMENT):** a list rendered from a scan prints the scan's own read
  time, and one older than the last scan reads as stale.
