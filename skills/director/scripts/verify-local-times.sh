#!/usr/bin/env bash
# Prove that every time director shows the operator is local (America/Chicago, CST or CDT) while
# stored and bus values stay UTC:
#   - standing-hook's first line is the local clock, "now HH:MM CST|CDT", before the drain age;
#   - board-html's "Generated" stamp and dws digest's "generated" and "read" stamps render local;
#   - the zone is the named one from the tz database, never the host clock's: a hostile TZ changes nothing;
#   - the ledger and the heartbeat keep their UTC stamps;
#   - DIRECTOR_TZ is a test-only override: honored only with DWS_TEST_CLOCK=1;
#   - skills/director/SKILL.md says it under Relaying.
# CST is 2026-11-12T19:44:00Z = 13:44 CST; CDT is 2026-07-04T19:44:00Z = 14:44 CDT.
#
# Usage: skills/director/scripts/verify-local-times.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
H="${STANDING_HOOK_BIN:-$here/standing-hook}"
BOARD_HTML="${BOARD_HTML:-$here/board-html}"
DWS="${DWS:-$here/dws}"
SKILL="$here/../SKILL.md"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
export DIRECTOR_PAGE_NAMESPACE_TEST=1 GH=/usr/bin/false PYTHONDONTWRITEBYTECODE=1 DWS_NO_RERENDER=1
export DIRECTOR_STATE="$root/state"; mkdir -p "$DIRECTOR_STATE"
export HOME="$root/home"; mkdir -p "$HOME"
export TZ=Asia/Tokyo   # a host clock that is neither CST nor CDT: the named zone must win

CST=2026-11-12T19:44:00Z; CDT=2026-07-04T19:44:00Z

# --- the standing hook ---------------------------------------------------------------------------
for c in "$CST|now 13:44 CST" "$CDT|now 14:44 CDT"; do
  at="${c%%|*}"; want="${c#*|}"
  first="$(DWS_TEST_CLOCK=1 DWS_NOW="$at" "$H" | sed -n 1p)"
  [[ "$first" == "$want" ]] && ok "standing-hook's first line at $at is '$want'" || bad "standing-hook first line at $at" "$first"
done
second="$(DWS_TEST_CLOCK=1 DWS_NOW="$CST" "$H" | sed -n 2p)"
[[ "$second" == "drain age:"* ]] && ok "the drain age follows the clock line" || bad "the drain age follows the clock line" "$second"
"$H" beat
grep -Eq '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:]{8}Z$' "$DIRECTOR_STATE/last-drain" && ok "the heartbeat file stays UTC" || bad "the heartbeat file stays UTC" "$(cat "$DIRECTOR_STATE/last-drain")"
real="$(env -u DWS_TEST_CLOCK -u DWS_NOW "$H" | sed -n 1p)"
[[ "$real" =~ ^now\ [0-9]{2}:[0-9]{2}\ (CST|CDT)$ ]] && ok "the real clock line is Chicago time" || bad "the real clock line is Chicago time" "$real"

# --- the test-only override ------------------------------------------------------------------------
first="$(DWS_TEST_CLOCK=1 DWS_NOW="$CST" DIRECTOR_TZ=America/New_York "$H" | sed -n 1p)"
[[ "$first" == "now 14:44 EST" ]] && ok "DIRECTOR_TZ moves the zone under DWS_TEST_CLOCK=1" || bad "DIRECTOR_TZ under the test clock" "$first"
first="$(env -u DWS_TEST_CLOCK -u DWS_NOW DIRECTOR_TZ=Asia/Tokyo "$H" | sed -n 1p)"
[[ "$first" =~ \ (CST|CDT)$ ]] && ok "DIRECTOR_TZ is ignored without DWS_TEST_CLOCK=1" || bad "DIRECTOR_TZ ignored outside tests" "$first"

# --- board-html ------------------------------------------------------------------------------------
printf '# director board\n\n## Uncaptured\n' >"$DIRECTOR_STATE/board.md"
for c in "$CST|2026-11-12 13:44 CST" "$CDT|2026-07-04 14:44 CDT"; do
  at="${c%%|*}"; want="${c#*|}"
  DWS_TEST_CLOCK=1 DWS_NOW="$at" "$BOARD_HTML" >/dev/null 2>&1 || bad "board-html runs at $at"
  grep -q "Generated $want from " "$DIRECTOR_STATE/board.html" && ok "board-html says 'Generated $want'" || bad "board-html generated stamp at $at" "$(grep -o 'Generated [^<]*' "$DIRECTOR_STATE/board.html" | head -1)"
done

# --- dws digest ------------------------------------------------------------------------------------
export DWS_TEST_CLOCK=1
for c in "$CST|2026-11-12 13:44 CST" "$CDT|2026-07-04 14:44 CDT"; do
  at="${c%%|*}"; want="${c#*|}"
  rm -f "$DIRECTOR_STATE/workstreams.jsonl"
  DWS_NOW="$at" "$DWS" open lt --title "a row" >/dev/null
  DWS_NOW="$at" "$DWS" digest >"$root/digest.out" 2>"$root/digest.err" || bad "digest runs at $at" "$(cat "$root/digest.err")"
  head -1 "$root/digest.out" | grep -q "^# Digest, generated $want by dws digest" && ok "digest header at $at is local" || bad "digest header at $at" "$(head -1 "$root/digest.out")"
  grep -q "^sources: .*ledger read $want" "$root/digest.out" && ok "digest 'read' stamp at $at is local" || bad "digest read stamp at $at" "$(grep '^sources' "$root/digest.out")"
  grep -q "\"${at}\"" "$DIRECTOR_STATE/workstreams.jsonl" && ! grep -Eq 'CST|CDT' "$DIRECTOR_STATE/workstreams.jsonl" && ok "the ledger keeps UTC at $at" || bad "the ledger keeps UTC at $at" "$(head -c 300 "$DIRECTOR_STATE/workstreams.jsonl")"
  cmp -s "$root/digest.out" "$DIRECTOR_STATE/digest.md" && ok "digest.md equals stdout at $at" || bad "digest.md equals stdout at $at"
done

# --- the rule --------------------------------------------------------------------------------------
sec="$(sed -n '/^## Relaying/,/^## Capture triggers/p' "$SKILL")"
printf '%s\n' "$sec" | grep -q 'America/Chicago' && ok "SKILL.md names America/Chicago under Relaying" || bad "SKILL.md names America/Chicago under Relaying"
printf '%s\n' "$sec" | tr '\n' ' ' | grep -Eiq 'records, logs, ledgers and bus text stay UTC' && ok "SKILL.md keeps records, logs, ledgers and bus text UTC" || bad "SKILL.md keeps stored text UTC"
tr '\n' ' ' <"$SKILL" | grep -Eiq 'times shown here are local' && ok "the sweep shape points at the local-time rule" || bad "the sweep shape points at the local-time rule"

echo "passed $pass, failed $fail"
[[ "$fail" == 0 ]]
