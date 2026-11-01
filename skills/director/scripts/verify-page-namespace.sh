#!/usr/bin/env bash
# verify-page-namespace.sh: page-namespace puts one name in the title and description of every page
# director publishes, so two installs' pages can be told apart.
#   - set: the one line in $DIRECTOR_STATE/page-namespace is used, and the title and a description
#     meta carry it (helper, the Desk page, `dws page`, board-html);
#   - unset: the owner's GitHub login (a stub for gh) is used; when gh cannot answer there is no
#     namespace and the page is byte for byte what it was;
#   - malformed (empty, more than one line, a bad character, over 39 characters, not UTF-8): exit 2
#     and one fixed line, and the writers refuse to render.
# Two mutants of the helper (no validation; apply does nothing) must each be caught.
#
# Usage: scripts/verify-page-namespace.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
desk="$here/../desk/desk.html"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d "${TMPDIR:-/tmp}/page-ns.XXXXXX")"
trap 'rm -rf "$root"' EXIT
unset DIRECTOR_HOME
export DWS_NOW=2099-01-01T00:00:00Z DWS_TEST_CLOCK=1

# A gh stub: prints the login named by $STUB_LOGIN, or fails when it is unset.
mkdir -p "$root/bin"
printf '#!/bin/sh\n[ -n "${STUB_LOGIN:-}" ] || exit 1\necho "$STUB_LOGIN"\n' > "$root/bin/gh"; chmod +x "$root/bin/gh"

# tree NAME [HELPER]: the three scripts in one directory, as installed, with the given helper.
tree() {
  mkdir -p "$root/$1"; cp "$here/dws" "$here/board-html" "$root/$1/"
  cp "${2:-$here/page-namespace}" "$root/$1/page-namespace"; chmod +x "$root/$1/"*
  echo "$root/$1"
}
state() { rm -rf "$root/st"; mkdir -p "$root/st"; printf '%s\n' "# board" "" "## Now" "- a" > "$root/st/board.md"; }
ns_file() { printf '%b' "$1" > "$root/st/page-namespace"; }
helper() { # TREE ARGS... ; sets RC OUT ERR
  local t="$1"; shift; RC=0
  OUT="$(env -i HOME="$root" PATH="$root/bin:/usr/bin:/bin" DIRECTOR_STATE="$root/st" GH="$root/bin/gh" ${STUB_LOGIN:+STUB_LOGIN="$STUB_LOGIN"} "$t/page-namespace" "$@" 2>"$root/err")" || RC=$?
  ERR="$(cat "$root/err")"
}
real="$(tree real)"
page='<html><head><title>Director Board</title></head><body></body></html>'

# set
state; ns_file 'arcaven\n'
helper "$real" get; [[ $RC == 0 && "$OUT" == arcaven ]] && ok "set: get prints the file's line" || bad "set: get" "rc=$RC $OUT $ERR"
OUT="$(printf '%s' "$page" | env -i HOME="$root" PATH="/usr/bin:/bin" DIRECTOR_STATE="$root/st" "$real/page-namespace" apply)"
[[ "$OUT" == *'<title>Director Board (arcaven)</title>'* && "$OUT" == *'<meta name="description" content="Director Board, namespace arcaven">'* ]] && ok "set: apply puts it in the title and the description" || bad "set: apply" "$OUT"
ns_file 'a&b<c\n'; helper "$real" get; [[ $RC == 2 ]] && ok "set: a name with markup characters is refused, not escaped into a page" || bad "markup name" "rc=$RC"
ns_file 'ok.name_1-x'; helper "$real" get; [[ $RC == 0 && "$OUT" == ok.name_1-x ]] && ok "set: a file with no trailing newline is read" || bad "no newline" "rc=$RC $OUT"

# unset
state; STUB_LOGIN=jared helper "$real" get; [[ $RC == 0 && "$OUT" == jared ]] && ok "unset: the owner's GitHub login is the namespace" || bad "unset login" "rc=$RC $OUT $ERR"
state; unset STUB_LOGIN; helper "$real" get; [[ $RC == 0 && -z "$OUT" ]] && ok "unset, gh cannot answer: no namespace" || bad "unset no gh" "rc=$RC $OUT"
OUT="$(printf '%s' "$page" | env -i HOME="$root" PATH="/usr/bin:/bin" DIRECTOR_STATE="$root/st" GH="$root/bin/gh" "$real/page-namespace" apply)"
[[ "$OUT" == "$page" ]] && ok "unset, gh cannot answer: the page is byte for byte unchanged" || bad "unchanged page" "$OUT"

