#!/usr/bin/env bash
# Prove the seat decision record (D1, sim/design/seat-decisions.md, section 3):
# decision events in workstreams.jsonl under the ledger lock; `decision add --from`
# parses one seat-decision@1 block; actual, confirm, override and rework (state
# checks only); `decisions --json/--unconfirmed/--daily` reads and writes nothing.
# One fixture per refusal in section 3's table, each leaving the ledger as it was;
# a skip (no command) leaves the state unchanged; no command moves a state on a
# clock, and dws ignores DWS_NOW unless DWS_TEST_CLOCK=1 is set.
#
# Usage: skills/director/scripts/verify-dws-decisions.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DWS="${DWS:-$here/dws}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
export DIRECTOR_STATE="$root/state"
export DWS_TEST_CLOCK=1 DWS_NO_RERENDER=1 PYTHONDONTWRITEBYTECODE=1
mkdir -p "$DIRECTOR_STATE"
LEDGER="$DIRECTOR_STATE/workstreams.jsonl"
at() { export DWS_NOW="$1"; }
lines() { if [[ -f "$LEDGER" ]]; then wc -l <"$LEDGER" | tr -d ' '; else echo 0; fi; }
q() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
state_of() { "$DWS" decisions --json 2>/dev/null | q "[x['state'] for x in d['decisions'] if x['id']=='$1'][0]" 2>/dev/null || echo none; }
refuses() {  # LABEL WORD cmd...
  local label="$1" word="$2"; shift 2; local before; before="$(lines)"
  if "$@" >"$root/o" 2>"$root/e"; then bad "$label" "accepted"
  elif ! grep -qiF -- "$word" "$root/e"; then bad "$label" "refused without '$word': $(cat "$root/e")"
  elif [[ "$(lines)" != "$before" ]]; then bad "$label" "wrote anyway"
  else ok "$label"; fi
}

# block FILE ID [field=value...]: a seat-decision@1 block with every field set; a
# field=value pair replaces that field, field= drops it, and +line appends a line.
block() {
  local file="$1" id="$2"; shift 2
  python3 - "$file" "$id" "$@" <<'P'
import sys
path, did, edits = sys.argv[1], sys.argv[2], sys.argv[3:]
fields = [("id", did), ("seat", "team-a/builder"),
          ("question", "Which lock timeout should the fill use?"),
          ("options", "a) 5 s; b) 10 s"),
          ("chosen", "b) 10 s because the slow host took 7 s"),
          ("test", "1 ok 2 ok 3 ok 4 ok 5 ok 6 ok 7 ok 8 ok"),
          ("estimate", "2"), ("undo", "set it back to 5 s in one commit"),
          ("changes", "pr:ArcavenAE/director#901 bd:aae-orc-x1")]
extra = []
for e in edits:
    if e.startswith("+"):
        extra.append(e[1:])
        continue
    k, _, v = e.partition("=")
    fields = [(fk, v if fk == k else fv) for fk, fv in fields if not (fk == k and v == "")]
with open(path, "w") as f:
    f.write("seat-decision@1\n" + "".join(f"{k}: {v}\n" for k, v in fields) + "".join(x + "\n" for x in extra))
P
}

at 2026-10-20T12:00:00Z
# a row exists, so the decision events sit in a ledger the rest of dws reads
"$DWS" open base --title "Base row" --owner team-a/architect >/dev/null 2>&1 || bad "open the base row"

