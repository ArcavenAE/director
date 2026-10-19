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
- `stage`: one of the ledger's ten stages, or marvel's `state` value;
- `age_h`: hours since the row last moved;
- `area`: a label from the Desk's own area list;
- `title`: for ledger rows only, the title director typed, when it passes
  the content checks below;
- `health`, `last_active`, `cluster`: for session rows only.

**Content, not only fields.** The page has readers other than director
(Contributors see the Desk), so the writer bounds what each field may hold.
Every value is printable text with no control or escape characters. Then:

| field | allowed |
|---|---|
| `id` | lower-case letters, digits, `-` and `.`, at most 80 characters |
| `stage` | ledger rows: one of the ten ledger stages. Session rows: the grammar below |
| `age_h` | a number |
| `area` | one of the Desk's area labels |
| `cluster` | one of the cluster names in director's own cluster list |
| `health` | the grammar below |
| `last_active` | marvel's age words, a dash, or the stale form, plus an optional `*`; at most 40 characters |
| `title` | at most 120 characters, and none of the refusals below |

**Session STATE and HEALTH, matched to marvel main** (`cmd/marvel/main.go`,
the session row builder, and `internal/api` for the values):
- STATE is one base word, `pending`, `running`, `limited`, `succeeded`,
  `failed`, `crashloop-backoff` or `crashed`, then at most one suffix:
  - `failed (<tag>)`: marvel adds the start of the row's reason text, which
    is free text, so the writer writes `failed (terminal)`;
  - `succeeded (exit N)` or `crashed (exit N)`, with N one to three digits,
    written as printed.
  Nothing else follows the base word.
- HEALTH is one base word, `healthy`, `unhealthy` or `unknown`, then in
  this order, each at most once:
  - ` (stalled)`;
  - then at most one of ` (logged-out)` or ` (parked: <reason>)`. marvel
    sets them from one harness reading, so both never appear. The reason is
    free text and runs to the last `)` of the cell; the writer drops it and
    writes ` (parked)`.
  So `healthy (stalled) (parked: <reason>)` is written as
  `healthy (stalled) (parked)`.
- A suffix marvel adds later, or any other order, fails the grammar.

Refused in any free-text value (`id`, `title`, `last_active`):
- a path shape: `~/`, `/Users/`, `/home/`, `/private/`, `/tmp/`, `/var/`, or
  any run that starts with `/` and holds a second `/`;
- a token shape: a known token prefix (`ghp_`, `gho_`, `ghs_`,
  `github_pat_`, `xox`, `sk-`, `AKIA`), or any run of 24 or more base64 or
  hex characters;
- a URL (`://`) or an address with `@`;
- any entry in a private-names list, a local file director reads
  (`$DIRECTOR_STATE/dash-withhold.txt`, 0600, one name per line, matched
  as whole words and without regard to case). The file itself is never
  written anywhere.

**What each failure does.** Every failure is counted in the panel's
`refusal` by field and reason, never by value.

| field fails | outcome |
|---|---|
| `id`, `stage` or `cluster` | the row is not written: it cannot be placed or named |
| `health` | the row is written with `health` = `unreadable` |
| `last_active` | the row is written with `last_active` = `unreadable` |
| `area` | the row is written with `area` = `unlabelled` |
| `title` | the row is written with `(title withheld: <reason>)` |
| `age_h` not a number | the row is written with `age_h` empty |

A written placeholder is a fixed word, so nothing from the failed value
reaches the page. Titles
are otherwise written as typed.

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
2. Read `marvel --cluster <name> get sessions --columns
   team,role,name,state,health,last-active` once for each cluster in
   director's cluster list. A cluster that does not answer gets one line,
   "<cluster>: not reached at T", and no rows.

   **This parses a table formatted for people.** marvel prints it for a
   terminal, and a release can change it: a new health suffix, a new mark,
   a new form for an absent value. Nothing in marvel promises the layout. The
   parse is built to refuse when the layout moves, never to guess.
   - **The header.** The expected header labels are `TEAM`, `ROLE`,
     `AGENT NAME`, `STATE`, `HEALTH`, `LAST-ACTIVE`, the labels marvel's
     column registry gives those six column names. The header must be those
     six labels, in that order, each followed by two or more spaces except the
     last. `AGENT NAME` holds a space, so the header is never split on
     whitespace either. On any mismatch the panel's `refusal` names it and no
     rows are drawn.
   - **The split rule: fixed offsets from the header.** marvel lays the table
     out with Go's `tabwriter`, padding 2, spaces only, so every cell of a
     column starts at the same character offset as its header label. The
     writer takes each label's start offset, counted in characters, not bytes,
     and cuts each row at those offsets. A cell is the text from its offset to
     the next one, with trailing spaces removed; the last cell runs to the end
     of the line.
   - **A row that cannot be cut cleanly is refused, alone.** That is a row
     shorter than the last offset, a cell that starts with a space, or a cell
     with fewer than two spaces before the next offset (a cell that ran into
     its neighbour). Also refused: a row holding a tab or a control
     character. The panel shows that row as "unreadable row" in the panel's
     `refusal` count and draws the others. It never draws a shifted value.
   - Then each cell goes through the content checks in section 3.
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
2. **The field list and its content.**
   - The writer refuses to write any field not in section 3. A fixture row
     carrying `next` or `repos` writes nothing for that row, and the refusal
     says why.
   - A ledger title holding `~/work/x`, `/Users/a/b`, a `ghp_` token, a
     30-character hex run, a URL, or a name from `dash-withhold.txt` is
     written as `(title withheld: <reason>)`. The refusal text never repeats
     the value.
   - A session `id` with a `/`, or a `cluster` not on director's list, writes
     nothing for that row.
   - A HEALTH of `healthy (parked: /Users/a)` is written as `healthy (parked)`,
     and `healthy (stalled) (parked: a (b) c)` as `healthy (stalled) (parked)`.
   - STATE `crashed (exit 3)` is written as printed, `failed (budget)` as
     `failed (terminal)`, and `running (exit 3)` fails the grammar and drops
     the row.
   - A HEALTH of `healthy (parked: x) (stalled)` (wrong order) writes
     `unreadable`; a `last_active` over 40 characters writes `unreadable`; an
     unknown area writes `unlabelled`. Each is counted in `refusal`, and the
     page never shows the failed text.
3. **Header check and split.**
   - A sessions table with a renamed, missing or extra column draws no rows
     and sets `refusal`.
   - A row whose HEALTH is `healthy (parked: x y)` and whose LAST-ACTIVE ends
     in `*` parses into the right six cells, and HEALTH is written as
     `healthy (parked)`.
   - A row whose cell runs into the next column, a row with a tab in it, and a
     row shorter than the last offset are each refused alone. The other rows
     are drawn, and no value lands in the wrong field.
   - A cluster that does not answer shows "not reached" and no rows.
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