# malformed
long="$(printf 'a%.0s' $(seq 1 40))"
for case in 'empty|' 'two lines|a\nb\n' 'a space|a b\n' 'a slash|a/b\n' 'leading hyphen|-a\n' 'a CR|a\r\n' "forty characters|$long\n" 'not UTF-8|\377\376\n'; do
  name="${case%%|*}"; body="${case#*|}"
  state; ns_file "$body"
  helper "$real" get
  if [[ $RC == 2 && "$ERR" == page-namespace:* && $(printf '%s\n' "$ERR" | wc -l | tr -d ' ') == 1 && -z "$OUT" ]]; then ok "malformed ($name): refused with one line"
  else bad "malformed ($name)" "rc=$RC out=$OUT err=$ERR"; fi
done

# the writers
writer() { # TREE ; runs dws page and board-html under the current state
  local t="$1"
  WRC=0; env -i HOME="$root" PATH="/usr/bin:/bin" DIRECTOR_STATE="$root/st" GH=/usr/bin/false DWS_NOW="$DWS_NOW" DWS_TEST_CLOCK=1 "$t/dws" page --out "$root/page.html" >"$root/wout" 2>"$root/werr" || WRC=$?
  BRC=0; env -i HOME="$root" PATH="/usr/bin:/bin" DIRECTOR_STATE="$root/st" GH=/usr/bin/false DWS_NOW="$DWS_NOW" DWS_TEST_CLOCK=1 DWS_NO_RERENDER=1 "$t/board-html" >"$root/bout" 2>"$root/berr" || BRC=$?
}
state; ns_file 'arcaven\n'; rm -f "$root/page.html"; writer "$real"
if [[ $WRC == 0 && $(/usr/bin/grep -c '<title>Director: Work Ledger (arcaven)</title>' "$root/page.html") == 1 && $(/usr/bin/grep -c 'name="description" content="Director: Work Ledger, namespace arcaven"' "$root/page.html") == 1 ]]; then ok "dws page: title and description carry the namespace"
else bad "dws page set" "rc=$WRC $(cat "$root/werr")"; fi
if [[ $BRC == 0 && $(/usr/bin/grep -c '<title>Director Board (arcaven)</title>' "$root/st/board.html") == 1 && $(/usr/bin/grep -c 'name="description" content="Director Board, namespace arcaven"' "$root/st/board.html") == 1 ]]; then ok "board-html: title and description carry the namespace"
else bad "board-html set" "rc=$BRC $(cat "$root/berr")"; fi
state; rm -f "$root/page.html"; writer "$real"
if [[ $WRC == 0 && $(/usr/bin/grep -c '<title>Director: Work Ledger</title>' "$root/page.html") == 1 && $(/usr/bin/grep -c 'name="description"' "$root/page.html") == 0 && $BRC == 0 && $(/usr/bin/grep -c '<title>Director Board</title>' "$root/st/board.html") == 1 ]]; then ok "no namespace: dws page and board-html keep their plain titles"
else bad "writers unset" "dws=$WRC board=$BRC"; fi
state; ns_file 'bad name\n'; rm -f "$root/page.html" "$root/st/board.html"; writer "$real"
if [[ $WRC != 0 && ! -e "$root/page.html" && "$(cat "$root/werr")" == *"page-namespace"* && $BRC != 0 && ! -e "$root/st/board.html" && "$(cat "$root/berr")" == *"page-namespace"* ]]; then ok "malformed file: dws page and board-html refuse and write no page"
else bad "writers malformed" "dws=$WRC board=$BRC $(cat "$root/werr") $(cat "$root/berr")"; fi

# the Desk page
state; ns_file 'arcaven\n'
OUT="$(env -i HOME="$root" PATH="/usr/bin:/bin" DIRECTOR_STATE="$root/st" "$real/page-namespace" apply "$desk")"
[[ $(printf '%s\n' "$OUT" | /usr/bin/grep -c '<title>Director Decision Desk (arcaven)</title>') == 1 && $(printf '%s\n' "$OUT" | /usr/bin/grep -c 'name="description" content="Director Decision Desk, namespace arcaven"') == 1 ]] && ok "desk.html: apply puts the namespace in its title and description" || bad "desk apply"
state; env -i HOME="$root" PATH="/usr/bin:/bin" DIRECTOR_STATE="$root/st" GH=/usr/bin/false "$real/page-namespace" apply "$desk" > "$root/desk.out"
cmp -s "$desk" "$root/desk.out" && ok "desk.html: with no namespace apply returns the file byte for byte" || bad "desk unchanged"

