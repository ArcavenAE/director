#!/usr/bin/env bash
# Prove the seat decision wait edges, the window, the order, the affected list and the two
# paths (D2, sim/design/seat-decisions.md, sections 4, 5, 6 and 8), built on D1's record.
# Each refusal leaves the ledger as it was. The merge-guard read is proved in
# verify-merge-guard.sh.
#
# Usage: skills/director/scripts/verify-dws-seat-waits.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DWS="${DWS:-$here/dws}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
export DWS_TEST_CLOCK=1 DWS_NO_RERENDER=1 PYTHONDONTWRITEBYTECODE=1
fresh() { export DIRECTOR_STATE="$root/$1"; mkdir -p "$DIRECTOR_STATE"; LEDGER="$DIRECTOR_STATE/workstreams.jsonl"; }
at() { export DWS_NOW="$1"; }
lines() { if [[ -f "$LEDGER" ]]; then wc -l <"$LEDGER" | tr -d ' '; else echo 0; fi; }
q() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
refuses() {  # LABEL WORD cmd...
  local label="$1" word="$2"; shift 2; local before; before="$(lines)"
  if "$@" >"$root/o" 2>"$root/e"; then bad "$label" "accepted"
  elif ! grep -qiF -- "$word" "$root/e"; then bad "$label" "refused without '$word': $(cat "$root/e")"
  elif [[ "$(lines)" != "$before" ]]; then bad "$label" "wrote anyway"
  else ok "$label"; fi
}
accepts() {  # LABEL cmd...
  local label="$1"; shift
  if "$@" >"$root/o" 2>"$root/e"; then ok "$label"; else bad "$label" "$(cat "$root/e")"; fi
}
state_of() { "$DWS" decisions --json 2>/dev/null | q "[x['state'] for x in d['decisions'] if x['id']=='$1'][0]" 2>/dev/null || echo none; }
waits_of() { "$DWS" show --json 2>/dev/null | q "' '.join([r for r in d['rows'] if r['slug']=='$1'][0].get('waits', []))" 2>/dev/null || echo none; }
links_of() { "$DWS" show --json 2>/dev/null | q "' '.join([r for r in d['rows'] if r['slug']=='$1'][0]['links'])" 2>/dev/null || echo none; }
row() { "$DWS" open "$1" --title "Row $1" --owner team-a/builder --stage "${2:-building}" >/dev/null 2>&1 || bad "open row $1"; }
dec() {  # ID [estimate] [question]; a decision added by the seat
  python3 - "$root/blk" "$1" "${2:-1}" "${3:-Which way?}" <<'P'
import sys
path, did, est, q = sys.argv[1:5]
open(path, "w").write(f"seat-decision@1\nid: {did}\nseat: team-a/builder\nquestion: {q}\noptions: a) one; b) two\n"
  "chosen: b) two because it is cheaper\ntest: 1 ok 2 ok 3 ok 4 ok 5 ok 6 ok 7 ok 8 ok\n"
  f"estimate: {est}\nundo: revert the commit\nchanges: pr:ArcavenAE/director#900\n")
P
  "$DWS" decision add --from "$root/blk" >/dev/null 2>"$root/e" || bad "add decision $1" "$(cat "$root/e")"
}
D1=team-a/builder-20261020-1

