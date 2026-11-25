# Director's standing loop: who runs each duty, how it is wired, and how it reports itself

Proposal, 2026-11-12. Nothing here is built. Section 8 splits it into one-PR slices,
each with a named red test; none is written. It builds on
`sim/design/director-bootstrap.md` (the start path, the plugin, the profile) and
does not repeat it.

## 0. Why

Director's standing duties (drain the inbox, read answered Desk cards, keep the
work ledger, write the dashboard and the digest, act on merge recommendations)
run only when a director session remembers them. finding-director-a53a named the
cause on 2026-10-14: the duties have no cue of their own, so a fast run of
operator questions crowds them out. Its first fix, `standing-hook`, was built.
Nothing wires it.

On the fleet that prompted this design, read on 2026-11-12:

- `last-drain` held a time about 20 hours old, though the session drained in
  batches all day;
- the hook was wired in no settings file;
- the Desk snapshot was about 47 hours old, with 71 open cards;
- the dashboard had last been applied ten days earlier, two runs ever;
- no digest existed;
- the work ledger and board were two days old;
- by director's own account, five operator decisions made that day reached
  the operator as chat text, not as Desk cards.

A second user who installs director today gets the scripts and none of the
triggers, defaults that name the first install's paths, and a Desk only a
Claude Code session can read or write. This design says which duty runs where,
how it is wired for any install, and how the loop tells someone when it has
stopped.

## 1. Who runs each step

The sweep in `skills/director/SKILL.md` ("Standing checklist") has seven steps.
The rule for sorting them: a step that consumes mail, needs judgment, or needs a
tool only a session holds stays with the session; a step that reads and computes
goes to a script on a schedule (SOUL section 9); the hook only reminds.

| step | runner | why |
|---|---|---|
| 1 drain the inbox | session | draining consumes mail; a script would take it from the model |
| 2 record the drain | the shim, in code | bookkeeping a session forgets (section 4) |
| 3 read answered Desk cards, picks | session | only a session reaches the Desk today (finding-director-glst) |
| 4 `dws refresh --local` | tick | local files only |
| 5 dashboard | session | `dash-writer run` prints writes only with a versions map a session reads from the Artifact; the tick reports "dash due" |
| 6 digest | tick, on an operator ruling (section 7) | read-only output |
| 7 answer QUERYs, act on MERGE RECOMMENDED | session | judgment; `merge-guard` stays the check before the act |

The tick also runs `dws waiting write` and the stalled-ideas scan (`dbi`), which
the hook must never run, and it reads presence for per-seat cue state. None of
these consumes mail.

## 2. The tick

`director tick` is one script, one pass, idempotent. Any scheduler runs it:
launchd, cron, a systemd timer, or a marvel scheduled role (aae-orc ADR-010,
Amendment 1). marvel is optional (aae-orc ADR-005).

It writes `$DIRECTOR_STATE/loop-status.json`: when it ran, one entry per step
(ok, skipped, or a gap with its reason), what is due for a session (drain, Desk
read, dashboard), and per-seat cue state read from presence (section 5).

The script is `skills/director/scripts/director-tick`. The file is written to a
temporary name and renamed, with this shape:

```
{"schema": 1, "ran_at": "<UTC>",
 "steps": {"<step>": {"status": "ok|skipped|gap", "reason": "<text unless ok>"}},
 "due": {"drain": bool, "desk_read": bool, "dashboard": bool},
 "seats": [{"id": "...", "cue": "...", "cue_reason": "..."}]}
```

