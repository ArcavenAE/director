#!/usr/bin/env bash
# Prove the standing hook's "waiting:" line (director flow study, item A): the hook shows only what
# is waiting, and reads a dead signal as dead.
#   - nothing waiting prints no line; answers waiting, PRs waiting and both print their clauses, with
#     the Desk read age last on the line;
#   - a missing, unreadable, future or too-old input prints `waiting: UNKNOWN (reason)`;
#   - the line is the last line of output in every case it appears; with neither input present it
#     reads UNKNOWN naming both, and every other line is what the hook printed before;
#   - the hook never calls gh (a stub gh on PATH records every call), and finishes inside a second;
#   - `dws waiting write` is the step that calls gh: it writes the approved, unmerged PRs of the
#     ledger's rows, skips a parked row, and on a PR it cannot read writes nothing.
# Run conditions: a scratch HOME and DIRECTOR_STATE, no MARVEL_ variables, gh a stub, no network.
#
# Usage: skills/director/scripts/verify-waiting.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
H="${STANDING_HOOK_BIN:-$here/standing-hook}"
DWS="${DWS:-$here/dws}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d "${TMPDIR:-/tmp}/waiting.XXXXXX")"
trap 'rm -rf "$root"' EXIT
export HOME="$root/home"; mkdir -p "$HOME"
export DIRECTOR_STATE="$root/state"; mkdir -p "$DIRECTOR_STATE"
export PYTHONDONTWRITEBYTECODE=1
unset MARVEL_SOCKET DIRECTOR_WAITING_MAX_HOURS DWS_NOW DWS_TEST_CLOCK
READS="$DIRECTOR_STATE/desk-reads.jsonl"
PRS="$DIRECTOR_STATE/waiting-prs.json"

# stamp HOURS MINUTES: a UTC stamp that long ago, with 30s of margin so the age rounds down cleanly.
stamp() { python3 -c 'import datetime as d,sys;print((d.datetime.now(d.timezone.utc)-d.timedelta(hours=float(sys.argv[1]),minutes=float(sys.argv[2]),seconds=30)).strftime("%Y-%m-%dT%H:%M:%SZ"))' "$1" "${2:-0}"; }
reads_row() {  # READ_AGE_H ANSWERS_JSON
  printf '{"read_at":"%s","cards":4,"answered":[],"answered_not_relayed":%s}\n' "$(stamp "$1")" "$2" >"$READS"
}
prs_file() {  # WRITTEN_AGE_H PRS_JSON
  printf '{"written_at":"%s","checked":2,"approved_not_merged":%s}\n' "$(stamp "$1")" "$2" >"$PRS"
}
last() { "$H" | tail -n 1; }
count() { "$H" | grep -c '^waiting: ' || true; }

# --- no input at all: UNKNOWN naming both, the other lines as they were ------------------------------
full="$("$H")"
before="$(printf '%s\n' "$full" | sed '$d')"
[[ "$(printf '%s\n' "$full" | tail -n 1)" == "waiting: UNKNOWN (desk-reads.jsonl missing"*"; waiting-prs.json missing"* && "$(count)" == 1 ]] && ok "with neither input the hook prints UNKNOWN naming both" || bad "no inputs" "$full"
[[ "$(printf '%s\n' "$before" | tail -n 1)" == "stalled ideas: "* ]] && ok "and the stalled-ideas line is the last of the rest" || bad "the rest" "$before"

# --- nothing waiting ----------------------------------------------------------------------------------
reads_row 1 '[]'; prs_file 1 '[]'
out="$("$H")"
[[ "$(count)" == 0 && "$out" == "$(printf '%s\n' "$out" | sed '/^waiting: /d')" && "$(printf '%s\n' "$out" | tail -n 1)" == "stalled ideas: "* ]] && ok "nothing waiting prints no line" || bad "nothing waiting" "$out"
[[ -z "$(printf '%s\n' "$out" | tail -n 1 | tr -d '[:space:]')" ]] && bad "no trailing empty line" || ok "and no empty line or zero count"

# --- answers waiting ----------------------------------------------------------------------------------
reads_row 1 "[{\"card\":\"ca-1\",\"answered_at\":\"$(stamp 5)\"},{\"card\":\"ca-2\",\"answered_at\":\"$(stamp 2)\"}]"
[[ "$(last)" == "waiting: 2 Desk answers not relayed (oldest 5h, ids ca-1, ca-2); Desk read 1h ago" ]] && ok "answers waiting: the clause, the oldest age, the ids, the Desk read age" || bad "answers clause" "$(last)"
[[ "$(count)" == 1 ]] && ok "the line prints once" || bad "the line prints once"