# --- the edge: its own field, never a link -------------------------------------------------
fresh s1; at 2026-10-20T12:00:00Z
row r1; row r2; dec $D1
refuses "link refuses a decision: edge" "dws set --waits" "$DWS" link r1 "decision:$D1"
refuses "open refuses a decision: link" "dws set --waits" "$DWS" open r9 --title t --link "decision:$D1"
refuses "waits refuses a decision that is not in the ledger" "no decision" "$DWS" set r1 --waits decision:team-a/builder-20261020-9
refuses "waits refuses a kind it does not know" "decision:" "$DWS" set r1 --waits nonsense:x
accepts "set --waits decision:<id> adds the edge" "$DWS" set r1 --waits "decision:$D1"
[[ "$(waits_of r1)" == "$D1" ]] && ok "the edge is in waits" || bad "the edge is in waits" "$(waits_of r1)"
[[ "$(links_of r1)" == "" ]] && ok "the edge is not in links" || bad "the edge is not in links" "$(links_of r1)"
python3 -c "
import json, sys
ev = [json.loads(l) for l in open('$LEDGER') if l.strip()]
w = [e for e in ev if e.get('event') == 'wait']
sys.exit(0 if len(w) == 1 and w[0]['ws'] == 'r1' and w[0]['add'] == ['$D1'] and w[0]['actor'] == 'director' else 1)
" 2>/dev/null && ok "the edge is one wait event" || bad "the edge is one wait event"
before="$(lines)"; "$DWS" set r1 --waits "decision:$D1" >/dev/null 2>&1 || true
[[ "$(lines)" == "$before" ]] && ok "setting an edge a row already has writes nothing" || bad "setting an edge a row already has writes nothing"
"$DWS" show --json 2>/dev/null | q "[r for r in d['rows'] if r['slug']=='r1'][0]['waits_state']['$D1']" 2>/dev/null | grep -qx waiting && ok "show --json shows the edge as waiting" || bad "show --json shows the edge as waiting"
refuses "only director writes an edge" "refresh may append" "$DWS" --actor refresh set r2 --waits "decision:$D1"

# --- unwait --------------------------------------------------------------------------------
refuses "unwait is refused while the decision is decided-by-seat" "confirm" "$DWS" set r1 --unwait "decision:$D1"
refuses "unwait refuses an edge the row does not have" "no edge" "$DWS" set r2 --unwait "decision:$D1"
"$DWS" decision confirm $D1 --words yes --ref relay:R-1 >/dev/null 2>&1 || bad "confirm"
"$DWS" show --json 2>/dev/null | q "[r for r in d['rows'] if r['slug']=='r1'][0]['waits_state']['$D1']" 2>/dev/null | grep -qx met && ok "after a confirm the edge is shown as met and stays" || bad "after a confirm the edge is shown as met and stays"
accepts "unwait is allowed after a confirm" "$DWS" set r1 --unwait "decision:$D1"
[[ "$(waits_of r1)" == "" ]] && ok "the edge is gone after unwait" || bad "the edge is gone after unwait"
refuses "waits is refused on a confirmed decision" "decided-by-seat" "$DWS" set r1 --waits "decision:$D1"

# --- the window ----------------------------------------------------------------------------
fresh s2; at 2026-10-20T12:00:00Z
for r in a b c d e; do row "r$r"; done; dec $D1
for r in a b c; do accepts "waits: dependent $r of three" "$DWS" set "r$r" --waits "decision:$D1"; done
refuses "a fourth dependent is refused as a full window" "window full: stop, and the decision goes to the top of the operator's list" "$DWS" set rd --waits "decision:$D1"
"$DWS" park ra --from building >/dev/null 2>&1 || bad "park ra"
accepts "a parked dependent frees a slot" "$DWS" set rd --waits "decision:$D1"
refuses "un-parking a fourth dependent is refused" "window full" "$DWS" stage ra building --from parked
accepts "parking a dependent is not checked" "$DWS" park rd --from building
accepts "un-parking when a slot is free is allowed" "$DWS" stage ra building --from parked
fresh s3; at 2026-10-20T12:00:00Z
row ra; row rb; dec $D1
at 2026-10-25T12:00:00Z
accepts "a wait exactly 120 hours after the decision is allowed" "$DWS" set ra --waits "decision:$D1"
at 2026-10-25T12:00:01Z
refuses "a wait past 120 hours is refused" "window full" "$DWS" set rb --waits "decision:$D1"
"$DWS" park ra --from building >/dev/null 2>&1 || bad "park ra"
refuses "un-parking past 120 hours is refused" "window full" "$DWS" stage ra building --from parked
fresh s3b; at 2026-10-20T12:00:00Z
row ra; dec $D1; "$DWS" set ra --waits "decision:$D1" >/dev/null 2>&1 || bad "wait"
"$DWS" decision override $D1 --words no --ref relay:R-2 --instead other >/dev/null 2>&1 || bad "override"
row rn
refuses "an overridden decision takes no new work" "decided-by-seat" "$DWS" set rn --waits "decision:$D1"

