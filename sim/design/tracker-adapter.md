# A tracker adapter: director's seat tooling reads work items without calling bd

Status: design for review, 2026-11-06. Owner: the architect role. Docs only; no code lands until this is reviewed.

Code read at director `4c994ca`.

Source: a research report of 2026-11-06 on how far director should depend on bd (its sections 3, 4 and 6: the minimal read-only surface, the failure modes and the test-architect view). Its director citations were re-checked at `4c994ca` and hold, with one path correction: the adapter precedent is `skills/director/reference/adapters.md:6-10`. The report's recommendation that this design is the first step, ahead of bead-backed rows and dispatch-as-claim, is taken as given.

## 0. Why

Director's seat tooling calls bd by name in three scripts. On a team with no bd, those reads fail. And a failure has one shape whatever the cause: a missing binary, a refused server and an unknown id all come back as the same "unreadable" warning or "bd ready failed" gap. So a team that never had a tracker reads as a team whose tracker is down.

A small read-only adapter gives each script one surface to call. It names which tracker a team uses (bd, a directory of files, or none), and it says why a read failed. That keeps director independent of any one tracker, which is the platform's independence rule applied to director's own scripts.

Cost of the change: on a team that keeps bd, two things change.
- Every failure line gains a class.
- `idle-shadow` no longer finds the label convention by its working directory. A deployment that relies on that lookup sets `label_convention` once (section 6); until then, its run shows the components gap where it now shows components.

Nothing else changes: an unset configuration selects bd, and each caller keeps its current timeout.

## 1. The call sites, verified

| script | line | what it does with bd |
|---|---|---|
| `skills/director/scripts/dws` | 789 | `LINK` accepts `bd:<id>`, grammar `[A-Za-z0-9][A-Za-z0-9._-]*` |
| `dws` | 877-883 | `fetch` runs `bd show <id> --json` through `run_json` (804-808), and reads `status` and `updated_at` |
| `dws` | 945-949 | `refresh` catches `RuntimeError`, `OSError` and the rest as one class, and writes `<slug>: <link> unreadable: <err>` |
| `dws` | 2692-2705 | decision links accept `bd:` with the same grammar |
| `skills/director/scripts/idle-shadow` | 86 | `bd="${IDLE_SHADOW_BD:-bd}"` |
| `idle-shadow` | 196, 211 | `bd ready --exclude-label scope:client --json -n 0`, and the same with `--label scope:client` piped into a counter |
| `idle-shadow` | 219-237 | any failure is `items: bd ready failed (<first stderr line>)` or the client-count gap |
| `idle-shadow` | 241-251 | the label convention is read from `$IDLE_SHADOW_LABEL_CONVENTION`, else `.claude/skills/task-workflow/SKILL.md` at the git top level. The Component row of that file names the components |
| `skills/director/scripts/dbi` | 51 | `BD_ID` matches one hard-coded id prefix |
| `dbi` | 246-254 | `bd_tickets` runs `bd sql --json "select id, status, description from issues"` and returns `None` on any failure |

The tests stub bd per script: `verify-dws-refresh.sh` writes a `bin/bd` that answers from `bd-<id>.json` fixtures (lines 50-53), and `verify-idle-shadow.sh` has its own stub that refuses a call without `--json` (line 48).

So bd is named at nine lines across three scripts: `dws` 789, 877, 2692 and 2703; `idle-shadow` 86, 196 and 211; `dbi` 51 and 248. The label lookup at `idle-shadow` 241-251 is a tenth coupling, to one orchestrator's file layout. `run_json` cannot tell a missing binary (`FileNotFoundError`, an `OSError`) from a refusal (a nonzero exit with stderr) once `refresh` catches both, and `dbi` returns the same `None` for every failure.

## 2. The surface

One executable, `skills/director/scripts/trk`, installed beside the others in the bin home. The Python scripts call it as a process, as they already call `bd` and `gh`, and the bash ones do the same, so there is one copy of the adapter logic and no import path to keep in step. Every subcommand reads only. Output is JSON on stdout. The class of any failure is the exit code, with one JSON line on stderr.

