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

# The hook now prints the local clock first (verify-local-times.sh); these checks read what follows it.
hook() { "$H" "$@" | sed 1d; }

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
export DIRECTOR_STATE="$root/state"
export HOME="$root/home"; mkdir -p "$HOME"

out="$(hook)"
first="$(printf '%s\n' "$out" | sed -n 1p)"
[[ "$first" == "drain age: unknown, no drain recorded"* ]] && ok "no heartbeat reads as unknown" || bad "no heartbeat reads as unknown" "$first"

t0="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
"$H" beat
t1="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
st="$(cat "$DIRECTOR_STATE/last-drain")"
[[ ! "$st" < "$t0" && ! "$st" > "$t1" ]] && ok "the heartbeat stamp comes from the clock" || bad "the heartbeat stamp comes from the clock" "$t0 <= $st <= $t1"
first="$(hook | sed -n 1p)"
[[ "$first" == "drain age: 0m since the last drain ($st)" ]] && ok "a fresh heartbeat reads 0m" || bad "a fresh heartbeat reads 0m" "$first"

old="$(python3 -c 'import datetime as d;print((d.datetime.now(d.timezone.utc)-d.timedelta(minutes=150,seconds=30)).strftime("%Y-%m-%dT%H:%M:%SZ"))')"
echo "$old" >"$DIRECTOR_STATE/last-drain"
first="$(hook | sed -n 1p)"
[[ "$first" == "drain age: 150m since the last drain ($old)" ]] && ok "an old heartbeat reads its age in minutes" || bad "an old heartbeat reads its age" "$first"

echo "2999-01-01T00:00:00Z" >"$DIRECTOR_STATE/last-drain"
hook | sed -n 1p | grep -q 'unknown, the last drain stamp' && ok "a future stamp reads as unknown" || bad "a future stamp reads as unknown"
echo "garbage" >"$DIRECTOR_STATE/last-drain"
hook | sed -n 1p | grep -q '^drain age: unknown' && ok "a garbled file reads as unknown" || bad "a garbled file reads as unknown"

out="$(hook)"
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
first="$(unset DIRECTOR_STATE; hook | sed -n 1p)"
[[ "$first" == "drain age: 0m since the last drain ("* ]] && ok "and the hook reads it back from there" || bad "the hook reads the default path" "$first"

# --- the stalled-ideas cue (ld16k): a read of one file, never a scan ----------------------
# The waiting line (item A) comes after it when present, so the stalled-ideas line is last
# otherwise: the waiting line, when present, is last; else the stalled-ideas line is.
tail1() { hook | grep -v '^waiting: ' | tail -n 1; }
out="$(hook)"
if printf '%s\n' "$out" | grep -q '^waiting: '; then
  [[ "$(printf '%s\n' "$out" | tail -n 1)" == "waiting: "* ]] && ok "the waiting line, when present, is last" || bad "the waiting line is not last"