The steps, in order: `desk_refresh` (`dws refresh --local`), `waiting_write`
(`dws waiting write`), `stalled_ideas` (`dbi --json`), `presence`
(`director-mcp roster --json`, whose rows give `seats`) and `decided_not_built`
(the rows of `desk/decided-not-built.seed.jsonl`, director's file, which the tick
reads and never writes). That step is skipped when the file is absent. When it
exists the status gains `"decided_not_built": {"count": N, "rows": [...]}`, each
row an object with a non-empty string `card` and optional `ruled_at` and
`forward` (a `bd:`, `pr:` or `finding:` edge), carried verbatim in file order. A
line that is not valid UTF-8, not a JSON object or without a `card` makes the
step a gap with one `line <n>: <why>` entry per bad line, and the good lines are
still carried. A step that fails is a
gap carrying the first line of its error, and never fails the tick. A step whose
tool is not installed is skipped, with that reason. A fifth step, `profile`,
appears only when the profile is refused. When `director-profile check` refuses
it, the step is a gap carrying the first error line, both defaults apply, and
the tick exits 0. When the tick itself refuses a `[loop]` value that is not a
positive integer, the gap names `loop.<key>` and that key's default applies. No
profile file at the default path, or one with no `[loop]`, stays clean.

What is due comes from the stamps the loop already keeps, never from mail:

- `drain`: `last-drain` is absent or older than `tick_interval` minutes;
- `desk_read`: `desk-reads.jsonl` is absent or last written more than 24 hours
  ago, the bound `dws` applies before it accepts an operator block;
- `dashboard`: `last_run` in `dash-writer.json` is absent or older than
  `digest_max_age` hours (12 by default).

`tick_interval` defaults to 30 minutes when the profile has no `[loop]`. That is
provisional, until the operator rules a cadence. Both values come from `[loop]`
and must be positive integers.

It never:

- types into any pane;
- sends or acks anything on the bus;
- writes the Desk;
- merges, relays, answers a card, files a ticket, or starts a session.

Its output is read on the next prompt, by the hook. With a `loop-status.json`
present, the hook prints one `loop:` line after the stalled-ideas line (tick age,
what is due, seats whose cue is not live) and adds the digest and ledger ages to
its waiting line when they pass 12 and 24 hours. The tick counts as stale past 90
minutes, `DIRECTOR_TICK_MAX_MINUTES`, which is provisional until the operator
rules a cadence, as `tick_interval` is. With no `loop-status.json` the hook prints
what it printed before. One clause is not built: "beat broken", which compares the
drain log of section 4 (slice 20) with `last-drain`. Relays asking the
operator to decide with no card are built in slice 11, and card batches parsed and
never checked in slice 13. Nothing acts on silence.
a53a's dissent stands: when nobody types, nothing reaches the session. This
design adds no alarm outside the hook; any alarm is for the ruling a53a already
asks for.

Running `director tick` by hand needs no ruling. Installing a schedule does:
`director init --schedule` prints the host's entry and installs it only on an
explicit `--yes`. The profile's `scheduler` defaults to `none`.

## 3. Wiring and portability for any install

The bootstrap design installs one start path and a Claude Code plugin whose
`hooks/hooks.json` wires the hooks; it is not built yet (`director-init` writes
the profile today and no hook wiring). This design adds to it:

- `hooks.json`: `UserPromptSubmit` runs `standing-hook`; a `SessionStart` line
  prints the tick's age and what is due.
- The loop scripts ship inside the plugin's per-version directory, so an
  upgrade and a rollback cover them, not only the launcher and the shim.
  Built as slice 8, hooks only: `plugin/director` holds the manifest, the hooks file
  and a link to the real `standing-hook`. `UserPromptSubmit` runs it with `--seen`
  (stamps `$DIRECTOR_STATE/hook-seen`, silent on failure); `SessionStart` runs it with
  `--loop-line --seen` (the loop line only, nothing with no tick). A plain run still
  writes nothing. `director start --plugin-dir PATH` loads a tree for one session. Built as
  slice 10: `director-install plugin` installs the tree and the loop scripts it runs
  (`standing-hook`, `dws`, `dbi`, `director-tick`, `director-profile`, `board-html`,
  `page-namespace`) into `lib/plugin/<sha>/`, with the link's target copied and a stable
  `lib/plugin/current` switched by rename, so an upgrade and a rollback cover the hook with
  the tree. `director start` loads `current` by default when it is a directory, an explicit
  `--plugin-dir` wins, `--no-plugin` skips it, and a `current` that is not a directory is
  skipped with a WARN. Running the install is the opt-in; without `current` the launcher
  hands claude main's arguments. The skill, `role.md` and `.mcp.json` stay out: the
  launcher keeps `--mcp-config`, because `--strict-mcp-config` skips plugin MCP servers.
