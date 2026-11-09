#!/usr/bin/env bash
# Prove `schedule-ack` (sim/design/schedule-ack.md): the sweep line for a scheduled firing that has no acked result.
# It reads marvel's `describe team` JSON (the Schedules block marvel already returns), the results directory the sweep
# writes, and a route ledger, and prints one line per scheduled role whose last due firing has no succeeded run with
# an acked result by due plus 2 hours. Acceptance, from the ticket:
#   - stale false, cron 0 11 * * 1-5, now Thursday 13:05Z, no succeeded run for Thursday 11:00Z: one line;
#   - the same with a succeeded run, its results file and sha256 line, and a ledger row for that firing whose
#     results_sha256 matches: no line;
#   - a succeeded run with no ledger row, or a row whose results_sha256 does not match: one line;
#   - an unsettled run at 13:05Z: one line.
# Reminds only: it writes nothing, runs nothing and gates nothing. Every run is under `env -i` with a scratch HOME.
# Fixture values are placeholders. Written for bash 3.2 and bash 5.
#
# Usage: skills/director/scripts/verify-schedule-ack.sh   Exit nonzero on any miss.
set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
A="${SCHEDULE_ACK:-$here/schedule-ack}"
example="$here/fixtures/schedule-ack.describe.example.json"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }
[[ -f "$A" ]] || { echo "FAIL schedule-ack exists at $A"; echo "0 passed, 1 failed"; exit 1; }

root="$(mktemp -d "${ACK_TMP:-${TMPDIR:-/tmp}}/ack.XXXXXX")" || { echo "FAIL scratch root"; exit 1; }
root="$(cd "$root" && pwd -P)"
trap 'rm -rf "$root"' EXIT
mkdir -p "$root/home" "$root/results"
PYDIR="$(dirname "$(command -v python3)")"

NOW="2030-01-03T13:05:00Z"          # a Thursday
FIRING="20300103T110000Z"           # its 11:00Z due time
SHA="$(printf 'placeholder results\n' | shasum -a 256 | cut -d' ' -f1)"
OTHER="$(printf 'other\n' | shasum -a 256 | cut -d' ' -f1)"

run() { # run <args...>
  env -i HOME="$root/home" PATH="$PYDIR:/usr/bin:/bin" python3 "$A" "$@" >"$root/out" 2>"$root/err"
  rc=$?
}
# describe <file> <firing> <outcome|none> [stale] [role]: a describe team document with one scheduled role.
describe() {
  python3 - "$1" "$2" "$3" "${4:-false}" "${5:-placeholder-sweep}" <<'PY'
import json, sys
path, firing, outcome, stale, role = sys.argv[1:6]
runs = []
if outcome != "none":
    due = firing[:4] + "-" + firing[4:6] + "-" + firing[6:8] + "T" + firing[9:11] + ":" + firing[11:13] + ":" + firing[13:15] + "Z"
    runs.append({"session": "placeholder-session", "firing": firing, "due_at": due, "started_at": due, "ended_at": due,
                 "outcome": outcome, "tokens": {"prompt": 0, "out": 0, "metered": False}, "result_truncated": False, "duration": "1m0s"})
doc = {"Name": "placeholder-team", "Schedules": [{
    "role": role, "cron": "0 11 * * 1-5", "timezone": "Etc/UTC", "stale_after": "30h0m0s", "next_due": "2030-01-04T11:00:00Z",
    "firing": firing, "since": "2030-01-01T00:00:00Z", "stale": stale == "true", "runs": runs}]}
json.dump(doc, open(path, "w"))
PY
}
results() { # results <firing> <sha>: the results file and its sha256 line
  printf 'placeholder results\n' >"$root/results/$1.md"
  printf '%s  %s.md\n' "$2" "$1" >"$root/results/$1.md.sha256"
}
ledger() { # ledger <role> <firing> <sha>
  printf '{"scheduled_role":"%s","firing_id":"%s","results_sha256":"%s","acked_at":"2030-01-03T11:30:00Z"}\n' "$1" "$2" "$3" >>"$root/ledger.jsonl"
}
reset() { rm -rf "$root/results" "$root/ledger.jsonl"; mkdir -p "$root/results"; : >"$root/ledger.jsonl"; }
check() { run check --describe "$root/d.json" --results-dir "$root/results" --ledger "$root/ledger.jsonl" --now "${1:-$NOW}" "${@:2}"; }
lines() { grep -c '^scheduled: ' "$root/out" || true; }
oneline() { # oneline <name> <fragment>
  if [[ $rc -eq 0 && "$(lines)" == 1 ]] && grep -qF "$2" "$root/out"; then ok "$1"; else bad "$1" "rc=$rc out=$(head -c 300 "$root/out")"; fi
}
noline() { [[ $rc -eq 0 && "$(lines)" == 0 ]] && ok "$1" || bad "$1" "rc=$rc out=$(head -c 300 "$root/out")"; }

