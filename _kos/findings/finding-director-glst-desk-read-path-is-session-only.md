# finding-director-glst: a shell script cannot read the Desk today; a running session can, and can write a snapshot a script reads

Spike K5 (aae-orc-ivvpm) of the director functions redesign, section 6, row K5.
The question: can a script read the Desk cards and answers, so that a missed
answer is caught? The answer feeds K10, K11 and K13.

## Answer

Not from a shell, on 2026-10-14. A Claude Code session can read both
collections in full through its Artifact data tool, and a script can read what
a session wrote down. Nothing else was found.

## What was measured

1. **A session reads everything.** One `list` per collection with a limit of
   1000 returned all documents, written to local JSON files with the tool's
   `out_dir` option: 368 cards and 298 answers, each in one call. The tool
   reads as the signed-in person, so no new credential is involved.
2. **The shapes are stable enough to script.** A card carries `area`,
   `expiry`, `kind`, `opened`, `options`, `owner`, `question`, `rec`,
   `ruling`, `source`, `status`, `title`. An answer, keyed by the same id,
   carries `at`, `choice`, `note`, `ready`. Of the 368 cards, 41 had status
   `open`, 306 `relayed`, 10 `closed`, and 11 carried one of five other values
   (`split`, `yours`, `withdrawn`, `resolved`, `superseded`). Of the 298
   answers, 297 had `ready` true. So "open" is a status value, and the status
   vocabulary is wider than open and closed; a card that is not `open` is not
   necessarily done.
3. **Headless does not reach it.** `claude -p` with the data tool allowed, on
   the haiku model, reported the tool not available in that session after two
   searches, and read nothing. The `claude` CLI has no subcommand for artifact
   data (`claude --help` lists agents, attach, auth, mcp, plugin and others).
   This is one attempt on one model; it shows the path is not ready, not that
   it cannot exist.
4. **Not tried, on purpose.** Calling the web endpoints behind the artifact
   directly would need the session's OAuth token. Under the custody boundary
   (SOUL section 3) a script should not hold that, so I did not look for it.

## What it means for K10, K11 and K13

- A reconciler that must read the Desk runs inside a session (the standing
  tick is one), not from cron or a shell hook.
- The session can hand a script what it read: write the open card ids and the
  time of the read to a local snapshot, and let scripts check against that.
  A script that finds no snapshot, or an old one, reports UNREAD, as section 4
  of the design already requires for every source.
- For K13, `dws set --blocked operator` can refuse unless the id it is given
  appears in that snapshot as `open`. Without a snapshot it should refuse and
  say UNREAD, not accept the id on faith. The check is as fresh as the last
  snapshot, which the refusal message should print.

## Gaps

- The snapshot writer does not exist; K13 or K11 has to own it. The Desk's
  write rule (director writes cards, the page writes answers) is unchanged.
- Whether the data tool can be enabled in headless sessions is a question for
  the harness, not something I could settle from here. If it can, a cron
  reader becomes possible and the snapshot step goes away.
- The status values `yours` and `resolved` are not defined in the design.
  K13 needs to say which count as open.
