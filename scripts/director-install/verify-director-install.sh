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

# --- 14. director-mcp: build at a commit, check, switch, roll back -----------
# The source is a scratch repo holding this checkout's shim source and recipe,
# so the real recipe builds the real shim. Only --version is run, and it exits before any connection.
msrc="$root/msrc"; mhome="$root/mhome"
mg() { git -C "$msrc" -c user.name=verify -c user.email=verify@example.invalid -c commit.gpgsign=false "$@"; }
mkdir -p "$msrc/cmd" "$msrc/scripts"
git init -q -b main "$msrc"
cp -R "$repo/cmd/director-mcp" "$msrc/cmd/director-mcp"
cp "$repo/scripts/build-director-mcp" "$msrc/scripts/build-director-mcp"
mg add -A && mg commit -q -m M1
M1="$(mg rev-parse HEAD)"
echo "// m2" >> "$msrc/cmd/director-mcp/main.go"
mg commit -q -am M2
M2="$(mg rev-parse HEAD)"
# A recipe that stamps the wrong commit, and a commit with no recipe at all.
mg switch -q -c mismatch
sed -i.bak 's/-X main.revision=\$sha/-X main.revision=0000000000000000000000000000000000000000/' "$msrc/scripts/build-director-mcp"
rm -f "$msrc/scripts/build-director-mcp.bak"
mg commit -q --allow-empty -am Mis
MIS="$(mg rev-parse HEAD)"
mg switch -q main; mg switch -q -c norecipe
mg rm -q scripts/build-director-mcp; mg commit -q -m NoRecipe
NOR="$(mg rev-parse HEAD)"
mg switch -q main
BLOB="$(mg rev-parse "$M1:cmd/director-mcp/go.mod")"

dm() {
  env DIRECTOR_HOME="$mhome" DIRECTOR_SRC_URL="$msrc" DIRECTOR_MANIFESTS_DIR="$root/manifests" \
      "$INSTALLER" "$@" >"$out/stdout" 2>"$out/stderr"
}
mlink() { readlink "$mhome/bin/director-mcp" 2>/dev/null || echo none; }

# A plain file already at the link is kept, not overwritten.
mkdir -p "$mhome/bin"; printf 'hand-installed build\n' > "$mhome/bin/director-mcp"; chmod +x "$mhome/bin/director-mcp"
if dm director-mcp --ref "$M1"; then
  [[ "$(mlink)" == "$mhome/lib/director-mcp/$M1/director-mcp" ]] \
    && ok "director-mcp --ref installs at the commit and points the link at it" \
    || bad "director-mcp link" "$(mlink)"
  legacy="$(cat "$mhome/lib/director-mcp/.previous" 2>/dev/null || true)"
  [[ "$legacy" == legacy-* && "$(cat "$mhome/lib/director-mcp/$legacy/director-mcp")" == "hand-installed build" ]] \
    && ok "a hand-installed file at the link is kept as the previous version" \
    || bad "legacy kept" "previous=$legacy"
  grep -q "^sha=$M1\$" "$mhome/lib/director-mcp/$M1/STAMP" && grep -q "^version=0.1.0-alpha\." "$mhome/lib/director-mcp/$M1/STAMP" \
    && ok "the STAMP records the sha and the commit-derived version" || bad "STAMP" "$(cat "$mhome/lib/director-mcp/$M1/STAMP")"
  vline="$(env -i PATH="$PATH" "$mhome/lib/director-mcp/$M1/director-mcp" --version </dev/null 2>&1)"
  [[ "$vline" == "director-mcp 0.1.0-alpha."*".${M1:0:7} rev $M1 channel alpha" ]] \
    && ok "the installed shim's --version names the commit" || bad "--version line" "$vline"
  go version -m "$mhome/lib/director-mcp/$M1/director-mcp" | grep -q 'vcs\.' \
    && bad "the installed shim carries vcs.* build settings" || ok "the installed shim carries no vcs.* settings"
else
  bad "director-mcp --ref $M1 exited nonzero" "$(cat "$out/stderr")"
fi

# The same commit builds to the same bytes.
if bash "$repo/scripts/build-director-mcp" --git-dir "$msrc" --sha "$M1" --out "$root/again/director-mcp" >/dev/null 2>&1; then
  cmp -s "$root/again/director-mcp" "$mhome/lib/director-mcp/$M1/director-mcp" \
    && ok "a second build of the same commit is byte-identical" || bad "the rebuild differs from the installed binary"
