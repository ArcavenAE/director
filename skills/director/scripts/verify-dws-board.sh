#!/usr/bin/env bash
# Prove `dws board-state` and `dws board-check` (K19, sim/design/director-functions-redesign.md,
# section 4, "Board"): the board gets a generated state section first, between
# <!-- state:begin --> and <!-- state:end -->, rewritten from the stores; everything outside the
# markers is left byte for byte; a malformed marker pair is refused and nothing is written.
# board-check lists ask: and ws: ids in the prose that resolve to no record, and exits 0 (a
# diagnostic, not a gate); an id whose store is UNREAD is "not checked", never "unresolved".
#
# Usage: skills/director/scripts/verify-dws-board.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DWS="${DWS:-$here/dws}"
SNAP="${SNAP_BIN:-$here/desk-snapshot}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
export DWS_TEST_CLOCK=1 DWS_NOW=2026-10-20T12:00:00Z DWS_NO_RERENDER=1 PYTHONDONTWRITEBYTECODE=1
fresh() { export DIRECTOR_STATE="$root/st$1"; rm -rf "$DIRECTOR_STATE"; mkdir -p "$DIRECTOR_STATE"; BOARD="$DIRECTOR_STATE/board.md"; }
q() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
B='<!-- state:begin -->'; E='<!-- state:end -->'

authored() { printf '%s\n' '# Board' '' 'Narrative the director wrote.' 'See ask:c1 and ask:ghost, and ws:alpha and ws:nowhere.' '' '## Uncaptured' '- a free judgment line'; }
store() {  # a fresh Desk snapshot, then a ledger with two rows (one blocked on the operator's card c1)
  printf '{"c1":{"status":"open"},"c2":{"status":"open"},"c3":{"status":"closed"}}' >"$root/cards.json"
  "$SNAP" write --cards "$root/cards.json" --reported 3 >/dev/null
  "$DWS" open alpha --title "alpha" --owner director --stage building --blocked operator --ask c1 >/dev/null
  "$DWS" open beta --title "beta" --owner team-a/builder --stage "in review" >/dev/null
}

# --- no board.md: refused, nothing created -----------------------------------------------
fresh 1; store
if "$DWS" board-state >"$root/o" 2>"$root/e"; then bad "board-state refuses with no board.md"; else
  grep -qi 'board.md' "$root/e" && [[ ! -e "$BOARD" ]] && ok "board-state refuses with no board.md and creates none" || bad "refusal names board.md and creates nothing" "$(cat "$root/e")"; fi

# --- no markers: the block goes at the top, the prose is untouched -------------------------
authored >"$BOARD"; cp "$BOARD" "$root/before"
"$DWS" board-state >"$root/o" 2>"$root/e" && ok "board-state exits 0 on a board with no markers" || bad "board-state exits 0" "$(cat "$root/e")"
[[ "$(head -1 "$BOARD")" == "$B" ]] && ok "the generated block is first" || bad "the generated block is first" "$(head -3 "$BOARD")"
sed "1,/^$E\$/d" "$BOARD" >"$root/rest"
diff <(cat "$root/before") <(sed '1{/^$/d;}' "$root/rest") >/dev/null || diff "$root/before" "$root/rest" >/dev/null && ok "everything after the block is the authored board, byte for byte" || bad "the authored prose is untouched" "$(diff "$root/before" "$root/rest" | head -5)"
for w in "alpha" "beta" "building" "in review" "ask:c1" "2026-10-20T12:00:00Z"; do
  sed -n "/^$B\$/,/^$E\$/p" "$BOARD" | grep -qF -- "$w" && ok "the block shows: $w" || bad "the block shows: $w"
done
sed -n "/^$B\$/,/^$E\$/p" "$BOARD" | grep -Eq 'open Desk cards: 2' && ok "the block counts the open Desk cards (2)" || bad "open Desk cards count" "$(sed -n "/^$B\$/,/^$E\$/p" "$BOARD")"

