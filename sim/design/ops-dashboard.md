# The operations view: a second tab on the Desk

Status: design for review, 2026-10-19. Owner: the architect role. Docs only;
nothing is built by this document. The page work is director's, because
director publishes the Desk; the writer step is director's sweep.

The operator asked for one place to see what waits on them, where work sits,
what has stopped moving, and how the running seats are doing, without asking
a seat each time. Today each answer takes several `gh`, `bd` and `marvel`
reads. This design puts those answers on a second tab of the Decision Desk,
drawn from rows director writes on its sweep.

- Commission: the operator's question of 2026-10-19, whether a Claude
  artifact could show director status, triage, the Desk, the ledger and the
  flow of work, through the arcaven supervisor.
- A five-seat design party worked it in two rounds, with a forced vote. The
  operator ruled six of its seven cards on 2026-10-19 (section 6).
- Related: `board-workstream-ledger.md` (the ledger and its stages),
  `director-functions-redesign.md` K13 (the Desk snapshot), director finding
  glst (a script cannot reach artifact data).

## 1. What the premises changed

Each was checked with one command on 2026-10-19 (MEASURED unless marked).

- **The Desk is kept current; the ledger is not.** No ledger stage has
  changed since 2026-10-06. The refresh that moves rows from GitHub last ran
  2026-10-16 and can see only the 16 of 151 rows that carry a link. 128 of
  151 rows are ideas that were never staged.
- **An artifact page reads only its own database** (artifact runtime db type
  definition, "One store per artifact"). A separate page could not show Desk
  cards live, which is why the view lives on the Desk.
- **Only a session can write artifact data** (finding glst). The writer is a
  step of director's sweep, not a launchd job.
- **`marvel get sessions` has no JSON output** (`-o applies to team and
  workspace only`). The writer parses its text table.
- **The ledger's own thresholds flag merged rows forever,** because nothing
  records the stages after merged: 17 of the 23 rows that flag today are
  merged.
- **The supervisor route ledger has no single schema** (482 key shapes), so
  no panel draws "routed and unacked" from it.

## 2. The page

A second tab on the Desk page, beside the decisions view. The decisions view
loads and looks exactly as it does today, and a missing or malformed
dashboard row cannot break it (section 5, test 1).

Four panels, in reading order, single column at phone width:

1. **Waiting on you.** Open Desk cards by age band (under a day, 2 to 6 days,
   7 days or more), read live from the Desk's own `decisions` collection, so
   it needs no writer and no copy. Then the ledger rows with `blocked` set to
   `operator`, from the writer's rows.
2. **Where work sits.** The count of ledger rows per stage. Ideas are one
   line: "128 ideas, oldest opened 10-05". Merged rows are one line: "17
   merged, later stages not tracked" (ruled, dash-merged-rows).
3. **In flight.** Ledger rows past idea and before merged, oldest first, each
   with its stage and "unmoved Nd" against the ledger's own thresholds, as
   text, never colour alone.
4. **Sessions.** For each cluster: each session's state, health and last
   active, with marvel's column names and values word for word, under the
   heading "Sessions" (ruled, dash-fourth-panel).

The page never uses the words stuck, busy, free or idle, has no red, no alarm
sort and no buttons, and nothing reads it as a gate (ADR-007: metrics inform, they do not gate). Free and busy stay off the page until the open
cards on what a working seat means are ruled. Running teams are named by
team or seat label.

**Sequence, not yet ruled.** Card dash-sequence (Desk only first, or all four
panels now) is still open. Panel 1's Desk half needs no writer and can ship
on its own either way. Panels 2 and 3 wait on that card.

## 3. The rows (ruled, dash-data-model)

One shaped snapshot document per panel, written whole, plus one counts-only
document per panel per day, kept 14 days. The ledger file stays the history.

