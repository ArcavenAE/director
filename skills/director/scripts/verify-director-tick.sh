#!/usr/bin/env bash
# Prove director-tick, slice 5 of sim/design/director-standing-loop.md (section 2, "The tick", and
# director's S5 rulings recorded on aae-orc-5icwm). Red first: until director-tick exists every case
# below reports MISS, and each MISS is an assertion on a missing output, not a build error.
#
# The tick is one pass. It reads and computes, and writes $DIRECTOR_STATE/loop-status.json:
#   {"schema":1,"ran_at":"<UTC>","steps":{"<step>":{"status":"ok|skipped|gap","reason":"..."}},
#    "due":{"drain":bool,"desk_read":bool,"dashboard":bool},
#    "seats":[{"id","cue","cue_reason"}]}
# steps: desk_refresh, waiting_write, stalled_ideas, presence, decided_not_built (and profile, only when a [loop] value
# is refused). It never types into a pane, never sends or acks on the bus, never writes the Desk, and
# never runs the dashboard writer.
#
# Run conditions: every run is env -i with a scratch HOME, a short scratch TMPDIR and a scratch
# DIRECTOR_STATE, no MARVEL_*, DIRECTOR_* (beyond DIRECTOR_STATE and DIRECTOR_PROFILE set inside
# the scratch tree) or NATS_* variable. tmux, nats, nats-server, gh, dash-writer and director-mcp on
# PATH are fakes that log every call, so a call to the live bus, a pane or GitHub is visible and none
# is made. Nothing is written outside the scratch tree. The clock is TICK_NOW, so ages are exact.
#
# Covered: decided_not_built (seed rows read into the status); status-written-and-valid-json; no-tmux-or-nats-call; dashboard-untouched; idempotent;
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
# It holds a current digest.md: the tick writes a missing or old digest (slice 17), and a first run that wrote one
# would change an input of the second, so two runs at one clock would differ for a reason these cases do not test.
# The digest step is proved in verify-dws-digest.sh.
fresh_state() { local d="$work/$1"; rm -rf "$d"; mkdir -p "$d/home" "$d/tmp" "$d/state"; printf 'a current digest\n' >"$d/state/digest.md"; printf '%s' "$d"; }

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
[[ "$(pyq "$s" 'sorted(d["steps"])')" == "['decided_not_built', 'desk_refresh', 'digest', 'presence', 'stalled_ideas', 'waiting_write']" ]] \
  && okay "the six steps are named" || miss "steps are decided_not_built, desk_refresh, digest, presence, stalled_ideas, waiting_write"
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

# --- decided_not_built: the seed file's rows, read into the status (aae-orc I-652, director's call) ----------------
# $DIRECTOR_STATE/desk/decided-not-built.seed.jsonl is director's file. The step reads it and writes nothing to it.
# Absent file: the step is skipped with a reason and the status has no "decided_not_built" key. Readable file:
# step ok and "decided_not_built": {"count": N, "rows": [<row objects in file order>]}. A blank line is ignored.
# A line that is not a JSON object with a non-empty string "card" makes the step a gap whose reason names the
# 1-based line number, one "line <n>: <why>" entry per bad line; the valid lines are still counted and carried, and the tick still exits 0.
# "card" is the only required key. "ruled_at" and "forward" (a bd:, pr: or finding: edge) are optional and carried
# verbatim; a row without them is not a gap (director, 2026-11-25).
seed() { mkdir -p "$1/state/desk"; printf '%s' "$2" >"$1/state/desk/decided-not-built.seed.jsonl"; }
dnb() { pyq "$(status "$1")" "$2"; }
FIX2=$'{"card":"ops2-desk-store","ruled_at":"2026-10-10","forward":"pr:ArcavenAE/director#699"}\n{"card":"flow1027-1","note":"timer"}\n'

d="$(fresh_state dnb_absent)"; tick "$d"; s="$(status "$d")"
{ [[ "$(pyq "$s" 'd["steps"]["decided_not_built"]["status"]')" == skipped && "$(pyq "$s" 'len(d["steps"]["decided_not_built"]["reason"])>0')" == True \
  && "$(pyq "$s" '"decided_not_built" in d')" == False ]]; } \
  && okay "decided_not_built: no seed file is a skipped step with a reason and no top-level key" || miss "decided_not_built: absent seed file should be skipped with a reason and no top-level key"

