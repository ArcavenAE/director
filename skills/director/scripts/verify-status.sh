#!/usr/bin/env bash
# Prove `director status` (standing loop S7, sim/design/director-standing-loop.md section 3 and slice row 7):
# it prints exactly what the standing hook prints, and it changes nothing about the launcher's other words.
#
# What `status` must do (director's S7 rulings, recorded on aae-orc-oopc8):
#   - exec the standing-hook: same stdout, same stderr, same exit code, same environment, the hook's own clock;
#   - find the installed bin/standing-hook first, and the checkout's skills/director/scripts/standing-hook only
#     as the fallback; with neither, "STOP: standing-hook not found; nothing was run" on stderr and exit 3;
#   - take no argument (a word after `status` is a usage error, exit 2), so it can never reach the hook's
#     `beat` or `scan-beat`, which write last-drain and last-stalled-scan;
#   - write nothing, create no state directory, and read no seat.env or MCP config.
# The launcher's other words keep main's bytes and exit codes. The values pinned in the "main" group below were
# recorded by running scripts/director at main 97cad4177e0ce5faf15f0c800b12e234d2a5c0ee, before `status` existed,
# under: env -i HOME=<scratch> TMPDIR=<scratch> PATH=/opt/homebrew/bin:/usr/bin:/bin LANG=C
# scripts/director <args>, with the arguments each case names. Each case says what it exercises. start and hub
# cannot run for real in scratch, so they are pinned where they stop safely: start without a seat file hits its
# STOP before any claude runs, start with a bad flag is usage, and hub runs a fake director-mcp that prints its
# arguments and exits 4.
#
# Run conditions: every run is env -i with a scratch HOME and TMPDIR, no MARVEL_*, DIRECTOR_* (beyond
# DIRECTOR_HOME and DIRECTOR_STATE set inside scratch) or NATS_* variable, no network, no write outside scratch.
#
# Usage: skills/director/scripts/verify-status.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
L="${DIRECTOR_LAUNCHER:-$here/scripts/director}"
H="$here/skills/director/scripts/standing-hook"
PY="$(command -v python3 || true)"
[[ -n "$PY" ]] || { echo "python3 not on PATH"; exit 2; }
pydir="$(dirname "$PY")"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(cd "$(mktemp -d "${TMPDIR:-/tmp}/verify-status.XXXXXX")" && pwd -P)"
trap 'rm -rf "$root"' EXIT
home="$root/home"; mkdir -p "$home" "$root/tmp"
CLOCK=(DWS_TEST_CLOCK=1 DWS_NOW=2026-11-18T12:00:00Z)

# cap <name> <dir for DIRECTOR_STATE or -> <program> [args]: one env -i run, output in $root/<name>.{out,err,rc}
cap() {
  local n="$1" st="$2"; shift 2
  local rc=0 dh=()
  [[ -n "${DH:-}" ]] && dh=(DIRECTOR_HOME="$DH")
  env -i HOME="$home" TMPDIR="$root/tmp" PATH="$pydir:/usr/bin:/bin" LANG=C ${dh[@]+"${dh[@]}"} ${st:+DIRECTOR_STATE="$st"} "${CLOCK[@]}" "$@" \
    >"$root/$n.out" 2>"$root/$n.err" || rc=$?
  printf '%s' "$rc" >"$root/$n.rc"
}
same() { # same <a> <b>: stdout, stderr and exit code of two captures are equal
  cmp -s "$root/$1.out" "$root/$2.out" && cmp -s "$root/$1.err" "$root/$2.err" && [[ "$(cat "$root/$1.rc")" == "$(cat "$root/$2.rc")" ]]
}
treehash() { (cd "$1" 2>/dev/null && find . -type f -exec shasum -a 256 {} + | sort | shasum -a 256) || echo none; }

