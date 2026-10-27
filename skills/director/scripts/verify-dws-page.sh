#!/usr/bin/env bash
# Prove `dws page` and `dws picks` (W3, sim/design/board-workstream-ledger.md): the ledger renders
# as one self-contained HTML page with six sections in order, each with a count line and its
# rows; the same inputs give the same bytes; the theme and nav are instance files put verbatim
# at the top of the body; the page renders in full with no db; each "Your call" control writes
# the picks doc shape; and `picks` reports a ready pick once.
#
# Usage: skills/director/scripts/verify-dws-page.sh   Exit nonzero on any miss.
# The JS checks need node. Set NODE to a binary; with none that runs, they print SKIP.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DWS="${DWS:-$here/dws}"
SNAP="${SNAP_BIN:-$here/desk-snapshot}"
NODE="${NODE:-node}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
export DWS_TEST_CLOCK=1 DWS_NO_RERENDER=1 PYTHONDONTWRITEBYTECODE=1
NOW=2026-10-27T12:00:00Z
fresh() { export DIRECTOR_STATE="$root/st$1"; rm -rf "$DIRECTOR_STATE"; mkdir -p "$DIRECTOR_STATE"; }
at() { local t=$1; shift; DWS_NOW="$t" "$@"; }
# What the page holds: the slugs in a section, in order, and its count line.
slugs() { python3 - "$1" "$2" <<'EOF'
import re, sys
page, key = open(sys.argv[1], encoding="utf-8").read(), sys.argv[2]
m = re.search(r'<section id="s-%s">(.*?)</section>' % key, page, re.S)
print(" ".join(re.findall(r'data-slug="([^"]*)"', m.group(1))))
EOF
}
count() { python3 - "$1" "$2" <<'EOF'
import re, sys
page, key = open(sys.argv[1], encoding="utf-8").read(), sys.argv[2]
m = re.search(r'<section id="s-%s">.*?<p class="count">(.*?)</p>' % key, page, re.S)
print(m.group(1))
EOF
}
actions() { python3 - "$1" "$2" <<'EOF'
import re, sys
page, key = open(sys.argv[1], encoding="utf-8").read(), sys.argv[2]
m = re.search(r'<section id="s-%s">(.*?)</section>' % key, page, re.S)
rows = re.findall(r'<li class="row".*?</li>', m.group(1), re.S)
print(" | ".join(",".join(re.findall(r'data-action="([^"]*)"', r)) for r in rows))
EOF
}

# --- a fixture ledger with one row per section, boundary rows, and rows the page does not show ---
store() {
  printf '{"c1":{"status":"open"},"c2":{"status":"closed"}}' >"$root/cards.json"
  DWS_NOW=2026-10-27T10:00:00Z "$SNAP" write --cards "$root/cards.json" --reported 2 >/dev/null
  at 2026-10-27T10:30:00Z "$DWS" open b1 --title 'Tom & "Jerry" <b>x</b>' --owner director --stage building --blocked operator --ask c1 --next "answer it" --link 'pr:ArcavenAE/director#9' 'bd:proj-abc1' >/dev/null
  at 2026-10-25T09:00:00Z "$DWS" open m1 --title "Motion one" --owner team-a/builder --stage building --next "finish tests" --link 'issue:ArcavenAE/director#3' >/dev/null
  at 2026-10-20T12:00:01Z "$DWS" open m2 --title "Motion edge" --stage "in review" >/dev/null
  at 2026-10-24T09:00:00Z "$DWS" open w1 --title "Waiting one" --owner team-a/builder --stage building --blocked seat >/dev/null
  at 2026-10-26T09:00:00Z "$DWS" open adhoc-q1 --title "Ad hoc one" --stage idea >/dev/null
  at 2026-10-26T15:00:00Z "$DWS" open d1 --title "Done one" --stage merged >/dev/null
  at 2026-10-10T09:00:00Z "$DWS" open q1 --title "Quiet one" --stage designed >/dev/null
  at 2026-10-20T12:00:00Z "$DWS" open q2 --title "Quiet edge" --stage building >/dev/null
  at 2026-10-27T09:00:00Z "$DWS" open i1 --title "Fresh idea" --stage idea >/dev/null
  at 2026-10-01T09:00:00Z "$DWS" open d0 --title "Old done" --stage deployed >/dev/null
  at 2026-10-02T09:00:00Z "$DWS" open p1 --title "Parked" --stage parked >/dev/null
}
page() { DWS_NOW=$NOW "$DWS" page "$@"; }

