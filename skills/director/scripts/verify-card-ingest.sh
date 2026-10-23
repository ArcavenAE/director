#!/usr/bin/env bash
# Prove card-ingest (sim/design/card-ingest.md, issue #320): one parse from a
# seat's card file to a Desk write, which refuses a card missing a part, all or
# nothing per file, and a read-back check of what the Desk holds afterwards.
#
# Fixtures are written fresh as heredocs on a neutral subject (a report schedule,
# a log retention period); none is copied from a real card.
#
# Usage: skills/director/scripts/verify-card-ingest.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CI="${CI_BIN:-$here/card-ingest}"
SNAP="${SNAP:-$here/desk-snapshot}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d "${TMPDIR:-/tmp}/card-ingest.XXXXXX")"
trap 'rm -rf "$root"' EXIT
export DIRECTOR_STATE="$root/state"
mkdir -p "$DIRECTOR_STATE"
export DWS_TEST_CLOCK=1
export PYTHONDONTWRITEBYTECODE=1
BATCHDIR="$DIRECTOR_STATE/card-ingest"
LOG="$DIRECTOR_STATE/card-ingest.jsonl"
NOW="2026-10-19T12:00:00Z"

nbatch() { if [[ -d "$BATCHDIR" ]]; then find "$BATCHDIR" -type f | wc -l | tr -d ' '; else echo 0; fi; }
nlog() { if [[ -f "$LOG" ]]; then wc -l <"$LOG" | tr -d ' '; else echo 0; fi; }
clean_state() { rm -rf "$BATCHDIR" "$LOG" "$DIRECTOR_STATE/card-ingest.lock"; }

# The Desk read the session hands desk-snapshot: one relayed card, one open and
# unanswered, one open and answered, one closed.
write_snapshot() {  # [DWS_NOW for the read]
  cat >"$root/cards.json" <<'J'
{"rpt-3": {"status": "relayed"},
 "rpt-4": {"status": "open"},
 "rpt-5": {"status": "open"},
 "rpt-6": {"status": "closed"},
 "keep": {"status": "open"}}
J
  printf '[{"card": "rpt-5"}]' >"$root/answers.json"
  DWS_NOW="${1:-2026-10-19T10:00:00Z}" "$SNAP" write --cards "$root/cards.json" --reported 5 \
    --answers "$root/answers.json" --answers-reported 1 >/dev/null
}

# One valid card, every part on one line except the options.
BASE="$root/base.md"
cat >"$BASE" <<'M'
Source: bus 01EXAMPLE, the request for this card

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
# sub OUT OLD NEW: BASE with the first OLD replaced (OLD must exist, so a typo cannot pass a case).
sub() {
  python3 - "$BASE" "$1" "$2" "$3" <<'P'
import sys
src, out, old, new = sys.argv[1:5]
t = open(src).read()
assert old in t, "fixture text not in the base card: %r" % old
open(out, "w").write(t.replace(old, new, 1))
P
}
# text OUT: BASE replaced by stdin.
text() { cat >"$1"; }

run() {  # FILE [extra args]  (prefix rpt, area and owner fixed)
  local f="$1"; shift
  "$CI" parse "$f" --area test-area --owner test-seat --prefix rpt "$@"
}
# refuses LABEL FILE WORD [args]: exit 1, "refused" and WORD on stderr, no batch file, no log line.
refuses() {
  local label="$1" f="$2" word="$3"; shift 3
  clean_state
  local rc=0
  run "$f" "$@" >"$root/out" 2>"$root/err" || rc=$?
  if [[ $rc -ne 1 ]]; then bad "$label" "exit $rc, want 1: $(cat "$root/err")"
  elif ! grep -q "refused" "$root/err"; then bad "$label" "no 'refused' line: $(cat "$root/err")"
  elif ! grep -qiF -- "$word" "$root/err"; then bad "$label" "refused without '$word': $(cat "$root/err")"
  elif [[ "$(nbatch)" != 0 || "$(nlog)" != 0 ]]; then bad "$label" "wrote a batch or a log line anyway"
  else ok "$label"; fi
}
# accepts LABEL FILE: exit 0 and one batch file.
accepts() {
  local label="$1" f="$2"; shift 2
  clean_state
  if run "$f" "$@" >"$root/out" 2>"$root/err"; then ok "$label"; else bad "$label" "$(cat "$root/err")"; fi
}
batch_json() { find "$BATCHDIR" -type f | head -1; }

export DWS_NOW="$NOW"
write_snapshot

# --- positive control: a valid two-card file --------------------------------
two="$root/two.md"
python3 - "$BASE" "$two" <<'P'
import sys
t = open(sys.argv[1]).read()
second = t.split("\n", 2)[2].replace("## Card 1. Should the nightly report run at 06:00 or 07:00?", "## Card 2. Should the log be kept 30 or 90 days?").replace("Option 2.", "Option 1.")
open(sys.argv[2], "w").write(t + "\n" + second)
P
clean_state
if run "$two" >"$root/out" 2>"$root/err"; then
  python3 - "$(batch_json)" "$NOW" "$LOG" <<'P' && ok "positive control: two valid cards give two set writes with every Desk field and two log lines" || bad "positive control: batch content" "$(cat "$root/out")"
import json, sys
b = json.load(open(sys.argv[1]))
assert b["made_at"] == sys.argv[2], b
assert len(b["file_sha256"]) == 64, b
w = b["writes"]
assert [x["doc_id"] for x in w] == ["rpt-1", "rpt-2"], w
for x in w:
    assert x["op"] == "set" and x["collection"] == "decisions", x
    d = x["data"]
    assert sorted(d) == sorted(["title", "question", "context", "options", "rec", "rec_key", "expiry", "source", "kind", "area", "owner", "status", "ruling", "opened"]), sorted(d)
    assert d["status"] == "open" and d["ruling"] == "" and d["opened"] == sys.argv[2], d
    assert d["area"] == "test-area" and d["owner"] == "test-seat" and d["kind"] == "choice", d
d = w[0]["data"]
assert d["title"] == "Should the nightly report run at 06:00 or 07:00?", d["title"]
assert d["question"] == "Should the nightly report run at 06:00 or 07:00?", d["question"]
assert d["options"] == [{"key": "1", "text": "*Keep 06:00.* Nothing changes. The cost is a report short of late data."}, {"key": "2", "text": "*Move to 07:00.* One schedule line changes. The cost is a later report.", "recommended": True}], d["options"]
assert d["rec_key"] == "2", d["rec_key"]
assert d["rec"] == "Option 2. Complete data matters more than an hour.", d["rec"]
assert d["expiry"] == "2026-11-02. The reporting seat re-checks it then.", d["expiry"]
assert d["source"] == "bus 01EXAMPLE, the request for this card", d["source"]
assert d["context"].startswith("**IN SHORT.** The report runs"), d["context"]
assert d["context"].index("**IN SHORT.**") < d["context"].index("**The problem.**") < d["context"].index("**Terms.**"), d["context"]
lines = [json.loads(l) for l in open(sys.argv[3])]
assert len(lines) == 2 and [l["slug"] for l in lines] == ["rpt-1", "rpt-2"], lines
assert all(l["op"] == "set" and l["at"] == sys.argv[2] and len(l["data_sha256"]) == 64 and len(l["file_sha256"]) == 64 for l in lines), lines
assert [l["card"] for l in lines] == [1, 2], lines
P
  [[ "$(basename "$(batch_json)")" =~ ^rpt-[0-9a-f]{8}\.json$ ]] && ok "the batch file is named <prefix>-<sha8>.json" || bad "the batch file is named <prefix>-<sha8>.json" "$(basename "$(batch_json)")"
  grep -q "rpt-1" "$root/out" && grep -q "rpt-2" "$root/out" && ok "parse prints the batch path and each slug" || bad "parse prints the batch path and each slug" "$(cat "$root/out")"
