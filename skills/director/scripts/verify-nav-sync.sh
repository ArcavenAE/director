#!/usr/bin/env bash
# Prove nav-sync check reports, for each exported Director page, whether the nav
# it carries matches the instance file page-nav.html, and writes nothing.
#   - a page whose nav matches reads ok, whichever link it marks current;
#   - a page missing a link reads stale and names the missing label;
#   - a page with an extra link reads stale and names the extra label;
#   - a page with the same links but other markup reads differs;
#   - a page with no nav reads no-nav; an unreadable page reads unreadable;
#   - the exit code is 0 only when every page is ok, 1 on any miss, 2 on usage;
#   - a missing or navless page-nav.html refuses (exit 2) rather than pass;
#   - the default nav is $DIRECTOR_STATE/page-nav.html;
#   - nothing it reads is modified.
#
# Usage: skills/director/scripts/verify-nav-sync.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NS="${NAV_SYNC_BIN:-$here/nav-sync}"
[[ -x "$NS" ]] || { echo "nav-sync not executable at $NS"; exit 2; }
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
nav_html() { # $1 = label to mark current, rest = labels in the menu
  local cur="$1"; shift
  printf '<nav class="dnav" aria-label="Director pages"><span class="dnav-brand">Director</span><ul>'
  local l n=0
  for l in "$@"; do
    n=$((n+1))
    if [[ "$l" == "$cur" ]]; then printf '<li><a href="https://claude.ai/artifact/id%s" target="_top" aria-current="page">%s</a></li>' "$n" "$l"
    else printf '<li><a href="https://claude.ai/artifact/id%s" target="_top">%s</a></li>' "$n" "$l"; fi
  done
  printf '</ul></nav>\n'
}
page() { printf '<html><head><title>t</title></head><body>\n%s<main>body text</main></body></html>\n' "$1"; }

nav_html "Work Ledger" "Decision Desk" "Work Ledger" "Reply Shelf" >"$root/page-nav.html"
page "$(nav_html "Decision Desk" "Decision Desk" "Work Ledger" "Reply Shelf")" >"$root/desk.html"
page "$(nav_html "Reply Shelf" "Decision Desk" "Work Ledger" "Reply Shelf")" >"$root/shelf.html"
page "$(nav_html "Decision Desk" "Decision Desk" "Work Ledger")" >"$root/stale.html"
page "$(nav_html "Decision Desk" "Decision Desk" "Work Ledger" "Reply Shelf" "Old Page")" >"$root/extra.html"
page "$(nav_html "Decision Desk" "Decision Desk" "Work Ledger" "Reply Shelf" | sed 's/target="_top"/target="_blank"/')" >"$root/markup.html"
printf '<html><body>no menu here</body></html>\n' >"$root/nonav.html"
snap() { (cd "$root" && find . -type f ! -name out ! -name err -exec shasum -a 256 {} + | LC_ALL=C sort | shasum -a 256); }
before="$(snap)"

run() { set +e; "$NS" check "$@" >"$root/out" 2>"$root/err"; rc=$?; set -e; }

run --nav "$root/page-nav.html" "$root/desk.html" "$root/shelf.html"
[[ $rc -eq 0 ]] && ok "matching pages exit 0, whichever link each marks current" || bad "matching pages exit 0" "rc=$rc $(cat "$root/out" "$root/err")"
[[ "$(grep -c ': ok$' "$root/out")" == 2 ]] && ok "each matching page reads ok" || bad "each matching page reads ok" "$(cat "$root/out")"

run --nav "$root/page-nav.html" "$root/stale.html"
[[ $rc -eq 1 ]] && ok "a page missing a link exits 1" || bad "a page missing a link exits 1" "rc=$rc"
grep -q 'stale.html: stale' "$root/out" && grep -q 'missing "Reply Shelf"' "$root/out" && ok "and names the missing label" || bad "and names the missing label" "$(cat "$root/out")"

run --nav "$root/page-nav.html" "$root/extra.html"
[[ $rc -eq 1 ]] && grep -q 'extra "Old Page"' "$root/out" && ok "an extra link is named and exits 1" || bad "an extra link is named and exits 1" "rc=$rc $(cat "$root/out")"

run --nav "$root/page-nav.html" "$root/markup.html"
[[ $rc -eq 1 ]] && grep -q 'markup.html: differs' "$root/out" && ok "same links, other markup reads differs" || bad "same links, other markup reads differs" "rc=$rc $(cat "$root/out")"

run --nav "$root/page-nav.html" "$root/nonav.html"
[[ $rc -eq 1 ]] && grep -q 'nonav.html: no-nav' "$root/out" && ok "a page with no nav reads no-nav" || bad "a page with no nav reads no-nav" "rc=$rc $(cat "$root/out")"

run --nav "$root/page-nav.html" "$root/absent.html"
[[ $rc -eq 1 ]] && grep -q 'absent.html: unreadable' "$root/out" && ok "an unreadable page reads unreadable" || bad "an unreadable page reads unreadable" "rc=$rc $(cat "$root/out")"

run --nav "$root/page-nav.html" "$root/desk.html" "$root/stale.html" "$root/shelf.html"
[[ $rc -eq 1 ]] && [[ "$(grep -c . "$root/out")" == 3 ]] && ok "one line per page, and one miss makes the run exit 1" || bad "one line per page" "rc=$rc $(cat "$root/out")"

run --nav "$root/absent-nav.html" "$root/desk.html"
[[ $rc -eq 2 ]] && ok "a missing page-nav.html refuses with exit 2" || bad "a missing page-nav.html refuses" "rc=$rc"
printf 'no nav element\n' >"$root/bad-nav.html"
run --nav "$root/bad-nav.html" "$root/desk.html"
[[ $rc -eq 2 ]] && ok "a page-nav.html with no nav element refuses with exit 2" || bad "a navless page-nav.html refuses" "rc=$rc"
run --nav "$root/page-nav.html"
[[ $rc -eq 2 ]] && ok "no page given is a usage error (exit 2)" || bad "no page given is a usage error" "rc=$rc"
run
[[ $rc -eq 2 ]] && ok "no subcommand is a usage error (exit 2)" || bad "no subcommand is a usage error" "rc=$rc"

mkdir -p "$root/state"; cp "$root/page-nav.html" "$root/state/page-nav.html"
before="$(snap)"
set +e; DIRECTOR_STATE="$root/state" "$NS" check "$root/desk.html" >"$root/out" 2>&1; rc=$?; set -e
[[ $rc -eq 0 ]] && ok "the default nav is \$DIRECTOR_STATE/page-nav.html" || bad "the default nav is \$DIRECTOR_STATE/page-nav.html" "rc=$rc $(cat "$root/out")"

[[ "$(snap)" == "$before" ]] && ok "no fixture was modified and no file was created" || bad "no fixture was modified and no file was created"

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
