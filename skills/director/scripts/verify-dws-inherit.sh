#!/usr/bin/env bash
# Prove `dws inherit <role>` (K15, R-204): one by-role read of what a successor
# inherits: the role's standing terms (a replaced term is marked, never shown as
# standing), the grants made to it (confirmed and live, drafts waiting, expired),
# and its open asks (ledger rows it owns that link a card open on the Desk). A
# source that cannot be read says UNREAD; it is never an empty list. Read only.
#
# Usage: skills/director/scripts/verify-dws-inherit.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DWS="${DWS:-$here/dws}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
export DIRECTOR_STATE="$root/state"
export DWS_TEST_CLOCK=1 DWS_NOW=2026-10-20T12:00:00Z DWS_NO_RERENDER=1 PYTHONDONTWRITEBYTECODE=1
mkdir -p "$DIRECTOR_STATE"
LEDGER="$DIRECTOR_STATE/workstreams.jsonl"
RULINGS="$DIRECTOR_STATE/rulings.jsonl"
q() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }

# --- nothing readable: UNREAD, never empty -------------------------------------
"$DWS" inherit team-a/architect --json >"$root/j0" 2>/dev/null && ok "inherit exits 0 with no stores" || bad "inherit exits 0 with no stores"
for s in rulings ledger desk; do
  [[ "$(q "d['sources']['$s']['state']" <"$root/j0")" == "UNREAD" ]] && ok "$s is UNREAD with no store" || bad "$s is UNREAD with no store"
done
[[ "$(q "d['terms']" <"$root/j0")" == "None" ]] && ok "terms are unknown, not an empty list, while rulings is UNREAD" || bad "terms are unknown, not an empty list, while rulings is UNREAD"
[[ ! -e "$RULINGS" && ! -e "$LEDGER" ]] && ok "inherit creates no store" || bad "inherit creates no store"

# --- fixtures --------------------------------------------------------------------
: >"$RULINGS"
rec() { printf '%s\n' "$1" >>"$RULINGS"; }
tm='"kind":"term","ts":"2026-10-10T00:00:00Z","actor":"director","tag":"mirror","source":"s","subject":"posting"'
rec "{$tm,\"id\":\"t1\",\"role\":\"team-a/architect\",\"text\":\"draft only, never post\",\"set_by\":\"director\"}"
rec "{$tm,\"id\":\"t2\",\"role\":\"team-a/architect\",\"text\":\"post a COMMENT, never a review\",\"set_by\":\"director\",\"replaces\":\"t1\"}"
rec "{$tm,\"id\":\"t3\",\"role\":\"team-a/architect\",\"text\":\"keep notes short\",\"set_by\":\"director\"}"
rec "{$tm,\"id\":\"t-other\",\"role\":\"team-b/builder\",\"text\":\"other role\",\"set_by\":\"director\"}"
g='"kind":"grant","ts":"2026-10-01T00:00:00Z","actor":"director","tag":"mirror","source":"s","subject":"x","scope":"y","text":"t"'
rec "{$g,\"id\":\"g-live\",\"to\":\"team-a/architect\",\"state\":\"confirmed\",\"expires\":\"2026-12-01\"}"
rec "{$g,\"id\":\"g-noexp\",\"to\":\"team-a/architect\",\"state\":\"confirmed\"}"
rec "{$g,\"id\":\"g-expired\",\"to\":\"team-a/architect\",\"state\":\"confirmed\",\"expires\":\"2026-10-10\"}"
rec "{$g,\"id\":\"g-draft\",\"to\":\"team-a/architect\",\"state\":\"draft\",\"tag\":\"proposal\"}"
rec "{$g,\"id\":\"g-old\",\"to\":\"team-a/architect\",\"state\":\"confirmed\",\"expires\":\"2026-12-01\"}"
rec "{$g,\"id\":\"g-new\",\"to\":\"team-a/architect\",\"state\":\"confirmed\",\"expires\":\"2026-12-02\",\"replaces\":\"g-old\"}"
rec "{$g,\"id\":\"g-other\",\"to\":\"team-b/builder\",\"state\":\"confirmed\"}"
t='"ts":"2026-10-20T10:00:00Z","actor":"director","tag":"judgment"'
printf '%s\n' "{$t,\"event\":\"open\",\"ws\":\"ws1\",\"title\":\"one\",\"owner\":\"team-a/architect\",\"stage\":\"designed\",\"links\":[\"ask:c1\",\"ask:c-closed\"]}" >"$LEDGER"
printf '%s\n' "{$t,\"event\":\"open\",\"ws\":\"ws2\",\"title\":\"two\",\"owner\":\"team-b/builder\",\"stage\":\"designed\",\"links\":[\"ask:c1\"]}" >>"$LEDGER"
printf '%s\n' "{$t,\"event\":\"open\",\"ws\":\"ws3\",\"title\":\"three\",\"owner\":\"team-a/architect\",\"stage\":\"designed\",\"links\":[]}" >>"$LEDGER"
cat >"$DIRECTOR_STATE/desk-snapshot.json" <<J
{"read_at":"2026-10-20T09:00:00Z","cards":2,"open":["c1"],"ids":{"c1":"open","c-closed":"closed"}}
J
before="$(shasum -a 256 "$LEDGER" "$RULINGS" "$DIRECTOR_STATE/desk-snapshot.json")"