# --- ask: waits keep K13 and stay links -------------------------------------------------------
fresh s4; at 2026-10-20T12:00:00Z; row ra
printf '{"read_at":"2026-10-20T11:00:00Z","open":["card-9"]}' >"$DIRECTOR_STATE/desk-snapshot.json"
accepts "waits ask:<card> on an open card is a link" "$DWS" set ra --waits ask:card-9
[[ "$(links_of ra)" == "ask:card-9" && "$(waits_of ra)" == "" ]] && ok "an ask: wait stays in links, not in waits" || bad "an ask: wait stays in links" "$(links_of ra) / $(waits_of ra)"
refuses "waits ask:<card> on a card that is not open is refused" "not open" "$DWS" set ra --waits ask:card-8

# --- the order and the affected list ---------------------------------------------------------
fresh s5; at 2026-10-20T08:00:00Z
for r in a b c d e f g; do row "r$r"; done
dec team-a/builder-20261020-2 1 "Two?"
dec team-a/builder-20261020-10 1 "Ten?"
at 2026-10-21T08:00:00Z
dec team-a/builder-20261021-1 2 "Full window?"
dec team-a/builder-20261021-2 3 "Two waiting?"
dec team-a/builder-20261021-3 1 "One waiting?"
"$DWS" set ra --waits decision:team-a/builder-20261021-1 >/dev/null 2>&1; "$DWS" set rb --waits decision:team-a/builder-20261021-1 >/dev/null 2>&1; "$DWS" set rc --waits decision:team-a/builder-20261021-1 >/dev/null 2>&1
"$DWS" set rd --waits decision:team-a/builder-20261021-2 >/dev/null 2>&1; "$DWS" set re --waits decision:team-a/builder-20261021-2 >/dev/null 2>&1
"$DWS" set rf --waits decision:team-a/builder-20261021-3 >/dev/null 2>&1
at 2026-10-22T08:00:00Z
order() { "$DWS" decisions --unconfirmed --json 2>/dev/null | q "' '.join(x['id'] for x in d['decisions'])"; }
want="team-a/builder-20261021-1 team-a/builder-20261021-2 team-a/builder-20261021-3 team-a/builder-20261020-2 team-a/builder-20261020-10"
[[ "$(order)" == "$want" ]] && ok "order: full window, then dependents, then age, with -10 after -2 on a tie" || bad "order" "$(order)"
"$DWS" decisions --unconfirmed --json 2>/dev/null | q "[x['window_full'] for x in d['decisions']][:2]" | grep -qx '\[True, False\]' && ok "window_full is true for three dependents only" || bad "window_full"
"$DWS" decisions --daily --trial-start 2026-10-19 >"$root/daily" 2>/dev/null || true
grep -qF 'team-a/builder-20261021-2  2 waiting, 1d old, 3h at risk: Two waiting?' "$root/daily" && ok "the daily line counts the dependents waiting" || bad "the daily line counts the dependents waiting" "$(cat "$root/daily")"
at 2026-10-27T08:00:00Z
"$DWS" decisions --unconfirmed --json 2>/dev/null | q "' '.join(x['id'] for x in d['decisions'])" | grep -q '^team-a/builder-20261021-1 team-a/builder-20261021-2 team-a/builder-20261021-3 ' && ok "past five days a decision with one dependent is a full window and sorts first" || bad "past five days"
at 2026-10-22T08:00:00Z
# a tie on dependents, age and hours: seat label, then date, then number
fresh s5b; at 2026-10-20T08:00:00Z; dec team-b/builder-20261020-1; dec team-a/builder-20261020-1
[[ "$(order)" == "team-a/builder-20261020-1 team-b/builder-20261020-1" ]] && ok "a full tie is broken by seat label" || bad "tie by seat label" "$(order)"

