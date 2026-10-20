#!/usr/bin/env bash
# Prove rings.jsonl (K14, sim/design/director-functions-redesign.md, Q6 and R-200):
# a ring is recorded per seat with its first turn, its ack and its blocker; a ring
# needs 10 minutes idle with mail; at most 3 rings per seat per hour, then the
# seat is reported as stuck. The store follows the dws shape: append-only, folded
# on read, one flock around fold, check and append, a torn line counted not repaired.
#
# Usage: skills/director/scripts/verify-dws-rings.sh   Exit nonzero on any miss.
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
RINGS="$DIRECTOR_STATE/rings.jsonl"
at() { export DWS_NOW="$1"; }
lines() { if [[ -f "$RINGS" ]]; then wc -l <"$RINGS" | tr -d ' '; else echo 0; fi; }
q() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
refuses() {  # LABEL WORD cmd...
  local label="$1" word="$2"; shift 2; local before; before="$(lines)"
  if "$@" >"$root/o" 2>"$root/e"; then bad "$label" "accepted"
  elif ! grep -qiF -- "$word" "$root/e"; then bad "$label" "refused without '$word': $(cat "$root/e")"
  elif [[ "$(lines)" != "$before" ]]; then bad "$label" "wrote anyway"
  else ok "$label"; fi
}

at 2026-10-20T12:00:00Z
refuses "a ring needs 10 minutes idle" "idle" "$DWS" ring team-a/reviewer --idle-minutes 9 --mail 2
refuses "a ring needs mail waiting" "mail" "$DWS" ring team-a/reviewer --idle-minutes 30 --mail 0
refuses "a ring refuses an instance id" "instance id" "$DWS" ring reviewer-g9-9 --idle-minutes 30 --mail 1
refuses "only director may ring" "actor" "$DWS" --actor refresh ring team-a/reviewer --idle-minutes 30 --mail 1
"$DWS" ring team-a/reviewer --idle-minutes 10 --mail 2 >/dev/null 2>"$root/e" && ok "a ring at exactly 10 minutes with mail is recorded" || bad "a ring at exactly 10 minutes with mail is recorded" "$(cat "$root/e")"
[[ "$(lines)" == "1" ]] && ok "one line appended" || bad "one line appended"

refuses "a follow-up needs a ring to follow" "no ring" "$DWS" ring-note team-b/builder ack
refuses "a blocker needs its words" "text" "$DWS" ring-note team-a/reviewer blocker
refuses "an unknown follow-up kind is refused" "invalid choice" "$DWS" ring-note team-a/reviewer shrug

"$DWS" rings --json >"$root/j" 2>/dev/null
[[ "$(q "d['seats']['team-a/reviewer']['status']" <"$root/j")" == "rung" ]] && ok "status rung after a ring" || bad "status rung after a ring"

at 2026-10-20T12:03:00Z
"$DWS" ring-note team-a/reviewer first-turn >/dev/null 2>&1 && ok "first turn recorded" || bad "first turn recorded"
"$DWS" rings --json >"$root/j"; [[ "$(q "d['seats']['team-a/reviewer']['status']" <"$root/j")" == "turned" ]] && ok "status turned" || bad "status turned"
at 2026-10-20T12:04:00Z
"$DWS" ring-note team-a/reviewer blocker --text 'permission denied: "gh pr review" was refused by the classifier' >/dev/null 2>&1 && ok "blocker recorded" || bad "blocker recorded"
"$DWS" rings --json >"$root/j"
[[ "$(q "d['seats']['team-a/reviewer']['status']" <"$root/j")" == "blocked" ]] && ok "status blocked" || bad "status blocked"
[[ "$(q "d['seats']['team-a/reviewer']['blocker']" <"$root/j")" == 'permission denied: "gh pr review" was refused by the classifier' ]] && ok "the blocker text is stored as given" || bad "the blocker text is stored as given"
at 2026-10-20T12:05:00Z
"$DWS" ring-note team-a/reviewer ack >/dev/null 2>&1 && ok "ack recorded" || bad "ack recorded"
"$DWS" rings --json >"$root/j"; [[ "$(q "d['seats']['team-a/reviewer']['status']" <"$root/j")" == "acked" ]] && ok "status acked" || bad "status acked"