| subcommand | returns |
|---|---|
| `trk capabilities` | `{"adapter": "bd", "read": true, "ready": true, "list": true, "note": false, "create": false, "claim": false}` |
| `trk parse <ref>` | `{"adapter": "bd", "id": "x-1a2b", "ref": "trk:bd:x-1a2b"}`, or exit 2 |
| `trk read <ref> [--timeout S]` | one item (below) |
| `trk ready [--label L]... [--exclude-label L]... [--timeout S]` | a JSON array of items. Optional: an adapter with `ready: false` exits 3 |

**Capabilities.** The names cover the read calls and the write calls a later version might add (`note`, `create`, `claim`), so a caller can ask before it tries. In this version every write capability is `false`. A capability that is absent or `false` means UNREAD: a caller reports the read as not done, never as an empty answer. This is this design's own rule (TA-5). It follows the spirit of `skills/director/reference/adapters.md:6-10` for harness adapters ("An adapter declares what IT supplies"), which does not itself state the UNREAD reading.

**The ref.** `trk:<adapter>:<id>`. `<adapter>` matches `[a-z][a-z0-9-]*`. `<id>` keeps today's bd grammar, `[A-Za-z0-9][A-Za-z0-9._-]*`, whose first character keeps an id from ever reaching a CLI as an option (`dws:786-788`). The `files` adapter also refuses an id that is `.` or `..`, so an id can never name a path outside its root. `bd:<id>` is an alias for `trk:bd:<id>`, and `parse` returns the long form, so existing rows and cards keep working unchanged. A ref that names an adapter other than the configured one exits 3, naming both. It is never read through the configured adapter instead.

**The item.** Normalized, so no caller reads a tracker's own field names:

```json
{"ref": "trk:bd:x-1a2b", "id": "x-1a2b", "state": "active",
 "native": "in_progress", "moved_at": "2026-11-05T12:00:00Z",
 "created_at": "2026-11-01T09:00:00Z", "title": "...",
 "priority": 1, "labels": ["director"]}
```

- `state` is one of `open`, `active`, `deferred`, `done` or `unknown`, the report's vocabulary. `native` is the tracker's own word, kept for display: bd's `in_progress` reads `active`, `closed` reads `done`, and `blocked` reads `open` with `native: blocked`, since whether an item can be worked is the `ready` call's question, not the state's.
- `moved_at` and `created_at` are UTC with a `Z`, or null. The adapter does the time conversion that `dws` does today at 880-882.
- `title` is returned by `read` only. `ready` items carry no title, so `idle-shadow` keeps its rule that a title or text is never held or printed (`idle-shadow:12-13`).
- `priority` is an integer or null. `labels` is a list of strings, possibly empty.
- `ready` items carry the other fields. `idle-shadow` keeps its own filter on top, and still drops a `scope:client` item that the tracker failed to exclude.

## 3. The three adapters

| adapter | read | ready | for |
|---|---|---|---|
| `bd` | `bd show <id> --json` | `bd ready --json -n 0` with the label flags passed through | teams on bd, which is every team today |
| `files` | `<root>/<id>.json` | every item whose state is `open` and whose `blocked_by` list names no item that is not `done`, so an item blocked by an `active` one is not ready, as in bd | teams that keep work items as files, such as clean-room teams; also the test double for every caller |
| `none` | exit 3 | exit 3 | a team with no tracker; `capabilities` reports every capability `false`. This is a valid way to run: links still display, and nothing refreshes |

**The files format** is the item shape of section 2, plus optional `title` and `blocked_by` (a list of ids), one file per item, named `<id>.json`. The adapter never writes it. A team whose files use another shape gets a variant adapter later; this design does not guess at formats it has not read.

**One test double, where a ref allows it.** Because `files` is a real adapter, a caller's test can point `trk` at a fixture directory instead of stubbing bd. That works for `idle-shadow`, whose `ready` read names no adapter, and for `dws` cases that use `trk:files:` refs. It does not work for a stored `bd:` row: `bd:` is an alias for `trk:bd:`, and a ref naming another adapter exits 3 rather than being read through the configured one (section 2). So `verify-dws-refresh.sh` keeps its bd stub for `bd:` rows. That stub moves into `trk`'s own verify script, backed by the recorded bd fixtures (section 4), once the bd adapter has them. The `idle-shadow` stub can go when its cases move to the files double.

## 4. Failure classes

The point of the design is that these stop reading alike:

