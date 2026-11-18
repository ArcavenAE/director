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
trap 'chmod -R u+rwx "$root" 2>/dev/null; rm -rf "$root"' EXIT  # the write-protected state dir below must not block cleanup
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

# --- the seen-stamp and the loop line (standing loop S8; director's S8 rulings on aae-orc-rtz9w) ---------------------
# `standing-hook --seen` stamps $DIRECTOR_STATE/hook-seen (one RFC 3339 UTC line, written by rename like last-drain)
# and is the only way it is written: a plain run writes nothing, which every existing install's wiring relies on.
# A stamp that cannot be written is silent: same stdout, same exit, nothing on stderr. `--loop-line` prints only the
# loop line (nothing when there is no loop-status.json), for the plugin's SessionStart. The two flags combine in
# either order, and SessionStart passes both. Any other argument shape is the usage error it is today.
seen_state="$root/seen-state"; rm -rf "$seen_state"; mkdir -p "$seen_state"
export DIRECTOR_STATE="$seen_state"
clk() { env DWS_TEST_CLOCK=1 DWS_NOW=2026-11-18T12:00:00Z "$H" "$@"; }
clk >"$root/seen-plain.out" 2>"$root/seen-plain.err"
[[ -z "$(ls -A "$seen_state")" ]] && ok "writes-seen-stamp: a plain run writes nothing into the state directory" || bad "a plain run wrote into the state directory" "$(ls -A "$seen_state")"

t0="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
rc=0; clk --seen >"$root/seen-flag.out" 2>"$root/seen-flag.err" || rc=$?
t1="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
[[ "$rc" == 0 && ! -s "$root/seen-flag.err" ]] && ok "--seen exits 0 and says nothing on stderr" || bad "--seen exit or stderr" "rc=$rc err=[$(head -c 200 "$root/seen-flag.err")]"
cmp -s "$root/seen-plain.out" "$root/seen-flag.out" && ok "--seen prints exactly the plain run's bytes" || bad "--seen changed the output"
st="$(cat "$seen_state/hook-seen" 2>/dev/null || true)"
{ [[ "$st" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ]] && [[ ! "$st" < "$t0" && ! "$st" > "$t1" ]] && [[ "$(wc -l <"$seen_state/hook-seen" | tr -d ' ')" == 1 ]]; } \
  && ok "--seen stamps hook-seen: one RFC 3339 UTC line from the clock" || bad "hook-seen shape" "[$st] between $t0 and $t1"
[[ "$(ls -A "$seen_state")" == "hook-seen" ]] && ok "--seen leaves no temp file behind" || bad "--seen left extra files" "$(ls -A "$seen_state" | tr '\n' ' ')"
ln "$seen_state/hook-seen" "$root/seen-hold" 2>/dev/null || true; sleep 1
clk --seen >/dev/null 2>&1 || true
{ [[ -e "$root/seen-hold" && "$(cat "$root/seen-hold")" == "$st" ]] && [[ "$(cat "$seen_state/hook-seen" 2>/dev/null)" != "$st" ]]; } \
  && ok "the stamp is replaced by rename (an old hard link keeps the old bytes)" || bad "hook-seen was rewritten in place"
rm -f "$root/seen-hold"

# a stamp that cannot be written changes nothing the caller sees
rm -rf "$seen_state"; mkdir -p "$seen_state"
if [[ "$(id -u)" == 0 ]]; then
  skip_msg="the unwritable state directory case: running as root, where a mode bit does not stop a write"; echo "SKIP $skip_msg"
else
  chmod 555 "$seen_state"
  rc=0; clk --seen >"$root/seen-ro.out" 2>"$root/seen-ro.err" || rc=$?
  chmod 755 "$seen_state"
  { [[ "$rc" == 0 && ! -s "$root/seen-ro.err" ]] && cmp -s "$root/seen-plain.out" "$root/seen-ro.out" && [[ -z "$(ls -A "$seen_state")" ]]; } \
    && ok "an unwritable state directory: same stdout, exit 0, nothing on stderr, no stamp" || bad "unwritable state directory" "rc=$rc err=[$(head -c 200 "$root/seen-ro.err")]"
fi
printf 'not a directory\n' >"$root/seen-file"
# the baseline is a plain run on the same path: a file there changes what the hook reads (the asks line), not what --seen does
DIRECTOR_STATE="$root/seen-file" clk >"$root/seen-nd0.out" 2>/dev/null || true
rc=0; DIRECTOR_STATE="$root/seen-file" clk --seen >"$root/seen-nd.out" 2>"$root/seen-nd.err" || rc=$?
{ [[ "$rc" == 0 && ! -s "$root/seen-nd.err" ]] && cmp -s "$root/seen-nd0.out" "$root/seen-nd.out"; } \
  && ok "a state path that is a file: same stdout, exit 0, nothing on stderr" || bad "state path is a file" "rc=$rc err=[$(head -c 200 "$root/seen-nd.err")]"