# --- the ticket's acceptance --------------------------------------------------------------------------
reset; describe "$root/d.json" "$FIRING" none
check; oneline "no succeeded run for the 11:00Z firing at 13:05Z yields one line" "scheduled: placeholder-sweep firing $FIRING"
grep -qF 'no succeeded run' "$root/out" && ok "the line says there is no succeeded run" || bad "the line says there is no succeeded run" "$(cat "$root/out")"
grep -qF 'due 2030-01-03T11:00:00Z' "$root/out" && ok "the line gives the due time read from the firing id" || bad "the due time" "$(cat "$root/out")"

reset; describe "$root/d.json" "$FIRING" succeeded; results "$FIRING" "$SHA"; ledger placeholder-sweep "$FIRING" "$SHA"
check; noline "a succeeded run, its results file and sha256 line, and a matching ledger row yield no line"

reset; describe "$root/d.json" "$FIRING" succeeded; results "$FIRING" "$SHA"
check; oneline "a succeeded run with no ledger row yields one line" "no ack in the route ledger"
reset; describe "$root/d.json" "$FIRING" succeeded; results "$FIRING" "$SHA"; ledger placeholder-sweep "$FIRING" "$OTHER"
check; oneline "a ledger row whose results_sha256 does not match yields one line" "does not match"
reset; describe "$root/d.json" "$FIRING" succeeded; results "$FIRING" "$SHA"; ledger placeholder-sweep "20300102T110000Z" "$SHA"
check; oneline "a ledger row for another firing does not ack this one" "no ack in the route ledger"
reset; describe "$root/d.json" "$FIRING" succeeded; results "$FIRING" "$SHA"
printf '{"scheduled_role":"placeholder-sweep","firing_id":"%s","results_sha256":"%s"}\n' "$FIRING" "$SHA" >"$root/ledger.jsonl"
check; oneline "a ledger row with no acked_at is not an ack" "no ack in the route ledger"
reset; describe "$root/d.json" "$FIRING" succeeded; results "$FIRING" "$SHA"; ledger other-role "$FIRING" "$SHA"
check; oneline "a ledger row for another role does not ack this one" "no ack in the route ledger"

reset; describe "$root/d.json" "$FIRING" failed
check; oneline "a failed run for the firing is not a succeeded run: one line" "no succeeded run"
reset; describe "$root/d.json" "$FIRING" cancelled
check; oneline "a cancelled run for the firing is not a succeeded run: one line" "no succeeded run"

# --- the grace period and the other states --------------------------------------------------------------
reset; describe "$root/d.json" "$FIRING" none
check "2030-01-03T12:59:59Z"; noline "before due plus 2h (12:59:59Z) there is no line"
check "2030-01-03T13:00:00Z"; oneline "at due plus 2h exactly (13:00Z) there is one line" "scheduled:"
check "2030-01-03T11:30:00Z"; noline "an hour after the due time there is no line"
reset; describe "$root/d.json" "$FIRING" succeeded; results "$FIRING" "$SHA"
check "2030-01-03T12:00:00Z"; noline "inside the grace period a succeeded but unacked firing is not yet a line"
reset; describe "$root/d.json" "$FIRING" succeeded
ledger placeholder-sweep "$FIRING" "$SHA"
check; oneline "a succeeded run and a ledger row but no results file yields one line" "no results file"
reset; describe "$root/d.json" "$FIRING" succeeded; printf 'placeholder results\n' >"$root/results/$FIRING.md"; ledger placeholder-sweep "$FIRING" "$SHA"
check; oneline "a results file with no sha256 line yields one line" "no sha256 line"
reset; describe "$root/d.json" "$FIRING" succeeded; results "$FIRING" "$SHA"; printf 'not a hash  x.md\n' >"$root/results/$FIRING.md.sha256"; ledger placeholder-sweep "$FIRING" "$SHA"
check; oneline "a sha256 line that is not a hash yields one line" "no sha256 line"
reset; describe "$root/d.json" "$FIRING" none true
check; oneline "a stale schedule still reports the missing ack, and says it is stale" "stale"
reset; describe "$root/d.json" "" none
check; noline "a schedule that has not fired yet has no line"
reset; describe "$root/d.json" "bad-id" none
check; oneline "a firing id that is not a due time reads as unknown, not as a pass" "UNKNOWN"

