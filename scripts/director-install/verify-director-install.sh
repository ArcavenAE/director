#!/usr/bin/env bash
# Prove director-install installs the launcher as a versioned, verified copy
# behind an atomic symlink, rolls back, refuses a ref whose verify fails, and
# reports its lag without ever failing. Network-free: the source is a local git
# repo built here from this checkout's own sim/twin files, and every path
# (DIRECTOR_HOME, the manifests dir, the Claude config) is a temp directory.
#
# Usage: scripts/director-install/verify-director-install.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
INSTALLER="${DIRECTOR_INSTALL:-$here/director-install}"
[[ -x "$INSTALLER" ]] || { echo "director-install not executable at $INSTALLER"; exit 2; }

pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
home="$root/home"
out="$root/out"
mkdir -p "$out"

# --- the source repo ----------------------------------------------------------
# Commit A is this checkout's launcher and verify, so the verify step during
# install runs the real verify against the real launcher as installed.
src="$root/src"
g() { git -C "$src" -c user.name=verify -c user.email=verify@example.invalid -c commit.gpgsign=false "$@"; }
mkdir -p "$src/sim/twin"
git init -q -b main "$src"
cp "$repo/sim/twin/cast-launch.sh" "$repo/sim/twin/verify-cast-launch.sh" "$src/sim/twin/"
g add -A && g commit -q -m A
A="$(g rev-parse HEAD)"
echo "# change B" >> "$src/sim/twin/cast-launch.sh"
g commit -q -am B
B="$(g rev-parse HEAD)"
# A branch whose launcher fails its own verify.
g switch -q -c bad
{ echo '#!/usr/bin/env bash'; echo 'exit 3'; } > "$src/sim/twin/cast-launch.sh"
g commit -q -am C
C="$(g rev-parse HEAD)"
g switch -q main

# Run the installer with a controlled environment.
di() {
  env DIRECTOR_HOME="$home" DIRECTOR_SRC_URL="$src" \
      DIRECTOR_MANIFESTS_DIR="$root/manifests" \
      "$INSTALLER" "$@" >"$out/stdout" 2>"$out/stderr"
}
link_target() { readlink "$home/bin/cast-launch" 2>/dev/null || echo "(no link)"; }
want_link() { echo "$home/lib/cast-launch/$1/cast-launch.sh"; }

# --- 1. install at A: versioned dir, stamp, symlink ---------------------------
if di cast-launch --ref "$A"; then
  [[ "$(link_target)" == "$(want_link "$A")" ]] \
    && ok "install at A: the symlink points at A's versioned directory" \
    || bad "install at A: symlink" "$(link_target)"
  stamp="$home/lib/cast-launch/$A/STAMP"
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

# --- 2. the installed bytes are the commit's, not a working tree's ------------
if git -C "$src" show "$A:sim/twin/cast-launch.sh" | cmp -s - "$home/lib/cast-launch/$A/cast-launch.sh"; then
  ok "install at A: the installed launcher is byte-identical to A's commit"
else
  bad "install at A: installed bytes differ from the commit"
fi

# --- 3. the installed copy runs, through the symlink ---------------------------
if CAST_LAUNCH="$home/bin/cast-launch" bash "$home/lib/cast-launch/$A/verify-cast-launch.sh" >"$out/verify" 2>&1; then
  ok "the installed launcher passes verify-cast-launch when run through ~/.director/bin/cast-launch"
else
  bad "installed launcher verify" "$(tail -3 "$out/verify")"
fi

# --- 4. install at origin/main (B): switch, and A stays for rollback ----------
if di cast-launch --ref origin/main; then
  [[ "$(link_target)" == "$(want_link "$B")" ]] \
    && ok "install at origin/main: the symlink moves to B" \
    || bad "install at origin/main: symlink" "$(link_target)"
  [[ -d "$home/lib/cast-launch/$A" ]] \
    && ok "install at origin/main: A's directory remains" \
    || bad "install at origin/main: A's directory was removed"
else
  bad "install at origin/main" "$(cat "$out/stderr")"
fi

# --- 5. the switch leaves no temporary link behind -----------------------------
leftover="$(find "$home/bin" -name '.*' -o -name '*tmp*' | grep -v '^$' || true)"
[[ -z "$leftover" ]] && ok "the atomic switch leaves nothing behind in bin" \
                     || bad "leftover in bin" "$leftover"

# --- 6. --rollback returns to A ------------------------------------------------
if di cast-launch --rollback; then
  [[ "$(link_target)" == "$(want_link "$A")" ]] \
    && ok "--rollback returns the symlink to A" \
    || bad "--rollback: symlink" "$(link_target)"
else
  bad "--rollback" "$(cat "$out/stderr")"
fi

# --- 6b. a re-install after the rollback returns to B ---------------------------
if di cast-launch --ref origin/main; then
  [[ "$(link_target)" == "$(want_link "$B")" ]] \
    && ok "a re-install after --rollback returns the symlink to B" \
    || bad "re-install after rollback: symlink" "$(link_target)"
else
  bad "re-install after rollback" "$(cat "$out/stderr")"
fi
if di cast-launch --rollback && [[ "$(link_target)" == "$(want_link "$A")" ]] \
   && di cast-launch --rollback && [[ "$(link_target)" == "$(want_link "$B")" ]]; then
  ok "--rollback twice toggles between the last two installs"
