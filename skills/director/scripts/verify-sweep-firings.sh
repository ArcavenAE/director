#!/usr/bin/env bash
# Prove sweep-firings (aae-orc-ijrm5, H-6) prints one board line for a scheduled
# firing that has no acked result two hours after it was due, and none otherwise:
#   - a due firing with no finished run, a failed or cancelled run, a succeeded run
#     with no results file or no sha256 line, with no route-ledger row, or with a row
#     whose results_sha256 differs from the file's sha256 line, each yields one line;
#   - a succeeded run with its results file and a matching ledger row yields none;
#   - nothing prints before due plus two hours, a weekend reads the last weekday firing,
#     a suspended or frozen schedule is skipped, a list and step cron is read;
#   - every input it cannot read becomes a counted GAP line (describe, ledger, timezone,
#     cron), and the exit status is 0 whether it found nothing or a lot;
#   - it is read-only: it never calls marvel when handed a describe file, and writes
#     nothing under HOME or DIRECTOR_STATE.
# The describe fixture follows a real `marvel describe team` capture of
# 2026-11-06: a top-level Schedules array whose entries carry role, cron, timezone,
# firing, stale, next_due and runs[] (newest first) with due_at, firing and outcome.
# Run conditions: a scratch HOME and DIRECTOR_STATE, no MARVEL_ variables, marvel a
# stub that records calls, no network.
#
# Usage: skills/director/scripts/verify-sweep-firings.sh   Exit nonzero on any miss.
# SWEEP_FIRINGS_BIN points the suite at another build, for a negative control.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SWEEP="${SWEEP_FIRINGS_BIN:-$here/sweep-firings}"
[[ -x "$SWEEP" ]] || { echo "sweep-firings not executable at $SWEEP"; exit 2; }
command -v python3 >/dev/null || { echo "python3 not on PATH"; exit 2; }

pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d "${TMPDIR:-/tmp}/sweep-firings.XXXXXX")"
trap 'rm -rf "$root"' EXIT
export HOME="$root/home"; mkdir -p "$HOME"
export DIRECTOR_STATE="$root/state"; mkdir -p "$DIRECTOR_STATE"
export PYTHONDONTWRITEBYTECODE=1
unset MARVEL_SOCKET SWEEP_FIRINGS_TEAM SWEEP_FIRINGS_RESULTS SWEEP_FIRINGS_LEDGER SWEEP_FIRINGS_DESCRIBE
mkdir -p "$root/bin"
printf '#!/bin/sh\necho "$*" >> "%s/marvel.calls"\nexit 3\n' "$root" > "$root/bin/marvel"
chmod +x "$root/bin/marvel"
export PATH="$root/bin:$PATH"

epoch() { python3 -I -c 'import sys,datetime as d; print(int(d.datetime.fromisoformat(sys.argv[1].replace("Z","+00:00")).timestamp()))' "$1"; }
SHA_A="$(printf 'a' | shasum -a 256 | cut -d' ' -f1)"
SHA_B="$(printf 'b' | shasum -a 256 | cut -d' ' -f1)"

# Thursday 2026-11-05 and the Friday after it.
THU_1305="$(epoch 2026-11-05T13:05:00Z)"
THU_1255="$(epoch 2026-11-05T12:55:00Z)"
SAT_1305="$(epoch 2026-11-07T13:05:00Z)"

# describe NAME CRON TZ RUNS_JSON [EXTRA_JSON]: write $root/NAME.json with one schedule.
describe() {
  python3 -I - "$root/$1.json" "$2" "$3" "$4" "${5:-{\}}" <<'PY'
import json, sys
path, cron, tz, runs, extra = sys.argv[1:6]
s = {"role": "worker", "cron": cron, "timezone": tz, "stale": False,
     "stale_after": "80h0m0s", "firing": "20261105T110000Z",
     "next_due": "2026-11-06T11:00:00Z", "runs": json.loads(runs)}
s.update(json.loads(extra))
json.dump({"Name": "example", "Schedules": [s]}, open(path, "w"))
PY
}
run() { # FIRING DUE OUTCOME
  printf '{"due_at":"%s","firing":"%s","outcome":"%s","session":"ops/example-worker-1"}' "$2" "$1" "$3"
}
THU_RUN="$(run 20261105T110000Z 2026-11-05T11:00:00Z succeeded)"
FRI_RUN="$(run 20261106T110000Z 2026-11-06T11:00:00Z succeeded)"

