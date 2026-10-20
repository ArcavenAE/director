#!/usr/bin/env bash
# Prove the grant confirm card (K10, sim/design/director-functions-redesign.md,
# "Grants"): director drafts a grant, `dws grant-card` renders a Desk card that
# shows its bounds, and `dws grant-confirm` records the confirmed grant only
# after the operator's answer to that card is on the Desk (the snapshot's
# "answered" list). A draft has no effect; confirming never edits the draft.
#
# Usage: skills/director/scripts/verify-dws-grant-card.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DWS="${DWS:-$here/dws}"
CI="${CI_BIN:-$here/card-ingest}"
SNAP="${SNAP_BIN:-$here/desk-snapshot}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
export DIRECTOR_STATE="$root/state"
export DWS_TEST_CLOCK=1 DWS_NOW=2026-10-20T12:00:00Z DWS_NO_RERENDER=1 PYTHONDONTWRITEBYTECODE=1
mkdir -p "$DIRECTOR_STATE"
RULINGS="$DIRECTOR_STATE/rulings.jsonl"
: >"$RULINGS"

"$DWS" record grant --id g1 --source "operator relay R-1" --subject "merge ArcavenAE docs PRs" \
  --scope "ArcavenAE repos, docs-only PRs, ready and approved at head" --to "director" --state draft \
  --expires 2026-11-20 --text "merge docs-only PRs that are ready and approved" >/dev/null
"$DWS" record grant --id g-noexp --source "operator relay R-2" --subject "x" --scope "y" --to "director" \
  --state draft --text "no expiry" >/dev/null
"$DWS" record grant --id g-conf --source "operator relay R-3" --subject "x" --scope "y" --to "director" \
  --state confirmed --text "already confirmed" >/dev/null

snap() {  # ANSWERS_JSON
  printf '{"grant-card-1":{"status":"open"},"other":{"status":"open"}}' >"$root/cards.json"
  printf '%s' "$1" >"$root/answers.json"
  "$SNAP" write --cards "$root/cards.json" --reported 2 --answers "$root/answers.json" --answers-reported "$2" >/dev/null
}

# --- the card ---------------------------------------------------------------
"$DWS" grant-card g1 --out "$root/card.md" >"$root/o" 2>"$root/e" && ok "grant-card renders a draft" || bad "grant-card renders a draft" "$(cat "$root/e")"
for w in "merge ArcavenAE docs PRs" "ArcavenAE repos, docs-only PRs, ready and approved at head" "director" "2026-11-20" "merge docs-only PRs that are ready and approved" "operator relay R-1" "g1"; do
  grep -qF -- "$w" "$root/card.md" && ok "the card shows: $w" || bad "the card shows: $w"
done
snap '[]' 0
"$CI" parse "$root/card.md" --area grants --owner director --prefix grant >"$root/o2" 2>"$root/e2" \
  && ok "card-ingest accepts the rendered card" || bad "card-ingest accepts the rendered card" "$(cat "$root/e2")"
"$DWS" grant-card g-noexp >"$root/c2" 2>/dev/null && grep -q 'no expiry' "$root/c2" && ok "a grant with no expiry says so on the card" || bad "a grant with no expiry says so on the card"
if "$DWS" grant-card g-conf >/dev/null 2>"$root/e"; then bad "grant-card refuses a confirmed grant"; else ok "grant-card refuses a confirmed grant"; fi
if "$DWS" grant-card nope >/dev/null 2>"$root/e"; then bad "grant-card refuses an unknown id"; else ok "grant-card refuses an unknown id"; fi
if grep -q 'takes effect\|absent a ruling' "$root/card.md"; then bad "the card has no silence default"; else ok "the card has no silence default"; fi

# --- confirm ------------------------------------------------------------------
before="$(wc -l <"$RULINGS" | tr -d ' ')"
conf() { "$DWS" grant-confirm g1 --card grant-card-1 --quote "yes, confirm it" "$@"; }
rm -f "$DIRECTOR_STATE/desk-snapshot.json"
if conf >"$root/o" 2>"$root/e"; then bad "confirm refuses with no Desk snapshot"; else grep -q 'UNREAD' "$root/e" && ok "confirm refuses with no Desk snapshot (UNREAD)" || bad "confirm refuses with no Desk snapshot (UNREAD)" "$(cat "$root/e")"; fi
[[ "$(wc -l <"$RULINGS" | tr -d ' ')" == "$before" ]] && ok "nothing written on that refusal" || bad "nothing written on that refusal"

snap '[{"card":"other"}]' 1
if conf >/dev/null 2>"$root/e"; then bad "confirm refuses while the card is unanswered"; else grep -q 'not answered' "$root/e" && ok "confirm refuses while the card is unanswered" || bad "confirm refuses while the card is unanswered" "$(cat "$root/e")"; fi
[[ "$(wc -l <"$RULINGS" | tr -d ' ')" == "$before" ]] && ok "nothing written while unanswered" || bad "nothing written while unanswered"

snap '[{"card":"grant-card-1"}]' 1
if "$DWS" grant-confirm g1 --card grant-card-1 >/dev/null 2>&1; then bad "confirm needs the operator's quoted answer"; else ok "confirm needs the operator's quoted answer"; fi
conf >"$root/o" 2>"$root/e" && ok "confirm records once the card is answered" || bad "confirm records once the card is answered" "$(cat "$root/e")"
[[ "$(wc -l <"$RULINGS" | tr -d ' ')" == "$((before+1))" ]] && ok "exactly one record appended" || bad "exactly one record appended"
last="$(tail -1 "$RULINGS")"
j() { python3 -c "import json,sys; d=json.loads(sys.argv[1]); print($1)" "$last"; }
[[ "$(j "d['kind'], d['state'], d['replaces'], d['card']")" == "grant confirmed g1 grant-card-1" ]] && ok "the record is a confirmed grant replacing the draft, with the card" || bad "the record is a confirmed grant replacing the draft, with the card"
[[ "$(j "d['subject']+'|'+d['scope']+'|'+d['to']+'|'+d['expires']")" == "merge ArcavenAE docs PRs|ArcavenAE repos, docs-only PRs, ready and approved at head|director|2026-11-20" ]] && ok "bounds are copied from the draft" || bad "bounds are copied from the draft"
[[ "$(j "d['quote']")" == "yes, confirm it" ]] && ok "the operator's words are stored as given" || bad "the operator's words are stored as given"
grep -q '"id": *"g1"' "$RULINGS" && [[ "$(grep -c '"state": *"draft"' "$RULINGS")" == "2" ]] && ok "the draft line is untouched" || bad "the draft line is untouched"
"$DWS" records --json | python3 -c "import json,sys; d=json.load(sys.stdin); r=[x for x in d['records'] if x['id']=='g1'][0]; print(r['replaced_by'])" | grep -q . && ok "records shows the draft as replaced" || bad "records shows the draft as replaced"
if conf >/dev/null 2>"$root/e"; then bad "a second confirm of the same draft is refused"; else ok "a second confirm of the same draft is refused"; fi
if "$DWS" grant-confirm g-conf --card grant-card-1 --quote q >/dev/null 2>&1; then bad "confirm refuses a grant that is not a draft"; else ok "confirm refuses a grant that is not a draft"; fi
if "$DWS" --actor refresh grant-confirm g-noexp --card grant-card-1 --quote q >/dev/null 2>&1; then bad "only director may confirm"; else ok "only director may confirm"; fi

echo "dws grant-card: $pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
