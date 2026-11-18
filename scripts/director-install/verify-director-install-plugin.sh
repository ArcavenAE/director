#!/usr/bin/env bash
# Prove `director-install plugin` puts the director plugin tree, and the loop scripts it runs, inside a per-version
# directory, so a rollback restores the hook with the tree (standing loop S10, sim/design/director-standing-loop.md
# slice row 10, case rollback-restores-standing-hook; director's S10 rulings recorded on aae-orc-lxati). Red first:
# until the plugin component exists every case below fails on an assertion.
#
# The shape, the skills component's: a versioned copy made with git archive under $DIRECTOR_HOME/lib/plugin/<sha>/, a
# check run on the staged copy before anything is switched, an atomic switch of one link,
# $DIRECTOR_HOME/lib/plugin/current, a previous install remembered in lib/plugin/.previous, a --rollback, and a
# --status line. The tree is plugin/director with the link's TARGET copied: scripts/standing-hook is a regular file,
# and so are the loop scripts that must sit beside it to run from inside a version (dws, dbi, director-tick,
# director-profile, and the two helpers dws launches by sibling path, board-html and page-namespace). install.sh's
# copies in $DIRECTOR_HOME/bin sit outside every version and are never what the plugin runs.
#
# Fixtures (director's S10 ruling c): this script makes no commit, signed or not. A is the commit that added
# plugin/director, which must be 74b936b9f0343d5963164e98dbf1c6175cff1b59 (the squash of #689), and B is HEAD of the
# checkout under test. The two-version cases skip, with the reason, when A is HEAD. R is the repository's first
# commit, which has no plugin tree, and is the commit whose check fails. Needs a full clone.
#
# Network-free and server-free: the source is a scratch clone of this checkout; every path (DIRECTOR_HOME, the
# manifests dir, the state dir) is a temp directory. Run it under env -i with a scratch HOME and TMPDIR.
#
# Usage: scripts/director-install/verify-director-install-plugin.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
INSTALLER="${DIRECTOR_INSTALL:-$here/director-install}"
[[ -x "$INSTALLER" ]] || { echo "director-install not executable at $INSTALLER"; exit 2; }
PY="$(command -v python3 || true)"
[[ -n "$PY" ]] || { echo "python3 not on PATH"; exit 2; }

pass=0; fail=0; skipped=0
ok()   { echo "PASS $1"; pass=$((pass+1)); }
bad()  { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }
skip() { echo "SKIP $1"; skipped=$((skipped+1)); }

root="$(cd "$(mktemp -d "${TMPDIR:-/tmp}/gpl.XXXXXX")" && pwd -P)"
trap 'rm -rf "$root"' EXIT
home="$root/home"; out="$root/out"; mkdir -p "$out" "$root/state" "$root/tmp"

# --- the fixtures -------------------------------------------------------------------------------------------------
WANT_A=74b936b9f0343d5963164e98dbf1c6175cff1b59
src="$root/src"
git clone -q --no-hardlinks "$repo" "$src"
A="$(git -C "$src" log --diff-filter=A --format=%H -- plugin/director/hooks/hooks.json | tail -n 1)"
B="$(git -C "$repo" rev-parse HEAD)"
R="$(git -C "$src" rev-list --max-parents=0 HEAD | tail -n 1)"
[[ "$A" == "$WANT_A" ]] || { echo "the commit that added plugin/director is [$A], not the $WANT_A director ruled; the fixture is wrong"; exit 2; }
[[ -n "$B" && -n "$R" ]] || { echo "need a full clone"; exit 2; }
if git -C "$src" cat-file -e "$R:plugin/director/hooks/hooks.json" 2>/dev/null; then echo "the first commit has a plugin tree; the failing-check fixture is wrong"; exit 2; fi
git -C "$src" update-ref refs/heads/main "$B"
git -C "$src" update-ref refs/heads/bad "$R"

di() { # di <args...> : stdout and stderr in $out, DIRECTOR_HOME=$home
  env -i HOME="$root/h" TMPDIR="$root/tmp" PATH="$(dirname "$PY"):/usr/bin:/bin" LANG=C \
      DIRECTOR_HOME="$home" DIRECTOR_SRC_URL="$src" DIRECTOR_MANIFESTS_DIR="$root/manifests" \
      "$INSTALLER" "$@" >"$out/stdout" 2>"$out/stderr"
}
link_target() { readlink "$home/lib/plugin/current" 2>/dev/null || echo "(no link)"; }
want_link() { echo "$home/lib/plugin/$1"; }
blob() { git -C "$src" show "$1:$2" >"$3"; }   # blob <commit> <path> <file>
LOOP="standing-hook dws dbi director-tick director-profile board-html page-namespace"