else
  bad "the recipe failed on a second build"
fi

# A second commit switches; the first is kept and is the previous.
if dm director-mcp --ref "$M2"; then
  [[ "$(mlink)" == "$mhome/lib/director-mcp/$M2/director-mcp" && -x "$mhome/lib/director-mcp/$M1/director-mcp" \
     && "$(cat "$mhome/lib/director-mcp/.previous")" == "$M1" ]] \
    && ok "a second ref switches the link and keeps the first version as previous" \
    || bad "second install" "$(mlink)"
else
  bad "director-mcp --ref $M2 exited nonzero" "$(cat "$out/stderr")"
fi
dm director-mcp --ref "$M2" && grep -q "already installed" "$out/stdout" \
  && ok "reinstalling the same commit builds nothing and says so" || bad "reinstall same commit" "$(cat "$out/stdout")"

# Rollback returns to the first, and a second rollback returns to the second.
if dm director-mcp --rollback; then
  [[ "$(mlink)" == "$mhome/lib/director-mcp/$M1/director-mcp" && "$(cat "$mhome/lib/director-mcp/.previous")" == "$M2" ]] \
    && ok "--rollback switches back to the previous version" || bad "rollback" "$(mlink)"
else
  bad "director-mcp --rollback exited nonzero"
fi

# Refusals leave the live link where it is.
live="$(mlink)"
for r in "$BLOB" "no-such-ref-xyz" "$NOR" "$MIS"; do
  if dm director-mcp --ref "$r"; then
    bad "director-mcp --ref $r was accepted"
  else
    [[ "$(mlink)" == "$live" ]] && ok "a refused ref leaves the old binary in place: ${r:0:12}" || bad "link moved for ${r:0:12}" "$(mlink)"
  fi
done
dm director-mcp --ref "$BLOB" || { grep -q "cannot resolve ref" "$out/stderr" && ok "a blob id is refused as not a commit" || bad "blob refusal text" "$(cat "$out/stderr")"; }
dm director-mcp --ref "$NOR" || { grep -q "no scripts/build-director-mcp" "$out/stderr" && ok "a commit with no recipe is refused by name" || bad "no recipe text" "$(cat "$out/stderr")"; }
dm director-mcp --ref "$MIS" || { grep -q "does not report rev $MIS" "$out/stderr" && ok "a build that does not match the ref is refused by name" || bad "mismatch text" "$(cat "$out/stderr")"; }
[[ ! -d "$mhome/lib/director-mcp/$MIS" && ! -d "$mhome/lib/director-mcp/$NOR" ]] \
  && ok "a refused build leaves no installed directory" || bad "refused build left a directory"
ls "$mhome/lib/director-mcp" | grep -q '^\.stage\.' && bad "a stage directory was left behind" || ok "no stage directory is left behind"

# Rollback with nothing to return to.
if env DIRECTOR_HOME="$root/mempty" DIRECTOR_SRC_URL="$msrc" "$INSTALLER" director-mcp --rollback >"$out/stdout" 2>"$out/stderr"; then
  bad "--rollback with no previous install exited 0"
else
  grep -q "no previous director-mcp install" "$out/stderr" && ok "--rollback with no previous install refuses" || bad "rollback empty text"
fi

# --status names the shim and WARNs on a binary it did not install.
if dm --status; then
  grep -q "^director-mcp: ${M1:0:12} (0.1.0-alpha" "$out/stdout" \
    && ok "--status names the installed shim and its version" || bad "status shim row" "$(cat "$out/stdout")"
fi
mkdir -p "$root/mfree/bin"; printf 'x\n' > "$root/mfree/bin/director-mcp"
env DIRECTOR_HOME="$root/mfree" DIRECTOR_SRC_URL="$root/nowhere" DIRECTOR_MANIFESTS_DIR="$root/none" "$INSTALLER" --status >"$out/stdout" 2>&1
grep -q "WARN: .*director-mcp was not installed by director-install" "$out/stdout" \
  && ok "--status WARNs on a shim it did not install" || bad "status foreign shim" "$(cat "$out/stdout")"

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