fi
rm -f "$DIRECTOR_STATE/last-stalled-scan"
last="$(tail1)"
[[ "$last" == "stalled ideas: unknown, no scan recorded"* ]] && ok "no scan recorded reads as unknown" || bad "no scan recorded reads as unknown" "$last"
[[ "$(hook | sed -n 2p)" == "Before you answer:" && "$(hook | grep -c '^[123]\. ')" == 3 ]] && ok "the cue sits after the checklist and leaves it as it was" || bad "the cue leaves the checklist as it was"
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
out="$(hook)"
[[ "$(printf '%s\n' "$out" | grep -c '^dash last applied: ')" == 1 ]] && ok "the dash line is printed once" || bad "the dash line is printed once"
[[ "$(printf '%s\n' "$out" | grep '^dash last applied: ')" == "dash last applied: unknown, no batch recorded"* ]] && ok "no dash-writer.json reads as unknown" || bad "no dash-writer.json reads as unknown"
[[ ! -e "$DIRECTOR_STATE/dash-writer.json" ]] && ok "the hook does not create dash-writer.json" || bad "the hook created dash-writer.json"
echo '{"last_run": "2026-10-22T09:30:00Z", "runs": 4, "days": []}' >"$DIRECTOR_STATE/dash-writer.json"
[[ "$(hook | grep '^dash last applied: ')" == "dash last applied: 2026-10-22T09:30:00Z" ]] && ok "the line carries the last_run dash-writer ack recorded" || bad "the line carries last_run" "$(hook | grep '^dash last applied: ')"
out="$(hook)"
[[ "$(printf '%s\n' "$out" | sed -n 2p)" == "Before you answer:" && "$(printf '%s\n' "$out" | grep -c '^[123]\. ')" == 3 ]] && ok "the dash line leaves the age line and the checklist as they were" || bad "the dash line moved the checklist"
n_dash="$(printf '%s\n' "$out" | grep -n '^dash last applied: ' | cut -d: -f1)"; n_chk="$(printf '%s\n' "$out" | grep -n '^3\. ' | cut -d: -f1)"; n_scan="$(printf '%s\n' "$out" | grep -n '^stalled ideas: ' | cut -d: -f1)"
[[ "$n_dash" -gt "$n_chk" && "$n_scan" -gt "$n_dash" ]] && ok "the dash line sits after the checklist and before the stalled-ideas line" || bad "the dash line order" "$n_chk $n_dash $n_scan"
echo '{not json' >"$DIRECTOR_STATE/dash-writer.json"
hook | grep -q '^dash last applied: unknown' && ok "a garbled dash-writer.json reads as unknown" || bad "a garbled dash-writer.json reads as unknown"
echo '{"last_run": "yesterday-ish"}' >"$DIRECTOR_STATE/dash-writer.json"
hook | grep -q '^dash last applied: unknown' && ok "a last_run that is not a time reads as unknown, never echoed" || bad "a bad last_run is echoed"
echo '{"runs": 2}' >"$DIRECTOR_STATE/dash-writer.json"
hook | grep -q '^dash last applied: unknown' && ok "a file with no last_run reads as unknown" || bad "no last_run reads as unknown"
rm -f "$DIRECTOR_STATE/dash-writer.json"

