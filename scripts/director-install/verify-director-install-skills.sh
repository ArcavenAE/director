#!/usr/bin/env bash
# Prove `director-install skills` installs the director and stansfield skills and
# the director command from a commit, the way cast-launch is installed: a versioned
# copy made with git archive, a check run on that copy before anything is switched,
# an atomic switch of one link, a rollback, and a --status line. Seats then run
# merged code instead of a link into a working checkout.
#
# Network-free and server-free: the source is a scratch clone of this checkout,
# so every commit used here is a real, signed commit of this repository's history
# (no commit is made by this script, and none is made unsigned). Every path
# (DIRECTOR_HOME, the manifests dir) is a temp directory.
#
# Usage: scripts/director-install/verify-director-install-skills.sh   Exit nonzero on any miss.
# Needs a full clone: it uses the repository's first commit as the commit that lacks
# the stansfield skill.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
INSTALLER="${DIRECTOR_INSTALL:-$here/director-install}"
[[ -x "$INSTALLER" ]] || { echo "director-install not executable at $INSTALLER"; exit 2; }

pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d "${TMPDIR:-/tmp}/gsk.XXXXXX")"
trap 'rm -rf "$root"' EXIT
home="$root/home"
out="$root/out"
mkdir -p "$out"

# --- the source ---------------------------------------------------------------
# B is the newest commit that touched the skills or the command, A the one before
# it. R is the repository's first commit: it has the director skill and no
# stansfield skill, so the check refuses it.
src="$root/src"
git clone -q --no-hardlinks "$repo" "$src"
B="$(git -C "$src" log -n 1 --format=%H -- skills commands)"
A="$(git -C "$src" log -n 1 --skip 1 --format=%H -- skills commands)"
R="$(git -C "$src" rev-list --max-parents=0 HEAD | tail -n 1)"
[[ -n "$A" && -n "$B" && "$A" != "$B" && -n "$R" ]] || { echo "need a full clone with two commits that touched skills/ or commands/"; exit 2; }
git -C "$src" update-ref refs/heads/main "$B"
git -C "$src" update-ref refs/heads/bad "$R"
for c in "$A" "$B"; do
  git -C "$src" cat-file -e "$c:skills/stansfield/SKILL.md" 2>/dev/null \
    || { echo "commit $c has no stansfield skill; the fixture commits are not what this script expects"; exit 2; }
done
if git -C "$src" cat-file -e "$R:skills/stansfield/SKILL.md" 2>/dev/null; then
  echo "the first commit has a stansfield skill; the failing-check fixture is wrong"; exit 2
fi

di() {
  env DIRECTOR_HOME="$home" DIRECTOR_SRC_URL="$src" \
      DIRECTOR_MANIFESTS_DIR="$root/manifests" \
      "$INSTALLER" "$@" >"$out/stdout" 2>"$out/stderr"
}
link_target() { readlink "$home/skills-current" 2>/dev/null || echo "(no link)"; }
want_link() { echo "$home/lib/skills/$1"; }

# --- 1. install at A: versioned dir, stamp, link ------------------------------
if di skills --ref "$A"; then
  [[ "$(link_target)" == "$(want_link "$A")" ]] \
    && ok "install at A: skills-current points at A's versioned directory" \
    || bad "install at A: link" "$(link_target)"
  stamp="$home/lib/skills/$A/STAMP"
  miss=""
  grep -qx "sha=$A" "$stamp" 2>/dev/null         || miss+=" sha"
  grep -qx "ref=$A" "$stamp" 2>/dev/null         || miss+=" ref"
  grep -qx "source=$src" "$stamp" 2>/dev/null    || miss+=" source"
  grep -qE '^installed_at=[0-9]{4}-[0-9]{2}-[0-9]{2}T' "$stamp" 2>/dev/null || miss+=" installed_at"
  [[ -z "$miss" ]] && ok "install at A: STAMP records sha, ref, source and install time" \
                   || bad "install at A: STAMP" "missing:$miss"
else
  bad "install at A" "$(cat "$out/stderr")"
fi

# --- 2. the installed bytes are the commit's, modes included -------------------
want="$root/want-A"; mkdir -p "$want"
git -C "$src" archive "$A" skills commands | tar -x -C "$want"
if diff -r "$want/skills" "$home/lib/skills/$A/skills" >"$out/d1" 2>&1 \
   && diff -r "$want/commands" "$home/lib/skills/$A/commands" >"$out/d2" 2>&1; then
  ok "install at A: skills/ and commands/ are byte-identical to A's commit"
