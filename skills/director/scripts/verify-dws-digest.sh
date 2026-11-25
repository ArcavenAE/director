#!/usr/bin/env bash
# Prove `dws digest` (K18, sim/design/director-functions-redesign.md, rows 8 and 9; Q1 and Q7 as
# ruled): an operator digest and a close-or-kill list rendered from the reconciler and the queue,
# on demand only (nothing runs on a clock). It prints to stdout and writes the same text to
# $DIRECTOR_STATE/digest.md. The interrupt list is the Q1 default (an outage; a console step or
# recommendation expiring before the next digest; a seat blocked on your ask); an item whose
# source cannot be read says so and is never "none". It writes digest.md and nothing else.
#
# Usage: skills/director/scripts/verify-dws-digest.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DWS="${DWS:-$here/dws}"
SNAP="${SNAP_BIN:-$here/desk-snapshot}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
export DWS_TEST_CLOCK=1 DWS_NOW=2026-10-20T12:00:00Z DWS_NO_RERENDER=1 PYTHONDONTWRITEBYTECODE=1
export DIRECTOR_STATE="$root/state"; mkdir -p "$DIRECTOR_STATE"
LEDGER="$DIRECTOR_STATE/workstreams.jsonl"; RULINGS="$DIRECTOR_STATE/rulings.jsonl"; DIGEST="$DIRECTOR_STATE/digest.md"
section() { sed -n "/^## $1/,/^## /p" "$DIGEST" | sed '$d'; }

# --- nothing readable ---------------------------------------------------------------------------
"$DWS" digest >"$root/o0" 2>"$root/e0" && ok "digest exits 0 with no stores" || bad "digest exits 0 with no stores" "$(cat "$root/e0")"
cmp -s "$root/o0" "$DIGEST" && ok "stdout and digest.md are the same text" || bad "stdout equals digest.md"
grep -q 'UNREAD' "$root/o0" && ok "unread sources are said, not omitted" || bad "UNREAD said" "$(cat "$root/o0")"
section Interrupts | grep -qi 'blocked on your ask.*UNREAD' && ok "with the Desk UNREAD, 'a seat blocked on your ask' is UNREAD, not none" || bad "interrupt UNREAD" "$(section Interrupts)"
[[ ! -e "$LEDGER" && ! -e "$RULINGS" ]] && ok "digest creates neither the ledger nor the rulings file" || bad "digest creates no store"

