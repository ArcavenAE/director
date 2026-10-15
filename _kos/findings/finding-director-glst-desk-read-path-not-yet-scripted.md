# finding-director-glst: a session reads the Desk in full; one headless attempt did not reach it, and no supported shell path was found

Spike K5 (aae-orc-ivvpm) of the director functions redesign, section 6, row K5.
The question: can a script read the Desk cards and answers, so that a missed
answer is caught? The answer feeds K10, K11 and K13.

## Answer

A Claude Code session can read both collections in full through its Artifact
data tool. A shell script could not, in the one headless attempt made on
2026-10-14, and no supported shell path was found (the CLI has no artifact
subcommand). That supports "not yet", not "cannot". A script can read what a
session wrote down.

## What was measured

1. **A session reads everything.** One `list` per collection with a limit of
   1000 returned 368 cards and 298 answers, written to local JSON files with
   the tool's `out_dir` option. Completeness was checked two ways: both counts
   are below the limit, and the files on disk match the counts the tool
   reported (368 and 298). I did not separately inspect a continuation token.
   The tool reads as the signed-in person, so no new credential is involved.
2. **The shapes are stable enough to script.** A card carries `area`,
   `expiry`, `kind`, `opened`, `options`, `owner`, `question`, `rec`,
   `ruling`, `source`, `status`, `title`. An answer, keyed by the same id,
   carries `at`, `choice`, `note`, `ready`. Of the 368 cards, 41 had status
   `open`, 306 `relayed`, 10 `closed`, and 11 carried one of five other values
   (`split`, `yours`, `withdrawn`, `resolved`, `superseded`). Of the 298
   answers, 297 had `ready` true. So "open" is a status value, and the status
   vocabulary is wider than open and closed; a card that is not `open` is not
   necessarily done.
3. **Headless did not reach it, in one attempt.** Invocation, so the run can
   be repeated: `claude -p --model haiku --allowedTools ArtifactData
   --max-budget-usd 0.25 --output-format json "<a prompt asking for one get>"`,
   with Claude Code 2.1.299 on macOS, which resolved the model to
   `claude-haiku-5-5`. The session reported the tool not available after two
   tool searches and read nothing. A different result on a rerun could mean a
   different harness build or a different invocation, so record both. The
   `claude` CLI has no subcommand for artifact data (`claude --help` lists
   agents, attach, auth, mcp, plugin and others). This shows the path is not
   ready, not that it cannot exist.
4. **Not tried, on purpose.** Calling the web endpoints behind the artifact
   directly would need the session's OAuth token. Under the custody boundary
   (SOUL section 3) a script should not hold that, so I did not look for it.

## What it means for K10, K11 and K13

- A reconciler that must read the Desk runs inside a session (the standing
  tick is one), unless the headless path is later found to work.
- A session can hand a script what it read: write the open card ids and the
  time of the read to a local snapshot, and let scripts check against that.
  A script that finds no snapshot, or one older than the limit, reports
  UNREAD, as section 4 of the design already requires for every source.
- For K13, `dws set --blocked operator` can refuse unless the id it is given
  appears in that snapshot as `open`, and print the snapshot's age. Without a
  usable snapshot it refuses and says UNREAD, not accept the id on faith.

## Snapshot design, what is open

**Who writes it, and how often.** Not decided here; it is K13 question 3 and
has gone to the operator. The options:

- (a) The session that runs the standing tick writes it on each tick, so its
  freshness is the tick interval.
- (b) A separate helper writes it from a JSON file a session produces, and the
  caller (a tick, or director by hand) decides when.
- (c) Nothing writes it, and K13 refuses every operator-blocked row until the
  headless path works.

My recommendation is (b), with the tick calling it, because it keeps the
writer testable apart from any session. This is a recommendation, and the K13
ruling decides it.

**What "old" means.** The design keeps every threshold in one operator-editable
table (section 4, the automation boundary). The snapshot age belongs there as
its own entry, with no constant in code. I recommend 24 hours as the starting
value, to be tuned by the operator.

**The single-session dependency.** With a snapshot as the only readable copy,
every script check depends on one interactive session having run and written
it recently. If none has, `dws set --blocked operator` refuses every call.
That may be the right trade, but it should be a stated one. The design already
carries its recovery path: "If the Desk snapshot reads UNREAD twice in a
week, the architect role re-presents Q4" (section 4). A K13 that refuses on a
stale snapshot is the mechanism that would make that line fire.

## Gaps

- The snapshot writer does not exist, and who owns it is the K13 ruling above.
  The Desk's write rule (director writes cards, the page writes answers) is
  unchanged.
- Whether the data tool can be enabled in headless sessions is a question for
  the harness, not something I could settle from here. If it can, a cron
  reader becomes possible and the snapshot step goes away.
- The status values `yours` and `resolved` are not defined in the design.
  K13 needs to say which count as open.