else
  bad "install at A: installed tree differs from the commit" "$(head -3 "$out/d1" "$out/d2" 2>/dev/null)"
fi
script="skills/director/scripts/dws"
if [[ -x "$home/lib/skills/$A/$script" ]]; then
  ok "install at A: a script keeps its executable bit"
else
  bad "install at A: $script is not executable in the installed copy"
fi
[[ -f "$home/skills-current/skills/stansfield/SKILL.md" && -f "$home/skills-current/commands/director.md" ]] \
  && ok "install at A: both skills and the command are reachable through skills-current" \
  || bad "install at A: skills-current does not reach both skills and the command"

# --- 3. install at origin/main (B): switch, and A stays for rollback ----------
if di skills --ref origin/main; then
  [[ "$(link_target)" == "$(want_link "$B")" ]] \
    && ok "install at origin/main: the link moves to B" \
    || bad "install at origin/main: link" "$(link_target)"
  [[ -d "$home/lib/skills/$A" ]] \
    && ok "install at origin/main: A's directory remains" \
    || bad "install at origin/main: A's directory was removed"
else
  bad "install at origin/main" "$(cat "$out/stderr")"
fi

# --- 4. the default ref is origin/main -----------------------------------------
if di skills && [[ "$(link_target)" == "$(want_link "$B")" ]]; then
  ok "with no --ref the install is at origin/main"
else
  bad "default ref" "$(cat "$out/stderr") $(link_target)"
fi

# --- 5. the switch leaves nothing temporary behind -----------------------------
leftover="$(find "$home" -maxdepth 3 \( -name '.stage.*' -o -name '*.new.*' -o -name '.skills-current.new*' \) | grep -v '^$' || true)"
[[ -L "$home/skills-current" && -z "$leftover" ]] && ok "the atomic switch and the install leave no temporary file behind" \
                     || bad "leftover" "$leftover"

# --- 6. --rollback toggles between the last two installs -----------------------
if di skills --rollback && [[ "$(link_target)" == "$(want_link "$A")" ]] \
   && di skills --rollback && [[ "$(link_target)" == "$(want_link "$B")" ]]; then
  ok "--rollback twice toggles between the last two installs"
else
  bad "--rollback" "$(cat "$out/stderr") $(link_target)"
fi

# --- 7. a commit whose check fails changes nothing ------------------------------
di skills --ref "$A" >/dev/null 2>&1 || true
before="$(link_target)"
if di skills --ref bad; then
  bad "a commit that lacks the stansfield skill was installed"
else
  # The refusal must be the check's own, or the passes below prove nothing.
  grep -q "skills check" "$out/stderr" || bad "the refusal is not the skills check's" "$(head -2 "$out/stderr")"
  [[ "$before" == "$(want_link "$A")" && "$(link_target)" == "$before" ]] \
    && ok "a commit whose check fails leaves the link unchanged" \
    || bad "failed check moved the link" "$(link_target)"
  [[ -d "$home/lib/skills/$A" && ! -e "$home/lib/skills/$R" ]] \
    && ok "a commit whose check fails leaves no version directory" \
    || bad "failed check left $home/lib/skills/$R"
  if [[ -d "$home/lib/skills" ]] && ! ls -A "$home/lib/skills" | grep -q '^\.stage'; then
    ok "a commit whose check fails leaves no stage directory"
  else
    bad "failed check left a stage directory, or lib/skills is missing"
  fi
  grep -q "skills check" "$out/stderr" && grep -q "stansfield" "$out/stderr" \
    && ok "a failed check is reported by name, naming the missing skill" \
    || bad "failed check message" "$(cat "$out/stderr")"
  grep -q "nothing was switched" "$out/stderr" \
    && ok "a failed check says nothing was switched" \
    || bad "failed check does not say nothing was switched" "$(cat "$out/stderr")"
fi

# --- 8. an unknown ref changes nothing ------------------------------------------
before="$(link_target)"
if di skills --ref no-such-ref; then
  bad "an unknown ref was installed"
else
  [[ "$(link_target)" == "$before" ]] && grep -q "cannot resolve ref" "$out/stderr" \
    && ok "an unknown ref is refused and the link is unchanged" \
    || bad "unknown ref" "$(cat "$out/stderr") $(link_target)"
fi