# --- the unlinked-asks line: asks of the last 24 h with neither a relay entry nor a row ----------------
# Read from `dws asks --json`, read only. It prints nothing when the count is 0, says UNKNOWN when the
# join cannot be read, and sits after the stalled-ideas line and before the waiting line.
ASKLINE='^asks in the last 24 h with no relay or row: '
export RELAY_LOG="$root/relay-log.md"
rm -f "$DIRECTOR_STATE/asks.jsonl" "$DIRECTOR_STATE/workstreams.jsonl" "$RELAY_LOG" "$RELAY_LOG.lock"
: >"$RELAY_LOG"
before="$(hook)"
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
out="$(hook)"
[[ "$(printf '%s\n' "$out" | grep "$ASKLINE")" == "asks in the last 24 h with no relay or row: 2 (oldest I-3)" ]] && ok "counts the unlinked asks of the last 24 h and names the oldest" || bad "counts the unlinked asks" "$(printf '%s\n' "$out" | grep "$ASKLINE")"
[[ "$(printf '%s\n' "$out" | grep -c "$ASKLINE")" == 1 ]] && ok "the asks line is printed once" || bad "the asks line is printed once"
[[ "$(printf '%s\n' "$out" | sed -n 2p)" == "Before you answer:" && "$(printf '%s\n' "$out" | grep -c '^[123]\. ')" == 3 ]] && ok "the asks line leaves the age line and the checklist as they were" || bad "the asks line moved the checklist"
n_scan="$(printf '%s\n' "$out" | grep -n '^stalled ideas: ' | cut -d: -f1 || true)"; n_asks="$(printf '%s\n' "$out" | grep -n "$ASKLINE" | cut -d: -f1 || true)"
[[ -n "$n_asks" && "$n_asks" -gt "$n_scan" ]] && ok "the asks line sits after the stalled-ideas line" || bad "the asks line order" "$n_scan $n_asks"
# linking the two remaining asks takes the count to 0 and the line away
"$here/relay-log" append --to agent://x/y --ask I-2 --kind relay --text "fixture" >/dev/null
"$here/relay-log" append --to agent://x/y --ask I-3 --kind relay --text "fixture" >/dev/null
! hook | grep -q "$ASKLINE" && ok "all linked prints nothing, no empty line and no zero" || bad "all linked prints nothing"
[[ "$(hook | grep -c '^$')" == "$(printf '%s\n' "$before" | grep -c '^$')" ]] && ok "and adds no blank line" || bad "blank line added"
# the oldest is by received_at, not by id order
rm -f "$RELAY_LOG" "$RELAY_LOG.lock"; : >"$RELAY_LOG"
hook | grep -q "no relay or row: 3 (oldest I-3)" && ok "the oldest is the earliest received, not the lowest id" || bad "oldest by received_at"
# a closed ask is no longer unlinked: closing I-2 takes the count from 3 to 2
"$here/dws" ask-close I-2 --as answered --ref pr:x/y#1 >/dev/null 2>&1 || true
hook | grep -q "no relay or row: 2 (oldest I-3)" && ok "a closed ask drops out of the unlinked count" || bad "a closed ask still counted" "$(hook | grep "$ASKLINE")"
# the hook reads: it writes none of the files it looks at
sum_before="$(cksum "$DIRECTOR_STATE/asks.jsonl" "$DIRECTOR_STATE/workstreams.jsonl" "$RELAY_LOG" | tr '\n' ' ')"; "$H" >/dev/null
[[ "$sum_before" == "$(cksum "$DIRECTOR_STATE/asks.jsonl" "$DIRECTOR_STATE/workstreams.jsonl" "$RELAY_LOG" | tr '\n' ' ')" ]] && ok "the hook writes none of the stores it reads" || bad "the hook wrote a store"
# the join cannot be read: UNKNOWN, never a silent nothing
stub="$root/dws-fail"; printf '#!/bin/sh\necho boom >&2\nexit 3\n' >"$stub"; chmod +x "$stub"
STANDING_HOOK_DWS="$stub" hook | grep -q '^asks in the last 24 h with no relay or row: UNKNOWN (' && ok "a failing join reads as UNKNOWN" || bad "a failing join reads as UNKNOWN"
stub="$root/dws-slow"; printf '#!/bin/sh\nsleep 30\n' >"$stub"; chmod +x "$stub"
t0s="$(date +%s)"; slow="$(STANDING_HOOK_DWS="$stub" STANDING_HOOK_ASKS_TIMEOUT=1 hook | grep "$ASKLINE")"; t1s="$(date +%s)"
[[ "$slow" == *"UNKNOWN ("* && $((t1s - t0s)) -lt 10 ]] && ok "a slow join times out and reads as UNKNOWN" || bad "a slow join" "$slow after $((t1s - t0s))s"
stub="$root/dws-junk"; printf '#!/bin/sh\necho not-json\n' >"$stub"; chmod +x "$stub"
STANDING_HOOK_DWS="$stub" hook | grep -q "${ASKLINE}UNKNOWN (" && ok "a join that is not JSON reads as UNKNOWN" || bad "junk join reads as UNKNOWN"
# an unreadable relay log must not make every ask look unlinked
RELAY_LOG="$root/no-such-dir/relay-log.md" hook | grep -q "${ASKLINE}UNKNOWN (.*relay" && ok "an unreadable relay log reads as UNKNOWN, not as every ask unlinked" || bad "an unreadable relay log reads as UNKNOWN"
# and with no ask in the window the same unreadable log prints nothing
mv "$DIRECTOR_STATE/asks.jsonl" "$root/asks.keep"; mkask I-9 40
! RELAY_LOG="$root/no-such-dir/relay-log.md" hook | grep -q "$ASKLINE" && ok "no ask in the window prints nothing, whatever the relay log" || bad "no ask in the window"
mv "$root/asks.keep" "$DIRECTOR_STATE/asks.jsonl"

