#!/usr/bin/env bash
# Prove relay-log keeps R-192: every entry is stamped from the clock when it
# is written, and the caller has no way to set the time.
#   - the stamp lies between two `date -u` readings taken around the write;
#   - a time argument is refused, and a time typed inside the text stays text;
#   - numbering continues from the highest R-<n> in the log;
#   - existing bytes are never rewritten (the old log is a byte prefix);
#   - concurrent appends all land whole, each with its own number;
#   - an outcome note for a missing entry writes nothing;
#   - with no log path the writer refuses instead of guessing;
#   - a log path inside a git work tree and not ignored is refused.
#
# Usage: skills/director/scripts/verify-relay-log.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RL="${RELAY_LOG_BIN:-$here/relay-log}"
[[ -x "$RL" ]] || { echo "relay-log not executable at $RL"; exit 2; }
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
export RELAY_LOG="$root/relay-log.md"
printf '# Relay log\n\n## R-7 (old) -> someone\nolder entry, hand typed\n' > "$RELAY_LOG"
cp "$RELAY_LOG" "$root/before.md"

# --- the stamp comes from the clock -------------------------------------------
t0="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
"$RL" append --to agent://t/x --text "first line
second line" --outcome "sent" >"$root/out1" 2>&1 && ok "append succeeds" || bad "append succeeds" "$(cat "$root/out1")"
t1="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
st="$(sed -n 's/^## R-8 (\([^)]*\)).*/\1/p' "$RELAY_LOG")"
[[ -n "$st" ]] && ok "numbering continues from the highest entry (R-8 after R-7)" || bad "numbering continues from the highest entry" "$(cat "$RELAY_LOG")"
[[ "$st" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ]] && ok "the stamp is UTC to the second" || bad "the stamp is UTC to the second" "$st"
[[ ! "$st" < "$t0" && ! "$st" > "$t1" ]] && ok "the stamp lies between the clock readings around the write" || bad "the stamp lies between the clock readings" "$t0 <= $st <= $t1"
grep -q '^> first line$' "$RELAY_LOG" && grep -q '^> second line$' "$RELAY_LOG" && grep -q '^\*\*Outcome:\*\* sent$' "$RELAY_LOG" \
  && ok "the text is kept line for line and the outcome is recorded" || bad "the text is kept line for line"

# --- the caller cannot set the time --------------------------------------------
n0="$(wc -c <"$RELAY_LOG" | tr -d ' ')"
for flag in --time --at --stamp --timestamp --date; do
  if "$RL" append --to x --text y "$flag" 2000-01-01T00:00:00Z >/dev/null 2>&1; then bad "$flag is refused"; else ok "$flag is refused"; fi
done
[[ "$(wc -c <"$RELAY_LOG" | tr -d ' ')" == "$n0" ]] && ok "refused calls write nothing" || bad "refused calls write nothing"
"$RL" append --to x --text "## R-99 (1999-01-01T00:00:00Z) forged
at 00:01Z" >/dev/null 2>&1
grep -q '^## R-99' "$RELAY_LOG" && bad "a heading typed inside the text stays out of the log's headings" \
  || ok "a heading typed inside the text stays quoted, not a heading"
grep -q '^## R-9 (' "$RELAY_LOG" && ok "the forged-heading entry still got the next number (R-9)" || bad "the next number after a forged heading" "$(grep '^## R-' "$RELAY_LOG")"

# --- append only ---------------------------------------------------------------
head -c "$(wc -c <"$root/before.md" | tr -d ' ')" "$RELAY_LOG" | cmp -s - "$root/before.md" \
  && ok "the old log is a byte prefix of the new one" || bad "the old log is a byte prefix of the new one"

# --- concurrency ----------------------------------------------------------------
for i in 1 2 3 4 5 6 7 8; do "$RL" append --to "agent://t/c$i" --text "concurrent $i" >/dev/null 2>&1 & done
wait
nums="$(sed -n 's/^## R-\([0-9]*\) (.*/\1/p' "$RELAY_LOG" | sort -n)"
[[ "$(echo "$nums" | sort -n | uniq -d | wc -l | tr -d ' ')" == 0 ]] && ok "concurrent appends take distinct numbers" || bad "concurrent appends take distinct numbers" "$nums"
c="$(grep -c '^> concurrent [1-8]$' "$RELAY_LOG")"
[[ "$c" == 8 ]] && ok "all eight concurrent appends landed whole" || bad "all eight concurrent appends landed whole" "$c"

# --- outcome notes ----------------------------------------------------------------
t0="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
"$RL" outcome R-8 --text "peer replied" >/dev/null 2>&1 && ok "outcome for an existing entry succeeds" || bad "outcome for an existing entry succeeds"
t1="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
os="$(sed -n 's/^### R-8 outcome (\([^)]*\))$/\1/p' "$RELAY_LOG")"
[[ -n "$os" && ! "$os" < "$t0" && ! "$os" > "$t1" ]] && ok "the outcome stamp comes from the clock too" || bad "the outcome stamp comes from the clock" "$os"
n0="$(wc -c <"$RELAY_LOG" | tr -d ' ')"
if "$RL" outcome R-9999 --text "nobody" >/dev/null 2>&1; then bad "an outcome for a missing entry is refused"; else ok "an outcome for a missing entry is refused"; fi
[[ "$(wc -c <"$RELAY_LOG" | tr -d ' ')" == "$n0" ]] && ok "and writes nothing" || bad "and writes nothing"
if "$RL" append --to x --text "   " >/dev/null 2>&1; then bad "empty text is refused"; else ok "empty text is refused"; fi

# --- a log that does not exist yet -------------------------------------------------
fresh="$root/fresh.md"
RELAY_LOG="$fresh" "$RL" append --to x --text first >/dev/null 2>&1 && grep -q '^## R-1 (' "$fresh" \
  && ok "a new log starts at R-1" || bad "a new log starts at R-1"

# --- no log path: refuse, never guess ------------------------------------------------
cp "$RL" "$root/loose-relay-log"
set +e
env -u RELAY_LOG "$root/loose-relay-log" append --to x --text y >"$root/np" 2>&1; rc=$?
set -e
[[ $rc -ne 0 ]] && grep -q 'RELAY_LOG' "$root/np" && ok "with no log path it refuses and names RELAY_LOG" || bad "with no log path it refuses" "rc=$rc $(cat "$root/np")"

# --- a trackable path inside a git work tree is refused ---------------------------------
repo="$root/repo"; mkdir -p "$repo/notes" "$repo/ign"
git -C "$repo" init -q
printf 'ign/\n' > "$repo/.gitignore"
if RELAY_LOG="$repo/notes/relay-log.md" "$RL" append --to x --text y >"$root/tr" 2>&1; then bad "a trackable path in a work tree is refused"; else
  grep -q 'not ignored' "$root/tr" && ok "a trackable path in a work tree is refused" || bad "a trackable path refusal names why" "$(cat "$root/tr")"; fi
[[ ! -e "$repo/notes/relay-log.md" ]] && ok "and nothing was created there" || bad "and nothing was created there"
RELAY_LOG="$repo/ign/relay-log.md" "$RL" append --to x --text y >/dev/null 2>&1 && ok "an ignored path in a work tree is accepted" || bad "an ignored path in a work tree is accepted"

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