# --- add: the block parses and appends decided-by-seat ------------------------------------
block "$root/d1" team-a/builder-20261020-1
"$DWS" decision add --from "$root/d1" >"$root/o" 2>"$root/e" && ok "add accepts a whole seat-decision@1 block" || bad "add accepts a whole seat-decision@1 block" "$(cat "$root/e")"
[[ "$(state_of team-a/builder-20261020-1)" == "decided-by-seat" ]] && ok "a new decision is decided-by-seat" || bad "a new decision is decided-by-seat" "$(state_of team-a/builder-20261020-1)"
"$DWS" decisions --json >"$root/j" 2>/dev/null || true
[[ "$(q "d['decisions'][0]['chosen']" <"$root/j" 2>/dev/null)" == "b) 10 s" ]] && ok "chosen is split on the first ' because '" || bad "chosen is split on the first ' because '"
[[ "$(q "d['decisions'][0]['reason']" <"$root/j" 2>/dev/null)" == "the slow host took 7 s" ]] && ok "reason is the text after ' because '" || bad "reason is the text after ' because '"
[[ "$(q "d['decisions'][0]['estimate_h']" <"$root/j" 2>/dev/null)" == "2.0" ]] && ok "estimate_h is a number" || bad "estimate_h is a number"
[[ "$(q "' '.join(d['decisions'][0]['changes'])" <"$root/j" 2>/dev/null)" == "pr:ArcavenAE/director#901 bd:aae-orc-x1" ]] && ok "changes are kept as links" || bad "changes are kept as links"
[[ "$(q "d['decisions'][0]['actual_h']" <"$root/j" 2>/dev/null)" == "None" ]] && ok "actual_h is empty until recorded" || bad "actual_h is empty until recorded"
python3 -c "
import json, sys
ev = [json.loads(l) for l in open('$LEDGER') if l.strip()]
d = [e for e in ev if e.get('event') == 'decision']
sys.exit(0 if len(d) == 1 and d[0].get('op') == 'add' and d[0].get('actor') == 'director' and d[0].get('tag') == 'judgment' else 1)
" 2>/dev/null && ok "the add is one decision event in workstreams.jsonl, actor director, tag judgment" || bad "the add is one decision event in workstreams.jsonl"
"$DWS" show --json >"$root/s" 2>/dev/null || true
[[ "$(q "len(d['rows'])" <"$root/s" 2>/dev/null)" == "1" ]] && ok "a decision event makes no row" || bad "a decision event makes no row"

# --- add refusals (section 3's table: missing, unknown or repeated field; the id exists;
# the test is not eight oks; the estimate is over 4) -----------------------------------------
block "$root/r" team-a/builder-20261020-2 undo=
refuses "add refuses a missing field" "missing" "$DWS" decision add --from "$root/r"
block "$root/r" team-a/builder-20261020-2 "chosen=b) 10 s for the slow host"
refuses "add refuses a chosen line with no ' because <reason>'" "because" "$DWS" decision add --from "$root/r"
block "$root/r" team-a/builder-20261020-2 "+colour: blue"
refuses "add refuses an unknown field" "unknown" "$DWS" decision add --from "$root/r"
block "$root/r" team-a/builder-20261020-2 "+seat: team-b/builder"
refuses "add refuses a repeated field" "repeated" "$DWS" decision add --from "$root/r"
block "$root/r" team-a/builder-20261020-1 "question=Another question?"
refuses "add refuses an id that exists" "already" "$DWS" decision add --from "$root/r"
block "$root/r" team-a/builder-20261020-2 "test=1 ok 2 ok 3 ok 4 no 5 ok 6 ok 7 ok 8 ok"
refuses "add refuses a test with a failed line" "eight" "$DWS" decision add --from "$root/r"
block "$root/r" team-a/builder-20261020-2 "test=1 ok 2 ok 3 ok 4 ok 5 ok 6 ok 7 ok"
refuses "add refuses a test of seven oks" "eight" "$DWS" decision add --from "$root/r"
block "$root/r" team-a/builder-20261020-2 estimate=4.5
refuses "add refuses an estimate over 4" "over 4" "$DWS" decision add --from "$root/r"
block "$root/r" team-a/builder-20261020-2 estimate=soon
refuses "add refuses an estimate that is not a number" "number" "$DWS" decision add --from "$root/r"
block "$root/r" team-a/builder-g9-9-20261020-2
refuses "add refuses an id that carries an instance id" "instance id" "$DWS" decision add --from "$root/r"
block "$root/r" team-a/builder-20261020-2 seat=builder-g9-9
refuses "add refuses a seat that carries an instance id" "instance id" "$DWS" decision add --from "$root/r"
block "$root/r" Team-A/Builder-20261020-2
refuses "add refuses an id that is not <seat>-<YYYYMMDD>-<n> in lower case" "<seat label>-<YYYYMMDD>-<n>" "$DWS" decision add --from "$root/r"
refuses "add refuses a file it cannot read" "cannot read" "$DWS" decision add --from "$root/no-such-file"
block "$root/r" team-a/builder-20261020-2
refuses "only director appends a decision" "refresh may append" "$DWS" --actor refresh decision add --from "$root/r"
block "$root/d2" team-a/builder-20261020-2 estimate=4
"$DWS" decision add --from "$root/d2" >/dev/null 2>"$root/e" && ok "add accepts an estimate of exactly 4" || bad "add accepts an estimate of exactly 4" "$(cat "$root/e")"

