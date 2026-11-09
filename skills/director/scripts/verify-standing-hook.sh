#!/usr/bin/env bash
# Prove standing-hook shows drain age and the three-line checklist, and that the
# heartbeat comes from the clock:
#   - with no heartbeat it says the age is unknown instead of inventing one;
#   - after `beat` the age reads 0m and the stamp lies between two `date -u` readings;
#   - an old heartbeat reads its age in minutes;
#   - a future stamp and a garbled file read as unknown;
#   - the checklist is printed after the age line, three numbered lines;
#   - `beat` takes no argument that could set the time;
#   - the heartbeat is replaced by rename (an old hard link keeps the old bytes);
#   - "dash last applied: T" is the last_run in dash-writer.json, sits between the checklist and the stalled-ideas
#     line, reads unknown with no file or a garbled one, and the hook never writes that file;
#   - the unlinked-asks line counts the last 24 h of asks with neither a relay entry nor a row, names the oldest,
#     prints nothing at 0, reads UNKNOWN when the join fails, times out or the relay log is unreadable.
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
printf '%s\n' "$out" | grep -q '^1\. Drained, and answered Desk cards and page picks read, since my last answer' && ok "line 1 is the drain, cards and page picks check" || bad "line 1 wording"

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
# The waiting line (item A) comes after it when present, so the stalled-ideas line is last
# otherwise: the waiting line, when present, is last; else the stalled-ideas line is.
tail1() { "$H" | grep -v '^waiting: ' | tail -n 1; }
out="$("$H")"
if printf '%s\n' "$out" | grep -q '^waiting: '; then
  [[ "$(printf '%s\n' "$out" | tail -n 1)" == "waiting: "* ]] && ok "the waiting line, when present, is last" || bad "the waiting line is not last"
fi
rm -f "$DIRECTOR_STATE/last-stalled-scan"
last="$(tail1)"
[[ "$last" == "stalled ideas: unknown, no scan recorded"* ]] && ok "no scan recorded reads as unknown" || bad "no scan recorded reads as unknown" "$last"
[[ "$("$H" | sed -n 2p)" == "Before you answer:" && "$("$H" | grep -c '^[123]\. ')" == 3 ]] && ok "the cue sits after the checklist and leaves it as it was" || bad "the cue leaves the checklist as it was"
t0="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
"$H" scan-beat
t1="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
st="$(cat "$DIRECTOR_STATE/last-stalled-scan")"
[[ ! "$st" < "$t0" && ! "$st" > "$t1" ]] && ok "the scan stamp comes from the clock" || bad "the scan stamp comes from the clock" "$t0 <= $st <= $t1"
[[ "$(tail1)" == "stalled ideas: last scan 0h ago ($st)" ]] && ok "a fresh scan reads 0h" || bad "a fresh scan reads 0h" "$(tail1)"
old="$(python3 -c 'import datetime as d;print((d.datetime.now(d.timezone.utc)-d.timedelta(hours=30,minutes=5)).strftime("%Y-%m-%dT%H:%M:%SZ"))')"
echo "$old" >"$DIRECTOR_STATE/last-stalled-scan"
[[ "$(tail1)" == "stalled ideas: last scan 30h ago ($old)" ]] && ok "an old scan reads its age in hours" || bad "an old scan reads its age in hours" "$(tail1)"
echo "garbage" >"$DIRECTOR_STATE/last-stalled-scan"
tail1 | grep -q '^stalled ideas: unknown' && ok "a garbled scan stamp reads as unknown" || bad "a garbled scan stamp reads as unknown"
if "$H" scan-beat 2000-01-01T00:00:00Z >/dev/null 2>&1; then bad "scan-beat refuses a time argument"; else ok "scan-beat refuses a time argument"; fi