"$DWS" inherit team-a/architect --json >"$root/j" 2>"$root/e" && ok "inherit exits 0 on fixtures" || bad "inherit exits 0 on fixtures" "$(cat "$root/e")"
ids() { q "sorted(x['id'] for x in d['$1'])" <"$root/j"; }
[[ "$(ids terms)" == "['t2', 't3']" ]] && ok "standing terms: the replacement and the untouched one" || bad "standing terms" "$(ids terms)"
[[ "$(q "[(x['id'], x['replaced_by']) for x in d['replaced_terms']]" <"$root/j")" == "[('t1', ['t2'])]" ]] && ok "a replaced term is marked with its replacement, not standing" || bad "a replaced term is marked with its replacement"
[[ "$(ids grants)" == "['g-live', 'g-new', 'g-noexp']" ]] && ok "grants in force: confirmed, live or with no expiry, not replaced" || bad "grants in force" "$(ids grants)"
[[ "$(ids pending_grants)" == "['g-draft']" ]] && ok "a draft grant is listed as pending, never in force" || bad "pending grants" "$(ids pending_grants)"
[[ "$(ids expired_grants)" == "['g-expired']" ]] && ok "an expired grant is listed as expired" || bad "expired grants" "$(ids expired_grants)"
[[ "$(q "[(x['ws'], x['card']) for x in d['open_asks']]" <"$root/j")" == "[('ws1', 'c1')]" ]] && ok "open asks: only the role's rows and only cards open on the Desk" || bad "open asks" "$(q "d['open_asks']" <"$root/j")"
[[ "$(q "d['sources']['desk']['read_at']" <"$root/j")" == "2026-10-20T09:00:00Z" ]] && ok "the Desk read time is printed" || bad "the Desk read time is printed"
after="$(shasum -a 256 "$LEDGER" "$RULINGS" "$DIRECTOR_STATE/desk-snapshot.json")"
[[ "$before" == "$after" ]] && ok "the stores are byte-identical after" || bad "the stores are byte-identical after"
"$DWS" inherit team-b/builder --json | q "sorted(x['id'] for x in d['terms'])" | grep -q "t-other" && ok "another role sees its own terms" || bad "another role sees its own terms"
"$DWS" inherit team-a/architect >"$root/t" 2>&1; grep -q 'draft only' "$root/t" && bad "text output shows a replaced term as standing" || ok "text output does not show a replaced term as standing"
grep -q 'post a COMMENT' "$root/t" && ok "text output lists the standing term" || bad "text output lists the standing term"
if "$DWS" inherit reviewer-g9-9 >/dev/null 2>"$root/e"; then bad "inherit refuses an instance id"; else grep -qi 'instance id' "$root/e" && ok "inherit refuses an instance id" || bad "inherit refuses an instance id"; fi

# --- a stale Desk snapshot: asks UNREAD, the rest still read ------------------------
cat >"$DIRECTOR_STATE/desk-snapshot.json" <<J
{"read_at":"2026-10-10T09:00:00Z","cards":1,"open":["c1"],"ids":{"c1":"open"}}
J
"$DWS" inherit team-a/architect --json >"$root/j2"
[[ "$(q "d['sources']['desk']['state']" <"$root/j2")" == "UNREAD" && "$(q "d['open_asks']" <"$root/j2")" == "None" ]] && ok "a stale snapshot makes open asks UNREAD, not empty" || bad "a stale snapshot makes open asks UNREAD, not empty"
[[ "$(q "len(d['terms'])" <"$root/j2")" == "2" ]] && ok "terms still read" || bad "terms still read"

echo "dws inherit: $pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