- One profile table, `[loop]`, a closed set of optional keys: `state_dir`,
  `ticket_pattern` (a regex) and `workspace` are strings, `idea_roots` is a list
  of strings, `tick_interval` is whole minutes, `digest_max_age` is whole hours,
  and `scheduler` accepts only `"none"` until slice 16 widens it. An unknown key
  is refused as `loop.<key>`.

Five defaults today fit only the first install: the shipped idea roots, the
session's default workspace, the ticket id pattern the stalled-ideas scan and
`introspect` match, `introspect`'s project filter, and the manifests directory.
On another machine they return empty results, not errors. The keys now exist,
and no script reads them yet: each script keeps its current value as the
fallback, so an install that already exists is unchanged, and removing the
built-in values is a later, separate ruling. A fresh install ships a
comment-only `idea-roots.conf` and says so in one line, rather than another
user's paths; an existing file is never touched. The manifests directory stays
out of `[loop]`: it is marvel's, and `DIRECTOR_MANIFESTS_DIR` already overrides
it.

A user without Claude Code gets `director status`, which prints exactly what the
hook prints: it runs the standing hook (the one installed beside the launcher as
invoked, then beside the launcher a link points at, else the checkout's), so the output, the exit code and the environment are the
hook's. It takes no argument, so it never reaches the hook's `beat` or
`scan-beat`, and it writes nothing. With no hook to run it stops with exit 3.
crush can call it from its own hook. codex and opencode have no
pre-prompt hook, so the user's agent instructions ask the agent to run it. That
is a request, not a cue, and this design says so rather than claim parity.

## 4. The drain beat records the act

`last-drain` is written by the shim when a batch wait returns with `remaining` 0
on every tier. In the default mode a wait holds its messages unacked until the
reply is written (`cmd/director-mcp/ackafter.go`, `mcp.go`), and `remaining` is
read before that ack, so it counts the messages being returned. A batch that
returns mail never reads 0, and an empty wait never computes `remaining`, so the
beat never fires. One install's friction log recorded the symptom on
2026-11-12 (a batch of 5 reporting 5 remaining); this is the code-level cause,
read and not yet run.

The rule: a beat records the act, not a computed number. `remaining` stops
counting the call's own held messages; a completed drain writes the beat; an
empty wait is handled per section 7. Every age the loop prints reads fresh,
stale, or UNKNOWN with a reason, plus "beat broken" when a drain was logged
after the stamp, so a broken beat no longer looks like an idle director.

The drain log (slice 20). Director ruled the shape (ruling Q1 of this slice): an
append-only log beside `last-drain`, one line per completed drain, written by
the shim on the same path that writes `last-drain`.

- Path: `drain.log` in the state directory, so `$DIRECTOR_STATE/drain.log`, default
  `$HOME/.director/state/drain.log`. The shim resolves it with the function that
  resolves `last-drain`, and the hook with `state_dir()`, so the two files never
  sit in different places.
- Line: `<time> ended=<batch|silence> stamp=<written|failed>`, space separated,
  one line, newline ended. `time` is the RFC 3339 UTC stamp `last-drain` carries
  (`2006-01-02T15:04:05Z`), taken from the same clock reading as the stamp.
  `ended` is how the wait returned: `batch` for a wait that returned mail with
  every tier at 0 after it, `silence` for the empty wait of section 7. `stamp` is
  what happened to `last-drain` on that drain: `written`, or `failed` when the
  temp write or the rename returned an error.
- When: a line is appended exactly when the shim reaches the point where it
  writes `last-drain`, which is the condition `beatLastDrain` already tests (no
  failed tier read, every tier at 0). A wait that returns mail with some left
  waiting writes no stamp and no line, so a normal busy drain never reads as broken.
  The stamp is written first and the line second, so the line carries the
  stamp's result.
- Write: one `O_APPEND` write of the whole line, mode 0600, directory created as
  `last-drain` does it. A failure to append is logged and never fails the wait,
  as for the stamp. The file is never rewritten, truncated or rotated by the shim;
  at one line per drain (about 45 bytes) a silent wait every 2 minutes adds under
  35 KB a day. Rotation is not in this slice.
- Reader rule, in the hook: read the last line only (the final 4 KB of the file
  is enough). The beat is broken when the line's `time` is later than the
  `last-drain` stamp, or when `last-drain` is missing or unreadable while the log
  has a line. The clause reads
  `beat broken: a drain was logged at <time> but last-drain reads <stamp or UNKNOWN>`.
  When there is no `drain.log`, or the last line has the same time as the stamp or
  an earlier one, the hook prints nothing for it. A `drain.log` the hook cannot
  read or whose last line does not parse reads
  `beat: UNKNOWN (drain log <reason>)`, never a guess. As with the other loop
  clauses, nothing prints when `loop-status.json` is absent, so that output stays
  byte-identical.
- What it does not catch: a drain that never reached the shim's beat (a crashed
  shim, or a session that does not drain) leaves no line, which is the idle case
  the age already reports. A manual `standing-hook beat` newer than the log
  clears the clause, as it should.

