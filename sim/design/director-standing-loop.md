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
the hook must never run.

## 2. The tick

`director tick` is one script, one pass, idempotent. Any scheduler runs it:
launchd, cron, a systemd timer, or a marvel scheduled role (aae-orc ADR-010,
Amendment 1). marvel is optional (aae-orc ADR-005).

It writes `$DIRECTOR_STATE/loop-status.json`: when it ran, one entry per step
(ok, skipped, or a gap with its reason), what is due for a session (drain, Desk
read, dashboard), and per-seat cue state read from presence (section 5).

It never:

- types into any pane;
- sends or acks anything on the bus;
- writes the Desk;
- merges, relays, answers a card, files a ticket, or starts a session.

Its output is read on the next prompt, by the hook. Nothing acts on silence.
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
- One profile table, `[loop]`: state dir, idea roots, ticket id pattern,
  workspace, tick interval, scheduler, digest maximum age.

Five defaults today fit only the first install: the shipped idea roots, the
session's default workspace, the ticket id pattern the stalled-ideas scan and
`introspect` match, `introspect`'s project filter, and the manifests directory.
On another machine they return empty results, not errors. They move into the
profile, and a fresh install writes empty roots rather than another user's
paths.

A user without Claude Code gets `director status`, which prints exactly what the
hook prints. crush can call it from its own hook. codex and opencode have no
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

The hook also writes a seen-stamp each time it runs, so `doctor` can tell a hook
that is wired but never reached from one that is not wired; on a harness other
than Claude Code it cannot read the wiring at all. A wait that returns nothing,
with every tier at 0 and no failed read, writes `last-drain` with `ended:
silence`: an empty inbox at that moment is what the beat means. Presence gains
two cue fields: a count of nonces sent while unverified, which tells "no mail
since start" from "nonces sent and never echoed", and the time of the last
proof, which gives a live cue an age.

## 8. Slices

Director repo unless named. RULING marks an operator ruling first. "Boot n" is
ticket n of `sim/design/director-bootstrap.md`, depended on and not duplicated.
Each red test is named here and written in its slice's PR; none exists yet.

| # | slice | red test (fails today because) | blocked by |
|---|---|---|---|
| 1 | Shim: `remaining` excludes the call's own held messages; a batch wait through the real call path writes `last-drain` | `TestRemainingExcludesTheCallsOwnHeldMessages`, `TestABatchWaitThroughCallToolWritesLastDrain` (ack-pending counts the batch) | none |
| 2 | Shim: an empty wait with every tier at 0 writes `last-drain`, `ended: silence` | `TestASilentWaitWithNothingPendingWritesLastDrain` (the empty branch never beats) | 1 |
| 3 | Shim: the roster CLI carries `cue` and `cue_reason`; presence adds the unechoed-nonce count and the last-proof time | `TestRosterJSONCarriesCueReason`, `TestPresenceCountsNoncesSentWhileUnverified`, `TestPresenceCarriesCueProvedAt` (no such fields) | none |
| 4 | Profile `[loop]` table and fresh-install defaults | `verify-director-profile.sh standing-keys-accepted` (unknown key refused); `verify-install-fresh-home.sh no-foreign-roots` (shipped roots name one install) | none (extends the built profile) |
| 5 | `director tick` writes `loop-status.json` | `verify-director-tick.sh`: fake `tmux` and `nats` log no call, status written, dashboard untouched (no command) | 3, 4 |
| 6 | Hook reads the status: tick age, digest and ledger age, beat broken, seats not live, relayed asks with no card, card batches parsed and never checked | `verify-standing-hook.sh` cases `beat_broken`, `digest_over_12h_waiting`, `batch_parsed_not_checked_is_waiting` (no such clauses) | 1, 5 |
| 7 | `director status` prints the hook's text | `verify-status.sh matches-hook` (no command) | 6 |
| 8 | Plugin `hooks.json`: `standing-hook`, a SessionStart status line, the hook's seen-stamp | `verify-plugin-hooks.sh names-standing-hook`; `verify-standing-hook.sh writes-seen-stamp` (no plugin, no stamp) | Boot 0 and the hook binding |
| 9 | `director doctor`, extending `director-init --check` | `verify-director-doctor.sh` cases `unwired-hook-exit1`, `stale-digest-exit0`, `seat-not-live-listed` (no command); its seen-stamp row follows 8 | 3, 5 |
| 10 | Loop scripts inside the plugin's per-version directory | `verify-director-install.sh rollback-restores-standing-hook` (scripts sit outside every version) | Boot 0 |
| 11 | `card-ingest template`; the relay template's "Operator decision: card file path, or none" field | `verify-card-ingest.sh template_parses_clean`; `has_operator_decision_field` (neither exists) | none |
| 12 | The role library: the shared fragment names the Card standard and its trigger | a render check that every role carrying the Desk-writes clause names the Card standard (0 match today) | 11 |
| 13 | Desk outbox: `card-ingest parse` appends `cards.jsonl`, `check` appends `applied.jsonl`; reconcile reports | `verify-card-ingest.sh parse-appends-cards-jsonl` (no file) | none |
| 14 | Answer mirror: `desk-snapshot` keeps answer choices in `answers.jsonl` | `verify-desk-snapshot.sh answers_persisted_with_choice` (choices are dropped) | none |
| 15 | RULING (store): `director answer` and reconcile make the local log the record | `answer_without_artifact_reaches_snapshot`, `conflict_reported_not_repaired` | 13, 14 |
| 16 | RULING (finding a53a, fix 3): `director init --schedule` prints the entry and installs it only on `--yes` | `verify-director-init.sh no-install-without-yes` | 5 |
| 17 | RULING (digest): the tick writes the digest | `verify-dws-digest.sh runs-with-no-session` | 5 |
| 18 | `verify-cue-live.sh`, host-run against a real client, a release check and not CI | the script itself (absent) | 3 |
| 19 | SKILL.md: each standing step names its runner (session, tick, hook) and where its output lands | a doc check that steps 1 to 7 each name a runner (all are the session's today) | 5, 6 |

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
