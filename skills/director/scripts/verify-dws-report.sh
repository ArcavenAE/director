#!/usr/bin/env bash
# Prove `dws report` (aae-orc-12p1l, T7 of the director-cue plan): the operator report as a board
# diff. The report compares the ledger now against the last state it reported and prints only what
# the operator must see: a new or changed WAKE or QUEUE row, and a completion nobody registered on
# the board. A change that leaves every row RECORD prints nothing. The baseline is
# $DIRECTOR_STATE/report-seen.json, beside picks-seen.jsonl. An ask closed answered or carded whose
# `dws asks` row reads "none" is a custody gap, not a presentation exclusion, so it prints as
# "unaccounted" with the ask id and the --ref of its close (director ruling on aae-orc-12p1l; wardrobe
# director role section 8, "the unaccounted remainder reported separately").
#
# Usage: skills/director/scripts/verify-dws-report.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DWS="${DWS:-$here/dws}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
export DWS_TEST_CLOCK=1 DWS_NOW=2026-11-12T12:00:00Z DWS_NO_RERENDER=1 PYTHONDONTWRITEBYTECODE=1
export DIRECTOR_STATE="$root/state"; mkdir -p "$DIRECTOR_STATE"
SEEN="$DIRECTOR_STATE/report-seen.json"
rep() { local out="$1"; shift; if "$DWS" report "$@" >"$out" 2>&1; then rc=0; else rc=$?; fi; }  # run report; rc is its exit status

# --- nothing to report: silent, exit 0 -------------------------------------------------------------
rep "$root/o0"
[[ $rc == 0 ]] && ok "report exits 0 with no stores" || bad "report exits 0 with no stores" "$(cat "$root/o0")"
[[ $rc == 0 && ! -s "$root/o0" ]] && ok "an empty board prints nothing" || bad "an empty board prints nothing" "$(cat "$root/o0")"

# --- unaccounted: a completion with no board row --------------------------------------------------
ask() { printf '%s\n' "$1" | "$DWS" intake --text-file - --source test; }
a1="$(ask 'ask one')"; a2="$(ask 'ask two')"; a3="$(ask 'ask three')"; a4="$(ask 'ask four')"; a5="$(ask 'ask five')"
"$DWS" ask-close "$a1" --as answered --ref R-9 >/dev/null
"$DWS" ask-close "$a2" --as carded --ref card-x >/dev/null
"$DWS" ask-close "$a3" --as not-an-ask --reason 'a question' >/dev/null
"$DWS" ask-close "$a5" --as answered >/dev/null
"$DWS" open rowtwo --title t2 --owner director --link "intake:$a2" >/dev/null
# $a1 closed answered, no row: unaccounted. $a2 closed carded with a row: accounted. $a3 not an ask:
# not a completion. $a4 still open: not a completion. $a5 closed answered, no row, no --ref.
cp -R "$DIRECTOR_STATE" "$root/state-j"
"$DWS" report >"$root/o1" 2>"$root/e1" && ok "report exits 0 with a completion that has no row" || bad "report exit" "$(cat "$root/e1")"
grep -Eq "^unaccounted $a1 ref=R-9\$" "$root/o1" && ok "a completion with no row prints as unaccounted, with its ask id and --ref" || bad "unaccounted line" "$(cat "$root/o1")"
grep -Eq "^unaccounted $a5 ref=none\$" "$root/o1" && ok "a close with no --ref prints ref=none" || bad "unaccounted without ref" "$(cat "$root/o1")"
[[ "$(grep -c '^unaccounted ' "$root/o1")" == "2" ]] && ok "only the two unregistered completions are unaccounted: a row, a not-an-ask and an open ask are not" || bad "unaccounted count" "$(cat "$root/o1")"
grep -q "^unaccounted " "$root/o1" && ! grep -qi exclu "$root/o1" && ok "it is never called an exclusion" || bad "no exclusion wording" "$(cat "$root/o1")"
DIRECTOR_STATE="$root/state-j" "$DWS" report --json >"$root/j1" 2>/dev/null || true
[[ "$(python3 -c "import json,sys; d=json.load(open('$root/j1')); print(sorted((x['ask_id'], x['ref']) for x in d['unaccounted']))")" == "[('$a1', 'R-9'), ('$a5', 'none')]" ]] && ok "--json carries the unaccounted list" || bad "--json unaccounted" "$(cat "$root/j1")"

# --- the baseline: a reported item is not reported twice ------------------------------------------
[[ -s "$SEEN" ]] && ok "report records what it reported in report-seen.json" || bad "baseline written"
rep "$root/o2"
[[ $rc == 0 && ! -s "$root/o2" ]] && ok "a second run with nothing new prints nothing" || bad "second run silent" "$(cat "$root/o2")"
ledger_before="$(shasum -a 256 "$DIRECTOR_STATE/workstreams.jsonl" "$DIRECTOR_STATE/asks.jsonl")"
rep /dev/null
[[ -s "$SEEN" && "$(shasum -a 256 "$DIRECTOR_STATE/workstreams.jsonl" "$DIRECTOR_STATE/asks.jsonl")" == "$ledger_before" ]] && ok "report writes only its baseline, never the ledger or the asks" || bad "report leaves the stores alone"
a6="$(ask 'ask six')"
"$DWS" ask-close "$a6" --as carded --ref card-y >/dev/null
rep "$root/o3"
[[ $rc == 0 && "$(cat "$root/o3")" == "unaccounted $a6 ref=card-y" ]] && ok "a new unregistered completion prints alone on the next run" || bad "new completion alone" "$(cat "$root/o3")"
"$DWS" link rowtwo "intake:$a6" >/dev/null
rep "$root/o4"
[[ $rc == 0 && ! -s "$root/o4" ]] && ok "registering the row afterwards prints nothing" || bad "registered later is silent" "$(cat "$root/o4")"

