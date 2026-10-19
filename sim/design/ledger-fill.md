# Ledger fill: rows move from the events director already writes

Status: design for review, 2026-10-19. Owner: the architect role. Docs only;
no code lands until this is reviewed.

- Issue: #321.
- Commission: the operator's "yes please execute on each" on director's
  improvement list, item 4b (ledger fill).
- Requirements: R-36, R-186, R-192, R-197, R-202 (`sim/requirements.md`).
- Prior art: `board-workstream-ledger.md` (sections 5, 6, 10: W2 refresh, the
  writer table, W5), `director-functions-redesign.md` (design D; K5, K6, K11,
  K13; Q5), `ask-ledger.md` (the bus ask rows W5 reads),
  `authority-never-in-content.md` (why a join never reads message text),
  `reference/functions.md` sections 1, 2, 4 and 13; bd `aae-orc-eeb2v` (K13).
- Checked against director origin/main 59f8512.

## 1. Why

The workstream ledger is the board's answer to "what is stuck", and it only
answers when its rows move. Today they do not, while director keeps working:
in the four days since 2026-10-16 director wrote 109 clock-stamped relay-log
entries and one ledger event. A row that looks idle may be the busiest
stream on the board, so the ledger now misleads in the direction it was built
to prevent (board design section 1).

The fix is to move rows from the records director already writes (the relay
log, the Desk snapshot, later the ask ledger), joined by an explicit id, under
the existing writer table plus one stated change. Nothing in this design reads
message text to guess a link.

## 2. Why the ledger went stale (measured)

Measured read-only on 2026-10-19 against `$DIRECTOR_STATE/workstreams.jsonl`
(a Python fold of the file, counts only) and `dws show --json`:

| measure | value |
|---|---|
| event lines | 1,381, none torn |
| rows | 151 |
| by event | open 151, set 544, observe 664, stage 18, link 4 |
| by actor | director 712, refresh 669 |
| by tag | untagged 1,381 (every line predates K8, #315) |

Events by date:

| date | events | director | refresh |
|---|---|---|---|
| 2026-10-05 | 389 | 245 | 144 |
| 2026-10-06 | 303 | 198 | 105 |
| 2026-10-07 | 572 | 194 | 378 |
| 2026-10-08 | 95 | 74 | 21 |
| 2026-10-16 | 21 | 0 | 21 |
| 2026-10-19 | 1 | 1 | 0 |

Newest event per row: 31 rows on 10-05, 39 on 10-06, 33 on 10-07, 32 on
10-08, 15 on 10-16 (refresh observes only), 1 on 10-19. Folded `last_moved`:
48, 45, 43 and 13 rows on 10-05 to 10-08, and one row each on 10-14 and
10-15. The last stage move was on 10-06. Stages: idea 128, merged 17,
building 3, defined 2, in review 1. So the premise holds, with one
correction: two rows moved after 10-07, both through a refresh observe.

What writes the ledger, and what stopped:

1. **Director, by hand.** The only writer of open, set, stage and link. It
   wrote 151 opens and 544 sets in four days (the SEED step, board design
   section 8), then stopped. Sweep step 5 tells it to keep the ledger current
   (SKILL.md:93-96); that instruction is a habit nothing triggers. Inferred:
   director's attention moved to the relay log and the Desk, which are on its
   path for every send.
2. **`refresh`, also by hand.** It runs only when director types `dws
   refresh` in step 5. The board design says refresh "runs on every render"
   (board-workstream-ledger.md:139), but `board-html` only calls `dws show
   --json` (board-html:67). Measured by file times: board.md and board.html
   were rewritten at 04:00 today; the ledger's last write was the 10-19 event.
   The page is rendered regularly; the ledger is not refreshed.
3. **Refresh has nothing to read on most rows.** 135 of 151 rows carry no
   link at all; 9 link a PR, 6 a bd id, 2 an issue, 1 an untyped link, 0 an
   `ask:` card. Even a refresh on every render would move at most 16 rows.
4. **A director `set` is not movement in the fold.** `apply()` changes owner,
   next, blocked and repos on a set and leaves `last_moved` alone
   (dws:257-260). R-197 (OBSERVED) says "an authored update counts as
   movement", so the 544 sets moved no clock. This is R-197 unbuilt, and it
   conflicts with the board design's own definition of last moved (section 2,
   stage or linked artifact). R-197 is later and observed; this design follows
   it.
