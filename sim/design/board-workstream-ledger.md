# The board as a workstream ledger

Status: design for review, 2026-10-02. Owner: the architect role. Commission:
the operator's dispatch of 2026-10-02 ("the improvements to director board
we discussed"), through the team supervisor. Design only; builders follow
review. **Supersedes director#168** (section 9 says why and what carries
over).

## 1. Why

The board is a log, so it cannot say what is stuck. Measured on 2026-10-02:
board.md is about 2,150 lines in some 150 sections appended by timestamp
since 2026-09-19, and board.html is about 318 KB of the same. There is no
row per workstream, no lifecycle stage, and no time of last movement, so a
workstream idle for four days reads like one that moved an hour ago. That is
how a seven-hour stall in one supervisor's queue went unseen. "Blocked on
you" and "Uncaptured" are buried in prose, and answering "what did we start
and not progress" took director about six `gh` and `bd` queries.

The fix is a current-state ledger at the top of the board, one row per
workstream, with the log kept below as history.

## 2. The row

| column | content |
|---|---|
| workstream | a short slug and title, one row per stream of work (an issue, a design and its builds) |
| repos | the repositories it touches |
| owner | a seat label or a ROLE (`team-a/architect`, `supervisor`); never an instance id |
| stage | one of the ten in section 3 |
| last moved | when the stage last changed, or a linked artifact last moved, whichever is later (section 5) |
| next action | one line: who does what next |
| blocked on | `operator`, a role, `external`, or empty |
| links | PRs, issues, bd ids, ask ids |

Owner refuses an instance id: a value ending in `-g<digits>-<digits>` is an
error at write. Instance ids change at every respawn; the role does not.

## 3. Stages

`idea`, `defined`, `designed`, `building`, `in review`, `merged`,
`released`, `deployed`, `accepted`, `parked`.