| collection | document | fields, and nothing else |
|---|---|---|
| `dash_panels` | the panel name | `panel`, `as_of`, `writer`, `rows`, `counts`, `refusal` |
| `dash_daily` | `<panel>-<YYYY-MM-DD>` | `panel`, `day`, `counts` |
| `dash_meta` | `writer` | `last_run`, `runs`, `reader` |

A row holds only these fields:
- `id`: a workstream slug or a session name;
- `stage`: one of the ledger's ten stages, or marvel's `state` value as
  printed;
- `age_h`: hours since the row last moved;
- `area`: a label from the Desk's own area list;
- `title`: for ledger rows only, the title director typed;
- `health`, `last_active`, `cluster`: for session rows only, as marvel
  prints them.

Never a row: tokens, keys, socket paths, settings values, message bodies,
route note or basis text, issue or PR body text, `next` or `repos` from the
ledger, people's names.

**This field list needs the operator's approval before the first row is
written,** as the ruling's cost says. A field not on the list is never
written: an overwritten document may keep its old version, so a field
copied once may stay readable (INFERRED, not checked for artifact data).

**Access.** The Desk's rule that only director writes is extended by name
to `dash_panels`, `dash_daily` and `dash_meta`, never widened to
Contributors.

## 4. The writer and its cadence (ruled, dash-writer and dash-refresh)

One added step in director's sweep, in a session:
1. Read `dws show --json` and fold it with dws's own fold, so the page cannot
   fold differently from the CLI.
2. Read `marvel get sessions --columns team,role,name,state,health,last-active`
   once per cluster. Before parsing, check that the header row names exactly
   those columns. On a mismatch, the Sessions panel's `refusal` names it and
   no rows are drawn from a table the writer cannot read.
3. Shape each panel, check each document against the 256 KiB document cap,
   and write a refusal into `refusal` instead of a cut-short panel.
4. Write every changed document in one batch of at most 50 writes, then
   `dash_meta/writer`.

It reads sources and never writes one, never writes a Desk card, and needs
no new credential.

**Cadence, as ruled:** each sweep, at most every 30 minutes. A panel whose
`as_of` is more than 60 minutes old says "stale since T" in text and keeps
its numbers. The ruling chose one mark, so a fresh write over a source that
has stopped moving reads as current. The header line says when the writer
last ran, in words, so a silent writer shows.

## 5. Tests (red first)

1. **The Desk is unharmed.** With `dash_*` collections missing, empty, or
   holding a malformed document, the decisions view renders the same cards
   in the same order.
2. **The field list.** The writer refuses to write any field not in
   section 3. A fixture row carrying `next`, `repos` or a token-shaped string
   writes nothing for that row, and the refusal says why.
3. **Header check.** A sessions table with a renamed, missing or extra
   column draws no rows and sets `refusal`.
4. **Size.** A panel over 256 KiB is not written; `refusal` says so.
5. **Stale.** With `as_of` 61 minutes old the panel shows "stale since T";
   at 59 minutes it does not.
6. **Merged and ideas.** A fixture with 17 merged and 128 idea rows shows
   two summary lines and lists neither in "In flight".
7. **No verdict words.** The rendered tab contains none of stuck, busy,
   free or idle.
8. **Write rule.** As a Contributor (`as_level` interact), a write to each
   `dash_*` collection is refused.

## 6. Rulings, 2026-10-19

| card | ruling |
|---|---|
| dash-home | a second tab on the Desk page |
| dash-writer | director's sweep, in a session |
| dash-data-model | one shaped snapshot per panel, 14 days of daily counts, an approved field list |
| dash-refresh | each sweep, 30-minute floor, stale after twice that |
| dash-merged-rows | merged rows as one summary line |
| dash-fourth-panel | Sessions per cluster, marvel's columns as printed |
| dash-sequence | not yet ruled |

## 7. Not covered

- kos, research and test-design views, and lifecycle maps: the second pass.
- Bus inbox depth per seat: no read path on main yet.
- "Routed and unacked" from the route ledger: no defined reader.