5. **The Desk half cannot run.** `$DIRECTOR_STATE/desk-snapshot.json` does not
   exist, so K13 refuses every new `--blocked operator` with UNREAD
   (dws:163-164). Five rows still read blocked on the operator from before
   K13, none with a card link.

Recommendation: treat causes 2 to 4 as this item's build, cause 1 as what the
fill relieves (director stops being the only clock), and cause 5 as an input
the session supplies (K5 stands: a script cannot read the Desk).

## 3. The sources and what each may move

The relay log was measured the same way, headings only, no entry text
(the log `relay-log` finds by default, `sim/notes/relay-log.md` in the
checkout, local and gitignored):

- 792 level-2 headings. 350 begin `## R-<n>`, carrying only 213 distinct
  numbers, so R-n is not unique across the file. 145 more entries are list
  items (`- R-<n>`). The rest are date or section headings.
- 109 headings are in the writer's form, `## R-<n> (<UTC stamp>) -> <to>`
  (relay-log:118), R-2587 to R-2695, all distinct, dated 10-16 (79), 10-17
  (6), 10-18 (4), 10-19 (20). No `### R-<n> outcome` note in the writer's
  form exists yet.
- No heading or field names a workstream. The writer has no such field
  (relay-log:15-18). Entry texts carry `#N` tokens (69 of the 109) and bd-shaped
  ids (7), which is exactly the material a join must not read (section 4).

| source | what ties it to a row | event | actor, tag | in today's table? |
|---|---|---|---|---|
| relay entry in writer form | `--ws <slug>` on append (new), or `relay:R-<n>` on the row (new link type) | `observe`, `moved_at` = the entry stamp, `src` = `relay:R-<n>@<stamp>` | refresh, mirror | yes, observe |
| relay outcome note (writer form) | its entry's R-n, then as above | `observe`, `moved_at` = the note's stamp | refresh, mirror | yes |
| relay entry in any other form | none | not read; counted under gaps | none | n/a |
| Desk card still open | `ask:<card>` (K13) | `observe` recording card and snapshot `read_at` | refresh, mirror | yes |
| Desk card left open for a closing status | `ask:<card>` on a row blocked on the operator | `set blocked ""` plus a hint "card X is <status>; next action is director's" | refresh, mirror | **no: table change, LF4** |
| Desk card `yours` or `split` | as above | hint only | refresh, mirror | yes (hints ride observe) |
| Desk card absent from the snapshot | as above | listed under gaps as "card not on the Desk" | none | n/a |
| open card no row links | none | listed under Uncaptured (board design section 4.3) | none | n/a |
| ask-ledger row (W5) | `msg:<message_id>` on the row (new link type) | `observe`, `moved_at` = `state_at`; blocked or answered as a hint | refresh, mirror | yes |

**Stage moves stay where W2 put them.** No new source moves a stage. A relay
says nothing about a PR's state that the PR itself does not say better, and
W2 already reads the PR.