else
  bad "positive control: a valid two-card file is accepted" "$(cat "$root/err")"
fi

# --- accepted spellings ------------------------------------------------------
sub "$root/a1.md" '**The problem.**' '**Problem.**'; accepts "accepts **Problem.** for the problem part" "$root/a1.md"
sub "$root/a2.md" '**Recommendation.**' '**Recommendation:**'; accepts "accepts **Recommendation:**" "$root/a2.md"
sub "$root/a3.md" 'The cost is a later report.' 'The cost is a later report. [RECOMMENDED]'
clean_state
if run "$root/a3.md" >/dev/null 2>"$root/err" && python3 - "$(batch_json)" <<'P'
import json, sys
o = json.load(open(sys.argv[1]))["writes"][0]["data"]["options"]
assert o[1]["text"].endswith("The cost is a later report."), o
P
then ok "accepts an agreeing RECOMMENDED marker, and removes it from the option text"; else bad "accepts an agreeing RECOMMENDED marker" "$(cat "$root/err")"; fi
sub "$root/a4.md" '2026-11-02. The reporting' '2026-11-02, or until the schedule change merges, whichever comes first. The reporting'; accepts "accepts the event clause" "$root/a4.md"
sub "$root/a5.md" '2026-11-02. The reporting' '2026-11-02 (the schedule review), or until the schedule change merges, whichever comes first. The reporting'; accepts "accepts the event clause with an aside after the date" "$root/a5.md"
sub "$root/a6.md" '2026-11-02. The reporting' '2026-11-02 (a Monday). The reporting'; accepts "accepts an aside after a plain date" "$root/a6.md"
sub "$root/a7.md" '**Terms.** None.' '**Terms.** None'; accepts "accepts Terms with None" "$root/a7.md"
printf 'Source: bus 01EXAMPLE\n\nSome commentary for the director, a table, and a long preamble.\n| a | b |\n\n' >"$root/a8.md"
python3 - "$BASE" "$root/a8.md" <<'P'
import sys
t = open(sys.argv[1]).read()
body = t.split("\n", 2)[2]
open(sys.argv[2], "a").write(body.replace("\n\n**Terms.**", "\n\n**Terms.**"))
P
clean_state
if run "$root/a8.md" >"$root/out" 2>"$root/err" && grep -qi "skipped" "$root/out" "$root/err"; then ok "preamble prose is skipped and counted, not dropped silently"; else bad "preamble prose is skipped and counted" "$(cat "$root/out" "$root/err")"; fi
# a preamble Source applies to every card; a card may set its own
clean_state
run "$two" >/dev/null 2>&1 && python3 - "$(batch_json)" <<'P' && ok "a preamble Source applies to every card" || bad "a preamble Source applies to every card"
import json, sys
w = json.load(open(sys.argv[1]))["writes"]
assert all(x["data"]["source"] == "bus 01EXAMPLE, the request for this card" for x in w), w
P
python3 - "$BASE" "$root/a9.md" <<'P'
import sys
t = open(sys.argv[1]).read()
t = t.replace("Source: bus 01EXAMPLE, the request for this card\n\n", "")
t = t.replace("**Terms.** None.", "**Source.** the card's own pointer\n\n**Terms.** None.")
open(sys.argv[2], "w").write(t)
P
accepts "a card with its own Source part and no preamble Source is accepted" "$root/a9.md"
# a context section is kept; a Kind part sets the kind
sub "$root/a10.md" '**Terms.** None.' $'**Terms.** None.\n\n**Why it came up.** The schedule changed last week.'
clean_state; run "$root/a10.md" >/dev/null 2>&1 && python3 - "$(batch_json)" <<'P' && ok "a bold-led context section is kept in the context, in file order" || bad "a bold-led context section is kept in the context"
import json, sys
c = json.load(open(sys.argv[1]))["writes"][0]["data"]["context"]
assert "**Why it came up.** The schedule changed last week." in c and c.index("**Terms.**") < c.index("**Why it came up.**"), c
P
python3 - "$BASE" "$root/act.md" <<'P'
import sys
t = open(sys.argv[1]).read()
t = t.replace("1. *Keep 06:00.* Nothing changes. The cost is a report short of late data.\n2. *Move to 07:00.* One schedule line changes. The cost is a later report.", "1. *Do it.* One change. The cost is an hour.")
t = t.replace("Option 2.", "Option 1.")
t = t.replace("**Terms.** None.", "**Terms.** None.\n\n**Kind.** action")
open(sys.argv[2], "w").write(t)
P
clean_state; run "$root/act.md" >"$root/out" 2>"$root/err" && python3 - "$(batch_json)" <<'P' && ok "an action card with one option and Kind action is accepted" || bad "an action card with one option is accepted" "$(cat "$root/err")"
import json, sys
d = json.load(open(sys.argv[1]))["writes"][0]["data"]
assert d["kind"] == "action" and len(d["options"]) == 1, d
P

# a wrapped option whose last line ends in RECOMMENDED: the marker is read at the end of the
# option's whole text, checked against the recommendation, and removed from the text
python3 - "$BASE" "$root/w1.md" "$root/w2.md" <<'P'
import sys
t = open(sys.argv[1]).read()
wrapped = t.replace("1. *Keep 06:00.* Nothing changes. The cost is a report short of late data.",
                    "1. *Keep 06:00.* Nothing changes.\n   The cost is a report short of late data. [RECOMMENDED]")
open(sys.argv[2], "w").write(wrapped)
open(sys.argv[3], "w").write(wrapped.replace("Option 2.", "Option 1."))
P
refuses "refuses a wrapped option 1 ending in RECOMMENDED when option 2 is recommended" "$root/w1.md" "RECOMMENDED marks option 1 and the recommendation names option 2"
clean_state
if run "$root/w2.md" >/dev/null 2>"$root/err" && python3 - "$(batch_json)" <<'P'
import json, sys
o = json.load(open(sys.argv[1]))["writes"][0]["data"]["options"]
assert "RECOMMENDED" not in o[0]["text"] and o[0]["text"].endswith("late data."), o
P
then ok "an agreeing marker at the end of a wrapped option is accepted and removed from the text"; else bad "a wrapped agreeing marker is removed" "$(cat "$root/err")"; fi
# RECOMMENDED may end any line of an option: its first, a middle or its last. It is read
# wherever it ends a line, checked against the recommendation, and removed from the text.
for pos in first middle last; do
  python3 - "$BASE" "$pos" "$root/rec-agree-$pos.md" "$root/rec-differ-$pos.md" <<'P'