## 5. The cue proves itself on real mail

The cue self-test is an MCP channel notice the shim sends its own client, not a
pane inject, so no pane rule applies to it. While a seat's cue is unverified,
every real cue already asks for a fresh nonce, and an echo makes it live; a live
seat with old mail reads as stalled. So no timed self-test: it would cost every
seat a model turn and prove nothing the next real cue does not. Re-proof is a
shim restart.

What is missing is a reader. Presence carries `cue` and `cue_reason`, but the
roster CLI drops both, and no standing step reads presence. On 2026-11-12 four
seats on one host showed "no nonce echo after self-test" at the same build as
sibling seats whose cue was live, so the build is not the difference; the cause
is not known. The fix here is to make the failure visible: the roster CLI
carries the fields, the tick copies per-seat cue state, and `director doctor`
and the hook list seats that are not live. The existing tests have the test rig
send the echo itself, so a host-run `verify-cue-live.sh` against a real client
becomes the release check.

## 6. The Desk and cards

**Cards come from the seat that knows the work.** Every role in the role library
already says "Desk writes stay as card files that director
ingests", but none names the Card standard or says when a card is owed, and the
relay template has no card field. The role text gains the standard by name and a
trigger: a question only the operator can answer is a card file, never chat.
`card-ingest template` prints a minimal valid card; the relay template gains an
"Operator decision: card file path, or none" field; `card-ingest check` stays
the structural check. A relay that asks the operator to decide and carries no
card goes back to the seat. A decision the operator makes in chat with director
has director as its author, and director writes that card itself.

**Director owns the store; the Artifact Desk renders it.** Under
`$DIRECTOR_STATE/desk/`, append-only:

- `cards.jsonl`, written by `card-ingest parse`;
- `answers.jsonl`, the answers a session read from the Artifact and, later,
  answers from a `director answer` command;
- `applied.jsonl`, what each renderer holds.

A reconcile reports one-sided or conflicting rows and never repairs one. The
outbox and the answer mirror add files and change nothing else. Making the local
log the record of answers, with `director answer` for a user without Claude
Code, needs an operator ruling.

## 7. Checking the loop itself

`director doctor` extends `director-init --check`. Structural rows may exit
nonzero: the hook not wired, a schedule named but absent, the profile invalid, a
state path unwritable. Every age reports and never fails a command (SOUL
section 8, ADR-007): last drain, Desk read, ledger, digest, tick, seats not live.
`doctor` is the only place "hook not wired" can show, since a hook cannot report
its own absence. The hook's waiting line carries the top items. Nothing becomes a
Desk card unless the operator must decide something.