fresh s6; at 2026-10-20T08:00:00Z
row ra building; row rb "in review"; row rc; dec $D1
for r in ra rb rc; do "$DWS" set "$r" --waits "decision:$D1" >/dev/null 2>&1; done
"$DWS" link ra pr:ArcavenAE/director#901 >/dev/null 2>&1
"$DWS" decisions --json 2>/dev/null | q "' '.join(sorted(x['slug']+':'+x['stage'] for x in d['decisions'][0]['affected']))" | grep -qx 'ra:building rb:in review rc:building' && ok "decisions --json carries every row with an edge, with its stage" || bad "affected in json" "$("$DWS" decisions --json 2>&1 | head -30)"
"$DWS" decisions --json 2>/dev/null | q "[x for x in d['decisions'][0]['affected'] if x['slug']=='ra'][0]['prs']" | grep -qF 'pr:ArcavenAE/director#901' && ok "an affected row carries its pr: links" || bad "affected pr links"
refuses "rework refuses a decision that is not overridden" "overridden" "$DWS" decision rework $D1
"$DWS" decision override $D1 --words no --ref relay:R-3 --instead other >"$root/o" 2>/dev/null || bad "override"
for s in "ra" "rb" "rc" "pr:ArcavenAE/director#901" "pr:ArcavenAE/director#900"; do grep -qF "$s" "$root/o" && ok "override prints $s" || bad "override prints $s" "$(cat "$root/o")"; done
refuses "rework refuses while an affected row is open" "ra" "$DWS" decision rework $D1
"$DWS" park ra --from building >/dev/null 2>&1; "$DWS" park rb --from "in review" >/dev/null 2>&1
refuses "rework still refuses with one open row" "rc" "$DWS" decision rework $D1
accepts "unwait is allowed after an override" "$DWS" set rc --unwait "decision:$D1"
accepts "rework follows once every row is parked or redone" "$DWS" decision rework $D1
[[ "$(state_of $D1)" == "reworked" ]] && ok "the decision is reworked" || bad "the decision is reworked"