# --- fixtures --------------------------------------------------------------------------------------------
printf '{"c1":{"status":"open"},"c2":{"status":"open"}}' >"$root/cards.json"
"$SNAP" write --cards "$root/cards.json" --reported 2 >/dev/null
"$DWS" open alpha --title a --owner director --stage building --blocked operator --ask c1 >/dev/null
DWS_NOW=2026-09-01T00:00:00Z "$DWS" open oldidea --title "an old idea" --owner director --stage idea >/dev/null
DWS_NOW=2026-10-19T00:00:00Z "$DWS" open fresh --title f --owner director --stage building >/dev/null
printf '%s\n' '{"kind":"grant","id":"g-old","ts":"2026-09-01T00:00:00Z","actor":"director","tag":"mirror","source":"s","subject":"x","scope":"y","to":"director","state":"confirmed","text":"t","expires":"2026-10-01"}' >"$RULINGS"
before="$(shasum -a 256 "$LEDGER" "$RULINGS" "$DIRECTOR_STATE/desk-snapshot.json")"
manifest() { (cd "$DIRECTOR_STATE" && find . -type f ! -name digest.md ! -name 'digest.md.lock' -exec shasum -a 256 {} + | LC_ALL=C sort); }
mbefore="$(manifest)"
"$DWS" digest >"$root/o" 2>"$root/e" && ok "digest exits 0 on fixtures" || bad "digest exits 0" "$(cat "$root/e")"
cmp -s "$root/o" "$DIGEST" && ok "stdout and digest.md are the same text" || bad "stdout equals digest.md"
[[ "$(shasum -a 256 "$LEDGER" "$RULINGS" "$DIRECTOR_STATE/desk-snapshot.json")" == "$before" ]] && ok "digest writes no ledger, ruling or snapshot" || bad "digest writes no store"
grep -q '^## Interrupts' "$DIGEST" && grep -q '^## Queue' "$DIGEST" && grep -q '^## Close-or-kill' "$DIGEST" && grep -q '^## Not run' "$DIGEST" && ok "the digest has its four sections" || bad "sections" "$(grep '^##' "$DIGEST")"
section Interrupts | grep -q 'alpha.*c1' && ok "a seat blocked on your ask is listed with its row and card" || bad "blocked on your ask" "$(section Interrupts)"
section Interrupts | grep -qi 'default list' && ok "the interrupt list is labelled as the default list" || bad "default list label"
section Interrupts | grep -qi 'outage.*not run' && section Interrupts | grep -qi 'expiring.*not run' && ok "the interrupt items with no source say 'not run'" || bad "not-run interrupt items" "$(section Interrupts)"
section Queue | grep -q '2 open' && ok "the queue section carries the open count" || bad "queue count" "$(section Queue)"
section Close-or-kill | grep -q 'oldidea' && ok "close-or-kill lists the unmoved row" || bad "close-or-kill lists oldidea" "$(section Close-or-kill)"
section Close-or-kill | grep 'oldidea' | grep -qi 'idea' && section Close-or-kill | grep 'oldidea' | grep -q '[0-9][0-9]* days' && ok "an idea row carries its age in days" || bad "idea age line" "$(section Close-or-kill)"
section Close-or-kill | grep -q 'fresh' && bad "a moved row is not listed" || ok "a recently moved row is not listed"
grep -q 'grant-expired' "$DIGEST" && grep -q 'g-old' "$DIGEST" && ok "other reconcile proposals are listed (the expired grant)" || bad "other proposals"
section 'Not run' | grep -q 'recommendation-sell-by' && ok "the reconciler's not-run checks are carried" || bad "not run carried" "$(section 'Not run')"
[[ "$(manifest)" == "$mbefore" ]] && ok "the state directory holds nothing new and nothing changed but digest.md (review 5586720919)" || bad "digest wrote something besides digest.md" "$(diff <(echo "$mbefore") <(manifest) | head -5)"
[[ ! -e "$DIRECTOR_STATE/reconcile-proposals.json" ]] && ok "digest does not write reconcile-proposals.json" || bad "reconcile-proposals.json written by digest"
"$DWS" digest >"$root/o2" 2>/dev/null; cmp -s "$root/o" "$root/o2" && ok "a second run under the same clock is byte for byte the same" || bad "idempotent"

# --- the Q1 default list is documented where the operator edits it -----------------------------------------
grep -q 'DIGEST_INTERRUPTS' "$DWS" && ok "the default interrupt list is a named table in dws" || bad "DIGEST_INTERRUPTS table"
grep -q 'Q1' "$DWS" && ok "dws cites Q1 for it" || bad "cites Q1"

