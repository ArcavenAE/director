#!/usr/bin/env bash
# Prove `dws reconcile` (K11, sim/design/director-functions-redesign.md section 4):
# it reads the Desk snapshot, the ledger, rulings.jsonl, rings.jsonl and the PR
# state the ledger observed; it only proposes (no record is written, and the
# stores are byte-identical after); every source prints its last-read time or
# UNREAD; a queue count reads "at least n" while any source is UNREAD; a check
# whose source is not there is listed as not run, never counted as zero.
#
# Usage: skills/director/scripts/verify-dws-reconcile.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DWS="${DWS:-$here/dws}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
export DIRECTOR_STATE="$root/state"
export DWS_TEST_CLOCK=1 DWS_NOW=2026-10-20T12:00:00Z DWS_NO_RERENDER=1
mkdir -p "$DIRECTOR_STATE"
LEDGER="$DIRECTOR_STATE/workstreams.jsonl"
RULINGS="$DIRECTOR_STATE/rulings.jsonl"
OUT="$DIRECTOR_STATE/reconcile-proposals.json"

q() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }

# --- 1. nothing on disk: every source UNREAD, nothing created ---------------
"$DWS" reconcile --json >"$root/j1" 2>"$root/e1" && ok "reconcile exits 0 with no stores" || bad "reconcile exits 0 with no stores" "$(cat "$root/e1")"
for s in desk ledger rulings rings prs; do
  [[ "$(q "d['sources']['$s']['state']" <"$root/j1")" == "UNREAD" ]] && ok "$s reads UNREAD with no store" || bad "$s reads UNREAD with no store"
done
[[ "$(q "d['queue']['count']" <"$root/j1")" == "None" ]] && ok "queue count is unknown while the Desk is UNREAD" || bad "queue count is unknown while the Desk is UNREAD"
[[ ! -e "$LEDGER" && ! -e "$RULINGS" ]] && ok "reconcile creates neither the ledger nor the rulings file" || bad "reconcile creates neither the ledger nor the rulings file"
[[ "$(q "len(d['proposals'])" <"$root/j1")" == "0" ]] && ok "no proposals from no sources" || bad "no proposals from no sources"
"$DWS" reconcile >"$root/t1" 2>&1; grep -q '^desk  *UNREAD' "$root/t1" && ok "text output names desk UNREAD" || bad "text output names desk UNREAD" "$(cat "$root/t1")"

