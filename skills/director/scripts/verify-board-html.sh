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

### PROPOSE from sup-seat (ws aae) to agent://example/builder-g9-9
- stored once, never answered

## 2030-12-31 ~09:00Z: a later section
- Merged the other thing.
MD

# The guard: the page with no ledger file. GOLDEN is the sha256 of main's page
# for this board.md, with the generated stamp and the state path normalized.
# A deliberate change to the no-ledger page updates it.
GOLDEN=5d6150e5577e5c34ba18f691e3767658926b9c98bb8d6c045d3caa9329bfd60f
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
grep -qF "PROPOSE from sup-seat (ws aae) to agent://example/builder-g9-9" <<<"$u" && ok "Uncaptured carries the board's Uncaptured lines" || bad "Uncaptured carries the board's Uncaptured lines" "$u"
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

# ---- the page age counts from the render, not from page load ----------------
# Run the inline script under node with a faked clock: a page rendered at
# 12:00 and opened 2 days and 5 minutes later must read 2885m at once.
if node -e 0 >/dev/null 2>&1; then
  age() { # elapsed ms since the render
    python3 - "$root/page.html" <<'PY' >"$root/age.js"
import re, sys
t = open(sys.argv[1], encoding="utf-8").read()
at = re.search(r'id="page-age" data-at="(\d+)"', t).group(1)
src = re.search(r"<script>(\(function\(\)\{var el=document.getElementById\('page-age'\).*?)</script>", t, re.S).group(1)
print("var AT=%s;var el={dataset:{at:String(AT)},textContent:'x'};var document={getElementById:function(){return el;}};" % at)
print("var ELAPSED=Number(process.argv[2]);Date.now=function(){return AT*1000+ELAPSED;};setInterval=function(){};")
print(src)
print("console.log(el.textContent);")
PY
    node "$root/age.js" "$1"
  }
  [[ "$(age $((2*24*3600*1000 + 5*60*1000)))" == "2885m" ]] && ok "a page opened 2 days after its render reads its true age at once" || bad "a page opened 2 days after its render reads its true age at once" "$(age $((2*24*3600*1000 + 5*60*1000)))"
  [[ "$(age 0)" == "0m" ]] && ok "a page opened at its render reads 0m" || bad "a page opened at its render reads 0m" "$(age 0)"
else
  bad "the page age script runs under node" "no working node on PATH; this check cannot run"
fi

# ---- every rendered field is escaped ------------------------------------------
# Another state: one hostile payload per field, in a row and in a board.md
# heading. None may appear raw; each must appear escaped.
main_state="$DIRECTOR_STATE"
fresh() { export DIRECTOR_STATE="$root/$1"; mkdir -p "$DIRECTOR_STATE"; }
fresh hostile
cat >"$DIRECTOR_STATE/board.md" <<'MD'
# director board

## Uncaptured

### <i/f=heading> from a seat
MD
open_at 2030-12-31T10:00:00Z h1 --title "<i/f=title>" --stage building --owner "<i/f=owner>" --blocked operator --next "<i/f=next>" --link "ask:<i/f=ask>"
open_at 2030-12-31T10:00:00Z h2 --title "other" --stage building --owner supervisor --blocked "<i/f=blocked>" --next "n"
DWS_NOW=2030-12-31T12:00:00Z "$BOARD_HTML" >/dev/null 2>"$root/err" || bad "board-html renders hostile text" "$(cat "$root/err")"
cp "$DIRECTOR_STATE/board.html" "$root/page.html"
# The board.md source is embedded as JSON in a script, where "<" is escaped
# as <, so raw means the markup form only.
if grep -qF '<i/f=' "$root/page.html"; then bad "no field renders raw markup" "$(grep -oE '<i/f=[a-z]*>' "$root/page.html" | sort -u | tr '\n' ' ')"; else ok "no field renders raw markup"; fi
for f in owner next blocked ask heading; do
  grep -qF "&lt;i/f=$f&gt;" "$root/page.html" && ok "the $f field is rendered escaped" || bad "the $f field is rendered escaped"
done

# ---- template placeholders in the data are not substituted ---------------------
fresh placeholders
printf '# director board\n\n## __LEDGER_HEAD__ __GENERATED__ __SRC__ in a heading\n- text\n' >"$DIRECTOR_STATE/board.md"
open_at 2030-12-31T10:00:00Z p1 --title "placeholder" --stage building --owner supervisor --blocked operator --next "__BOARD_JSON__ __GENERATED__ __LEDGER_HEAD__"
DWS_NOW=2030-12-31T12:00:00Z "$BOARD_HTML" >/dev/null 2>"$root/err" || bad "board-html renders placeholder text" "$(cat "$root/err")"
cp "$DIRECTOR_STATE/board.html" "$root/page.html"
python3 - "$root/page.html" <<'PY' && ok "placeholder text in a row or in board.md is not substituted" || bad "placeholder text in a row or in board.md is not substituted"
import sys
t = open(sys.argv[1], encoding="utf-8").read()
want = {
  'id="ledger-header"': 1,              # board.md's __LEDGER_HEAD__ did not place a second ledger
  "__BOARD_JSON__ __GENERATED__ __LEDGER_HEAD__": 2,   # the row text, in Blocked on you and the table
  "const BOARD = ": 1,                  # the row did not splice a second copy of the board
}
for k, n in want.items():
    if t.count(k) != n:
        print("%r x%d, want %d" % (k, t.count(k), n)); sys.exit(1)
PY

# ---- the unmoved boundaries ---------------------------------------------------
# Strictly past the threshold flags; hours below 48, whole days from 48h.
fresh edges
printf '# director board\n' >"$DIRECTOR_STATE/board.md"
open_at 2030-12-30T12:00:00Z e24 --title t --stage building --owner s --next n          # exactly 24h
open_at 2030-12-30T10:00:00Z e26 --title t --stage "in review" --owner s --next n       # 26h
open_at 2030-12-29T12:00:00Z e48 --title t --stage building --owner s --next n          # exactly 48h
open_at 2030-12-29T12:00:00Z d48 --title t --stage designed --owner s --next n          # exactly 48h
open_at 2030-12-29T11:00:00Z d49 --title t --stage designed --owner s --next n          # 49h
open_at 2030-12-28T12:00:00Z f72 --title t --stage defined --owner s --next n           # exactly 72h
open_at 2030-12-28T11:00:00Z f73 --title t --stage defined --owner s --next n           # 73h
open_at 2030-12-01T12:00:00Z i30 --title t --stage idea --owner s --next n              # 30 days
DWS_NOW=2030-12-31T12:00:00Z "$BOARD_HTML" >/dev/null 2>"$root/err" || bad "board-html renders the boundary rows" "$(cat "$root/err")"
cp "$DIRECTOR_STATE/board.html" "$root/page.html"
flag() { local r; r="$(row "$1")"; [[ -n "$r" ]] || { echo MISSING; return; }; grep -oE 'unmoved [0-9]+[hd]' <<<"$r" || echo none; }
for c in "e24:none" "e26:unmoved 26h" "e48:unmoved 2d" "d48:none" "d49:unmoved 2d" "f72:none" "f73:unmoved 3d" "i30:none"; do
  slug="${c%%:*}"; want="${c#*:}"; got="$(flag "$slug")"
  [[ "$got" == "$want" ]] && ok "unmoved at the boundary: $slug is $want" || bad "unmoved at the boundary: $slug is $want" "$got"
done
export DIRECTOR_STATE="$main_state"

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
