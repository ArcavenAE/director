#!/usr/bin/env bash
# Prove K13 (director functions redesign, section 6, row K13; ticket
# aae-orc-eeb2v): `dws --blocked operator` refuses unless it names a card that a
# fresh Desk snapshot shows as open, and desk-snapshot only writes a snapshot
# from input it can validate, counting status "open" and nothing else.
#
# Usage: skills/director/scripts/verify-desk-snapshot.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DWS="${DWS:-$here/dws}"
SNAP="${SNAP:-$here/desk-snapshot}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
export DIRECTOR_STATE="$root/state"
mkdir -p "$DIRECTOR_STATE"
export DWS_TEST_CLOCK=1
LEDGER="$DIRECTOR_STATE/workstreams.jsonl"
SNAPFILE="$DIRECTOR_STATE/desk-snapshot.json"
lines() { if [[ -f "$LEDGER" ]]; then wc -l <"$LEDGER" | tr -d ' '; else echo 0; fi; }

# refuses CMD...: the command exits nonzero, appends nothing, and says WORD.
refuses() {
  local label="$1" word="$2"; shift 2
  local before; before="$(lines)"
  if "$@" >"$root/out" 2>&1; then bad "$label" "accepted"
  elif [[ "$(lines)" != "$before" ]]; then bad "$label" "appended anyway"
  elif ! grep -qi -- "$word" "$root/out"; then bad "$label" "refused without '$word': $(cat "$root/out")"
  else ok "$label"; fi
}
# snapshot_refuses CMD...: desk-snapshot exits nonzero, writes no snapshot, says WORD.
snap_refuses() {
  local label="$1" word="$2"; shift 2
  rm -f "$SNAPFILE"
  if "$@" >"$root/out" 2>&1; then bad "$label" "accepted"
  elif [[ -e "$SNAPFILE" ]]; then bad "$label" "wrote a snapshot anyway"
  elif ! grep -qi -- "$word" "$root/out"; then bad "$label" "refused without '$word': $(cat "$root/out")"
  else ok "$label"; fi
}

export DWS_NOW="2026-10-15T12:00:00Z"

# --- no snapshot: every operator-blocked row is refused ---------------------
refuses "open --blocked operator with no snapshot is refused as UNREAD" "UNREAD" \
  "$DWS" open a1 --title t --blocked operator --ask card-1
refuses "open --blocked operator with no --ask is refused" "--ask" \
  "$DWS" open a2 --title t --blocked operator
"$DWS" open base --title "Base row" --owner "team-a/architect" >/dev/null 2>&1 \
  && ok "open without a blocked value is untouched" || bad "open without a blocked value is untouched"
refuses "set --blocked operator with no snapshot is refused as UNREAD" "UNREAD" \
  "$DWS" set base --blocked operator --ask card-1

# --- the helper validates what a session produced ---------------------------
cards="$root/cards.json"
cat >"$cards" <<'J'
{"card-1": {"status": "open", "title": "ruling needed"},
 "card-2": {"status": "relayed"},
 "card-3": {"status": "closed"},
 "card-4": {"status": "split"},
 "card-5": {"status": "open"}}
J
snap_refuses "desk-snapshot refuses a missing cards file" "cards" "$SNAP" write --cards "$root/none.json" --reported 5
printf 'not json' >"$root/bad.json"
snap_refuses "desk-snapshot refuses a file that is not JSON" "json" "$SNAP" write --cards "$root/bad.json" --reported 1
printf '{}' >"$root/empty.json"
snap_refuses "desk-snapshot refuses zero cards" "no cards" "$SNAP" write --cards "$root/empty.json" --reported 0
snap_refuses "desk-snapshot refuses a reported count that differs from the file" "reported" "$SNAP" write --cards "$cards" --reported 6
snap_refuses "desk-snapshot refuses a count at the read limit, which may be truncated" "limit" "$SNAP" write --cards "$cards" --reported 5 --limit 5
printf '{"c": {"title": "no status"}}' >"$root/nostatus.json"
snap_refuses "desk-snapshot refuses a card with no status" "status" "$SNAP" write --cards "$root/nostatus.json" --reported 1
printf '[{"id": "c", "status": "open"}, {"id": "c", "status": "closed"}]' >"$root/dup.json"
snap_refuses "desk-snapshot refuses a duplicate card id" "duplicate" "$SNAP" write --cards "$root/dup.json" --reported 2
printf '{"c": {"status": 7}}' >"$root/numstatus.json"
snap_refuses "desk-snapshot refuses a status that is not a string" "status" "$SNAP" write --cards "$root/numstatus.json" --reported 1
printf '{"c": "open"}' >"$root/notcard.json"
snap_refuses "desk-snapshot refuses a card that is not an object" "card" "$SNAP" write --cards "$root/notcard.json" --reported 1

