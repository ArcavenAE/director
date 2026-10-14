#!/usr/bin/env bash
# Prove composer-check refuses an inject into typed text, accepts an empty
# composer or a dim suggestion, and handles the auto-mode menu with "2" only.
# The captures are synthetic, built from the colour codes a `capture-pane -e`
# shows (typed text in the default colour code 39, a suggestion wrapped in the
# dim code 2), and tmux is a fake that records every key sent.
#
# Usage: skills/director/scripts/verify-composer-check.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
C="${COMPOSER_CHECK_BIN:-$here/composer-check}"
[[ -x "$C" ]] || { echo "composer-check not executable at $C"; exit 2; }
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
P=$'❯'; E=$'\033'

check() { # name, expected verdict, expected rc, capture text
  local name="$1" want="$2" rc="$3" text="$4" got r
  printf '%s\n' "$text" >"$root/cap"
  set +e; got="$("$C" --file "$root/cap" 2>/dev/null)"; r=$?; set -e
  [[ "$got" == "$want" && "$r" == "$rc" ]] && ok "$name" || bad "$name" "got $got rc=$r, want $want rc=$rc"
}

check "an empty composer is EMPTY"            EMPTY      0 "some output
${P} "
check "an empty composer with a cursor block is EMPTY" EMPTY 0 "${P} ${E}[7m ${E}[0m"
check "a dim suggestion is SUGGESTION"        SUGGESTION 0 "${P} ${E}[2mgo ahead with the merge${E}[0m"
check "a dim suggestion with a cursor on its first letter is SUGGESTION" SUGGESTION 0 "${P} ${E}[2m${E}[7mg${E}[27mo ahead${E}[0m"
check "typed text in the default colour is HAS-TEXT" HAS-TEXT 1 "${P} ${E}[39mhalf a message"
check "typed text with no colour code is HAS-TEXT"   HAS-TEXT 1 "${P} half a message"
check "typed text after a dim span is HAS-TEXT"      HAS-TEXT 1 "${P} ${E}[2mhint${E}[0m typed"
check "an older prompt line does not count; the last one decides" EMPTY 0 "${P} ${E}[39mold text
reply
${P} "
check "no prompt mark is NO-PROMPT"           NO-PROMPT  3 "just some output"
check "the auto-mode menu is MENU"            MENU       2 "${P} 
Teach auto mode about your environment?
 1. Yes
 2. Not now"

# --- the menu handler: a fake tmux that records keys -----------------------------
cat >"$root/tmux" <<FAKE
#!/usr/bin/env bash
state="$root/menu-gone"
case "\$*" in
  *capture-pane*) if [[ -e "\$state" ]]; then printf '%s \n' "$P"; else printf '%s\nTeach auto mode about your environment?\n 1. Yes\n 2. Not now\n' "$P"; fi ;;
  *send-keys*) echo "\$*" >>"$root/keys"; [[ -n "\${NEVER_GONE:-}" ]] || touch "\$state" ;;
esac
FAKE
chmod +x "$root/tmux"
export TMUX_BIN="$root/tmux"

: >"$root/keys"
set +e; got="$("$C" --pane %1 2>/dev/null)"; r=$?; set -e
[[ "$got" == MENU && "$r" == 2 && ! -s "$root/keys" ]] && ok "without --handle-menu nothing is sent" || bad "without --handle-menu nothing is sent" "$got rc=$r keys=$(cat "$root/keys")"

set +e; got="$("$C" --pane %1 --handle-menu 2>/dev/null)"; r=$?; set -e
[[ "$got" == EMPTY && "$r" == 0 ]] && ok "--handle-menu answers the menu and reports the recapture" || bad "--handle-menu reports the recapture" "$got rc=$r"
[[ "$(cat "$root/keys")" == "send-keys -t %1 2" ]] && ok "the only key sent is 2, never Enter" || bad "the only key sent is 2" "$(cat "$root/keys")"

rm -f "$root/menu-gone"; : >"$root/keys"
set +e; got="$(NEVER_GONE=1 "$C" --pane %1 --handle-menu 2>/dev/null)"; r=$?; set -e
[[ "$got" == MENU && "$r" == 2 && "$(wc -l <"$root/keys" | tr -d ' ')" == 1 ]] && ok "a menu that stays is reported MENU after one key, no retry" || bad "a stuck menu is reported, one key" "$got rc=$r keys=$(cat "$root/keys")"

if "$C" --file "$root/cap" --handle-menu >/dev/null 2>&1; then bad "--handle-menu with --file is refused"; else ok "--handle-menu with --file is refused"; fi

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