# --- several roles, filtering, the example file ----------------------------------------------------------
python3 - "$root/two.json" "$FIRING" <<'PY'
import json, sys
f = sys.argv[2]
mk = lambda role, runs: {"role": role, "cron": "0 11 * * 1-5", "timezone": "Etc/UTC", "stale_after": "30h0m0s", "firing": f, "stale": False, "runs": runs}
ok = {"session": "s", "firing": f, "due_at": "2030-01-03T11:00:00Z", "ended_at": "2030-01-03T11:05:00Z", "outcome": "succeeded"}
json.dump({"Schedules": [mk("role-a", []), mk("role-b", [ok])]}, open(sys.argv[1], "w"))
PY
reset; results "$FIRING" "$SHA"; ledger role-b "$FIRING" "$SHA"
run check --describe "$root/two.json" --results-dir "$root/results" --ledger "$root/ledger.jsonl" --now "$NOW"
[[ $rc -eq 0 && "$(lines)" == 1 ]] && grep -qF 'role-a' "$root/out" && ! grep -qF 'role-b' "$root/out" && ok "two roles: only the one with no acked result is named" || bad "two roles" "$(cat "$root/out")"
run check --describe "$root/two.json" --results-dir "$root/results" --ledger "$root/ledger.jsonl" --now "$NOW" --role role-b
noline "--role limits the check to one role"
run check --describe "$example" --results-dir "$root/results" --ledger "$root/ledger.jsonl" --now "2030-01-03T13:05:00Z"
oneline "the checked-in example describe document reads: its current firing has no succeeded run" "firing 20300103T110000Z"
run check --describe "$example" --results-dir "$root/results" --ledger "$root/ledger.jsonl" --now "2030-01-02T13:05:00Z"
[[ $rc -eq 0 ]] && ok "the example, read a day earlier, exits 0" || bad "the example, a day earlier" "rc=$rc"

# --- unreadable input is UNKNOWN, never a pass ------------------------------------------------------------
reset; describe "$root/d.json" "$FIRING" none
run check --describe "$root/missing.json" --results-dir "$root/results" --ledger "$root/ledger.jsonl" --now "$NOW"
[[ $rc -eq 0 ]] && grep -qF 'scheduled: UNKNOWN (describe unreadable)' "$root/out" && ok "a missing describe file prints UNKNOWN (describe unreadable)" || bad "missing describe" "rc=$rc $(cat "$root/out")"
printf 'not json' >"$root/bad.json"
run check --describe "$root/bad.json" --results-dir "$root/results" --ledger "$root/ledger.jsonl" --now "$NOW"
grep -qF 'scheduled: UNKNOWN (describe unreadable)' "$root/out" && ok "a describe file that is not JSON prints UNKNOWN" || bad "bad describe" "$(cat "$root/out")"
printf '{"Name":"x"}' >"$root/none.json"
run check --describe "$root/none.json" --results-dir "$root/results" --ledger "$root/ledger.jsonl" --now "$NOW"
[[ $rc -eq 0 && "$(lines)" == 0 ]] && ok "a team with no scheduled role has no line" || bad "no schedules" "$(cat "$root/out")"
reset; describe "$root/d.json" "$FIRING" succeeded; results "$FIRING" "$SHA"
run check --describe "$root/d.json" --results-dir "$root/results" --ledger "$root/nope.jsonl" --now "$NOW"
grep -qF 'scheduled: UNKNOWN (route ledger unreadable)' "$root/out" && ok "a missing route ledger prints UNKNOWN (route ledger unreadable), never a no-ack claim" || bad "missing ledger" "$(cat "$root/out")"
printf '{"scheduled_role":"placeholder-sweep"\nnot json\n' >"$root/ledger.jsonl"
check; grep -qF 'no ack in the route ledger' "$root/out" && ok "a ledger line that is not JSON is skipped, the rest still read" || bad "bad ledger line" "$(cat "$root/out")"
reset; describe "$root/d.json" "$FIRING" succeeded; results "$FIRING" "$SHA"; printf 'garbage\n' >"$root/ledger.jsonl"; ledger placeholder-sweep "$FIRING" "$SHA"
check; noline "a matching row after an unreadable line still acks"
run check --describe "$root/d.json" --results-dir "$root/results" --ledger "$root/ledger.jsonl" --now "soon"
[[ $rc -eq 2 ]] && ok "an unreadable --now is a usage error (exit 2)" || bad "bad now" "rc=$rc"
run; [[ $rc -eq 2 ]] && ok "no subcommand is exit 2" || bad "no subcommand" "rc=$rc"
run check --describe "$root/d.json"; [[ $rc -eq 2 ]] && ok "missing --results-dir or --ledger is exit 2" || bad "missing options" "rc=$rc"

# --- it only reads ------------------------------------------------------------------------------------------
reset; describe "$root/d.json" "$FIRING" succeeded; results "$FIRING" "$SHA"; ledger placeholder-sweep "$FIRING" "$OTHER"
before="$(cd "$root" && find . -type f ! -name out ! -name err | LC_ALL=C sort | xargs shasum | shasum | cut -c1-16)"
check; check
after="$(cd "$root" && find . -type f ! -name out ! -name err | LC_ALL=C sort | xargs shasum | shasum | cut -c1-16)"
[[ "$before" == "$after" ]] && ok "no file under the scratch tree changed" || bad "a file changed"
! grep -q 'placeholder results' "$root/out" && ok "the line never carries the results text" || bad "the line carries results text"
! grep -qE "$SHA|$OTHER" "$root/out" && ok "the line never carries a hash value" || bad "the line carries a hash"

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