fresh 1; store
page >"$root/p1.html" 2>"$root/e" && ok "page exits 0 and prints to stdout" || bad "page exits 0" "$(cat "$root/e")"
grep -q '<title>Director: Work Ledger</title>' "$root/p1.html" && grep -q '<h1>Director: Work Ledger</h1>' "$root/p1.html" && ok "the title is Director: Work Ledger" || bad "title"

# sections, in order
python3 - "$root/p1.html" <<'EOF' && ok "six sections, in the ruled order" || bad "section order"
import re, sys
h = re.findall(r'<section id="s-([a-z]+)"><h2>(.*?)</h2>', open(sys.argv[1], encoding="utf-8").read())
want = [("blocked", "Blocked on you"), ("motion", "In motion"), ("waiting", "Waiting on a seat"), ("adhoc", "Ad hoc"),
        ("done", "Done in the last 7 days"), ("quiet", "Quiet 7 days or more")]
sys.exit(0 if h == want else (print(h) or 1))
EOF
[[ "$(slugs "$root/p1.html" blocked)" == "b1" ]] && ok "blocked: an operator-blocked row" || bad "blocked" "$(slugs "$root/p1.html" blocked)"
[[ "$(slugs "$root/p1.html" motion)" == "m1 m2" ]] && ok "motion: building and in review, newest first, the 6d23h59m row in" || bad "motion" "$(slugs "$root/p1.html" motion)"
[[ "$(slugs "$root/p1.html" waiting)" == "w1" ]] && ok "waiting: a row blocked on a seat" || bad "waiting" "$(slugs "$root/p1.html" waiting)"
[[ "$(slugs "$root/p1.html" adhoc)" == "adhoc-q1" ]] && ok "ad hoc: the adhoc- prefix" || bad "adhoc" "$(slugs "$root/p1.html" adhoc)"
[[ "$(slugs "$root/p1.html" done)" == "d1" ]] && ok "done: merged in the last 7 days, the old deployed row out" || bad "done" "$(slugs "$root/p1.html" done)"
[[ "$(slugs "$root/p1.html" quiet)" == "q1 q2" ]] && ok "quiet: oldest first, the exactly-7-days row in" || bad "quiet" "$(slugs "$root/p1.html" quiet)"
for pair in "blocked:1 row" "motion:2 rows" "waiting:1 row" "adhoc:1 row" "done:1 row" "quiet:2 rows"; do
  [[ "$(count "$root/p1.html" "${pair%%:*}")" == "${pair#*:}" ]] && ok "the ${pair%%:*} count line reads ${pair#*:}" || bad "count ${pair%%:*}" "$(count "$root/p1.html" "${pair%%:*}")"
done
grep -q 'Not shown: 3 row(s)' "$root/p1.html" && ok "the footer counts the three rows no section holds (parked, old done, fresh idea)" || bad "footer" "$(grep -o 'Not shown[^<]*' "$root/p1.html")"
[[ "$(actions "$root/p1.html" motion)" == "nudge-owner,mark-done | nudge-owner,mark-done" && "$(actions "$root/p1.html" waiting)" == "nudge-owner,reassign,park" && "$(actions "$root/p1.html" adhoc)" == "nudge-owner,close,park" && "$(actions "$root/p1.html" quiet)" == "kick-off,park,close | kick-off,park,close" ]] && ok "each section carries its own calls" || bad "calls per section"
[[ -z "$(actions "$root/p1.html" blocked | tr -d ' |')" && -z "$(actions "$root/p1.html" done | tr -d ' |')" ]] && ok "blocked and done rows carry no call (answer on Desk is a link only)" || bad "no calls on blocked and done"
grep -q 'data-section="quiet" data-slug="q1"' "$root/p1.html" && ok "a row names its section and slug, which make the doc id section--slug" || bad "row data attributes"

