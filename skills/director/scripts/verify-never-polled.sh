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

row() { # agent, state, connected_at, waiting, [fresh, default true], [polled_at]
  local polled=""
  [[ -n "${6:-}" ]] && polled=",\"polled_at\":\"$6\""
  printf '{"agent_id":"%s","team":"ops","role":"builder","instance":"01INSTANCE%s","state":"%s","connected_at":"%s","waiting":%s,"fresh":%s%s}' "$1" "$1" "$2" "$3" "$4" "${5:-true}" "$polled"
}
{
  printf '{"read_at":"%s","rows":[' "$NOW"
  row deaf catching-up 2026-11-18T06:49:00Z 0; printf ','
  row loaded catching-up 2026-11-18T06:40:00Z 3; printf ','
  row fresh catching-up 2026-11-18T06:55:00Z 0; printf ','
  row steady idle 2026-11-18T05:00:00Z 0; printf ','
  row heard catching-up 2026-11-18T06:30:00Z 0 true 2026-11-18T06:58:00Z; printf ','
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
printf '%s\n' "$out" | grep -q "(01INSTANCEheard)" && bad "a seat that has polled is not listed" "$out" || ok "a seat that has polled is not listed"
printf '%s\n' "$out" | grep -q 'steady' && bad "an idle seat is not listed" "$out" || ok "an idle seat is not listed"

set +e; out2="$("$S" --roster "$root/roster.json" --now "$NOW" 2>&1)"; set -e
[[ "$out" == "$out2" ]] && ok "a second run lists the same line" || bad "a second run lists the same line" "$out2"

printf '{"read_at":"%s","rows":[]}\n' "$NOW" >"$root/empty.json"
set +e; eout="$("$S" --roster "$root/empty.json" --now "$NOW" 2>&1)"; erc=$?; set -e
[[ "$erc" == 0 && -z "$eout" ]] && ok "an empty roster prints nothing" || bad "an empty roster prints nothing" "rc=$erc out=$eout"

# A roster that cannot be read is a GAP line on stdout and exit 0, never silence
# and never an error that reads as no seats (review of #685, Low 1).
set +e; gout="$("$S" --roster "$root/absent.json" --now "$NOW" 2>/dev/null)"; grc=$?; set -e
[[ "$grc" == 0 ]] && ok "an unreadable roster exits 0" || bad "an unreadable roster exits 0" "rc=$grc"
printf '%s\n' "$gout" | grep -q '^- GAP never-polled' && ok "an unreadable roster prints a GAP line" || bad "an unreadable roster prints a GAP line" "out=$gout"

# The roster subprocess has a timeout: a hung director-mcp gives a GAP line, not a hang.
cat >"$root/slow" <<'SLOW'
#!/usr/bin/env bash
sleep 6
SLOW
chmod +x "$root/slow"
t0=$SECONDS
set +e; tout="$(DIRECTOR_MCP_BIN="$root/slow" "$S" --now "$NOW" --timeout 1 2>/dev/null)"; trc=$?; set -e
el=$((SECONDS - t0))
[[ "$el" -le 4 ]] && ok "a hung roster command is cut off by --timeout" || bad "a hung roster command is cut off by --timeout" "took ${el}s"
[[ "$trc" == 0 ]] && printf '%s\n' "$tout" | grep -q '^- GAP never-polled' && ok "a timed-out roster prints a GAP line and exits 0" || bad "a timed-out roster prints a GAP line and exits 0" "rc=$trc out=$tout"

# --strict keeps a nonzero exit for a caller that tests it: the same GAP line, exit 2.
set +e; sout="$("$S" --roster "$root/absent.json" --now "$NOW" --strict 2>/dev/null)"; src=$?; set -e
[[ "$src" == 2 ]] && printf '%s\n' "$sout" | grep -q '^- GAP never-polled' && ok "--strict makes a GAP exit 2 with the same line" || bad "--strict makes a GAP exit 2 with the same line" "rc=$src out=$sout"
set +e; "$S" --roster "$root/empty.json" --now "$NOW" --strict >/dev/null 2>&1; crc=$?; set -e
[[ "$crc" == 0 ]] && ok "--strict exits 0 on a readable roster" || bad "--strict exits 0 on a readable roster" "rc=$crc"

echo "never-polled: $pass passed, $fail failed"
[[ "$fail" == 0 ]]