# --- PRs waiting --------------------------------------------------------------------------------------
reads_row 1 '[]'; prs_file 1 "[{\"pr\":\"o/r#7\",\"approved_at\":\"$(stamp 3)\"},{\"pr\":\"o/r#8\",\"approved_at\":\"$(stamp 1)\"}]"
[[ "$(last)" == "waiting: 2 approved PRs not merged (oldest 3h); Desk read 1h ago" ]] && ok "PRs waiting: the clause and the oldest age" || bad "PR clause" "$(last)"

# --- both ---------------------------------------------------------------------------------------------
reads_row 2 "[{\"card\":\"ca-9\",\"answered_at\":\"$(stamp 50)\"}]"
[[ "$(last)" == "waiting: 1 Desk answers not relayed (oldest 2d, ids ca-9); 2 approved PRs not merged (oldest 3h); Desk read 2h ago" ]] && ok "both clauses, answers first, the Desk read age last" || bad "both clauses" "$(last)"
reads_row 2 '[{"card":"ca-3"}]'
[[ "$(last)" == "waiting: 1 Desk answers not relayed (oldest unknown, ids ca-3);"* ]] && ok "an answer with no time reads oldest unknown, never invented" || bad "no answer time" "$(last)"

# --- UNKNOWN: a dead signal reads as dead -----------------------------------------------------------------
reads_row 1 '[]'; rm -f "$PRS"
[[ "$(last)" == "waiting: UNKNOWN (waiting-prs.json missing"* ]] && ok "a missing PR cache prints UNKNOWN with the reason" || bad "missing PR cache" "$(last)"
prs_file 1 '[]'; rm -f "$READS"
[[ "$(last)" == "waiting: UNKNOWN (desk-reads.jsonl missing"* ]] && ok "a missing Desk read row prints UNKNOWN with the reason" || bad "missing desk reads" "$(last)"
reads_row 30 '[{"card":"ca-1"}]'; prs_file 1 '[]'
[[ "$(last)" == "waiting: UNKNOWN (desk-reads.jsonl is 30h old, over the 24h limit)" ]] && ok "an input older than the threshold prints UNKNOWN naming its age" || bad "old desk read" "$(last)"
[[ "$(DIRECTOR_WAITING_MAX_HOURS=48 "$H" | tail -n 1)" == "waiting: 1 Desk answers not relayed"*"Desk read 30h ago" ]] && ok "the threshold is DIRECTOR_WAITING_MAX_HOURS" || bad "threshold override"
reads_row 1 '[]'; prs_file 26 '[]'
[[ "$(last)" == "waiting: UNKNOWN (waiting-prs.json is 26h old, over the 24h limit)" ]] && ok "an old PR cache prints UNKNOWN naming its age" || bad "old PR cache" "$(last)"
reads_row 30 '[]'; prs_file 26 '[]'
[[ "$(last)" == "waiting: UNKNOWN (desk-reads.jsonl is 30h old"*"; waiting-prs.json is 26h old"* ]] && ok "two dead inputs are both named" || bad "two reasons" "$(last)"
printf 'not json\n' >"$READS"; prs_file 1 '[]'
[[ "$(last)" == "waiting: UNKNOWN (desk-reads.jsonl unreadable)" ]] && ok "an unreadable Desk read row prints UNKNOWN" || bad "garbled reads" "$(last)"
reads_row 1 '[]'; printf '{broken' >"$PRS"
[[ "$(last)" == "waiting: UNKNOWN (waiting-prs.json unreadable)" ]] && ok "an unreadable PR cache prints UNKNOWN" || bad "garbled prs" "$(last)"
printf '{"read_at":"2999-01-01T00:00:00Z","cards":1,"answered":[],"answered_not_relayed":[]}\n' >"$READS"; prs_file 1 '[]'
[[ "$(last)" == "waiting: UNKNOWN (desk-reads.jsonl is stamped in the future"* ]] && ok "a future stamp prints UNKNOWN" || bad "future stamp" "$(last)"