# row fields
python3 - "$root/p1.html" <<'EOF' && ok "a row shows owner, stage, next action, last moved date and links" || bad "row fields"
import re, sys
page = open(sys.argv[1], encoding="utf-8").read()
b1 = re.search(r'<li class="row" data-section="blocked".*?</li>', page, re.S).group(0)
for want in ("director", "building", "answer it", "2026-10-27", "ArcavenAE/director#9", "proj-abc1", "<code>c1</code>"):
    if want not in b1:
        print("missing", want); sys.exit(1)
EOF
grep -q 'href="bd:' "$root/p1.html" && bad "a bd id is text, not a link" || ok "bd ids are rendered as text"
grep -q 'Tom &amp; &quot;Jerry&quot; &lt;b&gt;x&lt;/b&gt;' "$root/p1.html" && ! grep -q '<b>x</b>' "$root/p1.html" && ok "titles are escaped" || bad "escape"
grep -q 'Desk snapshot read 2026-10-27T10:00:00Z' "$root/p1.html" && ok "the source line names the Desk read time" || bad "desk read time"

# deterministic, self-contained, writes nothing
page >"$root/p2.html" && cmp -s "$root/p1.html" "$root/p2.html" && ok "the same inputs give the same bytes" || bad "deterministic"
page --out "$root/o.html" && cmp -s "$root/p1.html" "$root/o.html" && ok "--out writes the same bytes" || bad "--out"
[[ -z "$(ls -A "$root" | grep '^\.page-' || true)" ]] && ok "--out leaves no temp file behind" || bad "temp file left"
if grep -Eiq '<script[^>]+src=|<link[ >]|@import|url\(|https?://[^"'"'"' )]*\.(js|css|woff)|googleapis|fetch\(|XMLHttpRequest|WebSocket' "$root/p1.html"; then bad "no network and no external script" "$(grep -Eio '<script[^>]+src=|<link[ >]|@import|googleapis|fetch\(|XMLHttpRequest' "$root/p1.html" | head -3)"; else ok "no network and no external script, style or font"; fi
before="$(cd "$DIRECTOR_STATE" && find . -type f -exec shasum -a 256 {} + | sort | shasum -a 256)"; page >/dev/null
[[ "$before" == "$(cd "$DIRECTOR_STATE" && find . -type f -exec shasum -a 256 {} + | sort | shasum -a 256)" ]] && ok "page writes nothing into the state directory" || bad "state changed by page"
! grep -q $'\xe2\x80\x94' "$root/p1.html" && ok "the page holds no em dash" || bad "em dash in the page"

# theme and nav: instance files, verbatim at the top of the body
printf '<style>:root{--bg:#101010}</style>\n' >"$root/theme.html"; printf '<nav id="n">Desk | Ledger</nav>\n' >"$root/nav.html"
page --theme "$root/theme.html" --nav "$root/nav.html" >"$root/p3.html"
python3 - "$root/p3.html" "$root/theme.html" "$root/nav.html" <<'EOF' && ok "--theme then --nav sit verbatim at the top of the body" || bad "theme and nav verbatim"
import sys
page, theme, nav = (open(p, encoding="utf-8").read() for p in sys.argv[1:4])
body = page.split("<body>\n", 1)[1]
sys.exit(0 if body.startswith(theme + nav + "<main>") else (print(body[:120]) or 1))
EOF
cp "$root/theme.html" "$DIRECTOR_STATE/page-theme.css.html"; cp "$root/nav.html" "$DIRECTOR_STATE/page-nav.html"
page >"$root/p4.html"; cmp -s "$root/p3.html" "$root/p4.html" && ok "the defaults are page-theme.css.html and page-nav.html in the state directory" || bad "default instance files"
rm "$DIRECTOR_STATE/page-theme.css.html" "$DIRECTOR_STATE/page-nav.html"
! grep -q '<nav' "$root/p1.html" && ok "with neither file there is no nav" || bad "nav present with no file"
if page --theme "$root/missing.html" >/dev/null 2>"$root/e"; then bad "an explicit theme that is missing is refused"; else grep -q 'does not exist' "$root/e" && ok "an explicit theme that is missing is refused" || bad "missing theme message" "$(cat "$root/e")"; fi