# --- 9. a dirty source tree does not reach the installed bytes ------------------
git -C "$src" checkout -q "$B"
echo "uncommitted edit" >> "$src/skills/director/SKILL.md"
if di skills --ref "$B" && ! grep -q "uncommitted edit" "$home/lib/skills/$B/skills/director/SKILL.md"; then
  ok "a dirty source tree does not change the installed bytes"
else
  bad "dirty tree" "$(cat "$out/stderr")"
fi
git -C "$src" checkout -q -- skills/director/SKILL.md

# --- 10. reinstalling the installed sha is harmless ------------------------------
if di skills --ref "$B" && [[ "$(link_target)" == "$(want_link "$B")" ]] \
   && grep -q "already installed" "$out/stdout"; then
  ok "reinstalling the installed sha succeeds, keeps the link and says so"
else
  bad "reinstall same sha" "$(cat "$out/stdout") $(cat "$out/stderr")"
fi

# --- 11. rollback with nothing to roll back to ----------------------------------
h2="$root/home2"
if env DIRECTOR_HOME="$h2" DIRECTOR_SRC_URL="$src" "$INSTALLER" skills --rollback >"$out/stdout" 2>"$out/stderr"; then
  bad "--rollback with no previous install exited 0"
else
  grep -q "no previous skills install" "$out/stderr" \
    && ok "--rollback with no previous install says so and exits nonzero" \
    || bad "--rollback message" "$(cat "$out/stderr")"
fi

# --- 12. something at the link that is not ours is refused, untouched -----------
h3="$root/home3"; mkdir -p "$h3/skills-current/skills/director"
echo "hand copy" > "$h3/skills-current/skills/director/SKILL.md"
if env DIRECTOR_HOME="$h3" DIRECTOR_SRC_URL="$src" "$INSTALLER" skills --ref "$A" >"$out/stdout" 2>"$out/stderr"; then
  bad "an install over a directory it did not make exited 0"
else
  [[ "$(cat "$h3/skills-current/skills/director/SKILL.md")" == "hand copy" && ! -L "$h3/skills-current" ]] \
    && grep -q "not installed by director-install" "$out/stderr" \
    && ok "a directory at the link that director-install did not make is left untouched" \
    || bad "foreign directory was changed"
  grep -q "not installed by director-install" "$out/stderr" \
    && ok "the refusal says the directory was not installed by director-install" \
    || bad "foreign directory message" "$(cat "$out/stderr")"
fi

# --- 13. --status: lag counts only skills/ and commands/ commits, exits 0 --------
di skills --ref "$A" >/dev/null 2>&1 || true
want_behind="$(git -C "$src" rev-list --count "$A..$B" -- skills commands)"
if di --status; then
  grep -q "^skills: ${A:0:12} " "$out/stdout" \
    && ok "--status names the installed skills sha" \
    || bad "--status sha" "$(cat "$out/stdout")"
  grep -q "skills: .* ${want_behind} skills/ and commands/ commits behind main" "$out/stdout" \
    && ok "--status counts the skills/ and commands/ commits behind main ($want_behind)" \
    || bad "--status lag count (want $want_behind)" "$(cat "$out/stdout")"
else
  bad "--status exited nonzero"
fi

# --- 14. --status never fails: nothing installed, no mirror, no source -----------
if env DIRECTOR_HOME="$root/empty" DIRECTOR_SRC_URL="$root/nowhere" \
       DIRECTOR_MANIFESTS_DIR="$root/none" "$INSTALLER" --status >"$out/stdout" 2>"$out/stderr"; then
  grep -q "skills: not installed" "$out/stdout" \
    && ok "--status with nothing installed says skills are not installed and exits 0" \
    || bad "--status empty" "$(cat "$out/stdout")"
else
  bad "--status exited nonzero with nothing installed"
fi

# --- 15. the help names the verb ------------------------------------------------
if "$INSTALLER" -h 2>&1 | grep -q "director-install skills"; then
  ok "--help names the skills verb"
else
  bad "--help does not name the skills verb"
fi

# --- 16. the cast-launch and director-mcp verbs are untouched by the new one ----
h4="$root/home4"
if env DIRECTOR_HOME="$h4" DIRECTOR_SRC_URL="$src" "$INSTALLER" skills --ref "$A" >/dev/null 2>&1 \
   && [[ ! -e "$h4/bin/cast-launch" && ! -e "$h4/bin/director-mcp" && ! -e "$h4/lib/cast-launch" ]]; then
  ok "a skills install makes no cast-launch or director-mcp link"
else
  bad "a skills install touched the launcher or the shim paths"
fi

echo "---"
echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
