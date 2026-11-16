#!/usr/bin/env bash
# Prove verify-cast-launch.sh's summary counts what it prints (director#649).
# A case that calls ok() or bad() inside a subshell, such as the right side of a
# pipeline, prints its line and updates a copy of the counters, so the summary
# misses it. Three runs of the verifier, each about three minutes:
#   A. a clean run. Against its own log: the PASS lines equal the summary's
#      passed count, and the FAIL lines equal its failed count.
#   B. a run where one case fails. The test points CAST_LAUNCH at a copy of this
#      directory whose dismiss-devch helper reports the wrong exit status for the
#      "option 2 reworded" screen, so one devch case, run in a pipeline, fails
#      without any edit to the verifier. The verifier must print a FAIL line for
#      it, count it in the summary, and exit nonzero.
#   C. a run that loses its tally. The copy's helper, for the same screen, points
#      the verifier's tally file at /dev/null (every later count is lost, and
#      grep reads an empty file as zero). The verifier must fail with a message
#      naming the tally and exit nonzero, never print a summary and exit 0.
# Usage: sim/twin/verify-cast-launch-count.sh     Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
verifier="$here/verify-cast-launch.sh"
[[ -f "$verifier" ]] || { echo "verify-cast-launch.sh not found at $verifier"; exit 2; }

fail=0
ok()  { echo "PASS $1"; }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

scratch="$(mktemp -d "${TMPDIR:-/tmp}/cast-launch-count.XXXXXX")"
trap 'rm -rf "$scratch"' EXIT

# run_verifier LOG LAUNCH: runs the verifier, leaves its exit status in vrc.
run_verifier() {
  vrc=0
  if [[ -n "$2" ]]; then
    CAST_LAUNCH="$2" bash "$verifier" > "$1" 2>&1 || vrc=$?
  else
    bash "$verifier" > "$1" 2>&1 || vrc=$?
  fi
}

# summary_of LOG: sets spass and sfail from the 'N passed, M failed' line.
summary_of() {
  local line
  line="$(grep -E '^[0-9]+ passed, [0-9]+ failed$' "$1" | tail -1 || true)"
  spass=""; sfail=""
  [[ -n "$line" ]] || return 1
  spass="${line%% passed*}"
  sfail="${line#*, }"; sfail="${sfail%% failed*}"
}

# --- A. a clean run ------------------------------------------------------------
logA="$scratch/clean.log"
run_verifier "$logA" ""
if summary_of "$logA"; then
  lp="$(grep -c '^PASS' "$logA" || true)"
  lf="$(grep -c '^FAIL' "$logA" || true)"
  if [[ "$lp" == "$spass" ]]; then
    ok "clean run: PASS lines equal the summary's passed count ($lp)"
  else
    bad "clean run: PASS lines equal the summary's passed count" "$lp PASS lines, summary says $spass passed"
  fi
  if [[ "$lf" == "$sfail" ]]; then
    ok "clean run: FAIL lines equal the summary's failed count ($lf)"
  else
    bad "clean run: FAIL lines equal the summary's failed count" "$lf FAIL lines, summary says $sfail failed"
  fi
else
  bad "clean run: the verifier printed a summary" "no 'N passed, M failed' line; exit $vrc; last line: $(tail -1 "$logA")"
fi

# --- B. a run where one case fails ----------------------------------------------
# The copy holds the launcher and helper the verifier tests. Its helper runs the
# real one and, for the screen whose option 2 reads "Quit", reports exit 0 where
# the case expects 2.
twin="$scratch/twin"
mkdir -p "$twin"
cp -R "$here/." "$twin/"
# The stub names the real helper by absolute path, because the verifier copies the
# helper file alone into other directories, and it must still work there.
{
  printf '#!/usr/bin/env bash\n'
  printf '# Test copy: the real helper, run in place of this process for every screen but\n'
  printf '# one. The "option 2 reworded" screen runs it as a child and exits 0.\n'
  printf 'real=%q\n' "$here/dismiss-devch"
  cat <<'STUB'
if [[ -n "${FAKE:-}" ]] && grep -q '2\. Quit' "$FAKE/before" 2>/dev/null; then
  "$real" "$@" || true
  exit 0
fi
exec "$real" "$@"
STUB
} > "$twin/dismiss-devch"
chmod +x "$twin/dismiss-devch"

logB="$scratch/failing.log"
run_verifier "$logB" "$twin/cast-launch.sh"
if grep -q '^FAIL devch: option 2 reworded' "$logB"; then
  ok "failing run: the verifier printed a FAIL line for the case that was made to fail"
else
  bad "failing run: the verifier printed a FAIL line for the case that was made to fail" "no 'FAIL devch: option 2 reworded' line, so the test could not make the case fail; exit $vrc"
fi
if summary_of "$logB"; then
  lf="$(grep -c '^FAIL' "$logB" || true)"
  if [[ "$lf" == "$sfail" ]]; then
    ok "failing run: FAIL lines equal the summary's failed count ($lf)"
  else
    bad "failing run: FAIL lines equal the summary's failed count" "$lf FAIL lines, summary says $sfail failed"
  fi
else
  bad "failing run: the verifier printed a summary" "no 'N passed, M failed' line; exit $vrc; last line: $(tail -1 "$logB")"
fi
lf="$(grep -c '^FAIL' "$logB" || true)"
if (( lf > 0 && vrc == 0 )); then
  bad "failing run: the verifier exits nonzero when it prints a FAIL line" "$lf FAIL lines and exit 0"
else
  ok "failing run: the verifier's exit status agrees with its FAIL lines (exit $vrc, $lf FAIL lines)"
fi

# --- C. a run that loses its tally ------------------------------------------------
# HOME is the harness root in the devch cases, so the stub can reach the tally.
twinC="$scratch/twinC"
mkdir -p "$twinC"
cp -R "$here/." "$twinC/"
{
  printf '#!/usr/bin/env bash\n'
  printf '# Test copy: the real helper, except that the "option 2 reworded" screen also\n'
  printf '# points the verifier tally at /dev/null (HOME is the harness root there).\n'
  printf 'real=%q\n' "$here/dismiss-devch"
  cat <<'STUB'
if [[ -n "${FAKE:-}" ]] && grep -q '2\. Quit' "$FAKE/before" 2>/dev/null; then
  rc=0
  "$real" "$@" || rc=$?
  rm -f "$HOME/tally"
  ln -s /dev/null "$HOME/tally"
  exit "$rc"
fi
exec "$real" "$@"
STUB
} > "$twinC/dismiss-devch"
chmod +x "$twinC/dismiss-devch"

logC="$scratch/lost-tally.log"
run_verifier "$logC" "$twinC/cast-launch.sh"
if (( vrc == 0 )); then
  bad "lost tally: the verifier exits nonzero" "exit 0; last line: $(tail -1 "$logC")"
else
  ok "lost tally: the verifier exits nonzero (exit $vrc)"
fi
if grep -qi 'tally' "$logC"; then
  ok "lost tally: the verifier prints a message naming the tally"
else
  bad "lost tally: the verifier prints a message naming the tally" "no line mentions the tally; last line: $(tail -1 "$logC")"
fi

if (( fail > 0 )); then
  echo "$fail failed"
  exit 1
fi
echo "all passed"