# the Desk card link and an unread snapshot
printf 'https://desk.test/p\n' >"$root/desk-url"
page --desk-url "$root/desk-url" | grep -q 'href="https://desk.test/p#c1"' && ok "--desk-url links a card id" || bad "desk url link"
printf 'http://plain.test\n' >"$root/bad-url"
if page --desk-url "$root/bad-url" >/dev/null 2>&1; then bad "a Desk url that is not https is refused"; else ok "a Desk url that is not https is refused"; fi
rm "$DIRECTOR_STATE/desk-snapshot.json"
page >"$root/p5.html" 2>/dev/null && ok "page renders with the Desk snapshot missing" || bad "render without a snapshot"
grep -q 'Desk UNREAD' "$root/p5.html" && ok "the page says the Desk is UNREAD and does not call the cards open" || bad "UNREAD said"
[[ "$(slugs "$root/p5.html" blocked)" == "b1" ]] && ok "the blocked row is still listed with the Desk UNREAD" || bad "blocked with desk unread"

# ledger missing
fresh 2
page >"$root/p6.html" 2>/dev/null && grep -q 'Ledger UNREAD' "$root/p6.html" && ok "with no ledger the page renders and says Ledger UNREAD" || bad "no ledger"
[[ "$(count "$root/p6.html" blocked)" == "none" ]] && ok "an empty section reads none" || bad "empty count"

# JS: db null, db present, read-only
if "$NODE" -e 1 >/dev/null 2>&1; then
  cat >"$root/harness.js" <<'EOF'
const fs = require("fs");
const page = fs.readFileSync(process.argv[2], "utf8");
const mode = process.argv[3];
const src = page.match(/<script>([\s\S]*?)<\/script>/)[1];
const stub = (o) => Object.assign({ hidden: true, textContent: "", disabled: false, checked: false, value: "",
  setAttribute() {}, classList: { contains: () => false } }, o);
const notice = stub({}), note = stub({}), rdy = stub({}), st = stub({}), call = stub({});
const attrs = { "data-section": "motion", "data-slug": "m1", "data-title": "Motion one" };
const li = stub({ getAttribute: (a) => attrs[a],
  querySelector: (s) => (s === ".note" ? note : s === ".ready" ? rdy : s === ".st" ? st : s.startsWith(".act") ? btn : null),
  querySelectorAll: (s) => (s === ".act" ? [btn] : []) });
const btn = stub({ textContent: "Nudge owner", getAttribute: (a) => (a === "data-action" ? "nudge-owner" : null),
  closest: (s) => (s === "button.act" ? btn : s === "li.row" ? li : null) });
rdy.closest = (s) => (s === "li.row" ? li : null);
rdy.classList = { contains: (c) => c === "ready" };
const handlers = {}; const writes = [];
global.document = { activeElement: null, addEventListener: (t, f) => (handlers[t] = f),
  querySelector: (s) => (s === "#notice" ? notice : null),
  querySelectorAll: (s) => (s === ".call" ? [call] : s === "li.row" ? [li] : []) };
const db = { collection: () => ({ onSnapshot: (ok) => ok({ docs: [] }) }),
  doc: (p) => ({ set: (b) => (writes.push({ p, b }), Promise.resolve()), delete: () => Promise.resolve() }) };