| exit | class | example | how the adapter knows |
|---|---|---|---|
| 0 | ok | | |
| 2 | bad_ref | `trk:Bd:x`, `pr:o/r#1` | grammar |
| 3 | not_configured | adapter `none`, or a ref naming another adapter, or `ready` on an adapter without it | configuration, before any exec |
| 4 | missing_binary | bd not on `PATH`, or not executable | `shutil.which` before the exec, and `FileNotFoundError` or `PermissionError` from it |
| 5 | refused | bd ran and exited nonzero for a server or connection reason | nonzero exit, stderr not matched as not_found |
| 6 | not_found | the id is unknown | nonzero exit with bd's not-found text; for `files`, no such file |
| 7 | malformed | the answer is not the JSON expected | parse failure |
| 8 | timeout | no answer within the bound | the adapter's own bound, kept in process. Each caller passes its current value with `--timeout`: 60 s from `dws` (`run_json`, 805) and `dbi` (248), 30 s from `idle-shadow` (87). With no `--timeout` and no configured value, the default is 60 s. It never wraps the call in an external `timeout` binary: on one host that binary is not on `PATH`, and a wrapper that cannot be found turns every read into what looks like an outage |

The stderr line is `{"class": "refused", "adapter": "bd", "detail": "<first line of bd's stderr>"}`.

A third cause the report names: the binary is present but its client environment was never installed, so it cannot reach any server. That exits 5, `refused`, with bd's own detail. The detail is what tells it apart from a server that is down, and the design does not try to class it further.

**One thing to measure before the build.** Telling refused from not_found depends on bd's exit code and stderr text for an unknown id, against a server that refuses or is unreachable. I have not run bd for this design. The builder's first step is to capture both cases from a scratch bd and pin them as fixtures. Where they cannot be told apart, the adapter reports `refused` with the detail, never `not_found`, because a dangling reference reads as an answer and a refusal must not.

**What the callers print.**
- `dws refresh` keeps its warning line and adds the class: `<slug>: trk:bd:x unreadable (refused: <detail>)`, or `(missing_binary: bd is not on PATH; set DIRECTOR_TRACKER=none if this team has no tracker)`.
- A not_found ref becomes a dangling-reference hint, as `dbi` already treats a PR that `gh` cannot find.
- `idle-shadow` gaps name the class: `items: tracker ready failed (missing_binary)`, or `items: no tracker (adapter none)`. A team with adapter `none` gets a gap that says so, never a failure.
- `dbi` passes the class through, where it now returns `None`.

## 5. Moving the callers

1. **dws.**
   - `LINK` (789) and `DECISION_LINK_KINDS` (2692) accept `trk:` beside `bd:`. `bd:` stays valid and is not rewritten in stored rows.
   - `fetch` (877-883) calls `trk read` and passes `--timeout 60`. It returns `{"kind": "trk", "adapter": ..., "state": ...}` for a `trk:` ref, and keeps `kind: bd` with `state` set to the item's `native` word for a `bd:` ref, so stored observations compare as before and no row records a move it did not see.
   - `refresh` (945-949) reads the exit class.
2. **idle-shadow.**
   - The two reads (196, 211) call `trk ready` with the same label flags and pass `--timeout 30`. `IDLE_SHADOW_BD` becomes `IDLE_SHADOW_TRK`, and the old name is honored with a deprecation line.
   - Before reading, it calls `trk capabilities`; with `ready: false` it writes the no-tracker gap and goes on with the steps that do not need items.
3. **dbi.**
   - `bd_tickets` (246-254) is a bulk SQL read, a different capability from `read` and `ready`. It moves behind an optional `trk list --fields id,state,description`, called with `--timeout 60` and offered only where the adapter has it. On an adapter without it, `dbi` reports the bd-linked checks as not run, with the class.
   - `BD_ID` (51) takes its pattern from configuration.
   - This is a third call site the commission did not name. It may ship in the same PR or the next.

## 6. Configuration

One file, read only by `trk`, at `$DIRECTOR_STATE/tracker.json`. Environment variables override it:

