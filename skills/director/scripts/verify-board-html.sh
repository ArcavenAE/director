#!/usr/bin/env bash
# Prove board-html keeps its W3 contract (sim/design/board-workstream-ledger.md
# section 4 and section 11 item 4): from the workstream ledger it renders the
# header, Blocked on you, Uncaptured and the ledger table, in that order,
# above board.md as collapsed history; a row unmoved past its stage's
# threshold carries "unmoved 30h" as text; every value is escaped. Guard
# (passes on main): with no ledger file the page is today's page.
#
# Usage: skills/director/scripts/verify-board-html.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BOARD_HTML="${BOARD_HTML:-$here/board-html}"
DWS="${DWS:-$here/dws}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
export DIRECTOR_STATE="$root/state"
mkdir -p "$DIRECTOR_STATE"
export DWS_TEST_CLOCK=1

cat >"$DIRECTOR_STATE/board.md" <<'MD'
# director board

Authored, edited not overwritten.

## 2030-12-30 ~10:00Z: a section
- Dispatched the thing.

## Uncaptured

### PROPOSE from sup-seat (ws aae) to agent://ops/michael
- stored once, never answered

## 2030-12-31 ~09:00Z: a later section
- Merged the other thing.
MD

# The guard: the page with no ledger file. GOLDEN is the sha256 of main's page
# for this board.md, with the generated stamp and the state path normalized.
# A deliberate change to the no-ledger page updates it.
GOLDEN=af6750411461c4fd374fd6e4ce8234f680941eb983e07148f5d4e0f8bbeb971a
norm() {
  python3 - "$1" "$DIRECTOR_STATE" <<'PY'
import hashlib, re, sys
t = open(sys.argv[1], encoding="utf-8").read().replace(sys.argv[2], "STATE")
t = re.sub(r"Generated [^<]*? from ", "Generated STAMP from ", t)
print(hashlib.sha256(t.encode()).hexdigest())
PY
}
"$BOARD_HTML" >/dev/null 2>&1 || bad "board-html runs with no ledger"
got="$(norm "$DIRECTOR_STATE/board.html")"
[[ "$got" == "$GOLDEN" ]] && ok "with no ledger file the page is today's page" || bad "with no ledger file the page is today's page" "$got"

# The ledger. Now is 2030-12-31T12:00:00Z; rows are opened at the times that
# make them 30h, 20h, 2h and 100h old.
open_at() { local at="$1"; shift; DWS_NOW="$at" "$DWS" open "$@" >/dev/null; }
open_at 2030-12-30T06:00:00Z b1 --title "building one" --stage building --owner supervisor --next "ship the build"
open_at 2030-12-30T16:00:00Z bo --title "blocked old" --stage designed --owner architect --blocked operator --next "rule on X"
open_at 2030-12-31T10:00:00Z bn --title "blocked new" --stage defined --owner architect --blocked operator --next "ship <b>it</b>"
open_at 2030-12-27T08:00:00Z pk --title "parked" --stage parked --owner supervisor --next "wait"
open_at 2030-12-31T11:00:00Z ak --title "asks" --stage idea --owner supervisor --next "answer" --link ask:ask-9 ask:b1 bd:aae-orc-zz9
TZ=UTC touch -t 203012311154 "$DIRECTOR_STATE/workstreams.jsonl"   # ledger 6m old at now

page() {
  DWS_NOW=2030-12-31T12:00:00Z "$BOARD_HTML" >/dev/null 2>"$root/err" || { bad "board-html runs with a ledger" "$(cat "$root/err")"; return 1; }
  cat "$DIRECTOR_STATE/board.html"
}
html="$(page || true)"
printf '%s' "$html" >"$root/page.html"

has() { grep -qF -- "$2" "$root/page.html" && ok "$1" || bad "$1" "missing: $2"; }
text_of() { python3 - "$root/page.html" "$1" <<'PY'
import html, re, sys
t = open(sys.argv[1], encoding="utf-8").read()
m = re.search(r'<(\w+)[^>]*\bid="%s"[^>]*>(.*?)</\1>' % re.escape(sys.argv[2]), t, re.S)
print(html.unescape(re.sub(r"<[^>]+>", "\n", m.group(2))).strip() if m else "")
PY
}

# Header (section 4 item 1).
want="Now: 5 streams, 2 blocked on you, 1 unmoved past threshold | ledger 6m | page 0m"
got="$(text_of ledger-header | tr '\n' ' ' | sed -E 's/ +/ /g; s/ $//')"
[[ "$got" == "$want" ]] && ok "the header counts streams, blocked on you, unmoved, ledger and page age" || bad "the header counts streams, blocked on you, unmoved, ledger and page age" "$got"