The ledger sorts by stage in that order, then oldest last-moved first within
a stage, so the stream that has waited longest in each stage is on top.
`parked` sorts last. A move to any stage is allowed (work skips stages), but
each move records the stage it came from, and a move whose `from` no longer
matches the current stage is refused (the director re-reads and retries; the
same rule #168's review settled for stale moves).

## 4. The page

From the top:

1. **Header:** `Now: 14 streams, 3 blocked on you, 4 unmoved past threshold | ledger 6m | page 2m`.
2. **Blocked on you:** every row with `blocked on = operator`, oldest first,
   as `<owner> - <workstream>: <next action>` (the sweep's line form).
3. **Uncaptured:** the sweep's Uncaptured lines, as today, plus any ask id
   seen in a row's links with no row of its own.
4. **The ledger:** one table, sorted as in section 3. A row unmoved past its
   stage's threshold carries `unmoved 2d` as text, not color alone.
5. **History:** board.md as today, collapsed.

board-html renders sections 1 to 4 from the ledger, never from prose, and
section 5 from board.md. With no ledger file, the page is today's page: the
ledger is opt-in by existence, the same rule the renderer already follows.

**Unmoved thresholds** (diagnostic, not a gate, ADR-007): `building` and `in
review` 24h; `designed`, `merged`, `released`, `deployed` 48h; `defined` 72h;
`idea`, `accepted` and `parked` never flag. Thresholds live in one table in
the renderer, so the operator changes them in one place.

## 5. Last moved, refreshed from sources

The point of the row is the time column, and a time typed by director is
only as fresh as director's last sweep. So last moved is refreshed from the
sources each row links (idea #178, director as a cache):

| link | refresh reads | moves last-moved when |
|---|---|---|
| PR | `gh pr view --json state,isDraft,reviewDecision,updatedAt,mergedAt` | a commit, review, ready or merge |
| issue | `gh issue view --json state,updatedAt` | a comment or state change |
| bd id | `bd show --json` | a status or notes change |
| ask id | director's bus history | a message carrying the ask id |

`refresh` runs on every render and in the sweep, skips a link checked in the
last 10 minutes, and records what it saw as an `observe` event with the time.

**What refresh may change, under the automation boundary (SOUL section 8):**
- It always updates last moved, which is an observation.
- It advances a stage only on a fact the source states: `building` to `in
  review` when a linked PR leaves draft, and `in review` to `merged` when a
  linked PR merges. Each such move is an event with actor `refresh` and the
  fact it read.
- Every other stage (`released`, `deployed`, `accepted`, `parked`) is
  director's act. Refresh shows a hint when a source suggests one (a release
  tag that contains the merge), and never applies it.

## 6. Store and writers

- **Store:** `$DIRECTOR_STATE/workstreams.jsonl`, append-only events folded
  into the rows on read. Events: `open`, `stage`, `set` (next action, blocked
  on, owner, repos), `link`, `observe`, `park`. A torn last line is skipped
  and counted, never repaired.
- **Concurrency:** one `flock` around fold, check and append; 5s timeout that
  fails loudly.
- **Writers:**

| writer | may append |
|---|---|
| director, through the CLI | every event |
| refresh | `observe`, and the two factual `stage` moves in section 5 |
| anything else | nothing |

- **CLI:** one stdlib Python script beside `dsi` and `dsx`
  (`skills/director/scripts/`), name the operator's call (ruling 3; working
  name `dws`): `open`, `stage`, `set`, `link`, `park`, `refresh`, `show
  [--json]`. After a write it re-renders when board.html exists.
- **dsi atomic writes (SH1, already filed):** dsi writes `sessions.json` and
  `roster.md` by truncate-then-write, and the page reads them. SH1 lands
  first.

## 7. The sweep

The sweep's printed contract (skill "Mode: sweep", step 4: the four blocks
and the `<session> - <the ask in one line>` line form) does not change.
Step 4's "Blocked on you" lines come from ledger rows with `blocked on =
operator`, printed as `<owner> - <workstream>: <next action>`, with the owner
in the session slot. Step 5 gains: open a row for each new stream of work,
move stages that changed, run `refresh`. Without the CLI, the sweep runs as
today.

## 8. Seed

Director seeds the ledger once by hand from the open work: the open draft
PRs, the open design issues, and the bd tickets with a claim, one row per
stream. That is the "what did we start and not progress" query run once and
kept. Rows for finished work are not back-filled; the log holds that history.

## 9. Supersede #168, not revise

#168 designed an item ledger: one row per **ask** (an operator decision, a
delivery), with ask states (`AWAITING_OPERATOR`, `BLOCKED_AT_DELIVERY`), a
capped tiered panel, a seat health strip and an alarm timer. The operator
saw it rendered and declined it on 2026-10-01. Today's dispatch asks for a
different unit, the **workstream**, with lifecycle stages and a last-moved
time refreshed from sources. Revising #168 into that would replace its model,
page and parts; a new document reads more plainly and leaves #168's record
intact.

**Carried over from #168:** the append-only ledger with a fold, one lock for
all writers, refusal of a stale move, the sweep's unchanged line form, the
opt-in-by-existence page, and the two live small parts (P0b, the install fix;
SH1, dsi atomic writes).

**Dropped:** the ask-item model and states, the capped tiered panel, the
seat health strip, the alarm timer, the board.md snapshot (X1).

**#168's open review items, disposed:**
- Review 5373351518 (the snapshot rules, the X1 test naming, the writer-table
  row for snapshots, Standing-mode coverage): moot, X1 is dropped. board.md
  stays history; state is never written there.
- Review 5371041926 (the sweep line form; stale moves): adopted here in
  sections 7 and 3.
- The writer table is in section 6.

On approval, #168 gets a comment pointing here and is closed by its author.

## 10. Parts, in order

| part | what | depends on |
|---|---|---|
| SH1 | dsi atomic writes (filed) | none |
| P0b | install.sh names a non-symlink target and installs the rest (filed) | none |
| W1 | store, fold, lock, CLI (`open`, `stage`, `set`, `link`, `park`, `show`), instance-id refusal | none |
| W2 | `refresh` over PR, issue and bd links; the two factual stage moves; 10-minute skip | W1 |
| W3 | board-html renders header, Blocked on you, Uncaptured, ledger, thresholds; history below | W1 |
| W4 | skill text: sweep step 4 lines from the ledger, step 5 additions | W1, W3 |
| W5 | ask-id refresh from bus history | W2, and director#126 slice M for the reader |
| SEED | director seeds the rows (an operator-side step, no PR) | W1 |

## 11. Tests (red first)

1. **W1:** an owner ending in `-g9-9` (synthetic) is refused; a move with a stale `from`
   exits nonzero and appends nothing; two processes appending 200 events each
   give 400 parseable lines; a torn last line is skipped and counted.
2. **W1 sort:** a fixture of 12 rows across stages folds into stage order,
   oldest last-moved first within a stage, `parked` last.
3. **W2:** with `gh` and `bd` stubbed, a PR that left draft moves `building`
   to `in review` with actor `refresh`; a merged PR moves `in review` to
   `merged`; a release tag produces a hint and no move; a link checked 5
   minutes ago is skipped.
4. **W3:** the fixture renders Blocked on you first, Uncaptured second, the
   ledger third, history collapsed; a `building` row unmoved 30h carries
   `unmoved 30h` as text; with no ledger file the page equals today's.
5. **W4:** the sweep's step 4 fence is byte-identical to main, and from the
   fixture each Blocked on you line reads `<owner> - <workstream>: <next>`.

## 12. Rulings needed (operator, via director)

| # | question | default |
|---|---|---|
| 1 | Supersede #168 with this design (section 9) | yes |
| 2 | Refresh may make the two factual stage moves (draft to ready is `in review`; merge is `merged`) and only those (section 5) | yes |
| 3 | The CLI's name | `dws` |
| 4 | Unmoved thresholds (section 4) | as listed |
