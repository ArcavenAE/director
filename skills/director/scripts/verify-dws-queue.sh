#!/usr/bin/env bash
# Prove `dws queue` (K12, sim/design/director-functions-redesign.md, sections 4 and 6; Q2 and Q5
# as ruled): the operator's queue is the open Desk cards, joined to ledger rows through their
# ask: links. A card whose linked PRs are all observed draft, merged or closed is hidden (R-171,
# R-198, Q5 c: it comes back only if the card is still open and a PR is not in those states,
# and goes for good when director closes the card). Every other open card is shown, and a card no
# row links is "unclassified, shown" (Q2: no cap, show everything). The two checks that need
# card fields the snapshot does not hold are reported "check not run", never guessed. Read only.
#
# Usage: skills/director/scripts/verify-dws-queue.sh   Exit nonzero on any miss.
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
export DIRECTOR_STATE="$root/state"; mkdir -p "$DIRECTOR_STATE"
LEDGER="$DIRECTOR_STATE/workstreams.jsonl"
q() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
obs() {  # WS LINK STATE: a PR observation as dws refresh records it
  printf '{"ts":"2026-10-20T11:00:00Z","actor":"refresh","tag":"mirror","event":"observe","ws":"%s","link":"%s","seen":{"state":"%s"},"checked_at":"2026-10-20T11:00:00Z"}\n' "$1" "$2" "$3" >>"$LEDGER"
}

# --- nothing read: UNREAD, exit 0, nothing created --------------------------------------------
"$DWS" queue --json >"$root/j0" 2>"$root/e0" && ok "queue exits 0 with no stores" || bad "queue exits 0 with no stores" "$(cat "$root/e0")"
[[ "$(q "d['sources']['desk']['state']" <"$root/j0")" == "UNREAD" && "$(q "d['shown']" <"$root/j0")" == "None" ]] && ok "no Desk snapshot: the queue is UNREAD, not empty" || bad "no snapshot: UNREAD" "$(cat "$root/j0")"
[[ ! -e "$LEDGER" ]] && ok "queue creates no store" || bad "queue creates no store"

# --- fixtures -------------------------------------------------------------------------------------
printf '{"c1":{"status":"open"},"c2":{"status":"open"},"c3":{"status":"open"},"c4":{"status":"open"},"c5":{"status":"open"},"c6":{"status":"open"},"c7":{"status":"open"},"c9":{"status":"closed"}}' >"$root/cards.json"
"$SNAP" write --cards "$root/cards.json" --reported 8 >/dev/null
"$DWS" open alpha --title a --owner director --link ask:c1 'pr:o/r#1' >/dev/null
"$DWS" open beta --title b --owner director --link ask:c2 'pr:o/r#2' >/dev/null
"$DWS" open gamma --title g --owner director --link ask:c3 'pr:o/r#3' >/dev/null
"$DWS" open delta --title d --owner director --link ask:c4 'pr:o/r#4' >/dev/null
"$DWS" open eps --title e --owner director --link ask:c6 'pr:o/r#5' 'pr:o/r#6' >/dev/null
"$DWS" open zeta --title z --owner director --link ask:c7 >/dev/null
"$DWS" open closedrow --title cr --owner director --link ask:c9 >/dev/null
obs alpha 'pr:o/r#1' ready; obs beta 'pr:o/r#2' merged; obs gamma 'pr:o/r#3' draft
obs eps 'pr:o/r#5' merged; obs eps 'pr:o/r#6' ready
before="$(shasum -a 256 "$LEDGER" "$DIRECTOR_STATE/desk-snapshot.json")"
"$DWS" queue --json >"$root/j" 2>"$root/e" && ok "queue exits 0 on fixtures" || bad "queue exits 0" "$(cat "$root/e")"
shown() { q "sorted(x['card'] for x in d['shown'])" <"$root/j"; }
hidden() { q "sorted((x['card'], x['why']['state']) for x in d['hidden'])" <"$root/j"; }
[[ "$(shown)" == "['c1', 'c4', 'c5', 'c6', 'c7']" ]] && ok "shown: a ready PR, an unobserved PR, a card no row links, one ready among merged, a row with no PR" || bad "shown cards" "$(shown)"
[[ "$(hidden)" == "[('c2', 'merged'), ('c3', 'draft')]" ]] && ok "hidden: the merged PR's card and the draft PR's card, each with the PR state" || bad "hidden cards" "$(hidden)"
[[ "$(q "[x['kind'] for x in d['shown'] if x['card']=='c5']" <"$root/j")" == "['unclassified']" ]] && ok "a card no row links is unclassified, shown" || bad "unclassified"
[[ "$(q "[x['note'] for x in d['shown'] if x['card']=='c4']" <"$root/j")" == "['PR state unread']" ]] && ok "a shown card whose PR was never observed says so" || bad "PR state unread" "$(q "d['shown']" <"$root/j")"
[[ "$(q "'c9' in [x['card'] for x in d['shown']+d['hidden']]" <"$root/j")" == "False" ]] && ok "a closed card is not in the queue" || bad "closed card excluded"
[[ "$(q "d['counts']" <"$root/j")" == "{'open': 7, 'shown': 5, 'hidden': 2}" ]] && ok "the counts add up: 7 open, 5 shown, 2 hidden" || bad "counts" "$(q "d['counts']" <"$root/j")"
[[ "$(q "sorted(n['check'] for n in d['not_run'])" <"$root/j")" == "['claims with no source', 'clerical relay', 'grouping by kind']" ]] && ok "the checks the snapshot cannot support are listed as not run" || bad "not run" "$(q "d['not_run']" <"$root/j")"
[[ "$(shasum -a 256 "$LEDGER" "$DIRECTOR_STATE/desk-snapshot.json")" == "$before" ]] && ok "queue writes nothing" || bad "queue writes nothing"
"$DWS" queue >"$root/t" 2>&1
grep -Eq '^shown +5 of 7' "$root/t" && grep -q 'c2' "$root/t" && grep -q 'merged' "$root/t" && grep -qi 'not run' "$root/t" && ok "the text output shows the counts, the hidden card with its reason, and what was not run" || bad "text output" "$(cat "$root/t")"