rm -f "$SNAPFILE"
"$SNAP" write --cards "$cards" --reported 5 >"$root/out" 2>&1 \
  && ok "desk-snapshot writes a snapshot from valid cards" || bad "desk-snapshot writes a snapshot from valid cards" "$(cat "$root/out")"
python3 - "$SNAPFILE" <<'P' && ok "the snapshot lists exactly the open cards, with the read time and counts" || bad "the snapshot lists exactly the open cards"
import json, sys
s = json.load(open(sys.argv[1]))
assert s["open"] == ["card-1", "card-5"], s
assert s["read_at"] == "2026-10-15T12:00:00Z", s
assert s["cards"] == 5 and s["by_status"] == {"open": 2, "relayed": 1, "closed": 1, "split": 1}, s
P
printf '[{"id": "x1", "status": "open"}, {"doc_id": "x2", "data": {"status": "closed"}}]' >"$root/list.json"
"$SNAP" write --cards "$root/list.json" --reported 2 >/dev/null 2>&1 \
  && python3 -c "import json;s=json.load(open('$SNAPFILE'));assert s['open']==['x1'],s" \
  && ok "desk-snapshot reads the list shape, with id or doc_id and a status under data" \
  || bad "desk-snapshot reads the list shape"
"$SNAP" write --cards "$cards" --reported 5 >/dev/null 2>&1

# --- card-ingest inputs (design card-ingest.md, B2): every id with its status, and answers
"$SNAP" write --cards "$cards" --reported 5 >/dev/null 2>&1
python3 - "$SNAPFILE" <<'P' && ok "the snapshot holds every card id with its status, and no answers key without an answers read" || bad "the snapshot holds every card id with its status"
import json, sys
s = json.load(open(sys.argv[1]))
assert s["ids"] == {"card-1": "open", "card-2": "relayed", "card-3": "closed", "card-4": "split", "card-5": "open"}, s
assert "answered" not in s, s
assert s["open"] == ["card-1", "card-5"], s
P
printf '[{"card": "card-1", "choice": "1"}, {"card_id": "card-5"}, {"card": "card-1"}]' >"$root/answers.json"
"$SNAP" write --cards "$cards" --reported 5 --answers "$root/answers.json" --answers-reported 3 >"$root/out" 2>&1 \
  && python3 -c "import json;s=json.load(open('$SNAPFILE'));assert s['answered']==['card-1','card-5'],s" \
  && ok "an answers read records the sorted distinct cards that have an answer" \
  || bad "an answers read records the answered cards" "$(cat "$root/out")"
snap_refuses "an answers file whose count differs from --answers-reported is refused" "answers" \
  "$SNAP" write --cards "$cards" --reported 5 --answers "$root/answers.json" --answers-reported 2
snap_refuses "an answers read at the read limit is refused as possibly truncated" "limit" \
  "$SNAP" write --cards "$cards" --reported 5 --answers "$root/answers.json" --answers-reported 3 --limit 3
snap_refuses "--answers without --answers-reported is refused" "answers-reported" \
  "$SNAP" write --cards "$cards" --reported 5 --answers "$root/answers.json"
printf '[{"choice": "1"}]' >"$root/answers-nocard.json"
snap_refuses "an answer that names no card is refused" "answer" \
  "$SNAP" write --cards "$cards" --reported 5 --answers "$root/answers-nocard.json" --answers-reported 1
printf '[{"card": "ghost"}]' >"$root/answers-ghost.json"
snap_refuses "an answer for a card the read does not hold is refused" "ghost" \
  "$SNAP" write --cards "$cards" --reported 5 --answers "$root/answers-ghost.json" --answers-reported 1
printf '[{"card": "card-1", "card_id": "card-5"}]' >"$root/answers-differ.json"
snap_refuses "an answer whose card and card_id differ is refused, not read as the first" "an answer names card 'card-1' in 'card' and 'card-5' in 'card_id'; give one card, or two that agree" \
  "$SNAP" write --cards "$cards" --reported 5 --answers "$root/answers-differ.json" --answers-reported 1
printf '[{"card": "card-1", "card_id": "card-1"}]' >"$root/answers-agree.json"
"$SNAP" write --cards "$cards" --reported 5 --answers "$root/answers-agree.json" --answers-reported 1 >/dev/null 2>&1 \
  && python3 -c "import json;s=json.load(open('$SNAPFILE'));assert s['answered']==['card-1'],s" \
  && ok "an answer whose card and card_id agree is read once" || bad "an answer whose card and card_id agree is read once"
