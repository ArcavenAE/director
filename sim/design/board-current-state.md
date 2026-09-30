# The director board: current state over history (design and plan)

> **Status:** design and execution plan, draft. It comes from a board review
> and a five-round design party, both kept private because they discuss live
> board content. Every example here is synthetic. Effort level: rapid
> prototyping. The two widest items (the health strip and the alarms) get a
> token demonstration only.

## 1. The problem

The director board (`$DIRECTOR_STATE/board.md`) is authored prose: dated
sections, edited rather than overwritten, read before the roster in every
sweep. That makes it good history and a poor control surface.

- **The operator scans the whole file to find what needs them.** A board a
  few weeks old runs to well over a thousand lines of mixed old and current
  notes.
- **Filtering and ordering come from prose.** The renderer
  (`skills/director/scripts/board-html`) tags each section by matching
  keywords against its whole body, and gives an undated section the previous
  section's date. So an old note tags a live card, and "Jump to latest" opens
  the last section in the file, which the author may not have written last.
- **Nothing records who owes what, or since when.** A forwarded request, a
  review verdict with no disposition, or finished work that failed to
  deliver looks the same as a note, and it can quietly drop out of view.
- **Nothing is dated by verification.** The page cannot tell a fact checked
  ten minutes ago from one inherited from last week.

The review ranked nine changes, a to i:

- a. a current-state view over the history;
- b. structured items with stable ids and lifecycle fields;
- c. an attention queue;
- d. freshness and verification badges;
- e. field filters and saved views;
- f. an actionable default order;
- g. custody and delivery lanes;
- h. a cluster and agent health strip;
- i. event-driven overdue and missed-forward alarms.

## 2. The decision in one paragraph

board.md stays exactly what it is: authored history, with the director
session as its only writer. Item state moves to a new append-only ledger,
`$DIRECTOR_STATE/items.jsonl`, written only through one small CLI and folded
into a current-state projection on every read. The page shows that
projection first, as one capped panel in an actionable order, with the
history collapsed below. Views, filters, badges and lanes read item fields
only; nothing on the page derives state from prose. The sweep's four blocks
and its `<session> - <the ask>` line form do not change (section 8).

## 3. Writers and write sets

Every correctness rule in this design is one row of this table.

| file | writers | may write |
|---|---|---|
| `board.md` | the director session | authored prose only; never item state |
| `items.jsonl` | the CLI, acting as `director` | open, set, move, close, verify |
| | the CLI, acting as `dsx` | verify events only |
| | the CLI, acting as `timer` | alarm events only |
| `items.json` (projection) | the fold | rebuilt from scratch on every render; never read as a source |
| `sessions.json`, `roster.md` | `dsi` | the inventory, written atomically (temp file plus rename) |
| `board.html` | the renderer | the page, written atomically |
| `timer.last` | the timer | its own heartbeat |

No script or timer writes board.md. The bus is not a writer in this slice.

## 4. The item ledger

### 4.1 Events

Every line is one event:

```json
{"v":1,"eid":"<ulid>","item":"it-7q2k9x","kind":"open","at":"2026-01-01T12:00:00Z","actor":"director",
 "summary":"review verdict needs a disposition","state":"AWAITING_OPERATOR","owner":"seat-a","source_ref":"G000"}
```

| kind | actor | fields |
|---|---|---|
| open | director | summary, state, owner (required); recipient, due_at, next_action, artifact, source_ref, blocked_reason, unblock_owner (optional) |
| set | director | any optional field, or summary and owner; `null` clears |
| move | director | from, to, reason (optional) |
| verify | dsx, director | cmd, result (`ok`, `fail`, `error`), observed (short) |
| alarm | timer | rule (`overdue`), deadline (the `due_at` it fired on) |
| close | director | disposition (`done`, `dropped`, `superseded`, `duplicate`), to_item (optional) |

- **Ids are minted** by the CLI (`it-` plus six random base32 characters). An
  origin id such as a gate number is kept in `source_ref` and shown as the
  row label. Origin ids are not reused as item ids: different supervisors
  issue overlapping gate ranges.
