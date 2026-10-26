# Fleet health: one record, read every sweep

Status: design for review, 2026-10-26. Owner: the architect role. Docs only;
a director builder builds it after review. Nothing here is ratified.

The operator's ops view said every seat on one cluster was `healthy` while all
of that cluster's Claude seats sat at "Login expired · Please run /login" for
hours, and it showed a second cluster only as `not_reached`, with no time
since. The sources it reads (`marvel get sessions`, presence) report a process
that is alive, not a seat that can work. This design adds one read-only
record that says which seats are blocked and since when, so the operator sees
a stopped cluster on the page instead of in a rerouted review.

- Commission: the operator's request of 2026-10-26, relayed by director
  (01M5TX9P4E) and the arcaven supervisor (01M5TXASPG), item B.
- Builds on: `ops-dashboard.md` (the Desk ops tab and `dash-writer`),
  `skills/director/scripts/freebusy` (the cluster read), `composer-check`
  (the menu detector), and marvel's harness-state watchdog.
- Related upstream: ArcavenAE/marvel#1079 (no watchdog pattern for the
  expired-session screen), #703 (no pattern for the installed claude version),
  #801 (the statusline heartbeat keeps a stopped seat looking active).

## 1. What the record answers

Four questions, each with a since-time:

1. **Is each cluster reached?** Per cluster: `reached` or `not_reached`, the
   last time it was reached, and the first failed read of the current outage.
2. **Which seats are blocked at a prompt?** Per seat: a blocked class, read
   from a pane capture, and the first sweep that saw it. Classes:
   `auth-expired` (the expired-login screen), `consent-dialog` (the
   dev-channels consent dialog at startup), `automode-menu` (the "Teach auto
   mode about your environment?" menu), and `other-dialog` (a marvel watchdog
   `parked` verdict with its reason).
3. **Which seats have mail they have not taken?** Per seat: the oldest unread
   age from the durable's ack floor above the cue receipt window (300 s,
   `cmd/director-mcp/cue.go:59`), recorded once per episode. An episode opens
   on a live seat's first `over_threshold` row and closes on the first sweep
   without it.
4. **Do versions drift?** Per host: the marvel client build, each daemon's
   build, and the version the tap's `Formula/marvel.rb` names.

A reading it cannot take is a `GAP` with its reason, never `healthy` and never
0, which is the rule `freebusy` and `dash-writer` already follow.

## 2. The source

**An extension of `freebusy`, not a new script.** `freebusy` already lists the
clusters, reads each one under a timeout, keeps the last known state of a
cluster it could not read, and holds a sweep lock. A second script would
repeat all four and drift from them. The extension writes one more file:

```
$DIRECTOR_STATE/freebusy/health.jsonl   one row per cluster, seat and host, replaced each run
$DIRECTOR_STATE/freebusy/health-episodes.jsonl   one line per opened or closed episode, append-only
```

Reads, each read-only and bounded by `FREEBUSY_CALL_TIMEOUT`:

