#!/usr/bin/env bash
# Prove the Reply Shelf page (skills/director/shelf/shelf.html): its logic block (/*shelf:start*/ to
# /*shelf:end*/) decides what a row shows (new, read, cleared and recoverable for 14 days, or gone), what
# opening, clearing, recovering and asking for more write (only the operator's own fields), and the order
# rows show in; and the page around it declares the capabilities the design names, draws row text as
# escaped text only, and writes nothing but what those patches hold.
#
# The block is cut out, shelf-cases.js is appended, and the result runs in node when it is on PATH, then
# macOS JavaScriptCore, then headless Chrome with a scratch profile. With none of them it stops: a check
# that did not run is not a pass.
#
# Usage: skills/director/scripts/verify-shelf-page.sh   Exit nonzero on any miss. Touches only a scratch dir.
set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
page="${SHELF_PAGE:-$here/../shelf/shelf.html}"
cases="$here/../shelf/shelf-cases.js"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }
finish() { echo "$pass passed, $fail failed"; [[ $fail -eq 0 ]]; exit $?; }

root="$(mktemp -d "${SHELF_TMP:-${TMPDIR:-/tmp}}/shelf.XXXXXX")" || { echo "FAIL scratch dir"; exit 1; }
trap 'rm -rf "$root"' EXIT

[[ -f "$page" ]] || { bad "the page exists" "no file at $page"; finish; }
[[ -f "$cases" ]] || { bad "the cases exist" "no file at $cases"; finish; }

