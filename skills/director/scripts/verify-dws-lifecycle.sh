#!/usr/bin/env bash
# Prove BL1 of the board lifecycle (sim/design/board-lifecycle.md, sections 5, 6.4 and 8): a row
# declares its last stage (`done_at`) and the id it came from (`from`) when it opens, and both
# can change with `set`; `idea:<repo>/<path>` is a typed link; `planned` is a stage between
# designed and building.
#
# Usage: skills/director/scripts/verify-dws-lifecycle.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DWS="${DWS:-$here/dws}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
export DWS_TEST_CLOCK=1 DWS_NOW=2026-11-05T12:00:00Z DWS_NO_RERENDER=1 PYTHONDONTWRITEBYTECODE=1
fresh() { export DIRECTOR_STATE="$root/st$1"; rm -rf "$DIRECTOR_STATE"; mkdir -p "$DIRECTOR_STATE"; }
row() {  # row <slug> <python expr over the row dict r>
  "$DWS" show --json | python3 -c "import json,sys; d=json.load(sys.stdin); r=[x for x in d['rows'] if x['slug']=='$1'][0]; print($2)"
}
lines() { wc -l <"$DIRECTOR_STATE/workstreams.jsonl" 2>/dev/null | tr -d ' ' || echo 0; }

# --- done_at and from on open ---------------------------------------------------------------
fresh 1
"$DWS" open a --title "a" --done-at deployed --from ask:c1 >/dev/null 2>"$root/e" && ok "open takes --done-at and --from" || bad "open takes --done-at and --from" "$(cat "$root/e")"
[[ "$(row a "r['done_at']")" == deployed ]] && ok "done_at is stored on the row" || bad "done_at is stored on the row"
[[ "$(row a "r['from']")" == ask:c1 ]] && ok "from is stored on the row" || bad "from is stored on the row"
[[ "$(row a "'ask:c1' in r['links']")" == True ]] && ok "from also joins the row's links, so the ask: joins still find it" || bad "from joins links"

"$DWS" open b --title "b" >/dev/null
[[ "$(row b "r['done_at']")" == merged ]] && ok "done_at defaults to merged" || bad "done_at defaults to merged"
[[ "$(row b "repr(r['from'])")" == "''" ]] && ok "from defaults to empty" || bad "from defaults to empty"

# --- refused values write nothing ------------------------------------------------------------
n0="$(lines)"
for v in parked idea shipped ""; do
  if "$DWS" open c --title c --done-at "$v" >/dev/null 2>&1; then bad "open refuses --done-at '$v'"; else ok "open refuses --done-at '$v'"; fi
done
for v in foo ask: idea: idea:nopath idea:/x ask:"a b"; do
  if "$DWS" open c --title c --from "$v" >/dev/null 2>&1; then bad "open refuses --from '$v'"; else ok "open refuses --from '$v'"; fi
done
[[ "$(lines)" == "$n0" ]] && ok "no refused open wrote a line" || bad "no refused open wrote a line"

# --- set ----------------------------------------------------------------------------------------
"$DWS" set b --done-at accepted --from idea:ArcavenAE/director/sim/ideas/x.md >/dev/null 2>"$root/e" && ok "set takes --done-at and --from" || bad "set takes --done-at and --from" "$(cat "$root/e")"
[[ "$(row b "r['done_at']")" == accepted ]] && ok "set moves done_at" || bad "set moves done_at"
[[ "$(row b "r['from']")" == idea:ArcavenAE/director/sim/ideas/x.md ]] && ok "set moves from" || bad "set moves from"
[[ "$(row b "'idea:ArcavenAE/director/sim/ideas/x.md' in r['links']")" == True ]] && ok "set puts the idea: from on the links" || bad "set puts from on links"
if "$DWS" set b --done-at nowhere >/dev/null 2>&1; then bad "set refuses a bad --done-at"; else ok "set refuses a bad --done-at"; fi
if "$DWS" set zz --done-at merged >/dev/null 2>&1; then bad "set refuses a missing row"; else ok "set refuses a missing row"; fi
if "$DWS" --actor refresh set b --done-at merged >/dev/null 2>&1; then bad "refresh may not set done_at"; else ok "refresh may not set done_at (writer table unchanged)"; fi

# --- the idea: link type -----------------------------------------------------------------------
"$DWS" link a idea:ArcavenAE/director/sim/ideas/y.md >/dev/null 2>&1 && ok "link takes idea:<repo>/<path>" || bad "link takes idea:<repo>/<path>"
for v in idea: idea:x idea:/y "idea:a/"; do
  if "$DWS" link a "$v" >/dev/null 2>&1; then bad "link refuses '$v'"; else ok "link refuses '$v'"; fi
done