# --- the dash cue: the last batch the session applied, read from dash-writer.json -------------------
rm -f "$DIRECTOR_STATE/dash-writer.json"
out="$("$H")"
[[ "$(printf '%s\n' "$out" | grep -c '^dash last applied: ')" == 1 ]] && ok "the dash line is printed once" || bad "the dash line is printed once"
[[ "$(printf '%s\n' "$out" | grep '^dash last applied: ')" == "dash last applied: unknown, no batch recorded"* ]] && ok "no dash-writer.json reads as unknown" || bad "no dash-writer.json reads as unknown"
[[ ! -e "$DIRECTOR_STATE/dash-writer.json" ]] && ok "the hook does not create dash-writer.json" || bad "the hook created dash-writer.json"
echo '{"last_run": "2026-10-22T09:30:00Z", "runs": 4, "days": []}' >"$DIRECTOR_STATE/dash-writer.json"
[[ "$("$H" | grep '^dash last applied: ')" == "dash last applied: 2026-10-22T09:30:00Z" ]] && ok "the line carries the last_run dash-writer ack recorded" || bad "the line carries last_run" "$("$H" | grep '^dash last applied: ')"
out="$("$H")"
[[ "$(printf '%s\n' "$out" | sed -n 2p)" == "Before you answer:" && "$(printf '%s\n' "$out" | grep -c '^[123]\. ')" == 3 ]] && ok "the dash line leaves the age line and the checklist as they were" || bad "the dash line moved the checklist"
n_dash="$(printf '%s\n' "$out" | grep -n '^dash last applied: ' | cut -d: -f1)"; n_chk="$(printf '%s\n' "$out" | grep -n '^3\. ' | cut -d: -f1)"; n_scan="$(printf '%s\n' "$out" | grep -n '^stalled ideas: ' | cut -d: -f1)"
[[ "$n_dash" -gt "$n_chk" && "$n_scan" -gt "$n_dash" ]] && ok "the dash line sits after the checklist and before the stalled-ideas line" || bad "the dash line order" "$n_chk $n_dash $n_scan"
echo '{not json' >"$DIRECTOR_STATE/dash-writer.json"
"$H" | grep -q '^dash last applied: unknown' && ok "a garbled dash-writer.json reads as unknown" || bad "a garbled dash-writer.json reads as unknown"
echo '{"last_run": "yesterday-ish"}' >"$DIRECTOR_STATE/dash-writer.json"
"$H" | grep -q '^dash last applied: unknown' && ok "a last_run that is not a time reads as unknown, never echoed" || bad "a bad last_run is echoed"
echo '{"runs": 2}' >"$DIRECTOR_STATE/dash-writer.json"
"$H" | grep -q '^dash last applied: unknown' && ok "a file with no last_run reads as unknown" || bad "no last_run reads as unknown"
rm -f "$DIRECTOR_STATE/dash-writer.json"

# --- the unlinked-asks line: asks of the last 24 h with neither a relay entry nor a row ----------------
# Read from `dws asks --json`, read only. It prints nothing when the count is 0, says UNKNOWN when the
# join cannot be read, and sits after the stalled-ideas line and before the waiting line.
ASKLINE='^asks in the last 24 h with no relay or row: '
export RELAY_LOG="$root/relay-log.md"
rm -f "$DIRECTOR_STATE/asks.jsonl" "$DIRECTOR_STATE/workstreams.jsonl" "$RELAY_LOG" "$RELAY_LOG.lock"
: >"$RELAY_LOG"
before="$("$H")"
! printf '%s\n' "$before" | grep -q "$ASKLINE" && ok "no asks recorded prints no asks line" || bad "no asks recorded prints no asks line"
ago() { python3 -c 'import datetime as d,sys;print((d.datetime.now(d.timezone.utc)-d.timedelta(hours=float(sys.argv[1]))).strftime("%Y-%m-%dT%H:%M:%SZ"))' "$1"; }
mkask() { printf '{"ask_id": "%s", "received_at": "%s", "text": "fixture", "source": "operator"}\n' "$1" "$(ago "$2")" >>"$DIRECTOR_STATE/asks.jsonl"; }
mkask I-1 30      # older than the window: never counted
mkask I-2 1
mkask I-3 5
mkask I-4 2       # gets a relay entry
mkask I-5 3       # gets a ledger row link
"$here/relay-log" append --to agent://x/y --ask I-4 --kind relay --text "fixture" >/dev/null
"$here/dws" open fixture-row --title fixture --link intake:I-5 >/dev/null
"$here/dws" asks --json 2>/dev/null | python3 -c 'import json,sys;r={e["ask_id"]:e for e in json.load(sys.stdin)};sys.exit(0 if r["I-4"]["relay"]!="none" and r["I-5"]["row"]!="none" else 1)' \
  || { echo "fixture is wrong: dws asks does not show the relay and row links"; exit 2; }