- **Required at open:** `summary`, `state`, `owner`. Everything else is
  optional.
- **Derived, never stored:** `created_at`, `updated_at`, `last_verified_at`,
  the verification result, alarms, and every age.
- **Lines stay short** (under 4 KB) and are written in one append.

### 4.2 States

`WORKING`, `AWAITING_AGENT`, `AWAITING_OPERATOR`, `BLOCKED_AT_DELIVERY`,
`DELIVERED_PENDING_ACK`, `CLOSED`.

- Any of the first four may move to any other of the four, and to
  `DELIVERED_PENDING_ACK`.
- From `DELIVERED_PENDING_ACK`, an item moves to `BLOCKED_AT_DELIVERY`
  (delivery failed) or `AWAITING_AGENT` (sent back).
- `CLOSED` is reached only by `close`, from any state. A `done` close from any
  state other than `DELIVERED_PENDING_ACK` is kept and flagged
  `closed_without_ack`.
- A move into `BLOCKED_AT_DELIVERY` should carry `artifact`, `blocked_reason`
  and `unblock_owner`. A move missing them is kept and flagged
  `blocked_incomplete`.

The flags are diagnostics shown on the page. The fold never rejects a
legitimate history because it looks odd.

### 4.3 The fold

1. Order is line order, not `at`.
2. A repeated event id is ignored.
3. Each kind accepts only its listed actors. The actor is declared by the
   CLI flag, so this is an allowlist over a claim, not authentication;
   acceptable for a prototype with one operator's processes as the writers.
4. `open` for an existing id, or any other kind for an unknown id, is an
   error.
5. `set` is last write wins per field.
6. `move` applies only when `from` equals the current state and the step is
   in the table. Otherwise it is an error, and the state does not change.
   The CLI never writes such a line: it folds and checks under the lock and
   refuses a stale `--from` (section 4.5). Rule 6 therefore covers only lines
   the CLI did not write (a hand-appended or replayed line), where a
   mismatched `from` is the visible trace of a lost update.
7. `verify` does not change `updated_at`.
8. `alarm` counts only while the item is open and `deadline` equals the
   current `due_at`.
9. Nothing follows `close`. To reopen, open a new item with
   `source_ref: item:<old>`.
10. **The fold never fails and never drops silently.** Every rejected line
    goes into `errors` with its line number and reason, and the page shows
    the count.

Refolding the same ledger gives byte-identical output, and a duplicated line
changes nothing.

### 4.4 Concurrency

- **One lock.** Every writer takes `fcntl.flock` on `items.jsonl.lock`, with a
  5 second timeout, then fails loudly. The lock is released if the process
  dies, which a lock directory is not.
- **Fold, check, append, all under the lock.** This makes the transition
  check exact.
- **One line per event:** `O_APPEND`, newline, fsync, unlock.
- **A torn last line** (no newline) is skipped and counted, never repaired.
  Nothing rewrites the ledger. There is no compaction in the prototype.
- **Retries are safe.** Human-driven appends print their event id. A retry
  passes it back, so a retry of an append that landed is a no-op. Machine
  appends use a hash of their inputs as the event id. For the timer the
  inputs are item, rule and due date, so one deadline alarms once. For
  `dsx` they include the check time bucketed at the 10-minute skip, so a
  repeated passing check still advances `last_verified_at` and a verified
  item does not drift into the stale tier.

### 4.5 The CLI

This is one stdlib Python script beside `dsi` and `dsx`
(`skills/director/scripts/`), installed to `$DIRECTOR_HOME/bin`. Its name
is the operator's choice; this doc calls it `ditem`.