# --- planned -----------------------------------------------------------------------------------
fresh 2
"$DWS" open d1 --title d1 --stage designed >/dev/null
"$DWS" open d2 --title d2 --stage building >/dev/null
"$DWS" open d3 --title d3 --stage defined >/dev/null
"$DWS" stage d1 planned --from designed >/dev/null 2>"$root/e" && ok "planned is a stage" || bad "planned is a stage" "$(cat "$root/e")"
order="$("$DWS" show --json | python3 -c "import json,sys; print(' '.join(x['slug'] for x in json.load(sys.stdin)['rows']))")"
[[ "$order" == "d3 d1 d2" ]] && ok "planned sorts between designed and building" || bad "planned sorts between designed and building" "$order"
"$DWS" stage d1 building --from planned >/dev/null 2>&1 && ok "a planned row moves on to building" || bad "planned moves to building"

# --- close, park --reason and the fold-out (rulings 1 to 3) ---------------------------------------
fresh 3
"$DWS" open k1 --title k1 --stage merged --done-at merged >/dev/null
"$DWS" open k2 --title k2 --stage building >/dev/null
"$DWS" open k3 --title k3 --stage building >/dev/null
if "$DWS" close k1 --from building >/dev/null 2>"$root/e"; then bad "close refuses a stale --from"; else grep -q 'stale move' "$root/e" && ok "close refuses a stale --from" || bad "stale refusal text" "$(cat "$root/e")"; fi
if "$DWS" close nope --from merged >/dev/null 2>&1; then bad "close refuses a missing row"; else ok "close refuses a missing row"; fi
if "$DWS" --actor refresh close k1 --from merged >/dev/null 2>&1; then bad "refresh may not close"; else ok "refresh may not close (writer table unchanged)"; fi
"$DWS" close k1 --from merged >/dev/null 2>"$root/e" && ok "close takes the row out" || bad "close takes the row out" "$(cat "$root/e")"
grep -c '"event":"close"' "$DIRECTOR_STATE/workstreams.jsonl" | grep -q '^1$' && ok "close is one close event" || bad "close is one close event"
grep '"event":"close"' "$DIRECTOR_STATE/workstreams.jsonl" | grep -q '"ts":"2026-11-05T12:00:00Z"' && ok "the close event carries a ts" || bad "the close event carries a ts"
if "$DWS" close k1 --from merged >/dev/null 2>&1; then bad "close refuses a closed row"; else ok "close refuses a closed row"; fi
"$DWS" park k2 --from building --reason "waiting on a design that was dropped" >/dev/null 2>&1 && ok "park takes --reason" || bad "park takes --reason"
"$DWS" park k3 --from building >/dev/null 2>&1 && ok "park with no reason is not refused" || bad "park with no reason is not refused"
"$DWS" open k4 --title k4 >/dev/null
txt="$("$DWS" show)"
echo "$txt" | awk '/^Done, last 14 days/{exit} {print}' | grep -qE 'k1|k2|k3' && bad "closed and parked rows are out of the table" || ok "closed and parked rows are out of the table"
echo "$txt" | awk '/^Done, last 14 days/{exit} {print}' | grep -q k4 && ok "an open row stays in the table" || bad "an open row stays in the table"
echo "$txt" | sed -n '/^Done, last 14 days/,$p' | grep -q 'k1.*closed.*2026-11-05T12:00:00Z.*merged' && ok "show lists the closed row with its done_at" || bad "show lists the closed row" "$txt"
echo "$txt" | sed -n '/^Done, last 14 days/,$p' | grep -q 'k2.*parked.*waiting on a design that was dropped' && ok "show lists a parked row with its reason" || bad "show lists parked with reason"
echo "$txt" | sed -n '/^Done, last 14 days/,$p' | grep -q 'k3.*parked.*parked from building' && ok "a park with no reason shows the stage it parked from" || bad "no-reason park shows its stage"
"$DWS" show --json | python3 -c "
import json,sys; d=json.load(sys.stdin)
assert [r['slug'] for r in d['rows']]==['k4','k2','k3'], [r['slug'] for r in d['rows']]  # closed rows leave rows; parked stay for consumers
assert sorted(x['slug'] for x in d['done'])==['k1','k2','k3'], d['done']
k=[x for x in d['done'] if x['slug']=='k1'][0]; assert k['kind']=='close' and k['at']=='2026-11-05T12:00:00Z' and k['done_at']=='merged', k
" 2>"$root/e" && ok "show --json: a closed row leaves rows, and done is the fold-out" || bad "show --json split" "$(cat "$root/e")"
out15="$(DWS_NOW=2026-11-20T12:00:01Z "$DWS" show)"; echo "$out15" | grep -qE 'k1|k2|k3' && bad "after 14 days the fold-out drops the row" || ok "after 14 days the fold-out drops the row"
out14b="$(DWS_NOW=2026-11-19T12:00:01Z "$DWS" show)"; echo "$out14b" | grep -qE 'k1|k2|k3' && bad "one second past 14 days the row is gone" || ok "one second past 14 days the row is gone"
out14="$(DWS_NOW=2026-11-19T12:00:00Z "$DWS" show)"; echo "$out14" | grep -q k1 && ok "at exactly 14 days the row is still listed" || bad "at 14 days still listed"
if "$DWS" park k4 --from idea --reason "worker-g9-9 stalled" >/dev/null 2>&1; then bad "park --reason refuses an instance id"; else ok "park --reason refuses an instance id"; fi