| key | env | default | meaning |
|---|---|---|---|
| `adapter` | `DIRECTOR_TRACKER` | `bd` | `bd`, `files` or `none` |
| `bd.binary` | `DIRECTOR_TRACKER_BD` | `bd` | the bd executable |
| `files.root` | `DIRECTOR_TRACKER_FILES` | none; required for `files` | the items directory |
| `timeout_s` | `DIRECTOR_TRACKER_TIMEOUT` | 60 | per call, used only when the caller passes no `--timeout`; the callers pass today's values (section 4) |
| `label_convention` | `IDLE_SHADOW_LABEL_CONVENTION` (kept) | none | the label convention file |
| `client_label` | | `scope:client` | the label whose items are counted, never read |
| `id_pattern` | | the bd ref grammar | what `dbi` matches as an item id in prose |

**The label path moves into config.** `idle-shadow` stops looking for `.claude/skills/task-workflow/SKILL.md` at the git top level (241-247). That path is one orchestrator's layout, and the lookup also makes the script's answer depend on its cwd. With no `label_convention` set, it writes the existing gap. A deployment that wants today's behavior sets the key to that file. That is a one-line deployment step, done where the adapter is installed, and the cost line in section 0 names it.

**The default adapter is `bd` when nothing is configured,** so every current seat behaves as it does now. The alternative is a default of `none`, which would make a missing configuration read as "no tracker" and would turn every current bd read into a gap on upgrade. Recommended: `bd`. This recommendation is valid until 2026-11-20; the architect re-checks it then.

## 7. Out of scope

- **Writes of any kind.** The adapter reads. Claiming, closing and creating stay where they are.
- **Bead-backed rows and dispatch-as-claim.** Both are held, and this design neither builds nor presumes them.
- **`relay-log --bead`.** A small flag that records an item ref on a relay entry. It is a follow-up PR on top of `trk parse`, named here so the parse grammar is the one it will use.
- **Trackers other than bd and files,** such as GitHub issues or Jira. The surface allows them; none is designed here.

## 8. Test plan

Risk first, then the cases that cover each risk, then the gates. Each case is marked **gate** (structural: is the code correct) or **report** (a reading that informs and never changes an exit code), as the report's test-architect view asks. No case sets a threshold on a count. Every case runs under a scratch `HOME` and a scratch `DIRECTOR_STATE`, with `env -i`, no `MARVEL_SOCKET`, and `gh` and `ssh` replaced by stubs that fail if called.

### 8.1 Risks

| id | risk | probability | impact | score | priority |
|---|---|---|---|---|---|
| R1 | a missing binary or refused server still reads as an answer (a state, or an empty ready list) | 2 | 3 | 6 | P0 |
| R2 | a not_found is reported for what was a refusal, so a live item reads as dangling | 2 | 3 | 6 | P0 |
| R3 | an existing `bd:` row records a false move after the change | 2 | 3 | 6 | P0 |
| R4 | a `scope:client` item reaches a file or variable through `trk ready` | 1 | 3 | 3 | P0 (data handling, scored up) |
| R5 | adapters disagree on the item shape, so a caller works on one and fails on another | 2 | 2 | 4 | P1 |
| R6 | `none` makes `idle-shadow` or `dws` fail where it should report a gap and go on | 2 | 2 | 4 | P1 |
| R7 | the label convention lookup still depends on cwd | 1 | 2 | 2 | P2 |
| R8 | a timeout leaves a child process running | 1 | 2 | 2 | P2 |

### 8.2 The contract suite, run against all three adapters

One table of cases in `verify-trk.sh`. Each row runs once per adapter, with the expected result per adapter in the row. The structure is the test: a case that passes on `files` and is never run on `bd` is a gap the table makes visible.

Every C case is a gate.

| case | given | when | then |
|---|---|---|---|
| C1 | a configured adapter | `trk capabilities` | the JSON names the adapter, and every write capability is false; `none` reports every capability false |
| C2 | a valid `bd:` ref | `trk parse` | the long form `trk:bd:<id>`; same on every adapter, since parse needs no tracker |
| C3 | a bad ref (`trk:Bd:x`, an empty id, an id starting with `-`, `trk:files:..`, `pr:o/r#1`) | `trk parse` | exit 2 on every adapter |
| C4 | an item that exists | `trk read` | the normalized shape: `state` in the five words, `native` kept, times UTC with `Z`. `none`: exit 3 |
| C5 | an item that does not exist | `trk read` | exit 6 on `bd` (fixture from the measured not-found text) and `files`; exit 3 on `none` |
| C6 | a ref naming another adapter | `trk read` | exit 3, naming both, with no exec of either tracker (the stubs record calls) |
| C7 | items with and without `scope:client` | `trk ready --exclude-label scope:client` | no `scope:client` item in the output; `none`: exit 3 |
| C8 | `blocked_by` naming an `open` item, and another naming an `active` item (files) | `trk ready` | neither blocked item is listed; one whose blocker is `done` is |