| verb | does |
|---|---|
| `open --summary S --owner O --state S [--due T] [--next N] [--source-ref R] ...` | appends `open`; prints the new id |
| `set ID field=value ...` | appends `set` |
| `move ID --from S --to S [--reason R]` | appends `move` |
| `verify ID --cmd C --result R [--observed O]` | appends `verify` |
| `close ID --disposition D` | appends `close` |
| `alarms [--notify]` | the timer's entry point (section 7) |
| `show [ID] [--json]`, `fold` | prints the projection; appends nothing |

An invalid event exits nonzero and appends nothing. After a mutating verb,
the CLI reruns the renderer when `board.html` already exists. That keeps the
page opt-in by existence, as the skill has it today. A render failure warns
but does not fail the append, because the ledger is the truth.

## 5. The page

### 5.1 The current-state panel (items a, c, f, and the lanes of g)

The panel sits directly under the header and is always open. History (the
authored sections) is collapsed below it.

**Header line.** Ages here are live, computed from the viewer's clock:

```
Now: 7 open, 2 overdue, 3 on you | ledger 14m | page 3m
```

When the page is over an hour old, the header adds: `This page is 5h old.
Re-render before acting.` When the page carries no render time at all (a
page rendered before this change), the header says `Page age unknown.
Re-render before acting.`, so an old page never looks fresh.

**Columns:**

| column | content |
|---|---|
| # | the row's rank in this view |
| Id | `source_ref` if present, else the item id |
| Ask | `summary`, truncated at 80 characters |
| State | a text chip |
| Owner | `owner`, followed by `→ recipient` when set |
| Age / due | `overdue 3h`, `due in 2h`, or `waiting 5h` |
| Verified | a badge |
| Next | `next_action`; the column is hidden when no item has one |

**Default order.** Each item appears once, in its highest tier. Within a
tier, the longest wait comes first.

1. overdue
2. `AWAITING_OPERATOR`
3. `BLOCKED_AT_DELIVERY`
4. due within 4h
5. `DELIVERED_PENDING_ACK`
6. stale (last verification over 24h old)
7. `WORKING` and `AWAITING_AGENT`

Rows are grouped by state, and each group shows a count. Closed items appear
only as a footer line: `Closed in the last 24h: N`. "Newest" means the
item's `updated_at`, never position in the file.

**Cap.** The panel shows 12 rows. A final row always reads `+N more open,
show all`, so nothing is ever truncated silently.

**Delivery rows (g).**

- A blocked item reads: "held at `artifact`; `unblock_owner` to unblock:
  `blocked_reason`".
- A pending item reads: "at `artifact`, awaiting ack from `recipient`".
- Missing fields show the `blocked_incomplete` badge.

**Row expand.** Expanding a row shows the item's event trail, oldest first.

### 5.2 Verification badges (d)

| badge | when |
|---|---|
| `verified 12m` | last verification is under 24h old |
| `verified 2d` | 24h or older; the item also enters the stale tier |
| `failed` | the last check failed |
| `unverified` | no verification yet (grey, not red) |
| `claim differs` | the check disagrees with the item's state, for example a merged PR on an open item |

PR and issue state belong to the repositories and to beadle's boards, which
read them live. The ledger never stores them as item state; it keeps only a
dated `verify` observation. `claim differs` is the one place the two meet.

Every badge carries text, so color is never the only signal. Checks come from
a `dsx` pass that reads only `id`, `artifact` and `state` of open items. It
chooses its check from the form of `artifact` alone:

- `owner/repo#N` is checked with the PR call `dsx` already makes;
- an absolute path is checked with `test -e`;
- anything else is not checked.

`dsx` never moves state. It skips an item verified in the last 10 minutes.

### 5.3 Filters and views (e)

**Views.** There are two views in the URL hash: `#now` (the default) and
`#history`. A filtered view is a bookmark:

```
#now?state=AWAITING_OPERATOR,BLOCKED_AT_DELIVERY&owner=seat-a
```

**Filter fields:**

- state;
- owner;
- recipient;
- due: overdue, due within 24h, or no due date;
- verified: fresh, stale, failed, or unverified;
- alarm.

Values within one field are combined with OR, and fields with AND. A filter
that matches nothing says `0 of N items match` and offers a clear link. Free
text search stays, but only over history.

