#!/usr/bin/env bash
# Prove the intake record (director flow study, item E): an operator ask gets one id when it
# arrives, and the id follows it to the relay entry, the card, the answer and the ledger row,
# where one join shows the chain.
#   - `dws intake` stamps the ask from the clock, mints I-<n>, and takes no time argument;
#   - relay-log --ask/--kind write field lines, check the shape, and write nothing on a bad one;
#   - a malformed intake: link is refused; card-ingest parse --intake carries the id into the card;
#   - desk-snapshot keeps the card's intake id and each answer's time, and a write that read
#     answers appends exactly one row to desk-reads.jsonl (a refused write, or one with no
#     answers read, appends none);
#   - `dws asks` joins ask, card, answer time, relay entry and row, shows a blank as "none",
#     and gives the same bytes for the same files.
# Run conditions: a scratch HOME and DIRECTOR_STATE, no MARVEL_ variables, no network.
#
# Usage: skills/director/scripts/verify-intake.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DWS="${DWS:-$here/dws}"
RL="${RELAY_LOG_BIN:-$here/relay-log}"
CI="${CI_BIN:-$here/card-ingest}"
SNAP="${SNAP:-$here/desk-snapshot}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d "${TMPDIR:-/tmp}/intake.XXXXXX")"
trap 'rm -rf "$root"' EXIT
export HOME="$root/home"; mkdir -p "$HOME"
export DIRECTOR_STATE="$root/state"; mkdir -p "$DIRECTOR_STATE"
export RELAY_LOG="$root/relay-log.md"
export PYTHONDONTWRITEBYTECODE=1
unset MARVEL_SOCKET
ASKS="$DIRECTOR_STATE/asks.jsonl"
READS="$DIRECTOR_STATE/desk-reads.jsonl"
nlines() { if [[ -f "$1" ]]; then wc -l <"$1" | tr -d ' '; else echo 0; fi; }

# --- the clock stamps the ask; the caller cannot set it ---------------------------------------
sub="$(
  export DIRECTOR_STATE="$root/clock-state"
  t0="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  printf 'first line\nsecond line\n' >"$root/ask.txt"
  id="$("$DWS" intake --text-file "$root/ask.txt")"
  t1="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  got="$(python3 -c 'import json,sys;e=json.loads(open(sys.argv[1]).readline());print(e["received_at"],e["text"].replace("\n","|"),e["source"],sep="\t")' "$DIRECTOR_STATE/asks.jsonl")"
  at="${got%%$'\t'*}"
  [[ "$id" == "I-1" ]] && ok "the first ask is I-1" || bad "the first ask is I-1" "$id"
  [[ ! "$at" < "$t0" && ! "$at" > "$t1" ]] && ok "received_at lies between the clock readings around the write" || bad "received_at comes from the clock" "$t0 <= $at <= $t1"
  [[ "${got#*$'\t'}" == $'first line|second line\toperator' ]] && ok "the text is kept verbatim and the source defaults to operator" || bad "text verbatim" "$got"
  n0="$(nlines "$DIRECTOR_STATE/asks.jsonl")"
  for flag in --time --at --received-at --stamp; do
    if "$DWS" intake --text-file "$root/ask.txt" "$flag" 2000-01-01T00:00:00Z >/dev/null 2>&1; then bad "$flag is refused"; else ok "$flag is refused"; fi
  done
  : >"$root/empty.txt"
  if "$DWS" intake --text-file "$root/empty.txt" >/dev/null 2>&1; then bad "an empty ask is refused"; else ok "an empty ask is refused"; fi
  [[ "$(nlines "$DIRECTOR_STATE/asks.jsonl")" == "$n0" ]] && ok "refused intakes write nothing" || bad "refused intakes write nothing"
  echo "$pass $fail"
)"
# the subshell prints its PASS and FAIL lines, then its counts; fold them in
printf '%s\n' "$sub" | sed '$d'
read -r sp sf <<<"$(printf '%s\n' "$sub" | tail -n 1)"; pass=$((pass+sp)); fail=$((fail+sf))