# --- Q5 c: closing the card ends it; a merged PR does not -------------------------------------
printf '{"c1":{"status":"open"},"c2":{"status":"closed"},"c3":{"status":"open"},"c4":{"status":"open"},"c5":{"status":"open"},"c6":{"status":"open"},"c7":{"status":"open"}}' >"$root/cards2.json"
"$SNAP" write --cards "$root/cards2.json" --reported 7 >/dev/null
"$DWS" queue --json >"$root/j" 2>/dev/null
[[ "$(q "d['counts']['open']" <"$root/j")" == "6" && "$(hidden)" == "[('c3', 'draft')]" ]] && ok "once director closes the card it leaves the queue (c2 is not hidden any more, it is closed)" || bad "closing a card" "$(q "d['counts']" <"$root/j")"

# --- review 5586679189 item 1: a torn ledger line may hold a PR state, so nothing is hidden on it -------------
"$SNAP" write --cards "$root/cards.json" --reported 8 >/dev/null
cp -R "$DIRECTOR_STATE" "$root/state-torn"
printf 'torn line, perhaps a ready observation\n' >>"$root/state-torn/workstreams.jsonl"
DIRECTOR_STATE="$root/state-torn" "$DWS" queue --json >"$root/jt" 2>"$root/et" && ok "a partly torn ledger still exits 0" || bad "torn ledger exit" "$(cat "$root/et")"
[[ "$(q "d['sources']['ledger']['torn']" <"$root/jt")" == "1" ]] && ok "the JSON carries the torn count" || bad "torn count in JSON"
[[ "$(q "d['hidden']" <"$root/jt")" == "[]" && "$(q "d['counts']['hidden']" <"$root/jt")" == "0" ]] && ok "with a torn ledger line no card is hidden" || bad "no card hidden on a torn ledger" "$(q "d['hidden']" <"$root/jt")"
[[ "$(q "sorted(x['card'] for x in d['shown'])" <"$root/jt")" == "['c1', 'c2', 'c3', 'c4', 'c5', 'c6', 'c7']" ]] && ok "the cards that would have been hidden are shown" || bad "would-be hidden cards shown" "$(q "sorted(x['card'] for x in d['shown'])" <"$root/jt")"
[[ "$(q "[x['note'] for x in d['shown'] if x['card']=='c2']" <"$root/jt")" == "['would be hidden (merged); shown because a torn ledger line may hold a PR state']" ]] && ok "each says why it is shown" || bad "would-be hidden note" "$(q "[x for x in d['shown'] if x['card']=='c2']" <"$root/jt")"
DIRECTOR_STATE="$root/state-torn" "$DWS" queue >"$root/tt" 2>&1
grep -Eq '^queue .*ledger read \(torn 1\)' "$root/tt" && ok "the text output shows (torn 1) as board-state does" || bad "text shows torn count" "$(head -2 "$root/tt")"
"$SNAP" write --cards "$root/cards2.json" --reported 7 >/dev/null

# --- a stale snapshot, an unreadable ledger -----------------------------------------------------------
DWS_NOW=2026-10-23T12:00:00Z "$DWS" queue --json >"$root/j" 2>/dev/null
[[ "$(q "d['sources']['desk']['state']" <"$root/j")" == "UNREAD" && "$(q "d['shown']" <"$root/j")" == "None" ]] && ok "a stale Desk snapshot makes the queue UNREAD" || bad "stale snapshot" "$(cat "$root/j")"
mv "$LEDGER" "$root/ledger.saved"
"$DWS" queue --json >"$root/j" 2>/dev/null
[[ "$(q "d['sources']['ledger']['state']" <"$root/j")" == "UNREAD" && "$(q "sorted(x['kind'] for x in d['shown'])" <"$root/j")" == "['unclassified', 'unclassified', 'unclassified', 'unclassified', 'unclassified', 'unclassified']" && "$(q "d['hidden']" <"$root/j")" == "[]" ]] && ok "with the ledger UNREAD every open card is shown as unclassified and none is hidden" || bad "ledger UNREAD" "$(cat "$root/j")"
mv "$root/ledger.saved" "$LEDGER"

echo "dws queue: $pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
