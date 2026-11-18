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
printf '[{"id": "a1", "status": "open", "area": "Ops"}, {"doc_id": "a2", "data": {"status": "open", "area": "Research"}}, {"id": "a3", "status": "open", "area": "Ops"}, {"id": "a4", "status": "open"}, {"id": "a5", "status": "open", "area": 7}, {"id": "a6", "status": "open", "area": "bad\\u0007one"}]' >"$root/areas.json"
"$SNAP" write --cards "$root/areas.json" --reported 6 >/dev/null 2>&1 \
  && python3 -c "import json;s=json.load(open('$SNAPFILE'));assert s['areas']==['Ops','Research'],s" \
  && ok "desk-snapshot records the distinct printable area strings, sorted, from area or data.area; a card with none, a non-string or a control character adds nothing" \
  || bad "desk-snapshot records areas"
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

# --- S14: the answer mirror (aae-orc-o8zbi; director rulings 01M7R8WQE9, A1 to A5) ------------------------------------
# desk-snapshot write --answers keeps the answers it read, choices included, in $DIRECTOR_STATE/desk/answers.jsonl.
# Each case runs in its own state dir so the cases above are not disturbed.
saved_state="$DIRECTOR_STATE"
mirror_cards="$root/m-cards.json"; mirror_answers="$root/m-answers.json"
printf '%s' '{"card-1": {"status": "open"}, "card-5": {"status": "open"}, "card-7": {"status": "relayed"}}' >"$mirror_cards"
printf '%s' '[{"card": "card-1", "choice": "1", "note": "go", "ready": true, "at": "2026-10-15T11:00:00.123Z"}, {"card_id": "card-5", "at": "2026-10-15T11:05:00Z"}]' >"$mirror_answers"
fresh() { export DIRECTOR_STATE="$root/mirror-$1"; rm -rf "$DIRECTOR_STATE"; mkdir -p "$DIRECTOR_STATE"; AJ="$DIRECTOR_STATE/desk/answers.jsonl"; }
mwrite() {  # [extra args]: the S14 fixture write at the fixed clock
  DWS_NOW="${MNOW:-2026-10-15T12:00:00Z}" "$SNAP" write --cards "$mirror_cards" --reported 3 --answers "${MANS:-$mirror_answers}" --answers-reported "${MCOUNT:-2}" "$@"
}
nrows() { if [[ -f "$AJ" ]]; then wc -l <"$AJ" | tr -d ' '; else echo 0; fi; }

fresh a
mwrite >"$root/out" 2>&1 || true
python3 - "$AJ" <<'P' && ok "answers_persisted_with_choice" || bad "answers_persisted_with_choice" "no row for card-1 with choice '1' in desk/answers.jsonl: $(cat "$root/out")"
import json, os, sys
assert os.path.isfile(sys.argv[1]), 'no desk/answers.jsonl was written'
rows = [json.loads(l) for l in open(sys.argv[1])]
assert any(r.get("card") == "card-1" and r.get("choice") == "1" for r in rows), rows
P
python3 - "$AJ" <<'P' && ok "answers_row_is_the_record_verbatim_plus_read_at_and_card" || bad "answers_row_is_the_record_verbatim_plus_read_at_and_card" "rows differ from the expected two"
import json, os, sys
assert os.path.isfile(sys.argv[1]), 'no desk/answers.jsonl was written'
rows = [json.loads(l) for l in open(sys.argv[1])]
want = [{"card": "card-1", "choice": "1", "note": "go", "ready": True, "at": "2026-10-15T11:00:00.123Z", "read_at": "2026-10-15T12:00:00Z"},
        {"card_id": "card-5", "at": "2026-10-15T11:05:00Z", "read_at": "2026-10-15T12:00:00Z", "card": "card-5"}]
assert rows == want, rows
P
python3 - "$AJ" <<'P' && ok "answers_no_choice_answer_is_still_a_row_without_a_choice_key" || bad "answers_no_choice_answer_is_still_a_row_without_a_choice_key"
import json, os, sys
assert os.path.isfile(sys.argv[1]), 'no desk/answers.jsonl was written'
rows = [json.loads(l) for l in open(sys.argv[1])]
r = [x for x in rows if x.get("card") == "card-5"]
assert len(r) == 1 and "choice" not in r[0], rows
P