# --- two paths (section 8) ---------------------------------------------------------------------
P1=team-a/architect-20261020-5
OK8="1 ok 2 ok 3 ok 4 ok 5 ok 6 ok 7 ok 8 ok"
paths() {  # ID CARD [extra args replacing defaults via env A_EST etc.]
  "$DWS" decision paths "$1" --card "$2" --evidence "${EV-measured both on the slow host}" --why-waiting "the card waits on the operator" \
    --row-a "${RA-pa}" --estimate-a "${EA-2}" --test-a "${TA-$OK8}" --row-b "${RB-pb}" --estimate-b "${EB-2}" --test-b "${TB-$OK8}"
}
fresh s7; at 2026-10-20T12:00:00Z
row pa; row pb; row pc
printf '{"read_at":"2026-10-20T11:00:00Z","open":["card-1","card-2"]}' >"$DIRECTOR_STATE/desk-snapshot.json"
EA=3 refuses "paths refuses estimates adding over 4" "4" paths $P1 card-1
EB=3 EA=2 refuses "paths refuses 5 seat-hours in total" "4" paths $P1 card-1
refuses "paths refuses a card that is not open" "not open" paths $P1 card-7
TA="1 ok 2 ok 3 ok 4 no 5 ok 6 ok 7 ok 8 ok" refuses "paths refuses a path whose test is not eight oks" "eight" paths $P1 card-1
TB="1 ok" refuses "paths refuses a second path whose test is not eight oks" "eight" paths $P1 card-1
EV=" " refuses "paths refuses blank evidence" "evidence" paths $P1 card-1
RA=nosuch refuses "paths refuses a row that does not exist" "no workstream" paths $P1 card-1
RB=pa refuses "paths refuses the same row twice" "two rows" paths $P1 card-1
accepts "paths records two paths" paths $P1 card-1
[[ "$(state_of $P1)" == "paths-open" ]] && ok "the record is paths-open" || bad "the record is paths-open" "$(state_of $P1)"
[[ "$(waits_of pa)" == "$P1" && "$(waits_of pb)" == "$P1" ]] && ok "paths writes the two edges itself" || bad "paths writes the two edges" "$(waits_of pa) / $(waits_of pb)"
python3 -c "
import json, sys
ev = [json.loads(l) for l in open('$LEDGER') if l.strip()]
i = [n for n, e in enumerate(ev) if e.get('event') == 'decision' and e.get('op') == 'paths']
w = [n for n, e in enumerate(ev) if e.get('event') == 'wait']
sys.exit(0 if len(i) == 1 and len(w) == 2 and ev[i[0]]['ts'] == ev[w[0]]['ts'] else 1)
" 2>/dev/null && ok "the record and its two edges are one append" || bad "the record and its two edges are one append"
refuses "a second paths on the same card is refused" "card-1" paths team-a/architect-20261020-6 card-1
refuses "waits to a paths-open id is refused for every row" "decided-by-seat" "$DWS" set pc --waits "decision:$P1"
refuses "unwait on a paths-open record is refused" "confirm" "$DWS" set pa --unwait "decision:$P1"
refuses "confirm refuses a paths-open record" "decided-by-seat" "$DWS" decision confirm $P1 --words yes --ref relay:R-4
"$DWS" decisions --unconfirmed --json 2>/dev/null | q "[x['waiting'] for x in d['decisions'] if x['id']=='$P1'][0]" | grep -qx 2 && ok "each path counts as one dependent" || bad "each path counts as one dependent"
refuses "pick refuses a winner that is not a or b" "a or b" "$DWS" decision pick $P1 --winner c --result "lessons in the winning PR notes"
refuses "pick refuses while the loser is not parked" "parked" "$DWS" decision pick $P1 --winner a --result "lessons in the winning PR notes"
"$DWS" park pb --from building >/dev/null 2>&1 || bad "park the loser"
refuses "pick refuses an empty result" "result" "$DWS" decision pick $P1 --winner a --result " "
accepts "pick moves the record to decided-by-seat" "$DWS" decision pick $P1 --winner a --result "lessons in the winning PR notes"
[[ "$(state_of $P1)" == "decided-by-seat" ]] && ok "after pick the record is decided-by-seat" || bad "after pick" "$(state_of $P1)"
"$DWS" decisions --json 2>/dev/null | q "[x for x in d['decisions'] if x['id']=='$P1'][0]['estimate_h']" | grep -qx 2.0 && ok "pick sets the winner's estimate" || bad "pick sets the winner's estimate"
"$DWS" decisions --unconfirmed --json 2>/dev/null | q "[x['waiting'] for x in d['decisions'] if x['id']=='$P1'][0]" | grep -qx 1 && ok "the winner's row is the one dependent" || bad "the winner's row is the one dependent"
refuses "pick on a record that is not paths-open is refused" "paths-open" "$DWS" decision pick $P1 --winner a --result "again"
refuses "a second paths on the same card after pick is refused" "card-1" paths team-a/architect-20261020-6 card-1
refuses "withdraw on a record that is not paths-open is refused" "paths-open" "$DWS" decision withdraw $P1 --words no --ref relay:R-5
accepts "the winner is confirmed under card 5" "$DWS" decision confirm $P1 --words yes --ref relay:R-6