### 5.4 Screen states, and what each means

"Nothing to do" must never look like "broken".

| state | the page shows | the operator concludes |
|---|---|---|
| normal | the panel | act from the top |
| empty | `Nothing open. Last item closed 2h ago.` plus the ledger age | nothing is owed, if the ledger is recent |
| no ledger | `No item ledger at <path>. Showing history only.` | the prototype is off or not installed; the sweep runs as before |
| bad lines | `2 ledger lines skipped` in the header | the ledger has a torn or invalid line; look before trusting counts |
| all unverified | grey badges and `0 of 7 verified. Run dsx.` | a verification pass has not run yet |
| stale page | `This page is 5h old. Re-render before acting.` | the file on screen is old, whatever it says |
| stale seat data | the seat strip greyed, `seat data 3d old; counts may be wrong` | run a sweep before reading seat counts |
| alarm | red `OVERDUE 2: ...` banner, not dismissible | an item is past due |
| silent timer | amber `Alarm timer silent 47m. Overdue items may be missing.` | the alarm timer has stopped; absence of alarms means nothing |
| timer not installed | muted `alarm timer not installed` | alarms are off by choice |

## 6. The health strip (h, token)

This is one line between the header and the panel, built from `sessions.json`:

```
Seats (this host): 2 busy, 11 idle, 1 shell | 6 no process | 1 unknown | 1 open item held by a seat with no process | seat data 3d old
```

(The counts in this example are synthetic.)

- **Labels.** Labels use the file's own words: busy, idle, shell, no
  process, unknown. They never say "dead" or "healthy".
- **Scope.** The strip says "this host", because liveness is a local process
  check.
- **Key clause.** Open items owned by a session with no process are the
  strip's point, since they are the Stranded set.
- **Staleness.** Past one hour the strip greys and leads with its age.
- **Privacy guard.** The renderer embeds only each session's project, name,
  status and the file's timestamp. A permanent test asserts that session
  goals, user text and working directories never appear in the page.
- **Grouping.** Grouping is by project today. Grouping by cluster needs a new
  `dsi` field, which is out of scope.

## 7. The alarm timer (i, token)

This is the first process in the design that runs without a director turn.

- **Schedule.** Run by launchd every 300 seconds, with `RunAtLoad` and no
  `KeepAlive`. Each run evaluates "now" and nothing else.
- **Each run:**
  - take the lock and fold;
  - for each open item past `due_at`, append one `alarm` event whose id is a
    hash of item, rule and due date, so one deadline alarms once however many
    runs see it;
  - write `timer.last`;
  - re-render if it appended.
- **Re-arming.** Moving `due_at` re-arms the alarm; closing the item ends it.
- **Missed forwards.** A forward is opened with `due_at` at forward time, so
  a missed forward is the same overdue rule.
- **Write set.** The timer may write alarm events, the page, its heartbeat,
  and a macOS notification behind `--notify`. The notification is off by
  default, and the header says `notifications off`. The timer may not write
  states, due dates, owners, acks, or board.md.
- **Install.** It is installed only by `install.sh --timer` and removed by
  `install.sh --untimer`, under a generic label
  (`<reverse-dns>.director.alarms`).

**The demo script.** The script runs in a scratch state directory with a
60 second interval, and refuses to run against the live state directory:

1. Positive control: after one tick there is a heartbeat and no alarm.
2. Open a synthetic item due in two minutes.
3. With no director turn, exactly one alarm with actor `timer` appears after
   several ticks.
4. Two concurrent runs still leave one alarm.
5. A new due date re-arms the alarm; closing the item ends it.
6. Negative control: unload the timer and re-render, and the page shows the
   timer as silent.

## 8. The sweep

The printed contract (skill "Mode: sweep", step 4: the four blocks and the
`<session> - <the ask in one line>` line form) does not change. An item's
`owner` is the session the line names today: the seat waiting on the
operator, or the seat whose ask is stranded. The skill text gains a few
lines:

