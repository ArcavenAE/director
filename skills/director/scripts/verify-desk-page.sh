#!/usr/bin/env bash
# Prove the Decision Desk page (skills/director/desk/desk.html): its Markdown-subset renderer draws each
# construct (paragraphs, **bold**, *italic*, bullets, numbered lists, `code`), renders everything else as
# text (no raw HTML, no links), and the card draws context, recommendation, ruling and option text through
# it while the operator's own answers stay escaped text.
#
# The renderer sits between the markers /*md:start*/ and /*md:end*/ in desk.html. This script cuts that
# block out, appends desk-md-cases.js, and runs the result in node when it is on PATH, otherwise in headless
# Chrome (macOS app path, or google-chrome or chromium on PATH) with a scratch profile. With neither it
# stops: a check that did not run is not a pass.
#
# Usage: skills/director/scripts/verify-desk-page.sh   Exit nonzero on any miss. Touches only a scratch dir.
set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
desk="${DESK_PAGE:-$here/../desk/desk.html}"
cases="$here/../desk/desk-md-cases.js"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }
finish() { echo "$pass passed, $fail failed"; [[ $fail -eq 0 ]]; exit $?; }

root="$(mktemp -d "${DESK_TMP:-${TMPDIR:-/tmp}}/desk.XXXXXX")" || { echo "FAIL scratch dir"; exit 1; }
trap 'rm -rf "$root"' EXIT

[[ -f "$desk" ]] || { bad "the page exists" "no file at $desk"; finish; }
[[ -f "$cases" ]] || { bad "the cases exist" "no file at $cases"; finish; }

# --- the renderer block -------------------------------------------------------------------------
if grep -q '/\*md:start\*/' "$desk" && grep -q '/\*md:end\*/' "$desk"; then ok "the page marks its renderer block"
else bad "the page marks its renderer block" "no /*md:start*/ and /*md:end*/ in $desk"; finish; fi
sed -n '/\/\*md:start\*\//,/\/\*md:end\*\//p' "$desk" >"$root/md.js"
[[ "$(grep -c 'function mdRender' "$root/md.js")" == 1 ]] && ok "the block defines mdRender once" || bad "the block defines mdRender once"

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
  { echo 'function print(l){console.log(l)}'; cat "$root/md.js" "$cases"; } >"$root/run.js"
  node "$root/run.js" >"$root/out" 2>"$root/err"
elif [[ "$runtime" == jxa ]]; then
  { printf '%s\n' 'var __out=[];function print(l){__out.push(l)}'; cat "$root/md.js" "$cases"; printf '%s\n' '__out.join("\n");'; } >"$root/run.js"
  RUNJS="$root/run.js" perl -e 'alarm shift; exec @ARGV or die "exec $ARGV[0]: $!\n"' 60 \
    osascript -l JavaScript -e 'ObjC.import("Foundation"); eval($.NSString.stringWithContentsOfFileEncodingError(ObjC.unwrap($.NSProcessInfo.processInfo.environment.objectForKey("RUNJS")),4,null).js)' \
    >"$root/out" 2>"$root/err" || true
else
  { echo '<!doctype html><meta charset=utf-8><pre id=o></pre><script>var __o=[];function print(l){__o.push(l)}'
    sed 's#</script#<\\/script#g' "$root/md.js" "$cases"
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
ok "the cases ran under $(basename "$runtime")"
npass="$(grep -c '^PASS ' "$root/out" || true)"; nfail="$(grep -c '^FAIL ' "$root/out" || true)"
if [[ "$nfail" == 0 && "$npass" -gt 40 ]]; then ok "the renderer cases pass ($npass checks, none failed)"
else bad "the renderer cases pass" "$npass passed, $nfail failed: $(grep '^FAIL ' "$root/out" | head -5 | tr '\n' ';')"; fi
grep '^FAIL ' "$root/out" | head -20

# --- the card uses the renderer, and the operator's own words stay text -------------------------
card="$(sed -n '/function card(d)/,/^  function render()/p' "$desk")"
for f in 'mdRender(d.context)' 'mdRender(o.text)' 'mdRender(d.rec)' 'mdRender(d.ruling)'; do
  grep -qF "$f" <<<"$card" && ok "the card draws $f" || bad "the card draws $f"
done
grep -qF 'esc(a.note' <<<"$card" && ok "the operator's note is still escaped text" || bad "the operator's note is still escaped text"
! grep -qE 'mdRender\((a\.|state\.answers|a\.note)' <<<"$card" && ok "answers are not run through the renderer" || bad "answers are not run through the renderer"
! grep -qE '(esc\(d\.context\)|esc\(o\.text\)|esc\(d\.ruling\))' <<<"$card" && ok "no context, option or ruling text bypasses the renderer" || bad "context, option or ruling text bypasses the renderer"
! grep -qE 'innerHTML *= *[^;]*(d\.context|o\.text|d\.ruling|d\.rec)\b' "$desk" && ok "no raw card text is assigned to innerHTML" || bad "raw card text is assigned to innerHTML"
finish