# --- the fixed clock from here on -------------------------------------------------------------
export DWS_TEST_CLOCK=1
export DWS_NOW=2026-10-19T08:00:00Z
id1="$(printf 'build the thing\n' | "$DWS" intake --text-file -)"
DWS_NOW=2026-10-19T09:00:00Z id2="$(printf 'a second ask\n' | "$DWS" intake --text-file - --source bus)"
[[ "$id1 $id2" == "I-1 I-2" ]] && ok "ids count up under the lock: I-1, I-2" || bad "ids count up" "$id1 $id2"
[[ "$(nlines "$ASKS")" == 2 ]] && ok "each ask is one appended line" || bad "each ask is one line"

# --- relay-log carries the id ---------------------------------------------------------------------
"$RL" append --to agent://t/x --ws slug-a --ask I-1 --kind build --text "do it" >/dev/null
if sed -n '1,6p' "$RELAY_LOG" | grep -q '^\*\*Workstream:\*\* slug-a$' && grep -q '^\*\*Intake:\*\* I-1$' "$RELAY_LOG" && grep -q '^\*\*Kind:\*\* build$' "$RELAY_LOG"; then
  ok "relay-log --ask and --kind write field lines after the heading"
else bad "relay-log field lines" "$(cat "$RELAY_LOG")"; fi
n0="$(wc -c <"$RELAY_LOG" | tr -d ' ')"
for args in "--ask foo" "--ask I-" "--ask i-1" "--ask I-1x" "--kind other"; do
  # shellcheck disable=SC2086
  if "$RL" append --to x --text y $args >/dev/null 2>&1; then bad "relay-log refuses $args"; else ok "relay-log refuses $args"; fi
done
[[ "$(wc -c <"$RELAY_LOG" | tr -d ' ')" == "$n0" ]] && ok "a bad ask or kind writes nothing" || bad "a bad ask or kind writes nothing"
"$RL" append --to agent://t/y --text "plain" >/dev/null
plain="$(awk '/^## R-2 /{f=1} f' "$RELAY_LOG" | sed 's/^## R-2 ([^)]*)/## R-2 (T)/')"
[[ "$plain" == $'## R-2 (T) -> agent://t/y\n**Text, verbatim:**\n> plain\n**Outcome:** pending' ]] && ok "an entry with no --ask has the shape it had before" || bad "no-ask entry shape" "$plain"

# --- the row carries it ----------------------------------------------------------------------------
"$DWS" open slug-a --title "the build" --owner x --stage building >/dev/null
"$DWS" link slug-a intake:I-1 >/dev/null && ok "an intake: link goes on a row" || bad "an intake: link goes on a row"
for l in intake:foo intake:I- intake:I-1x intake:; do
  if "$DWS" link slug-a "$l" >/dev/null 2>&1; then bad "dws link refuses $l"; else ok "dws link refuses $l"; fi
done
if "$DWS" open slug-b --title t --link intake:nope >/dev/null 2>&1; then bad "dws open refuses a malformed intake: link"; else ok "dws open refuses a malformed intake: link"; fi

# --- the card carries it ---------------------------------------------------------------------------
cat >"$root/card.md" <<'M'
Source: an operator ask, recorded at intake

## Card 1. Should the nightly report run at 06:00 or 07:00?

**IN SHORT.** The report runs at 06:00 and often misses late data. Moving it to 07:00 catches that data and lands after the morning stand-up starts.

**The problem.** Late data arrives between 06:00 and 06:40 on most days.

**Terms.** None.

**Options.**
1. *Keep 06:00.* Nothing changes. The cost is a report short of late data.
2. *Move to 07:00.* One schedule line changes. The cost is a later report.

**Recommendation.** Option 2. Complete data matters more than an hour.