- **Step 2.** After board.md, read the fold (`ditem show --json`). State comes
  from the fold, the story from the board.
- **Step 4.** Blocked on you and Stranded are filled from items where an item
  exists. The line form is the skill's own, `<session> - <the ask in one
  line>`:
  - `<session>` is the item's `owner`;
  - the ask is `<source_ref> <summary>`, or the summary alone when the item
    has no `source_ref`. The minted id never appears;
  - a Stranded line keeps `(died Nh ago)`, taken from the owner's session in
    `sessions.json`, because Stranded means an ask whose owner has no
    process.
  "Detail by number" still means list position.
- **Stale moves.** The director reads the fold at step 2 and moves items at
  step 5. If an item changed in between, the CLI refuses the move (its
  `--from` no longer matches) and appends nothing. The director refolds and
  retries with the current state. Two `set`s on one item are fine: last write
  wins per field.
- **Step 5.** Open an item for each surfaced ask that lacks one. Move or close
  items whose state changed. Never write item state into board.md.
- **Standing mode.** After relaying a forward, open it with `--due`.
- **What the sweep must not do:**
  - run the alarm timer's entry point;
  - parse board.md to open items;
  - fail when the CLI is absent. Without it, the sweep runs as today.

The step 4 addition sits inside the step's text without changing its shape,
so it needs an operator ruling.

## 9. Install, upgrade, and which renderer ran

**Where tools land.** `install.sh` links the skill and places tools in
`$DIRECTOR_HOME/bin`. It never deletes, and refuses to replace a regular
file.

**The failure that started this.** An installed renderer that predates the
repo's renderer (a plain file, not a link) stops `install.sh` at its first
refusal. Every later target is then skipped, and the page is rendered by the
old copy.

**The fixes:**

- `install.sh` continues past refusals, reports each one, and exits nonzero
  at the end;
- the skill names the renderer by its absolute installed path;
- the page footer prints the renderer's git revision, or `untracked copy`.

**Link or copy.** In link mode the tools follow whatever branch the checkout
has; builders work in worktrees, so a branch switch does not change the
fleet's renderer. In copy mode a repo fix arrives only on reinstall. The
footer revision makes either case visible.

**Operator step before anything is visible:** move the stale installed
renderer aside, then rerun `install.sh`.

## 10. Off switch and rollback

**Running without it.** When the CLI is absent, the sweep runs as today. When
the ledger is absent, the page shows the no-ledger state and the history.

**Removing it.**

1. Move `items.jsonl` and `items.json` aside.
2. Run `install.sh --untimer`.

board.md was never written by any part, so nothing needs restoring there.

## 11. Parts, in dependency order

These are flat work items with dependency edges.

| part | scope | depends on |
|---|---|---|
| P0a | operator: move the stale installed renderer aside, rerun install.sh | none |
| P0b | skill names the renderer absolutely; install.sh continues past refusals and reports them | none |
| SH1 | dsi writes sessions.json and roster.md atomically | none |
| SH2 | ledger and CLI (all verbs, validation, minted ids, optional fields, one locked append path, install line) | none |
| SH3 | fold (dedupe, actor allowlist, transitions, diagnostic flags, derived times, counted errors) and the shared synthetic fixtures | SH2 |
| SH4 | renderer reads the projection; UTC embedded; page-script ages; a fixed test clock (`DIRECTOR_NOW`); page-age banner; renderer revision in the footer | SH3 |
| A1 | the panel: tiers, state groups, cap, header counts, screen states, badges, `#now` and `#history` | SH4 |
| B1-demo | the b+a demo on synthetic items | SH2, A1 |
| B1-seed | operator: open the live items with the CLI | B1-demo |
| D2 | dsx verify pass | SH2, SH3 |
| E1 | filter parameters and chips, in one named function | A1 |
| G1 | delivery row texts and the incomplete badge | SH3, A1 |
| H1 | the seat strip, with its permanent privacy test | SH1, SH3, SH4 |
| I1a | timer core: alarm rule, deterministic ids, heartbeat, plist, `--timer` and `--untimer`, the demo script | SH3, P0b |
| I1b | alarm, silent-timer and not-installed banners | I1a, A1 |
| K1 | skill text for the sweep (section 8) | SH2, SH3 |
| K2 | optional: install dsi and dsx; absolute paths for all | P0b |
| X1 | board.md snapshot before director edits, only on an operator ruling | none |
| DOC | this document | none |