else
  bad "--rollback twice" "$(link_target)"
fi
di cast-launch --ref "$A" >/dev/null 2>&1 || true  # leave A installed for case 7

# --- 7. a ref whose verify fails changes nothing -------------------------------
before="$(link_target)"
if di cast-launch --ref bad; then
  bad "a ref whose verify fails was installed"
else
  [[ "$(link_target)" == "$before" ]] \
    && ok "a ref whose verify fails leaves the symlink unchanged" \
    || bad "failed verify moved the symlink" "$(link_target)"
  [[ ! -e "$home/lib/cast-launch/$C" ]] \
    && ok "a ref whose verify fails leaves no version directory" \
    || bad "failed verify left $home/lib/cast-launch/$C"
  grep -q "verify" "$out/stderr" \
    && ok "a failed verify is reported by name" \
    || bad "failed verify message" "$(cat "$out/stderr")"
fi

# --- 8. a dirty working tree does not reach the installed bytes ---------------
echo "# uncommitted edit" >> "$src/sim/twin/cast-launch.sh"
if di cast-launch --ref origin/main \
   && git -C "$src" show "$B:sim/twin/cast-launch.sh" | cmp -s - "$home/lib/cast-launch/$B/cast-launch.sh"; then
  ok "a dirty source tree does not change the installed bytes"
else
  bad "dirty tree" "$(cat "$out/stderr")"
fi
git -C "$src" checkout -q -- sim/twin/cast-launch.sh

# --- 9. reinstalling the installed sha is harmless ------------------------------
if di cast-launch --ref "$B" && [[ "$(link_target)" == "$(want_link "$B")" ]]; then
  ok "reinstalling the installed sha succeeds and keeps the link"
else
  bad "reinstall same sha" "$(cat "$out/stderr")"
fi

# --- 10. --status: lag counts only sim/twin/ commits, and exits 0 --------------
if di --status; then
  grep -q "cast-launch.* 0 sim/twin/ commits behind main" "$out/stdout" \
    && ok "--status at the tip reports 0 behind" \
    || bad "--status at tip" "$(cat "$out/stdout")"
  grep -q "^cast-launch: ${B:0:12} " "$out/stdout" \
    && ok "--status names the installed sha" \
    || bad "--status sha" "$(cat "$out/stdout")"
else
  bad "--status exited nonzero at the tip"
fi
echo "readme" > "$src/README"
g add README && g commit -q -m "outside sim/twin"
echo "# more 1" >> "$src/sim/twin/cast-launch.sh"; g commit -q -am D
echo "# more 2" >> "$src/sim/twin/verify-cast-launch.sh"; g commit -q -am E
if di --status; then
  grep -q "cast-launch.* 2 sim/twin/ commits behind main" "$out/stdout" \
    && ok "--status counts the 2 sim/twin/ commits and not the other one" \
    || bad "--status lag count" "$(cat "$out/stdout")"
else
  bad "--status exited nonzero when behind"
fi

# --- 11. --status WARNs on a wrapper that still names a checkout path ----------
mkdir -p "$root/manifests"
printf '#!/usr/bin/env bash\nexec /work/director/sim/twin/cast-launch.sh "$@"\n' > "$root/manifests/cast-old.sh"
printf '#!/usr/bin/env bash\nexec "$HOME/.director/bin/cast-launch" "$@"\n' > "$root/manifests/cast-new.sh"
if di --status; then
  grep -q "WARN.*cast-old.sh" "$out/stdout" \
    && ok "--status WARNs on a wrapper that execs a checkout path" \
    || bad "--status checkout WARN" "$(cat "$out/stdout")"
  grep -q "WARN.*cast-new.sh" "$out/stdout" \
    && bad "--status WARNed on a wrapper that execs the installed launcher" \
    || ok "--status does not WARN on a wrapper that execs the installed launcher"
else
  bad "--status exited nonzero with wrappers present"
fi

# --- 12. --status never fails: nothing installed, no mirror, no source --------
if env DIRECTOR_HOME="$root/empty" DIRECTOR_SRC_URL="$root/nowhere" \
       DIRECTOR_MANIFESTS_DIR="$root/none" "$INSTALLER" --status >"$out/stdout" 2>"$out/stderr"; then
  grep -q "cast-launch: not installed" "$out/stdout" \
    && ok "--status with nothing installed says so and exits 0" \
    || bad "--status empty" "$(cat "$out/stdout")"
else
  bad "--status exited nonzero with nothing installed"
fi

# --- 13. install.sh installs director-install itself ---------------------------
if env CLAUDE_HOME="$root/claude" DIRECTOR_HOME="$root/ihome" DIRECTOR_STATE="$root/istate" \
       bash "$repo/install.sh" >"$out/stdout" 2>"$out/stderr"; then
  [[ "$(readlink "$root/ihome/bin/director-install")" == "$repo/scripts/director-install/director-install" ]] \
    && ok "install.sh installs director-install into \$DIRECTOR_HOME/bin" \
    || bad "install.sh director-install link" "$(readlink "$root/ihome/bin/director-install" || echo none)"
  [[ -L "$root/ihome/bin/board-html" && -L "$root/claude/skills/director" ]] \
    && ok "install.sh still installs the board and the skill" \
    || bad "install.sh existing installs"
else
  bad "install.sh" "$(cat "$out/stderr")"
fi

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
