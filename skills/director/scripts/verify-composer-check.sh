#!/usr/bin/env bash
# Prove composer-check refuses an inject into typed text, accepts an empty
# composer or a dim suggestion, and handles the auto-mode menu with "2" only.
# The captures are synthetic, built from the colour codes a `capture-pane -e`
# shows (typed text in the default colour code 39, a suggestion wrapped in the
# dim code 2), and tmux is a fake that records every key sent.
#
# The unmeasured part: whether the key 2 picks "Not now" at once in the real menu
# is not tested here and was not tried on a live pane.
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

# --- review 5523173097: colour sub-parameters are not the dim attribute ------------
check "RGB foreground typed text is HAS-TEXT"     HAS-TEXT 1 "${P} ${E}[38;2;230;230;230mhalf a message"
check "RGB background typed text is HAS-TEXT"     HAS-TEXT 1 "${P} ${E}[48;2;40;40;40m half a message"
check "256-colour 2 typed text is HAS-TEXT"       HAS-TEXT 1 "${P} ${E}[38;5;2mhalf a message"
check "a colon-form colour is HAS-TEXT"           HAS-TEXT 1 "${P} ${E}[38:2::10:20:30mhalf a message"
check "dim plus an RGB colour is still SUGGESTION" SUGGESTION 0 "${P} ${E}[2;38;2;120;120;120mgo ahead${E}[0m"
check "SGR 22 ends dim: text after it is HAS-TEXT" HAS-TEXT 1 "${P} ${E}[2mhint${E}[22m typed"
check "SGR 39 ends dim: text after it is HAS-TEXT" HAS-TEXT 1 "${P} ${E}[2mhint${E}[39m typed"

# --- the composer is the whole region down to the rule ---------------------------------
R="$(for _ in $(seq 1 20); do printf '\xe2\x94\x80'; done)"
check "draft text on a second composer line is HAS-TEXT" HAS-TEXT 1 "${R}
${P} 
  second line of a draft
${R}
  status line"
check "a dim second line is SUGGESTION"           SUGGESTION 0 "${R}
${P} ${E}[2mfirst${E}[0m
  ${E}[2msecond${E}[0m
${R}"
check "a status line below the rule that ends in the mark does not hide a draft" HAS-TEXT 1 "${R}
${P} typed draft
${R}
  model ${P}"
check "a status line ending in the mark does not make an empty composer a draft" EMPTY 0 "${R}
${P} 
${R}
  status ${P}"
check "text below the rule is not the composer"   EMPTY 0 "${R}
${P} 
${R}
  some status text"
check "a transcript echo of an old prompt does not count" EMPTY 0 "${P} old typed message
reply text
${R}
${P} 
${R}"

# --- the menu is its own block, below the composer ---------------------------------------
check "the menu phrase in the transcript with a draft is HAS-TEXT" HAS-TEXT 1 "we discussed Teach auto mode about your environment
${R}
${P} typed draft
${R}"
check "the menu phrase in the transcript with an empty composer is EMPTY" EMPTY 0 "we discussed Teach auto mode about your environment
${R}
${P} 
${R}"
check "a quoted copy of the whole menu above the composer is not the menu" EMPTY 0 "Teach auto mode about your environment?
 1. Yes
 2. Not now
${R}
${P} 
${R}"
check "the title without both options is not the menu, and the stray text refuses" HAS-TEXT 1 "${P} 
Teach auto mode about your environment?
 1. Yes"
check "a draft in the composer wins over a menu below it" HAS-TEXT 1 "${P} typed draft
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

printf '%s \n' "$P" >"$root/cap-empty"
if "$C" --file "$root/cap-empty" --handle-menu >/dev/null 2>&1; then bad "--handle-menu with --file is refused"; else ok "--handle-menu with --file is refused (an empty-composer file would otherwise exit 0)"; fi

# a pane that is not a menu: --handle-menu sends nothing, on every verdict
for kind in empty suggestion typed noprompt typed-with-phrase; do
  case "$kind" in
    empty)      body="$P " ;;
    suggestion) body="$P ${E}[2mgo ahead${E}[0m" ;;
    typed)      body="$P typed draft" ;;
    noprompt)   body="only output" ;;
    typed-with-phrase) body="we discussed Teach auto mode about your environment
${P} typed draft" ;;
  esac
  printf '%s\n' "$body" >"$root/pane-body"
  cat >"$root/tmux2" <<FAKE
#!/usr/bin/env bash
case "\$*" in
  *capture-pane*) cat "$root/pane-body" ;;
  *send-keys*) echo "\$*" >>"$root/keys2" ;;
esac
FAKE
  chmod +x "$root/tmux2"; : >"$root/keys2"
  set +e; TMUX_BIN="$root/tmux2" "$C" --pane %1 --handle-menu >/dev/null 2>&1; set -e
  [[ ! -s "$root/keys2" ]] && ok "--handle-menu sends nothing on a $kind pane" || bad "--handle-menu sends nothing on a $kind pane" "$(cat "$root/keys2")"
done

# a failed capture is a refusal with no verdict, never EMPTY
cat >"$root/tmux3" <<'FAKE'
#!/usr/bin/env bash
case "$*" in *capture-pane*) echo "can't find pane" >&2; exit 1 ;; esac
FAKE
chmod +x "$root/tmux3"
set +e; got="$(TMUX_BIN="$root/tmux3" "$C" --pane %1 2>/dev/null)"; r=$?; set -e
[[ -z "$got" && "$r" -ne 0 ]] && ok "a failed capture exits nonzero with no verdict" || bad "a failed capture exits nonzero with no verdict" "got '$got' rc=$r"

# a failed send-keys is not ignored
cat >"$root/tmux4" <<FAKE
#!/usr/bin/env bash
case "\$*" in
  *capture-pane*) printf '%s\nTeach auto mode about your environment?\n 1. Yes\n 2. Not now\n' "$P" ;;
  *send-keys*) echo "no server" >&2; exit 1 ;;
esac
FAKE
chmod +x "$root/tmux4"
set +e; got="$(TMUX_BIN="$root/tmux4" "$C" --pane %1 --handle-menu 2>/dev/null)"; r=$?; set -e
[[ -z "$got" && "$r" -ne 0 ]] && ok "a failed send-keys exits nonzero with no verdict" || bad "a failed send-keys exits nonzero with no verdict" "got '$got' rc=$r"

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