results="$root/results"; ledger="$root/ledger.jsonl"
mkdir -p "$results"
# results FIRING_SHORT SHA: a results file whose sha256 line holds SHA.
results_file() { printf 'stepsecurity-threat-response OK none\nsha256: %s\nsend: not available to this role\n' "$2" > "$results/$1.md"; }
ledger_row() { # FIRING_ID SHA
  printf '{"scheduled_role":"worker","firing_id":"%s","results_sha256":"%s","acked_at":"2026-11-05T11:30:00Z"}\n' "$1" "$2" >> "$ledger"
}
reset() { rm -f "$results"/*.md "$ledger"; : > "$ledger"; }

# sweep NAME NOW [env...]: run against $root/NAME.json; sets OUT and RC.
OUT=""; RC=0
sweep() {
  local name="$1" now="$2"; shift 2
  RC=0
  OUT="$(env SWEEP_FIRINGS_DESCRIBE="$root/$name.json" SWEEP_FIRINGS_NOW="$now" \
    SWEEP_FIRINGS_RESULTS="$results" SWEEP_FIRINGS_LEDGER="$ledger" "$@" "$SWEEP" 2>"$root/stderr")" || RC=$?
}
lines() { printf '%s\n' "$OUT" | grep -c '^- sweep-firings: ' || true; }
gaps()  { printf '%s\n' "$OUT" | grep -c '^- GAP sweep-firings: ' || true; }
check() { # LABEL EXPECTED_LINES EXPECTED_GAPS
  local n g; n="$(lines)"; g="$(gaps)"
  if [[ "$RC" == 0 && "$n" == "$2" && "$g" == "$3" ]]; then ok "$1"
  else bad "$1" "rc=$RC lines=$n gaps=$g want lines=$2 gaps=$3; output: $OUT"; fi
}

before="$(find "$HOME" "$DIRECTOR_STATE" | LC_ALL=C sort | shasum -a 256)"

# 1. the acceptance row: no succeeded run for Thursday 11:00Z, now Thursday 13:05Z.
describe norun "0 11 * * 1-5" Etc/UTC "[]"
reset; sweep norun "$THU_1305"; check "a due firing with no run at 13:05Z yields one line" 1 0
printf '%s\n' "$OUT" | grep -q 'worker' && printf '%s\n' "$OUT" | grep -q '20261105T110000Z' \
  && ok "the line names the role and the firing" || bad "the line names the role and the firing" "$OUT"

# 2. succeeded, results file, matching ledger row: none.
describe acked "0 11 * * 1-5" Etc/UTC "[$THU_RUN]"
reset; results_file 20261105T1100Z "$SHA_A"; ledger_row 20261105T110000Z "$SHA_A"
sweep acked "$THU_1305"; check "a succeeded run with a matching ledger row yields none" 0 0

# 3. succeeded, no ledger row.
reset; results_file 20261105T1100Z "$SHA_A"
sweep acked "$THU_1305"; check "a succeeded run with no ledger row yields one line" 1 0

# 4. a row whose sha256 differs.
reset; results_file 20261105T1100Z "$SHA_A"; ledger_row 20261105T110000Z "$SHA_B"
sweep acked "$THU_1305"; check "a ledger row with a different results_sha256 yields one line" 1 0

# 4b. a row for another firing does not ack this one.
reset; results_file 20261105T1100Z "$SHA_A"; ledger_row 20261104T110000Z "$SHA_A"
sweep acked "$THU_1305"; check "a ledger row for another firing yields one line" 1 0

# 4c. a row for another scheduled role does not ack this one.
reset; results_file 20261105T1100Z "$SHA_A"
printf '{"scheduled_role":"other","firing_id":"20261105T110000Z","results_sha256":"%s","acked_at":"x"}\n' "$SHA_A" >> "$ledger"
sweep acked "$THU_1305"; check "a ledger row for another scheduled role yields one line" 1 0

# 5. unsettled: only an earlier run is recorded.
describe unsettled "0 11 * * 1-5" Etc/UTC "[$(run 20261104T110000Z 2026-11-04T11:00:00Z succeeded)]"
reset; sweep unsettled "$THU_1305"; check "an unsettled firing at 13:05Z yields one line" 1 0

# 6. failed and cancelled runs.
describe failed "0 11 * * 1-5" Etc/UTC "[$(run 20261105T110000Z 2026-11-05T11:00:00Z failed)]"
reset; sweep failed "$THU_1305"; check "a failed run yields one line" 1 0
describe cancelled "0 11 * * 1-5" Etc/UTC "[$(run 20261105T110000Z 2026-11-05T11:00:00Z cancelled)]"
sweep cancelled "$THU_1305"; check "a cancelled run yields one line" 1 0

# 6b. a failed run is not acked by a results file and a matching row.
reset; results_file 20261105T1100Z "$SHA_A"; ledger_row 20261105T110000Z "$SHA_A"
sweep failed "$THU_1305"; check "a failed run with a results file and a matching row still yields one line" 1 0

# 7. the results file is missing, or holds no sha256 line.
reset; ledger_row 20261105T110000Z "$SHA_A"
sweep acked "$THU_1305"; check "a succeeded run with no results file yields one line" 1 0
printf 'member OK\n' > "$results/20261105T1100Z.md"
sweep acked "$THU_1305"; check "a results file with no sha256 line yields one line" 1 0

# 8. nothing before due plus two hours.
reset; sweep norun "$THU_1255"; check "nothing prints before due plus two hours" 0 0

# 9. a weekend reads Friday's firing.
describe fri "0 11 * * 1-5" Etc/UTC "[$FRI_RUN]"
reset; sweep fri "$SAT_1305"; check "Saturday with Friday's run unacked yields one line" 1 0
results_file 20261106T1100Z "$SHA_A"; ledger_row 20261106T110000Z "$SHA_A"
sweep fri "$SAT_1305"; check "Saturday with Friday's run acked yields none" 0 0

# 10. suspended and frozen schedules are skipped.
describe susp "0 11 * * 1-5" Etc/UTC "[]" '{"suspended": true}'
reset; sweep susp "$THU_1305"; check "a suspended schedule yields none" 0 0
describe frozen "0 11 * * 1-5" Etc/UTC "[]" '{"frozen": true}'
sweep frozen "$THU_1305"; check "a frozen schedule yields none" 0 0

# 11. a list and step cron is read: "30 9,11 * * *" last fires 11:30; at 13:05 two hours have not passed.
describe listcron "30 9,11 * * *" Etc/UTC "[]"
reset; sweep listcron "$THU_1305"; check "a list cron is read: 11:30 is not yet due plus two hours at 13:05" 0 0
sweep listcron "$(epoch 2026-11-05T13:31:00Z)"; check "a list cron is read: 11:30 is due plus two hours at 13:31" 1 0
describe stepcron "*/15 * * * *" Etc/UTC "[]"
sweep stepcron "$(epoch 2026-11-05T13:10:00Z)"; check "a step cron is read: 13:00 plus two hours is not yet" 0 0

# 11b. day-of-month and day-of-week both set: either one makes the day due (cron's rule).
describe either "0 11 13 * 4" Etc/UTC "[]"
reset; sweep either "$THU_1305"
printf '%s\n' "$OUT" | grep -q 'firing 20261105T110000Z' \
  && ok "a cron with day-of-month and day-of-week reads Thursday 11-05 as due" || bad "a cron with day-of-month and day-of-week reads Thursday 11-05 as due" "$OUT"

# 12. what it cannot read becomes a GAP, exit 0.
describe othertz "0 11 * * 1-5" America/Chicago "[]"
sweep othertz "$THU_1305"; check "a timezone other than UTC is a gap, not a guess" 0 1
describe badcron "0 11 * * MON" Etc/UTC "[]"
sweep badcron "$THU_1305"; check "a cron it cannot read is a gap" 0 1
printf 'not json' > "$root/junk.json"
sweep junk "$THU_1305"; check "a describe that is not JSON is a gap" 0 1
printf '%s\n' "$OUT" | grep -q 'not run' && ok "an unreadable describe says it was not run" || bad "an unreadable describe says it was not run" "$OUT"
sweep missing "$THU_1305"; check "a missing describe file is a gap" 0 1
printf '{"Name":"example"}' > "$root/noscheds.json"
sweep noscheds "$THU_1305"; check "a describe with no Schedules field is a gap" 0 1

# 13. the ledger has no home yet: unreadable means a gap and no ack line, while a missing run still prints.
reset; results_file 20261105T1100Z "$SHA_A"
sweep acked "$THU_1305" SWEEP_FIRINGS_LEDGER="$root/no-such-ledger"; check "a missing ledger is a gap and does not read as unacked" 0 1
sweep norun "$THU_1305" SWEEP_FIRINGS_LEDGER="$root/no-such-ledger"; check "a missing ledger still lets a missing run print" 1 1
printf 'not json\n' > "$root/badledger.jsonl"
sweep acked "$THU_1305" SWEEP_FIRINGS_LEDGER="$root/badledger.jsonl"; check "a ledger line that is not JSON is a gap" 0 1

# 14. a summary line is always printed first, and the exit status is 0 on a miss.
reset; sweep norun "$THU_1305"
printf '%s\n' "$OUT" | head -1 | grep -q '^- sweep-firings [0-9T:Z-]*: 1 waiting, 0 gap(s)' \
  && ok "the first line is the summary with its counts" || bad "the first line is the summary with its counts" "$OUT"

# 15. read-only: no marvel call with a describe file, nothing written under HOME or DIRECTOR_STATE.
[[ ! -e "$root/marvel.calls" ]] && ok "marvel is never called when a describe file is given" || bad "marvel is never called when a describe file is given" "$(cat "$root/marvel.calls")"
after="$(find "$HOME" "$DIRECTOR_STATE" | LC_ALL=C sort | shasum -a 256)"
[[ "$before" == "$after" ]] && ok "nothing is written under HOME or DIRECTOR_STATE" || bad "nothing is written under HOME or DIRECTOR_STATE"

# 16. with no describe file it asks marvel for the team, and a refusal is a gap; no team is a gap too.
RC=0; OUT="$(env -u SWEEP_FIRINGS_DESCRIBE SWEEP_FIRINGS_NOW="$THU_1305" "$SWEEP" 2>/dev/null)" || RC=$?
check "no describe file and no team is a gap and exit 0" 0 1
[[ ! -e "$root/marvel.calls" ]] && ok "no team means marvel is not called" || bad "no team means marvel is not called"
RC=0; OUT="$(env -u SWEEP_FIRINGS_DESCRIBE SWEEP_FIRINGS_TEAM=ops/example SWEEP_FIRINGS_NOW="$THU_1305" SWEEP_FIRINGS_RESULTS="$results" SWEEP_FIRINGS_LEDGER="$ledger" "$SWEEP" 2>/dev/null)" || RC=$?
check "marvel failing is a gap and exit 0" 0 1
grep -q 'describe team ops/example' "$root/marvel.calls" 2>/dev/null \
  && ok "the describe is marvel describe team ops/example" || bad "the describe is marvel describe team ops/example" "$(cat "$root/marvel.calls" 2>/dev/null)"

echo "$pass passed, $fail failed"
[[ "$fail" == 0 ]]