# --- the launcher's other words, pinned to main's bytes (these pass before and after) -----------------------------
USAGE=$'usage: director start [--cluster <name>] [--resume <session name>] [--seat-file <path>]\n       director env [--seat-file <path>]\n       director prove\n       director hub user|settings ...   (run on the hub host; see sim/design/hub-user-lifecycle.md)'
pin() { # pin <name> <rc> <stdout> <stderr> <args...>
  local n="$1" rc="$2" out="$3" err="$4"; shift 4
  cap "pin-$n" "" "$L" "$@"
  if [[ "$(cat "$root/pin-$n.rc")" == "$rc" && "$(cat "$root/pin-$n.out")" == "$out" && "$(cat "$root/pin-$n.err")" == "$err" ]]; then ok "main's bytes and exit $rc: $n"; else bad "main's bytes and exit $rc: $n" "rc=$(cat "$root/pin-$n.rc") out=[$(cat "$root/pin-$n.out")] err=[$(head -c 200 "$root/pin-$n.err")]"; fi
}
# Every case that prints the usage text (director's S7 ruling (b)): exit 2, nothing on stdout, the four lines
# recorded from main unchanged and in order, and exactly one added line, "       director status". Dropping that
# one line gives main's usage byte for byte.
STATUS_LINE='       director status'
# S9 adds one more line, "       director doctor" (director's S9 ruling): dropping it too still gives main's bytes.
DOCTOR_LINE='       director doctor'
# S8 adds a third, for the new start flag (director's S8 ruling (b)): dropping all three gives main's usage byte for byte.
PLUGIN_LINE='       director start --plugin-dir <path>   (load a Claude Code plugin tree for this session)'
pin_usage() { # pin_usage <name> <args...>
  local n="$1"; shift
  cap "usage-$n" "" "$L" "$@"
  local err; err="$(cat "$root/usage-$n.err")"
  local count; count="$(grep -cxF -- "$STATUS_LINE" "$root/usage-$n.err" || true)"
  local dcount; dcount="$(grep -cxF -- "$DOCTOR_LINE" "$root/usage-$n.err" || true)"
  local pcount; pcount="$(grep -cxF -- "$PLUGIN_LINE" "$root/usage-$n.err" || true)"
  local rest; rest="$(grep -vxF -e "$STATUS_LINE" -e "$DOCTOR_LINE" -e "$PLUGIN_LINE" "$root/usage-$n.err" || true)"
  if [[ "$(cat "$root/usage-$n.rc")" == 2 && ! -s "$root/usage-$n.out" && "$count" == 1 && "$dcount" == 1 && "$pcount" == 1 && "$rest" == "$USAGE" ]]; then ok "usage, exit 2: main's lines in order plus the status, doctor and plugin-dir lines: $n"; else bad "usage, exit 2: main's lines in order plus the status, doctor and plugin-dir lines: $n" "rc=$(cat "$root/usage-$n.rc") count=$count err=[$err]"; fi
}
# no argument (exercises the dispatch's default branch) and an unknown word (exercises `*) usage`)
pin_usage no-argument
pin_usage unknown-word bogus
# start with a flag it does not know, and with a flag that lacks its value: usage before any seat file or claude
pin_usage start-bad-flag start --bogus
pin_usage start-missing-value start --cluster
# env with a flag it does not know, and with a flag that lacks its value
pin_usage env-bad-flag env --bogus
pin_usage env-missing-value env --seat-file
# prove ignores extra words and is not usage: its STOP, byte for byte
pin prove-extra-word 3 "" "STOP: no launch id: run this inside a session started by director start" prove x
# start with no seat file: its STOP for the missing identity values, exit 3, before claude would run
pin start-no-seat 3 "" "STOP: missing DIRECTOR_AGENT_ID DIRECTOR_TEAM DIRECTOR_WORKSPACE; put them in $home/.director/seat.env as KEY=value lines or pass them by flag (--cluster covers DIRECTOR_CLUSTER only)" start
# env with no seat file: prints nothing, exit 0
pin env-no-seat 0 "" "" env
pin env-missing-file 0 "" "" env --seat-file "$root/none"
# prove outside a launcher-started session: its STOP, exit 3
pin prove 3 "" "STOP: no launch id: run this inside a session started by director start" prove
# hub with no installed director-mcp: its STOP naming the path, exit 3
pin hub-no-binary 3 "" "STOP: no installed director-mcp at $home/.director/bin/director-mcp; run director-install first" hub
# hub with a fake director-mcp in the bin home: the arguments pass through and its exit code is the launcher's
mkdir -p "$home/.director/bin"; printf '#!/bin/sh\necho "fake director-mcp: $*"\nexit 4\n' >"$home/.director/bin/director-mcp"; chmod +x "$home/.director/bin/director-mcp"
pin hub-passthrough 4 "fake director-mcp: hub user list" "" hub user list
rm -f "$home/.director/bin/director-mcp"