Built as slice 9. `director doctor` runs a new
`director-doctor` script that prints `director-init --check`'s output first and unchanged, then
its own rows, and writes nothing. The hook counts as wired when a `UserPromptSubmit` command in a
Claude Code settings file (user level, or the `.claude` of the directory it runs in, each as
`settings.json` and `settings.local.json`) names `standing-hook` or resolves by realpath to the
installed one; the `hook:` row always lists the paths searched. A hook wired through a wrapper
with another name is a known false negative. The check reads Claude Code settings files only. When
none exists or parses, the hook is not checked and that is not a failure; a host that has a Claude
Code settings file is checked even when another harness is in use. Exit 1: `--check` drifts, the hook is not wired, or an existing
state directory is not writable; ages and seats only print.

The hook writes a seen-stamp when it is run with `--seen`, which the plugin's wiring
passes (slice 8), so `doctor` can tell a hook that is wired but never reached from
one that is not wired; a plain run writes nothing, so a hook wired through settings is
unchanged. On a harness other than Claude Code it cannot read the wiring at all. A wait that returns nothing,
with every tier at 0 and no failed read, writes `last-drain`, and the shim logs
`ended: silence`: an empty inbox at that moment is what the beat means.
`last-drain` stays one stamp line, as `standing-hook` and its verifiers read it,
so the reason is logged and not stored in it; the drain log of section 4
(slice 20) is where each drain is recorded as a line. Presence gains
two cue fields, present only when the cue is on. `cue_nonces_sent` is an integer:
the nonces sent while unverified, not counting the start-up self-test, which
tells "no mail since start" from "nonces sent and never echoed". `cue_proved_at`
is an RFC 3339 UTC time: when an echo last made the cue live, absent until the
first proof, which gives a live cue an age. Both are cleared on a shim restart.
The roster CLI passes `cue`, `cue_reason` and both fields through, each omitted
when absent.

## 8. Slices

Director repo unless named. RULING marks an operator ruling first. "Boot n" is
ticket n of `sim/design/director-bootstrap.md`, depended on and not duplicated.
Each red test is named here and written in its slice's PR; none exists yet.