# --- the line is last, the rest is as it was -------------------------------------------------------------------
reads_row 1 '[{"card":"ca-1"}]'; prs_file 1 '[]'
out="$("$H")"
[[ "$(printf '%s\n' "$out" | tail -n 1)" == "waiting: "* && "$(printf '%s\n' "$out" | sed -n 2p)" == "Before you answer:" && "$(printf '%s\n' "$out" | grep -c '^[123]\. ')" == 3 ]] && ok "the waiting line is last and the checklist is as it was" || bad "line order" "$out"
[[ "$(printf '%s\n' "$out" | sed '$d')" == "$before" ]] && ok "every other line matches the output with no inputs" || bad "the other lines changed"

# --- no network, no gh, inside the time budget ---------------------------------------------------------------------
mkdir -p "$root/bin" "$root/gh"
cat >"$root/bin/gh" <<'G'
#!/usr/bin/env bash
echo "$*" >>"${GH_LOG:?}"
f="${GH_DIR:?}/$3.json"
[[ -f "$f" ]] || { echo "no such pull request" >&2; exit 1; }
cat "$f"
G
chmod +x "$root/bin/gh"
export GH_LOG="$root/gh.log" GH_DIR="$root/gh"; : >"$GH_LOG"
t0="$(python3 -c 'import time;print(time.monotonic())')"
PATH="$root/bin:$PATH" "$H" >/dev/null
t1="$(python3 -c 'import time;print(time.monotonic())')"
[[ ! -s "$GH_LOG" ]] && ok "the hook never calls gh" || bad "the hook called gh" "$(cat "$GH_LOG")"
python3 -c 'import sys;sys.exit(0 if float(sys.argv[2])-float(sys.argv[1])<1.0 else 1)' "$t0" "$t1" && ok "the hook finishes inside a second" || bad "the hook took too long"
sans="$(mktemp -d "${TMPDIR:-/tmp}/nogh.XXXXXX")"; ln -s "$(command -v python3)" "$sans/python3"
[[ "$(env -i HOME="$HOME" DIRECTOR_STATE="$DIRECTOR_STATE" PATH="$sans" "$(command -v python3)" "$H" | tail -n 1)" == "waiting: "* ]] && ok "the hook runs with gh off PATH" || bad "no gh on PATH"
rm -rf "$sans"

# --- dws waiting write: the step that calls gh ------------------------------------------------------------------
"$DWS" open ws-a --title a --stage building --link pr:o/r#1 pr:o/r#2 pr:o/r#3 >/dev/null
"$DWS" open ws-b --title b --stage building --link pr:o/r#4 >/dev/null
"$DWS" open ws-p --title p --stage idea --link pr:o/r#5 >/dev/null
"$DWS" park ws-p --from idea >/dev/null
printf '{"state":"OPEN","isDraft":false,"reviewDecision":"APPROVED","reviews":[{"state":"APPROVED","submittedAt":"%s"},{"state":"COMMENTED","submittedAt":"%s"}]}' "$(stamp 4)" "$(stamp 1)" >"$GH_DIR/1.json"
printf '{"state":"MERGED","isDraft":false,"reviewDecision":"APPROVED","reviews":[{"state":"APPROVED","submittedAt":"%s"}]}' "$(stamp 9)" >"$GH_DIR/2.json"
printf '{"state":"OPEN","isDraft":false,"reviewDecision":"REVIEW_REQUIRED","reviews":[]}' >"$GH_DIR/3.json"
printf '{"state":"OPEN","isDraft":true,"reviewDecision":"APPROVED","reviews":[{"state":"APPROVED","submittedAt":"%s"}]}' "$(stamp 2)" >"$GH_DIR/4.json"
printf '{"state":"OPEN","isDraft":false,"reviewDecision":"APPROVED","reviews":[{"state":"APPROVED","submittedAt":"%s"}]}' "$(stamp 6)" >"$GH_DIR/5.json"
: >"$GH_LOG"; rm -f "$PRS"
PATH="$root/bin:$PATH" "$DWS" waiting write >"$root/w.out" 2>&1 && ok "waiting write succeeds with a stub gh" || bad "waiting write" "$(cat "$root/w.out")"
python3 - "$PRS" <<'P' && ok "it keeps the open approved PR only: not merged, unapproved, draft or parked" || bad "the cache holds the wrong PRs" "$(cat "$PRS")"
import json, sys
c = json.load(open(sys.argv[1]))
assert [p["pr"] for p in c["approved_not_merged"]] == ["o/r#1"], c
assert c["checked"] == 4 and c["written_at"].endswith("Z"), c
P
[[ "$(wc -l <"$GH_LOG" | tr -d ' ')" == 4 ]] && ok "it asked gh about each live PR once, and not the parked row's" || bad "gh calls" "$(cat "$GH_LOG")"
rm -f "$GH_DIR/3.json"; cp "$PRS" "$root/prs.before"
PATH="$root/bin:$PATH" "$DWS" waiting write >"$root/w.out" 2>&1 && bad "an unreadable PR is refused" || ok "a PR gh cannot read makes the write refuse"
cmp -s "$PRS" "$root/prs.before" && grep -q 'o/r#3' "$root/w.out" && ok "the old cache is untouched and the PR is named" || bad "refused write changed the cache" "$(cat "$root/w.out")"

