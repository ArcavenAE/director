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
3. **Which seats have mail they have not taken?** Per seat: presence
   `oldest_pending_s` above the cue receipt window (300 s,
   `cmd/director-mcp/cue.go:59`), recorded once per episode. An episode opens
   on the first sweep over the window and closes on the first sweep under it.
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
- **Blocked:** first the session's `harness_state` from `marvel --cluster C
  get sessions -o json` (`logged-out`, or `parked` and its reason). Where that
  is absent or `unknown`, `marvel --cluster C capture <session> -e` and a
  match in the script: the expired-login line, the consent dialog's title,
  and the auto-mode menu through `composer-check --file`. Captures are taken
  only for seats whose harness is claude and whose marvel state is running,
  at most `FREEBUSY_CAPTURE_MAX` (default 40) per run, and the rest are
  `GAP: capture cap`. The script's own match is temporary. When marvel's
  watchdog covers a class (marvel#1079 for `auth-expired`), the script stops
  matching that class and reads `harness_state` alone.
- **Mail:** `director-mcp` presence, the `oldest_pending_s` field, and
  `cue.unanswered` events where the seat emitted one.
- **Versions:** `marvel version` here, `marvel --cluster C version` for each
  daemon, and one read of the tap formula per run.

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
   30-minute floor and the batch limit, so a new panel inherits them.
2. **Director: Fleet Metrics.** One "Fleet health" row in the "Right now"
   strip. Red means a blocked seat or an unreached cluster, amber means an
   open mail episode or version drift, and otherwise it is green. It carries
   counts only, read from `metrics/summary`, with a link to the ops tab. No
   seat names, so the summary takes no new withhold path.
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
- `harness_state: logged-out` from `get sessions -o json` wins over the script
  match, and no capture is taken for that seat.
- A cluster whose read times out is `not_reached` with its first-failed time.
  A second failed run keeps that time, and the first good run clears it.
- `oldest_pending_s` of 301 opens one episode. Three more sweeps over the
  window append nothing, and one under it appends the close.
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