const user = { id: () => Promise.resolve("u1"), can: () => Promise.resolve(mode !== "readonly") };
global.window = { claude: { use: (n) => Promise.resolve(mode === "null" ? null : n === "db" ? db : user) } };
global.claude = window.claude;
(0, eval)(src);
const wait = (ms) => new Promise((r) => setTimeout(r, ms));
(async () => {
  await wait(50);
  if (mode === "null") { console.log(JSON.stringify({ notice: notice.textContent, callHidden: call.hidden })); return; }
  handlers.click({ target: btn }); await wait(700);
  rdy.checked = true; handlers.change({ target: rdy }); await wait(700);
  console.log(JSON.stringify({ notice: notice.textContent, callHidden: call.hidden, writes }));
})().catch((e) => { console.log("ERROR " + e); process.exit(1); });
EOF
  out="$("$NODE" "$root/harness.js" "$root/p1.html" null 2>&1)"
  [[ "$(python3 -c "import json,sys; d=json.loads(sys.argv[1]); print(d['callHidden'], 'not available' in d['notice'])" "$out" 2>/dev/null)" == "True True" ]] && ok "db null: the script says the controls are unavailable and leaves them hidden, without an error" || bad "db null" "$out"
  out="$("$NODE" "$root/harness.js" "$root/p1.html" rw 2>&1)"
  python3 - "$out" <<'EOF' && ok "a call writes picks/<section>--<slug> with the ruled fields, and ready follows the checkbox" || bad "pick write" "$out"
import json, sys
d = json.loads(sys.argv[1])
assert d["callHidden"] is False, "controls not shown"
w = d["writes"]
assert len(w) == 2, w
assert all(x["p"] == "picks/motion--m1" for x in w), w
first, second = w[0]["b"], w[1]["b"]
want = {"action", "action_label", "note", "ready", "section", "title", "page", "at", "by"}
assert set(first) == want, set(first) ^ want
assert (first["action"], first["action_label"], first["ready"], first["section"], first["title"], first["page"], first["by"]) == \
       ("nudge-owner", "Nudge owner", False, "motion", "Motion one", "work-ledger", "u1"), first
assert second["ready"] is True and second["action"] == "nudge-owner", second
EOF
  out="$("$NODE" "$root/harness.js" "$root/p1.html" readonly 2>&1)"
  python3 - "$out" <<'EOF' && ok "read-only viewer: the banner says so and no write is made" || bad "read-only" "$out"
import json, sys
d = json.loads(sys.argv[1])
assert "Read-only" in d["notice"], d["notice"]
assert d["writes"] == [], d["writes"]
EOF
else
  echo "SKIP the JS checks (db null, a pick write, read-only): no node runs here (NODE=$NODE)"
fi

