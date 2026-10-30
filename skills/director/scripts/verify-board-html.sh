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
DESK_SNAPSHOT="${DESK_SNAPSHOT:-$here/desk-snapshot}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
export DIRECTOR_STATE="$root/state"
mkdir -p "$DIRECTOR_STATE"
export DWS_TEST_CLOCK=1
# The fill reads a relay log; no test reads the real one (ledger fill, LF5).
export RELAY_LOG="$root/no-relay-log.md"
export PYTHONDONTWRITEBYTECODE=1

# K13: dws refuses --blocked operator without an open Desk card, so every state
# dir that opens such a row gets a snapshot with card c1 open, read before the
# rows below are opened and within the 24 h limit of each of them.
desk_open() {
  printf '{"c1": {"status": "open"}}' >"$root/cards.json"
  DWS_NOW=2030-12-30T12:00:00Z "$DESK_SNAPSHOT" write --cards "$root/cards.json" --reported 1 >/dev/null
}
desk_open

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
GOLDEN=9cb082fac030d2cb136f33d1c3a470a9820b9f89b056b5069a543d74b7ed6a90
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
open_at 2030-12-30T16:00:00Z bo --title "blocked old" --stage designed --owner architect --blocked operator --ask c1 --next "rule on X"
open_at 2030-12-31T10:00:00Z bn --title "blocked new" --stage defined --owner architect --blocked operator --ask c1 --next "ship <b>it</b>"
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
fresh() { export DIRECTOR_STATE="$root/$1"; mkdir -p "$DIRECTOR_STATE"; desk_open; }
fresh hostile
cat >"$DIRECTOR_STATE/board.md" <<'MD'
# director board

## Uncaptured

### <i/f=heading> from a seat
MD
open_at 2030-12-31T10:00:00Z h1 --title "<i/f=title>" --stage building --owner "<i/f=owner>" --blocked operator --ask c1 --next "<i/f=next>" --link "ask:<i/f=ask>"
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
open_at 2030-12-31T10:00:00Z p1 --title "placeholder" --stage building --owner supervisor --blocked operator --ask c1 --next "__BOARD_JSON__ __GENERATED__ __LEDGER_HEAD__"
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

# ---- the fill runs on every render (ledger fill, LF5) -----------------------------------
# board-html runs `dws refresh --local` before it reads the rows, with DWS_NO_RERENDER=1 so
# the render does not render itself. A relay entry written with --ws moves its row without
# anyone typing dws refresh.
fresh fill
export RELAY_LOG="$root/fill-relay.md"
printf '# director board\n' >"$DIRECTOR_STATE/board.md"
open_at 2026-10-01T12:00:00Z fl1 --title "fill row" --stage building --owner supervisor --next n
"$here/relay-log" append --to agent://t/x --ws fl1 --text "a send about the fill row" >/dev/null 2>&1
fstamp="$(sed -n 's/^## R-1 (\([^)]*\)).*/\1/p' "$RELAY_LOG")"
DWS_NOW=2099-01-01T00:00:00Z "$BOARD_HTML" >/dev/null 2>"$root/err" || bad "board-html renders with a relay entry waiting" "$(cat "$root/err")"
lm="$("$DWS" show --json | python3 -c 'import json,sys;print({r["slug"]: r for r in json.load(sys.stdin)["rows"]}["fl1"]["last_moved"])')"
[[ "$lm" == "$fstamp" ]] && ok "a render moves a row from a relay entry linked with --ws, with nobody typing dws refresh" || bad "a render runs the fill" "last moved $lm, entry $fstamp"
grep -q 'fl1' "$DIRECTOR_STATE/board.html" && ok "and the page was still written" || bad "the page was written"
n="$(wc -l <"$DIRECTOR_STATE/workstreams.jsonl" | tr -d ' ')"
DWS_NOW=2099-01-01T00:00:00Z "$BOARD_HTML" >/dev/null 2>&1
[[ "$(wc -l <"$DIRECTOR_STATE/workstreams.jsonl" | tr -d ' ')" == "$n" ]] && ok "a second render over the same sources appends nothing" || bad "a second render appends nothing"
# a fill that fails does not stop the page
cp "$DWS" "$root/dws-broken"; printf '#!/bin/sh\nexit 3\n' >"$root/dws-broken"; chmod +x "$root/dws-broken"
mkdir -p "$root/bh"; cp "$BOARD_HTML" "$root/bh/board-html"; cp "$DWS" "$root/bh/dws"
python3 - "$root/bh/dws" <<'PY'
import sys
t = open(sys.argv[1]).read()
open(sys.argv[1], "w").write(t.replace("def cmd_refresh_local(a):", "def cmd_refresh_local(a):\n    sys.exit(3)", 1))
PY
rm -f "$DIRECTOR_STATE/board.html"
DWS_NOW=2099-01-01T00:00:00Z "$root/bh/board-html" >/dev/null 2>"$root/err" && [[ -f "$DIRECTOR_STATE/board.html" ]] \
  && ok "a fill that fails does not stop the page being written" || bad "a failing fill stops the page" "$(cat "$root/err")"