# a marker where install.sh puts the hook, which no plugin install or rollback may touch or use
mkdir -p "$home/bin"; printf '#!/bin/sh\necho INSTALL-SH-COPY\n' >"$home/bin/standing-hook"; chmod +x "$home/bin/standing-hook"
marker="$(shasum -a 256 "$home/bin/standing-hook")"

# --- 1. install at A: versioned directory, stamp, link ------------------------------------------------------------
if di plugin --ref "$A"; then
  [[ "$(link_target)" == "$(want_link "$A")" ]] \
    && ok "install at A: lib/plugin/current points at A's versioned directory" || bad "install at A: link" "$(link_target)"
  stamp="$home/lib/plugin/$A/STAMP"; miss=""
  grep -qx "sha=$A" "$stamp" 2>/dev/null      || miss+=" sha"
  grep -qx "ref=$A" "$stamp" 2>/dev/null      || miss+=" ref"
  grep -qx "source=$src" "$stamp" 2>/dev/null || miss+=" source"
  grep -qE '^installed_at=[0-9]{4}-[0-9]{2}-[0-9]{2}T' "$stamp" 2>/dev/null || miss+=" installed_at"
  [[ -z "$miss" ]] && ok "install at A: STAMP records sha, ref, source and install time" || bad "install at A: STAMP" "missing:$miss"
else
  bad "install at A" "$(head -c 300 "$out/stderr")"
fi

# --- 2. the tree: A's manifest and hooks file, the link's target copied, nothing a link ---------------------------
V="$home/lib/plugin/$A"
for f in .claude-plugin/plugin.json hooks/hooks.json; do
  blob "$A" "plugin/director/$f" "$root/want"
  cmp -s "$root/want" "$V/$f" 2>/dev/null && ok "install at A: $f is byte-identical to A's" || bad "install at A: $f differs from A's"
done
for s in $LOOP; do
  blob "$A" "skills/director/scripts/$s" "$root/want"
  { [[ -f "$V/scripts/$s" && ! -L "$V/scripts/$s" && -x "$V/scripts/$s" ]] && cmp -s "$root/want" "$V/scripts/$s"; } \
    && ok "install at A: scripts/$s is a regular executable file holding A's bytes" || bad "install at A: scripts/$s" "$(ls -l "$V/scripts/$s" 2>&1 | head -c 160)"
done
[[ -d "$V" && -z "$(find "$V" -type l 2>/dev/null)" ]] && ok "install at A: the version directory holds no symlink (the link's target was copied, not the link)" || bad "install at A: a symlink in the version directory" "$(find "$V" -type l 2>/dev/null | head -3 | tr '\n' ' ')"

# --- 3. the plugin runs from inside the version, and not from bin/ ------------------------------------------------
hook_run() { # hook_run <current-dir> <args...>
  local c="$1"; shift
  env -i HOME="$root/h" TMPDIR="$root/tmp" PATH="$(dirname "$PY"):/usr/bin:/bin" LANG=C CLAUDE_PLUGIN_ROOT="$c" DIRECTOR_STATE="$root/state" \
    "$c/scripts/standing-hook" "$@" 2>"$out/hook.err"
}
rc=0; hook_run "$home/lib/plugin/current" --loop-line >"$out/hook.out" || rc=$?
{ [[ "$rc" == 0 && ! -s "$out/hook.out" ]]; } && ok "the installed hook runs through current: --loop-line prints nothing with no tick" || bad "installed hook --loop-line" "rc=$rc out=[$(head -c 100 "$out/hook.out")]"
rc=0; hook_run "$home/lib/plugin/current" >"$out/hook.out" || rc=$?
{ [[ "$rc" == 0 ]] && grep -q '^drain age: ' "$out/hook.out" && ! grep -q 'dws asks could not run' "$out/hook.out"; } \
  && ok "the installed hook finds dws beside itself inside the version (no 'dws asks could not run')" || bad "installed hook beside dws" "rc=$rc out=[$(grep 'asks' "$out/hook.out" | head -c 200)]"