# --- actual ------------------------------------------------------------------------------------
refuses "actual refuses an unknown id" "no decision" "$DWS" decision actual team-a/builder-20261020-9 --hours 1 --source "SEAT-DECISION-ACTUAL relay:R-12"
"$DWS" decision actual team-a/builder-20261020-1 --hours 3 --source "SEAT-DECISION-ACTUAL relay:R-12" >/dev/null 2>"$root/e" && ok "actual records the figure" || bad "actual records the figure" "$(cat "$root/e")"
"$DWS" decisions --json >"$root/j" 2>/dev/null || true
[[ "$(q "[x for x in d['decisions'] if x['id']=='team-a/builder-20261020-1'][0]['actual_h']" <"$root/j" 2>/dev/null)" == "3.0" ]] && ok "actual_h is stored" || bad "actual_h is stored"
[[ "$(q "[x for x in d['decisions'] if x['id']=='team-a/builder-20261020-1'][0]['actual_source']" <"$root/j" 2>/dev/null)" == "SEAT-DECISION-ACTUAL relay:R-12" ]] && ok "the actual's source is kept" || bad "the actual's source is kept"
[[ "$(state_of team-a/builder-20261020-1)" == "decided-by-seat" ]] && ok "actual moves no state" || bad "actual moves no state"

# --- confirm, override, rework: state checks -------------------------------------------------
refuses "confirm refuses empty words" "words" "$DWS" decision confirm team-a/builder-20261020-1 --words "  " --ref relay:R-20
refuses "override refuses empty words" "words" "$DWS" decision override team-a/builder-20261020-1 --words "" --ref relay:R-20 --instead "a) 5 s"
refuses "confirm refuses an unknown id" "no decision" "$DWS" decision confirm team-a/builder-20261020-9 --words yes --ref relay:R-20
refuses "override refuses an unknown id" "no decision" "$DWS" decision override team-a/builder-20261020-9 --words no --ref relay:R-20 --instead x
refuses "rework refuses an unknown id" "no decision" "$DWS" decision rework team-a/builder-20261020-9
refuses "rework refuses a decision that is not overridden" "overridden" "$DWS" decision rework team-a/builder-20261020-1

"$DWS" decision confirm team-a/builder-20261020-1 --words "yes, 10 s is fine" --ref relay:R-21 >/dev/null 2>"$root/e" && ok "confirm on decided-by-seat" || bad "confirm on decided-by-seat" "$(cat "$root/e")"
[[ "$(state_of team-a/builder-20261020-1)" == "confirmed" ]] && ok "confirm moves to confirmed" || bad "confirm moves to confirmed"
"$DWS" decisions --json >"$root/j" 2>/dev/null || true
[[ "$(q "[x for x in d['decisions'] if x['id']=='team-a/builder-20261020-1'][0]['moves'][-1]['words']" <"$root/j" 2>/dev/null)" == "yes, 10 s is fine" ]] && ok "the operator's words are kept on the confirm" || bad "the operator's words are kept on the confirm"
refuses "confirm refuses a decision that is not decided-by-seat" "decided-by-seat" "$DWS" decision confirm team-a/builder-20261020-1 --words again --ref relay:R-22
refuses "override refuses a decision that is not decided-by-seat" "decided-by-seat" "$DWS" decision override team-a/builder-20261020-1 --words no --ref relay:R-22 --instead "a) 5 s"
refuses "rework refuses a confirmed decision" "overridden" "$DWS" decision rework team-a/builder-20261020-1

"$DWS" decision override team-a/builder-20261020-2 --words "no, keep 5 s" --ref relay:R-23 --instead "a) 5 s" >"$root/o" 2>"$root/e" && ok "override on decided-by-seat" || bad "override on decided-by-seat" "$(cat "$root/e")"
grep -qF 'pr:ArcavenAE/director#901' "$root/o" && grep -qF 'bd:aae-orc-x1' "$root/o" && ok "override prints the decision's own changes" || bad "override prints the decision's own changes" "$(cat "$root/o")"
[[ "$(state_of team-a/builder-20261020-2)" == "overridden" ]] && ok "override moves to overridden" || bad "override moves to overridden"
"$DWS" decisions --json >"$root/j" 2>/dev/null || true
[[ "$(q "[x for x in d['decisions'] if x['id']=='team-a/builder-20261020-2'][0]['moves'][-1]['instead']" <"$root/j" 2>/dev/null)" == "a) 5 s" ]] && ok "the override keeps what was chosen instead" || bad "the override keeps what was chosen instead"
refuses "confirm refuses an overridden decision" "decided-by-seat" "$DWS" decision confirm team-a/builder-20261020-2 --words yes --ref relay:R-24
"$DWS" decision rework team-a/builder-20261020-2 >/dev/null 2>"$root/e" && ok "rework on an overridden decision" || bad "rework on an overridden decision" "$(cat "$root/e")"
[[ "$(state_of team-a/builder-20261020-2)" == "reworked" ]] && ok "rework moves to reworked" || bad "rework moves to reworked"
refuses "rework refuses a decision already reworked" "overridden" "$DWS" decision rework team-a/builder-20261020-2

