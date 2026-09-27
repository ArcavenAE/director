# U3: the operational diagnostics battery (design)

Owner: arcaven-architect-g5-0. Commission: arcaven-supervisor work order,
2026-09-27 (operator GO, batched rollout). Design only. The author filed the
idea (`_kos/ideas/director-operational-diagnostic-battery.md`); this note
designs the runnable battery that becomes the harness for gate A onward.

Status, 2026-09-27: U3-1 (the shape) and U3-2 (the broker must refuse before
a third cluster joins) are RULED, as recorded on the idea by director#117.
U3-3 is open. The BA2 scope for A2 is ruled option A (the token retires into
the session certificate, aae-orc-5yqw3), so A2's option-B negative does not
apply. Moved here from the author's local notes so the design has a git home.

## Why

Each batch gate asks the same question: did the change do what it claimed on
the fleet we actually run, and did it break the thing next to it? Today that
is answered by hand, differently each time, and a check that is never run
leaves the same trace as a check that passed. The battery makes each gate a
command whose output director reads before calling the gate.

## Rules the battery holds itself to

1. **Diagnostic, not a gate.** The runner always exits 0. No check is wired
   into CI, branch protection or a hook (SOUL §8,
   `diagnostic-not-gate.md`). Calling a gate stays director's act, taken
   after reading the report.
2. **Every check is a pair.** A positive case (the intended behavior works)
   and a negative case (the forbidden behavior is refused). A check PASSES
   only when both do. A negative that "passes" beside a failing positive is
   reported as INSTRUMENT, not PASS: a refusal proves nothing when the
   instrument is refusing everything.
3. **The expectation is registered before the run.** Each check file states
   its expected outcome per case. The report shows expected against observed,
   so a surprise cannot be explained away afterwards.
4. **Destructive or interventional cases never touch a live daemon.** They
   run against a scratch marvel daemon (its own socket, state file and tmux
   socket, simulator runtime), a scratch `DIRECTOR_HOME`, or a scratch NATS
   pair. Live checks are read-only. A check that would change another
   principal's state on the live fleet is marked `interventional: proposed`
   and does not run until the principal who holds that authority ratifies
   it.
5. **Names, never values.** Environment and credential checks read variable
   names only and never print a value.
6. **Errors are kept byte for byte.** A failing command's output is stored
   verbatim beside the report, for the stagekeeper patterns database.

## Shape

- **Runner:** `diag run [--gate A|B|C|D|all] [--host kinu|mokuzai]
  [--live-only]`.
- **Checks:** one file per check, `checks/<gate>/<id>.sh`, with a header
  block giving `id`, `gate`, `subject` (the ticket), `mode` (scratch, live,
  or interventional), and the expected result per case.
- **Per-case exit codes:**
  - 0: pass;
  - 1: fail;
  - 2: skip (a precondition is not met, for example the change is not
    installed yet);
  - 3: instrument (the check could not run).
- **Report:** one JSON line per case,
  `{run, host, gate, id, case, expected, observed, result, evidence, subject_sha, ts}`,
  plus a rendered table. Reports go to
  `~/.director/state/diag/<date>/<run>/`, which is operational state and
  never committed.
- **Gate 0, the positive control, runs first every time.** It confirms that:
  - a scratch marvel daemon starts and spawns a simulator seat;
  - the local broker and the hub are reachable;
  - `gh` answers.

  If gate 0 fails, every later case reports INSTRUMENT, never FAIL.
- **Home.** The battery tests the composition (marvel, director, the
  launcher and the bus together), so it lives at the orchestrator:
  `tools/diag/`, beside aq and ax. A component may ship its own check files
  later. No component depends on the battery.

## The checks

### Gate A: credential hygiene and the global tier

| id | subject | mode | positive | negative |
|---|---|---|---|---|
| A1 | aae-orc-31mlk, #418 | scratch, plus a live read | a seat spawned by a scratch daemon started with the denylisted names set reaches running and heartbeats | that seat's environment (names via `ps eww`, pane `show-environment`) holds none of the denylisted names; a live read-only sweep reports any live seat on either host that does |
| A2 | aae-orc-83xu (scope per the BA2 ruling) | scratch | the current session's heartbeat token is accepted | a previous session's token is refused. If option B is ruled: an expired token is refused as `heartbeat.expired` and a renewed token accepted |
| A3 | peer ruling 2026-09-27 | live, and interventional for the replies | each kinu supervisor on the roster (research included) sends a marked probe to `global://mokuzai/supervisor` with `reply_by` and gets a reply | a worker-role seat's `global://` send is refused |
| A3b | R-77, the local block | scratch NATS pair built from the live configs | a supervisor credential publishes `global.mokuzai.supervisor.inbox` | a worker credential's raw publish to `global.*` is refused by the BROKER, not only by the shim |