# --- the cap: 3 rings per seat per hour, then stuck ---------------------------------
at 2026-10-20T12:20:00Z
"$DWS" ring team-a/reviewer --idle-minutes 15 --mail 1 >/dev/null 2>&1 && ok "second ring" || bad "second ring"
at 2026-10-20T12:40:00Z
"$DWS" ring team-a/reviewer --idle-minutes 15 --mail 1 >/dev/null 2>&1 && ok "third ring" || bad "third ring"
at 2026-10-20T12:50:00Z
refuses "a fourth ring in the hour is refused and the seat is reported stuck" "stuck" "$DWS" ring team-a/reviewer --idle-minutes 15 --mail 1
"$DWS" rings --json >"$root/j"
[[ "$(q "d['seats']['team-a/reviewer']['status']" <"$root/j")" == "stuck" ]] && ok "status stuck at the cap with no ack since the last ring" || bad "status stuck at the cap with no ack since the last ring"
[[ "$(q "d['seats']['team-a/reviewer']['rings_in_window']" <"$root/j")" == "3" ]] && ok "three rings in the window" || bad "three rings in the window"
"$DWS" ring team-b/builder --idle-minutes 15 --mail 1 >/dev/null 2>&1 && ok "the cap is per seat" || bad "the cap is per seat"
at 2026-10-20T13:11:00Z
"$DWS" ring team-a/reviewer --idle-minutes 15 --mail 1 >/dev/null 2>&1 && ok "a ring is allowed once the first has left the hour" || bad "a ring is allowed once the first has left the hour"

# --- the store shape -----------------------------------------------------------------
printf '%s\n' '{"ts":"2026-10-20T13:12:00Z","ev' >>"$RINGS"
"$DWS" rings --json >"$root/j" 2>"$root/e"; [[ "$(q "d['torn']" <"$root/j")" == "1" ]] && grep -q 'not repaired' "$root/e" && ok "a torn line is counted, not repaired" || bad "a torn line is counted, not repaired"
"$DWS" rings >"$root/t" 2>/dev/null; grep -q 'team-a/reviewer' "$root/t" && ok "text output lists the seat" || bad "text output lists the seat"
before="$(lines)"
for i in 1 2 3 4 5 6; do (at 2026-10-21T0${i}:00:00Z; "$DWS" ring "par/seat$i" --idle-minutes 20 --mail 1 >/dev/null 2>&1) & done; wait
[[ "$(( $(lines) - before ))" == "6" ]] && ok "concurrent rings all land" || bad "concurrent rings all land"
[[ "$(python3 -c "
import json
n=0
for l in open('$RINGS'):
    try: json.loads(l)
    except ValueError: n+=1
print(n)")" == "1" ]] && ok "only the planted torn line is unreadable" || bad "only the planted torn line is unreadable"

# --- reconcile reads it ---------------------------------------------------------------
at 2026-10-21T12:00:00Z
"$DWS" reconcile --json >"$root/r" 2>/dev/null; [[ "$(q "d['sources']['rings']['state']" <"$root/r")" == "read" ]] && ok "reconcile reads rings.jsonl" || bad "reconcile reads rings.jsonl"

# a rings file with no readable line is UNREAD to reconcile (as for the other stores)
mkdir -p "$root/st2"; printf 'junk\n{broken\n' >"$root/st2/rings.jsonl"
DIRECTOR_STATE="$root/st2" "$DWS" reconcile --json >"$root/r2" 2>/dev/null
[[ "$(q "d['sources']['rings']['state']" <"$root/r2")" == "UNREAD" ]] && ok "reconcile reads a malformed rings file as UNREAD" || bad "reconcile reads a malformed rings file as UNREAD"

echo "dws rings: $pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