# --- a second run replaces only the block; idempotent under a fixed clock --------------------
cp "$BOARD" "$root/run1"; "$DWS" board-state >/dev/null 2>&1
cmp -s "$BOARD" "$root/run1" && ok "a second run is byte for byte the same" || bad "a second run is the same"
"$DWS" set beta --next "ship it" >/dev/null
DWS_NOW=2026-10-20T13:00:00Z "$DWS" board-state >/dev/null 2>&1
[[ "$(grep -c "^$B\$" "$BOARD")" == 1 && "$(grep -c "^$E\$" "$BOARD")" == 1 ]] && ok "still exactly one marker pair" || bad "one marker pair"
sed -n "/^$B\$/,/^$E\$/p" "$BOARD" | grep -qF '2026-10-20T13:00:00Z' && ok "the block was rewritten with the new read time" || bad "block rewritten"
sed "1,/^$E\$/d" "$BOARD" | diff - "$root/rest" >/dev/null && ok "text outside the markers is unchanged by a rewrite" || bad "outside text unchanged"

# --- authored text between and around markers placed by the director stays where it is -----------
fresh 2; store
{ echo '# Board'; echo 'intro line'; echo "$B"; echo 'STALE GENERATED TEXT'; echo "$E"; echo 'outro line'; } >"$BOARD"
"$DWS" board-state >/dev/null 2>&1
grep -q 'STALE GENERATED TEXT' "$BOARD" && bad "stale generated text is replaced" || ok "stale generated text between the markers is replaced"
[[ "$(sed -n 1,2p "$BOARD")" == $'# Board\nintro line' && "$(tail -1 "$BOARD")" == 'outro line' ]] && ok "text above and below an existing pair stays where it was" || bad "text around an existing pair" "$(cat "$BOARD")"

# --- malformed markers: refused, nothing written ------------------------------------------------------
for shape in "begin-only" "end-only" "end-before-begin" "two-pairs"; do
  fresh 3; store
  case $shape in
    begin-only) printf '%s\n%s\n' "$B" 'text' >"$BOARD";;
    end-only) printf '%s\n%s\n' 'text' "$E" >"$BOARD";;
    end-before-begin) printf '%s\n%s\n%s\n' "$E" 'x' "$B" >"$BOARD";;
    two-pairs) printf '%s\n%s\n%s\n%s\n%s\n%s\n' "$B" a "$E" "$B" b "$E" >"$BOARD";;
  esac
  cp "$BOARD" "$root/m"
  if "$DWS" board-state >/dev/null 2>"$root/e"; then bad "malformed markers ($shape) are refused"; else cmp -s "$BOARD" "$root/m" && ok "malformed markers ($shape) are refused and the board is untouched" || bad "board changed on refusal ($shape)"; fi
done

# --- review 5586620341: a board with CRLF line endings ---------------------------------------------------------
fresh 6; store
printf '# Board\r\nintro\r\n%s\r\nSTALE\r\n%s\r\nSee ask:ghostcr here.\r\n' "$B" "$E" >"$BOARD"
"$DWS" board-state >/dev/null 2>"$root/e" && ok "board-state accepts CRLF markers" || bad "CRLF markers accepted" "$(cat "$root/e")"
grep -q 'STALE' "$BOARD" && bad "the stale block is replaced on a CRLF board" || ok "the stale block is replaced on a CRLF board"
[[ "$(grep -c "^$B" "$BOARD")" == 1 && "$(grep -c "^$E" "$BOARD")" == 1 && "$(grep -c "^$B"$'\r'"\$" "$BOARD")" == 1 && "$(grep -c "^$E"$'\r'"\$" "$BOARD")" == 1 ]] && ok "one marker pair counting every marker line, and it is CRLF" || bad "one CRLF marker pair" "$(cat -A "$BOARD" | head -12)"
[[ "$(sed -n 1,2p "$BOARD" | tr -d '\r')" == $'# Board\nintro' && "$(tail -1 "$BOARD" | tr -d '\r')" == 'See ask:ghostcr here.' ]] && ok "text outside the markers is unchanged" || bad "CRLF text outside unchanged"
! LC_ALL=C grep -v $'\r$' "$BOARD" | grep -q . && ok "every line of the board still ends in CRLF, the block included" || bad "all lines CRLF" "$(LC_ALL=C grep -vc $'\r$' "$BOARD") line(s) lack CR"
"$DWS" board-check --json >"$root/j" 2>/dev/null
[[ "$(q "[x['token'] for x in d['unresolved']]" <"$root/j")" == "['ask:ghostcr']" ]] && ok "board-check on a CRLF board reads the prose, not the block" || bad "CRLF board-check" "$(cat "$root/j")"

