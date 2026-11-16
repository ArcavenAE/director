#!/usr/bin/env bash
# Prove verify-cast-launch.sh's summary counts what it prints (director#649).
# It runs the verifier once and checks, against its own log, that
#   1. the number of lines starting PASS equals the "N passed" in its summary;
#   2. the number of lines starting FAIL equals the "M failed" in its summary;
#   3. its exit status is nonzero when any line starts FAIL.
# A case that calls ok() or bad() inside a subshell, such as the right side of a
# pipeline, prints its line and updates a copy of the counters, so the summary
# misses it. A run takes about three minutes.
# Usage: sim/twin/verify-cast-launch-count.sh     Exit nonzero on any miss.
#        CAST_LAUNCH=<path> sim/twin/verify-cast-launch-count.sh
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
verifier="$here/verify-cast-launch.sh"
[[ -f "$verifier" ]] || { echo "verify-cast-launch.sh not found at $verifier"; exit 2; }

fail=0
ok()  { echo "PASS $1"; }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

scratch="$(mktemp -d "${TMPDIR:-/tmp}/cast-launch-count.XXXXXX")"
trap 'rm -rf "$scratch"' EXIT
log="$scratch/run.log"

rc=0
bash "$verifier" > "$log" 2>&1 || rc=$?

summary="$(grep -E '^[0-9]+ passed, [0-9]+ failed$' "$log" | tail -1 || true)"
if [[ -z "$summary" ]]; then
  bad "the verifier printed a summary" "no 'N passed, M failed' line; exit $rc; last line: $(tail -1 "$log")"
  echo "$fail failed"
  exit 1
fi
counted_pass="${summary%% passed*}"
counted_fail="${summary#*, }"; counted_fail="${counted_fail%% failed*}"
lines_pass="$(grep -c '^PASS' "$log" || true)"
lines_fail="$(grep -c '^FAIL' "$log" || true)"

if [[ "$lines_pass" == "$counted_pass" ]]; then
  ok "PASS lines equal the summary's passed count ($lines_pass)"
else
  bad "PASS lines equal the summary's passed count" "$lines_pass PASS lines, summary says $counted_pass passed"
fi
if [[ "$lines_fail" == "$counted_fail" ]]; then
  ok "FAIL lines equal the summary's failed count ($lines_fail)"
else
  bad "FAIL lines equal the summary's failed count" "$lines_fail FAIL lines, summary says $counted_fail failed"
fi
if (( lines_fail > 0 && rc == 0 )); then
  bad "the verifier exits nonzero when a FAIL line is printed" "$lines_fail FAIL lines and exit 0"
else
  ok "the verifier's exit status agrees with its FAIL lines (exit $rc, $lines_fail FAIL lines)"
fi

if (( fail > 0 )); then
  echo "$fail failed"
  exit 1
fi
echo "all passed"