**The one table change (LF4).** Clearing an operator block when its card
closes is a `set`, which refresh may not append today (dws:326-336; board
design section 6, "only those" in ruling 2). It is a factual move: the card is
the one source for "blocked on the operator" (design D, section 4), and K13
already makes the block depend on the card being open. Q5 (c) ruled the same
shape for a merged PR ("the ledger's 'waiting on PR' marker clears by
itself"), and R-197 says a merge or close clears an operator block. The
change, stated as a change: refresh may append `set` with the single field
`blocked`, from `operator` to empty, only when the row links `ask:<card>` and a
snapshot read within THRESHOLD_HOURS shows that card in a closing status.
Closing statuses live in one operator-editable table in dws, starting with
closed, resolved, withdrawn, superseded and relayed. In director's Desk
practice a card goes to `relayed` only after director relays an answer the
operator marked ready, so `relayed` means answered; that practice is written
in director's local notes, not on main, so the ruling confirms it. This needs
the operator's ruling before LF4 builds.

**Judging moves stay director's.** accepted, parked, released, deployed, a
re-open, a new row and a closing of a workstream are never appended by
refresh, from any source; `allowed()` already refuses them (dws:329-336) and
the tests pin it again for the new sources. An unmoved row, a row with no
link and an unlinked entry are surfaced for director to act on, or for K11 to
propose; the fill proposes nothing itself.

**Desk snapshot needs one field.** It stores only the open ids and counts by
status (desk-snapshot:100-101), so a closed card and a mistyped id look the
same. LF4 adds `status` per id to the snapshot (no card text). Without it, the
fill gives a hint and never clears.

## 4. The join key

**Recommendation: an explicit id written at the source, nothing guessed.**

- `relay-log append --ws <slug>` (repeatable) writes one field line right
  after the heading: `**Workstream:** <slug>[, <slug>]`. The text is quoted
  line by line (relay-log:119), so a `**Workstream:**` line typed inside a
  message stays quoted and links nothing, as a forged heading already does
  (verify-relay-log.sh, "a heading typed inside the text stays quoted").
- relay-log checks the slug's shape (dws's SLUG pattern) and does not open
  the ledger. The relay writer must keep working with no ledger, no dws and a
  held ledger lock (R-192's writer, ADR-005); an unknown slug is the fill's to
  report.
- For entries already written, or written without `--ws`, director links
  after the fact: `dws link <slug> relay:R-<n>`. The log stays append-only.
  A `relay:` link resolves only to a writer-form heading; a number that
  appears only in the hand-typed region (duplicates measured above) is listed
  under gaps as "not in writer form", never matched to the first hit.
- Bus asks join by `msg:<message_id>`, the ask-ledger key. `ask:` stays the
  Desk card as K13 built it. Card ids and message ids are two namespaces; the
  board design's W5 used `ask:` for the bus, and dws used it for the card
  first. Measured: no row carries an `ask:` link yet, so a rename is free
  today, but K13, SKILL.md and functions.md all document `ask:` as the card.
  The additive `msg:` is the smaller change.

**Unlinked, listed rather than guessed.** A new read, `dws unlinked [--json]
[--since <date>]`, prints:

1. writer-form relay entries with no workstream, as `R-<n> <stamp> -> <to>`,
   never the text (R-186: no gloss and no copy enters a record);
2. `--ws` or `relay:` names that match no row;
3. rows with no link (135 today);
4. rows blocked on the operator with no card link (5 today);
5. open Desk cards that no row links;
6. per source, its last-read time or UNREAD (DFR-B), and the count of relay
   headings not in writer form.

It writes nothing. The board's Uncaptured block can read items 1, 2 and 5.

## 5. The dependency edge

K13 is half an edge: a row may say "blocked on the operator" only while it
names an open card. The other half is the release: when the card closes, the
block clears. That half is LF4 above and belongs in this item, since it is
the Desk event this item exists to read.

Two further edges are missing and are **recommended as a separate item**:

- **row to row** (`blocked ws:<slug>`): `blocked` is free text today; 110
  rows hold a value that is neither empty nor `operator`. A typed form needs a
  row-schema change, a cycle check (the ask ledger's chain-and-stop rule,
  ask-ledger.md section 7) and a board column.
- **row to bd id** (`blocked bd:<id>`): bd already holds dependencies between
  tickets. Mirroring them into the ledger risks a second record of bd state
  (the same reason refresh reads PR state and does not own it, board design
  section 5).

Neither is needed for rows to move, and both change what the board shows, so
folding them in would put a schema review in front of a staleness fix.

## 6. When it runs

| option | gives | costs |
|---|---|---|
| a. sweep step 5 only | no new trigger | this is today's design, and it stopped (section 2, cause 2) |
| b. a hook in `relay-log append` | the row moves at the send | couples the relay writer to the ledger and its lock; a relay could fail on a ledger problem |
| c. `dws refresh --local` from `board-html`, plus the sweep's full refresh | runs whenever the page is rendered, which already happens; no network | the renderer starts a write, so it must not re-render itself |

**Recommended: (c).** It is what the board design already says refresh does
(section 5), and the render already runs on its own. `--local` reads only the
relay log, the Desk snapshot and the ask reader's file, so a render never
waits on `gh` or `bd`; PR, issue and bd links stay with the full refresh in
the sweep. When K20 puts drain and reconcile on the standing tick, the same
call moves there.

**No render loop.** `dws` re-renders after a write when board.html exists
(dws:379-388). board-html calls refresh with `DWS_NO_RERENDER=1`, and dws
honours it. A test pins it.

**Idempotence by ledger position, never by clock.** Each fill event carries
`src`: `relay:R-<n>@<stamp>`, `relay:R-<n>/outcome@<stamp>`,
`desk:<card>@<read_at>`, `msg:<id>@<state_at>`. The fold keeps the set of
consumed `src` per row, and the fill skips any `src` already folded. This is
W2's no-re-applied-fact rule applied to a new key. Replaying the same log and
snapshot twice appends nothing. The Desk clear also requires the row to be
blocked on the operator with that card at the time of the check, so a cleared
row is not cleared again, and a block director sets again later (K13 makes
that need the card open) clears again only on a later snapshot.

**Locks, never nested.** The fill reads the relay log under a shared flock on
relay-log's own lock file (relay-log writes under an exclusive one with an
fsync, relay-log:104-130), with a 5 s timeout that reports the relay source
unreadable and moves on. It releases that lock, then takes the ledger lock for
fold, check and append, as `write_many` does today. Holding one lock at a time
rules out a lock-order deadlock between the two writers.

