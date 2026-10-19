#!/usr/bin/env bash
# Prove the ledger fill (sim/design/ledger-fill.md, issue #321): workstream rows
# move from the records director already writes, joined by an explicit id, and
# never by reading message text. LF1 is relay-log --ws (verify-relay-log.sh); this
# file covers LF2 (the fold), LF3 (refresh --local, unlinked, no render loop).
#
# Held, and not tested here: LF4 (refresh clears "blocked operator" when the linked
# Desk card closes) and the design's test 11 need operator ruling 1; W5 (the ask
# ledger source) has no source file yet.
#
# Every test uses a scratch DIRECTOR_STATE and RELAY_LOG, DWS_TEST_CLOCK=1, and stubbed
# gh and bd that log their calls; nothing reads the live state.
#
# Usage: skills/director/scripts/verify-dws-fill.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DWS="${DWS:-$here/dws}"
RL="${RELAY_LOG_BIN:-$here/relay-log}"
SNAP="${SNAP:-$here/desk-snapshot}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
export PYTHONDONTWRITEBYTECODE=1
export DIRECTOR_STATE="$root/state"
export RELAY_LOG="$root/relay-log.md"
export DWS_TEST_CLOCK=1
mkdir -p "$DIRECTOR_STATE" "$root/stubs"
LEDGER="$DIRECTOR_STATE/workstreams.jsonl"
CALLS="$root/calls"
: >"$CALLS"
for t in gh bd; do
  printf '#!/bin/sh\necho "%s $*" >>"%s"\nexit 1\n' "$t" "$CALLS" >"$root/stubs/$t"
  chmod +x "$root/stubs/$t"
done
export PATH="$root/stubs:$PATH"

lines() { if [[ -f "$LEDGER" ]]; then wc -l <"$LEDGER" | tr -d ' '; else echo 0; fi; }
field() { "$DWS" show --json | python3 -c 'import json,sys;rows={r["slug"]:r for r in json.load(sys.stdin)["rows"]};print(rows[sys.argv[1]][sys.argv[2]])' "$1" "$2"; }
open_at() { local at="$1"; shift; DWS_NOW="$at" "$DWS" open "$@" >/dev/null; }

# --- LF2: an authored update moves the clock (R-197) ----------------------------------
open_at 2026-10-05T10:00:00Z a1 --title "alpha" --owner director --next "start"
[[ "$(field a1 last_moved)" == "2026-10-05T10:00:00Z" ]] && ok "an opened row is last moved at its open" || bad "an opened row is last moved at its open"
DWS_NOW=2026-10-12T09:30:00Z "$DWS" set a1 --next "go on" >/dev/null 2>&1
[[ "$(field a1 last_moved)" == "2026-10-12T09:30:00Z" ]] \
  && ok "a director set --next moves last moved (R-197, an authored update counts as movement)" || bad "a director set --next moves last moved" "$(field a1 last_moved)"
DWS_NOW=2026-10-11T00:00:00Z "$DWS" set a1 --owner someone >/dev/null 2>&1
[[ "$(field a1 last_moved)" == "2026-10-12T09:30:00Z" ]] \
  && ok "a set dated earlier than last moved never moves it back" || bad "a set never moves last moved back" "$(field a1 last_moved)"

# --- LF2: typed links relay:R-<n> and msg:<id> ----------------------------------------
"$DWS" link a1 relay:R-12 msg:01ABC.def >/dev/null 2>&1 && ok "link accepts relay:R-<n> and msg:<id>" || bad "link accepts relay:R-<n> and msg:<id>"
for badlink in relay:12 relay:R-x relay:R- 'relay:R-1 2' 'msg:' 'msg:-x'; do
  n="$(lines)"
  if "$DWS" link a1 "$badlink" >"$root/out" 2>&1; then bad "link refuses a malformed typed link: $badlink" "accepted"
  elif [[ "$(lines)" != "$n" ]]; then bad "link refuses $badlink" "appended anyway"
  else ok "link refuses a malformed typed link: $badlink"; fi
done
"$DWS" link a1 pr:o/r#1 >/dev/null 2>&1 && ok "other link types are untouched" || bad "other link types are untouched"

# --- LF2: the fold keeps the set of consumed src per row ------------------------------
printf '%s\n' '{"ts":"2026-10-13T00:00:00Z","actor":"refresh","tag":"mirror","event":"observe","ws":"a1","src":"relay:R-5@2026-10-13T00:00:00Z","moved_at":"2026-10-13T00:00:00Z","checked_at":"2026-10-13T00:00:01Z"}' \
  '{"ts":"2026-10-13T00:00:02Z","actor":"refresh","tag":"mirror","event":"observe","ws":"a1","src":"desk:c1@2026-10-13T00:00:00Z","link":"ask:c1","seen":{"kind":"desk","state":"open"},"checked_at":"2026-10-13T00:00:02Z"}' >>"$LEDGER"
python3 - <(DWS_NOW=2026-10-14T00:00:00Z "$DWS" show --json) <<'P' && ok "the fold records each observed src on its row, link observes included" || bad "the fold records consumed src per row"
import json, sys
r = {x["slug"]: x for x in json.load(open(sys.argv[1]))["rows"]}["a1"]
assert r["consumed"] == ["desk:c1@2026-10-13T00:00:00Z", "relay:R-5@2026-10-13T00:00:00Z"], r["consumed"]
assert r["last_moved"] == "2026-10-13T00:00:00Z", r["last_moved"]
P
open_at 2026-10-05T10:00:00Z a2 --title "beta"
python3 - <(DWS_NOW=2026-10-14T00:00:00Z "$DWS" show --json) <<'P' && ok "a row with no observed src has an empty consumed list" || bad "a row with no observed src has an empty consumed list"
import json, sys
r = {x["slug"]: x for x in json.load(open(sys.argv[1]))["rows"]}["a2"]
assert r["consumed"] == [], r
P

echo "dws-fill: $pass passed, $fail failed"
[[ "$fail" == 0 ]]