import sys
t = open(sys.argv[1]).read()
pos = sys.argv[2]
mark = {"first": ["", ""], "middle": ["", ""], "last": ["", ""]}
lines = ["1. *Keep 06:00.* Nothing changes.", "   The cost is a report short of late data.", "   More detail on this option."]
i = {"first": 0, "middle": 1, "last": 2}[pos]
lines[i] += " [RECOMMENDED]"
opt = "\n".join(lines)
old = "1. *Keep 06:00.* Nothing changes. The cost is a report short of late data."
agree = t.replace(old, opt).replace("Option 2.", "Option 1.")
differ = t.replace(old, opt)
open(sys.argv[3], "w").write(agree)
open(sys.argv[4], "w").write(differ)
P
  refuses "refuses a RECOMMENDED marker ending the $pos line of option 1 when option 2 is recommended" "$root/rec-differ-$pos.md" "RECOMMENDED marks option 1 and the recommendation names option 2"
  clean_state
  if run "$root/rec-agree-$pos.md" >/dev/null 2>"$root/err" && python3 - "$(batch_json)" <<'P'
import json, sys
o = json.load(open(sys.argv[1]))["writes"][0]["data"]["options"]
assert "RECOMMENDED" not in o[0]["text"] and o[0]["text"].count("\n") == 2, o
P
  then ok "an agreeing RECOMMENDED marker ending the $pos line of option 1 is accepted and removed"; else bad "an agreeing marker on the $pos line" "$(cat "$root/err")"; fi
done
python3 - "$BASE" "$root/rec-twice.md" <<'P'
import sys
t = open(sys.argv[1]).read()
open(sys.argv[2], "w").write(t.replace("1. *Keep 06:00.* Nothing changes. The cost is a report short of late data.", "1. *Keep 06:00.* Nothing changes. [RECOMMENDED]\n   The cost is a report short of late data. [RECOMMENDED]"))
P
refuses "refuses a marker on two lines of one option" "$root/rec-twice.md" "option 1 carries RECOMMENDED on 2 lines"
# an action card whose Options part is prose, with no numbered line
python3 - "$BASE" "$root/prose.md" <<'P'
import sys
t = open(sys.argv[1]).read().replace("**Terms.** None.", "**Terms.** None.\n\n**Kind.** action")
a = t.index("1. *Keep")
b = t.index("**Recommendation.**")
t = t[:a] + "Just do it, there is nothing to choose.\n\n" + t[b:]
t = t.replace("Option 2.", "Option 1.")
open(sys.argv[2], "w").write(t)
P
refuses "an action card whose Options part has no numbered line is refused as 'an action card needs at least one option; found 0'" "$root/prose.md" "an action card needs at least one option; found 0"

# The marker grammar: only the bracketed token [RECOMMENDED], ending a line of the option. The bare
# word in prose is text: it is never a marker and never a refusal, and the text keeps it.
for rec in 1 2; do
  python3 - "$BASE" "$rec" "$root/prose-$rec.md" <<'P'
import sys
t = open(sys.argv[1]).read()
t = t.replace("1. *Keep 06:00.* Nothing changes. The cost is a report short of late data.",
              "1. *Keep 06:00.* Nothing changes. The cost is a report short of late data and this is not RECOMMENDED")
t = t.replace("Option 2.", "Option %s." % sys.argv[2])
open(sys.argv[3], "w").write(t)
P
  clean_state
  if run "$root/prose-$rec.md" >"$root/out" 2>"$root/err" && python3 - "$(batch_json)" <<'P'
import json, sys
o = json.load(open(sys.argv[1]))["writes"][0]["data"]["options"]
assert o[0]["text"].endswith("and this is not RECOMMENDED"), o
P
  then ok "prose ending in the word RECOMMENDED is text with the recommendation on option $rec: accepted, text intact"; else bad "prose ending in RECOMMENDED, recommendation on option $rec" "$(cat "$root/err")"; fi
  grep -q "without brackets" "$root/out" && ok "a bare RECOMMENDED at a line end is noted, not refused (option $rec)" || bad "a bare RECOMMENDED at a line end is noted" "$(cat "$root/out")"
done
python3 - "$BASE" "$root/prose-mid.md" <<'P'
import sys
t = open(sys.argv[1]).read().replace("Nothing changes.", "Nothing changes. It is RECOMMENDED by nobody, and RECOMMENDED. is also text.")
open(sys.argv[2], "w").write(t)
P
# case_prose_mid: 0 when the word inside a sentence is text, 1 when the card is refused or changed.
case_prose_mid() {
  clean_state; : >"$root/why"
  run "$root/prose-mid.md" >/dev/null 2>"$root/why" || return 1
  python3 - "$(batch_json)" <<'P' 2>>"$root/why"
import json, sys
t = json.load(open(sys.argv[1]))["writes"][0]["data"]["options"][0]["text"]
assert "It is RECOMMENDED by nobody, and RECOMMENDED. is also text." in t, t
P
}
case_prose_mid && ok "the word RECOMMENDED inside a sentence is text" || bad "the word RECOMMENDED inside a sentence is text" "$(cat "$root/why")"
python3 - "$BASE" "$root/dot.md" <<'P'
import sys
t = open(sys.argv[1]).read().replace("The cost is a later report.", "The cost is a later report. [RECOMMENDED].")
open(sys.argv[2], "w").write(t)
P
clean_state; run "$root/dot.md" >/dev/null 2>"$root/err" && python3 - "$(batch_json)" <<'P' && ok "[RECOMMENDED]. with a final period is a marker too" || bad "[RECOMMENDED]. is a marker" "$(cat "$root/err")"
import json, sys
o = json.load(open(sys.argv[1]))["writes"][0]["data"]["options"]
assert "RECOMMENDED" not in o[1]["text"], o
P