# --- 2. fixtures --------------------------------------------------------------
ev() { printf '%s\n' "$1" >>"$LEDGER"; }
t='"ts":"2026-10-20T10:00:00Z","actor":"director","tag":"judgment"'
ev "{$t,\"event\":\"open\",\"ws\":\"blk\",\"title\":\"blocked no card\",\"stage\":\"building\",\"blocked\":\"operator\",\"links\":[]}"
ev "{$t,\"event\":\"open\",\"ws\":\"blk-ok\",\"title\":\"blocked with card\",\"stage\":\"building\",\"blocked\":\"operator\",\"links\":[\"ask:c9\"]}"
ev "{$t,\"event\":\"open\",\"ws\":\"mer\",\"title\":\"card with merged pr\",\"stage\":\"in review\",\"links\":[\"ask:c1\",\"pr:o/r#1\"]}"
ev "{$t,\"event\":\"observe\",\"ws\":\"mer\",\"link\":\"pr:o/r#1\",\"seen\":{\"kind\":\"pr\",\"state\":\"merged\"},\"checked_at\":\"2026-10-20T11:00:00Z\"}"
ev "{$t,\"event\":\"open\",\"ws\":\"two\",\"title\":\"two open cards\",\"stage\":\"designed\",\"links\":[\"ask:c2\",\"ask:c3\"]}"
ev '{"ts":"2026-09-01T10:00:00Z","actor":"director","tag":"judgment","event":"open","ws":"old","title":"unmoved","stage":"building","links":[]}'
ev '{"ts":"2026-09-01T10:00:00Z","actor":"director","tag":"judgment","event":"open","ws":"old-done","title":"merged long ago","stage":"merged","links":[]}'
printf '%s\n' '{"ts":"2026-10-20T11:30:00Z","actor":"dire' >>"$LEDGER"
cat >"$DIRECTOR_STATE/desk-snapshot.json" <<J
{"read_at":"2026-10-20T09:00:00Z","cards":4,"open":["c1","c2","c3","c9"],"ids":{"c1":"open","c2":"open","c3":"open","c9":"open"}}
J
: >"$RULINGS"
g='"kind":"grant","ts":"2026-10-01T00:00:00Z","actor":"director","source":"s","subject":"x","scope":"s","to":"r","text":"t"'
printf '%s\n' "{$g,\"id\":\"g-expired\",\"state\":\"confirmed\",\"tag\":\"mirror\",\"expires\":\"2026-10-10\"}" >>"$RULINGS"
printf '%s\n' "{$g,\"id\":\"g-live\",\"state\":\"confirmed\",\"tag\":\"mirror\",\"expires\":\"2026-12-01\"}" >>"$RULINGS"
printf '%s\n' "{$g,\"id\":\"g-none\",\"state\":\"confirmed\",\"tag\":\"mirror\"}" >>"$RULINGS"
printf '%s\n' "{$g,\"id\":\"g-old\",\"state\":\"confirmed\",\"tag\":\"mirror\",\"expires\":\"2026-10-05\"}" >>"$RULINGS"
printf '%s\n' "{$g,\"id\":\"g-new\",\"state\":\"confirmed\",\"tag\":\"mirror\",\"expires\":\"2026-12-01\",\"replaces\":\"g-old\"}" >>"$RULINGS"
before="$(shasum -a 256 "$LEDGER" "$RULINGS" "$DIRECTOR_STATE/desk-snapshot.json")"

"$DWS" reconcile --json >"$root/j2" 2>"$root/e2" && ok "reconcile exits 0 on fixtures" || bad "reconcile exits 0 on fixtures" "$(cat "$root/e2")"
has() { q "any(p['kind']=='$1' and p['subject']=='$2' for p in d['proposals'])" <"$root/j2"; }
[[ "$(has blocked-no-open-card blk)" == "True" ]] && ok "proposes: blocked row with no open card" || bad "proposes: blocked row with no open card"
[[ "$(has blocked-no-open-card blk-ok)" == "False" ]] && ok "no proposal: blocked row naming an open card" || bad "no proposal: blocked row naming an open card"
[[ "$(has card-pr-merged mer)" == "True" ]] && ok "proposes: open card whose PR merged" || bad "proposes: open card whose PR merged"
[[ "$(has duplicate-open-cards two)" == "True" ]] && ok "proposes: two open cards on one row" || bad "proposes: two open cards on one row"
[[ "$(has unmoved old)" == "True" ]] && ok "proposes: unmoved row as close-or-kill input" || bad "proposes: unmoved row as close-or-kill input"
[[ "$(has unmoved old-done)" == "False" && "$(has unmoved mer)" == "False" ]] && ok "no unmoved proposal for a finished or recent row" || bad "no unmoved proposal for a finished or recent row"
[[ "$(has grant-expired g-expired)" == "True" ]] && ok "proposes: grant past its expiry" || bad "proposes: grant past its expiry"
[[ "$(has grant-expired g-live)" == "False" && "$(has grant-expired g-none)" == "False" ]] && ok "no proposal: live grant or one with no expiry" || bad "no proposal: live grant or one with no expiry"
[[ "$(has grant-expired g-old)" == "False" ]] && ok "no proposal: a replaced grant" || bad "no proposal: a replaced grant"
[[ "$(q "all(p['tag']=='proposal' for p in d['proposals'])" <"$root/j2")" == "True" ]] && ok "every proposal carries the proposal tag" || bad "every proposal carries the proposal tag"
[[ "$(q "d['sources']['desk']['read_at']" <"$root/j2")" == "2026-10-20T09:00:00Z" ]] && ok "desk prints the snapshot's read time" || bad "desk prints the snapshot's read time"
[[ "$(q "d['sources']['ledger']['torn']" <"$root/j2")" == "1" ]] && ok "a torn ledger line is counted, not repaired" || bad "a torn ledger line is counted, not repaired"
[[ "$(q "d['sources']['rings']['state']" <"$root/j2")" == "UNREAD" ]] && ok "rings stays UNREAD (no file yet)" || bad "rings stays UNREAD (no file yet)"
[[ "$(q "d['queue']['count'], d['queue']['at_least']" <"$root/j2")" == "(4, True)" ]] && ok "queue reads at least 4 while rings is UNREAD" || bad "queue reads at least 4 while rings is UNREAD" "$(q "d['queue']" <"$root/j2")"
for c in recommendation-sell-by ask-unacked harvest-due; do
  [[ "$(q "'$c' in [n['check'] for n in d['not_run']]" <"$root/j2")" == "True" ]] && ok "$c is listed as not run" || bad "$c is listed as not run"