# --- the asks line with a waiting line: the waiting line stays last --------------------------------------
# One unlinked ask in the window and one approved PR waiting. The waiting line is last and the asks line
# is the line just before it, so swapping the two blocks in the hook turns this red.
rm -f "$DIRECTOR_STATE/asks.jsonl" "$DIRECTOR_STATE/workstreams.jsonl" "$RELAY_LOG" "$RELAY_LOG.lock"; : >"$RELAY_LOG"
mkask I-1 1
nowz="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
printf '{"read_at": "%s", "answered_not_relayed": []}\n' "$nowz" >"$DIRECTOR_STATE/desk-reads.jsonl"
printf '{"written_at": "%s", "checked": 1, "approved_not_merged": [{"pr": "o/r#1", "approved_at": "%s"}]}\n' "$nowz" "$nowz" >"$DIRECTOR_STATE/waiting-prs.json"
out="$(hook)"
[[ "$(printf '%s\n' "$out" | tail -n 1)" == "waiting: "* ]] && ok "with both lines present the waiting line is last" || bad "the waiting line is not last" "$(printf '%s\n' "$out" | tail -n 1)"
[[ "$(printf '%s\n' "$out" | tail -n 2 | head -n 1)" == "asks in the last 24 h with no relay or row: 1 (oldest I-1)" ]] && ok "and the asks line is the line just before it" || bad "the asks line is not just before the waiting line"
rm -f "$DIRECTOR_STATE/desk-reads.jsonl" "$DIRECTOR_STATE/waiting-prs.json"