# a write that fails after the temp file exists (hook-seen is a directory, so the rename fails) leaves no temp file
# behind: --seen would otherwise add one on every prompt. beat keeps raising, as it always did.
rm -rf "$seen_state"; mkdir -p "$seen_state/hook-seen"
clk >"$root/seen-d0.out" 2>/dev/null || true
rc=0; clk --seen >"$root/seen-d.out" 2>"$root/seen-d.err" || rc=$?
{ [[ "$rc" == 0 && ! -s "$root/seen-d.err" ]] && cmp -s "$root/seen-d0.out" "$root/seen-d.out"; } \
  && ok "hook-seen is a directory: same stdout, exit 0, nothing on stderr" || bad "hook-seen is a directory" "rc=$rc err=[$(head -c 200 "$root/seen-d.err")]"
[[ "$(ls -A "$seen_state" | grep -c '^\.stamp\.' || true)" == 0 ]] \
  && ok "hook-seen is a directory: no .stamp.* temp file is left behind" || bad "a failed --seen left a temp file" "$(ls -A "$seen_state" | tr '\n' ' ')"
rm -rf "$seen_state"; mkdir -p "$seen_state/last-drain"
rc=0; clk beat >/dev/null 2>&1 || rc=$?
[[ "$rc" != 0 ]] && ok "beat still fails loudly when its stamp cannot be written (last-drain is a directory)" || bad "beat swallowed a write failure"
[[ "$(ls -A "$seen_state" | grep -c '^\.stamp\.' || true)" == 0 ]] \
  && ok "a failed beat leaves no temp file either" || bad "a failed beat left a temp file" "$(ls -A "$seen_state" | tr '\n' ' ')"

rm -rf "$seen_state"
clk --seen >/dev/null 2>&1 || true
[[ -s "$seen_state/hook-seen" ]] && ok "--seen creates a missing state directory, as beat does" || bad "--seen with no state directory"

# other argument shapes stay usage errors and write nothing
rm -rf "$seen_state"; mkdir -p "$seen_state"
for args in "beat --seen" "--seen beat" "scan-beat --seen" "--bogus" "--seen --bogus" "--loop-line --bogus"; do
  read -ra argv <<<"$args"
  rc=0; clk "${argv[@]}" >"$root/seen-u.out" 2>"$root/seen-u.err" || rc=$?
  { [[ "$rc" != 0 && ! -s "$root/seen-u.out" && "$(head -c 6 "$root/seen-u.err")" == "usage:" && -z "$(ls -A "$seen_state")" ]]; } \
    && ok "'$args' is a usage error and writes nothing" || bad "'$args' is not a clean usage error" "rc=$rc out=[$(head -c 100 "$root/seen-u.out")] files=[$(ls -A "$seen_state" | tr '\n' ' ')]"
done

# --loop-line: only the loop line
fakes="$root/seenfakes"; mkdir -p "$fakes"; : >"$root/seencalls"
for t in tmux nats nats-server gh dbi; do printf '#!/bin/sh\necho "%s $*" >>"%s"\n' "$t" "$root/seencalls" >"$fakes/$t"; chmod +x "$fakes/$t"; done
status_at() { printf '{"schema": 1, "ran_at": "%s", "steps": {}, "due": {"drain": true, "desk_read": false, "dashboard": false}, "seats": []}\n' "$(ago "$1")" >"$seen_state/loop-status.json"; }
status_at 5
rc=0; PATH="$fakes:$PATH" "$H" --loop-line >"$root/ll.out" 2>"$root/ll.err" || rc=$?
{ [[ "$rc" == 0 && ! -s "$root/ll.err" ]] && [[ "$(cat "$root/ll.out")" == "loop: tick 5m ago; due: drain" ]]; } \
  && ok "--loop-line prints only the loop line" || bad "--loop-line output" "rc=$rc out=[$(cat "$root/ll.out")]"
