#!/usr/bin/env bash
# Prove director-tick, slice 5 of sim/design/director-standing-loop.md (section 2, "The tick", and
# director's S5 rulings recorded on aae-orc-5icwm). Red first: until director-tick exists every case
# below reports MISS, and each MISS is an assertion on a missing output, not a build error.
#
# The tick is one pass. It reads and computes, and writes $DIRECTOR_STATE/loop-status.json:
#   {"schema":1,"ran_at":"<UTC>","steps":{"<step>":{"status":"ok|skipped|gap","reason":"..."}},
#    "due":{"drain":bool,"desk_read":bool,"dashboard":bool},
#    "seats":[{"id","cue","cue_reason"}]}
# steps: desk_refresh, waiting_write, stalled_ideas, presence (and profile, only when a [loop] value
# is refused). It never types into a pane, never sends or acks on the bus, never writes the Desk, and
# never runs the dashboard writer.
#
# Run conditions: every run is env -i with a scratch HOME, a short scratch TMPDIR and a scratch
# DIRECTOR_STATE, no MARVEL_*, DIRECTOR_* (beyond DIRECTOR_STATE and DIRECTOR_PROFILE set inside
# the scratch tree) or NATS_* variable. tmux, nats, nats-server, gh, dash-writer and director-mcp on
# PATH are fakes that log every call, so a call to the live bus, a pane or GitHub is visible and none
# is made. Nothing is written outside the scratch tree. The clock is TICK_NOW, so ages are exact.
#
# Covered: status-written-and-valid-json; no-tmux-or-nats-call; dashboard-untouched; idempotent;
# each-step-gap-has-reason; due-flags-from-stamps (drain at the 30 minute default, desk_read at 24
# hours, dashboard at the 12 hour default); loop values read from [loop] (tick_interval,
# digest_max_age); positive-interval-refused; never-list; no temp file left behind.
#
# Usage: skills/director/scripts/verify-director-tick.sh   Exit nonzero on any miss.
#        DIRECTOR_TICK_BIN=<path> to test another copy.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TICK="${DIRECTOR_TICK_BIN:-$here/director-tick}"
PY="$(command -v python3 || true)"
[[ -n "$PY" ]] || { echo "python3 not on PATH"; exit 2; }
"$PY" -c 'import sys; sys.exit(0 if sys.version_info >= (3, 11) else 1)' || { echo "python3 3.11 or newer is needed"; exit 2; }
pydir="$(dirname "$PY")"

work="$(cd "$(mktemp -d "${TMPDIR:-/tmp}/director-tick-verify.XXXXXX")" && pwd -P)"
trap 'rm -rf "$work"' EXIT
fail=0
miss() { echo "MISS: $*"; fail=1; }
okay() { echo "ok: $*"; }

NOW=2026-11-18T12:00:00Z
bin="$work/fakebin"; mkdir -p "$bin"
calls="$work/calls.log"; : >"$calls"
for t in tmux nats nats-server gh dash-writer; do
  printf '#!/bin/sh\nprintf "%s %%s\\n" "$*" >>"$CALLS_LOG"\nexit 0\n' "$t" >"$bin/$t"
done
# gh answers an empty list, so waiting_write has nothing to ask about even if it does call it
printf '#!/bin/sh\nprintf "gh %%s\\n" "$*" >>"$CALLS_LOG"\necho "[]"\nexit 0\n' >"$bin/gh"
cat >"$bin/director-mcp" <<'EOF'
#!/bin/sh
printf 'director-mcp %s\n' "$*" >>"$CALLS_LOG"
case "${FAKE_ROSTER:-ok}" in
  fail) echo "director-mcp roster: connect refused" >&2; exit 1 ;;
  *) cat <<'JSON'
{"read_at":"2026-11-18T12:00:00Z","rows":[
 {"agent_id":"alpha-1","team":"t","role":"builder","instance":"i1","fresh":true,"age_seconds":5,"cue":"live"},
 {"agent_id":"beta-2","team":"t","role":"reviewer","instance":"i2","fresh":true,"age_seconds":9,"cue":"unverified","cue_reason":"no nonce echo after self-test"}]}
JSON
  ;;