out="$("$H")"
[[ "$(printf '%s\n' "$out" | grep "$ASKLINE")" == "asks in the last 24 h with no relay or row: 2 (oldest I-3)" ]] && ok "counts the unlinked asks of the last 24 h and names the oldest" || bad "counts the unlinked asks" "$(printf '%s\n' "$out" | grep "$ASKLINE")"
[[ "$(printf '%s\n' "$out" | grep -c "$ASKLINE")" == 1 ]] && ok "the asks line is printed once" || bad "the asks line is printed once"
[[ "$(printf '%s\n' "$out" | sed -n 2p)" == "Before you answer:" && "$(printf '%s\n' "$out" | grep -c '^[123]\. ')" == 3 ]] && ok "the asks line leaves the age line and the checklist as they were" || bad "the asks line moved the checklist"
n_scan="$(printf '%s\n' "$out" | grep -n '^stalled ideas: ' | cut -d: -f1 || true)"; n_asks="$(printf '%s\n' "$out" | grep -n "$ASKLINE" | cut -d: -f1 || true)"
[[ -n "$n_asks" && "$n_asks" -gt "$n_scan" ]] && ok "the asks line sits after the stalled-ideas line" || bad "the asks line order" "$n_scan $n_asks"
# linking the two remaining asks takes the count to 0 and the line away
"$here/relay-log" append --to agent://x/y --ask I-2 --kind relay --text "fixture" >/dev/null
"$here/relay-log" append --to agent://x/y --ask I-3 --kind relay --text "fixture" >/dev/null
! "$H" | grep -q "$ASKLINE" && ok "all linked prints nothing, no empty line and no zero" || bad "all linked prints nothing"
[[ "$("$H" | grep -c '^$')" == "$(printf '%s\n' "$before" | grep -c '^$')" ]] && ok "and adds no blank line" || bad "blank line added"
# the oldest is by received_at, not by id order
rm -f "$RELAY_LOG" "$RELAY_LOG.lock"; : >"$RELAY_LOG"
"$H" | grep -q "no relay or row: 3 (oldest I-3)" && ok "the oldest is the earliest received, not the lowest id" || bad "oldest by received_at"
# the hook reads: it writes none of the files it looks at
sum_before="$(cksum "$DIRECTOR_STATE/asks.jsonl" "$DIRECTOR_STATE/workstreams.jsonl" "$RELAY_LOG" | tr '\n' ' ')"; "$H" >/dev/null
[[ "$sum_before" == "$(cksum "$DIRECTOR_STATE/asks.jsonl" "$DIRECTOR_STATE/workstreams.jsonl" "$RELAY_LOG" | tr '\n' ' ')" ]] && ok "the hook writes none of the stores it reads" || bad "the hook wrote a store"
# the join cannot be read: UNKNOWN, never a silent nothing
stub="$root/dws-fail"; printf '#!/bin/sh\necho boom >&2\nexit 3\n' >"$stub"; chmod +x "$stub"
STANDING_HOOK_DWS="$stub" "$H" | grep -q '^asks in the last 24 h with no relay or row: UNKNOWN (' && ok "a failing join reads as UNKNOWN" || bad "a failing join reads as UNKNOWN"
stub="$root/dws-slow"; printf '#!/bin/sh\nsleep 30\n' >"$stub"; chmod +x "$stub"
t0s="$(date +%s)"; slow="$(STANDING_HOOK_DWS="$stub" STANDING_HOOK_ASKS_TIMEOUT=1 "$H" | grep "$ASKLINE")"; t1s="$(date +%s)"
[[ "$slow" == *"UNKNOWN ("* && $((t1s - t0s)) -lt 10 ]] && ok "a slow join times out and reads as UNKNOWN" || bad "a slow join" "$slow after $((t1s - t0s))s"
stub="$root/dws-junk"; printf '#!/bin/sh\necho not-json\n' >"$stub"; chmod +x "$stub"
STANDING_HOOK_DWS="$stub" "$H" | grep -q "${ASKLINE}UNKNOWN (" && ok "a join that is not JSON reads as UNKNOWN" || bad "junk join reads as UNKNOWN"
# an unreadable relay log must not make every ask look unlinked
RELAY_LOG="$root/no-such-dir/relay-log.md" "$H" | grep -q "${ASKLINE}UNKNOWN (.*relay" && ok "an unreadable relay log reads as UNKNOWN, not as every ask unlinked" || bad "an unreadable relay log reads as UNKNOWN"
# and with no ask in the window the same unreadable log prints nothing
mv "$DIRECTOR_STATE/asks.jsonl" "$root/asks.keep"; mkask I-9 40
! RELAY_LOG="$root/no-such-dir/relay-log.md" "$H" | grep -q "$ASKLINE" && ok "no ask in the window prints nothing, whatever the relay log" || bad "no ask in the window"
mv "$root/asks.keep" "$DIRECTOR_STATE/asks.jsonl"

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