## 7. Build items

For one builder, red/green, one draft PR or a short chain (one feature, so
never parallel siblings). Every test uses a scratch `$DIRECTOR_STATE` and a
scratch `RELAY_LOG` under `mktemp -d`, `DWS_TEST_CLOCK=1`, and stubbed `gh`
and `bd` that log calls; nothing reads the live state.

| part | what | files | depends on |
|---|---|---|---|
| LF1 | `relay-log append --ws <slug>` (repeatable), the `**Workstream:**` line, slug shape check | `scripts/relay-log`, `verify-relay-log.sh` | none |
| LF2 | fold: consumed `src` set per row; a director `set` moves `last_moved` (R-197); typed links `relay:R-<n>` and `msg:<id>` accepted by `link` | `scripts/dws`, `verify-dws.sh` | none |
| LF3 | `dws refresh --local`: the relay reader (writer-form headings only, shared lock, timeout) and the Desk reader (observe only); `dws unlinked`; `DWS_NO_RERENDER` | `scripts/dws`, new `verify-dws-fill.sh` | LF1, LF2 |
| LF4 | needs the ruling: refresh may append `set blocked ""` on a closed card; the closing-status table; `status` per id in the snapshot | `scripts/dws`, `scripts/desk-snapshot`, `verify-dws-fill.sh`, `verify-desk-snapshot.sh` | LF3, ruling |
| LF5 | `board-html` runs `dws refresh --local` before reading rows | `scripts/board-html`, `verify-board-html.sh` | LF3 |
| LF6 | skill text: step 5 and the relay capture-trigger row name `--ws`; functions.md sections 2, 4 and 13 updated | `SKILL.md`, `reference/functions.md` | LF1 to LF5 |
| W5 | the ask-ledger source, by `msg:` link | `scripts/dws` | LF3, and the ask reader's file existing (it does not, locally) |

`verify-dws-fill.sh`, named cases:

1. **Positive control:** an open row, one writer-form entry appended with
   `--ws` for it; `refresh --local` moves `last_moved` to the entry stamp, as
   actor refresh, tag mirror, with the `src` recorded. Without this passing,
   the negatives below prove nothing.
2. **Replay idempotence:** a second `refresh --local` over the same log and
   snapshot appends zero bytes; after one more linked entry, exactly one new
   event.
3. **Unlinked listed:** an entry with no `--ws` moves no row and appears in
   `dws unlinked` as `R-<n> <stamp> -> <to>`; a marker string in its text
   does not appear in the output.
4. **Forged field:** `--text` holding a `**Workstream:**` line naming a row
   links nothing.