### 8.3 Failure classes, bd only

Every F case is a gate. F1 against F2 is the report's item 7: a missing binary and a refused server give different errors.

| case | given | then |
|---|---|---|
| F1 | `bd.binary` names a path that does not exist | exit 4, class `missing_binary`, with no fallback to another bd on `PATH` |
| F2 | the stub exits nonzero with the measured refusal text | exit 5, class `refused`, detail is the stub's first stderr line |
| F3 | the stub exits nonzero with text matching neither case | exit 5, never 6 (R2) |
| F4 | the stub prints non-JSON | exit 7 |
| F5 | the stub sleeps past the timeout | exit 8, and `kill -0` on the stub's pid fails afterwards (R8) |
| F6 | `PATH` holds no `timeout` binary | F5 still gives exit 8, never exit 4 |

### 8.4 Callers

D1, D1b, D2, D3 and D5 to D7 are gates. D4's gap text is a gate; the count of items it would have read is a report.

| case | caller | given | then |
|---|---|---|---|
| D1 | `dws refresh` | adapter `bd`; a stored row with `bd:<id>` observed `open` before the change; the bd stub answering `status: open` | no move recorded, and the stored observation keeps `kind: bd` with state `open` (R3) |
| D1b | `dws refresh` | adapter `files`; a stored row with `trk:files:<id>` observed `open`; the files double answering `open` | no move recorded |
| D2 | `dws refresh` | adapter `bd` with a missing binary | the warning carries `missing_binary`, and no observation is written for the link (R1) |
| D3 | `dws refresh` | adapter `none`, a row with a `bd:` link | the warning says not configured, and the row is otherwise refreshed |
| D4 | `idle-shadow` | adapter `none` | gap `items: no tracker (adapter none)`, exit status as for any gap run, and the marry step reports not run (R6) |
| D5 | `idle-shadow` | the files double with one `scope:client` item | the client count is 1, and no file under the run's scratch holds that item's id (R4) |
| D6 | `idle-shadow` | no `label_convention`, run from a checkout that has `.claude/skills/task-workflow/SKILL.md` | the no-convention gap, so cwd no longer decides (R7) |
| D7 | `dbi` | adapter `none` | the bd-linked checks are reported not run, with the class, and the rest of the battery runs |

### 8.5 Gates

- P0 cases (C4 to C7, F1 to F3, D1, D2, D5) pass before merge, structural checks under the fleet's diagnostic-not-gate rule: they ask whether the scripts are correct, not whether a tracker is healthy.
- No case reaches a real bd server, the network or the real director state. The bd fixtures come from one recorded scratch run, with the bd version that produced them written beside them.
- `bash -n` on every changed bash file, and the existing `verify-dws-*.sh` and `verify-idle-shadow.sh` suites pass unchanged, apart from the `idle-shadow` stub once its cases move to the files double. The `dws` bd stub stays until the bd adapter's recorded fixtures replace it (section 3).

### 8.6 For the relay-log follow-up

When `relay-log --bead` is built on `trk parse`: the flag refuses a value with a leading `-` or one that fails `trk parse`, and an entry appended without the flag is byte-identical to today's (the report's item 3). Both are gates.

## 9. Candidate requirements (provisional)

- **TA-1** (provisional; source: this design, the call-site read). Director's seat scripts read work items only through one adapter surface, never by naming a tracker binary.
- **TA-2** (provisional; source: the call-site read, `refresh` 945-949 and `dbi` 246-254). A tracker read that fails reports one of the named classes, and a missing binary, a refusal and an unknown id never share a class.
- **TA-3** (provisional; source: independence). A team with no tracker runs every director script, with gaps that say so.
- **TA-4** (provisional; source: `idle-shadow` 241-247). No script finds its configuration by its working directory.
- **TA-5** (provisional; source: this design, after the research report section 3; in the spirit of `reference/adapters.md:6-10`). An adapter declares the capabilities it supplies, and an undeclared capability reads as UNREAD, never as an empty answer.