- **Reach:** the outcome of the `get sessions` read `freebusy` already makes.
- **Blocked:** first the watchdog's verdict, which is `harness_state` on the
  session, an object whose `.state` is `logged-out` or `parked` (with
  `.reason`). `get sessions` has no JSON form (see section 6), so the script
  reads it where marvel already prints it, the HEALTH cell: `(logged-out)` and
  `(parked: <reason>)`. Where the cell carries neither, and the seat is running
  and quiet, `marvel capture <session> --cluster C -e` and a match in the script: the expired-login line, the consent dialog's title,
  and the auto-mode menu through `composer-check --file`. Captures are taken
  only for seats whose marvel state is running and that are quiet (ACTIVE% 0
  or `-`), at most `FREEBUSY_CAPTURE_MAX` (default 40) per run, and the rest are
  `GAP: capture cap`. The script never passes `--repaint` or `--composer` to
  `capture`: both widen the pane by a column to force a redraw, which a seat
  that is only being read must not suffer. `-e` alone reads what the pane last
  drew. The script's own match is temporary. When marvel's
  watchdog covers a class (marvel#1079 for `auth-expired`), the script stops
  matching that class and reads `harness_state` alone.
- **Mail:** `director-mcp unread --json --older-than 300s`, the reader's own
  read-only mode. It works from each seat durable's ack floor, creates no
  consumer, acks nothing and writes no presence row, so a seat that is gone
  still shows the mail waiting for it. A row with `over_threshold` true is a
  live seat holding mail older than the 300 s window. `cue.unanswered` events
  are not read in this build.
- **Versions:** `marvel version` here (its first line), `marvel version
  --cluster C` for each daemon (its `daemon` line), and the `version` line of
  `Formula/marvel.rb` in a local checkout of the tap, the path set in
  `FREEBUSY_TAP_FORMULA` (no default, so an unset variable is a `GAP`).
  `freebusy` keeps its contract of no network and no `gh` call (its header,
  line 25), so the tap is read from disk. The value is as fresh as the
  checkout's last pull, and an unreadable file is a `GAP`, not a version.

Step 0 for the builder, before any test: confirm that `get sessions -o json`
carries `harness_state`, and that `capture` and `version` honour `--cluster`
on the installed client. Any that does not becomes a `GAP` with its reason and
a note on this design, not a workaround.

It never injects, answers a prompt, presses a key, restarts a seat, sends on
the bus, writes bd, or reads a credential. A capture is read and matched in
memory. No captured row is written anywhere, only the class, the pattern id
and the time, which is marvel's watchdog evidence rule. Seat and cluster names
pass through the `dash-withhold.txt` filter before they reach a page, as
`dash-writer` does now.

## 3. Where it shows

The request named three places. My recommendation is one home, with the
other two linking to it:

1. **Home: a "Fleet health" panel on the Desk ops tab.** `dash-writer` gains
   one panel, `dash_panels/health`, from `health.jsonl`: a row per blocked
   seat (cluster, seat, class, since), a row per unreached cluster (since),
   open mail episodes, and version drift. The ops tab is where the operator
   already looks, and `dash-writer` already holds the withhold rules, the
   30-minute floor and the batch limit, so a new panel inherits them. It is
   the fifth panel, so the batch is 11 writes (a panel and a daily document
   per panel, and the meta document) plus 5 per expired day, not 9 plus 4, and
   a backlog is cut to the oldest 7 days a batch (46 writes), not 10.
2. **Director: Fleet Metrics.** One "Fleet health" row in the "Right now"
   strip. Red means a blocked seat or an unreached cluster, amber means an
   open mail episode or version drift, and otherwise it is green. It carries
   counts only, with a link to the ops tab. No seat names, so it takes no new
   withhold path. Nothing in this repository holds the Fleet Metrics page or a
   `metrics/summary` document, so this build does not write one. The row reads
   the `counts` object of `dash_panels/health`, which `dash-writer` writes:
   `{blocked, not_reached, mail_open, versions, drift, level}` with `level` one
   of `red`, `amber` or `green`. A health record that is absent, unreadable or
   over an hour old leaves `counts` empty and the panel's `refusal` says why,
   so the row shows no level, never green. Wiring the row is the page owner's
   change.
3. **The typed Dashboard artifact: not used for this.** The copy read
   (sha 013f6a9e) is the type's runtime and holds no instance content yet. A
   third copy would need a third writer and a third withhold check, all for
   the same rows.

If the operator wants the Dashboard anyway, it should be built as a view over
`dash_panels/health`, never as a separate writer.

## 4. Tests, red first

- An expired-login capture fixture reads `auth-expired`. The same text quoted
  inside a transcript, above the composer, reads nothing.
- A consent-dialog fixture reads `consent-dialog`, and an auto-mode menu
  fixture reads `automode-menu` through `composer-check`.
- A seat whose HEALTH cell reads `(logged-out)` (marvel's rendering of
  `.harness_state.state`, an object, not a flat string) reads `auth-expired`
  with source `marvel`, wins over the script match, and no capture is taken
  for that seat. A `(parked: trust)` cell reads `other-dialog` with its reason.
- A cluster whose read times out is `not_reached` with its first-failed time.
  A second failed run keeps that time, and the first good run clears it.
- An `over_threshold` row for a live seat opens one episode. Three more sweeps
  over the window append nothing, and one without the row appends the close.
  A row that is not over the threshold opens nothing. A reader that is missing,
  exits nonzero or prints no report is a `GAP`, and leaves an open episode open.
- A capture cap of 2 with 3 eligible seats gives the third `GAP: capture cap`.
- No row in either file holds captured text, a token or a message body. This
  is checked against a fixture whose pane carries a secret-shaped string.
- The withhold list applies: a withheld seat name never reaches the panel.

## 5. Not covered

- **Clearing a block.** Pressing the dialog or running `/login` stays with
  the operator, or with a supervisor under its standing ruling on its own
  team's seats. The record only names the block.
- **The upstream fix.** marvel#1079 asks marvel to classify the expired
  screen itself. Until it lands, this script's match is the only reader.
- **Other harnesses.** Codex and opencode prompts are out of scope until a
  fixture for each is captured.

## 6. Step 0 results and build notes (2026-10-26, aae-orc-g974y)

Step 0 ran against the installed marvel client (help text and marvel source at
its main) with no live session read. Results:

- `marvel get sessions -o json` does not exist: `-o json|yaml` is for `get team`
  and `get workspace` only. The watchdog verdict reaches `get sessions` as the
  HEALTH cell suffixes `(logged-out)` and `(parked: <reason>)`, rendered from
  `.harness_state.state` and `.harness_state.reason` (`harness_state` is an
  object, `HarnessState` in marvel's `internal/api/types.go`). The script reads
  the cell, which it already fetches.
- `marvel version` prints the client line and then a `daemon` line, and
  `--cluster C` selects the daemon, so both honour `--cluster`.
- `marvel capture` honours `--cluster`. Of its flags only `-e` is used here.
- `director-mcp unread` is the mail source. On the installed build (a7ce948)
  the flags `--json`, `--older-than` and `--global` parse, and the row fields
  `oldest_unread_age_seconds`, `live` and `over_threshold` are in `unread.go`
  at main. `--older-than` takes a Go duration, so the window is `300s`; a bare
  `300` is refused with exit 2. Run against a closed port, the reader prints one
  stderr line and nothing on stdout, and still exits 0, so the script treats an
  empty or non-JSON answer as the failure signal, not the exit code. The check
  was not run against a live or scratch broker with real durables; the tests
  use a stub on PATH that prints the reader's JSON shape, and a first sweep on
  the real host is the remaining proof.
- The capture fixtures in `verify-freebusy.sh` are written from the screen
  names in section 1, not copied from a live pane. The expired-login match
  looks at the last three non-empty lines of the capture, so the same text
  quoted higher up, with a composer frame below it, reads nothing. It is
  temporary, as section 2 says, until marvel covers the class.
