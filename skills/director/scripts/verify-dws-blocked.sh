#!/usr/bin/env bash
# Prove that a blocked value of "none" means not blocked: `dws open` and `dws set` store it as empty,
# so every reader agrees, and `dws migrate-blocked-none` clears the "none" values stored before that,
# one marked set per row, without moving a row's last moved time and without touching anything else.
#
# Usage: skills/director/scripts/verify-dws-blocked.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DWS="${DWS:-$here/dws}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
export DWS_TEST_CLOCK=1 DWS_NOW=2026-10-27T12:00:00Z DWS_NO_RERENDER=1 PYTHONDONTWRITEBYTECODE=1
export DIRECTOR_STATE="$root/st"; mkdir -p "$DIRECTOR_STATE"; L="$DIRECTOR_STATE/workstreams.jsonl"
q() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
blocked_of() { "$DWS" show --json | q "[r['blocked'] for r in d['rows'] if r['slug']=='$1'][0]"; }
moved_of() { "$DWS" show --json | q "[r['last_moved'] for r in d['rows'] if r['slug']=='$1'][0]"; }

# --- the write mapping -------------------------------------------------------------------------------------------
"$DWS" open a1 --title "A" --stage building --blocked none >/dev/null
"$DWS" open a2 --title "B" --stage building --blocked " NONE " >/dev/null
"$DWS" open a3 --title "C" --stage building --blocked seat >/dev/null
"$DWS" open a4 --title "D" --stage building --blocked "nonesuch" >/dev/null
[[ "$(blocked_of a1)" == "" && "$(blocked_of a2)" == "" ]] && ok "open stores blocked none, in any case and with spaces, as empty" || bad "open mapping" "$(blocked_of a1)|$(blocked_of a2)"
[[ "$(blocked_of a3)" == "seat" && "$(blocked_of a4)" == "nonesuch" ]] && ok "other blocked values are stored as they were" || bad "other values"
! grep -q '"blocked":"none"' "$L" && ! grep -qi '"blocked":" none' "$L" && ok "no ledger line holds a blocked none" || bad "none written" "$(grep -i none "$L")"
"$DWS" set a3 --blocked None >/dev/null
[[ "$(blocked_of a3)" == "" ]] && ok "set --blocked None clears the block" || bad "set mapping" "$(blocked_of a3)"
"$DWS" set a4 --blocked none --next "unblocked" >/dev/null
[[ "$(blocked_of a4)" == "" ]] && ok "set --blocked none with another field clears the block" || bad "set with next"
"$DWS" board-state >/dev/null 2>&1 || true
printf '# Board\n' >"$DIRECTOR_STATE/board.md"; "$DWS" board-state >/dev/null 2>&1
! grep -q 'blocked on none' "$DIRECTOR_STATE/board.md" && ok "the board block prints no blocked on none" || bad "board prints none"

# --- the migration -------------------------------------------------------------------------------------------------
rm -f "$L"
w() { printf '%s\n' "$1" >>"$L"; }
w '{"ts":"2026-10-01T09:00:00Z","actor":"director","tag":"judgment","event":"open","ws":"n1","title":"N1","stage":"building","blocked":"none"}'
w '{"ts":"2026-10-02T09:00:00Z","actor":"director","tag":"judgment","event":"open","ws":"n2","title":"N2","stage":"idea","blocked":"None"}'
w '{"ts":"2026-10-03T09:00:00Z","actor":"director","tag":"judgment","event":"open","ws":"s1","title":"S1","stage":"building","blocked":"seat"}'
w '{"ts":"2026-10-04T09:00:00Z","actor":"director","tag":"judgment","event":"open","ws":"e1","title":"E1","stage":"building"}'
w '{"ts":"2026-10-05T09:00:00Z","actor":"director","tag":"judgment","event":"open","ws":"x1","title":"X1","stage":"building","blocked":"operator"}'
printf '{"torn' >>"$L"; printf '\n' >>"$L"
cp "$L" "$root/before"; "$DWS" show --json >"$root/show-before" 2>/dev/null
"$DWS" migrate-blocked-none --dry-run >"$root/o" 2>&1 && cmp -s "$L" "$root/before" && ok "--dry-run writes nothing" || bad "dry run wrote" "$(cat "$root/o")"
grep -q 'would clear blocked none on 2 row' "$root/o" && grep -q '^  n1$' "$root/o" && grep -q '^  n2$' "$root/o" && ok "--dry-run names the two rows" || bad "dry run names" "$(cat "$root/o")"
"$DWS" migrate-blocked-none >"$root/o" 2>&1 && grep -q 'cleared blocked none on 2 row' "$root/o" && ok "migrate-blocked-none clears two rows" || bad "migrate" "$(cat "$root/o")"
[[ "$(blocked_of n1)" == "" && "$(blocked_of n2)" == "" ]] && ok "both rows read not blocked" || bad "cleared"
[[ "$(blocked_of s1)" == "seat" && "$(blocked_of x1)" == "operator" && "$(blocked_of e1)" == "" ]] && ok "a seat block, an operator block and an empty one are untouched" || bad "others untouched"
[[ "$(moved_of n1)" == "2026-10-01T09:00:00Z" && "$(moved_of n2)" == "2026-10-02T09:00:00Z" ]] && ok "the migration does not move last moved" || bad "last moved moved" "$(moved_of n1) $(moved_of n2)"
[[ "$(wc -l <"$L" | tr -d ' ')" == "$(( $(wc -l <"$root/before" | tr -d ' ') + 2 ))" ]] && head -n "$(wc -l <"$root/before" | tr -d ' ')" "$L" | cmp -s - "$root/before" && ok "it appended two lines and left every earlier byte, the torn line included" || bad "append only"
tail -n 2 "$L" | grep -c '"migrate":"blocked-none"' | grep -qx 2 && ok "each appended line carries the migrate marker" || bad "marker" "$(tail -n 2 "$L")"
"$DWS" migrate-blocked-none >"$root/o" 2>&1; cp "$L" "$root/after1"
grep -q 'cleared blocked none on 0 row' "$root/o" && ok "a second run clears none" || bad "second run" "$(cat "$root/o")"
"$DWS" migrate-blocked-none >/dev/null 2>&1; cmp -s "$L" "$root/after1" && ok "and appends nothing" || bad "second run appended"
"$DWS" show --json 2>/dev/null | q "sorted(r['slug'] for r in d['rows'])" | grep -q "\['e1', 'n1', 'n2', 's1', 'x1'\]" && ok "the rows are the same five" || bad "rows changed"

echo "dws blocked: $pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