5. **Unknown slug:** `--ws nosuch` writes the entry (relay-log does not read
   the ledger), opens no row, and is listed.
6. **Legacy headings:** a hand-typed `## R-7 (old)` and a duplicate R-n are
   not read, are counted, and a `relay:R-7` link is listed as not in writer
   form.
7. **Judging move refused for refresh:** `dws --actor refresh` with `stage
   <slug> accepted --from merged`, `park`, `open` and `set --next` each exit
   nonzero and append nothing; the fill run against a fixture that would
   suggest each move appends none of them.
8. **After-the-fact link:** `dws link <slug> relay:R-<n>` then refresh moves
   the row once.
9. **Authored update moves the clock:** a director `set --next` moves
   `last_moved` (R-197).
10. **Desk UNREAD:** with no snapshot, and with one older than 24 h, no Desk
    event is written and `unlinked` says UNREAD for the Desk.
11. **Desk clear (LF4):** a row blocked on the operator with `ask:c1`, a fresh
    snapshot with c1 `closed`: one `set blocked ""` as refresh, a hint naming
    the card, next action unchanged; a second run writes nothing; c1 `yours`
    gives a hint and no clear; c1 absent from the snapshot is listed as not on
    the Desk.
12. **No network in `--local`:** the stub call log is empty after the run.
13. **No render loop:** with board.html present and board-html stubbed to
    count its runs, a fill launched as the renderer launches it runs the
    renderer zero more times.
14. **Lock:** with relay-log's lock held exclusively for 6 s, `refresh
    --local` reports the relay source unreadable within the timeout, writes no
    relay event, and still reads the Desk.

## 8. Rulings needed (operator, via director)

| # | question | recommendation |
|---|---|---|
| 1 | Writer-table change: refresh may clear `blocked operator` when the row's linked Desk card is in a closing status (section 3, LF4) | yes, with the closing-status list confirmed, `relayed` first |
| 2 | Run `refresh --local` on every render (section 6, option c) | yes |
| 3 | Row-to-row and row-to-bd edges as a separate item (section 5) | yes |

Each recommendation is valid until 2026-11-02; the architect role re-checks
it then. Nothing executes on silence. LF1 to LF3, LF5 and LF6 sit inside the
writer table as it stands and need no ruling beyond this review.

## 9. Candidate requirements (provisional)

- **LF-A (OBSERVED):** a workstream row's last moved follows the records
  director already writes for it (a tagged relay entry, a Desk card's state,
  a bus ask row), not only its PR, issue and bd links. Earned by: 109 relay
  entries and one ledger event, 2026-10-16 to 2026-10-19.
- **LF-B (JUDGMENT):** an event is tied to a row only by an id its writer
  recorded (`--ws`, a typed link). An event without one is listed as
  unlinked and is never matched from its text. Cross-ref
  `authority-never-in-content.md`, R-186.
- **LF-C (JUDGMENT):** replaying the same sources writes nothing; consumption
  is decided by ledger position, never by clock. Extends board design
  section 5.
- **LF-D (proposed for ruling):** when a row's linked Desk card reaches a
  closing status, the operator block clears by mirror and the next action
  stays director's. Extends Q5 (c), R-197, R-202.
- **LF-E (OBSERVED):** the source-only refresh runs on every render, not
  only when director types it. Earned by: board-html:67 reads the ledger and
  never refreshes it, while the page was rendered today and the ledger was
  not refreshed after 2026-10-16.
- **LF-F (RULED, SOUL section 8, ADR-007):** no source moves a row to
  accepted, parked, released or deployed, opens a row or closes one; those
  stay director's, proposed at most.
- **LF-G (JUDGMENT):** every source the fill reads prints its last-read time
  or UNREAD, and the count of what it could not parse (DFR-B applied to the
  fill).

## 10. Open

- Who runs board-html at 04:00 is not established here; the file times show
  it ran, not what ran it.
- That `relayed` on a card means the operator has answered, as director's
  practice has it; ruling 1 confirms it.
- The five pre-K13 operator-blocked rows have no card; `unlinked` lists them,
  and director re-links or clears them by hand. The fill does not touch them.