# Near forms of the marker are text, and say so: none is read as the marker, none is refused, each
# draws a NOTE that names the exact form, and the Desk text keeps what the seat wrote. The
# recommendation is on option 2, so a near form read as a marker would be refused as a disagreement.
# A token inside a code fence is an example, never a marker.
near_case() {  # label  option-1 text (python expression of the first option's whole text)  [note|nonote]
  local label="$1" body="$2" want="${3:-note}"
  python3 - "$BASE" "$root/near.md" "$body" <<'P'
import sys
t = open(sys.argv[1]).read()
old = "1. *Keep 06:00.* Nothing changes. The cost is a report short of late data."
assert old in t
open(sys.argv[2], "w").write(t.replace(old, sys.argv[3].replace("\\n", "\n"), 1))
P
  clean_state
  if run "$root/near.md" >"$root/out" 2>"$root/err"; then
    python3 - "$(batch_json)" "$body" <<'P' && ok "near form ($label) is read as text and kept in the Desk text" || bad "near form ($label): the text was changed" "$(cat "$root/out")"
import json, sys
o = json.load(open(sys.argv[1]))["writes"][0]["data"]["options"][0]["text"]
want = sys.argv[2].replace("\\n", "\n")
want = want.split(" ", 1)[1] if want[:3] == "1. " else want
assert o == want, (o, want)
P
    if [[ "$want" == note ]]; then
      grep -q 'is not the marker; it is read as text. The marker is \[RECOMMENDED\] ending a line' "$root/out" \
        && ok "near form ($label) draws a NOTE naming the accepted form" || bad "near form ($label): no NOTE naming the accepted form" "$(cat "$root/out")"
    else
      ! grep -q 'is not the marker' "$root/out" && ok "a fenced token ($label) draws no NOTE" || bad "a fenced token ($label) drew a NOTE" "$(cat "$root/out")"
    fi
  else
    bad "near form ($label) is accepted as text" "$(cat "$root/err")"
  fi
}
near_case "lowercase at the end of a line" '1. *Keep 06:00.* The cost is a late report. [recommended]'
near_case "spaces inside the brackets" '1. *Keep 06:00.* The cost is a late report. [ RECOMMENDED ]'
near_case "a trailing colon" '1. *Keep 06:00.* The cost is a late report. [RECOMMENDED]:'
near_case "at the start of a line" '1. [RECOMMENDED] *Keep 06:00.* The cost is a late report.'
near_case "in the middle of a line" '1. *Keep 06:00.* [RECOMMENDED] The cost is a late report.'
near_case "in backticks" '1. *Keep 06:00.* The cost is a late report. `[RECOMMENDED]`'
near_case "a wrapped line, lowercase" '1. *Keep 06:00.* Nothing changes.\n   The cost is a late report. [recommended]'
near_case "inside a code fence" '1. *Keep 06:00.* Nothing changes.\n   ```\n   write [RECOMMENDED]\n   ```\n   The cost is a late report.' nonote

# More near forms: bold and capitalised spellings draw a NOTE and are text.
near_case "bold upper case" '1. *Keep 06:00.* The cost is a late report. **RECOMMENDED**'
near_case "bold capitalised with a period" '1. *Keep 06:00.* The cost is a late report. **Recommended.**'
python3 - "$BASE" "$root/capword.md" <<'P'
import sys
t = open(sys.argv[1]).read()
old = "1. *Keep 06:00.* Nothing changes. The cost is a report short of late data."
open(sys.argv[2], "w").write(t.replace(old, "1. *Keep 06:00.* Nothing changes. The cost is a report short of late data. Recommended.", 1))
P
clean_state
if run "$root/capword.md" >"$root/out" 2>"$root/err" && grep -q 'without brackets' "$root/out"; then ok "a line ending in Recommended. in any case draws the bare-word NOTE and is text"; else bad "a capitalised Recommended. draws a NOTE" "$(cat "$root/out" "$root/err")"; fi

# Code fences. A fence line is made of the fence token (three or more backticks, or tildes), then an
# optional info string, and nothing else; a closer is the same character, at least as long, with no
# info string. State resets at every option, and an option that ends inside an open fence is refused,
# so a fence can never swallow the next option's marker. A line that merely starts with inline
# triple-backtick code is not a fence.
fence_case() {  # label  opt1  opt2  rec  want   (want: accept | refuse:<text>)
  local label="$1" o1="$2" o2="$3" rec="$4" want="$5"
  python3 - "$BASE" "$root/fence-case.md" "$o1" "$o2" "$rec" <<'P'
import sys
t = open(sys.argv[1]).read()
a = "1. *Keep 06:00.* Nothing changes. The cost is a report short of late data."
b = "2. *Move to 07:00.* One schedule line changes. The cost is a later report."
assert a in t and b in t
t = t.replace(a, sys.argv[3].replace("\\n", "\n")).replace(b, sys.argv[4].replace("\\n", "\n"))
t = t.replace("Option 2.", "Option %s." % sys.argv[5])
open(sys.argv[2], "w").write(t)
P
  clean_state
  local rc=0; run "$root/fence-case.md" >"$root/out" 2>"$root/err" || rc=$?
  if [[ "$want" == accept ]]; then
    [[ $rc -eq 0 ]] && ok "fence: $label" || bad "fence: $label" "$(cat "$root/err")"
  else
    [[ $rc -eq 1 ]] && grep -qF -- "${want#refuse:}" "$root/err" && ok "fence: $label" || bad "fence: $label" "rc $rc: $(cat "$root/err")"
  fi
}
O2='2. *Move to 07:00.* One schedule line changes. The cost is a later report.'
O1='1. *Keep 06:00.* Nothing changes. The cost is a late report.'
fence_case "a fence opened in option 1 and closed in option 2 is refused, naming option 1" \
  "1. *Keep 06:00.* The cost is a late report.\n   \`\`\`\n   some code" "2. *Move to 07:00.* More code. The cost is an hour.\n   \`\`\`\n   text [RECOMMENDED]" 1 \
  "option 1 ends inside an open code fence"
fence_case "a fence left open in the last option is refused" \
  "$O1" "2. *Move to 07:00.* The cost is an hour.\n   \`\`\`\n   [RECOMMENDED]" 1 "option 2 ends inside an open code fence"
fence_case "a tilde fence holds a token that is never the marker" \
  "1. *Keep 06:00.* Nothing changes.\n   ~~~\n   write [RECOMMENDED]\n   ~~~\n   The cost is a late report." "$O2" 2 accept
fence_case "a tilde fence left open is refused" \
  "1. *Keep 06:00.* The cost is a late report.\n   ~~~\n   [RECOMMENDED]" "$O2" 2 "option 1 ends inside an open code fence"
fence_case "a closer of the other character does not close a fence" \
  "1. *Keep 06:00.* The cost is a late report.\n   \`\`\`\n   code\n   ~~~" "$O2" 2 "option 1 ends inside an open code fence"
fence_case "a fence with an info string opens and closes, and the token inside is text" \
  "1. *Keep 06:00.* Nothing changes.\n   \`\`\`bash\n   echo [RECOMMENDED]\n   \`\`\`\n   The cost is a late report." "$O2" 2 accept
fence_case "a line that starts with inline triple-backtick code is not a fence: the marker after it is read" \
  "1. *Keep 06:00.* The cost is a late report.\n   \`\`\`x\`\`\` is inline code [RECOMMENDED]" "$O2" 2 \
  "RECOMMENDED marks option 1 and the recommendation names option 2"
fence_case "a fence line may carry any leading whitespace (8 spaces here), wider than CommonMark" \
  "1. *Keep 06:00.* Nothing changes.\n        \`\`\`\n        write [RECOMMENDED]\n        \`\`\`\n   The cost is a late report." "$O2" 2 accept
fence_case "a closer may carry trailing spaces" \
  "1. *Keep 06:00.* Nothing changes.\n   \`\`\`\n   write [RECOMMENDED]\n   \`\`\`   \n   The cost is a late report." "$O2" 2 accept
fence_case "a longer closer closes a shorter opener" \
  "1. *Keep 06:00.* Nothing changes.\n   \`\`\`\n   [RECOMMENDED]\n   \`\`\`\`\n   The cost is a late report." "$O2" 2 accept

