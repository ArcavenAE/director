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

RUN20="$(for _ in $(seq 1 20); do printf '\xe2\x94\x80'; done)"
RUN10="$(for _ in $(seq 1 10); do printf '\xe2\x94\x80'; done)"
# A DRAWN rule, as measured from three live panes: ESC[39m ESC[38;5;244m then a run of U+2500.
R="${E}[39m${E}[38;5;244m${RUN20}"
# the same characters with no colour code are what a user could type: never a rule
PR="${RUN20}"
MENU_BLOCK=" 1. Yes
 2. Not now"
MENU_T="Teach auto mode about your environment?"
LABELED="${E}[38;5;244m${RUN10} some-session-name ${RUN10}"   # the drawn top rule carries a label
# framed BODY: the screen shape the check reads, the composer between two rules and a status line below
framed() { printf '%s\n%s\n%s\n  status line' "$LABELED" "$1" "$R"; }

check "an empty composer is EMPTY"            EMPTY      0 "$(framed "${P} ")"
check "an empty composer with a cursor block is EMPTY" EMPTY 0 "$(framed "${P} ${E}[7m ${E}[0m")"
check "a dim suggestion is SUGGESTION"        SUGGESTION 0 "$(framed "${P} ${E}[2mgo ahead with the merge${E}[0m")"
check "a dim suggestion with a cursor on its first letter is SUGGESTION" SUGGESTION 0 "$(framed "${P} ${E}[2m${E}[7mg${E}[27mo ahead${E}[0m")"
check "typed text in the default colour is HAS-TEXT" HAS-TEXT 1 "$(framed "${P} ${E}[39mhalf a message")"
check "typed text with no colour code is HAS-TEXT"   HAS-TEXT 1 "$(framed "${P} half a message")"
check "typed text after a dim span is HAS-TEXT"      HAS-TEXT 1 "$(framed "${P} ${E}[2mhint${E}[0m typed")"
check "an older prompt line in the transcript does not count" EMPTY 0 "${P} ${E}[39mold text
reply text
$(framed "${P} ")"

check "a top rule that carries a label still frames the composer" SUGGESTION 0 "${LABELED}
${P} ${E}[2mgo ahead${E}[0m
${R}
  status"
check "typed text in the label-bearing frame is HAS-TEXT" HAS-TEXT 1 "${LABELED}
${P} typed
${R}"

# --- no frame, or not exactly one mark line in it: refused, never guessed --------------
check "no rules at all is UNCLEAR"            UNCLEAR 3 "just some output"
check "one rule only is UNCLEAR"              UNCLEAR 3 "${R}
${P} "
check "a prompt line with no frame is UNCLEAR" UNCLEAR 3 "${P} "
check "a frame with no mark line is UNCLEAR"  UNCLEAR 3 "$(framed "  nothing here")"
check "a frame with two mark lines is UNCLEAR" UNCLEAR 3 "$(framed "${P} git status
On branch main
${P} ")"

# --- review 5523173097: colour sub-parameters are not the dim attribute ------------
check "RGB foreground typed text is HAS-TEXT"     HAS-TEXT 1 "$(framed "${P} ${E}[38;2;230;230;230mhalf a message")"
check "RGB background typed text is HAS-TEXT"     HAS-TEXT 1 "$(framed "${P} ${E}[48;2;40;40;40m half a message")"
check "256-colour 2 typed text is HAS-TEXT"       HAS-TEXT 1 "$(framed "${P} ${E}[38;5;2mhalf a message")"
check "a colon-form colour is HAS-TEXT"           HAS-TEXT 1 "$(framed "${P} ${E}[38:2::10:20:30mhalf a message")"
check "RGB with channel values of 2 is HAS-TEXT (the unit takes five values)" HAS-TEXT 1 "$(framed "${P} ${E}[38;2;2;2;2mhalf a message")"
check "256-colour on a background of 2 is HAS-TEXT" HAS-TEXT 1 "$(framed "${P} ${E}[48;5;2mhalf a message")"
check "an underline-colour 58 is a unit too"      HAS-TEXT 1 "$(framed "${P} ${E}[58;5;2mhalf a message")"
check "an underline-colour 58 in RGB is a unit"   HAS-TEXT 1 "$(framed "${P} ${E}[58;2;2;2;2mhalf a message")"
check "dim plus an RGB colour is still SUGGESTION" SUGGESTION 0 "$(framed "${P} ${E}[2;38;2;120;120;120mgo ahead${E}[0m")"
check "SGR 22 ends dim: text after it is HAS-TEXT" HAS-TEXT 1 "$(framed "${P} ${E}[2mhint${E}[22m typed")"
check "SGR 39 ends dim: text after it is HAS-TEXT" HAS-TEXT 1 "$(framed "${P} ${E}[2mhint${E}[39m typed")"

# --- the composer is everything between the frame's two rules ---------------------------
check "draft text on a second composer line is HAS-TEXT" HAS-TEXT 1 "$(framed "${P} 
  second line of a draft")"
check "a dim second line is SUGGESTION"           SUGGESTION 0 "$(framed "${P} ${E}[2mfirst${E}[0m
  ${E}[2msecond${E}[0m")"
check "a status line that is only the mark does not hide a draft" HAS-TEXT 1 "${R}
${P} typed draft
${R}
${P} "
check "a status line ending in the mark does not make an empty composer a draft" EMPTY 0 "${R}
${P} 
${R}
  status ${P}"
check "a status line that is only the mark does not make an empty composer a draft" EMPTY 0 "${R}
${P} 
${R}
${P}"
check "a pasted shell transcript ending at a fresh prompt is refused" UNCLEAR 3 "$(framed "${P} git status
On branch main
${P} ")"
check "dashes inside a draft whose first line is empty are draft text" HAS-TEXT 1 "$(framed "${P} 
-----
  draft after a rule")"
check "a typed box rule inside a draft whose first line is empty is draft text" HAS-TEXT 1 "$(framed "${P} 
${PR}
  draft after a rule")"
check "a typed box rule inside a draft after typed text is draft text" HAS-TEXT 1 "$(framed "${P} typed
${PR}
more draft")"

# --- review 5523734158: a drawn rule carries the measured colour, a typed one cannot ------
# the five shapes that read EMPTY while only two rules are on screen
check "(1) a typed dashed line then an empty mark line is refused" UNCLEAR 3 "$(framed "${P} git log
----------
${P} ")"
check "(2) a typed labelled dashed line then an empty mark line is refused" UNCLEAR 3 "$(framed "${P} draft
----- note -
${P} ")"
check "(3) a typed box rule then an empty mark line is refused" UNCLEAR 3 "$(framed "${P} draft
${PR}
${P} ")"
check "(4) a scrolled-off mark above a typed rule is refused" UNCLEAR 3 "On branch main
----------
${P} 
${R}"
check "(4b) the same with a typed box rule" UNCLEAR 3 "On branch main
${PR}
${P} 
${R}"
check "(5) a typed dashed rule as the first visible line is refused" UNCLEAR 3 "-----
${P} typed
${R}"
check "(5b) a typed box rule as the first visible line is refused" UNCLEAR 3 "${PR}
${P} typed
${R}"
check "(5c) a typed labelled rule as the first visible line is refused" UNCLEAR 3 "${RUN10} note ${RUN10}
${P} typed
${R}"

# a correctly drawn rule in another theme colour is not a rule here: the frame is missing, so refuse
OTHER_TOP="${E}[38;5;240m${RUN10} some-session-name ${RUN10}"
OTHER_BOT="${E}[39m${E}[38;5;240m${RUN20}"
check "rules in another theme colour: UNCLEAR, never a verdict" UNCLEAR 3 "${OTHER_TOP}
${P} 
${OTHER_BOT}
  status line"
check "rules in another theme colour holding typed text: UNCLEAR" UNCLEAR 3 "${OTHER_TOP}
${P} typed
${OTHER_BOT}"
check "uncoloured box-drawing rules are not drawn rules" UNCLEAR 3 "${PR}
${P} 
${PR}"
check "coloured ASCII dashes are not a rule" UNCLEAR 3 "${E}[38;5;244m--------------------
${P} 
${E}[39m${E}[38;5;244m--------------------"
check "the colour code must start the line: one mid-line is not a rule" UNCLEAR 3 "x ${R}
${P} 
${R}"
check "a drawn top rule without the leading reset still frames" EMPTY 0 "${E}[38;5;244m${RUN20}
${P} 
${R}"
check "a transcript rule above an echoed mark line and a real frame is refused (second guard, fails safe)" UNCLEAR 3 "${R}
${P} an echoed message
$(framed "${P} ")"
check "a transcript rule with no mark line before the frame is fine" EMPTY 0 "${R}
some assistant text
$(framed "${P} ")"

# --- what counts as a rule is pinned ----------------------------------------------------
C244="${E}[38;5;244m"
check "a label with no trailing run is not a rule" UNCLEAR 3 "${C244}${RUN10} some-label
${P} typed
${R}"
check "four box characters are not a rule"         UNCLEAR 3 "${C244}$(for _ in 1 2 3 4; do printf '\xe2\x94\x80'; done)
${P} typed
${R}"
check "five box characters are a rule"             HAS-TEXT 1 "${C244}$(for _ in 1 2 3 4 5; do printf '\xe2\x94\x80'; done)
${P} typed
${R}"
check "text before the run is not a rule (colour, then text, then the run)" UNCLEAR 3 "${C244}x ${RUN20}
${P} typed
${R}"
check "leading spaces before the run are not a rule" UNCLEAR 3 "${C244} ${RUN20}
${P} typed
${R}"
check "a label must be set off by a space from the first run" UNCLEAR 3 "${C244}${RUN10}name ${RUN10}
${P} typed
${R}"
check "a draft line that only ends in dashes is draft text" HAS-TEXT 1 "$(framed "${P} note ----------")"

# --- the menu is its own block, below the bottom rule -------------------------------------
check "the auto-mode menu below the composer is MENU" MENU 2 "$(framed "${P} ")
${MENU_T}
${MENU_BLOCK}"
check "the menu with a cursor mark on its first option is MENU" MENU 2 "${R}
${P} 
${R}
${MENU_T}
 ${P} 1. Yes
 2. Not now"
check "the menu phrase in the transcript with a draft is HAS-TEXT" HAS-TEXT 1 "we discussed Teach auto mode about your environment
$(framed "${P} typed draft")"
check "the menu phrase in the transcript with an empty composer is EMPTY" EMPTY 0 "we discussed Teach auto mode about your environment
$(framed "${P} ")"
check "a quoted copy of the whole menu above the composer is not the menu" EMPTY 0 "${MENU_T}
${MENU_BLOCK}
$(framed "${P} ")"
check "the title without both options is not the menu" EMPTY 0 "$(framed "${P} ")
${MENU_T}
 1. Yes"
check "options in the wrong order are not the menu" EMPTY 0 "$(framed "${P} ")
${MENU_T}
 2. Not now
 1. Yes"
check "options beyond the span are not the menu"   EMPTY 0 "$(framed "${P} ")
${MENU_T}
a
b
c
d
e
f
 1. Yes
 2. Not now"
check "dim menu-looking text inside the frame is not the menu" SUGGESTION 0 "$(framed "${P} 
${E}[2m${MENU_T}${E}[0m
${E}[2m 1. Yes${E}[0m
${E}[2m 2. Not now${E}[0m")"
check "a mark in the middle of a draft line is draft text, not a second mark line" HAS-TEXT 1 "$(framed "${P} 
  pasted text with ${P} in it")"
check "a draft in the composer wins over a menu below it" HAS-TEXT 1 "$(framed "${P} typed draft")
${MENU_T}
${MENU_BLOCK}"
check "menu text typed into the draft is HAS-TEXT, not MENU" HAS-TEXT 1 "${R}
${P} 
${MENU_T}
${MENU_BLOCK}
${R}"
check "menu text typed into the draft, with a typed rule before the options, is HAS-TEXT not MENU" HAS-TEXT 1 "$(framed "${P} 
${MENU_T}
${PR}
${MENU_BLOCK}")"

# --- the menu handler: a fake tmux that records keys -----------------------------------------
MENU_PANE="$(framed "${P} ")
${MENU_T}
${MENU_BLOCK}"
EMPTY_PANE="$(framed "${P} ")"
printf '%s\n' "$MENU_PANE" >"$root/pane-menu"; printf '%s\n' "$EMPTY_PANE" >"$root/pane-empty"
cat >"$root/tmux" <<FAKE
#!/usr/bin/env bash
state="$root/menu-gone"
case "\$*" in
  *capture-pane*) if [[ -e "\$state" ]]; then cat "$root/pane-empty"; else cat "$root/pane-menu"; fi ;;
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

printf '%s\n' "$EMPTY_PANE" >"$root/cap-empty"
if "$C" --file "$root/cap-empty" --handle-menu >/dev/null 2>&1; then bad "--handle-menu with --file is refused"; else ok "--handle-menu with --file is refused (an empty-composer file would otherwise exit 0)"; fi

# a pane that is not a menu: --handle-menu sends nothing, on every verdict
for kind in empty suggestion typed unclear typed-with-phrase typed-menu; do
  case "$kind" in
    empty)      body="$EMPTY_PANE" ;;
    suggestion) body="$(framed "${P} ${E}[2mgo ahead${E}[0m")" ;;
    typed)      body="$(framed "${P} typed draft")" ;;
    unclear)    body="only output" ;;
    typed-with-phrase) body="we discussed ${MENU_T}
$(framed "${P} typed draft")" ;;
    typed-menu) body="${R}
${P} 
${MENU_T}
${MENU_BLOCK}
${R}" ;;
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
  *capture-pane*) cat "$root/pane-menu" ;;
  *send-keys*) echo "no server" >&2; exit 1 ;;
esac
FAKE
chmod +x "$root/tmux4"
set +e; got="$(TMUX_BIN="$root/tmux4" "$C" --pane %1 --handle-menu 2>/dev/null)"; r=$?; set -e
[[ -z "$got" && "$r" -ne 0 ]] && ok "a failed send-keys exits nonzero with no verdict" || bad "a failed send-keys exits nonzero with no verdict" "got '$got' rc=$r"

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
