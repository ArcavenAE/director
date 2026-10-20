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
[[ "$(q "d['queue']['count'], d['queue']['at_least']" <"$root/j2")" == "4 True" ]] && ok "queue reads at least 4 while rings is UNREAD" || bad "queue reads at least 4 while rings is UNREAD" "$(q "d['queue']" <"$root/j2")"
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
[[ "$(q "any(p['kind']=='unmoved' and p['subject']=='old' for p in d['proposals'])" <"$root/j3")" == "True" ]] && ok "ledger-only checks still run" || bad "ledger-only checks still run"

# --- 4. only director-class callers: an unknown flag is refused ----------------
"$DWS" reconcile --apply >/dev/null 2>&1 && bad "reconcile has no apply flag" || ok "reconcile has no apply flag"

# --- 5. a malformed store is UNREAD, never "read and empty" (review 5580134662) ----------
good_desk='{"read_at":"2026-10-20T09:00:00Z","cards":1,"open":["c1"],"ids":{"c1":"open"}}'
g2='"kind":"grant","ts":"2026-10-01T00:00:00Z","actor":"director","source":"s","subject":"x","scope":"s","to":"r","text":"t","state":"confirmed","tag":"mirror"'
fresh() {  # sets DIRECTOR_STATE to a new dir holding a good desk snapshot, ledger and rulings
  export DIRECTOR_STATE="$root/st$1"; mkdir -p "$DIRECTOR_STATE"
  printf '%s\n' "$good_desk" >"$DIRECTOR_STATE/desk-snapshot.json"
  printf '%s\n' '{"ts":"2026-09-01T10:00:00Z","actor":"director","tag":"judgment","event":"open","ws":"old","title":"t","stage":"building","links":[]}' >"$DIRECTOR_STATE/workstreams.jsonl"
  printf '%s\n' "{$g2,\"id\":\"g-ok\",\"expires\":\"2026-12-01\"}" >"$DIRECTOR_STATE/rulings.jsonl"
}
fresh 1; printf 'garbage one\n{broken\nnot json\n' >"$DIRECTOR_STATE/workstreams.jsonl"
"$DWS" reconcile --json >"$root/m1" 2>/dev/null
[[ "$(q "d['sources']['ledger']['state']" <"$root/m1")" == "UNREAD" ]] && ok "a ledger of only unreadable lines reads UNREAD" || bad "a ledger of only unreadable lines reads UNREAD" "$(q "d['sources']['ledger']" <"$root/m1")"
[[ "$(q "'torn 3' in d['sources']['ledger'].get('why','')" <"$root/m1")" == "True" ]] && ok "the UNREAD reason carries the torn count" || bad "the UNREAD reason carries the torn count"
[[ "$(q "sorted(n['check'] for n in d['not_run'] if n['check'] in ('unmoved','blocked-no-open-card','card-pr-merged','duplicate-open-cards'))" <"$root/m1")" == "['blocked-no-open-card', 'card-pr-merged', 'duplicate-open-cards', 'unmoved']" ]] && ok "the ledger's checks are listed as not run" || bad "the ledger's checks are listed as not run"
[[ "$(q "d['queue']['at_least']" <"$root/m1")" == "True" ]] && ok "the queue reads at least n with an UNREAD ledger" || bad "the queue reads at least n with an UNREAD ledger"
fresh 2; printf 'garbage\n{broken\n' >"$DIRECTOR_STATE/rulings.jsonl"
"$DWS" reconcile --json >"$root/m2" 2>/dev/null
[[ "$(q "d['sources']['rulings']['state']" <"$root/m2")" == "UNREAD" && "$(q "'grant-expired' in [n['check'] for n in d['not_run']]" <"$root/m2")" == "True" ]] && ok "a malformed rulings file reads UNREAD and grant-expired is not run" || bad "malformed rulings"
fresh 3; printf 'torn line\n' >>"$DIRECTOR_STATE/workstreams.jsonl"
"$DWS" reconcile >"$root/m3" 2>/dev/null
grep -E '^ledger +read .*torn 1' "$root/m3" >/dev/null && ok "a partly torn ledger shows its torn count in the text output" || bad "a partly torn ledger shows its torn count in the text output" "$(grep '^ledger' "$root/m3")"
fresh 4; : >"$DIRECTOR_STATE/workstreams.jsonl"
"$DWS" reconcile --json >"$root/m4" 2>/dev/null
[[ "$(q "d['sources']['ledger']['state']" <"$root/m4")" == "read" ]] && ok "an empty ledger file is read, with no rows" || bad "an empty ledger file is read, with no rows"
# round 3 (review 5580315151): lines that parse but fold to no row are UNREAD too
fresh 5; printf '%s\n' '{}' '{"ts":"2026-09-01T10:00:00Z","actor":"director","tag":"judgment","event":"move","ws":"nobody","stage":"done"}' >"$DIRECTOR_STATE/workstreams.jsonl"
"$DWS" reconcile --json >"$root/m5" 2>/dev/null && rc=0 || rc=$?
[[ "$rc" == 0 && "$(q "d['sources']['ledger']['state']" <"$root/m5")" == "UNREAD" ]] && ok "a ledger whose lines parse but fold to no row reads UNREAD" || bad "parsed but zero rows reads UNREAD" "rc=$rc $(q "d['sources']['ledger']" <"$root/m5")"
[[ "$(q "sorted(n['check'] for n in d['not_run'] if n['check'] in ('unmoved','blocked-no-open-card','card-pr-merged','duplicate-open-cards'))" <"$root/m5")" == "['blocked-no-open-card', 'card-pr-merged', 'duplicate-open-cards', 'unmoved']" && "$(q "d['queue']['at_least']" <"$root/m5")" == "True" ]] && ok "its checks are listed as not run and the queue reads at least n" || bad "zero rows: checks not run and at least n"
fresh 6; printf '\377\376 not utf8\n' >>"$DIRECTOR_STATE/workstreams.jsonl"; printf '\377bad\n' >>"$DIRECTOR_STATE/rulings.jsonl"
"$DWS" reconcile --json >"$root/m6" 2>"$root/e6" && rc=0 || rc=$?
[[ "$rc" == 0 ]] && ! grep -q Traceback "$root/e6" && [[ "$(q "[d['sources']['ledger'].get('torn'), d['sources']['rulings'].get('torn')]" <"$root/m6")" == "[1, 1]" ]] && ok "a non-UTF-8 line in the ledger or rulings is a torn line, not a traceback" || bad "non-UTF-8 line" "rc=$rc $(head -c 300 "$root/e6")"