fresh s8; at 2026-10-20T12:00:00Z
row pa; row pb
printf '{"read_at":"2026-10-20T11:00:00Z","open":["card-1","card-2"]}' >"$DIRECTOR_STATE/desk-snapshot.json"
paths $P1 card-1 >/dev/null 2>&1 || bad "paths for the withdraw case"
refuses "withdraw refuses while a row is not parked" "parked" "$DWS" decision withdraw $P1 --words "neither" --ref relay:R-7
"$DWS" park pa --from building >/dev/null 2>&1; "$DWS" park pb --from building >/dev/null 2>&1
refuses "withdraw refuses empty words" "words" "$DWS" decision withdraw $P1 --words " " --ref relay:R-7
accepts "withdraw moves the record to withdrawn" "$DWS" decision withdraw $P1 --words "neither path" --ref relay:R-7
[[ "$(state_of $P1)" == "withdrawn" ]] && ok "the record is withdrawn" || bad "the record is withdrawn" "$(state_of $P1)"
"$DWS" show --json 2>/dev/null | q "[r for r in d['rows'] if r['slug']=='pa'][0]['waits_state']['$P1']" | grep -qx ended && ok "a withdrawn record's edges are shown as ended" || bad "edges ended"
refuses "a second paths on the same card after withdraw is refused" "card-1" paths team-a/architect-20261020-6 card-1
refuses "a withdrawn record takes no pick" "paths-open" "$DWS" decision pick $P1 --winner a --result x
refuses "a withdrawn record takes no confirm" "decided-by-seat" "$DWS" decision confirm $P1 --words yes --ref relay:R-8
accepts "a withdrawn record's edge may be dropped" "$DWS" set pa --unwait "decision:$P1"
accepts "a new pair on a new card id is allowed" paths team-a/architect-20261020-7 card-2
[[ "$(state_of team-a/architect-20261020-7)" == "paths-open" ]] && ok "a new card id may get a new pair" || bad "a new card id may get a new pair"

# --- merge-hold: the read merge-guard makes ------------------------------------------------------
fresh s9; at 2026-10-20T12:00:00Z
row ra; row rb; dec $D1
"$DWS" link ra pr:ArcavenAE/director#901 >/dev/null 2>&1; "$DWS" set ra --waits "decision:$D1" >/dev/null 2>&1
if "$DWS" merge-hold ArcavenAE/director#901 >"$root/o" 2>"$root/e"; then bad "merge-hold holds a PR that waits on an unconfirmed decision"
else grep -qF "waits on seat decision $D1, not confirmed (seat-decide-window)" "$root/e" && ok "merge-hold holds a PR that waits on an unconfirmed decision" || bad "merge-hold text" "$(cat "$root/e")"; fi
accepts "merge-hold lets a PR the ledger does not link through" "$DWS" merge-hold ArcavenAE/director#999
accepts "merge-hold ignores the case of the repo" "$DWS" merge-hold arcavenae/director#999
"$DWS" decision confirm $D1 --words yes --ref relay:R-9 >/dev/null 2>&1
accepts "merge-hold lets the PR through once the decision is confirmed" "$DWS" merge-hold ArcavenAE/director#901

# A torn ledger line may be the very wait event that holds this PR, so merge-hold cannot call it clean.
fresh s10; at 2026-10-20T12:00:00Z
row ra; row rb; dec $D1
"$DWS" link ra pr:ArcavenAE/director#902 >/dev/null 2>&1; "$DWS" set ra --waits "decision:$D1" >/dev/null 2>&1
python3 - "$LEDGER" <<'P'
import sys
p = sys.argv[1]
b = open(p, "rb").read().rstrip(b"\n")
cut = b.rfind(b"\n") + 1
last = b[cut:]
open(p, "wb").write(b[:cut] + last[: len(last) // 2] + b"\n")  # the last event (the wait) cut in half
P
rc=0; "$DWS" merge-hold ArcavenAE/director#902 >"$root/o" 2>"$root/e" || rc=$?
if [[ $rc -eq 2 ]] && grep -qE '1 unreadable line' "$root/e"; then ok "merge-hold exits 2 and names the torn count when a ledger line is cut"
else bad "merge-hold exits 2 and names the torn count when a ledger line is cut" "rc=$rc $(cat "$root/e")"; fi

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
