#!/usr/bin/env bash
# Prove the director skill's sweep uses the workstream ledger the way the design
# says (sim/design/board-workstream-ledger.md sections 7 and 11 item 5). Step 4
# reads Blocked on you from ledger rows with blocked on = operator, in the form
# `<owner> - <workstream>: <next action>`, and its printed fence does not change.
# Step 5 gains: open a row for each new stream, move stages that changed, run
# refresh. Without a ledger or the CLI the sweep runs as before. The commands the
# skill names exist, and the line form it states is the one the page renders.
#
# Guard (passes on main): the step 4 fence is byte-identical to main's.
#
# Usage: scripts/verify-skill-ledger.sh   Exit nonzero on any miss. Touches only a temp dir.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
skill="$here/skills/director/SKILL.md"
DWS="$here/skills/director/scripts/dws"
BOARD_HTML="$here/skills/director/scripts/board-html"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

# The sweep's step 4, split into the printed fence and the prose around it.
part() { # fence | before | after
  python3 - "$skill" "$1" <<'PY'
import re, sys
t = open(sys.argv[1], encoding="utf-8").read()
i = t.index("4. Present, in this shape and no other:")
j = t.index("```", i) + 3
k = t.index("```", j)
n5 = t.index("\n5. Update `board.md`", k)
step5_end = t.index("\n## Mode: standing", n5)
which = sys.argv[2]
sys.stdout.write({"fence": t[j:k], "before": t[i:j - 3], "after": t[k + 3:n5], "step5": t[n5:step5_end]}[which])
PY
}

# Guard: the printed contract does not change.
FENCE=f8b035199d1d451f28b415de387b951494856bb991cd72df7ef17ca7ba35fff4
got="$(part fence | shasum -a 256 | cut -d' ' -f1)"
[[ "$got" == "$FENCE" ]] && ok "the step 4 fence is byte-identical to main's" || bad "the step 4 fence is byte-identical to main's" "$got"

# Markdown wraps anywhere, so match on the text with whitespace collapsed.
flat() { tr '\n' ' ' | sed -E 's/ +/ /g'; }
step4="$( { part before; part after; } | flat)"
step5="$(part step5 | flat)"
has() { grep -qF -- "$2" <<<"$1"; }

# Step 4: where Blocked on you comes from, and the form of each line.
has "$step4" 'scripts/dws show --json' && ok "step 4 reads the ledger with dws show --json" || bad "step 4 reads the ledger with dws show --json"
has "$step4" 'rows whose `blocked` is `operator`' && ok "step 4 takes Blocked on you from rows whose blocked is operator" || bad "step 4 takes Blocked on you from rows whose blocked is operator"
has "$step4" '<owner> - <workstream>: <next action>' && ok "step 4 states the line form" || bad "step 4 states the line form"
has "$step4" 'oldest `last_moved` first' && ok "step 4 orders Blocked on you by last_moved, oldest first" || bad "step 4 orders Blocked on you by last_moved, oldest first"
has "$step4" "from the row's \`owner\`, \`slug\` and \`next\`" && ok "step 4 names the row fields the line is built from" || bad "step 4 names the row fields the line is built from"
has "$step4" 'With no ledger, or no CLI, build the list from the board and the roster as above.' && ok "step 4 pins the no-ledger fallback" || bad "step 4 pins the no-ledger fallback"

# Step 5: the additions, and the render that was already there.
for c in 'dws open' 'dws stage' 'dws refresh'; do
  has "$step5" "$c" && ok "step 5 names $c" || bad "step 5 names $c"
done
has "$step5" 'bin/board-html' && ok "step 5 still re-renders the page" || bad "step 5 still re-renders the page"

# The commands the skill names exist.
help="$("$DWS" --help 2>&1 || true)"
for c in open stage refresh show; do
  grep -qE "\b$c\b" <<<"$help" && ok "dws has the $c command the skill names" || bad "dws has the $c command the skill names"
done

# The form the skill states is the form the page renders (W3, same fixture).
root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
export DIRECTOR_STATE="$root/state" DWS_TEST_CLOCK=1
mkdir -p "$DIRECTOR_STATE"
printf '# director board\n' >"$DIRECTOR_STATE/board.md"
DWS_NOW=2030-12-31T10:00:00Z "$DWS" open w1 --title "one" --stage designed --owner architect --blocked operator --next "rule on X" >/dev/null
# The row fields the skill names are the fields dws show --json emits. The list
# is read from the skill's step 4 text (its backticked lower-case words, less the
# value `operator`), so renaming a field there changes what is checked here.
fields="$(python3 - <<'PY' "$step4"
import re, sys
print(" ".join(sorted({w for w in re.findall(r"`([a-z_]+)`", sys.argv[1]) if w != "operator"})))
PY
)"
[[ "$fields" == "blocked last_moved next owner slug" ]] && ok "step 4 names the row fields blocked, last_moved, next, owner and slug" || bad "step 4 names the row fields blocked, last_moved, next, owner and slug" "read: $fields"
for f in $fields; do
  "$DWS" show --json | python3 -c 'import json,sys; sys.exit(0 if sys.argv[1] in json.load(sys.stdin)["rows"][0] else 1)' "$f" \
    && ok "dws show --json rows carry the $f field the skill names" || bad "dws show --json rows carry the $f field the skill names" 
done
DWS_NOW=2030-12-31T12:00:00Z "$BOARD_HTML" >/dev/null 2>&1 || bad "the page renders the fixture"
form="$(python3 - "$skill" <<'PY'
import re, sys
t = open(sys.argv[1], encoding="utf-8").read()
m = re.search(r"`(<owner> - <workstream>: <next action>)`", t)
print(m.group(1) if m else "")
PY
)"
want="${form//<owner>/architect}"; want="${want//<workstream>/w1}"; want="${want//<next action>/rule on X}"
if [[ -n "$form" ]] && grep -qF "<li>$want</li>" "$DIRECTOR_STATE/board.html"; then
  ok "each Blocked on you line reads $form, as the page renders it"
else
  bad "each Blocked on you line reads <owner> - <workstream>: <next action>, as the page renders it" "skill form: ${form:-none}"
fi

# The header comment states the contract in the skill's words, not the design's.
if sed -n 1,12p "$here/scripts/verify-skill-ledger.sh" | grep -q 'blocked on = operator'; then
  bad "the header names the blocked field as the skill does" "it still says 'blocked on = operator'"
else ok "the header names the blocked field as the skill does"; fi

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