# apply twice equals apply once (a page is republished again and again)
state; ns_file 'arcaven\n'
printf '%s' "$page" | env -i HOME="$root" PATH="/usr/bin:/bin" DIRECTOR_STATE="$root/st" "$real/page-namespace" apply > "$root/ap1"
env -i HOME="$root" PATH="/usr/bin:/bin" DIRECTOR_STATE="$root/st" "$real/page-namespace" apply "$root/ap1" > "$root/ap2"
cmp -s "$root/ap1" "$root/ap2" && [[ $(/usr/bin/grep -c 'namespace arcaven' "$root/ap2") == 1 ]] && ok "apply twice gives the bytes of apply once" || bad "apply is not idempotent" "$(cat "$root/ap2")"
env -i HOME="$root" PATH="/usr/bin:/bin" DIRECTOR_STATE="$root/st" "$real/page-namespace" apply "$desk" > "$root/dk1"
env -i HOME="$root" PATH="/usr/bin:/bin" DIRECTOR_STATE="$root/st" "$real/page-namespace" apply "$root/dk1" > "$root/dk2"
cmp -s "$root/dk1" "$root/dk2" && ok "the Desk page applied twice gives the bytes of applying once" || bad "desk apply is not idempotent"

# a missing helper: board-html and dash-writer exit 2 with one line, no traceback
mkdir -p "$root/nohelper"; cp "$here/board-html" "$here/dash-writer" "$root/nohelper/"; cp "$here/dws" "$root/nohelper/"
state; BRC=0; env -i HOME="$root" PATH="/usr/bin:/bin" DIRECTOR_STATE="$root/st" GH=/usr/bin/false DWS_NO_RERENDER=1 "$root/nohelper/board-html" >"$root/bout" 2>"$root/berr" || BRC=$?
[[ $BRC != 0 && "$(cat "$root/berr")" == "board-html: page-namespace is missing"* && "$(wc -l < "$root/berr" | tr -d ' ')" == 1 ]] && ok "board-html without the helper stops with one fixed line" || bad "board-html no helper" "rc=$BRC $(cat "$root/berr")"
printf "{}" > "$root/v.json"; DRC=0; env -i HOME="$root" PATH="/usr/bin:/bin" DIRECTOR_STATE="$root/st" GH=/usr/bin/false DWS_TEST_CLOCK=1 DWS_NOW="$DWS_NOW" python3 -I "$root/nohelper/dash-writer" run --force --versions "$root/v.json" >"$root/dout" 2>"$root/derr" || DRC=$?
[[ $DRC == 2 && "$(cat "$root/derr")" == *"dash-writer: page-namespace is missing"* && "$(cat "$root/derr")" != *Traceback* ]] && ok "dash-writer without the helper exits 2 with one fixed line" || bad "dash-writer no helper" "rc=$DRC $(head -c 300 "$root/derr")"

# mutants
sed 's/^    if not VALID.fullmatch(value):$/    if False:/' "$here/page-namespace" > "$root/m-novalid"
sed 's/^    if not ns:$/    if True:/' "$here/page-namespace" > "$root/m-noapply"
for m in novalid noapply; do cmp -s "$here/page-namespace" "$root/m-$m" && bad "mutant $m not built"; done
mt="$(tree mnv "$root/m-novalid")"; state; ns_file 'bad name\n'; helper "$mt" get
[[ $RC == 0 ]] && ok "a helper without the name check accepts a bad name, so the malformed cases guard it" || bad "no-validation mutant not caught" "rc=$RC"
mt="$(tree mna "$root/m-noapply")"; state; ns_file 'arcaven\n'
OUT="$(printf '%s' "$page" | env -i HOME="$root" PATH="/usr/bin:/bin" DIRECTOR_STATE="$root/st" "$mt/page-namespace" apply)"
[[ "$OUT" == "$page" ]] && ok "a helper whose apply does nothing leaves the title bare, so the set cases guard it" || bad "no-apply mutant not caught"

echo "page-namespace: $pass passed, $fail failed"
[[ $fail -eq 0 ]]