# --- status prints what the hook prints ----------------------------------------------------------------------------
quiet="$root/state-quiet"; mkdir -p "$quiet"
# matches-hook, checkout layout, no loop-status.json: the default state for an install without a tick
cap hook-quiet "$quiet" "$H"
cap status-quiet "$quiet" "$L" status
same hook-quiet status-quiet && [[ -s "$root/hook-quiet.out" ]] && ok "matches-hook: the checkout launcher prints the hook's bytes, stderr and exit code (no status file)" || bad "matches-hook: checkout launcher, no status file" "rc=$(cat "$root/status-quiet.rc") err=[$(head -c 160 "$root/status-quiet.err")]"

# matches-hook with a loop-status.json present (the loop line, a seat not live)
loopd="$root/state-loop"; mkdir -p "$loopd"
ran="$("$PY" -I -c 'import datetime as d;print((d.datetime.now(d.timezone.utc)-d.timedelta(minutes=150,seconds=30)).strftime("%Y-%m-%dT%H:%M:%SZ"))')"
printf '{"schema": 1, "ran_at": "%s", "steps": {}, "due": {"drain": true, "desk_read": false, "dashboard": false}, "seats": [{"id":"b2","cue":"unverified","cue_reason":"no nonce echo after self-test"}]}\n' "$ran" >"$loopd/loop-status.json"
cap hook-loop "$loopd" "$H"
cap status-loop "$loopd" "$L" status
same hook-loop status-loop && grep -q '^loop: tick 2h ago (stale, over 90m); due: drain; seats not live: b2' "$root/hook-loop.out" \
  && ok "matches-hook: with a loop-status.json the launcher prints the hook's loop line and the rest, byte for byte" || bad "matches-hook: with a loop-status.json" "rc=$(cat "$root/status-loop.rc")"

# an install in scratch: link mode and copy mode
run_install() { # run_install <home dir> [--copy]
  local h="$1"; shift
  env -i HOME="$h" TMPDIR="$root/tmp" PATH="$pydir:/usr/bin:/bin" LANG=C CLAUDE_HOME="$h/claude" DIRECTOR_HOME="$h/director" DIRECTOR_STATE="$h/director/state" \
    "$here/install.sh" "$@" >"$h/install.out" 2>&1
}
linkh="$root/link"; mkdir -p "$linkh"; run_install "$linkh" || bad "the scratch link install ran" "$(tail -n 3 "$linkh/install.out")"
copyh="$root/copy"; mkdir -p "$copyh"; run_install "$copyh" --copy || bad "the scratch copy install ran" "$(tail -n 3 "$copyh/install.out")"

DH="$linkh/director"
cap ihook-link "$quiet" "$DH/bin/standing-hook"
cap istatus-link "$quiet" "$DH/bin/director" status
same ihook-link istatus-link && [[ -s "$root/ihook-link.out" ]] && ok "matches-hook: the installed launcher (link install) prints the installed hook's bytes" || bad "matches-hook: link install" "rc=$(cat "$root/istatus-link.rc") err=[$(head -c 160 "$root/istatus-link.err")]"
DH="$copyh/director"
cap ihook-copy "$quiet" "$DH/bin/standing-hook"
cap istatus-copy "$quiet" "$DH/bin/director" status
same ihook-copy istatus-copy && [[ -s "$root/ihook-copy.out" ]] && ok "matches-hook: the installed launcher (copy install, no checkout beside it) prints the installed hook's bytes" || bad "matches-hook: copy install" "rc=$(cat "$root/istatus-copy.rc") err=[$(head -c 160 "$root/istatus-copy.err")]"