# --- runs-with-no-session: the tick writes the digest (slice 17, operator card i652r-1, choice 1) ---------------------
# director-tick runs `dws digest` as its step `digest` when digest.md is absent or older than digest_max_age hours
# (12 by default, from [loop]); otherwise the step is skipped as current. No session, pane, bus send or dashboard
# writer is involved: every run is env -i with a scratch HOME, TMPDIR and DIRECTOR_STATE, and tmux, nats, nats-server,
# gh, dash-writer and director-mcp on PATH are fakes that log each call.
TICK="${DIRECTOR_TICK_BIN:-$here/director-tick}"
PY="$(command -v python3)"; pydir="$(dirname "$PY")"
tnow=2026-11-18T12:00:00Z
tbin="$root/tickbin"; mkdir -p "$tbin"; tcalls="$root/tick-calls.log"
for t in tmux nats nats-server dash-writer; do printf '#!/bin/sh\nprintf "%s %%s\\n" "$*" >>"$CALLS_LOG"\nexit 0\n' "$t" >"$tbin/$t"; done
printf '#!/bin/sh\nprintf "gh %%s\\n" "$*" >>"$CALLS_LOG"\necho "[]"\n' >"$tbin/gh"
printf '#!/bin/sh\nprintf "director-mcp %%s\\n" "$*" >>"$CALLS_LOG"\necho "{\\"rows\\":[]}"\n' >"$tbin/director-mcp"
chmod +x "$tbin"/*
# tick_case <name> [VAR=value...]: a fresh scratch state, the tick run once; the case dir is printed
tick_case() {
  local n="$1"; shift; local d="$root/tk-$n"; rm -rf "$d"; mkdir -p "$d/home" "$d/tmp" "$d/state"; printf '%s' "$d"
}
run_tick() { # <dir> [VAR=value...]
  local d="$1"; shift; local rc=0
  env -i HOME="$d/home" TMPDIR="$d/tmp" PATH="$tbin:$pydir:/usr/bin:/bin" LANG=C DIRECTOR_STATE="$d/state" CALLS_LOG="$tcalls" \
    TICK_NOW="$tnow" DWS_TEST_CLOCK=1 DWS_NOW="$tnow" DWS_NO_RERENDER=1 PYTHONDONTWRITEBYTECODE=1 "$@" "$TICK" >"$d/out" 2>"$d/err" || rc=$?
  printf '%s' "$rc" >"$d/rc"
}
tq() { "$PY" -I -c '
import json,sys
try:
    d=json.load(open(sys.argv[1])); print(eval(sys.argv[2]))
except Exception:
    print("MISSING")' "$1/state/loop-status.json" "$2"; }

d="$(tick_case absent)"; : >"$tcalls"; run_tick "$d"
[[ "$(tq "$d" 'd["steps"]["digest"]["status"]')" == ok ]] && ok "runs-with-no-session: with no digest.md the tick's digest step is ok" || bad "runs-with-no-session: digest step ok with no digest.md" "$(tq "$d" 'd["steps"].get("digest")') rc $(cat "$d/rc") $(head -c 200 "$d/err")"
{ [[ -s "$d/state/digest.md" ]] && head -1 "$d/state/digest.md" | grep -q '^# Digest'; } && ok "runs-with-no-session: the tick wrote digest.md, a digest" || bad "runs-with-no-session: digest.md written by the tick"
{ ! grep -Eq '^(tmux|nats|nats-server|dash-writer) ' "$tcalls" && ! grep -E '^director-mcp ' "$tcalls" | grep -vqx 'director-mcp roster --json'; } \
  && ok "runs-with-no-session: no pane, bus, dashboard or non-roster call" || bad "runs-with-no-session: the tick called a pane, the bus or the dashboard writer" "$(tr '\n' ';' <"$tcalls")"
[[ "$(cat "$d/rc")" == 0 ]] && ok "runs-with-no-session: the tick exits 0" || bad "runs-with-no-session: tick exit"

# a digest younger than digest_max_age is left alone
d="$(tick_case fresh)"; printf 'SENTINEL\n' >"$d/state/digest.md"; TZ=UTC touch -t 202611180900 "$d/state/digest.md"   # 3h before tnow
st0="$("$PY" -I -c 'import os,sys; s=os.stat(sys.argv[1]); print(s.st_ino, s.st_size, s.st_mtime_ns)' "$d/state/digest.md")"
run_tick "$d"
st1="$("$PY" -I -c 'import os,sys; s=os.stat(sys.argv[1]); print(s.st_ino, s.st_size, s.st_mtime_ns)' "$d/state/digest.md")"
{ [[ "$(tq "$d" 'd["steps"]["digest"]["status"]')" == skipped && "$(tq "$d" '"current" in d["steps"]["digest"]["reason"]')" == True && "$st0" == "$st1" && "$(cat "$d/state/digest.md")" == SENTINEL ]]; } \
  && ok "runs-with-no-session: a 3 hour old digest.md is left alone and the step says it is current" || bad "runs-with-no-session: a 3 hour old digest.md should be left alone, step skipped as current ($st0 / $st1)"

# a digest older than the default 12 hours is rewritten
d="$(tick_case stale)"; printf 'SENTINEL\n' >"$d/state/digest.md"; TZ=UTC touch -t 202611172300 "$d/state/digest.md"   # 13h before tnow
run_tick "$d"
{ [[ "$(tq "$d" 'd["steps"]["digest"]["status"]')" == ok ]] && head -1 "$d/state/digest.md" | grep -q '^# Digest'; } \
  && ok "runs-with-no-session: a 13 hour old digest.md is rewritten" || bad "runs-with-no-session: a 13 hour old digest.md should be rewritten by the tick"

# the default bound is exactly 12 hours: 11 hours is current, 12 hours exactly is current, 12 hours and a minute is stale
for spec in "11h 202611180100 skipped" "12h 202611180000 skipped" "12h1m 202611172359 ok"; do
  set -- $spec; label="$1"; stamp="$2"; want="$3"
  d="$(tick_case "bound_$label")"; printf 'SENTINEL\n' >"$d/state/digest.md"; TZ=UTC touch -t "$stamp" "$d/state/digest.md"
  run_tick "$d"
  [[ "$(tq "$d" 'd["steps"]["digest"]["status"]')" == "$want" ]] \
    && ok "runs-with-no-session: a digest.md $label old is $want at the default 12 hour bound" || bad "runs-with-no-session: a digest.md $label old should be $want at the default bound (got $(tq "$d" 'd["steps"].get("digest")'))"
done

# the success path runs `dws digest` itself (a wrapper beside the tick logs the call and hands on to the real dws)
d="$(tick_case wrap)"; mkdir -p "$d/beside"; cp "$TICK" "$d/beside/director-tick"
for t in dws desk-snapshot dbi director-profile; do [[ -x "$here/$t" ]] && printf '#!/bin/sh\nprintf "%s %%s\\n" "$*" >>"$DWS_LOG"\nexec "%s/%s" "$@"\n' "$t" "$here" "$t" >"$d/beside/$t"; done
chmod +x "$d/beside"/*; : >"$d/dws.log"
rc=0; env -i HOME="$d/home" TMPDIR="$d/tmp" PATH="$tbin:$pydir:/usr/bin:/bin" LANG=C DIRECTOR_STATE="$d/state" CALLS_LOG="$tcalls" DWS_LOG="$d/dws.log" \
  TICK_NOW="$tnow" DWS_TEST_CLOCK=1 DWS_NOW="$tnow" DWS_NO_RERENDER=1 PYTHONDONTWRITEBYTECODE=1 "$d/beside/director-tick" >"$d/out" 2>"$d/err" || rc=$?
{ [[ "$(grep -cx 'dws digest' "$d/dws.log")" == 1 ]] && head -1 "$d/state/digest.md" | grep -q '^# Digest'; } \
  && ok "runs-with-no-session: the tick ran dws digest once, and digest.md is its output" || bad "runs-with-no-session: dws digest should run exactly once in the absent case ($(tr '\n' ';' <"$d/dws.log"))"

# digest_max_age comes from [loop]
d="$(tick_case profile)"; printf 'schema_version = 1\n[loop]\ndigest_max_age = 48\n' >"$d/profile.toml"
printf 'SENTINEL\n' >"$d/state/digest.md"; TZ=UTC touch -t 202611172300 "$d/state/digest.md"
run_tick "$d" DIRECTOR_PROFILE="$d/profile.toml"
{ [[ "$(tq "$d" 'd["steps"]["digest"]["status"]')" == skipped && "$(cat "$d/state/digest.md")" == SENTINEL ]]; } \
  && ok "runs-with-no-session: digest_max_age = 48 keeps a 13 hour old digest.md" || bad "runs-with-no-session: digest_max_age from [loop] should keep a 13 hour old digest"

# a failing dws is a gap with a reason and does not fail the tick
d="$(tick_case gap)"; mkdir -p "$d/beside"; cp "$TICK" "$d/beside/director-tick"
printf '#!/bin/sh\n[ "$1" = digest ] && { echo "dws digest: store unreadable" >&2; exit 1; }\nexit 0\n' >"$d/beside/dws"; chmod +x "$d/beside"/*
rc=0; env -i HOME="$d/home" TMPDIR="$d/tmp" PATH="$tbin:$pydir:/usr/bin:/bin" LANG=C DIRECTOR_STATE="$d/state" CALLS_LOG="$tcalls" TICK_NOW="$tnow" "$d/beside/director-tick" >"$d/out" 2>"$d/err" || rc=$?
{ [[ "$rc" == 0 && "$(tq "$d" 'd["steps"]["digest"]["status"]')" == gap && "$(tq "$d" '"unreadable" in d["steps"]["digest"]["reason"]')" == True ]]; } \
  && ok "runs-with-no-session: a failing dws digest is a gap with its first error line, and the tick exits 0" || bad "runs-with-no-session: a failing dws digest should be a gap with a reason and exit 0 (rc $rc)"

echo "dws digest: $pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
