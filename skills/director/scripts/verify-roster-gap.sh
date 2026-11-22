#!/usr/bin/env bash
# Prove `roster-gap` (sim/design/roster-gap.md): the sweep line for a seat up in marvel and absent from the roster.
# It reads a fixture shaped like `marvel get sessions --all-clusters -o json` and fixtures shaped like
# `director-mcp roster --json`. Checks:
#   - the join key (team, session Name against roster team, agent_id): a seat with a row prints nothing, a seat
#     without one prints one line, whatever its workspace or cluster;
#   - only running claude seats older than 10 minutes count; a failed seat, a non-claude seat and a young seat do not;
#   - a seat with no CreatedAt prints with "age unknown", never dropped;
#   - the pane text "no MCP server configured with that name" gives "no director MCP configured"; a pane read
#     without it gives plain "no roster row"; no pane program gives "reason unknown";
#   - a cluster that did not answer prints a GAP line and its seats are not claimed as missing; an unreadable
#     sessions file or roster prints a GAP and no seat line;
#   - a per-cluster roster is used for that cluster only, and a cluster with none prints a GAP.
# Every run is under `env -i` with a scratch HOME. Fixture values are placeholders. Nothing is deleted. Written for
# bash 3.2 and bash 5.
#
# Usage: skills/director/scripts/verify-roster-gap.sh   Exit nonzero on any miss.
set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
R="${ROSTER_GAP:-$here/roster-gap}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }
[[ -f "$R" ]] || { echo "FAIL roster-gap exists at $R"; echo "0 passed, 1 failed"; exit 1; }

root="$(mktemp -d "${ROSTER_GAP_TMP:-${TMPDIR:-/tmp}}/rgap.XXXXXX")" || { echo "FAIL scratch root"; exit 1; }
root="$(cd "$root" && pwd -P)"
mkdir -p "$root/home" "$root/bin"
PYDIR="$(dirname "$(command -v python3)")"
NOW="2030-01-10T12:00:00Z"

# a pane program: prints the text in $root/pane/<name>, exits 1 when there is none
mkdir -p "$root/pane"
printf '#!/bin/sh\nf="%s/pane/$2"\n[ -f "$f" ] || exit 1\ncat "$f"\n' "$root" >"$root/bin/panecmd"
chmod +x "$root/bin/panecmd"

cat >"$root/sessions.json" <<'JSON'
{"clusters":[{"cluster":"alpha","outcome":"answered","as_of":"2030-01-10T12:00:00Z"},
             {"cluster":"beta","outcome":"answered","as_of":"2030-01-10T12:00:00Z"},
             {"cluster":"gamma","outcome":"no_answer_in_time","as_of":"2030-01-10T12:00:00Z"}],
 "sessions":[
  {"cluster":"alpha","Name":"t1-builder-g1-0","Workspace":"w","Team":"t1","State":"running","CreatedAt":"2030-01-10T09:00:00Z","Runtime":{"Name":"claude","Command":"claude"}},
  {"cluster":"alpha","Name":"t1-lost-g1-0","Workspace":"w","Team":"t1","State":"running","CreatedAt":"2030-01-10T09:00:00Z","Runtime":{"Name":"claude","Command":"/usr/local/bin/claude"}},
  {"cluster":"alpha","Name":"t1-young-g1-0","Workspace":"w","Team":"t1","State":"running","CreatedAt":"2030-01-10T11:55:00Z","Runtime":{"Name":"claude","Command":"claude"}},
  {"cluster":"alpha","Name":"t1-failed-g1-0","Workspace":"w","Team":"t1","State":"failed","CreatedAt":"2030-01-10T09:00:00Z","Runtime":{"Name":"claude","Command":"claude"}},
  {"cluster":"alpha","Name":"t1-codex-g1-0","Workspace":"w","Team":"t1","State":"running","CreatedAt":"2030-01-10T09:00:00Z","Runtime":{"Name":"codex","Command":"codex"}},
  {"cluster":"alpha","Name":"t1-nodate-g1-0","Workspace":"w","Team":"t1","State":"running","CreatedAt":"0001-01-01T00:00:00Z","Runtime":{"Name":"claude","Command":"claude"}},
  {"cluster":"alpha","Name":"t2-builder-g1-0","Workspace":"w","Team":"t2","State":"running","CreatedAt":"2030-01-10T09:00:00Z","Runtime":{"Name":"claude","Command":"claude"}},
  {"cluster":"beta","Name":"t3-builder-g1-0","Workspace":"w","Team":"t3","State":"running","CreatedAt":"2030-01-10T08:00:00Z","Runtime":{"Name":"claude","Command":"claude"}},
  {"cluster":"gamma","Name":"t4-builder-g1-0","Workspace":"w","Team":"t4","State":"running","CreatedAt":"2030-01-10T08:00:00Z","Runtime":{"Name":"claude","Command":"claude"}}]}