[[ ! -e "$seen_state/hook-seen" ]] && ok "--loop-line alone writes no stamp" || bad "--loop-line alone wrote a stamp"
[[ ! -s "$root/seencalls" ]] && ok "--loop-line starts no tmux, nats, gh or dbi" || bad "--loop-line called a tool" "$(cat "$root/seencalls")"
rm -f "$seen_state/hook-seen"
PATH="$fakes:$PATH" "$H" --loop-line --seen >"$root/ll1.out" 2>/dev/null || true; rm -f "$seen_state/hook-seen"
PATH="$fakes:$PATH" "$H" --seen --loop-line >"$root/ll2.out" 2>/dev/null || true
{ cmp -s "$root/ll1.out" "$root/ll.out" && cmp -s "$root/ll2.out" "$root/ll.out" && [[ -s "$seen_state/hook-seen" ]]; } \
  && ok "--loop-line and --seen combine in either order: the loop line only, and the stamp" || bad "--loop-line with --seen"
rm -f "$seen_state/loop-status.json" "$seen_state/hook-seen"
rc=0; PATH="$fakes:$PATH" "$H" --loop-line >"$root/ll3.out" 2>"$root/ll3.err" || rc=$?
{ [[ "$rc" == 0 && ! -s "$root/ll3.out" && ! -s "$root/ll3.err" ]]; } \
  && ok "--loop-line with no loop-status.json prints nothing, so a session start adds no context" || bad "--loop-line with no status" "out=[$(head -c 100 "$root/ll3.out")]"
rc=0; PATH="$fakes:$PATH" "$H" --loop-line --seen >"$root/ll4.out" 2>/dev/null || rc=$?
{ [[ "$rc" == 0 && ! -s "$root/ll4.out" && -s "$seen_state/hook-seen" ]]; } \
  && ok "--loop-line --seen with no status prints nothing and still stamps" || bad "--loop-line --seen with no status"
printf 'not json\n' >"$seen_state/loop-status.json"
PATH="$fakes:$PATH" "$H" --loop-line >"$root/ll5.out" 2>/dev/null || true
[[ "$(cat "$root/ll5.out")" == "loop: UNKNOWN (loop-status.json unreadable)" ]] \
  && ok "--loop-line with an unreadable status prints the UNKNOWN line, as the full hook does" || bad "--loop-line with a bad status" "$(cat "$root/ll5.out")"
# --- S11: relays asking the operator to decide with no card (aae-orc-dysqv; director 01M7R3ECP4 R3, R4, R5) -----------
# A relay-log entry of Kind question that lacks a **Card:** line counts. 24 h window, gated on loop-status.json, silent at 0,
# UNKNOWN when the relay log cannot be read. Red until the clause exists.
saved_relay="${RELAY_LOG-}"
CARDLINE='^relays asking the operator to decide with no card in the last 24 h: '
export DIRECTOR_STATE="$root/card-clause-state"; rm -rf "$DIRECTOR_STATE"; mkdir -p "$DIRECTOR_STATE"
S="$DIRECTOR_STATE/loop-status.json"
fresh_inputs
export RELAY_LOG="$root/card-clause-relay.md"; : >"$RELAY_LOG"
"$here/relay-log" append --to agent://x/a --kind question --text "decide this" >/dev/null     # R-1 counts
"$here/relay-log" append --to agent://x/b --kind question --card none --text "no decision" >/dev/null 2>&1 || true   # R-2 has a Card line
"$here/relay-log" append --to agent://x/c --kind relay --text "plain relay" >/dev/null              # R-3 is not a question
printf '\n## R-4 (%s) -> agent://x/d\n**Kind:** question\n**Text, verbatim:**\n> old\n**Outcome:** pending\n' "$(ago 2880)" >>"$RELAY_LOG"   # R-4 is 48 h old
status 5 "$NODUE" '[]'
out="$(hook)"
[[ "$(printf '%s\n' "$out" | grep "$CARDLINE")" == "relays asking the operator to decide with no card in the last 24 h: 1 (oldest R-1)" ]] \
  && ok "hook_relays_with_no_card_counts_question_entries_without_a_card_line" \
  || bad "hook_relays_with_no_card_counts_question_entries_without_a_card_line" "got: [$(printf '%s\n' "$out" | grep "$CARDLINE")]"
