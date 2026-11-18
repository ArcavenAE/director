#!/usr/bin/env bash
# Prove never-polled lists a seat that has been catching-up for more than 10
# minutes with nothing waiting, once, as "connected, never polled", and lists no
# other seat (director#222, asks-die-unread design test 7b).
#
# The roster is the JSON `director-mcp roster --json` prints. A row carries
# `state`, `connected_at` (when the shim connected) and `waiting` (messages
# waiting across the seat's sources when the row was written). The fixtures here
# are synthetic and the clock is the one passed with --now.
#
# Not tested here: a live broker's presence rows. This runs on files only.
#
# Usage: skills/director/scripts/verify-never-polled.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
S="${NEVER_POLLED_BIN:-$here/never-polled}"
[[ -x "$S" ]] || { echo "never-polled not executable at $S"; exit 2; }
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d "${TMPDIR:-/tmp}/nevpolled.XXXXXX")"
trap 'rm -rf "$root"' EXIT
NOW="2026-11-18T07:00:00Z"

row() { # agent, state, connected_at, waiting, [fresh, default true]
  printf '{"agent_id":"%s","team":"ops","role":"builder","instance":"01INSTANCE%s","state":"%s","connected_at":"%s","waiting":%s,"fresh":%s}' "$1" "$1" "$2" "$3" "$4" "${5:-true}"
}
{
  printf '{"read_at":"%s","rows":[' "$NOW"
  row deaf catching-up 2026-11-18T06:49:00Z 0; printf ','
  row loaded catching-up 2026-11-18T06:40:00Z 3; printf ','
  row fresh catching-up 2026-11-18T06:55:00Z 0; printf ','
  row steady idle 2026-11-18T05:00:00Z 0; printf ','
  row stale catching-up 2026-11-18T05:00:00Z 0 false
  printf ']}\n'
} >"$root/roster.json"

set +e; out="$("$S" --roster "$root/roster.json" --now "$NOW" 2>&1)"; rc=$?; set -e
[[ "$rc" == 0 ]] && ok "exits 0" || bad "exits 0" "rc=$rc"

n="$(printf '%s\n' "$out" | grep -c 'connected, never polled' || true)"
[[ "$n" == 1 ]] && ok "one line says connected, never polled" || bad "one line says connected, never polled" "got $n in: $out"
printf '%s\n' "$out" | grep 'connected, never polled' | grep -q 'deaf' \
  && ok "the line names the deaf seat" || bad "the line names the deaf seat" "$out"
printf '%s\n' "$out" | grep -q 'loaded' && bad "a seat with mail waiting is not listed" "$out" || ok "a seat with mail waiting is not listed"
printf '%s\n' "$out" | grep -q 'fresh' && bad "a seat under 10 minutes is not listed" "$out" || ok "a seat under 10 minutes is not listed"
printf '%s\n' "$out" | grep -q 'stale' && bad "a stale row is not listed" "$out" || ok "a stale row is not listed"
printf '%s\n' "$out" | grep -q 'steady' && bad "an idle seat is not listed" "$out" || ok "an idle seat is not listed"

set +e; out2="$("$S" --roster "$root/roster.json" --now "$NOW" 2>&1)"; set -e
[[ "$out" == "$out2" ]] && ok "a second run lists the same line" || bad "a second run lists the same line" "$out2"

printf '{"read_at":"%s","rows":[]}\n' "$NOW" >"$root/empty.json"
set +e; eout="$("$S" --roster "$root/empty.json" --now "$NOW" 2>&1)"; erc=$?; set -e
[[ "$erc" == 0 && -z "$eout" ]] && ok "an empty roster prints nothing" || bad "an empty roster prints nothing" "rc=$erc out=$eout"

echo "never-polled: $pass passed, $fail failed"
[[ "$fail" == 0 ]]