JSON
# the roster has t1-builder, and t2-builder under the wrong team (the join needs both), and t3-builder
cat >"$root/roster.json" <<'JSON'
{"read_at":"2030-01-10T12:00:00Z","rows":[
 {"agent_id":"t1-builder-g1-0","team":"t1","role":"builder","instance":"i1","age_seconds":3,"fresh":true},
 {"agent_id":"t2-builder-g1-0","team":"other","role":"builder","instance":"i2","age_seconds":3,"fresh":true},
 {"agent_id":"t3-builder-g1-0","team":"t3","role":"builder","instance":"i3","age_seconds":3,"fresh":true}]}
JSON
printf '{"read_at":"2030-01-10T12:00:00Z","rows":[{"agent_id":"t3-builder-g1-0","team":"t3","instance":"i3","age_seconds":1,"fresh":true}]}\n' >"$root/roster-beta.json"
printf '{"read_at":"2030-01-10T12:00:00Z","rows":[{"agent_id":"t1-builder-g1-0","team":"t1","instance":"i1","age_seconds":1,"fresh":true}]}\n' >"$root/roster-alpha.json"

run() { # run [env VAR=...] -- args
  local extra=()
  while [[ "${1:-}" != "--" && $# -gt 0 ]]; do extra+=("$1"); shift; done
  shift
  env -i HOME="$root/home" PATH="$root/bin:$PYDIR:/usr/bin:/bin" ${extra[@]+"${extra[@]}"} python3 -I "$R" --now "$NOW" "$@" >"$root/out" 2>"$root/err"
  rc=$?
}
has() { grep -qF -- "$1" "$root/out"; }
hasnt() { ! grep -qF -- "$1" "$root/out"; }

# --- the join and the filters ------------------------------------------------------------------------------------------
run -- --sessions "$root/sessions.json" --roster "$root/roster.json"
[[ $rc -eq 0 ]] && ok "exit 0" || bad "exit" "rc=$rc"
hasnt "t1-builder-g1-0" && ok "a seat with a roster row on (team, name) prints nothing" || bad "joined seat printed" "$(cat "$root/out")"
has "roster-gap: alpha/t1-lost-g1-0 team t1 running 180 min, no roster row, reason unknown" && ok "a seat with no row prints one line with cluster, seat, team and minutes" || bad "lost seat" "$(cat "$root/out")"
has "alpha/t2-builder-g1-0" && ok "the same name under another team does not join (the key is team and name)" || bad "team in the key" "$(cat "$root/out")"
hasnt "t1-young-g1-0" && ok "a seat up 5 minutes is not listed" || bad "young seat listed"
hasnt "t1-failed-g1-0" && ok "a failed seat is not listed" || bad "failed seat listed"
hasnt "t1-codex-g1-0" && ok "a non-claude seat is not listed" || bad "codex seat listed"
has "alpha/t1-nodate-g1-0 team t1 running age unknown" && ok "a seat with no readable CreatedAt prints with age unknown, never dropped" || bad "no date" "$(cat "$root/out")"
hasnt "t3-builder-g1-0" && ok "a seat on another cluster joins a shared roster by team and name" || bad "beta joined" "$(cat "$root/out")"
has "roster-gap: GAP cluster gamma (no_answer_in_time)" && ok "a cluster that did not answer prints a GAP line" || bad "gap line" "$(cat "$root/out")"
hasnt "t4-builder-g1-0" && ok "a seat of an unanswered cluster is not claimed missing" || bad "gamma seat claimed"
has "roster-gap: checked 5 claude seat(s) past 10 min, 3 without a roster row, 1 cluster gap(s)" && ok "the last line counts the seats checked, the misses and the gaps" || bad "summary" "$(tail -1 "$root/out")"

# --- why there is no row ---------------------------------------------------------------------------------------------------
printf 'Error: no MCP server configured with that name\n' >"$root/pane/t1-lost-g1-0"
printf 'a normal prompt\n' >"$root/pane/t2-builder-g1-0"
run ROSTER_GAP_PANE="$root/bin/panecmd" -- --sessions "$root/sessions.json" --roster "$root/roster.json"
has "alpha/t1-lost-g1-0 team t1 running 180 min, no director MCP configured" && ok "the pane text gives the class no director MCP configured" || bad "no mcp class" "$(cat "$root/out")"
has "alpha/t2-builder-g1-0 team t2 running 180 min, no roster row" && ! grep -F "t2-builder-g1-0" "$root/out" | grep -qF "unknown" && ok "a pane read without the text is a plain missed join" || bad "plain missed join" "$(cat "$root/out")"
grep -F "t1-nodate-g1-0" "$root/out" | grep -qF "reason unknown" && ok "a pane that cannot be read leaves the reason unknown" || bad "unreadable pane" "$(cat "$root/out")"

# --- gaps never read as zero ---------------------------------------------------------------------------------------------
run -- --sessions "$root/none.json" --roster "$root/roster.json"
[[ "$(wc -l <"$root/out" | tr -d ' ')" == 1 ]] && has "roster-gap: GAP sessions (could not run or read)" && ok "an unreadable sessions file prints one GAP and no seat line" || bad "sessions gap" "$(cat "$root/out")"
printf 'not json\n' >"$root/bad.json"
run -- --sessions "$root/sessions.json" --roster "$root/bad.json"
has "roster-gap: GAP roster all clusters (not JSON)" && hasnt "roster-gap: alpha" && ok "an unreadable roster prints a GAP and claims no seat is missing" || bad "roster gap" "$(cat "$root/out")"
printf '{"nope":1}\n' >"$root/norows.json"
run -- --sessions "$root/sessions.json" --roster "$root/norows.json"
has "roster-gap: GAP roster all clusters (no rows list)" && ok "a roster with no rows list is a GAP" || bad "no rows" "$(cat "$root/out")"
run -- --sessions "$root/sessions.json"
has "roster-gap: GAP roster (" && ok "no roster file and no director-mcp is a GAP, not zero seats" || bad "no director-mcp" "$(cat "$root/out")"

# --- per-cluster rosters -----------------------------------------------------------------------------------------------------
run -- --sessions "$root/sessions.json" --roster "alpha=$root/roster-alpha.json" --roster "beta=$root/roster-beta.json"
has "alpha/t2-builder-g1-0" && hasnt "beta/" && ok "per-cluster rosters are used for their own cluster" || bad "per cluster" "$(cat "$root/out")"
run -- --sessions "$root/sessions.json" --roster "alpha=$root/roster-alpha.json" --roster "beta=$root/bad.json"
has "roster-gap: GAP roster beta (not JSON)" && has "alpha/t2-builder-g1-0" && hasnt "beta/t3-builder-g1-0" && ok "one unreadable per-cluster roster prints a GAP for that cluster and the other clusters still print" || bad "per-cluster unreadable" "$(cat "$root/out")"
has "roster-gap: checked 4 claude seat(s) past 10 min, 3 without a roster row, 2 cluster gap(s)" && ok "the unreadable cluster counts as a gap and its seats are not checked" || bad "per-cluster summary" "$(tail -1 "$root/out")"
sed 's/"cluster":"beta","outcome":"answered"/"cluster":"beta","outcome":"starting"/' "$root/sessions.json" >"$root/sessions-starting.json"
run -- --sessions "$root/sessions-starting.json" --roster "$root/roster.json"
has "roster-gap: NOTE cluster beta is starting, its rows may be incomplete" && ok "a starting cluster prints a NOTE" || bad "starting note" "$(cat "$root/out")"
run -- --sessions "$root/sessions.json" --roster "alpha=$root/roster-alpha.json"
has "roster-gap: GAP cluster beta (no roster given for it)" && ok "a cluster with no roster given prints a GAP when others have one" || bad "no roster for beta" "$(cat "$root/out")"
run -- --sessions "$root/sessions.json" --roster "$root/roster.json" --min-minutes 200
hasnt "t1-lost-g1-0" && ok "--min-minutes raises the age floor" || bad "min minutes" "$(cat "$root/out")"

# --- it only reads -----------------------------------------------------------------------------------------------------------
before="$(cd "$root" && find . -type f ! -name out ! -name err | LC_ALL=C sort | xargs shasum | shasum | cut -c1-16)"
run -- --sessions "$root/sessions.json" --roster "$root/roster.json"
after="$(cd "$root" && find . -type f ! -name out ! -name err | LC_ALL=C sort | xargs shasum | shasum | cut -c1-16)"
[[ "$before" == "$after" ]] && ok "no file under the scratch tree changed" || bad "a file changed"
run -- --bogus x; [[ $rc -eq 2 ]] && ok "an unknown option is exit 2" || bad "usage" "rc=$rc"
run -- --min-minutes x; [[ $rc -eq 2 ]] && ok "a bad --min-minutes is exit 2" || bad "min-minutes" "rc=$rc"

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