**Valid until** 2026-11-02. The reporting seat re-checks it then.
M
printf '{"other": {"status": "open"}}' >"$root/c0.json"
printf '[]' >"$root/a0.json"
DWS_NOW=2026-10-19T07:00:00Z "$SNAP" write --cards "$root/c0.json" --reported 1 --answers "$root/a0.json" --answers-reported 0 >/dev/null
r0="$(nlines "$READS")"
DWS_NOW=2026-10-19T12:00:00Z "$CI" parse "$root/card.md" --area ops --owner x --prefix rpt --intake I-1 >"$root/parse.out" 2>&1 && ok "card-ingest parse accepts --intake" || bad "card-ingest parse accepts --intake" "$(cat "$root/parse.out")"
slug="$(sed -n 's/^card-ingest: set //p' "$root/parse.out")"
batch="$(sed -n 's/^card-ingest: batch //p' "$root/parse.out")"
python3 - "$batch" "$DIRECTOR_STATE/card-ingest.jsonl" <<'P' && ok "the id is in the card data and the ingest log" || bad "the id is in the card data and the ingest log"
import json, sys
b = json.load(open(sys.argv[1]))
assert b["writes"][0]["data"]["intake"] == "I-1"
assert json.loads(open(sys.argv[2]).readline())["intake"] == "I-1"
P
nb="$(find "$DIRECTOR_STATE/card-ingest" -type f | wc -l | tr -d ' ')"
if DWS_NOW=2026-10-19T12:00:00Z "$CI" parse "$root/card.md" --area ops --owner x --prefix rpt2 --intake bogus >/dev/null 2>&1; then bad "card-ingest refuses a malformed --intake"; else ok "card-ingest refuses a malformed --intake"; fi
[[ "$(find "$DIRECTOR_STATE/card-ingest" -type f | wc -l | tr -d ' ')" == "$nb" ]] && ok "and writes no batch for it" || bad "and writes no batch for it"

# --- desk-snapshot keeps the id and the answer time; one read row per answered write ----------------
printf '{"%s": {"status": "open", "data": {"intake": "I-1"}}, "other": {"status": "open"}}' "$slug" >"$root/c1.json"
printf '[{"card": "%s", "choice": "1", "note": "", "ready": true, "at": "2026-10-19T11:30:00.123Z"}]' "$slug" >"$root/a1.json"
r0="$(nlines "$READS")"
DWS_NOW=2026-10-19T13:00:00Z "$SNAP" write --cards "$root/c1.json" --reported 2 --answers "$root/a1.json" --answers-reported 1 >/dev/null
python3 - "$DIRECTOR_STATE/desk-snapshot.json" "$slug" <<'P' && ok "the snapshot keeps the card's intake id and the answer's time" || bad "the snapshot keeps intake and answer time"
import json, sys
s = json.load(open(sys.argv[1])); c = sys.argv[2]
assert s["intake"] == {c: "I-1"}, s
assert s["answered_at"] == {c: "2026-10-19T11:30:00Z"}, s
P
[[ "$(nlines "$READS")" == "$((r0+1))" ]] && ok "a write that read answers appends exactly one read row" || bad "one read row" "$r0 -> $(nlines "$READS")"
python3 - "$READS" "$slug" <<'P' && ok "the row names the answered card and its time" || bad "the row names the answered card and its time"
import json, sys
r = json.loads(open(sys.argv[1]).read().splitlines()[-1])
assert r == {"read_at": "2026-10-19T13:00:00Z", "cards": 2, "answered": [sys.argv[2]],
             "answered_not_relayed": [{"card": sys.argv[2], "answered_at": "2026-10-19T11:30:00Z"}]}, r