| # | slice | red test (fails today because) | blocked by |
|---|---|---|---|
| 1 | Shim: `remaining` excludes the call's own held messages; a batch wait through the real call path writes `last-drain` | `TestRemainingExcludesTheCallsOwnHeldMessages`, `TestABatchWaitThroughCallToolWritesLastDrain` (ack-pending counts the batch) | none |
| 2 | Shim: an empty wait with every tier at 0 writes `last-drain` and logs `ended: silence` | `TestASilentWaitWithNothingPendingWritesLastDrain` (the empty branch never beats) | 1 |
| 3 | Shim: the roster CLI carries `cue` and `cue_reason`; presence adds the unechoed-nonce count and the last-proof time | `TestRosterJSONCarriesCueReason`, `TestPresenceCountsNoncesSentWhileUnverified`, `TestPresenceCarriesCueProvedAt` (no such fields) | none |
| 4 | Profile `[loop]` table and fresh-install defaults | `verify-director-profile.sh standing-keys-accepted` (unknown key refused); `verify-install-fresh-home.sh no-foreign-roots` (shipped roots name one install) | none (extends the built profile) |
| 5 | `director tick` writes `loop-status.json` | `verify-director-tick.sh`: fake `tmux` and `nats` log no call, status written, dashboard untouched (no command) | 3, 4 |
| 6 | Hook reads the status: tick age, digest and ledger age, seats not live, due items. Moved out: relayed asks with no card (after 11), card batches parsed and never checked (after 13), beat broken (after 20) | `verify-standing-hook.sh` case `digest_over_12h_waiting` and the loop-line cases (no such clauses); with no `loop-status.json` the output is byte-identical | 1, 5 |
| 7 | `director status` prints the hook's text | `verify-status.sh matches-hook` (no command) | 6 |
| 8 | Plugin `hooks.json`: `standing-hook`, a SessionStart status line, the hook's seen-stamp | `verify-plugin-hooks.sh names-standing-hook`; `verify-standing-hook.sh writes-seen-stamp` (no plugin, no stamp) | Boot 0 and the hook binding |
| 9 | `director doctor`, extending `director-init --check` | `verify-director-doctor.sh` cases `unwired-hook-exit1`, `stale-digest-exit0`, `seat-not-live-listed` (no command); its seen-stamp row follows 8 | 3, 5 |
| 10 | Loop scripts inside the plugin's per-version directory | `verify-director-install.sh rollback-restores-standing-hook` (scripts sit outside every version) | Boot 0 |
| 11 | `card-ingest template` (its Valid until is now plus 7 days from parse's clock); the relay template's "Operator decision: card file path, or none" field, after Ask and before Context; `relay-log append --card <path\|none>`, written as a `**Card:**` line after Kind (a widening of this row, ruled in scope by director 01M7R3ECP4); the hook's relayed-asks-with-no-card clause (moved here from slice 6): `relay-log` entries of Kind question in the last 24 h with no Card line, read from `$RELAY_LOG`, gated on `loop-status.json`, silent at 0, `UNKNOWN (<reason>)` when the log cannot be read, the oldest named by its R-number | `verify-card-ingest.sh template_parses_clean`, `template_valid_until_is_now_plus_7_days`, `has_operator_decision_field`; `verify-relay-log.sh` six `relay_log_card_*` cases; `verify-standing-hook.sh` four `hook_relays_with_no_card_*` cases (neither the subcommand, the field, the flag nor the clause exists) | none |
| 12 | The role library: the shared fragment names the Card standard and its trigger | a render check that every role carrying the Desk-writes clause names the Card standard (0 match today) | 11 |
| 13 | Desk outbox, additive (director 01M7R3ECP4): `card-ingest parse` also appends each log line to `desk/cards.jsonl` with a `batch` field, and `check` appends `{at, slug, batch, result, data_sha256}` per slug to `desk/applied.jsonl` (result PASS, MISMATCH or MISSING, for a batch `cards.jsonl` names); `card-ingest.jsonl` keeps every line it has. A read-only `card-ingest reconcile` prints NOT-APPLIED, NO-CARD and CONFLICT lines, repairs nothing and exits 0 on findings (a diagnostic; a gate on it needs its own written ruling). The hook's batches-parsed-and-never-checked clause (moved here from slice 6): batches in `cards.jsonl` that `applied.jsonl` does not name, gated on `desk/cards.jsonl` existing, silent at 0, `UNKNOWN (<reason>)` when either file cannot be read, the oldest named by batch file | `verify-card-ingest.sh parse-appends-cards-jsonl`, `refused_parse_adds_nothing_to_cards_jsonl`, `check_appends_applied_jsonl`, `check_records_a_mismatch_as_mismatch`; four `reconcile_*` cases beyond this row's list (director ruling 3); `verify-standing-hook.sh batch_parsed_not_checked_is_waiting` and four sibling cases (no outbox files, no subcommand, no clause) | none |
| 14 | Answer mirror: `desk-snapshot write --answers` appends the answers it read to `$DIRECTOR_STATE/desk/answers.jsonl` (director rulings A1 to A5): each row is the Desk's record verbatim plus `read_at` and `card`, an answer with no choice still a row; a row is appended only when its (card, choice, note, at) is new, decided under `desk/answers.lock`, so a repeat read adds nothing and a changed answer adds one row; written only with `--answers`, after the snapshot, and a refused write appends none; a failed append exits nonzero naming the file and keeps the snapshot. `desk-snapshot.json` and `desk-reads.jsonl` are unchanged | `verify-desk-snapshot.sh answers_persisted_with_choice` (choices are dropped), the `answers_row_*`, `answers_dedup_*`, `answers_a_changed_*` and `answers_a_failed_append_*` cases, and the guards `answers_no_write_*`, `answers_a_refused_write_*` and `answers_guard_snapshot_and_desk_reads_stay_byte_identical` | none |
| 15 | RULING (store): `director answer` and reconcile make the local log the record | `answer_without_artifact_reaches_snapshot`, `conflict_reported_not_repaired` | 13, 14 |
| 16 | RULING (finding a53a, fix 3): `director init --schedule` prints the entry and installs it only on `--yes` | `verify-director-init.sh no-install-without-yes` | 5 |
| 17 | RULING (digest): the tick writes the digest | `verify-dws-digest.sh runs-with-no-session` | 5 |
| 18 | `verify-cue-live.sh`, host-run against a real client, a release check and not CI | the script itself (absent) | 3 |
| 19 | SKILL.md: each standing step names its runner (session, tick, hook) and where its output lands | a doc check that steps 1 to 7 each name a runner (all are the session's today) | 5, 6 |
| 20 | Drain log and beat broken: the shim appends `drain.log` on each drain it stamps (section 4); the hook says "beat broken" when the newest line is later than `last-drain` | a Go test that a stamped drain appends one `<time> ended=<batch\|silence> stamp=<written\|failed>` line and a stamp failure still appends `stamp=failed` (no `drain.log` today); `verify-standing-hook.sh` case `beat_broken` (no such clause), with no `drain.log` the output is byte-identical | 1, 2, 6 |

Proposed as flat tickets with these blocks edges and no umbrella. None is filed;
they are filed after the operator reviews this design.

## 9. Recommendation

Valid until 2026-11-26, or until the operator rules on any item below,
whichever comes first; the architect seat that chaired this design re-checks it
then. Nothing here takes effect on silence.

Build slices 1, 3, 4, 11, 13 and 14 first: none needs a ruling, none blocks on
the bootstrap tickets, and slice 1 alone makes the drain age true again. Then 5,
6, 7 and 9, which give a second user the tick, the status line and the doctor
without a schedule installed. The plugin slices (8, 10) follow the bootstrap
design's ticket 0.

Three rulings are needed, each with the panel's recommendation:

1. **The digest on the tick (slice 17).** Recommended, 5-0. SKILL.md step 6 says
   both that a session runs the digest and that it "is not an action fired on a
   clock"; the digest writes a file and decides nothing, so writing it on the
   tick is reminding. One line in the skill changes.
2. **Installing a schedule (slice 16).** The ruling finding a53a already asks
   for. Recommended: allow it as opt-in per install (`--yes`, scheduler `none`
   by default), with the tick's never-list as its terms.
3. **The local log as the record of answers (slice 15).** Recommended: yes,
   after slices 13 and 14 have run for a while and reconcile shows how often the
   two stores disagree. It is what gives a user without Claude Code a Desk.

## 10. Dissent

- The hook seen-stamp (4-1): one seat holds that `doctor` reads the wiring
  structurally and the drain age already shows a hook that is wired but never
  reached, so the stamp adds a write to a read-only hook for little.
- The cue fields (4-1): one seat holds that the last-proof time alone is
  enough, since `cue_reason` already says a self-test failed.
- From finding a53a, still standing: with no schedule installed, the hook
  cannot fire when nobody types, so a card answered overnight waits for the
  next prompt. This design narrows that gap (the tick's status is waiting when
  someone types) and does not close it.
- Open, not dissent: why four seats never echo their self-test is unknown.
  Slices 3, 9 and 18 make it visible and measurable; none fixes it.

## 11. Prior art

- `_kos/findings/finding-director-a53a-standing-duties-have-no-cue.md`: the cause, and fixes 1 to 5.
- `_kos/findings/finding-director-glst-desk-read-path-not-yet-scripted.md`: no script path to the Desk yet.
- `sim/design/director-bootstrap.md`: the start path, plugin, profile and guard.
- `skills/director/SKILL.md`: the standing checklist and the Card standard.
- aae-orc SOUL sections 1, 2, 8 and 9; ADR-005, ADR-007, ADR-010.