# --- an approval counts only at the current head, by a non-author, when GitHub gives no decision ---------
cp "$root/prs.before" "$PRS"
printf '{"state":"OPEN","isDraft":false,"reviewDecision":"REVIEW_REQUIRED","reviews":[]}' >"$GH_DIR/3.json"
"$DWS" open ws-c --title c --stage building --link pr:o/r#6 pr:o/r#7 pr:o/r#8 pr:o/r#9 >/dev/null
rev() {  # LOGIN COMMIT AGE_H
  printf '{"state":"APPROVED","author":{"login":"%s"},"commit":{"oid":"%s"},"submittedAt":"%s"}' "$1" "$2" "$(stamp "$3")"
}
pr() {  # AUTHOR REVIEW_JSON...
  local a="$1"; shift; local IFS=,
  printf '{"state":"OPEN","isDraft":false,"reviewDecision":"","author":{"login":"%s"},"headRefOid":"head2","reviews":[%s]}' "$a" "$*"
}
pr alice "$(rev alice head2 3)" >"$GH_DIR/6.json"                 # the author's own approval
pr alice "$(rev bob head1 3)" >"$GH_DIR/7.json"                   # a non-author approval of an older commit
pr alice "$(rev bob head2 5)" "$(rev carol head2 2)" >"$GH_DIR/8.json"   # two approvals at the head
printf '{"state":"OPEN","isDraft":false,"reviewDecision":"","author":{"login":"alice"},"headRefOid":"head2","reviews":[{"state":"COMMENTED","author":{"login":"bob"},"commit":{"oid":"head2"},"submittedAt":"%s"}]}' "$(stamp 1)" >"$GH_DIR/9.json"
"$DWS" open ws-d --title d --stage building --link pr:o/r#10 pr:o/r#11 pr:o/r#12 pr:o/r#13 >/dev/null
rv() {  # LOGIN STATE AGE_H
  printf '{"state":"%s","author":{"login":"%s"},"commit":{"oid":"head2"},"submittedAt":"%s"}' "$2" "$1" "$(stamp "$3")"
}
pr alice "$(rv bob APPROVED 5)" "$(rv bob CHANGES_REQUESTED 2)" >"$GH_DIR/10.json"   # a later change request cancels the approval
pr alice "$(rv bob APPROVED 5)" "$(rv bob DISMISSED 2)" >"$GH_DIR/11.json"           # a later dismissal cancels it
pr alice "$(rv bob APPROVED 5)" "$(rv bob COMMENTED 2)" >"$GH_DIR/12.json"           # a later comment does not
pr alice "$(rv bob CHANGES_REQUESTED 5)" "$(rv bob APPROVED 2)" "$(rv carol CHANGES_REQUESTED 1)" >"$GH_DIR/13.json"   # bob re-approved, carol blocks: bob's counts
PATH="$root/bin:$PATH" "$DWS" waiting write >"$root/w.out" 2>&1 && ok "waiting write succeeds with the review cases" || bad "waiting write" "$(cat "$root/w.out")"
python3 - "$PRS" <<'P' && ok "an own approval, an older commit's, a comment, and an approval followed by a change request or a dismissal do not count; a non-author approval at the head does" || bad "approval rule" "$(cat "$PRS")"
import json, sys
c = json.load(open(sys.argv[1]))
got = {p["pr"]: p["approved_at"] for p in c["approved_not_merged"]}
assert sorted(got) == ["o/r#1", "o/r#12", "o/r#13", "o/r#8"], c
assert c["checked"] == 12, c
P

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