**Pull requests.** No pull request is based on another's branch.

| PR | parts | notes |
|---|---|---|
| PR1 | P0b, SH1 | runs in parallel with PR2 |
| PR2 | SH2, SH3, SH4, A1, B1-demo, D2 | after PR1 merges, fold main in and rerun P0b's install test |
| PR3 | E1, G1, H1, I1a, I1b | after PR1 and PR2 merge |
| PR4 | K1 (and K2) | alone, because its step 4 line waits on a ruling |

**Operator steps with no PR:** P0a (first), B1-seed, and the i demo run.

### Acceptance, one line per part

Every test runs against a scratch state directory and the synthetic
fixtures.

- **P0a:** after a render, the page carries the auto-refresh control, and
  `director-install` is installed.
- **P0b:** with one plain-file target in a scratch home, `install.sh` names
  it, installs every other target, and exits nonzero.
- **SH1:** both dsi writes use a rename; a reader loop sees no truncated
  file.
- **SH2:** an invalid `open` exits nonzero and appends nothing; a valid one
  appends one line with an event id. Two processes each append 200 events at
  once, giving 400 parseable lines with unique ids. Two sequential `move`s
  with the same stale `--from`: the second exits nonzero and appends nothing.
- **SH3:**
  - refolding the ledger, with a duplicated last line, is byte-identical;
  - in the `replayed-move` fixture (a hand-appended `move` line with a wrong
    `from`), the fold adds exactly one error and leaves the state unchanged;
  - two concurrent closes on one item: one is accepted, one rejected.
- **SH4 and A1:** with `DIRECTOR_NOW` fixed:
  - the tiers fixture renders its first 13 row ids in the expected order;
  - each degraded fixture shows its expected text.
- **B1-demo:**
  - the six-step script exits 0;
  - board.md's hash is unchanged;
  - a duplicated event changes nothing.
- **D2:** a missing-path artifact yields one `fail` verify event; an
  immediate rerun adds nothing.
- **E1:** the filter function returns 1 of 3 on a state filter, and an
  unknown value gives the zero-match text.
- **G1:** a blocked move without an unblock owner applies, and the page shows
  the incomplete badge.
- **H1:**
  - the stale-seats fixture shows the held-by-no-process clause, `this host`
    and the muted style;
  - the page contains no session goal, user text or working directory.
- **I1a:** the demo script exits 0 with its positive and negative controls.
- **I1b:**
  - the alarm state shows the alarm banner and the stopped state shows the
    silent-timer banner;
  - neither banner has a dismiss control.
- **K1:** the sweep's step 4 fence is byte-identical to main, and from the
  `sweep` fixture the rendered lines read `<owner> - <source_ref> <summary>`,
  `<owner> - <summary>` for an item with no `source_ref`, and a Stranded line
  ends in `(died Nh ago)`.

## 12. Rulings the operator holds

1. **P0a.** Moving the installed file is the operator's act.
2. **The step 4 text line** (section 8).
3. **X1.** Whether board.md gets a snapshot before director edits. It has no
   history today.
4. **The CLI's name.**

## 13. What this does not do

- **Unopened asks.** The ledger knows only asks someone opened. An unopened
  ask is exactly as invisible as it is today, so the authored Uncaptured
  block remains the only catch until the bus writes items.
- **Fleet health.** The health strip is one host's snapshot, not fleet
  health.
- **Event-driven alarms.** The alarm timer is a clock, not an event consumer.
  A bus-driven version of item i is later director software.