# --- the inbox alarm: director's own oldest unread age against the pending-age bound ---------------------
# Diagnostic only, whatever the cue state. Read through `director-mcp unread --json` (STANDING_HOOK_MCP names
# the binary), the row whose agent_id is this seat's. At or over the bound (DIRECTOR_PENDING_AGE_BOUND, a Go
# duration, default 10m = twice the cue window) it prints one line naming the oldest unread seq and its age;
# under it, nothing. A reader that fails reads as UNKNOWN; no binary at all prints nothing.
ALARM='^director inbox: '
export DIRECTOR_AGENT_ID=director
mcp_stub() { # mcp_stub NAME AGE [SEQ] [AGENT]: a director-mcp whose unread row has that age
  local f="$root/mcp-$1"
  printf '#!/bin/sh\nprintf '"'"'{"stream":"AGENT_INBOX","durables":[{"agent_id":"%s","instance":"i1","live":true,"pending":3,"oldest_unread_seq":%s,"oldest_unread_age_seconds":%s},{"agent_id":"other","instance":"i2","live":true,"pending":1,"oldest_unread_seq":7,"oldest_unread_age_seconds":99999}]}'"'"'\n' "${4:-director}" "${3:-43501}" "$2" >"$f"
  chmod +x "$f"; echo "$f"
}
rm -f "$DIRECTOR_STATE/asks.jsonl" "$DIRECTOR_STATE/workstreams.jsonl" "$RELAY_LOG" "$RELAY_LOG.lock"; : >"$RELAY_LOG"
m="$(mcp_stub at-bound 600)"
out="$(STANDING_HOOK_MCP="$m" "$H")"
[[ "$(printf '%s\n' "$out" | grep "$ALARM")" == "director inbox: oldest unread seq 43501 is 10m old (bound 10m)" ]] && ok "at the bound (2 windows) one line names the oldest seq and its age" || bad "alarm at the bound" "$(printf '%s\n' "$out" | grep "$ALARM")"
[[ "$(printf '%s\n' "$out" | grep -c "$ALARM")" == 1 ]] && ok "the alarm is printed once" || bad "alarm printed once"
m="$(mcp_stub under 299)"
! STANDING_HOOK_MCP="$m" hook | grep -q "$ALARM" && ok "one second under a window prints nothing" || bad "alarm under the bound"
m="$(mcp_stub just-under 599)"
! STANDING_HOOK_MCP="$m" hook | grep -q "$ALARM" && ok "and so does one second under the bound" || bad "alarm just under the bound"
m="$(mcp_stub other-agent 5 43501 someone-else)"
! STANDING_HOOK_MCP="$m" hook | grep -q "$ALARM" && ok "another seat's old backlog is not this seat's alarm" || bad "other seat alarmed"
m="$(mcp_stub env-bound 150)"
STANDING_HOOK_MCP="$m" DIRECTOR_PENDING_AGE_BOUND=2m hook | grep -q "^director inbox: oldest unread seq 43501 is 2m old (bound 2m)$" && ok "DIRECTOR_PENDING_AGE_BOUND moves the bound" || bad "env bound"
! STANDING_HOOK_MCP="$m" DIRECTOR_PENDING_AGE_BOUND=nonsense hook | grep -q "$ALARM" && ok "a bound that is not a Go duration falls back to 10m" || bad "bad bound"
m="$(mcp_stub order 900)"; mkask I-1 1
out="$(STANDING_HOOK_MCP="$m" "$H")"
n_a="$(printf '%s\n' "$out" | grep -n '^asks in the last 24 h' | cut -d: -f1)"; n_i="$(printf '%s\n' "$out" | grep -n "$ALARM" | cut -d: -f1)"
[[ -n "$n_a" && "$n_i" == $((n_a + 1)) ]] && ok "the alarm sits right after the asks line" || bad "alarm order" "$n_a $n_i"
printf '{"read_at": "%s", "answered_not_relayed": []}\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$DIRECTOR_STATE/desk-reads.jsonl"
printf '{"written_at": "%s", "checked": 1, "approved_not_merged": [{"pr": "o/r#1", "approved_at": "%s"}]}\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$DIRECTOR_STATE/waiting-prs.json"
[[ "$(STANDING_HOOK_MCP="$m" hook | tail -n 1)" == "waiting: "* ]] && ok "the waiting line stays last" || bad "waiting line not last"
rm -f "$DIRECTOR_STATE/desk-reads.jsonl" "$DIRECTOR_STATE/waiting-prs.json" "$DIRECTOR_STATE/asks.jsonl"
stub="$root/mcp-fail"; printf '#!/bin/sh\necho boom >&2\nexit 3\n' >"$stub"; chmod +x "$stub"
STANDING_HOOK_MCP="$stub" hook | grep -q "${ALARM}UNKNOWN (" && ok "a failing reader reads as UNKNOWN" || bad "failing reader"
stub="$root/mcp-junk"; printf '#!/bin/sh\necho not-json\n' >"$stub"; chmod +x "$stub"
STANDING_HOOK_MCP="$stub" hook | grep -q "${ALARM}UNKNOWN (" && ok "output that is not JSON reads as UNKNOWN" || bad "junk reader"
stub="$root/mcp-slow"; printf '#!/bin/sh\nsleep 30\n' >"$stub"; chmod +x "$stub"
t0s="$(date +%s)"; slow="$(STANDING_HOOK_MCP="$stub" STANDING_HOOK_UNREAD_TIMEOUT=1 hook | grep "$ALARM")"; t1s="$(date +%s)"
[[ "$slow" == *"UNKNOWN ("* && $((t1s - t0s)) -lt 10 ]] && ok "a slow reader times out and reads as UNKNOWN" || bad "slow reader" "$slow after $((t1s - t0s))s"
! STANDING_HOOK_MCP="$root/no-such-binary" hook | grep -q "$ALARM" && ok "no director-mcp at all prints nothing" || bad "missing binary printed"
! hook | grep -q "$ALARM" && ok "and the default lookup finds none under a scratch HOME" || bad "default lookup"
unset DIRECTOR_AGENT_ID
mkask I-1 1     # the timeout cases below count this ask

# --- STANDING_HOOK_ASKS_TIMEOUT: any value that is not finite and above 0 falls back to 3 -----------------
for v in inf -inf nan 0 -1 1e999 abc ""; do
  out="$(STANDING_HOOK_ASKS_TIMEOUT="$v" "$H" 2>&1)" && rc=0 || rc=$?
  [[ $rc -eq 0 && "$(printf '%s\n' "$out" | sed -n 2p)" == "drain age: "* && "$out" != *Traceback* ]] && ok "timeout '$v' does not crash the hook" || bad "timeout '$v' crashes the hook" "rc=$rc $(printf '%s\n' "$out" | tail -n 2 | tr '\n' ' ')"
  printf '%s\n' "$out" | grep -q '^asks in the last 24 h with no relay or row: 1 (oldest I-1)$' && ok "timeout '$v' still counts the unlinked ask" || bad "timeout '$v' loses the asks line"