# --- picks ---------------------------------------------------------------------------------------------------
fresh 3
cat >"$root/picks.json" <<'EOF'
[
 {"doc_id":"motion--m1","data":{"action":"nudge-owner","action_label":"Nudge owner","note":"ping them","ready":true,"section":"motion","title":"Motion one","page":"work-ledger","at":"2026-10-27T13:00:00.000Z","by":"u1"}},
 {"id":"quiet--q1","action":"park","action_label":"Park","note":"line one\nline two \u001b[31mred","ready":true,"section":"quiet","title":"Quiet one","page":"work-ledger","at":"2026-10-27T13:05:00.000Z","by":"u1"},
 {"doc_id":"adhoc--adhoc-q1","data":{"action":"close","action_label":"Close","note":"","ready":false,"section":"adhoc","title":"Ad hoc one","page":"work-ledger","at":"2026-10-27T13:06:00.000Z","by":"u1"}}
]
EOF
"$DWS" picks --file "$root/picks.json" >"$root/o" 2>"$root/e" && ok "picks exits 0" || bad "picks exits 0" "$(cat "$root/e")"
[[ "$(wc -l <"$root/o" | tr -d ' ')" == 2 ]] && grep -q '^pick motion--m1 ' "$root/o" && grep -q '^pick quiet--q1 ' "$root/o" && ok "one line per ready pick, none for the not-ready one" || bad "two lines" "$(cat "$root/o")"
grep -q 'note: ping them' "$root/o" && grep -q 'Nudge owner' "$root/o" && grep -q 'by u1' "$root/o" && ok "a line names the call, the note and who" || bad "line content" "$(cat "$root/o")"
! grep -q $'\x1b' "$root/o" && grep -q 'line one line two \\u001b\[31mred' "$root/o" && ok "a note is one line and control characters are shown as escapes" || bad "unsafe text" "$(cat -v "$root/o")"
grep -q '2 new, 0 already reported, 1 not ready' "$root/e" && ok "the summary goes to stderr" || bad "summary" "$(cat "$root/e")"
"$DWS" picks --file "$root/picks.json" >"$root/o2" 2>"$root/e2"
[[ ! -s "$root/o2" ]] && grep -q '0 new, 2 already reported' "$root/e2" && ok "a second run reports nothing: a pick is reported once" || bad "reported once" "$(cat "$root/o2" "$root/e2")"
[[ "$(wc -l <"$DIRECTOR_STATE/picks-seen.jsonl" | tr -d ' ')" == 2 ]] && ok "picks-seen.jsonl holds the two reported picks, not the not-ready one" || bad "seen file" "$(cat "$DIRECTOR_STATE/picks-seen.jsonl")"
[[ "$(ls "$DIRECTOR_STATE")" == "picks-seen.jsonl" ]] && ok "picks wrote nothing else" || bad "other files" "$(ls "$DIRECTOR_STATE")"
python3 - "$root/picks.json" <<'EOF'
import json, sys
p = sys.argv[1]; d = json.load(open(p))
d[0]["data"]["note"] = "ping them again"; d[0]["data"]["at"] = "2026-10-27T14:00:00.000Z"
d[2]["data"]["ready"] = True
json.dump(d, open(p, "w"))
EOF
"$DWS" picks --file "$root/picks.json" >"$root/o3" 2>/dev/null
[[ "$(wc -l <"$root/o3" | tr -d ' ')" == 2 ]] && grep -q 'ping them again' "$root/o3" && grep -q '^pick adhoc--adhoc-q1 ' "$root/o3" && ok "a changed pick and a newly ready pick are reported; the unchanged one is not" || bad "changed pick" "$(cat "$root/o3")"
printf 'not json at all\n{"torn"\n' >>"$DIRECTOR_STATE/picks-seen.jsonl"
"$DWS" picks --file "$root/picks.json" >"$root/o4" 2>"$root/e4" && [[ ! -s "$root/o4" ]] && grep -q 'unreadable line' "$root/e4" && ok "a torn line in the seen file is skipped and said, and reports nothing again" || bad "torn seen" "$(cat "$root/e4")"
if "$DWS" picks --file "$root/nope.json" >/dev/null 2>"$root/e"; then bad "a missing picks file is refused"; else grep -q 'does not exist' "$root/e" && ok "a missing picks file is refused" || bad "missing msg" "$(cat "$root/e")"; fi
printf '"x"' >"$root/str.json"
if "$DWS" picks --file "$root/str.json" >/dev/null 2>&1; then bad "a picks file that is not a list is refused"; else ok "a picks file that is not a list is refused"; fi
printf '{"docs":[{"doc_id":"a--b","data":{"ready":true,"action":"park","at":"t1"}},"junk"]}' >"$root/obj.json"
"$DWS" picks --file "$root/obj.json" >"$root/o5" 2>"$root/e5"
grep -q '^pick a--b ' "$root/o5" && grep -q '1 entr' "$root/e5" && ok "an object with a docs list is read, and an entry that is not a doc is counted" || bad "docs object" "$(cat "$root/o5" "$root/e5")"

echo "dws page: $pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
