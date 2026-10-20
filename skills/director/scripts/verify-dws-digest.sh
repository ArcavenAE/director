#!/usr/bin/env bash
# Prove `dws digest` (K18, sim/design/director-functions-redesign.md, rows 8 and 9; Q1 and Q7 as
# ruled): an operator digest and a close-or-kill list rendered from the reconciler and the queue,
# on demand only (nothing runs on a clock). It prints to stdout and writes the same text to
# $DIRECTOR_STATE/digest.md. The interrupt list is the Q1 default (an outage; a console step or
# recommendation expiring before the next digest; a seat blocked on your ask); an item whose
# source cannot be read says so and is never "none". Beyond digest.md and reconcile's own
# proposals file it writes no store.
#
# Usage: skills/director/scripts/verify-dws-digest.sh   Exit nonzero on any miss.
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
LEDGER="$DIRECTOR_STATE/workstreams.jsonl"; RULINGS="$DIRECTOR_STATE/rulings.jsonl"; DIGEST="$DIRECTOR_STATE/digest.md"
section() { sed -n "/^## $1/,/^## /p" "$DIGEST" | sed '$d'; }

# --- nothing readable ---------------------------------------------------------------------------
"$DWS" digest >"$root/o0" 2>"$root/e0" && ok "digest exits 0 with no stores" || bad "digest exits 0 with no stores" "$(cat "$root/e0")"
cmp -s "$root/o0" "$DIGEST" && ok "stdout and digest.md are the same text" || bad "stdout equals digest.md"
grep -q 'UNREAD' "$root/o0" && ok "unread sources are said, not omitted" || bad "UNREAD said" "$(cat "$root/o0")"
section Interrupts | grep -qi 'blocked on your ask.*UNREAD' && ok "with the Desk UNREAD, 'a seat blocked on your ask' is UNREAD, not none" || bad "interrupt UNREAD" "$(section Interrupts)"
[[ ! -e "$LEDGER" && ! -e "$RULINGS" ]] && ok "digest creates neither the ledger nor the rulings file" || bad "digest creates no store"

# --- fixtures --------------------------------------------------------------------------------------------
printf '{"c1":{"status":"open"},"c2":{"status":"open"}}' >"$root/cards.json"
"$SNAP" write --cards "$root/cards.json" --reported 2 >/dev/null
DWS_NOW=2026-10-10T00:00:00Z "$DWS" open alpha --title a --owner director --stage building --blocked operator --ask c1 >/dev/null
DWS_NOW=2026-09-01T00:00:00Z "$DWS" open oldidea --title "an old idea" --owner director --stage idea >/dev/null
DWS_NOW=2026-10-19T00:00:00Z "$DWS" open fresh --title f --owner director --stage building >/dev/null
printf '%s\n' '{"kind":"grant","id":"g-old","ts":"2026-09-01T00:00:00Z","actor":"director","tag":"mirror","source":"s","subject":"x","scope":"y","to":"director","state":"confirmed","text":"t","expires":"2026-10-01"}' >"$RULINGS"
before="$(shasum -a 256 "$LEDGER" "$RULINGS" "$DIRECTOR_STATE/desk-snapshot.json")"
"$DWS" digest >"$root/o" 2>"$root/e" && ok "digest exits 0 on fixtures" || bad "digest exits 0" "$(cat "$root/e")"
cmp -s "$root/o" "$DIGEST" && ok "stdout and digest.md are the same text" || bad "stdout equals digest.md"
[[ "$(shasum -a 256 "$LEDGER" "$RULINGS" "$DIRECTOR_STATE/desk-snapshot.json")" == "$before" ]] && ok "digest writes no ledger, ruling or snapshot" || bad "digest writes no store"
grep -q '^## Interrupts' "$DIGEST" && grep -q '^## Queue' "$DIGEST" && grep -q '^## Close-or-kill' "$DIGEST" && grep -q '^## Not run' "$DIGEST" && ok "the digest has its four sections" || bad "sections" "$(grep '^##' "$DIGEST")"
section Interrupts | grep -q 'alpha.*c1' && ok "a seat blocked on your ask is listed with its row and card" || bad "blocked on your ask" "$(section Interrupts)"
section Interrupts | grep -qi 'default list' && ok "the interrupt list is labelled as the default list" || bad "default list label"
section Interrupts | grep -qi 'outage.*not run' && section Interrupts | grep -qi 'expiring.*not run' && ok "the interrupt items with no source say 'not run'" || bad "not-run interrupt items" "$(section Interrupts)"
section Queue | grep -q '2 open' && ok "the queue section carries the open count" || bad "queue count" "$(section Queue)"
section Close-or-kill | grep -q 'oldidea' && ok "close-or-kill lists the unmoved row" || bad "close-or-kill lists oldidea" "$(section Close-or-kill)"
section Close-or-kill | grep 'oldidea' | grep -qi 'idea' && section Close-or-kill | grep 'oldidea' | grep -q '[0-9][0-9]* days' && ok "an idea row carries its age in days" || bad "idea age line" "$(section Close-or-kill)"
section Close-or-kill | grep -q 'fresh' && bad "a moved row is not listed" || ok "a recently moved row is not listed"
grep -q 'grant-expired' "$DIGEST" && grep -q 'g-old' "$DIGEST" && ok "other reconcile proposals are listed (the expired grant)" || bad "other proposals"
section 'Not run' | grep -q 'recommendation-sell-by' && ok "the reconciler's not-run checks are carried" || bad "not run carried" "$(section 'Not run')"
[[ -e "$DIRECTOR_STATE/reconcile-proposals.json" ]] && ok "reconcile's own proposals file was written (the digest runs the reconciler)" || bad "reconcile proposals file"
"$DWS" digest >"$root/o2" 2>/dev/null; cmp -s "$root/o" "$root/o2" && ok "a second run under the same clock is byte for byte the same" || bad "idempotent"

# --- the Q1 default list is documented where the operator edits it -----------------------------------------
grep -q 'DIGEST_INTERRUPTS' "$DWS" && ok "the default interrupt list is a named table in dws" || bad "DIGEST_INTERRUPTS table"
grep -q 'Q1' "$DWS" && ok "dws cites Q1 for it" || bad "cites Q1"

echo "dws digest: $pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
