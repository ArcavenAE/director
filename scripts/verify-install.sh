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
    director/bin/board-html director/bin/director-install
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

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