rm -f "$S"
! hook | grep -q "$CARDLINE" && ok "hook_relays_with_no_card_is_gated_on_loop_status" || bad "hook_relays_with_no_card_is_gated_on_loop_status" "the clause printed with no loop-status.json"
status 5 "$NODUE" '[]'
: >"$RELAY_LOG"; "$here/relay-log" append --to agent://x/a --kind question --card /tmp/cards/q.md --text "has a card" >/dev/null 2>&1 || true
! hook | grep -q "$CARDLINE" && ok "hook_relays_with_no_card_is_silent_at_zero" || bad "hook_relays_with_no_card_is_silent_at_zero" "$(hook | grep "$CARDLINE")"
export RELAY_LOG="$root/card-clause-relay-dir"; mkdir -p "$RELAY_LOG"
hook | grep -q "${CARDLINE}UNKNOWN (" && ok "hook_relays_with_no_card_reads_unknown_when_the_log_is_unreadable" || bad "hook_relays_with_no_card_reads_unknown_when_the_log_is_unreadable" "got: [$(hook | grep "$CARDLINE")]"
if [[ -n "$saved_relay" ]]; then export RELAY_LOG="$saved_relay"; else unset RELAY_LOG; fi

# --- S13: card batches parsed and never checked (aae-orc-klqn1; moved here from slice 6; director 01M7R3ECP4 ruling 4) --
# Gated on $DIRECTOR_STATE/desk/cards.jsonl existing (not on loop-status.json). A batch is waiting when cards.jsonl
# holds a line for it and applied.jsonl holds none. One line, silent at 0, naming the oldest waiting batch by its
# first cards line. Red until the clause exists.
BATCHLINE='^card batches parsed and never checked: '
export DIRECTOR_STATE="$root/batch-clause-state"; rm -rf "$DIRECTOR_STATE"; mkdir -p "$DIRECTOR_STATE/desk"
fresh_inputs
CJ="$DIRECTOR_STATE/desk/cards.jsonl"; AJ="$DIRECTOR_STATE/desk/applied.jsonl"
cardline() { printf '{"at": "%s", "slug": "%s", "op": "set", "data_sha256": "aa", "batch": "%s"}\n' "$1" "$2" "$3"; }
{ cardline "$(ago 600)" rpt-1 rpt-aaaa1111.json; cardline "$(ago 600)" rpt-2 rpt-aaaa1111.json
  cardline "$(ago 300)" rpt-3 rpt-bbbb2222.json
  cardline "$(ago 60)" rpt-4 rpt-cccc3333.json; } >"$CJ"
printf '{"at": "%s", "slug": "rpt-4", "batch": "rpt-cccc3333.json", "result": "PASS", "data_sha256": "aa"}\n' "$(ago 30)" >"$AJ"
out="$(hook)"
[[ "$(printf '%s\n' "$out" | grep "$BATCHLINE")" == "card batches parsed and never checked: 2 (oldest rpt-aaaa1111.json)" ]] \
  && ok "batch_parsed_not_checked_is_waiting" \
  || bad "batch_parsed_not_checked_is_waiting" "got: [$(printf '%s\n' "$out" | grep "$BATCHLINE")]"
printf '{"at": "%s", "slug": "rpt-1", "batch": "rpt-aaaa1111.json", "result": "MISMATCH", "data_sha256": "aa"}\n' "$(ago 20)" >>"$AJ"
[[ "$(hook | grep "$BATCHLINE")" == "card batches parsed and never checked: 1 (oldest rpt-bbbb2222.json)" ]] \
  && ok "batch_parsed_not_checked_counts_a_checked_batch_out_even_on_mismatch" \
  || bad "batch_parsed_not_checked_counts_a_checked_batch_out_even_on_mismatch" "got: [$(hook | grep "$BATCHLINE")]"
printf '{"at": "%s", "slug": "rpt-3", "batch": "rpt-bbbb2222.json", "result": "PASS", "data_sha256": "aa"}\n' "$(ago 10)" >>"$AJ"
! hook | grep -q "$BATCHLINE" && ok "batch_parsed_not_checked_is_silent_at_zero" || bad "batch_parsed_not_checked_is_silent_at_zero" "$(hook | grep "$BATCHLINE")"
: >"$AJ"
cardline "$(ago 5)" rpt-5 rpt-dddd4444.json >"$CJ"
mv "$CJ" "$CJ.off"
! hook | grep -q "$BATCHLINE" && ok "batch_parsed_not_checked_is_gated_on_cards_jsonl" || bad "batch_parsed_not_checked_is_gated_on_cards_jsonl" "the clause printed with no cards.jsonl"
mv "$CJ.off" "$CJ"
printf 'not json\n' >"$CJ"
hook | grep -q "${BATCHLINE}UNKNOWN (" && ok "batch_parsed_not_checked_reads_unknown_when_cards_jsonl_is_unreadable" || bad "batch_parsed_not_checked_reads_unknown_when_cards_jsonl_is_unreadable" "got: [$(hook | grep "$BATCHLINE")]"

export DIRECTOR_STATE="$saved_state"

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