# --- tiers: derived from blocked (director ruling on aae-orc-12p1l) ---------------------------------------
# The ledger has no tier field. On an open row, blocked "operator" is WAKE, any other non-empty
# blocked is QUEUE; an empty blocked, a parked row and a closed row are RECORD. A later tier field can
# replace the derivation without changing what the report means, so the derivation is named in --help
# and in --json (tier plus tier_source "blocked").
SNAP="${SNAP_BIN:-$here/desk-snapshot}"
printf '{"c1":{"status":"open"},"c2":{"status":"open"}}' >"$root/cards.json"
"$SNAP" write --cards "$root/cards.json" --reported 2 >/dev/null
"$DWS" open rec1 --title r1 --owner director --stage building >/dev/null
rep "$root/t0"
[[ $rc == 0 && ! -s "$root/t0" ]] && ok "a new RECORD row (no blocked) prints nothing" || bad "new RECORD row silent" "$(cat "$root/t0")"
"$DWS" open wk1 --title w1 --owner director --stage building --blocked operator --ask c1 >/dev/null
rep "$root/t1"
grep -Eq '^WAKE wk1( |$)' "$root/t1" && [[ "$(wc -l <"$root/t1" | tr -d ' ')" == "1" ]] && ok "a new WAKE row (blocked operator) prints, alone" || bad "new WAKE row" "$(cat "$root/t1")"
rep "$root/t2"
[[ $rc == 0 && ! -s "$root/t2" ]] && ok "an unchanged WAKE row is not printed again" || bad "WAKE reported once" "$(cat "$root/t2")"
"$DWS" open qu1 --title q1 --owner director --stage building --blocked 'waiting on review' >/dev/null
rep "$root/t3"
grep -Eq '^QUEUE qu1( |$)' "$root/t3" && ok "a new QUEUE row (blocked on something else) prints" || bad "new QUEUE row" "$(cat "$root/t3")"
"$DWS" set qu1 --blocked 'waiting on CI' >/dev/null
rep "$root/t4"
grep -Eq '^QUEUE qu1( |$)' "$root/t4" && ok "a changed QUEUE row prints" || bad "changed QUEUE row" "$(cat "$root/t4")"
"$DWS" stage rec1 'in review' --from building >/dev/null
"$DWS" set rec1 --owner someone --next 'ship it' >/dev/null
"$DWS" link rec1 'pr:o/r#7' >/dev/null
"$DWS" stage wk1 'in review' --from building >/dev/null
"$DWS" set qu1 --next 'wait' >/dev/null
rep "$root/t5"
[[ $rc == 0 && ! -s "$root/t5" ]] && ok "RECORD-only changes print nothing: stage moves, owner, next and links, on RECORD rows and on blocked rows whose blocked is unchanged" || bad "RECORD-only silent" "$(cat "$root/t5")"
"$DWS" set rec1 --blocked operator --ask c2 >/dev/null
rep "$root/t6"
grep -Eq '^WAKE rec1( |$)' "$root/t6" && ok "a row moving from RECORD to WAKE prints as a new WAKE row" || bad "RECORD to WAKE" "$(cat "$root/t6")"
"$DWS" set rec1 --blocked '' >/dev/null
"$DWS" park wk1 --from 'in review' >/dev/null
"$DWS" close qu1 --from building >/dev/null
rep "$root/t7"
[[ $rc == 0 && ! -s "$root/t7" ]] && ok "a row going to RECORD (unblocked, parked or closed) prints nothing" || bad "to RECORD silent" "$(cat "$root/t7")"
"$DWS" open wk2 --title w2 --owner director --stage building --blocked operator --ask c1 >/dev/null
"$DWS" report --json >"$root/tj" 2>/dev/null || true
[[ "$(python3 -c "import json; d=json.load(open('$root/tj')); print([(r['slug'], r['tier'], r['tier_source']) for r in d['rows']])")" == "[('wk2', 'WAKE', 'blocked')]" ]] && ok "--json names the tier and its source: tier WAKE, tier_source blocked" || bad "--json tier and tier_source" "$(cat "$root/tj")"
"$DWS" report --help >"$root/h" 2>&1 || true
grep -qi 'derived' "$root/h" && grep -q 'blocked' "$root/h" && ok "--help says the tier is derived from blocked" || bad "--help names the derivation" "$(cat "$root/h")"

# --- a report that cannot be delivered is not a report that was made --------------------------------------
# The baseline is written only after the output is written and flushed. Here stdout is a pipe whose
# read end is already closed, so the write fails; the next run must print the row it never delivered.
"$DWS" open wk3 --title w3 --owner director --stage building --blocked operator --ask c1 >/dev/null
python3 - "$DWS" >"$root/pipe-out" 2>&1 <<'PY' || true
import os, subprocess, sys
r, w = os.pipe()
os.close(r)
p = subprocess.run([sys.argv[1], "report"], stdout=w, stderr=subprocess.DEVNULL)
print("child rc", p.returncode)
PY
rep "$root/t8"
grep -Eq '^WAKE wk3( |$)' "$root/t8" && ok "a report whose output could not be written does not mark its rows as seen" || bad "undelivered row is printed on the next run" "$(cat "$root/pipe-out") / $(cat "$root/t8")"
rep "$root/t9"
[[ $rc == 0 && ! -s "$root/t9" ]] && ok "the row is printed once the output was delivered, and not again" || bad "delivered row not repeated" "$(cat "$root/t9")"

echo "dws report: $pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