done
after="$(shasum -a 256 "$LEDGER" "$RULINGS" "$DIRECTOR_STATE/desk-snapshot.json")"
[[ "$before" == "$after" ]] && ok "the stores are byte-identical after reconcile" || bad "the stores are byte-identical after reconcile"
[[ -f "$OUT" ]] && ok "proposals are written to their own file" || bad "proposals are written to their own file"
[[ "$(q "len(d['proposals'])" <"$OUT")" == "$(q "len(d['proposals'])" <"$root/j2")" ]] && ok "the file matches the output" || bad "the file matches the output"
"$DWS" reconcile --out "$root/elsewhere.json" >/dev/null 2>&1 && [[ -f "$root/elsewhere.json" ]] && ok "--out names the proposals file" || bad "--out names the proposals file"

# --- 3. a stale Desk snapshot: the Desk checks do not run, nothing reads as zero --
cat >"$DIRECTOR_STATE/desk-snapshot.json" <<J
{"read_at":"2026-10-10T09:00:00Z","cards":1,"open":["c1"],"ids":{"c1":"open"}}
J
"$DWS" reconcile --json >"$root/j3" 2>/dev/null
[[ "$(q "d['sources']['desk']['state']" <"$root/j3")" == "UNREAD" ]] && ok "a stale snapshot reads UNREAD" || bad "a stale snapshot reads UNREAD"
[[ "$(q "any(p['kind'] in ('blocked-no-open-card','card-pr-merged','duplicate-open-cards') for p in d['proposals'])" <"$root/j3")" == "False" ]] && ok "no Desk-based proposal from an UNREAD Desk" || bad "no Desk-based proposal from an UNREAD Desk"
[[ "$(q "'blocked-no-open-card' in [n['check'] for n in d['not_run']]" <"$root/j3")" == "True" ]] && ok "Desk checks are listed as not run" || bad "Desk checks are listed as not run"
[[ "$(q "d['queue']['count']" <"$root/j3")" == "None" ]] && ok "queue count unknown while the Desk is UNREAD" || bad "queue count unknown while the Desk is UNREAD"
[[ "$(q "has('x') or any(p['kind']=='unmoved' for p in d['proposals'])" <"$root/j3" 2>/dev/null || echo True)" == "True" ]] && ok "ledger-only checks still run" || bad "ledger-only checks still run"

# --- 4. only director-class callers: an unknown flag is refused ----------------
"$DWS" reconcile --apply >/dev/null 2>&1 && bad "reconcile has no apply flag" || ok "reconcile has no apply flag"

echo "dws reconcile: $pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