esac
EOF
chmod +x "$bin"/*

# fresh_state <name>: a new scratch state dir, printed
fresh_state() { local d="$work/$1"; rm -rf "$d"; mkdir -p "$d/home" "$d/tmp" "$d/state"; printf '%s' "$d"; }

# tick <case dir> [VAR=value...]: one env -i run; stdout and stderr go to <dir>/out and <dir>/err,
# the exit status to <dir>/rc, so a missing script is an assertion failure and not an abort
tick() {
  local d="$1"; shift
  local rc=0
  env -i HOME="$d/home" TMPDIR="$d/tmp" PATH="$bin:$pydir:/usr/bin:/bin" LANG=C \
    DIRECTOR_STATE="$d/state" CALLS_LOG="$calls" TICK_NOW="$NOW" "$@" \
    "$TICK" >"$d/out" 2>"$d/err" || rc=$?
  printf '%s' "$rc" >"$d/rc"
}
status() { printf '%s/state/loop-status.json' "$1"; }

# jq-free JSON probe: pyq <file> <python expr over d>; prints the value, or MISSING when unreadable
pyq() { "$PY" -I -c '
import json,sys
try:
    d=json.load(open(sys.argv[1])); print(eval(sys.argv[2]))
except Exception:
    print("MISSING")' "$1" "$2"; }

# --- control: the fakes log, so "no call" below means no call ---------------------------------------
: >"$calls"
env -i PATH="$bin:/usr/bin:/bin" CALLS_LOG="$calls" director-mcp roster --json >/dev/null
env -i PATH="$bin:/usr/bin:/bin" CALLS_LOG="$calls" tmux list-sessions
grep -q '^director-mcp roster --json$' "$calls" && grep -q '^tmux list-sessions$' "$calls" \
  && okay "control: the fakes log every call" || miss "control: a fake did not log"

# --- status-written-and-valid-json ---------------------------------------------------------------
d="$(fresh_state basic)"; : >"$calls"; tick "$d"
s="$(status "$d")"
[[ "$(cat "$d/rc" 2>/dev/null || echo none)" == 0 ]] && okay "tick exits 0" || miss "tick exits 0 (rc $(cat "$d/rc" 2>/dev/null || echo none): $(head -c 200 "$d/err" 2>/dev/null))"
[[ "$(pyq "$s" 'd["schema"]')" == 1 ]] && okay "schema is 1" || miss "status-written-and-valid-json: schema 1 in $s"
[[ "$(pyq "$s" 'd["ran_at"]')" == "$NOW" ]] && okay "ran_at is the tick clock" || miss "ran_at equals TICK_NOW"
[[ "$(pyq "$s" 'sorted(d["steps"])')" == "['desk_refresh', 'presence', 'stalled_ideas', 'waiting_write']" ]] \
  && okay "the four steps are named" || miss "steps are desk_refresh, presence, stalled_ideas, waiting_write"
[[ "$(pyq "$s" 'all(v["status"] in ("ok","skipped","gap") for v in d["steps"].values())')" == True ]] \
  && okay "every step is ok, skipped or gap" || miss "step statuses are ok, skipped or gap"
[[ "$(pyq "$s" 'sorted(d["due"])')" == "['dashboard', 'desk_read', 'drain']" && "$(pyq "$s" 'all(isinstance(v,bool) for v in d["due"].values())')" == True ]] \
  && okay "due names drain, desk_read, dashboard as booleans" || miss "due names drain, desk_read, dashboard as booleans"
[[ "$(pyq "$s" '[(x["id"],x["cue"],x.get("cue_reason","")) for x in d["seats"]]')" == "[('alpha-1', 'live', ''), ('beta-2', 'unverified', 'no nonce echo after self-test')]" ]] \
  && okay "seats carry id, cue and cue_reason from the roster" || miss "seats carry id, cue and cue_reason from the roster"

# --- no-tmux-or-nats-call ----------------------------------------------------------------------------
if [[ -f "$s" ]] && ! grep -Eq '^(tmux|nats|nats-server) ' "$calls" && ! grep -E '^director-mcp ' "$calls" | grep -vqx 'director-mcp roster --json' ; then
  grep -q '^director-mcp roster --json$' "$calls" && okay "no tmux or nats call; presence read once through roster --json" \
    || miss "no-tmux-or-nats-call: presence was not read through director-mcp roster --json"
else
  miss "no-tmux-or-nats-call: a pane, bus or non-roster call was logged, or no status was written ($(tr '\n' ';' <"$calls"))"
fi

# --- dashboard-untouched --------------------------------------------------------------------------------
d="$(fresh_state dash)"; printf '{"last_run":"2026-11-18T01:00:00Z"}\n' >"$d/state/dash-writer.json"
before="$(shasum -a 256 <"$d/state/dash-writer.json")"; : >"$calls"; tick "$d"
[[ -f "$(status "$d")" ]] || miss "dashboard-untouched: no status was written"
after="$(shasum -a 256 <"$d/state/dash-writer.json")"
{ [[ "$before" == "$after" ]] && ! grep -q '^dash-writer ' "$calls" && [[ -f "$(status "$d")" ]]; } \
  && okay "dash-writer.json is byte-equal and dash-writer is never run" || miss "dashboard-untouched"

# --- idempotent -----------------------------------------------------------------------------------------
d="$(fresh_state idem)"; tick "$d"; cp "$(status "$d")" "$d/first.json" 2>/dev/null || : >"$d/first.json"
tick "$d"; cp "$(status "$d")" "$d/second.json" 2>/dev/null || : >"$d/second.json"
if [[ -s "$d/first.json" ]] && cmp -s "$d/first.json" "$d/second.json"; then okay "two runs at one clock are byte-identical"; else miss "idempotent: two runs at one clock differ or wrote nothing"; fi
tick "$d" TICK_NOW=2026-11-18T12:00:30Z
strip='json.dumps({k: v for k, v in d.items() if k != "ran_at"}, sort_keys=True)'
a="$(pyq "$d/first.json" "$strip")"; b="$(pyq "$(status "$d")" "$strip")"
{ [[ "$a" != MISSING && "$a" == "$b" ]] && [[ "$(pyq "$(status "$d")" 'd["ran_at"]')" == 2026-11-18T12:00:30Z ]]; } \
  && okay "a later run differs only in ran_at" || miss "idempotent: a later run differs in more than ran_at"

# --- each-step-gap-has-reason -----------------------------------------------------------------------------
d="$(fresh_state gaps)"; tick "$d" FAKE_ROSTER=fail; s="$(status "$d")"
[[ "$(pyq "$s" 'd["steps"]["presence"]["status"]')" == gap && "$(pyq "$s" 'len(d["steps"]["presence"]["reason"])>0')" == True && "$(pyq "$s" 'd["seats"]')" == "[]" ]] \
  && okay "a failing roster is a presence gap with a reason and no seats" || miss "each-step-gap-has-reason: presence gap with FAKE_ROSTER=fail"
[[ "$(pyq "$s" 'd["steps"]["stalled_ideas"]["status"]')" == gap && "$(pyq "$s" 'len(d["steps"]["stalled_ideas"]["reason"])>0')" == True ]] \
  && okay "no idea root named is a stalled_ideas gap with a reason, not a failed tick" || miss "each-step-gap-has-reason: stalled_ideas gap with the comment-only roots file"
[[ "$(pyq "$s" 'all(v["status"]=="ok" or len(v.get("reason",""))>0 for v in d["steps"].values())')" == True && "$(cat "$d/rc" 2>/dev/null || echo none)" == 0 ]] \
  && okay "every non-ok step carries a reason and the tick still exits 0" || miss "each-step-gap-has-reason: a non-ok step has no reason, or the tick failed"

# --- due-flags-from-stamps ------------------------------------------------------------------------------------
due() { pyq "$(status "$1")" "d[\"due\"][\"$2\"]"; }
d="$(fresh_state due0)"; tick "$d"
{ [[ "$(due "$d" drain)" == True && "$(due "$d" desk_read)" == True && "$(due "$d" dashboard)" == True ]]; } \
  && okay "with no stamp, all three are due" || miss "due-flags-from-stamps: no stamps should make all three due"
d="$(fresh_state due1)"
printf '2026-11-18T11:45:00Z\n' >"$d/state/last-drain"
: >"$d/state/desk-reads.jsonl"; TZ=UTC touch -t 202611171300 "$d/state/desk-reads.jsonl"
printf '{"last_run":"2026-11-18T01:00:00Z"}\n' >"$d/state/dash-writer.json"
tick "$d"
{ [[ "$(due "$d" drain)" == False && "$(due "$d" desk_read)" == False && "$(due "$d" dashboard)" == False ]]; } \
  && okay "drain 15m, desk_read 23h and dashboard 11h old are not due" || miss "due-flags-from-stamps: fresh stamps should be not due"
d="$(fresh_state due2)"
printf '2026-11-18T11:15:00Z\n' >"$d/state/last-drain"
: >"$d/state/desk-reads.jsonl"; TZ=UTC touch -t 202611171100 "$d/state/desk-reads.jsonl"
printf '{"last_run":"2026-11-17T23:00:00Z"}\n' >"$d/state/dash-writer.json"
tick "$d"
{ [[ "$(due "$d" drain)" == True && "$(due "$d" desk_read)" == True && "$(due "$d" dashboard)" == True ]]; } \
  && okay "drain 45m (default 30m), desk_read 25h (24h) and dashboard 13h (12h) are due" || miss "due-flags-from-stamps: stale stamps should be due at the 30m, 24h and 12h bounds"

# --- loop values read from [loop] -----------------------------------------------------------------------------
profile() { printf 'schema_version = 1\n[loop]\n%s\n' "$2" >"$1/profile.toml"; }
d="$(fresh_state prof)"
profile "$d" $'tick_interval = 10\ndigest_max_age = 48'
printf '2026-11-18T11:45:00Z\n' >"$d/state/last-drain"
printf '{"last_run":"2026-11-17T23:00:00Z"}\n' >"$d/state/dash-writer.json"
tick "$d" DIRECTOR_PROFILE="$d/profile.toml"
{ [[ "$(due "$d" drain)" == True && "$(due "$d" dashboard)" == False ]]; } \
  && okay "tick_interval = 10 makes a 15m stamp due, digest_max_age = 48 keeps a 13h one fresh" || miss "loop values: tick_interval and digest_max_age are not read from [loop]"

# --- positive-interval-refused --------------------------------------------------------------------------------
for bad in "tick_interval = 0" "tick_interval = -5" "digest_max_age = 0"; do
  d="$(fresh_state "neg")"; profile "$d" "$bad"
  printf '2026-11-18T11:45:00Z\n' >"$d/state/last-drain"
  tick "$d" DIRECTOR_PROFILE="$d/profile.toml"; s="$(status "$d")"
  { [[ "$(pyq "$s" 'd["steps"]["profile"]["status"]')" == gap ]] && [[ "$(pyq "$s" '"loop." in d["steps"]["profile"]["reason"]')" == True ]] \
    && [[ "$(due "$d" drain)" == False ]] && [[ "$(cat "$d/rc" 2>/dev/null || echo none)" == 0 ]]; } \
    && okay "'$bad' is a profile gap naming loop.<key>; the default bound still applies" || miss "positive-interval-refused: '$bad'"
done

# --- never-list ----------------------------------------------------------------------------------------------------
d="$(fresh_state never)"; printf '2026-11-18T11:00:00Z\n' >"$d/state/last-drain"
before="$(cat "$d/state/last-drain")"; : >"$calls"; tick "$d"
absent=1
for f in desk-snapshot.json desk-reads.jsonl dash-writer.json desk; do [[ -e "$d/state/$f" ]] && absent=0; done
{ [[ -f "$(status "$d")" ]] && [[ "$absent" == 1 ]] && [[ "$(cat "$d/state/last-drain")" == "$before" ]] \
  && ! grep -Eq '^(tmux|nats|nats-server|dash-writer) ' "$calls" && ! grep -E '^gh ' "$calls" | grep -Eq '(pr (merge|comment|review|create)|issue|api .*(-X|--method))'; } \
  && okay "no Desk file, no last-drain write, no pane, bus, dashboard or GitHub write" || miss "never-list: the tick wrote the Desk or last-drain, or called a pane, bus, dashboard or a GitHub write"

# --- no temp file left behind ---------------------------------------------------------------------------------------
left="$(find "$d/state" -name '.loop-status*' -o -name 'loop-status.json.*' 2>/dev/null | head -1)"
{ [[ -f "$(status "$d")" ]] && [[ -z "$left" ]]; } && okay "the status is renamed into place; no temp file remains" || miss "temp file: status missing or a temp file remains ($left)"

((fail == 0)) && { echo "ok: director-tick verified"; exit 0; }
echo "director-tick: $fail group(s) missed"; exit 1