[[ "$(shasum -a 256 "$home/bin/standing-hook")" == "$marker" ]] && ok "install.sh's copy in bin/ is untouched by the install" || bad "bin/standing-hook was changed"
! cmp -s "$home/bin/standing-hook" "$V/scripts/standing-hook" 2>/dev/null && ok "the plugin's hook is not the bin/ copy" || bad "the plugin's hook is the bin/ copy"

# --- 4. install at B, and rollback-restores-standing-hook ---------------------------------------------------------
if [[ "$A" == "$B" ]]; then
  skip "two-version cases: HEAD is the commit that added the plugin tree (nothing after it), so there is no second version to install or roll back to"
else
  if di plugin --ref origin/main; then
    [[ "$(link_target)" == "$(want_link "$B")" ]] && ok "install at origin/main: current moves to B" || bad "install at origin/main: link" "$(link_target)"
    [[ -d "$home/lib/plugin/$A" ]] && ok "install at origin/main: A's directory remains" || bad "install at origin/main: A's directory was removed"
    [[ "$(cat "$home/lib/plugin/.previous" 2>/dev/null)" == "$A" ]] && ok "install at origin/main: lib/plugin/.previous names A" || bad "lib/plugin/.previous" "$(cat "$home/lib/plugin/.previous" 2>/dev/null || echo missing)"
    blob "$B" skills/director/scripts/standing-hook "$root/wantB"
    { [[ -f "$home/lib/plugin/current/scripts/standing-hook" && ! -L "$home/lib/plugin/current/scripts/standing-hook" ]] && cmp -s "$root/wantB" "$home/lib/plugin/current/scripts/standing-hook"; } \
      && ok "after the install at B, current's standing-hook is B's regular file" || bad "current's standing-hook is not B's"
  else
    bad "install at origin/main" "$(head -c 300 "$out/stderr")"
  fi
  if di plugin --rollback; then
    [[ "$(link_target)" == "$(want_link "$A")" ]] && ok "rollback-restores-standing-hook: --rollback points current at A" || bad "rollback link" "$(link_target)"
    blob "$A" skills/director/scripts/standing-hook "$root/wantA"
    { [[ -f "$home/lib/plugin/current/scripts/standing-hook" && ! -L "$home/lib/plugin/current/scripts/standing-hook" ]] && cmp -s "$root/wantA" "$home/lib/plugin/current/scripts/standing-hook"; } \
      && ok "rollback-restores-standing-hook: after --rollback current's standing-hook is A's regular file, with the tree" || bad "rollback did not restore A's standing-hook"
    [[ "$(shasum -a 256 "$home/bin/standing-hook")" == "$marker" ]] && ok "rollback-restores-standing-hook: bin/standing-hook is neither restored nor touched (scripts sit inside every version)" || bad "bin/standing-hook was changed by the rollback"
  else
    bad "rollback" "$(head -c 300 "$out/stderr")"
  fi
  if di plugin --rollback && [[ "$(link_target)" == "$(want_link "$B")" ]]; then ok "--rollback twice toggles between the last two installs"; else bad "second rollback" "$(head -c 200 "$out/stderr") $(link_target)"; fi
fi
di plugin --ref "$A" >/dev/null 2>&1 || true

# --- 5. no previous install: a rollback is refused ----------------------------------------------------------------
save="$home"; home="$root/home-fresh"; mkdir -p "$home"
rc=0; di plugin --rollback || rc=$?
{ [[ "$rc" != 0 ]] && grep -q 'no previous plugin install' "$out/stderr" && [[ ! -e "$home/lib/plugin/current" ]]; } \
  && ok "--rollback with no previous install is refused and changes nothing" || bad "rollback with no previous" "rc=$rc err=[$(head -c 200 "$out/stderr")]"

# --- 6. a commit whose check fails changes nothing ----------------------------------------------------------------
home="$root/home-check"; mkdir -p "$home"
di plugin --ref "$A" >/dev/null 2>&1 || true
before_t="$(readlink "$home/lib/plugin/current" 2>/dev/null || echo none)"
before_h="$(cd "$home" 2>/dev/null && find . -not -path './src/*' | LC_ALL=C sort | shasum -a 256)"
rc=0; di plugin --ref bad || rc=$?
after_h="$(cd "$home" 2>/dev/null && find . -not -path './src/*' | LC_ALL=C sort | shasum -a 256)"
{ [[ "$rc" != 0 ]] && grep -q 'plugin check failed' "$out/stderr" && grep -q 'nothing was switched' "$out/stderr" && [[ "$before_t" == "$(readlink "$home/lib/plugin/current" 2>/dev/null || echo none)" && "$before_h" == "$after_h" ]]; } \
  && ok "a commit with no plugin tree fails the check, switches nothing and leaves no stage behind" || bad "failed check" "rc=$rc err=[$(head -c 240 "$out/stderr")]"