# --- 6. an expiry that does not parse is named; a date-only expiry runs to the end of that day --------
fresh 5
printf '%s\n' "{$g2,\"id\":\"g-bad\",\"expires\":\"next week\"}" "{$g2,\"id\":\"g-today\",\"expires\":\"2026-10-20\"}" "{$g2,\"id\":\"g-yday\",\"expires\":\"2026-10-19\"}" >>"$DIRECTOR_STATE/rulings.jsonl"
"$DWS" reconcile --json >"$root/m5" 2>/dev/null
[[ "$(q "[n['check'] for n in d['not_run'] if 'g-bad' in n['check']]" <"$root/m5")" == "['grant-expired:g-bad']" ]] && ok "an expiry that does not parse is listed as not run" || bad "an unparseable expiry is listed" "$(q "d['not_run']" <"$root/m5")"
has5() { q "any(p['kind']=='grant-expired' and p['subject']=='$1' for p in d['proposals'])" <"$root/m5"; }
[[ "$(has5 g-today)" == "False" && "$(has5 g-yday)" == "True" ]] && ok "a date-only expiry holds through the end of that day" || bad "a date-only expiry holds through the end of that day"

# --- 7. a state dir that cannot be written is a refusal, not a traceback ---------------------------
fresh 6; chmod 555 "$DIRECTOR_STATE"
"$DWS" reconcile >"$root/m6" 2>"$root/m6e" && rc=0 || rc=$?
chmod 755 "$DIRECTOR_STATE"
[[ "$rc" == 1 ]] && ! grep -q Traceback "$root/m6e" && grep -q 'refused' "$root/m6e" && ok "an unwritable state dir exits 1 with a refusal and no traceback" || bad "unwritable state dir" "rc=$rc $(cat "$root/m6e" | head -3)"
"$DWS" reconcile --out "$root/elsewhere2.json" >/dev/null 2>&1 && ok "--out still works when the state dir cannot be written" || bad "--out elsewhere"

echo "dws reconcile: $pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