# --- UNREAD sources are said, never read as empty ----------------------------------------------------------
fresh 4; authored >"$BOARD"
"$DWS" board-state >/dev/null 2>&1 && ok "board-state writes with every store missing" || bad "board-state with no stores"
sed -n "/^$B\$/,/^$E\$/p" "$BOARD" | grep -qi 'ledger.*UNREAD' && sed -n "/^$B\$/,/^$E\$/p" "$BOARD" | grep -qi 'desk.*UNREAD' && ok "a missing ledger and Desk snapshot read UNREAD in the block" || bad "UNREAD in the block" "$(sed -n "/^$B\$/,/^$E\$/p" "$BOARD")"
sed -n "/^$B\$/,/^$E\$/p" "$BOARD" | grep -qi 'open Desk cards: UNREAD' && ok "the open-card count is UNREAD, not 0" || bad "count is UNREAD not 0"

# --- board-check ------------------------------------------------------------------------------------------------
fresh 5; store; authored >"$BOARD"; "$DWS" board-state >/dev/null 2>&1
"$DWS" board-check >"$root/o" 2>"$root/e" && ok "board-check exits 0 with unresolved ids (a diagnostic)" || bad "board-check exits 0" "$(cat "$root/e")"
grep -q 'ask:ghost' "$root/o" && grep -q 'ws:nowhere' "$root/o" && ok "it lists the ask and the workstream that resolve to nothing" || bad "unresolved listed" "$(cat "$root/o")"
! grep -q 'ask:c1\b.*unresolved' "$root/o" && ! grep -q 'ws:alpha.*unresolved' "$root/o" && ok "a resolving ask and workstream are not listed" || bad "resolving ids not listed" "$(cat "$root/o")"
"$DWS" board-check --json >"$root/j" 2>/dev/null
[[ "$(q "sorted(x['token'] for x in d['unresolved'])" <"$root/j")" == "['ask:ghost', 'ws:nowhere']" ]] && ok "board-check --json lists exactly those two" || bad "json unresolved" "$(cat "$root/j")"
sed -n "/^$B\$/,/^$E\$/p" "$BOARD" >/dev/null
printf 'inside block ask:notchecked\n' >"$root/x"; { echo "$B"; cat "$root/x"; echo "$E"; echo 'ask:ghost2'; } >"$BOARD"
"$DWS" board-check --json >"$root/j" 2>/dev/null
[[ "$(q "[x['token'] for x in d['unresolved']]" <"$root/j")" == "['ask:ghost2']" ]] && ok "ids inside the generated block are not checked, only the prose" || bad "block excluded" "$(cat "$root/j")"
rm -f "$DIRECTOR_STATE/desk-snapshot.json"; authored >"$BOARD"
"$DWS" board-check --json >"$root/j" 2>/dev/null && ok "board-check exits 0 with the Desk UNREAD" || bad "exit 0 with Desk unread"
[[ "$(q "d['sources']['desk']['state']" <"$root/j")" == "UNREAD" && "$(q "[x['token'] for x in d['unresolved'] if x['token'].startswith('ask:')]" <"$root/j")" == "[]" && "$(q "sorted(x['token'] for x in d['not_checked'])" <"$root/j")" == "['ask:c1', 'ask:ghost']" ]] && ok "with the Desk UNREAD the ask ids are not checked, not unresolved" || bad "UNREAD Desk: not checked" "$(cat "$root/j")"
rm -f "$BOARD"
if "$DWS" board-check >/dev/null 2>"$root/e"; then bad "board-check refuses with no board.md"; else ok "board-check refuses with no board.md"; fi

echo "dws board: $pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