# A log append that fails partway leaves the log as it was: no partial line without its batch.
# RLIMIT_FSIZE stops the append part-way through; the seed log is under the limit, the batch is too.
# CI_RLIMIT_SLACK (default 60) is how many bytes past the seed log the cap allows; a slack large
# enough that the cap never bites makes the case fail loudly, never stop the run.
# case_rlimit: 0 restored and no batch; 1 the log or the batch was left behind; 2 the cap never bit.
case_rlimit() {
  clean_state; mkdir -p "$DIRECTOR_STATE"
  python3 - "$LOG" <<'P'
import json, sys
with open(sys.argv[1], "w") as f:
    for i in range(30):
        f.write(json.dumps({"at": "2026-10-01T00:00:00Z", "checked": True, "batch": "old-%03d.json" % i, "slugs": [], "failed": 0}) + "\n")
P
  cp "$LOG" "$root/log.before"
  lsize="$(wc -c <"$LOG" | tr -d ' ')"
  local prc
  prc="$(python3 - "$CI" "$BASE" "$((lsize + ${CI_RLIMIT_SLACK:-60}))" "$root/err" <<'P'
import resource, subprocess, sys
ci, base, limit, errf = sys.argv[1], sys.argv[2], int(sys.argv[3]), sys.argv[4]
def cap():
    resource.setrlimit(resource.RLIMIT_FSIZE, (limit, limit))
with open(errf, "w") as e:
    print(subprocess.run([ci, "parse", base, "--area", "a", "--owner", "o", "--prefix", "rpt"], stdout=subprocess.DEVNULL, stderr=e, preexec_fn=cap).returncode)
P
)" || prc=2
  [[ "$prc" != 0 ]] || return 2
  cmp -s "$LOG" "$root/log.before" || { echo "now $(wc -c <"$LOG" | tr -d ' ') bytes, was $lsize" >"$root/why"; return 1; }
  [[ "$(nbatch)" == 0 ]] || { find "$BATCHDIR" -type f >"$root/why"; return 1; }
  return 0
}
rc=0; case_rlimit || rc=$?
case "$rc" in
  0) ok "an append cut short by the file size limit is refused, the log is restored byte for byte and no batch is left";;
  2) bad "an append cut short by the file size limit is refused" "the cap did not cut the append short (parse exited 0), so the case tests nothing";;
  *) bad "an append cut short leaves the log byte for byte as it was and no batch" "$(cat "$root/why")";;
esac
clean_state

# a log that cannot be appended leaves no batch behind
clean_state; mkdir -p "$LOG"
if run "$BASE" >/dev/null 2>"$root/err"; then bad "a log-append failure is refused" "accepted"
elif [[ "$(nbatch)" != 0 ]]; then bad "a log-append failure leaves no batch file" "$(find "$BATCHDIR" -type f)"
else ok "a log-append failure leaves no batch file behind"; fi
rmdir "$LOG"; clean_state

# --- refusals, one case each -------------------------------------------------
printf 'Source: x\n\nJust some text.\n' >"$root/r.md"; refuses "refuses a file with no card heading" "$root/r.md" "no card heading"
sub "$root/r.md" 'run at 06:00 or 07:00?' 'run at 06:00 or 07:00.'; refuses "refuses a heading with no question mark" "$root/r.md" "no question"
sub "$root/r.md" 'at 06:00 or 07:00?' 'at 06:00? Or at 07:00?'; refuses "refuses a heading that asks two questions" "$root/r.md" "2 questions"
for part in 'IN SHORT' 'The problem' 'Terms' 'Options' 'Recommendation' 'Valid until'; do
  python3 - "$BASE" "$root/r.md" "$part" <<'P'
import re, sys
t = open(sys.argv[1]).read()
lab = sys.argv[3]
out = []
skip = False
for l in t.split("\n"):
    if l.startswith("**" + lab):
        skip = True
        continue
    if skip and (l.startswith("**") or l == ""):
        if l.startswith("**"):
            skip = False
        elif l == "":
            skip = False
            continue
        else:
            pass
    if skip:
        continue
    out.append(l)
open(sys.argv[2], "w").write("\n".join(out))
P
  w="$(echo "$part" | sed 's/^The //' | tr 'A-Z' 'a-z')"
  refuses "refuses a card missing the $part part" "$root/r.md" "no $w part"
done
python3 - "$BASE" "$root/r.md" <<'P'
import sys
t = open(sys.argv[1]).read().replace("Source: bus 01EXAMPLE, the request for this card\n\n", "")
open(sys.argv[2], "w").write(t)
P
refuses "refuses a card with no Source on it or above it" "$root/r.md" "no source part"
grep -q "Source: <pointer>" "$root/err" && ok "the missing-source refusal names how to add one" || bad "the missing-source refusal names how to add one" "$(cat "$root/err")"
sub "$root/r.md" '**Terms.** None.' '**Terms.**'; refuses "refuses an empty part" "$root/r.md" "the terms part is empty"
sub "$root/r.md" '**Terms.** None.' $'**Terms.** None.\n\n**Terms.** None again.'; refuses "refuses a part written twice" "$root/r.md" "two terms parts"
sub "$root/r.md" '**Terms.** None.' $'**Terms.** None.\n\nPointer: sim/design/x.md'; refuses "refuses a Word: line that is not a card part" "$root/r.md" "'Pointer:' is not a card part"
sub "$root/r.md" '2. *Move' '3. *Move'; refuses "refuses an option-number gap" "$root/r.md" "numbered 1, 3"
python3 - "$BASE" "$root/r.md" <<'P'
import sys
t = open(sys.argv[1]).read().replace("2. *Move to 07:00.* One schedule line changes. The cost is a later report.\n", "").replace("Option 2.", "Option 1.")
open(sys.argv[2], "w").write(t)
P
refuses "refuses a one-option choice card" "$root/r.md" "at least two options; found 1"
sub "$root/r.md" '**Terms.** None.' $'**Terms.** None.\n\n**Kind.** maybe'; refuses "refuses an unknown kind" "$root/r.md" "kind 'maybe'"
sub "$root/r.md" 'Option 2. Complete' 'Complete'; refuses "refuses a recommendation naming no option" "$root/r.md" "names no option"
sub "$root/r.md" 'Option 2. Complete' 'Option 5. Complete'; refuses "refuses a recommendation naming a missing option" "$root/r.md" "names option 5; the card has 2"
sub "$root/r.md" 'Option 2. Complete' 'Option 1 or Option 2. Complete'; refuses "refuses a recommendation naming two options" "$root/r.md" "names options 1 and 2; recommend one"
sub "$root/r.md" 'The cost is a report short of late data.' 'The cost is a report short of late data. [RECOMMENDED]'; refuses "refuses a RECOMMENDED marker that disagrees with the recommendation" "$root/r.md" "RECOMMENDED marks option 1 and the recommendation names option 2"
python3 - "$BASE" "$root/r.md" <<'P'
import sys
t = open(sys.argv[1]).read().replace("short of late data.", "short of late data. [RECOMMENDED]").replace("a later report.", "a later report. [RECOMMENDED]")
open(sys.argv[2], "w").write(t)
P
refuses "refuses two RECOMMENDED markers" "$root/r.md" "RECOMMENDED marks 2 options"
sub "$root/r.md" '2026-11-02. The reporting' 'until the schedule change merges. The reporting'; refuses "refuses an event-only expiry" "$root/r.md" "does not start with a date"
sub "$root/r.md" '2026-11-02.' '2026-02-30.'; refuses "refuses a date that is not on the calendar" "$root/r.md" "2026-02-30 is not a calendar date"
sub "$root/r.md" '2026-11-02.' '2026-10-01.'; refuses "refuses an expiry date already past" "$root/r.md" "2026-10-01 is before today (2026-10-19)"
sub "$root/r.md" '2026-11-02. The reporting' '2026-11-02 and then some. The reporting'; refuses "refuses a bad clause after the date" "$root/r.md" "after the date, write"
sub "$root/r.md" ' The reporting seat re-checks it then.' ''; refuses "refuses an expiry that names nobody who re-checks it" "$root/r.md" "names nobody who re-checks"
sub "$root/r.md" '2026-11-02. The reporting' '2026-11-02. The change takes effect then. The reporting'; refuses "refuses 'takes effect' in the expiry" "$root/r.md" "'takes effect' reads as an action on silence"
sub "$root/r.md" 'Complete data matters' 'Absent a ruling, complete data matters'; refuses "refuses 'absent a ruling' in the recommendation" "$root/r.md" "action on silence"
# a time argument does not exist: the parser takes none
clean_state
run "$BASE" --now 2030-01-01T00:00:00Z >/dev/null 2>&1 && bad "a time argument is refused" "accepted" || { [[ "$(nbatch)" == 0 && "$(nlog)" == 0 ]] && ok "a time argument is refused and nothing is written" || bad "a time argument is refused" "wrote something"; }