done


# --- the loop status (standing loop S6): what the tick wrote, read on the next prompt ------------------------------
# Read from $DIRECTOR_STATE/loop-status.json (director-tick), digest.md and workstreams.jsonl (mtimes). Every
# clause is gated on loop-status.json existing: with no status file the output is byte-identical to a hook that
# knows nothing of the loop, which is what every install without a tick sees. The hook stays read-only and
# starts no new subprocess. Deferred, and not tested here: relayed asks with no card (slice 11), batches parsed
# and never checked (slice 13), and "beat broken" (no drain record is named yet).
ago() { python3 -I -c 'import datetime as d,sys;print((d.datetime.now(d.timezone.utc)-d.timedelta(minutes=float(sys.argv[1]))).strftime("%Y-%m-%dT%H:%M:%SZ"))' "$1"; }
touch_ago() { python3 -I -c 'import os,sys,time;t=time.time()-float(sys.argv[2])*60;os.utime(sys.argv[1],(t,t))' "$1" "$2"; }
saved_state="$DIRECTOR_STATE"
export DIRECTOR_STATE="$root/loop-state"; rm -rf "$DIRECTOR_STATE"; mkdir -p "$DIRECTOR_STATE"
S="$DIRECTOR_STATE/loop-status.json"
fresh_inputs() { # a quiet Desk: nothing waits, so only a loop clause can make the waiting line print
  local nz; nz="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  printf '{"read_at": "%s", "answered_not_relayed": []}\n' "$nz" >"$DIRECTOR_STATE/desk-reads.jsonl"
  printf '{"written_at": "%s", "checked": 0, "approved_not_merged": []}\n' "$nz" >"$DIRECTOR_STATE/waiting-prs.json"
}
status() { # status RAN_AT_MINUTES_AGO DUE_JSON SEATS_JSON
  printf '{"schema": 1, "ran_at": "%s", "steps": {}, "due": %s, "seats": %s}\n' "$(ago "$1")" "$2" "$3" >"$S"
}
NODUE='{"drain": false, "desk_read": false, "dashboard": false}'
loopline() { hook | grep '^loop: ' || true; }
fresh_inputs

# no status file: stale digest and ledger change nothing, and no loop line appears
printf 'digest\n' >"$DIRECTOR_STATE/digest.md"; touch_ago "$DIRECTOR_STATE/digest.md" 3000
printf '{}\n' >"$DIRECTOR_STATE/workstreams.jsonl"; touch_ago "$DIRECTOR_STATE/workstreams.jsonl" 3000
with_files="$(DWS_TEST_CLOCK=1 DWS_NOW=2026-11-18T12:00:00Z hook)"
mv "$DIRECTOR_STATE/digest.md" "$root/digest.hold"; mv "$DIRECTOR_STATE/workstreams.jsonl" "$root/ledger.hold"
without_files="$(DWS_TEST_CLOCK=1 DWS_NOW=2026-11-18T12:00:00Z hook)"
mv "$root/digest.hold" "$DIRECTOR_STATE/digest.md"; mv "$root/ledger.hold" "$DIRECTOR_STATE/workstreams.jsonl"
[[ "$with_files" == "$without_files" ]] && ok "no status file: a stale digest and ledger change nothing (output byte-identical)" || bad "no status file: output changed with digest.md and workstreams.jsonl present"
! printf '%s\n' "$with_files" | grep -Eq '^(loop:|waiting: .*(digest|ledger))' && ok "no status file: no loop line, no digest or ledger clause, no 'no tick' text" || bad "no status file printed a loop clause"
! printf '%s\n' "$with_files" | grep -qi 'no tick' && ok "no status file: nothing says there is no tick" || bad "no status file mentions the tick"
# The same inputs against main's bytes. fixtures/standing-hook-no-loop-status.golden is the full output of the
# standing-hook at main f054f5d1be24c5d16a6dded4cfa4778c8b24461c, before the loop clauses existed, on these inputs
# (a quiet Desk, a 50-hour-old digest.md and workstreams.jsonl, no loop-status.json, no drain, dash or scan stamp),
# made with: env -i HOME=<scratch> PATH=/opt/homebrew/bin:/usr/bin:/bin LANG=C DIRECTOR_STATE=<scratch state>
# DWS_TEST_CLOCK=1 DWS_NOW=2026-11-18T12:00:00Z <main's skills/director/scripts>/standing-hook
DWS_TEST_CLOCK=1 DWS_NOW=2026-11-18T12:00:00Z "$H" >"$root/no-status.out"
cmp -s "$root/no-status.out" "$here/fixtures/standing-hook-no-loop-status.golden" \
  && ok "no status file: the output is byte-identical to main's (golden from f054f5d1)" || bad "no status file: the output differs from main's golden"