# a hand-written move from the wrong state is not applied on read: no other move exists
python3 -c "
import json
print(json.dumps({'ts':'2026-10-20T12:30:00Z','actor':'director','tag':'judgment','event':'decision','op':'confirm','id':'team-a/builder-20261020-2','words':'x','ref':'relay:R-1'}))" >>"$LEDGER"
[[ "$(state_of team-a/builder-20261020-2)" == "reworked" ]] && ok "a stored move from the wrong state is not applied" || bad "a stored move from the wrong state is not applied"

# --- a skip, and no clock --------------------------------------------------------------------
at 2026-10-21T09:00:00Z
block "$root/d3" team-b/reviewer-20261021-1 estimate=1 "question=Split the test file?"
"$DWS" decision add --from "$root/d3" >/dev/null 2>&1 || bad "add the decision to skip"
before="$(lines)"
# a skip is no command at all; the reads and a later day leave it where it was
at 2026-10-22T09:00:00Z
"$DWS" decisions >/dev/null 2>&1 || true
"$DWS" decisions --unconfirmed >/dev/null 2>&1 || true
"$DWS" decisions --daily --trial-start 2026-10-19 >/dev/null 2>&1 || true
[[ "$(state_of team-b/reviewer-20261021-1)" == "decided-by-seat" ]] && ok "a skipped decision stays decided-by-seat" || bad "a skipped decision stays decided-by-seat"
"$DWS" decisions --unconfirmed >"$root/u" 2>/dev/null || true
grep -qF 'team-b/reviewer-20261021-1' "$root/u" && ok "a skipped decision stays on the unconfirmed list" || bad "a skipped decision stays on the unconfirmed list"
[[ "$(lines)" == "$before" ]] && ok "decisions writes nothing" || bad "decisions writes nothing"

at 2027-10-22T09:00:00Z
"$DWS" decisions --json >/dev/null 2>&1 || true
"$DWS" decisions --daily --trial-start 2026-10-19 >/dev/null 2>&1 || true
"$DWS" show >/dev/null 2>&1 || true
[[ "$(state_of team-b/reviewer-20261021-1)" == "decided-by-seat" ]] && ok "a year later by the test clock the state is unchanged" || bad "a year later by the test clock the state is unchanged"
[[ "$(state_of team-a/builder-20261020-1)" == "confirmed" ]] && [[ "$(state_of team-a/builder-20261020-2)" == "reworked" ]] && ok "a year later the answered decisions are unchanged" || bad "a year later the answered decisions are unchanged"
[[ "$(lines)" == "$before" ]] && ok "nothing was appended on the clock" || bad "nothing was appended on the clock"

block "$root/d4" team-c/builder-20261022-1
DWS_NOW=2020-01-01T00:00:00Z env -u DWS_TEST_CLOCK "$DWS" decision add --from "$root/d4" >/dev/null 2>&1 || bad "add without the test clock"
python3 -c "
import json, sys
ev = [json.loads(l) for l in open('$LEDGER') if l.strip()]
d = [e for e in ev if e.get('event') == 'decision' and e.get('id') == 'team-c/builder-20261022-1']
sys.exit(0 if len(d) == 1 and not d[0]['ts'].startswith('2020') else 1)
" 2>/dev/null && ok "without DWS_TEST_CLOCK=1, dws ignores DWS_NOW" || bad "without DWS_TEST_CLOCK=1, dws ignores DWS_NOW"