# --- all or nothing, every problem in one pass -------------------------------
python3 - "$two" "$root/r.md" <<'P'
import sys
t = open(sys.argv[1]).read()
head, second = t.split("## Card 2.")
second = second.replace("**Terms.** None.\n\n", "")
open(sys.argv[2], "w").write(head + "## Card 2." + second)
P
refuses "card 1 valid and card 2 without Terms writes nothing and names card 2" "$root/r.md" "card 2: no terms part"
python3 - "$BASE" "$root/r.md" <<'P'
import sys
t = open(sys.argv[1]).read().replace("Source: bus 01EXAMPLE, the request for this card\n\n", "").replace("**Terms.** None.\n\n", "")
open(sys.argv[2], "w").write(t)
P
clean_state; run "$root/r.md" >/dev/null 2>"$root/err" || true
if grep -q "no terms part" "$root/err" && grep -q "no source part" "$root/err"; then ok "a card missing source and Terms prints both problems"; else bad "a card missing source and Terms prints both" "$(cat "$root/err")"; fi

# --- slugs --------------------------------------------------------------------
clean_state; "$CI" parse "$BASE" --area a --owner o --prefix 'Bad_Prefix' >/dev/null 2>"$root/err" && bad "a bad prefix is refused" "accepted" || { grep -q "prefix" "$root/err" && ok "a bad prefix is refused" || bad "a bad prefix is refused" "$(cat "$root/err")"; }
clean_state; "$CI" parse "$BASE" --area a --owner o --prefix 'ab' >/dev/null 2>"$root/err" && bad "a prefix under 3 characters is refused" "accepted" || ok "a prefix under 3 characters is refused"
python3 - "$BASE" "$root/r.md" <<'P'
import sys
t = open(sys.argv[1]).read()
open(sys.argv[2], "w").write(t + "\n" + t.split("\n", 2)[2])
P
refuses "refuses two cards numbered alike" "$root/r.md" "two cards number 1"
python3 - "$BASE" "$root/r.md" <<'P'
import sys
t = open(sys.argv[1]).read()
b = t.split("\n", 2)[2]
open(sys.argv[2], "w").write(t + "\n" + b.replace("## Card 1.", "## Card 2."))
P
sub "$root/mixed.md" '## Card 1.' '# Card:'
python3 - "$root/mixed.md" "$root/r.md" <<'P'
import sys
t = open(sys.argv[1]).read()
b = t.split("\n", 2)[2].replace("# Card:", "## Card 2.")
open(sys.argv[2], "w").write(t + "\n" + b)
P
refuses "refuses a file with both Card: and Card N. headings" "$root/r.md" "both"
python3 - "$root/mixed.md" "$root/r.md" <<'P'
import sys
t = open(sys.argv[1]).read()
open(sys.argv[2], "w").write(t + "\n" + t.split("\n", 2)[2])
P
refuses "refuses two Card: headings" "$root/r.md" "two"
clean_state; run "$root/mixed.md" >/dev/null 2>&1 && python3 - "$(batch_json)" <<'P' && ok "a single Card: card gets the bare prefix as its slug" || bad "a single Card: card gets the bare prefix as its slug"
import json, sys
assert [w["doc_id"] for w in json.load(open(sys.argv[1]))["writes"]] == ["rpt"]
P
# the snapshot knows rpt-3 (relayed): a card numbered 3 collides
sub "$root/s.md" '## Card 1.' '## Card 3.'
refuses "refuses a slug the snapshot shows as relayed" "$root/s.md" "slug rpt-3 is on the Desk (status relayed, snapshot 2026-10-19T10:00:00Z); pass --update to revise an open card"
sub "$root/s.md" '## Card 1.' '## Card 6.'
refuses "refuses a slug the snapshot shows as closed" "$root/s.md" "slug rpt-6 is on the Desk"
sub "$root/s.md" '## Card 1.' '## Card 4.'
refuses "refuses a slug the snapshot shows as open when --update is not given" "$root/s.md" "slug rpt-4 is on the Desk (status open"
# a slug in the log, newer than the snapshot
clean_state; run "$BASE" >/dev/null 2>&1
DWS_NOW="2026-10-19T12:30:00Z" bash -c '"$0" parse "$1" --area a --owner o --prefix rpt' "$CI" "$BASE" >/dev/null 2>"$root/err" && bad "a slug in the log and not in a snapshot is refused" "accepted" || {
  grep -q "slug rpt-1 was emitted at 2026-10-19T12:00:00Z; read the Desk and run desk-snapshot write first" "$root/err" && ok "refuses a slug in the log that no snapshot has seen" || bad "refuses a slug in the log that no snapshot has seen" "$(cat "$root/err")"; }
nl="$(nlog)"; nb="$(nbatch)"
[[ "$nl" == 1 && "$nb" == 1 ]] && ok "the refused re-parse added no log line and no batch" || bad "the refused re-parse added nothing" "log $nl batch $nb"
# no snapshot, and an old one
mv "$DIRECTOR_STATE/desk-snapshot.json" "$root/snap.keep"
refuses "refuses with no snapshot, as UNREAD" "$BASE" "UNREAD"
cp "$root/snap.keep" "$DIRECTOR_STATE/desk-snapshot.json"
DWS_NOW="2026-10-18T11:00:00Z" "$SNAP" write --cards "$root/cards.json" --reported 5 --answers "$root/answers.json" --answers-reported 1 >/dev/null
refuses "refuses a snapshot read 25 hours ago, as UNREAD" "$BASE" "no Desk snapshot read in the last 24 hours; UNREAD"
write_snapshot
python3 - "$DIRECTOR_STATE/desk-snapshot.json" <<'P'
import json, sys
with open(sys.argv[1]) as f:
    s = json.load(f)