# re-open: open on a closed slug only
if "$DWS" open k4 --title again >/dev/null 2>&1; then bad "open refuses an open slug"; else ok "open refuses an open slug"; fi
"$DWS" open k1 --title "k1 again" >/dev/null 2>"$root/e" && ok "open re-opens a closed slug" || bad "open re-opens a closed slug" "$(cat "$root/e")"
"$DWS" show --json | python3 -c "
import json,sys; d=json.load(sys.stdin)
assert 'k1' in [r['slug'] for r in d['rows']] and 'k1' not in [x['slug'] for x in d['done']]" && ok "a re-opened row is back in the table, out of Done" || bad "re-open returns the row"
"$DWS" open k2 --title "k2 again" >/dev/null 2>&1 && ok "a parked slug re-opens by open too, as before" || ok "a parked slug is not re-opened by open (unchanged)"

# waiting write and reconcile read a closed row as finished
fresh 4
"$DWS" open w1 --title w1 --stage building --link pr:o/r#1 >/dev/null
"$DWS" close w1 --from building >/dev/null
fake="$root/bin"; mkdir -p "$fake"; printf '#!/bin/sh\necho "gh called: $*" >>"%s/gh.log"; exit 1\n' "$root" >"$fake/gh"; chmod +x "$fake/gh"
PATH="$fake:$PATH" "$DWS" waiting write >/dev/null 2>&1 || true
[[ ! -e "$root/gh.log" ]] && ok "waiting write does not ask gh about a closed row's PR" || bad "waiting write skips a closed row" "$(cat "$root/gh.log")"

# --- a row parked while blocked on the operator is not blocked on the operator any more ------------
fresh 5
printf '{"c1":{"status":"open"}}' >"$root/cards.json"
"$here/desk-snapshot" write --cards "$root/cards.json" --reported 1 >/dev/null
"$DWS" open bl --title bl --stage building --blocked operator --ask c1 >/dev/null 2>"$root/e" || bad "setup: a row blocked on the operator" "$(cat "$root/e")"
"$DWS" open ob --title ob --stage building --blocked operator --ask c1 >/dev/null 2>&1
"$DWS" park bl --from building --reason "dropped" >/dev/null 2>&1
"$DWS" show --json | python3 -c "
import json,sys; d=json.load(sys.stdin)
blocked=[r['slug'] for r in d['rows'] if r['blocked']=='operator']
assert blocked==['ob'], blocked
bl=[r for r in d['rows'] if r['slug']=='bl'][0]; assert bl['stage']=='parked' and bl['blocked']==''
" 2>"$root/e" && ok "park clears the operator block: only the open row reads as blocked on you in show --json rows" || bad "park clears the operator block" "$(cat "$root/e")"
"$DWS" digest 2>/dev/null | grep -q 'bl (card' && bad "the digest lists a parked row as blocked on the operator" || ok "the digest does not list the parked row as blocked on the operator"
"$DWS" open nb --title nb --stage building >/dev/null; "$DWS" park nb --from building >/dev/null 2>&1
[[ "$(grep -c '"ws":"nb"' "$DIRECTOR_STATE/workstreams.jsonl")" == 2 ]] && ok "park of a row not blocked on the operator writes only the park" || bad "park of an unblocked row writes only the park"

# --- F1: every verb but open refuses a closed row, and names the way back ------------------------------
fresh 6
"$DWS" open cz --title cz --stage building --link pr:o/r#1 >/dev/null
"$DWS" close cz --from building >/dev/null
n0="$(lines)"
for verb in "stage cz merged --from building" "park cz --from building" "set cz --next x" "link cz bd:abc-1" "link cz --remove pr:o/r#1" "set cz --done-at deployed" "set cz --from ask:c9"; do
  if "$DWS" $verb >/dev/null 2>"$root/e"; then bad "'dws $verb' refuses a closed row"
  else grep -q 'dws open' "$root/e" && ok "'dws $verb' refuses a closed row and names dws open" || bad "'dws $verb' names dws open" "$(cat "$root/e")"; fi
done
[[ "$(lines)" == "$n0" ]] && ok "no refused verb appended a line to a closed row" || bad "no refused verb appended a line"
"$DWS" open cz --title "cz again" >/dev/null 2>&1 && "$DWS" set cz --next "back" >/dev/null 2>&1 && ok "after dws open the row takes verbs again" || bad "after dws open the row takes verbs again"

# --- F2: the sweep's Blocked on you read skips parked rows (SKILL.md) ------------------------------------
grep -qE 'whose `blocked` is$' "$here/../SKILL.md" && sed -n '/whose `blocked` is$/,+1p' "$here/../SKILL.md" | grep -q 'stage` is not `parked`' && ok "SKILL.md takes Blocked on you from rows whose stage is not parked" || bad "SKILL.md filters parked rows from Blocked on you"
grep -q 'FLIGHT = ("defined", "designed", "planned"' "$here/dash-writer" && ok "planned is in the dash-writer in-flight stages" || bad "planned in FLIGHT"

echo "dws lifecycle: $pass passed, $fail failed"
[[ $fail == 0 ]]