# Order (section 11 item 4): Blocked on you, Uncaptured, ledger, then history.
python3 - "$root/page.html" <<'PY' && ok "Blocked on you, then Uncaptured, then the ledger, then history" || bad "Blocked on you, then Uncaptured, then the ledger, then history"
import re, sys
t = open(sys.argv[1], encoding="utf-8").read()
ids = ["ledger-header", "blocked-on-you", "uncaptured", "ledger-table", "history"]
pos = [t.find('id="%s"' % i) for i in ids]
sys.exit(0 if all(p >= 0 for p in pos) and pos == sorted(pos) else 1)
PY

# Blocked on you (item 2): operator rows, oldest first, in the sweep's form.
got="$(text_of blocked-on-you | { grep -E ' - ' || true; } | tr '\n' '|')"
want="architect - bo: rule on X|architect - bn: ship <b>it</b>|"
[[ "$got" == "$want" ]] && ok "Blocked on you lists operator rows oldest first as <owner> - <workstream>: <next>" || bad "Blocked on you lists operator rows oldest first as <owner> - <workstream>: <next>" "$got"
if grep -qF 'ship <b>it</b>' "$root/page.html"; then bad "values are escaped" "raw <b> in the page"
elif grep -qF 'ship &lt;b&gt;it&lt;/b&gt;' "$root/page.html"; then ok "values are escaped"; else bad "values are escaped" "the value is not in the page"; fi

# Uncaptured (item 3): board.md's lines, plus an ask id with no row of its own.
u="$(text_of uncaptured)"
grep -qF "PROPOSE from sup-seat (ws aae) to agent://ops/michael" <<<"$u" && ok "Uncaptured carries the board's Uncaptured lines" || bad "Uncaptured carries the board's Uncaptured lines" "$u"
grep -qF "ask:ask-9" <<<"$u" && ok "an ask id with no row of its own is Uncaptured" || bad "an ask id with no row of its own is Uncaptured" "$u"
[[ -n "$u" ]] && ! grep -qF "ask:b1" <<<"$u" && ! grep -qF "aae-orc-zz9" <<<"$u" && ok "an ask id that has a row, and a link that is not an ask, are not Uncaptured" || bad "an ask id that has a row, and a link that is not an ask, are not Uncaptured" "$u"

# The ledger (item 4): stage order, oldest first within a stage, parked last;
# the 30h building row says so as text, the 20h designed row does not.
slugs="$(python3 - "$root/page.html" <<'PY'
import re, sys
t = open(sys.argv[1], encoding="utf-8").read()
m = re.search(r'id="ledger-table".*?</table>', t, re.S)
print(",".join(re.findall(r'data-slug="([^"]+)"', m.group(0))) if m else "")
PY
)"
[[ "$slugs" == "ak,bn,bo,b1,pk" ]] && ok "the ledger sorts by stage, parked last" || bad "the ledger sorts by stage, parked last" "$slugs"
row() { python3 - "$root/page.html" "$1" <<'PY'
import html, re, sys
t = open(sys.argv[1], encoding="utf-8").read()
m = re.search(r'<tr[^>]*data-slug="%s".*?</tr>' % re.escape(sys.argv[2]), t, re.S)
print(html.unescape(re.sub(r"<[^>]+>", " ", m.group(0))) if m else "")
PY
}
grep -qF "unmoved 30h" <<<"$(row b1)" && ok "a building row unmoved 30h carries 'unmoved 30h' as text" || bad "a building row unmoved 30h carries 'unmoved 30h' as text" "$(row b1)"
[[ -n "$(row bo)" ]] && ! grep -q "unmoved" <<<"$(row bo)" && ok "a designed row unmoved 20h is not flagged" || bad "a designed row unmoved 20h is not flagged" "$(row bo)"
[[ -n "$(row pk)" ]] && ! grep -q "unmoved" <<<"$(row pk)" && ok "a parked row never flags" || bad "a parked row never flags" "$(row pk)"

# History (item 5): board.md below, collapsed.
grep -qE '<details id="history"( [^>]*)?>' "$root/page.html" && ! grep -qE '<details id="history"[^>]* open' "$root/page.html" \
  && ok "history is a collapsed section" || bad "history is a collapsed section"
has "board.md is still in the page" "a later section"

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