# A3: a second identical read adds nothing; a changed answer adds one row and leaves the first rows as they were.
cp "$AJ" "$root/answers.before" 2>/dev/null || : >"$root/answers.before"
MNOW="2026-10-15T13:00:00Z" mwrite >/dev/null 2>&1 || true
[[ "$(nrows)" == 2 ]] && cmp -s "$AJ" "$root/answers.before" && ok "answers_dedup_an_unchanged_answer_adds_no_row" || bad "answers_dedup_an_unchanged_answer_adds_no_row" "rows: $(nrows), want 2 and the same bytes"
printf '%s' '[{"card": "card-1", "choice": "2", "note": "go", "ready": true, "at": "2026-10-15T11:00:00.123Z"}, {"card_id": "card-5", "at": "2026-10-15T11:05:00Z"}]' >"$root/m-changed.json"
MNOW="2026-10-15T14:00:00Z" MANS="$root/m-changed.json" mwrite >/dev/null 2>&1 || true
python3 - "$AJ" "$root/answers.before" <<'P' && ok "answers_a_changed_answer_adds_one_row_and_never_rewrites" || bad "answers_a_changed_answer_adds_one_row_and_never_rewrites" "rows: $(nrows)"
import json, os, sys
assert os.path.isfile(sys.argv[1]), 'no desk/answers.jsonl was written'
now = open(sys.argv[1], "rb").read()
before = open(sys.argv[2], "rb").read()
assert now.startswith(before) and len(before) > 0, "the first rows were rewritten or missing"
rows = [json.loads(l) for l in now.decode().splitlines()]
assert len(rows) == 3 and rows[2]["card"] == "card-1" and rows[2]["choice"] == "2", rows
P

# A4: no write without --answers; a refused write appends none.
fresh b
DWS_NOW=2026-10-15T12:00:00Z "$SNAP" write --cards "$mirror_cards" --reported 3 >/dev/null 2>&1 || true
[[ ! -e "$DIRECTOR_STATE/desk" ]] && ok "answers_no_write_without_the_answers_flag" || bad "answers_no_write_without_the_answers_flag" "desk/ exists: $(ls "$DIRECTOR_STATE/desk")"
fresh c
MCOUNT=5 mwrite >"$root/out" 2>&1 && bad "answers_a_refused_write_appends_none" "the count mismatch was accepted" \
  || { [[ "$(nrows)" == 0 ]] && ok "answers_a_refused_write_appends_none" || bad "answers_a_refused_write_appends_none" "rows: $(nrows)"; }

# A5: an append that fails exits nonzero, names the file, and keeps the snapshot.
fresh d
mkdir -p "$DIRECTOR_STATE/desk/answers.jsonl"   # a directory where the file belongs, so the open fails
rc=0; mwrite >"$root/out" 2>&1 || rc=$?
if [[ $rc -eq 0 ]]; then bad "answers_a_failed_append_exits_nonzero_naming_the_file_and_keeps_the_snapshot" "exit 0: $(cat "$root/out")"
elif ! grep -q 'answers.jsonl' "$root/out"; then bad "answers_a_failed_append_exits_nonzero_naming_the_file_and_keeps_the_snapshot" "output does not name answers.jsonl: $(cat "$root/out")"
elif [[ ! -s "$DIRECTOR_STATE/desk-snapshot.json" ]]; then bad "answers_a_failed_append_exits_nonzero_naming_the_file_and_keeps_the_snapshot" "no snapshot kept"
else ok "answers_a_failed_append_exits_nonzero_naming_the_file_and_keeps_the_snapshot"; fi

# Guard: the snapshot and the desk-reads row are byte-identical to what the writer produced before S14 (hashes taken
# from this fixture on origin/main 814222c2, with the fixed clock).
fresh e
mwrite >/dev/null 2>&1 || true
want_snap=5e16ce8c0cafb48807c4e6e31944228c37ba28842e65642cc50a9273a64afe5c
want_reads=0695d2b36085cf1620e31ef45ffcc4a0c499ca39ddb4cedbb89ed11926b1f9b5
got_snap="$(shasum -a 256 <"$DIRECTOR_STATE/desk-snapshot.json" | cut -d' ' -f1)"; got_reads="$(shasum -a 256 <"$DIRECTOR_STATE/desk-reads.jsonl" | cut -d' ' -f1)"
[[ "$got_snap" == "$want_snap" && "$got_reads" == "$want_reads" ]] \
  && ok "answers_guard_snapshot_and_desk_reads_stay_byte_identical" || bad "answers_guard_snapshot_and_desk_reads_stay_byte_identical" "snapshot $got_snap reads $got_reads"
export DIRECTOR_STATE="$saved_state"

echo "desk-snapshot: $pass passed, $fail failed"
[[ "$fail" == 0 ]]