# --- 7. a current this installer did not make is left exactly as it is ---------------------------------------------
home="$root/home-foreign"; mkdir -p "$home/lib/plugin/current"; printf 'mine\n' >"$home/lib/plugin/current/file"
rc=0; di plugin --ref "$A" || rc=$?
{ [[ "$rc" != 0 ]] && grep -q 'was not installed by director-install' "$out/stderr" && [[ "$(cat "$home/lib/plugin/current/file")" == mine && "$(ls -A "$home/lib/plugin")" == current ]]; } \
  && ok "a directory at current is refused untouched, and nothing else is created" || bad "foreign directory at current" "rc=$rc err=[$(head -c 200 "$out/stderr")]"
home="$root/home-foreign2"; mkdir -p "$home/lib/plugin" "$root/elsewhere"; ln -s "$root/elsewhere" "$home/lib/plugin/current"
rc=0; di plugin --rollback || rc=$?
{ [[ "$rc" != 0 && "$(readlink "$home/lib/plugin/current")" == "$root/elsewhere" ]]; } \
  && ok "a link elsewhere at current is refused untouched by --rollback too" || bad "foreign link at current" "rc=$rc"

# --- 8. --status: a plugin line, not-installed said out loud, the rest unchanged ----------------------------------
mkdir -p "$root/main-installer"; git -C "$src" show "$A:scripts/director-install/director-install" >"$root/main-installer/director-install"; chmod +x "$root/main-installer/director-install"
home="$root/home-status"; mkdir -p "$home"
env -i HOME="$root/h" TMPDIR="$root/tmp" PATH="$(dirname "$PY"):/usr/bin:/bin" LANG=C DIRECTOR_HOME="$home" DIRECTOR_SRC_URL="$root/none" DIRECTOR_MANIFESTS_DIR="$root/none" "$root/main-installer/director-install" --status >"$out/status-main" 2>/dev/null || true
rc=0; env -i HOME="$root/h" TMPDIR="$root/tmp" PATH="$(dirname "$PY"):/usr/bin:/bin" LANG=C DIRECTOR_HOME="$home" DIRECTOR_SRC_URL="$root/none" DIRECTOR_MANIFESTS_DIR="$root/none" "$INSTALLER" --status >"$out/status-new" 2>/dev/null || rc=$?
{ [[ "$rc" == 0 ]] && grep -qxF 'plugin: not installed (install: director-install plugin --ref origin/main)' "$out/status-new"; } \
  && ok "--status with nothing installed says the plugin is not installed, and exits 0" || bad "--status, plugin not installed" "rc=$rc $(grep plugin "$out/status-new" | head -c 200)"
grep -vxF 'plugin: not installed (install: director-install plugin --ref origin/main)' "$out/status-new" >"$out/status-new-rest" || true
cmp -s "$out/status-main" "$out/status-new-rest" && ok "--status: every other line is main's, byte for byte" || bad "--status other lines differ from main's"
{ n_sk="$(grep -n '^skills: ' "$out/status-new" | head -n 1 | cut -d: -f1)"; n_pl="$(grep -n '^plugin: ' "$out/status-new" | head -n 1 | cut -d: -f1)"; [[ -n "$n_sk" && -n "$n_pl" && "$n_pl" == "$((n_sk+1))" ]]; } 2>/dev/null \
  && ok "--status: the plugin line directly follows the skills line" || bad "--status line order"
home="$root/home-status2"; mkdir -p "$home"
di plugin --ref origin/main >/dev/null 2>&1 || true
di --status || true
grep -Eq "^plugin: ${B:0:12} installed .*behind main" "$out/stdout" \
  && ok "--status names the installed plugin sha, its install time and the lag behind main" || bad "--status with the plugin installed" "$(grep plugin "$out/stdout" | head -c 200)"
home="$save"

echo "$pass passed, $fail failed, $skipped skipped"
[[ $fail -eq 0 ]]