# tick age: fresh, stale, the env bound, unreadable
status 5 "$NODUE" '[]'
[[ "$(loopline)" == "loop: tick 5m ago; due: nothing" ]] && ok "a fresh status prints the tick age and nothing due" || bad "fresh loop line" "$(loopline)"
status 89 "$NODUE" '[]'
[[ "$(loopline)" == "loop: tick 89m ago; due: nothing" ]] && ok "89 minutes is still fresh (the bound is 90)" || bad "89m loop line" "$(loopline)"
status 150 "$NODUE" '[]'
[[ "$(loopline)" == "loop: tick 2h ago (stale, over 90m); due: nothing" ]] && ok "past 90 minutes the tick is stale" || bad "stale loop line" "$(loopline)"
status 15 "$NODUE" '[]'
[[ "$(DIRECTOR_TICK_MAX_MINUTES=10 hook | grep '^loop: ')" == "loop: tick 15m ago (stale, over 10m); due: nothing" ]] && ok "DIRECTOR_TICK_MAX_MINUTES moves the bound" || bad "env tick bound"
printf 'not json\n' >"$S"
[[ "$(loopline)" == "loop: UNKNOWN (loop-status.json unreadable)" ]] && ok "a status that does not parse reads UNKNOWN" || bad "unreadable status" "$(loopline)"
printf '{"schema": 1, "steps": {}, "due": {}, "seats": []}\n' >"$S"
[[ "$(loopline)" == "loop: UNKNOWN (loop-status.json unreadable)" ]] && ok "a status with no ran_at reads UNKNOWN" || bad "no ran_at" "$(loopline)"

# due flags, in the order drain, desk_read, dashboard
status 5 '{"drain": true, "desk_read": false, "dashboard": true}' '[]'
[[ "$(loopline)" == "loop: tick 5m ago; due: drain, dashboard" ]] && ok "due names the flags that are true, in order" || bad "due flags" "$(loopline)"
status 5 '{"drain": true, "desk_read": true, "dashboard": true}' '[]'
[[ "$(loopline)" == "loop: tick 5m ago; due: drain, desk_read, dashboard" ]] && ok "all three due" || bad "all due" "$(loopline)"

# seats not live: only a present cue that is not live; the cap is three
status 5 "$NODUE" '[{"id":"a1","cue":"live","cue_reason":""},{"id":"b2","cue":"unverified","cue_reason":"no nonce echo after self-test"},{"id":"c3","cue":"","cue_reason":""}]'
[[ "$(loopline)" == "loop: tick 5m ago; due: nothing; seats not live: b2 (no nonce echo after self-test)" ]] && ok "a live seat and one with no cue are not listed; an unverified one is, with its reason" || bad "seats not live" "$(loopline)"
status 5 "$NODUE" '[{"id":"s1","cue":"unverified","cue_reason":"r1"},{"id":"s2","cue":"unverified"},{"id":"s3","cue":"failed","cue_reason":"r3"},{"id":"s4","cue":"unverified"},{"id":"s5","cue":"unverified"}]'
[[ "$(loopline)" == "loop: tick 5m ago; due: nothing; seats not live: s1 (r1), s2, s3 (r3), +2 more" ]] && ok "more than three seats not live: three, then +n more" || bad "seat cap" "$(loopline)"