d="$(fresh_state dnb_two)"; seed "$d" "$FIX2"; tick "$d"; s="$(status "$d")"
[[ "$(pyq "$s" 'd["steps"]["decided_not_built"]')" == "{'status': 'ok'}" ]] \
  && okay "decided_not_built: a 2-row seed is an ok step" || miss "decided_not_built: a 2-row seed should be an ok step"
[[ "$(pyq "$s" 'd["decided_not_built"]["count"]')" == 2 ]] \
  && okay "decided_not_built: count is 2" || miss "decided_not_built: count should be 2"
[[ "$(pyq "$s" '[r["card"] for r in d["decided_not_built"]["rows"]]')" == "['ops2-desk-store', 'flow1027-1']" ]] \
  && okay "decided_not_built: rows keep file order" || miss "decided_not_built: rows should keep file order"
[[ "$(pyq "$s" 'd["decided_not_built"]["rows"][0]["ruled_at"] + d["decided_not_built"]["rows"][0]["forward"] + d["decided_not_built"]["rows"][1]["note"]')" == "2026-10-10pr:ArcavenAE/director#699timer" ]] \
  && okay "decided_not_built: optional ruled_at and forward, and other keys, ride along verbatim" || miss "decided_not_built: other row keys should be carried verbatim"

d="$(fresh_state dnb_blank)"; seed "$d" $'\n{"card":"a"}\n   \n{"card":"b"}\n\n'; tick "$d"; s="$(status "$d")"
{ [[ "$(pyq "$s" 'd["steps"]["decided_not_built"]["status"]')" == ok && "$(pyq "$s" 'd["decided_not_built"]["count"]')" == 2 ]]; } \
  && okay "decided_not_built: blank lines are ignored" || miss "decided_not_built: blank lines should be ignored"

d="$(fresh_state dnb_empty)"; seed "$d" ""; tick "$d"; s="$(status "$d")"
{ [[ "$(pyq "$s" 'd["steps"]["decided_not_built"]["status"]')" == ok && "$(pyq "$s" 'd["decided_not_built"]')" == "{'count': 0, 'rows': []}" ]]; } \
  && okay "decided_not_built: an empty seed is ok with count 0" || miss "decided_not_built: an empty seed should be ok with count 0 and no rows"

# bad lines: line 2 is not JSON, line 3 is JSON without a card, line 4 is an array, line 5 has an empty card
d="$(fresh_state dnb_bad)"
seed "$d" $'{"card":"good1"}\nnot json at all\n{"note":"no card"}\n["card"]\n{"card":""}\n{"card":"good2"}\n'
tick "$d"; s="$(status "$d")"
{ [[ "$(pyq "$s" 'd["steps"]["decided_not_built"]["status"]')" == gap && "$(cat "$d/rc" 2>/dev/null || echo none)" == 0 ]]; } \
  && okay "decided_not_built: bad lines make a gap and the tick still exits 0" || miss "decided_not_built: bad lines should make a gap with exit 0"
{ [[ "$(pyq "$s" 'all(__import__("re").search(r"\bline %d\b" % n, d["steps"]["decided_not_built"]["reason"]) for n in (2,3,4,5))')" == True \
  && "$(pyq "$s" 'any(__import__("re").search(r"\bline %d\b" % n, d["steps"]["decided_not_built"]["reason"]) for n in (1,6))')" == False ]]; } \
  && okay "decided_not_built: the gap names line 2, 3, 4 and 5 as whole tokens and not the good lines 1 and 6" || miss "decided_not_built: the gap reason should hold the tokens line 2 to line 5 and neither line 1 nor line 6"
[[ "$(pyq "$s" '[d["decided_not_built"]["count"], [r["card"] for r in d["decided_not_built"]["rows"]]]')" == "[2, ['good1', 'good2']]" ]] \
  && okay "decided_not_built: the valid lines are still counted and carried" || miss "decided_not_built: valid lines should still be counted and carried"