s.pop("ids")
with open(sys.argv[1], "w") as f:
    json.dump(s, f)
P
refuses "refuses a snapshot with no ids (written before this tool), asking for a fresh one" "$BASE" "desk-snapshot write"
write_snapshot

# --- --update -----------------------------------------------------------------
sub "$root/u.md" '## Card 1.' '## Card 4.'
clean_state
if run "$root/u.md" --update >"$root/out" 2>"$root/err"; then
  python3 - "$(batch_json)" <<'P' && ok "--update of an open, unanswered card emits one content-only update" || bad "--update emits one content-only update"
import json, sys
w = json.load(open(sys.argv[1]))["writes"]
assert len(w) == 1 and w[0]["op"] == "update" and w[0]["doc_id"] == "rpt-4" and w[0]["collection"] == "decisions", w
assert sorted(w[0]["data"]) == sorted(["title", "question", "context", "options", "rec", "rec_key", "expiry", "source", "kind"]), sorted(w[0]["data"])
P
else bad "--update of an open, unanswered card" "$(cat "$root/err")"; fi
sub "$root/u.md" '## Card 1.' '## Card 5.'
refuses "refuses --update of a card with an answer on the Desk" "$root/u.md" "rpt-5 has an answer on the Desk; a card the operator has started answering is not rewritten under them. Ingest it under a new prefix" --update
sub "$root/u.md" '## Card 1.' '## Card 3.'
refuses "refuses --update of a relayed card" "$root/u.md" "never rewritten" --update
refuses "refuses --update of a card that is not on the Desk" "$BASE" "not on the Desk" --update
# no answers read: an update cannot be shown safe
DWS_NOW="2026-10-19T10:00:00Z" "$SNAP" write --cards "$root/cards.json" --reported 5 >/dev/null
sub "$root/u.md" '## Card 1.' '## Card 4.'
refuses "refuses --update when the snapshot holds no answers read" "$root/u.md" "answers" --update
write_snapshot

# --- check: read-back ---------------------------------------------------------
clean_state; run "$two" >/dev/null 2>&1
B="$(batch_json)"
python3 - "$B" "$root/read-ok.json" "$root/read-bad.json" "$root/read-missing.json" <<'P'
import json, sys
w = json.load(open(sys.argv[1]))["writes"]
good = [{"id": x["doc_id"], "data": dict(x["data"])} for x in w]
json.dump(good, open(sys.argv[2], "w"))
bad = json.loads(json.dumps(good)); bad[1]["data"]["options"][0]["text"] += " changed"
json.dump(bad, open(sys.argv[3], "w"))
json.dump(good[:1], open(sys.argv[4], "w"))
P
n0="$(nlog)"
"$CI" check --batch "$B" --read "$root/read-ok.json" >"$root/out" 2>&1 \
  && grep -q "PASS rpt-1" "$root/out" && grep -q "PASS rpt-2" "$root/out" && ok "check prints PASS per slug and exits 0 on a matching read" || bad "check on a matching read" "$(cat "$root/out")"
[[ "$(nlog)" == $((n0 + 1)) ]] && tail -1 "$LOG" | grep -q '"checked"' && ok "check appends one checked line to the log" || bad "check appends one checked line to the log" "$(tail -2 "$LOG")"
"$CI" check --batch "$B" --read "$root/read-bad.json" >"$root/out" 2>&1 && bad "check accepts a changed option" "accepted" \
  || { grep -q "MISMATCH rpt-2 options" "$root/out" && ok "check prints MISMATCH <slug> <field> and exits nonzero on a changed option" || bad "check mismatch output" "$(cat "$root/out")"; }
"$CI" check --batch "$B" --read "$root/read-missing.json" >"$root/out" 2>&1 && bad "check accepts a missing slug" "accepted" \
  || { grep -q "rpt-2" "$root/out" && ok "check exits nonzero on a slug the read does not hold" || bad "check missing slug output" "$(cat "$root/out")"; }
# an update is compared on its content fields only
write_snapshot; clean_state
sub "$root/u.md" '## Card 1.' '## Card 4.'
run "$root/u.md" --update >/dev/null 2>&1
python3 - "$(batch_json)" "$root/read-upd.json" <<'P'
import json, sys
w = json.load(open(sys.argv[1]))["writes"][0]
d = dict(w["data"]); d.update({"status": "open", "opened": "2026-01-01T00:00:00Z", "ruling": ""})
json.dump({w["doc_id"]: {"data": d}}, open(sys.argv[2], "w"))
P
"$CI" check --batch "$(batch_json)" --read "$root/read-upd.json" >"$root/out" 2>&1 \
  && ok "check compares an update on its content fields only, so the Desk's own opened and status stay" || bad "check of an update" "$(cat "$root/out")"