printf '[]' >"$root/answers-none.json"
"$SNAP" write --cards "$cards" --reported 5 --answers "$root/answers-none.json" --answers-reported 0 >/dev/null 2>&1 \
  && python3 -c "import json;s=json.load(open('$SNAPFILE'));assert s['answered']==[],s" \
  && ok "an answers read of zero answers records an empty answered list (read, none found)" \
  || bad "an answers read of zero answers records an empty answered list"
"$SNAP" write --cards "$cards" --reported 5 >/dev/null 2>&1

# --- dws against a fresh snapshot --------------------------------------------
before="$(lines)"
"$DWS" open a3 --title t --blocked operator --ask card-1 >"$root/out" 2>&1 \
  && ok "open --blocked operator --ask accepts an open card" || bad "open accepts an open card" "$(cat "$root/out")"
grep -q "0s\|age\|ago" "$root/out" && ok "the accepted write prints the snapshot's age" || bad "the accepted write prints the snapshot's age" "$(cat "$root/out")"
"$DWS" show --json 2>/dev/null | python3 -c "
import json,sys
rows=[r for r in json.load(sys.stdin)['rows'] if r['slug']=='a3']
assert rows and rows[0]['blocked']=='operator' and 'ask:card-1' in rows[0]['links'], rows" \
  && ok "the row is blocked on operator and carries the ask: link" || bad "the row is blocked on operator and carries the ask: link"
"$DWS" set base --blocked operator --ask card-5 >/dev/null 2>&1 \
  && ok "set --blocked operator --ask accepts an open card" || bad "set accepts an open card"
refuses "a card that is relayed is not open" "not open" "$DWS" set base --blocked operator --ask card-2
refuses "a card that is closed is not open" "not open" "$DWS" set base --blocked operator --ask card-3
refuses "a card with a status other than open is not open" "not open" "$DWS" set base --blocked operator --ask card-4
refuses "a card id the snapshot does not hold is refused" "not open" "$DWS" set base --blocked operator --ask card-99
refuses "a card id with a trailing newline is refused" "not a card id" "$DWS" set base --blocked operator --ask $'card-1\n'
refuses "--ask with a blocked value other than operator is refused" "operator" "$DWS" set base --blocked "team-a/architect" --ask card-1
refuses "--ask with no --blocked is refused" "operator" "$DWS" set base --ask card-1
for v in "Operator" " operator " "OPERATOR"; do
  refuses "blocked $(printf %q "$v") needs an ask like operator does" "--ask" "$DWS" set base --blocked "$v"
done
refuses "a fullwidth 'operator' needs an ask too" "--ask" "$DWS" set base --blocked $'\xef\xbd\x8f\xef\xbd\x90\xef\xbd\x85\xef\xbd\x92\xef\xbd\x81\xef\xbd\x94\xef\xbd\x8f\xef\xbd\x92'
"$DWS" set base --blocked "team-a/architect" >/dev/null 2>&1 \
  && ok "a role as blocked-on needs no ask" || bad "a role as blocked-on needs no ask"
"$DWS" set base --blocked "" >/dev/null 2>&1 \
  && ok "clearing blocked needs no ask" || bad "clearing blocked needs no ask"

# --- freshness ---------------------------------------------------------------
export DWS_NOW="2026-10-16T11:59:59Z"
"$DWS" set base --blocked operator --ask card-1 >"$root/out" 2>&1 \
  && ok "a snapshot just under 24 hours old is accepted" || bad "a snapshot just under 24 hours old is accepted" "$(cat "$root/out")"
export DWS_NOW="2026-10-16T12:00:01Z"
refuses "a snapshot over 24 hours old reads UNREAD" "UNREAD" "$DWS" set base --blocked operator --ask card-1
export DWS_NOW="2026-10-15T11:00:00Z"
refuses "a snapshot read in the future reads UNREAD" "UNREAD" "$DWS" set base --blocked operator --ask card-1
export DWS_NOW="2026-10-15T12:00:00Z"

# --- a damaged snapshot ------------------------------------------------------
cp "$SNAPFILE" "$root/good.json"
printf '{"read_at": "2026-10-15T12:00:00Z", "open": ' >"$SNAPFILE"
refuses "a torn snapshot reads UNREAD" "UNREAD" "$DWS" set base --blocked operator --ask card-1
printf '{"read_at": "yesterday", "open": ["card-1"]}' >"$SNAPFILE"
refuses "a snapshot with an unreadable time reads UNREAD" "UNREAD" "$DWS" set base --blocked operator --ask card-1
printf '{"read_at": "2026-10-15T12:00:00Z", "open": "card-1"}' >"$SNAPFILE"
refuses "a snapshot whose open list is not a list reads UNREAD" "UNREAD" "$DWS" set base --blocked operator --ask card-1
cp "$root/good.json" "$SNAPFILE"

echo "desk-snapshot: $pass passed, $fail failed"
[[ "$fail" == 0 ]]