# a line that does not decode as UTF-8 is a bad line: a gap naming line 2, and only line 1 is carried
d="$(fresh_state dnb_byte)"; mkdir -p "$d/state/desk"; printf '{"card":"a"}\n{"card":"\377"}\n' >"$d/state/desk/decided-not-built.seed.jsonl"
tick "$d"; s="$(status "$d")"
{ [[ "$(cat "$d/rc" 2>/dev/null || echo none)" == 0 && "$(pyq "$s" 'd["steps"]["decided_not_built"]["status"]')" == gap \
  && "$(pyq "$s" '__import__("re").search(r"\bline 2\b", d["steps"]["decided_not_built"]["reason"]) is not None')" == True \
  && "$(pyq "$s" '[d["decided_not_built"]["count"], [r["card"] for r in d["decided_not_built"]["rows"]]]')" == "[1, ['a']]" ]]; } \
  && okay "decided_not_built: an undecodable line is a gap naming line 2, and only the good row is carried" || miss "decided_not_built: an invalid byte should be a gap naming line 2 with count 1 and rows ['a'] (rc $(cat "$d/rc" 2>/dev/null || echo none))"

# a bad line after a blank line is numbered by its place in the file, blank lines counted
d="$(fresh_state dnb_blankbad)"; seed "$d" $'{"card":"a"}\n\nnot json\n{"card":"b"}\n'
tick "$d"; s="$(status "$d")"
{ [[ "$(pyq "$s" 'd["steps"]["decided_not_built"]["status"]')" == gap \
  && "$(pyq "$s" '__import__("re").search(r"\bline 3\b", d["steps"]["decided_not_built"]["reason"]) is not None and __import__("re").search(r"\bline 2\b", d["steps"]["decided_not_built"]["reason"]) is None')" == True \
  && "$(pyq "$s" 'd["decided_not_built"]["count"]')" == 2 ]]; } \
  && okay "decided_not_built: a bad line after a blank line is numbered by its place in the file (line 3)" || miss "decided_not_built: a bad line after a blank line should be line 3, with line 2 not named and count 2"