grep -qi 'fill' "$root/err" && ok "and the failure is said on stderr" || bad "the failure is said on stderr" "$(cat "$root/err")"
export DIRECTOR_STATE="$main_state"

# ---- the Seat decisions section (D3, sim/design/seat-decisions.md section 7) -------------------
# An empty ledger of decisions says so and shows nothing else.
fresh seatnone
printf '# director board\n' >"$DIRECTOR_STATE/board.md"
open_at 2026-10-20T08:00:00Z sd0 --title "a row" --stage building --owner supervisor --next n
DWS_NOW=2026-10-22T08:00:00Z "$BOARD_HTML" >/dev/null 2>"$root/err" || bad "board-html renders with no decisions" "$(cat "$root/err")"
cp "$DIRECTOR_STATE/board.html" "$root/page.html"
sec() { python3 - "$root/page.html" <<'PY'
import re, sys
t = open(sys.argv[1], encoding="utf-8").read()
m = re.search(r'<section id="seat-decisions">.*?</section>', t, re.S)
print(m.group(0) if m else "")
PY
}
sec_text() { sec | python3 -c "import html,re,sys; print(html.unescape(re.sub(r'<[^>]+>', '\n', sys.stdin.read())))" | sed '/^[[:space:]]*$/d'; }
[[ "$(sec_text | tr '\n' '|')" == "Seat decisions|No seat decisions recorded|" ]] && ok "a ledger with no decision events says 'No seat decisions recorded' and nothing else" || bad "the empty ledger line" "$(sec_text | tr '\n' '|')"
python3 - "$root/page.html" <<'PY' && ok "the section sits after the ledger table and before history" || bad "the section sits after the ledger table and before history"
import sys
t = open(sys.argv[1], encoding="utf-8").read()
pos = [t.find('id="%s"' % i) for i in ("ledger-table", "seat-decisions", "history")]
sys.exit(0 if all(p >= 0 for p in pos) and pos == sorted(pos) else 1)
PY

# Decisions: two unconfirmed (one with three dependents, a full window), one confirmed recently,
# one confirmed long ago, one overridden recently.
fresh seatdec
printf '# director board\n' >"$DIRECTOR_STATE/board.md"
blk() { printf 'seat-decision@1\nid: %s\nseat: team-a/builder\nquestion: %s\noptions: a) one; b) two\nchosen: b) two because cheaper\ntest: 1 ok 2 ok 3 ok 4 ok 5 ok 6 ok 7 ok 8 ok\nestimate: %s\nundo: revert it\nchanges: pr:ArcavenAE/director#900\n' "$1" "$2" "$3" >"$root/blk"; DWS_NOW="$4" "$DWS" decision add --from "$root/blk" >/dev/null 2>"$root/err" || bad "add decision $1" "$(cat "$root/err")"; }
for r in da db dc dd; do open_at 2026-10-01T08:00:00Z "$r" --title "row $r" --stage building --owner supervisor --next n; done
DWS_NOW=2026-10-01T08:00:00Z "$DWS" link da pr:ArcavenAE/director#901 >/dev/null 2>&1
blk team-a/builder-20261001-1 "Old and answered long ago?" 1 2026-10-01T08:00:00Z
DWS_NOW=2026-10-02T08:00:00Z "$DWS" decision confirm team-a/builder-20261001-1 --words yes --ref relay:R-1 >/dev/null 2>&1 || bad "confirm the old one"
blk team-a/builder-20261020-2 "Smaller one with one dependent <b>x</b>?" 1 2026-10-20T08:00:00Z
blk team-a/builder-20261020-3 "Full window?" 2 2026-10-20T09:00:00Z
blk team-a/builder-20261020-4 "Confirmed lately?" 1 2026-10-20T10:00:00Z
blk team-a/builder-20261020-5 "Overridden lately?" 1 2026-10-20T11:00:00Z
DWS_NOW=2026-10-21T08:00:00Z "$DWS" set dd --waits decision:team-a/builder-20261020-2 >/dev/null 2>&1 || bad "edge dd"
for r in da db dc; do DWS_NOW=2026-10-21T08:00:00Z "$DWS" set "$r" --waits decision:team-a/builder-20261020-3 >/dev/null 2>&1 || bad "edge $r"; done
DWS_NOW=2026-10-21T09:00:00Z "$DWS" decision confirm team-a/builder-20261020-4 --words yes --ref relay:R-2 >/dev/null 2>&1 || bad "confirm lately"
DWS_NOW=2026-10-21T10:00:00Z "$DWS" decision override team-a/builder-20261020-5 --words no --ref relay:R-3 --instead other >/dev/null 2>&1 || bad "override lately"
DWS_NOW=2026-10-22T08:00:00Z "$BOARD_HTML" >/dev/null 2>"$root/err" || bad "board-html renders with decisions" "$(cat "$root/err")"
cp "$DIRECTOR_STATE/board.html" "$root/page.html"