# --- the cases above must be able to fail: run them against mutants of the tool -------------------
# A case that passes at head and cannot fail under a mutant is a pin, not a test. Each mutant is a
# scratch copy of card-ingest with one line changed. A mutant counts as caught only when the case
# fails with the exact exit and the exact message that mutant should cause: a mutant that merely
# breaks the tool (a syntax error exits 1 too) is not a catch, and a meta case below shows it.
mutant() {  # NAME OLD NEW ; leaves the path in $MUT
  MUT="$root/mutant-$1"
  python3 - "$here/card-ingest" "$MUT" "$2" "$3" <<'P'
import sys
t = open(sys.argv[1]).read()
assert sys.argv[3] in t, "the tool no longer holds the text this mutant changes: %r" % sys.argv[3]
open(sys.argv[2], "w").write(t.replace(sys.argv[3], sys.argv[4], 1))
P
  chmod +x "$MUT"
}
# The fence fixtures the mutant cases run on.
python3 - "$BASE" "$root/fence-ok.md" "$root/fence-open.md" <<'P'
import sys
t = open(sys.argv[1]).read()
a = "1. *Keep 06:00.* Nothing changes. The cost is a report short of late data."
assert a in t
open(sys.argv[2], "w").write(t.replace(a, "1. *Keep 06:00.* Nothing changes.\n   ~~~\n   write [RECOMMENDED]\n   ~~~\n   The cost is a late report.", 1))
open(sys.argv[3], "w").write(t.replace(a, "1. *Keep 06:00.* The cost is a late report.\n   ```\n   some code", 1))
P
# case_fence_ok: 0 when a tilde-fenced token is text; 1 otherwise, with the tool's stderr in why.
case_fence_ok() { clean_state; run "$root/fence-ok.md" >/dev/null 2>"$root/why"; }
# case_fence_open: 0 when an unclosed fence is refused for being unclosed; 1 otherwise, why says what happened.
case_fence_open() {
  clean_state; local rc=0; run "$root/fence-open.md" >/dev/null 2>"$root/why" || rc=$?
  [[ $rc -eq 1 ]] && grep -q 'option 1 ends inside an open code fence' "$root/why" && return 0
  echo "exit $rc: $(cat "$root/why")" >"$root/why.tmp"; mv "$root/why.tmp" "$root/why"; return 1
}
# caught NAME CASE WANT: run CASE against the mutant; it must fail (exit 1) and its why must hold WANT.
caught() {
  local name="$1" fn="$2" want="$3" rc=0
  local real="$CI"; CI="$MUT"; "$fn" >/dev/null 2>&1 || rc=$?; CI="$real"
  [[ $rc -eq 1 ]] && grep -qF -- "$want" "$root/why"
}
if [[ "$CI" == "$here/card-ingest" ]]; then
  mutant bareword 'mk = re.search(r"\s*\[RECOMMENDED\]\.?\s*$", line)' 'mk = re.search(r"\s*RECOMMENDED", line)'
  caught M1 case_prose_mid "RECOMMENDED marks option 1 and the recommendation names option 2" \
    && ok "mutant M1 (the bare word anywhere is the marker): the prose case fails with the marker disagreement" || bad "mutant M1 is not caught by the prose case with the marker disagreement" "$(cat "$root/why")"
  mutant notruncate 'lambda: os.truncate(LOG, size)' 'lambda: None'
  caught M2 case_rlimit "now 3030 bytes, was 2970" 2>/dev/null \
    && ok "mutant M2 (no truncate after a partial append): the RLIMIT case fails with the grown log" || bad "mutant M2 is not caught by the RLIMIT case with the grown log" "$(cat "$root/why" 2>/dev/null)"
  mutant keepbatch 'lambda: os.unlink(path)' 'lambda: None'
  caught M3 case_rlimit "card-ingest/rpt-" \
    && ok "mutant M3 (the batch kept after a failed append): the RLIMIT case fails naming the batch left behind" || bad "mutant M3 is not caught by the RLIMIT case naming the batch" "$(cat "$root/why" 2>/dev/null)"
  mutant nofence 'fence = fm.group(1)' 'fence = None'
  caught M4 case_fence_ok "RECOMMENDED marks option 1 and the recommendation names option 2" \
    && ok "mutant M4 (a fence is not entered): the fenced-token case fails with the marker disagreement" || bad "mutant M4 is not caught by the fenced-token case" "$(cat "$root/why" 2>/dev/null)"
  mutant noclose 'if fence is not None:
            unclosed.append(n)' 'if False:
            unclosed.append(n)'
  caught M5 case_fence_open "exit 0" \
    && ok "mutant M5 (an unclosed fence is not refused): the unclosed-fence case fails with exit 0" || bad "mutant M5 is not caught by the unclosed-fence case" "$(cat "$root/why" 2>/dev/null)"
  # M0, a meta case: a tool that does not even parse exits 1 as a refusal would, and must NOT count
  # as a catch for any mutant above, because its message is a traceback and not the expected one.
  mutant broken 'fence = fm.group(1)' 'fence = ((:'
  ! caught M0 case_fence_ok "RECOMMENDED marks option 1 and the recommendation names option 2" \
    && ok "mutant M0 (a syntax error) is not accepted as a catch: the exact message is required" || bad "mutant M0, a syntax error, was accepted as a catch"
  ! caught M0 case_prose_mid "RECOMMENDED marks option 1 and the recommendation names option 2" \
    && ok "mutant M0 is not accepted as a catch of the prose case either" || bad "mutant M0 was accepted as a catch of the prose case"
else
  echo "SKIP the mutant cases: CI_BIN names another tool ($CI), and mutants are built from $here/card-ingest"
fi

# --- backfill: tag the recommended option on open cards that predate rec_key -------------------
bf="$root/bf"; rm -rf "$bf"; mkdir -p "$bf"
python3 - "$bf" <<'P'
import json, sys
d = sys.argv[1]
def card(slug, status, rec, **kw):
    doc = {"status": status, "rec": rec, "options": [{"key": "1", "text": "a"}, {"key": "2", "text": "b"}]}
    doc.update(kw)
    json.dump(doc, open("%s/%s.json" % (d, slug), "w"))
card("ok-prose", "open", "Option 2. It is cheaper.")
card("ok-marker", "open", "option 1: keep it.", )
card("no-option", "open", "Do the second thing.")
card("two-options", "open", "Option 1 or Option 2. Either works.")
card("out-of-range", "open", "Option 3. Larger.")
card("empty-rec", "open", "")
card("later-sentence", "open", "It depends. Option 2 wins.")
card("bare-key", "open", "2")
card("done", "closed", "Option 1. Old.")
card("tagged", "open", "Option 1. Old.", rec_key="1")
P
clean_state
if "$CI" backfill --docs "$bf" >"$root/out" 2>"$root/err" && python3 - "$(batch_json)" "$root/out" <<'P'
import json, sys
b = json.load(open(sys.argv[1])); out = open(sys.argv[2]).read()
w = {x["doc_id"]: x for x in b["writes"]}
assert sorted(w) == ["ok-marker", "ok-prose"], sorted(w)
assert all(x["op"] == "update" and x["collection"] == "decisions" for x in w.values()), w
assert sorted(w["ok-prose"]["data"]) == ["options", "rec_key"], w["ok-prose"]
assert w["ok-prose"]["data"]["rec_key"] == "2", w["ok-prose"]
assert w["ok-prose"]["data"]["options"] == [{"key": "1", "text": "a"}, {"key": "2", "text": "b", "recommended": True}], w["ok-prose"]
assert w["ok-marker"]["data"]["options"][0] == {"key": "1", "text": "a", "recommended": True}, w["ok-marker"]
for slug in ("no-option", "two-options", "out-of-range", "empty-rec", "later-sentence"):
    assert "UNPARSED " + slug in out, (slug, out)
for slug in ("bare-key", "done", "tagged", "ok-prose"):
    assert "UNPARSED " + slug not in out, (slug, out)
assert "2 to write, 5 unparsed, 3 skipped" in out, out
P
then ok "backfill writes updates for exact parses only, reports the five that do not parse, skips closed, tagged and bare-key cards"; else bad "backfill" "$(cat "$root/err") $(cat "$root/out")"; fi
# a read-back of the backfill passes when the Desk holds the batch, and names a doc that lacks it
python3 - "$(batch_json)" "$root/readback.json" <<'P'
import json, sys
b = json.load(open(sys.argv[1]))
json.dump({x["doc_id"]: dict(x["data"], title="t", status="open") for x in b["writes"]}, open(sys.argv[2], "w"))
P
if "$CI" check --batch "$(batch_json)" --read "$root/readback.json" >"$root/out" 2>&1 && [[ "$(grep -c '^PASS ' "$root/out")" == 2 ]]; then ok "check passes a backfill batch against what the Desk holds"; else bad "check of a backfill batch" "$(cat "$root/out")"; fi
python3 - "$root/readback.json" <<'P'
import json, sys
d = json.load(open(sys.argv[1])); d["ok-prose"]["rec_key"] = "1"; json.dump(d, open(sys.argv[1], "w"))
P
rc=0; "$CI" check --batch "$(batch_json)" --read "$root/readback.json" >"$root/out" 2>&1 || rc=$?
if [[ $rc -ne 0 ]] && grep -q "MISMATCH ok-prose rec_key" "$root/out"; then ok "check names a backfilled field the Desk does not hold"; else bad "check misses a rec_key mismatch" "rc=$rc $(cat "$root/out")"; fi
clean_state

echo "card-ingest: $pass passed, $fail failed"
[[ "$fail" == 0 ]]
