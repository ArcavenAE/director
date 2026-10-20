#!/usr/bin/env bash
# Prove standing-hook shows drain age and the three-line checklist, and that the
# heartbeat comes from the clock:
#   - with no heartbeat it says the age is unknown instead of inventing one;
#   - after `beat` the age reads 0m and the stamp lies between two `date -u` readings;
#   - an old heartbeat reads its age in minutes;
#   - a future stamp and a garbled file read as unknown;
#   - the checklist is printed after the age line, three numbered lines;
#   - `beat` takes no argument that could set the time;
#   - the heartbeat is replaced by rename (an old hard link keeps the old bytes).
#
# Usage: skills/director/scripts/verify-standing-hook.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
H="${STANDING_HOOK_BIN:-$here/standing-hook}"
[[ -x "$H" ]] || { echo "standing-hook not executable at $H"; exit 2; }
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
export DIRECTOR_STATE="$root/state"
export HOME="$root/home"; mkdir -p "$HOME"

out="$("$H")"
first="$(printf '%s\n' "$out" | sed -n 1p)"
[[ "$first" == "drain age: unknown, no drain recorded"* ]] && ok "no heartbeat reads as unknown" || bad "no heartbeat reads as unknown" "$first"

t0="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
"$H" beat
t1="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
st="$(cat "$DIRECTOR_STATE/last-drain")"
[[ ! "$st" < "$t0" && ! "$st" > "$t1" ]] && ok "the heartbeat stamp comes from the clock" || bad "the heartbeat stamp comes from the clock" "$t0 <= $st <= $t1"
first="$("$H" | sed -n 1p)"
[[ "$first" == "drain age: 0m since the last drain ($st)" ]] && ok "a fresh heartbeat reads 0m" || bad "a fresh heartbeat reads 0m" "$first"

old="$(python3 -c 'import datetime as d;print((d.datetime.now(d.timezone.utc)-d.timedelta(minutes=150,seconds=30)).strftime("%Y-%m-%dT%H:%M:%SZ"))')"
echo "$old" >"$DIRECTOR_STATE/last-drain"
first="$("$H" | sed -n 1p)"
[[ "$first" == "drain age: 150m since the last drain ($old)" ]] && ok "an old heartbeat reads its age in minutes" || bad "an old heartbeat reads its age" "$first"

echo "2999-01-01T00:00:00Z" >"$DIRECTOR_STATE/last-drain"
"$H" | sed -n 1p | grep -q 'unknown, the last drain stamp' && ok "a future stamp reads as unknown" || bad "a future stamp reads as unknown"
echo "garbage" >"$DIRECTOR_STATE/last-drain"
"$H" | sed -n 1p | grep -q '^drain age: unknown' && ok "a garbled file reads as unknown" || bad "a garbled file reads as unknown"

out="$("$H")"
[[ "$(printf '%s\n' "$out" | sed -n 2p)" == "Before you answer:" ]] && ok "the checklist follows the age line" || bad "the checklist follows the age line"
[[ "$(printf '%s\n' "$out" | grep -c '^[123]\. ')" == 3 ]] && ok "the checklist has three numbered lines" || bad "the checklist has three numbered lines"
printf '%s\n' "$out" | grep -q '^1\. Drained, and answered Desk cards read, since my last answer' && ok "line 1 is the drain-and-cards check" || bad "line 1 wording"

if "$H" beat 2000-01-01T00:00:00Z >/dev/null 2>&1; then bad "beat refuses a time argument"; else ok "beat refuses a time argument"; fi
if "$H" bogus >/dev/null 2>&1; then bad "an unknown subcommand is refused"; else ok "an unknown subcommand is refused"; fi

"$H" beat
ln "$DIRECTOR_STATE/last-drain" "$root/link"
cp "$root/link" "$root/link.bytes"
sleep 1; "$H" beat
cmp -s "$root/link" "$root/link.bytes" && ok "beat replaces by rename (the old inode keeps the old bytes)" || bad "beat replaces by rename"

# --- the default state path, with DIRECTOR_STATE unset --------------------------------
( unset DIRECTOR_STATE; "$H" beat )
[[ -s "$HOME/.director/state/last-drain" ]] && ok "with DIRECTOR_STATE unset the heartbeat lands under \$HOME/.director/state" || bad "the default state path"
first="$(unset DIRECTOR_STATE; "$H" | sed -n 1p)"
[[ "$first" == "drain age: 0m since the last drain ("* ]] && ok "and the hook reads it back from there" || bad "the hook reads the default path" "$first"

# --- the stalled-ideas cue (ld16k): a read of one file, never a scan ----------------------
rm -f "$DIRECTOR_STATE/last-stalled-scan"
last="$("$H" | tail -n 1)"
[[ "$last" == "stalled ideas: unknown, no scan recorded"* ]] && ok "no scan recorded reads as unknown" || bad "no scan recorded reads as unknown" "$last"
[[ "$("$H" | sed -n 2p)" == "Before you answer:" && "$("$H" | grep -c '^[123]\. ')" == 3 ]] && ok "the cue sits after the checklist and leaves it as it was" || bad "the cue leaves the checklist as it was"
t0="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
"$H" scan-beat
t1="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
st="$(cat "$DIRECTOR_STATE/last-stalled-scan")"
[[ ! "$st" < "$t0" && ! "$st" > "$t1" ]] && ok "the scan stamp comes from the clock" || bad "the scan stamp comes from the clock" "$t0 <= $st <= $t1"
[[ "$("$H" | tail -n 1)" == "stalled ideas: last scan 0h ago ($st)" ]] && ok "a fresh scan reads 0h" || bad "a fresh scan reads 0h" "$("$H" | tail -n 1)"
old="$(python3 -c 'import datetime as d;print((d.datetime.now(d.timezone.utc)-d.timedelta(hours=30,minutes=5)).strftime("%Y-%m-%dT%H:%M:%SZ"))')"
echo "$old" >"$DIRECTOR_STATE/last-stalled-scan"
[[ "$("$H" | tail -n 1)" == "stalled ideas: last scan 30h ago ($old)" ]] && ok "an old scan reads its age in hours" || bad "an old scan reads its age in hours" "$("$H" | tail -n 1)"
echo "garbage" >"$DIRECTOR_STATE/last-stalled-scan"
"$H" | tail -n 1 | grep -q '^stalled ideas: unknown' && ok "a garbled scan stamp reads as unknown" || bad "a garbled scan stamp reads as unknown"
if "$H" scan-beat 2000-01-01T00:00:00Z >/dev/null 2>&1; then bad "scan-beat refuses a time argument"; else ok "scan-beat refuses a time argument"; fi

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