# --- the lists and the daily line ------------------------------------------------------------
export DIRECTOR_STATE="$root/st2"; mkdir -p "$DIRECTOR_STATE"; LEDGER="$DIRECTOR_STATE/workstreams.jsonl"
at 2026-10-20T08:00:00Z
block "$root/e1" team-a/builder-20261020-1 estimate=1 "question=Older, smaller?"
"$DWS" decision add --from "$root/e1" >/dev/null 2>&1 || bad "add e1"
at 2026-10-21T08:00:00Z
block "$root/e2" team-a/builder-20261021-1 estimate=3 "question=Newer, larger?"
"$DWS" decision add --from "$root/e2" >/dev/null 2>&1 || bad "add e2"
block "$root/e3" team-a/builder-20261021-2 estimate=2 "question=Overridden one?"
"$DWS" decision add --from "$root/e3" >/dev/null 2>&1 || bad "add e3"
"$DWS" decision override team-a/builder-20261021-2 --words no --ref relay:R-30 --instead other >/dev/null 2>&1 || bad "override e3"
block "$root/e4" team-a/builder-20261021-3 estimate=1 "question=Confirmed one?"
"$DWS" decision add --from "$root/e4" >/dev/null 2>&1 || bad "add e4"
"$DWS" decision confirm team-a/builder-20261021-3 --words yes --ref relay:R-31 >/dev/null 2>&1 || bad "confirm e4"
at 2026-10-22T09:00:00Z
"$DWS" decisions --unconfirmed --json >"$root/j" 2>/dev/null || true
[[ "$(q "' '.join(x['id'] for x in d['decisions'])" <"$root/j" 2>/dev/null)" == "team-a/builder-20261020-1 team-a/builder-20261021-1" ]] && ok "--unconfirmed lists decided-by-seat only, oldest first" || bad "--unconfirmed lists decided-by-seat only, oldest first" "$(q "[x['id'] for x in d['decisions']]" <"$root/j" 2>&1)"
"$DWS" decisions --json >"$root/j" 2>/dev/null || true
[[ "$(q "len(d['decisions'])" <"$root/j" 2>/dev/null)" == "4" ]] && ok "decisions --json lists every decision" || bad "decisions --json lists every decision"
"$DWS" decisions --daily --trial-start 2026-10-19 >"$root/daily" 2>"$root/e" || bad "decisions --daily runs" "$(cat "$root/e")"
grep -qxF 'Seat decisions (trial day 4 of 14): 0 new today, 4 total, 1 confirmed, 1 overridden, rework 2 seat-hours.' "$root/daily" && ok "the daily line counts from the ledger" || bad "the daily line counts from the ledger" "$(cat "$root/daily")"
grep -qxF 'Unconfirmed, most work held first:' "$root/daily" && ok "the daily line heads the unconfirmed list" || bad "the daily line heads the unconfirmed list"
grep -qxF '  team-a/builder-20261020-1  0 waiting, 2d old, 1h at risk: Older, smaller?' "$root/daily" && ok "an unconfirmed line carries waiting, age, hours at risk and the question" || bad "an unconfirmed line carries waiting, age, hours at risk and the question" "$(cat "$root/daily")"
[[ "$(grep -n 'team-a/builder-20261020-1' "$root/daily" | cut -d: -f1)" -lt "$(grep -n 'team-a/builder-20261021-1' "$root/daily" | cut -d: -f1)" ]] 2>/dev/null && ok "the daily list is oldest first when nothing waits" || bad "the daily list is oldest first when nothing waits"
"$DWS" decisions --daily >"$root/daily2" 2>/dev/null && ok "--daily without --trial-start still prints" || bad "--daily without --trial-start still prints"
grep -qF 'trial day unknown' "$root/daily2" && ok "without --trial-start the trial day is unknown, not guessed" || bad "without --trial-start the trial day is unknown, not guessed" "$(cat "$root/daily2")"
at 2026-10-22T10:00:00Z
block "$root/e5" team-a/builder-20261022-1 estimate=1
"$DWS" decision add --from "$root/e5" >/dev/null 2>&1 || bad "add e5"
"$DWS" decisions --daily --trial-start 2026-10-19 >"$root/daily" 2>/dev/null || true
grep -qF '1 new today, 5 total' "$root/daily" && ok "a decision added today counts as new today" || bad "a decision added today counts as new today" "$(cat "$root/daily")"

# an empty ledger lists nothing and does not refuse
export DIRECTOR_STATE="$root/st3"; mkdir -p "$DIRECTOR_STATE"
"$DWS" decisions --daily --trial-start 2026-10-19 >"$root/daily" 2>"$root/e" && grep -qF '0 total' "$root/daily" && ok "an empty ledger prints a zero daily line" || bad "an empty ledger prints a zero daily line" "$(cat "$root/e")"

echo "dws decisions: $pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