# --- the logic block ----------------------------------------------------------------------------
if grep -q '/\*shelf:start\*/' "$page" && grep -q '/\*shelf:end\*/' "$page"; then ok "the page marks its logic block"
else bad "the page marks its logic block" "no /*shelf:start*/ and /*shelf:end*/ in $page"; finish; fi
sed -n '/\/\*shelf:start\*\//,/\/\*shelf:end\*\//p' "$page" >"$root/shelf.js"
for f in shelfView shelfOpenPatch shelfClearPatch shelfRecoverPatch shelfMorePatch shelfSort shelfDaysLeft; do
  [[ "$(grep -c "function $f(" "$root/shelf.js")" == 1 ]] && ok "the block defines $f once" || bad "the block defines $f once"
done

# --- run the cases ------------------------------------------------------------------------------
runtime=""
if command -v node >/dev/null 2>&1 && node -e '1' >/dev/null 2>&1; then runtime=node
elif command -v osascript >/dev/null 2>&1 && osascript -l JavaScript -e '1' >/dev/null 2>&1; then runtime=jxa  # macOS JavaScriptCore
else
  for c in "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" "$(command -v google-chrome 2>/dev/null || true)" "$(command -v chromium 2>/dev/null || true)"; do
    if [[ -n "$c" && -x "$c" ]]; then runtime="$c"; break; fi
  done
fi
if [[ -z "$runtime" ]]; then bad "a JavaScript runtime is available" "neither node nor a Chrome or Chromium was found"; finish; fi

if [[ "$runtime" == node ]]; then
  { echo 'function print(l){console.log(l)}'; cat "$root/shelf.js" "$cases"; } >"$root/run.js"
  node_rc=0
  node "$root/run.js" >"$root/out" 2>"$root/err" || node_rc=$?
elif [[ "$runtime" == jxa ]]; then
  { printf '%s\n' 'var __out=[];function print(l){__out.push(l)}'; cat "$root/shelf.js" "$cases"; printf '%s\n' '__out.join("\n");'; } >"$root/run.js"
  RUNJS="$root/run.js" perl -e 'alarm shift; exec @ARGV or die "exec $ARGV[0]: $!\n"' 60 \
    osascript -l JavaScript -e 'ObjC.import("Foundation"); eval($.NSString.stringWithContentsOfFileEncodingError(ObjC.unwrap($.NSProcessInfo.processInfo.environment.objectForKey("RUNJS")),4,null).js)' \
    >"$root/out" 2>"$root/err" || true
else
  { echo '<!doctype html><meta charset=utf-8><pre id=o></pre><script>var __o=[];function print(l){__o.push(l)}'
    sed 's#</script#<\\/script#g' "$root/shelf.js" "$cases"
    echo 'document.getElementById("o").textContent=__o.join("\n")</script>'; } >"$root/run.html"
  # Bounded: a browser that never exits is a failed run, not a hang (exec failure is an error, not rc 0).
  perl -e 'alarm shift; exec @ARGV or die "exec $ARGV[0]: $!\n"' 60 \
    "$runtime" --headless --disable-gpu --disable-background-networking --no-first-run \
    --user-data-dir="$root/profile" --dump-dom "file://$root/run.html" >"$root/dom" 2>"$root/err" </dev/null
  python3 - "$root/dom" >"$root/out" <<'PY'
import html, re, sys
m = re.search(r'<pre id="o">(.*?)</pre>', open(sys.argv[1], encoding="utf-8").read(), re.S)
print(html.unescape(m.group(1)) if m else "FAIL no output from the page")
PY
fi
if [[ "${node_rc:-0}" != 0 ]]; then bad "the cases ran to the end" "node exited $node_rc: $(head -c 300 "$root/err" | tr '\n' ' ')"; fi
ok "the cases ran under $(basename "$runtime")"
npass="$(grep -c '^PASS ' "$root/out" || true)"; nfail="$(grep -c '^FAIL ' "$root/out" || true)"
# The cases print their own last line; a run that stopped early (an exception) never prints it.
last="$(grep -c '^PASS shelf: [0-9]* passed, 0 failed$' "$root/out" || true)"
if [[ "$nfail" == 0 && "$npass" -gt 20 && "$last" == 1 ]]; then ok "the shelf logic cases pass ($npass checks, none failed)"
else bad "the shelf logic cases pass" "$npass passed, $nfail failed, summary line seen $last time(s): $(grep '^FAIL ' "$root/out" | head -5 | tr '\n' ';')"; fi
grep '^FAIL ' "$root/out" | head -20

# --- the page around the block ------------------------------------------------------------------
grep -qF '<title>Director Reply Shelf</title>' "$page" && ok "the page is titled Director Reply Shelf" || bad "the page is titled Director Reply Shelf"
caps='{"db":{"rules":[{"path":"replies","write":"owner"}]},"user":{}}'
grep -qF "$caps" "$page" && ok "the header comment names the capabilities: $caps" || bad "the header comment names the capabilities"
grep -qF 'db.collection("replies")' "$page" && ok "the page reads the replies collection" || bad "the page reads the replies collection"
! grep -qE 'collection\("(decisions|answers|dash_)' "$page" && ok "the page touches no Desk or dashboard collection" || bad "the page touches a Desk or dashboard collection"
body="$(sed '1,/<\/style>/d' "$page" | sed '/\/\*shelf:start\*\//,/\/\*shelf:end\*\//d')"
! grep -qE '\.set\(|\.delete\(|\.add\(' <<<"$body" && ok "the page only updates a row: no set, delete or add" || bad "the page calls set, delete or add"
grep -qF '.update(' <<<"$body" && ok "the page writes with update" || bad "the page writes with update"
for p in shelfOpenPatch shelfClearPatch shelfRecoverPatch shelfMorePatch; do
  grep -qF "$p(" <<<"$body" && ok "the page writes through $p" || bad "the page writes through $p"
done
! grep -qE 'update\((\{|[a-z]+\.)' <<<"$body" && ok "no update is built by hand: every write is a patch from the block" || bad "an update is built by hand"
! grep -qE 'innerHTML *= *[^;]*(r\.summary|r\.source|r\.more|r\.ask|r\.asked_)' "$page" && ok "no row text is assigned to innerHTML raw" || bad "row text is assigned to innerHTML raw"
for f in 'esc(r.summary)' 'esc(r.source)' 'esc(r.ask)' 'esc(r.asked_of)' 'esc(r.more)'; do
  grep -qF "$f" "$page" && ok "the page draws $f as escaped text" || bad "the page draws $f as escaped text"
done
! grep -v '^<link ' "$page" | grep -qE '<a |href=|<img|<iframe|fetch\(|XMLHttpRequest|sendBeacon|WebSocket' && ok "the page has no link, image, frame or network call (the two font <link> lines aside)" || bad "the page has a link, image, frame or network call"
! grep -qiE 'dispatch|review' <<<"$(sed -n '/<body>/,$p' "$page" | grep -iE '<h[12]|class="tab')" && ok "Dispatch and Review are not on the page (phase 2)" || bad "Dispatch or Review is on the page"
grep -qF 'viewport' "$page" && ok "the page sets a viewport for a phone" || bad "the page sets a viewport for a phone"
finish
