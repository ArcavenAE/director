# diag: the operational diagnostics battery

`diag run` runs the checks behind a batch gate and writes a report director
reads before calling the gate. Design: `sim/design/ops-diagnostics-battery.md`
(U3). This directory holds build item 1 of that design: the runner, gate 0,
the report format and the scratch-daemon helper. The checks for gates A to D
are separate tickets and land as files under `checks/<gate>/`.

```sh
scripts/diag/diag run                     # gate 0 (offline cases), then every registered gate
scripts/diag/diag run --gate A            # gate 0, then gate A
scripts/diag/diag run --live              # also run the live cases (read-only connects and calls)
scripts/diag/diag run --live-only         # the live cases only
scripts/diag/diag run --gate A --accept A3b.negative
scripts/diag/verify-diag.sh               # the runner's own proof; no live fleet
```

## What it promises

- **A diagnostic, not a gate.** A run that started always exits 0, whatever the
  cases found. Nothing here is wired into CI, branch protection or a hook. A bad
  command line exits 2 and starts no run. Calling a gate stays director's act.
- **Every check is a pair.** A positive case (the intended behavior works) and a
  negative case (the forbidden behavior is refused). A negative that passes
  beside a positive that did not is reported INSTRUMENT, never PASS: a refusal
  proves nothing when the instrument refuses everything.
- **INSTRUMENT is not FAIL.** FAIL says the subject did the wrong thing.
  INSTRUMENT says the check could not run, ran out of time, or exited with a
  code that is not a case code.
- **The expectation is registered before the run.** Each case header carries
  `expect=`; the table shows expected beside observed and marks a surprise.
- **Gate 0 runs first, every time**, even for `--gate A`. If any gate 0 case
  fails or is an instrument fault, every later case reports INSTRUMENT without
  running.
- **A gate with no checks says so** ("no checks registered; this is not a
  pass"). An empty gate never reads gate-ready.
- **gate-ready** means every case of the gate is PASS, or each non-PASS case is
  a FAIL registered as `expect=fail` and named by `--accept ID.CASE`. The gate
  call stays with director.
- **A default run touches nothing live.** A case whose mode is `live` (a
  connect to the broker or hub, a `gh` call) runs only under `--live` or
  `--live-only`, and is SKIP otherwise. A skipped gate 0 case keeps gate 0 from
  reading gate-ready, so a default run never calls a gate on cases it did not
  run.
- **Interventional cases never run.** A case whose mode is `interventional` is
  reported SKIP with a note that it awaits the principal who holds that
  authority.
- **Names, never values.** Each case runs under `env -i` with `PATH HOME LANG
  TMPDIR USER LOGNAME`, every `DIAG_*` name, and any name listed in
  `DIAG_PASS_ENV`. A check reads names only.
- **Errors byte for byte.** A case's stdout and stderr go to one evidence file,
  unfiltered, so a later reader can match the exact error text.

## Case exit codes

| code | observed | meaning |
|---|---|---|
| 0 | pass | the case held |
| 1 | fail | the subject did the wrong thing |
| 2 | skip | a precondition is not met (the change is not installed yet) |
| 3 | instrument | the check could not run |

Any other code, and a case past `DIAG_CASE_TIMEOUT` (default 180 seconds), is
INSTRUMENT.

## The report

One JSON line per case, in `report.jsonl`:

```json
{"run":"20261025T153000Z-4242","host":"<host>","gate":"A","id":"A1","case":"positive",
 "expected":"pass","observed":"pass","result":"PASS",
 "evidence":"evidence/A.A1.positive.txt","subject_sha":"<sha>","ts":"2026-10-25T15:30:04Z"}
```

`report.txt` holds the rendered table and one `gate X:` line per gate. Reports
go to `~/.director/state/diag/<date>/<run>/` (override with `--report-root` or
`DIAG_REPORT_ROOT`). That is operational state and is never committed.
`subject_sha` is this checkout's HEAD unless `DIAG_SUBJECT_SHA` says otherwise.

## Writing a check

A check is `checks/<gate>/<id>.sh`, run as `bash <file> <case>`. Its header:

```sh
# diag-id: A1
# diag-gate: A
# diag-subject: aae-orc-31mlk
# diag-mode: scratch            # scratch, live or interventional
# diag-case: positive expect=pass
# diag-case: negative expect=pass
```

A case line may add `mode=` to override the check's mode (gate 0 does this).
`live` is a read only; it needs `--live` or `--live-only`, runs on its own host
and is skipped when `--host` names another. The positive case of a check runs
first whatever order the header lists them in, so the pair rule never depends on
header order. Source `lib/scratch.sh` for anything that would change state:

| function | does |
|---|---|
| `diag_scratch_new` | makes a short scratch root and points `HOME`, `MARVEL_HOME`, `DIRECTOR_HOME` and the tmux socket into it; clears `MARVEL_SOCKET`, `TMUX` and the seat variables |
| `diag_scratch_daemon_up` | starts a marvel daemon with every path explicit and inside the root |
| `diag_scratch_seat_up NAME` | runs one simulator seat and waits for running |
| `diag_scratch_down` | stops the one pid it recorded, ends the tmux server it named, removes the root |
| `diag_scratch_marvel ARGS` | runs the marvel CLI against that daemon |

The helper never matches a process by pattern and refuses a root it did not
make or one under a real state directory. A missing `marvel` or `simulator`
binary returns 3 (INSTRUMENT). `simulator` builds from marvel's `cmd/simulator`;
set `DIAG_SIMULATOR` or put it on `PATH`.

## Gate 0

`gate0.sh` is the positive control: `daemon` (a scratch daemon answers on its
own socket), `seat` (a simulator seat reaches running), `broker` (the local
broker accepts a connection; `DIAG_BROKER_ADDR`, default `127.0.0.1:4222`),
`hub` (`DIAG_HUB_ADDR`; unset means SKIP, because a hub address is a fact of the
cluster) and `gh` (the rate-limit endpoint answers). `daemon` and `seat` run in a
default run. `broker`, `hub` and `gh` are live and run only under `--live` or
`--live-only`; they connect and read, and publish nothing. A scratch daemon that
does not stop makes its case INSTRUMENT, never PASS.

## Home

The design places the runner at the orchestrator (`tools/diag/`). It is here,
in director, because director has no `tools/` directory and `scripts/` already
holds `director-install` and its verifier. This recommendation is valid until
2026-11-01; the builder re-checks it then. A component may ship its own checks;
no component depends on the battery.