page_ids() { sec | python3 -c "import re,sys; print(' '.join(re.findall(r'<li data-decision=\"([^\"]+)\">', sys.stdin.read().split('seat-decisions-answered')[0])))"; }
want="$(DWS_NOW=2026-10-22T08:00:00Z "$DWS" decisions --unconfirmed --json | python3 -c "import json,sys; print(' '.join(x['id'] for x in json.load(sys.stdin)['decisions']))")"
[[ -n "$want" && "$(page_ids)" == "$want" ]] && ok "the unconfirmed blocks are in the order of dws decisions --unconfirmed" || bad "order matches dws decisions --unconfirmed" "page: $(page_ids) / dws: $want"
[[ "$(page_ids | awk '{print $1}')" == "team-a/builder-20261020-3" ]] && ok "the full window is first" || bad "the full window is first" "$(page_ids)"
t="$(sec_text)"
grep -qF 'team-a/builder-20261020-3 | team-a/builder | decided-by-seat | 1d old | 3 waiting | 2h at risk | window full' <<<"$t" && ok "a block carries id, seat, state, age, dependents waiting and hours at risk as text" || bad "block fields as text" "$t"
grep -qF 'window full' <<<"$t" && [[ "$(grep -c 'window full' <<<"$t")" == 1 ]] && ok "'window full' is said in words, for the full window only" || bad "window full in words"
grep -qF 'da | building | pr:ArcavenAE/director#901' <<<"$t" && ok "a dependent row shows its stage and its PR link" || bad "dependent row with stage and PR" "$t"
grep -qF 'Smaller one with one dependent <b>x</b>?' <<<"$t" && ! sec | grep -qF '<b>x</b>' && ok "the question is escaped" || bad "the question is escaped"
a="$(sed -n '/Answered in the last 14 days/,$p' <<<"$t")"
grep -qF 'team-a/builder-20261020-4 | confirmed | 2026-10-21 | Confirmed lately?' <<<"$a" && ok "a decision confirmed in the last 14 days is one line" || bad "recent confirmed line" "$a"
grep -qF 'team-a/builder-20261020-5 | overridden | 2026-10-21 | Overridden lately?' <<<"$a" && ok "a decision overridden in the last 14 days is one line" || bad "recent overridden line" "$a"
! grep -qF 'team-a/builder-20261001-1' <<<"$a" && ok "an answer older than 14 days is not listed" || bad "old answer listed"
# no colour alone, no red, no alarm sort, no gate: the state is a word, and the section carries no
# colour class or inline style
s_html="$(sec)"
if grep -qE 'class="[^"]*(bad|warn|good|info|unmoved)|style=|var\(--(bad|warn)' <<<"$s_html"; then bad "the section has no colour class or inline style"; else ok "the section has no colour class or inline style"; fi
if sed -n '/#seat-decisions/p' "$root/page.html" | grep -qE 'var\(--(bad|warn|good)|color:#'; then bad "the section's CSS uses no state colour"; else ok "the section's CSS uses no state colour"; fi
for st in decided-by-seat confirmed overridden; do grep -qF "$st" <<<"$t" && ok "the state $st is text" || bad "the state $st is text"; done

export DIRECTOR_STATE="$main_state"

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