# idempotent; and the seed is never written: it is made read-only before the tick, and its inode, size,
# mtime and mode must be what they were after two ticks as well as its bytes
seedfile() { printf '%s/state/desk/decided-not-built.seed.jsonl' "$1"; }
sig() { "$PY" -I -c 'import os,sys,hashlib
p=sys.argv[1]; st=os.stat(p); print(st.st_ino, st.st_size, st.st_mtime_ns, oct(st.st_mode), hashlib.sha256(open(p,"rb").read()).hexdigest())' "$1" 2>/dev/null || echo UNREADABLE; }
# seed_untouched <dir> <sig before>: the seed has the same signature, the status read it ok with 2 rows, and desk/ holds only it
seed_untouched() {
  [[ "$(sig "$(seedfile "$1")")" == "$2" && "$2" != UNREADABLE ]] \
    && [[ "$(pyq "$(status "$1")" 'd["steps"]["decided_not_built"]["status"]')" == ok && "$(pyq "$(status "$1")" 'd["decided_not_built"]["count"]')" == 2 ]] \
    && [[ "$(find "$1/state/desk" -type f | wc -l | tr -d ' ')" == 1 ]]
}
d="$(fresh_state dnb_idem)"; seed "$d" "$FIX2"; chmod a-w "$(seedfile "$d")"; before="$(sig "$(seedfile "$d")")"
tick "$d"; cp "$(status "$d")" "$d/first.json" 2>/dev/null || : >"$d/first.json"; tick "$d"
{ [[ -s "$d/first.json" ]] && cmp -s "$d/first.json" "$(status "$d")" && grep -q decided_not_built "$d/first.json"; } \
  && okay "decided_not_built: two runs at one clock are byte-identical" || miss "decided_not_built: two runs at one clock differ, or the step is absent"
seed_untouched "$d" "$before" && okay "decided_not_built: a read-only seed keeps its inode, size, mtime, mode and bytes across two ticks, and nothing else lands in desk/" \
  || miss "decided_not_built: the tick changed the seed (inode, size, mtime, mode or bytes), failed to read it, or wrote into desk/"
{ ! grep -Eq '^(tmux|nats|nats-server|dash-writer) ' "$calls" && grep -q decided_not_built "$d/first.json"; } \
  && okay "decided_not_built: the step calls no pane, bus or dashboard" || miss "decided_not_built: a pane, bus or dashboard call was logged, or the step is absent"

# mutants: a copy of the tick with one fault each must be caught by the checks above. Each mutant is made by one
# exact text replacement; if the replacement text is not in the tick, the mutant is a miss, not a pass.
mutant_tick() { # <name> <old> <new>: prints the path of the mutated copy, or nothing
  local out="$work/mutant-$1"; mkdir -p "$out"
  "$PY" -I -c '
import sys
src=open(sys.argv[1]).read()
if src.count(sys.argv[2]) != 1: sys.exit(1)
open(sys.argv[4],"w").write(src.replace(sys.argv[2], sys.argv[3]))' "$TICK" "$2" "$3" "$out/director-tick" && chmod +x "$out/director-tick" && printf '%s' "$out/director-tick"
}
READ_OLD='        with open(path, "rb") as f:
            raw = f.read()'
mutants_caught() { # <name> <old> <new> <check: seed|blank>
  local m saved="$TICK"; m="$(mutant_tick "$1" "$2" "$3")" || m=""
  if [[ -z "$m" ]]; then miss "mutant $1: the text to mutate is not in the tick"; return; fi
  if [[ "$4" == seed ]]; then
    local md; md="$(fresh_state "mut_$1")"; seed "$md" "$FIX2"; chmod a-w "$(seedfile "$md")"; local mb; mb="$(sig "$(seedfile "$md")")"
    TICK="$m"; tick "$md"; TICK="$saved"
    if seed_untouched "$md" "$mb"; then miss "mutant $1 survives: the seed checks did not catch it"; else okay "mutant $1 is caught by the seed checks"; fi
  else
    local md; md="$(fresh_state "mut_$1")"; seed "$md" $'{"card":"a"}\n\nnot json\n{"card":"b"}\n'
    TICK="$m"; tick "$md"; TICK="$saved"
    if [[ "$(pyq "$(status "$md")" '__import__("re").search(r"\bline 3\b", d["steps"]["decided_not_built"]["reason"]) is not None')" == True ]]; then miss "mutant $1 survives: the blank-line case did not catch it"; else okay "mutant $1 is caught by the blank-line case"; fi
  fi
}
mutants_caught rewrite "$READ_OLD" "$READ_OLD
        open(path, \"wb\").write(raw)" seed
mutants_caught append "$READ_OLD" "$READ_OLD
        open(path, \"ab\").close()" seed
mutants_caught touch "$READ_OLD" "$READ_OLD
        os.utime(path, (1, 1))" seed
mutants_caught blanknumbering 'enumerate(raw.split(b"\n"), 1)' 'enumerate([c for c in raw.split(b"\n") if c.strip()], 1)' blank

# --- a step that prints an invalid byte is still a step -----------------------------------------------------------
# dws and dbi run from beside the tick, so a copy of the tick sits beside fakes that write a byte that is not
# UTF-8 to stdout and stderr, once failing (exit 1) and once succeeding (exit 0). The tick must still exit 0,
# write the status, and show the step as ok, or as a gap with a reason.
for mode in 1 0; do
  d="$(fresh_state "badbyte$mode")"; mkdir -p "$d/beside"; cp "$TICK" "$d/beside/director-tick" 2>/dev/null || :
  for t in dws dbi; do
    printf '#!/bin/sh\nprintf "\\377 bad out\\n"\nprintf "\\377 bad err\\n" >&2\nexit %s\n' "$mode" >"$d/beside/$t"
  done
  chmod +x "$d/beside"/* 2>/dev/null || :
  rc=0; env -i HOME="$d/home" TMPDIR="$d/tmp" PATH="$bin:$pydir:/usr/bin:/bin" LANG=C DIRECTOR_STATE="$d/state" CALLS_LOG="$calls" TICK_NOW="$NOW" \
    "$d/beside/director-tick" >"$d/out" 2>"$d/err" || rc=$?
  s="$(status "$d")"
  { [[ "$rc" == 0 ]] && [[ "$(pyq "$s" 'all(v["status"]=="ok" or (v["status"] in ("gap","skipped") and len(v["reason"])>0) for k,v in d["steps"].items() if k in ("decided_not_built","digest")) and all(v["status"]=="ok" or (v["status"]=="gap" and len(v["reason"])>0) for k,v in d["steps"].items() if k not in ("decided_not_built","digest"))')" == True ]] \
    && [[ "$(pyq "$s" 'd["steps"]["desk_refresh"]["status"] in ("ok","gap")')" == True ]]; } \
    && okay "invalid output bytes from dws and dbi (exit $mode) leave the tick at exit 0 with a status" \
    || miss "invalid-byte: a step printing a non-UTF-8 byte (exit $mode) failed the tick or left no status (rc $rc: $(head -c 160 "$d/err" 2>/dev/null))"
done

((fail == 0)) && { echo "ok: director-tick verified"; exit 0; }
echo "director-tick: $fail group(s) missed"; exit 1