# placement: the loop line sits right after the stalled-ideas line; the waiting line stays last
status 5 "$NODUE" '[]'
out="$(hook)"
n_s="$(printf '%s\n' "$out" | grep -n '^stalled ideas:' | cut -d: -f1 || true)"; n_l="$(printf '%s\n' "$out" | grep -n '^loop: ' | cut -d: -f1 || true)"
[[ -n "$n_s" && "$n_l" == $((n_s + 1)) ]] && ok "the loop line follows the stalled-ideas line" || bad "loop line placement" "$n_s $n_l"

# digest age: over 12 hours is a waiting clause (digest_over_12h_waiting); at 11h59m it is not
status 5 "$NODUE" '[]'
touch_ago "$DIRECTOR_STATE/workstreams.jsonl" 5   # a fresh ledger, so only the digest can add a clause here
touch_ago "$DIRECTOR_STATE/digest.md" 719; fresh_inputs
! hook | grep -q '^waiting:' && ok "a digest 11h59m old adds no clause and the waiting line stays away" || bad "digest under 12h"
touch_ago "$DIRECTOR_STATE/digest.md" 785; fresh_inputs
[[ "$(hook | grep '^waiting:')" == "waiting: digest 13h old; Desk read 0m ago" ]] && ok "digest_over_12h_waiting: a digest over 12 hours old is a waiting clause" || bad "digest_over_12h_waiting" "$(hook | grep '^waiting:')"
rm -f "$DIRECTOR_STATE/digest.md"; fresh_inputs
! hook | grep -q '^waiting:' && ok "a missing digest.md adds no clause (the digest is not on the tick yet)" || bad "missing digest"

# ledger age: over 24 hours is a waiting clause, after the digest clause and before the Desk read
touch_ago "$DIRECTOR_STATE/workstreams.jsonl" 1500; fresh_inputs
[[ "$(hook | grep '^waiting:')" == "waiting: ledger 25h old; Desk read 0m ago" ]] && ok "a ledger over 24 hours old is a waiting clause" || bad "ledger clause" "$(hook | grep '^waiting:')"
touch_ago "$DIRECTOR_STATE/workstreams.jsonl" 1380; fresh_inputs
! hook | grep -q '^waiting:' && ok "a ledger 23h old adds no clause" || bad "ledger under 24h"
printf 'digest\n' >"$DIRECTOR_STATE/digest.md"; touch_ago "$DIRECTOR_STATE/digest.md" 785
touch_ago "$DIRECTOR_STATE/workstreams.jsonl" 1500; fresh_inputs
[[ "$(hook | grep '^waiting:')" == "waiting: digest 13h old; ledger 25h old; Desk read 0m ago" ]] && ok "both clauses, digest first, then the ledger, then the Desk read" || bad "both clauses" "$(hook | grep '^waiting:')"
[[ "$(hook | tail -n 1)" == "waiting: "* ]] && ok "the waiting line stays last" || bad "waiting not last"

# read only, and no new subprocess: the state tree is unchanged, and a tmux, nats or gh on PATH is never called
fakes="$root/loopfakes"; mkdir -p "$fakes"; : >"$root/loopcalls"
for t in tmux nats nats-server gh; do printf '#!/bin/sh\necho "%s $*" >>"%s"\n' "$t" "$root/loopcalls" >"$fakes/$t"; chmod +x "$fakes/$t"; done
status 5 '{"drain": true, "desk_read": false, "dashboard": false}' '[{"id":"b2","cue":"unverified"}]'
before="$(cd "$DIRECTOR_STATE" && find . -type f -exec shasum -a 256 {} + | sort | shasum -a 256)"
PATH="$fakes:$PATH" "$H" >/dev/null
after="$(cd "$DIRECTOR_STATE" && find . -type f -exec shasum -a 256 {} + | sort | shasum -a 256)"
[[ "$before" == "$after" ]] && ok "the hook writes none of the loop inputs" || bad "the hook wrote a loop input"
[[ ! -s "$root/loopcalls" ]] && ok "the hook calls no tmux, nats or gh" || bad "the hook called a pane, bus or GitHub tool" "$(cat "$root/loopcalls")"
export DIRECTOR_STATE="$saved_state"

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