# a copy install reached through the user's own link (for example ~/.local/bin/director pointing at the installed
# copy): the hook beside the resolved launcher is found, so the output and exit code are the same as by its own path
mkdir -p "$root/userbin"; ln -s "$copyh/director/bin/director" "$root/userbin/director"
cap istatus-copy-link "$quiet" "$root/userbin/director" status
same ihook-copy istatus-copy-link && [[ -s "$root/ihook-copy.out" ]] \
  && ok "a copy install run through a user's link prints the hook's bytes and exit code (the hook beside the resolved launcher)" \
  || bad "copy install through a user's link" "rc=$(cat "$root/istatus-copy-link.rc") err=[$(head -c 160 "$root/istatus-copy-link.err")]"

# the installed standing-hook comes first, and its exit code is the launcher's
DH="$linkh/director"; rm -f "$DH/bin/standing-hook"
printf '#!/bin/sh\necho "installed hook"\necho "to stderr" >&2\nexit 7\n' >"$DH/bin/standing-hook"; chmod +x "$DH/bin/standing-hook"
cap first "$quiet" "$DH/bin/director" status
{ [[ "$(cat "$root/first.out")" == "installed hook" && "$(cat "$root/first.err")" == "to stderr" && "$(cat "$root/first.rc")" == 7 ]]; } \
  && ok "the installed bin/standing-hook comes first; its stdout, stderr and exit code 7 pass through" || bad "installed hook first" "out=[$(cat "$root/first.out")] rc=$(cat "$root/first.rc")"
# with the installed hook gone, a link install falls back to the checkout's copy
rm -f "$DH/bin/standing-hook"
cap fallback "$quiet" "$DH/bin/director" status
same hook-quiet fallback && ok "with no installed hook the link install falls back to the checkout's hook" || bad "checkout fallback" "rc=$(cat "$root/fallback.rc") err=[$(head -c 160 "$root/fallback.err")]"
# a copy install with no hook anywhere stops, and says so
DH="$copyh/director"; rm -f "$DH/bin/standing-hook"
cap notfound "$quiet" "$DH/bin/director" status
{ [[ "$(cat "$root/notfound.rc")" == 3 && "$(cat "$root/notfound.err")" == "STOP: standing-hook not found; nothing was run" && ! -s "$root/notfound.out" ]]; } \
  && ok "no hook anywhere: STOP on stderr, exit 3, nothing on stdout" || bad "hook not found" "rc=$(cat "$root/notfound.rc") err=[$(head -c 160 "$root/notfound.err")]"

# --- status writes nothing and takes no argument -----------------------------------------------------------------------
DH="$linkh/director"
mkdir -p "$root/state-w"; printf '2026-11-18T11:00:00Z\n' >"$root/state-w/last-drain"
before="$(treehash "$root/state-w")"
cap plain "$root/state-w" "$L" status
after="$(treehash "$root/state-w")"
{ [[ "$(cat "$root/plain.rc")" == 0 && "$before" == "$after" ]]; } && ok "a plain status leaves the state tree unchanged" || bad "status wrote to the state tree, or failed" "rc=$(cat "$root/plain.rc")"
for w in beat scan-beat bogus; do
  rm -rf "$root/state-b"; mkdir -p "$root/state-b"
  cap "word-$w" "$root/state-b" "$L" status "$w"
  { [[ "$(cat "$root/word-$w.rc")" == 2 && "$(head -c 6 "$root/word-$w.err")" == "usage:" && -z "$(ls -A "$root/state-b")" ]]; } \
    && ok "'status $w' is a usage error (exit 2) and writes nothing" || bad "'status $w' is a usage error and writes nothing" "rc=$(cat "$root/word-$w.rc") files=[$(ls -A "$root/state-b" | tr '\n' ' ')]"
done
# a state directory that does not exist: the hook's own output, and the directory is not created
cap hook-absent "$root/absent/state" "$H"
cap status-absent "$root/absent/state" "$L" status
{ same hook-absent status-absent && [[ -s "$root/status-absent.out" && ! -e "$root/absent" ]]; } \
  && ok "a missing state directory prints the hook's output and is not created" || bad "missing state directory" "rc=$(cat "$root/status-absent.rc") exists=$([[ -e "$root/absent" ]] && echo yes || echo no)"

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
