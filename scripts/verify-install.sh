#!/usr/bin/env bash
# Prove install.sh continues past a refusal (P0b, sim/design/board-current-state.md
# section 9): with one plain-file target in a scratch home it names that target,
# installs every other one, and exits nonzero. Also check the skill names the
# renderer by its installed path, so a stale copy elsewhere is never the one run.
#
# Usage: scripts/verify-install.sh   Exit nonzero on any miss. Touches only a temp dir.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT

run_install() { # $1 = scratch home
  CLAUDE_HOME="$1/claude" DIRECTOR_HOME="$1/director" DIRECTOR_STATE="$1/director/state" \
    "$here/install.sh" >"$1/out" 2>&1
}

targets() { # every target install.sh places, relative to the scratch home
  printf '%s\n' claude/skills/director claude/skills/stansfield claude/commands/director.md \
    director/bin/board-html director/bin/dws director/bin/dsi director/bin/dsx director/bin/merge-guard director/bin/director-install
}

# Positive control: a clean home installs everything and exits 0.
clean="$root/clean"; mkdir -p "$clean"
if run_install "$clean"; then ok "clean home exits 0"; else bad "clean home exits 0" "$(cat "$clean/out")"; fi
while read -r t; do
  [[ -L "$clean/$t" ]] && ok "clean home installs $t" || bad "clean home installs $t"
done < <(targets)

# One plain file in the way: the renderer path holds an old regular file.
stale="$root/stale"; mkdir -p "$stale/director/bin"
echo "old renderer" >"$stale/director/bin/board-html"
if run_install "$stale"; then bad "a refusal exits nonzero" "exit 0"; else ok "a refusal exits nonzero"; fi
grep -q "board-html" "$stale/out" && grep -qi "refus" "$stale/out" \
  && ok "the refused target is named" || bad "the refused target is named" "$(cat "$stale/out")"
[[ "$(cat "$stale/director/bin/board-html")" == "old renderer" ]] \
  && ok "the plain file is left untouched" || bad "the plain file is left untouched"
while read -r t; do
  [[ "$t" == director/bin/board-html ]] && continue
  [[ -L "$stale/$t" ]] && ok "past the refusal, installs $t" || bad "past the refusal, installs $t"
done < <(targets)

# The skill runs the renderer by its installed path, never a relative one.
skill="$here/skills/director/SKILL.md"
grep -q '${DIRECTOR_HOME:-$HOME/.director}/bin/board-html' "$skill" \
  && ok "skill names the renderer by its installed path" || bad "skill names the renderer by its installed path"
grep -q 'run `scripts/board-html`' "$skill" \
  && bad "skill still runs a relative scripts/board-html" || ok "skill runs no relative renderer"

# Every script the skill calls is installed, executable, and called from the bin home.
# The list is the skill's: every installed-path call it makes. Each must exist in the checkout as the fallback.
BINHOME='${DIRECTOR_HOME:-$HOME/.director}/bin'
names="$(grep -o -E '\.director\}/bin/[A-Za-z0-9_-]+' "$skill" | sed 's#.*/bin/##' | sort -u | tr '\n' ' ')"
for n in dsi dsx dws merge-guard; do
  case " $names " in *" $n "*) ok "the skill references $n";; *) bad "the skill references $n" "list: $names";; esac
done
for n in $names; do
  [[ -x "$here/skills/director/scripts/$n" || "$n" == board-html ]] && ok "every installed-path call resolves in the checkout: $n" || bad "every installed-path call resolves in the checkout: $n"
done
for n in dsi dsx dws merge-guard; do
  [[ -x "$clean/director/bin/$n" ]] && ok "install puts $n in the bin home, executable" || bad "install puts $n in the bin home, executable"
  grep -qF "$BINHOME/$n" "$skill" && ok "skill calls $n from the bin home" || bad "skill calls $n from the bin home"
  [[ -x "$here/skills/director/scripts/$n" ]] && ok "the fallback scripts/$n exists in the checkout" || bad "the fallback scripts/$n exists in the checkout"
done
grep -qF 'only when the installed copy is missing' "$skill" && ok "skill states the fallback to scripts/ only when the installed copy is missing" || bad "skill states the fallback to scripts/ only when the installed copy is missing"
# A bare "run `scripts/<name>`" call must be gone; scripts/<name> may appear only in the fallback sentence.
bare="$(grep -n -E 'run `scripts/(dsi|dsx|dws|merge-guard)' "$skill" || true)"
[[ -z "$bare" ]] && ok "no bare run of scripts/<name> remains" || bad "no bare run of scripts/<name> remains" "$bare"

# A copy install runs from the bin home with no checkout beside it.
cp_home="$root/copy"; mkdir -p "$cp_home"
CLAUDE_HOME="$cp_home/claude" DIRECTOR_HOME="$cp_home/director" DIRECTOR_STATE="$cp_home/state" "$here/install.sh" --copy >"$cp_home/out" 2>&1 || true
for n in dsi dsx dws merge-guard; do
  [[ -f "$cp_home/director/bin/$n" && ! -L "$cp_home/director/bin/$n" && -x "$cp_home/director/bin/$n" ]] && ok "copy install puts a real executable $n in the bin home" || bad "copy install puts a real executable $n in the bin home"
done
set +e
"$cp_home/director/bin/merge-guard" >"$cp_home/mg" 2>&1; rc=$?
[[ $rc -eq 2 ]] && grep -q "merge-guard" "$cp_home/mg" && ok "installed merge-guard runs from the bin home and prints usage" || bad "installed merge-guard runs from the bin home and prints usage" "rc=$rc"
DIRECTOR_STATE="$cp_home/state" "$cp_home/director/bin/dsx" >"$cp_home/dsx" 2>&1; rc=$?
[[ $rc -eq 0 ]] && grep -q "no watch list" "$cp_home/dsx" && ok "installed dsx runs from the bin home" || bad "installed dsx runs from the bin home" "rc=$rc"
mkdir -p "$cp_home/home"; HOME="$cp_home/home" DIRECTOR_STATE="$cp_home/state" "$cp_home/director/bin/dsi" >"$cp_home/dsi" 2>&1; rc=$?
[[ $rc -eq 0 ]] && ok "installed dsi runs from the bin home" || bad "installed dsi runs from the bin home" "rc=$rc"
set -e

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