- **A3's supervisors.** A3 is run BY each supervisor on the battery's
  request, never by the battery holding a supervisor's credential. The
  replies land in the mokuzai supervisor's inbox, so that seat agrees to
  answer marked probes once (the interventional ratification).
- **The two layers in A3b.** A3's negative case proves the SHIM refuses (the
  director shim refuses a send with no global domain, R-86). A3b tests the
  broker.
- **Known result before the first run.** The kinu phase-0 broker
  (`probe/nats-phase-0/nats-server.conf`) has no authorization block, and its
  leaf (`leaf-kinu.conf`) carries `global.*.supervisor.inbox`. Any local
  client that bypasses the shim can therefore publish to another cluster's
  supervisor inbox. So A3b's negative is registered as expected FAIL on kinu
  today. U3-2 is ruled: shim-level refusal is not enough, so closing A3b
  means enforcing the director#4 block on the local broker before a third
  cluster joins. That is a broker change, not a battery fix.

### Gate B: launcher distribution (aae-orc-v0mu6, 6dkwn, e9zo5)

| id | mode | positive | negative |
|---|---|---|---|
| B1 | scratch `DIRECTOR_HOME` and a live read | `director-install --status` prints the installed sha, and it equals the STAMP | a tampered STAMP in scratch is reported as a mismatch |
| B2 | scratch | `verify-cast-launch` passes with `CAST_LAUNCH` pointing at the installed directory | a deliberately broken copy fails verify, and the symlink does not move |
| B3 | scratch daemon | a fresh spawn's log shows the launcher resolved under `~/.director/lib/cast-launch/<sha>` | with the install absent and `DIRECTOR_CAST_LAUNCH` unset, the wrapper fails loudly and names the install command; it never falls back to a checkout |
| B4 | scratch | install A, then B, then `--rollback`: the link points at A; re-install B: the link points at B | `--rollback` with no previous version refuses and leaves the link alone |

B1's live read also runs on mokuzai through the mokuzai supervisor seat once
aae-orc-yu5bv lands.

### Gate C

The subjects are named by the work order; bind each to its ticket id when the
author files it.

| id | subject | mode | positive | negative |
|---|---|---|---|---|
| C1 | instructions size | live read, plus a sampled behavioral check | the measured size of the instruction set a seat loads at spawn is under the declared limit (tokens, counted the way the harness counts) | a fixture over the limit reports over. The spot check: a canary rule that was moved out of the always-loaded file is still applied by a seat whose trigger calls for it, and a control seat without the rules file does not apply it |
| C2 | merge loop | scratch, with a stubbed `gh` | a PR whose mergeable state is CLEAN proceeds | UNKNOWN stops the loop with a message; it does not retry blindly or merge |
| C3 | duplicate-team apply | scratch daemon | a manifest with a unique team applies | a second manifest declaring an existing team is refused, the refusal names both sources, and nothing is reconciled |

C1's spot check is behavioral and costs model turns, so it runs sampled
(n=3 per run) and reports a rate, not a single verdict.

### Gate D: store limits (aae-orc-kjix3)

| id | mode | positive | negative |
|---|---|---|---|
| D1 | scratch NATS with small store limits | normal writes below the limit succeed and read back | a write at the limit is refused with an error naming the store, the limit and the usage; the error reaches the caller as a failed ack (a tool error), never a silent drop |
| D1-live | live read-only | reports each stream's usage against its limit and headroom on both hosts | none (a read has no negative); informs only |

## What director gets per gate

One table: each check and case, expected against observed, and the evidence
path. A line at the end says "gate-ready" only when every case of that gate is
PASS, or when a known FAIL was pre-registered and director has accepted it by
name. The gate call stays director's.

## Build tickets (on the yes; flat, with edges)

1. The runner, gate 0, the report format and the scratch-daemon helper.
2. The gate A checks. Blocked by 1; A2's shape follows the BA2 ruling.
3. The gate B checks. Blocked by 1 and by aae-orc-v0mu6.
4. The gate C checks. Blocked by 1 and by the C tickets.
5. The gate D checks. Blocked by 1 and by aae-orc-kjix3.

## Rulings needed

- **U3-1 (RULED yes, 2026-09-27):** approve the shape: the pair rule,
  INSTRUMENT distinct from FAIL, and scratch-only intervention.
- **U3-2 (RULED: the broker must refuse, 2026-09-27):** is shim-level refusal enough for gate A's worker case, or must
  the broker refuse too (A3b)? The default is that the broker must refuse
  before a third cluster joins; until then A3b is a pre-registered known FAIL
  on kinu.
- **U3-3 (open):** the mokuzai supervisor seat agrees to answer marked A3
  probes.