P
# the Desk writes at: new Date().toISOString(), which carries milliseconds
printf '[{"card": "%s", "at": "2026-10-19T11:30:00.000Z"}, {"card": "other", "at": "2026-10-19T10:00:00Z"}, {"card": "other", "at": "2026-10-19T10:15:00.123Z"}]' "$slug" >"$root/a-ms.json"
DWS_NOW=2026-10-19T13:00:00Z "$SNAP" write --cards "$root/c1.json" --reported 2 --answers "$root/a-ms.json" --answers-reported 3 >/dev/null
python3 - "$DIRECTOR_STATE/desk-snapshot.json" "$slug" <<'P' && ok "an answer time with .000Z or .123Z milliseconds reads to the second, and the latest of a card's answers is kept" || bad "millisecond answer times"
import json, sys
s = json.load(open(sys.argv[1]))
assert s["answered_at"] == {"other": "2026-10-19T10:15:00Z", sys.argv[2]: "2026-10-19T11:30:00Z"}, s["answered_at"]
P
printf '[{"card": "%s", "at": "2026-10-19T11:30:00.Z"}, {"card": "other", "at": "2026-10-19T11:30:00.123"}]' "$slug" >"$root/a-bad.json"
DWS_NOW=2026-10-19T13:00:00Z "$SNAP" write --cards "$root/c1.json" --reported 2 --answers "$root/a-bad.json" --answers-reported 2 >/dev/null
python3 - "$DIRECTOR_STATE/desk-snapshot.json" <<'P' && ok "a malformed fraction or a missing Z adds no time, never a guess" || bad "malformed times"
import json, sys
s = json.load(open(sys.argv[1])); assert "answered_at" not in s, s
P
DWS_NOW=2026-10-19T13:00:00Z "$SNAP" write --cards "$root/c1.json" --reported 2 --answers "$root/a1.json" --answers-reported 1 >/dev/null
r1="$(nlines "$READS")"
if DWS_NOW=2026-10-19T13:05:00Z "$SNAP" write --cards "$root/c1.json" --reported 3 --answers "$root/a1.json" --answers-reported 1 >/dev/null 2>&1; then bad "a miscounted read is refused"; else ok "a miscounted read is refused"; fi
[[ "$(nlines "$READS")" == "$r1" ]] && ok "a refused write appends no read row" || bad "a refused write appends no read row"
DWS_NOW=2026-10-19T13:10:00Z "$SNAP" write --cards "$root/c1.json" --reported 2 >/dev/null
[[ "$(nlines "$READS")" == "$r1" ]] && ok "a write with no answers read appends no read row" || bad "no answers, no row"
python3 - "$DIRECTOR_STATE/desk-snapshot.json" <<'P' && ok "a write with no answers drops the answer keys, as before" || bad "answers keys dropped"
import json, sys
s = json.load(open(sys.argv[1])); assert "answered" not in s and "answered_at" not in s
P
printf '{"%s": {"status": "relayed", "data": {"intake": "I-1"}}, "other": {"status": "open"}}' "$slug" >"$root/c2.json"
DWS_NOW=2026-10-19T14:00:00Z "$SNAP" write --cards "$root/c2.json" --reported 2 --answers "$root/a1.json" --answers-reported 1 >/dev/null
python3 - "$READS" <<'P' && ok "a relayed answered card is not answered-not-relayed" || bad "relayed card still listed"
import json, sys
r = json.loads(open(sys.argv[1]).read().splitlines()[-1]); assert r["answered_not_relayed"] == [] and len(r["answered"]) == 1, r
P
DWS_NOW=2026-10-19T13:00:00Z "$SNAP" write --cards "$root/c1.json" --reported 2 --answers "$root/a1.json" --answers-reported 1 >/dev/null

# --- the join -----------------------------------------------------------------------------------------
"$DWS" asks --json >"$root/j1"; "$DWS" asks --json >"$root/j2"
cmp -s "$root/j1" "$root/j2" && ok "the join gives the same bytes twice" || bad "same bytes twice"
python3 - "$root/j1" "$slug" <<'P' && ok "I-1 joins its card, answer time, relay entry and row; I-2 shows none, never omitted" || bad "the join rows" "$(cat "$root/j1")"
import json, sys
j = json.load(open(sys.argv[1]))
assert j == [
 {"answered_at": "2026-10-19T11:30:00Z", "ask_id": "I-1", "card": sys.argv[2], "received_at": "2026-10-19T08:00:00Z", "relay": "R-1", "row": "slug-a"},
 {"answered_at": "none", "ask_id": "I-2", "card": "none", "received_at": "2026-10-19T09:00:00Z", "relay": "none", "row": "none"}], j
P
tab="$("$DWS" asks)"
[[ "$(printf '%s\n' "$tab" | sed -n 1p | awk '{print NF}')" == 6 && "$(printf '%s\n' "$tab" | sed -n 3p | tr -s ' ')" == "I-2 2026-10-19T09:00:00Z none none none none" ]] && ok "the table prints six columns and none for a blank" || bad "the table" "$tab"
"$DWS" asks >"$root/t2"; [[ "$tab" == "$(cat "$root/t2")" ]] && ok "the table gives the same bytes twice" || bad "table bytes"
mkdir -p "$root/empty-state"
[[ "$(DIRECTOR_STATE="$root/empty-state" "$DWS" asks --json)" == "[]" ]] && ok "with no asks the join is an empty list" || bad "empty join"

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
