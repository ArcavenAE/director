#!/usr/bin/env bash
# Prove director-init (design: sim/design/director-bootstrap.md section 4a.2;
# first build, aae-orc-d2sol and director's rulings of 2026-11-12 recorded on
# that ticket). It writes an operating profile, reports what shadows the
# launcher, and with --check reports drift without writing.
#
# Broker-free and fleet-free: marvel and sideshow are stubs on PATH, nothing
# connects, and every run is env -i with a scratch HOME, so nothing under the
# real ~/.director, ~/.zshrc or ~/.claude.json is read or written.
#
# Covered:
#  - writing: the minimal profile when nothing is given, detection through the
#    marvel and sideshow stubs, --entry-role and repeatable --team GLOB=ROLE,
#    the prompt on a terminal, an existing profile (refused without a yes, a
#    backup kept), an atomic write, the summary printed, --manifests refused as
#    not in the first build;
#  - the shadow scan, which reports and edits nothing: a function, an alias, a
#    PATH entry, and the wrapper the operator keeps (reported as "wraps", not
#    drift);
#  - --check: clean on a sound state, one drift line per fault, exit 1 on
#    drift, and nothing written;
#  - the python rule: python3 older than 3.11 is refused with one line.
#
# Usage: skills/director/scripts/verify-director-init.sh   Exit nonzero on any miss.
#        DIRECTOR_INIT_BIN=<path> to test another copy.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN="${DIRECTOR_INIT_BIN:-$here/director-init}"
PROFILE_BIN="$here/director-profile"
[[ -x "$BIN" ]] || { echo "director-init not executable at $BIN"; exit 2; }
[[ -x "$PROFILE_BIN" ]] || { echo "director-profile not executable at $PROFILE_BIN"; exit 2; }
PY="$(command -v python3 || true)"
[[ -n "$PY" ]] || { echo "python3 not on PATH"; exit 2; }
"$PY" -c 'import sys; sys.exit(0 if sys.version_info >= (3, 11) else 1)' || { echo "python3 3.11 or newer is needed to run this check"; exit 2; }

work="$(cd "$(mktemp -d "${TMPDIR:-/tmp}/director-init-verify.XXXXXX")" && pwd -P)"
trap 'rm -rf "$work"' EXIT
pydir="$work/pybin"; mkdir -p "$pydir"; ln -s "$PY" "$pydir/python3"   # only python3, so no real marvel or sideshow is on the test PATH
fail=0
miss() { echo "MISS: $*"; fail=1; }

# fresh: a new scratch HOME with the installed bin home (the launcher and
# director-profile), an empty state directory, and a bin of stubs.
fresh() {
  rm -rf "$work/h" "$work/stubs" "$work/tmp"
  home="$work/h"; state="$home/.director/state"; dbin="$home/.director/bin"; stubs="$work/stubs"
  mkdir -p "$state" "$dbin" "$stubs" "$work/tmp"
  printf '#!/bin/sh\nexit 0\n' >"$dbin/director-session"; chmod +x "$dbin/director-session"
  cp "$PROFILE_BIN" "$dbin/director-profile"; chmod +x "$dbin/director-profile"
  prof="$state/operating-profile.toml"
}
stub_marvel() { printf '%s\n' '#!/bin/sh' 'echo cluster-a' 'echo cluster-b' >"$stubs/marvel"; chmod +x "$stubs/marvel"; }
stub_sideshow() { printf '%s\n' '#!/bin/sh' 'echo my-roles 0.2.0' 'echo bmad 6.12.4' >"$stubs/sideshow"; chmod +x "$stubs/sideshow"; }

# run [env...] -- args...: env -i, scratch HOME, no terminal; sets rc and out.
run() {
  local envs=()
  while [[ $# -gt 0 && "$1" != "--" ]]; do envs+=("$1"); shift; done
  shift
  set +e
  out="$(env -i HOME="$home" TMPDIR="$work/tmp" PATH="$stubs:$pydir:/usr/bin:/bin" ${envs[@]+"${envs[@]}"} "$BIN" "$@" 2>&1 </dev/null)"
  rc=$?
  set -e
}

# runtty <expect=>answer|...> -- args...: the same on a pty, answering each
# prompt in order. The pty is driven by pty.fork and a read loop that ends on
# EOF or EIO (pty.spawn blocks forever on macOS's stock python3 3.9 at EOF).
# Sets rc and out.
runtty() {
  local prompts="$1"; shift; shift
  local code='
import os, pty, select, sys, time
prompts = [p.split("=>", 1) for p in os.environ.pop("PROMPTS").split("|") if p]
env = {k: os.environ[k] for k in ("HOME", "TMPDIR", "PATH")}
pid, fd = pty.fork()
if pid == 0:
    os.execve(sys.argv[1], sys.argv[1:], env)
buf = b""; seen = 0; end = time.time() + 30
while time.time() < end:
    r, _, _ = select.select([fd], [], [], 1)
    if r:
        try:
            chunk = os.read(fd, 4096)
        except OSError:
            break
        if not chunk:
            break
        buf += chunk
        if prompts and prompts[0][0].encode() in buf[seen:]:
            os.write(fd, (prompts[0][1] + "\n").encode()); seen = len(buf); prompts.pop(0)
_, status = os.waitpid(pid, 0)
sys.stdout.write(buf.decode("utf-8", "replace").replace("\r", "") + "\nRC=%d\n" % (os.WEXITSTATUS(status) if os.WIFEXITED(status) else 99))
'
  set +e
  out="$(PROMPTS="$prompts" HOME="$home" TMPDIR="$work/tmp" PATH="$stubs:$pydir:/usr/bin:/bin" "$PY" -c "$code" "$BIN" "$@" 2>&1)"
  set -e
  rc="${out##*RC=}"; rc="${rc%%[!0-9]*}"
  out="${out%RC=*}"
}

hash_tree() { find "$home" -type f -exec shasum -a 256 {} + | LC_ALL=C sort | shasum -a 256 | cut -d' ' -f1; }
check_profile() { env -i HOME="$home" PATH="$pydir:/usr/bin:/bin" "$PROFILE_BIN" check "$1" 2>&1; }

# ---- I1: nothing given, nothing detected: the minimal profile, said plainly ----
fresh
run --
[[ $rc -eq 0 && -f "$prof" ]] || miss "I1 minimal profile not written: rc=$rc out=$out"
[[ "$out" == *"routing into a team is refused until an entry role is named"* ]] || miss "I1 must say what the minimal profile means: $out"
check_profile "$prof" | grep -qs 'profile ok' || miss "I1 the written profile must pass director-profile check"
grep -qs '^\[\[team\]\]' "$prof" && miss "I1 the minimal profile has no [[team]]"
grep -qs 'fleet_sources = \["bus-presence"\]' "$prof" || miss "I1 minimal fleet_sources: $(cat "$prof" 2>/dev/null)"

# ---- I2: detection through the stubs; sideshow is printed, never written ----
fresh; stub_marvel; stub_sideshow
run -- --entry-role supervisor
[[ $rc -eq 0 && "$out" == *"marvel: found"* && "$out" == *"my-roles 0.2.0"* && "$out" == *"bmad 6.12.4"* ]] || miss "I2 detection output: rc=$rc out=$out"
grep -qs 'fleet_sources = \["marvel-register", "bus-presence"\]' "$prof" || miss "I2 marvel found sets marvel-register: $(cat "$prof" 2>/dev/null)"
grep -qs -E 'my-roles|bmad' "$prof" && miss "I2 sideshow packs must not be written to the profile"
fresh
run -- --entry-role supervisor
[[ "$out" == *"marvel: not found"* && "$out" == *"sideshow: not found"* ]] || miss "I2 absent tools must be reported: $out"

# ---- I3: entry roles by flags ----
fresh
run -- --entry-role supervisor
grep -qs 'match = "\*"' "$prof" && grep -qs 'entry_role = "supervisor"' "$prof" || miss "I3 --entry-role: $(cat "$prof" 2>/dev/null)"
fresh
run -- --team 'aae/res*=lead' --team '*=sup'
[[ $rc -eq 0 ]] && [[ "$(env -i HOME="$home" PATH="$pydir:/usr/bin:/bin" "$PROFILE_BIN" entry "$prof" aae/research 2>&1)" == "lead" ]] && [[ "$(env -i HOME="$home" PATH="$pydir:/usr/bin:/bin" "$PROFILE_BIN" entry "$prof" x/y 2>&1)" == "sup" ]] || miss "I3 two --team flags in order: rc=$rc $(cat "$prof" 2>/dev/null)"
fresh
run -- --team 'no-equals-sign'
[[ $rc -eq 2 && "$out" == *"GLOB=ROLE"* && ! -e "$prof" ]] || miss "I3 bad --team: rc=$rc out=$out"
run -- --entry-role ''
[[ $rc -eq 2 && ! -e "$prof" ]] || miss "I3 empty role: rc=$rc out=$out"
fresh
run -- --entry-role 'a"b'
[[ "$(env -i HOME="$home" PATH="$pydir:/usr/bin:/bin" "$PROFILE_BIN" entry "$prof" w/t 2>&1)" == 'a"b' ]] || miss "I8 a quote in a role must be escaped: $(cat "$prof" 2>/dev/null)"

# ---- I3t: the prompt, on a terminal ----
fresh
runtty "entry role=>supervisor" --
[[ "$rc" == 0 && "$(grep -c 'entry role' <<<"$out")" -ge 1 ]] || miss "I3t prompt: rc=$rc out=$out"
grep -qs 'entry_role = "supervisor"' "$prof" || miss "I3t the answer must be written: $(cat "$prof" 2>&1)"
fresh
runtty "entry role=>" --
[[ "$rc" == 0 ]] && ! grep -qs '^\[\[team\]\]' "$prof" || miss "I3t a blank answer gives the minimal profile: rc=$rc $(cat "$prof" 2>&1)"

# ---- I4: an existing profile ----
fresh
printf 'schema_version = 1\nname = "mine"\n' >"$prof"; before="$(shasum -a 256 <"$prof")"
run -- --entry-role supervisor
[[ $rc -eq 1 && "$out" == *"already exists"* && "$out" == *"--yes"* ]] || miss "I4 existing profile without yes: rc=$rc out=$out"
[[ "$(shasum -a 256 <"$prof")" == "$before" ]] || miss "I4 the existing profile must be unchanged"
ls "$state" | grep -qs '\.bak-' && miss "I4 no backup without a write"
run -- --entry-role supervisor --yes
[[ $rc -eq 0 ]] && grep -qs 'entry_role = "supervisor"' "$prof" || miss "I4 --yes must replace: rc=$rc out=$out"
bak="$(ls "$state"/operating-profile.toml.bak-* 2>/dev/null | head -1 || true)"
[[ -n "$bak" && "$(shasum -a 256 <"$bak")" == "$before" ]] || miss "I4 the backup must hold the old bytes: $bak"
run -- --entry-role other --yes
[[ "$(ls "$state"/operating-profile.toml.bak-* | wc -l | tr -d ' ')" == "2" ]] || miss "I4 a second replace must keep both backups: $(ls "$state")"
fresh
printf 'schema_version = 1\nname = "mine"\n' >"$prof"; before="$(shasum -a 256 <"$prof")"
runtty "overwrite=>n" -- --entry-role supervisor
[[ "$rc" == 1 && "$(shasum -a 256 <"$prof")" == "$before" ]] || miss "I4 tty answer n: rc=$rc"
runtty "overwrite=>y" -- --entry-role supervisor
[[ "$rc" == 0 ]] && grep -qs 'entry_role = "supervisor"' "$prof" || miss "I4 tty answer y: rc=$rc out=$out"

# ---- I5 and I6: atomic write, summary printed ----
fresh
run -- --entry-role supervisor
[[ "$out" == *"profile ok:"* && "$out" == *"team *: entry_role supervisor"* ]] || miss "I6 the profile summary must be printed: $out"
[[ "$(ls -A "$state" | tr '\n' ' ')" == "operating-profile.toml " ]] || miss "I5 no temp file may be left: $(ls -A "$state")"

# ---- I7: --manifests is not in the first build ----
fresh
run -- --manifests /some/dir
[[ $rc -eq 2 && "$out" == *"not in the first build: --manifests"* && ! -e "$prof" ]] || miss "I7 --manifests: rc=$rc out=$out"

# ---- S1 to S4: the shadow scan reports and edits nothing ----
fresh
printf '%s\n' '# my rc' 'director-session() { echo shadowed; }' >"$home/.zshrc"
printf '%s\n' 'alias director-session="echo shadowed"' >"$home/.bashrc"
before="$(hash_tree)"
run -- --entry-role supervisor --yes
[[ "$out" == *"$home/.zshrc:2: function"* && "$out" == *"$home/.bashrc:1: alias"* ]] || miss "S1 function and alias must be reported with file and line: rc=$rc out=$out"
rm -f "$state"/operating-profile.toml*
[[ "$(cat "$home/.zshrc")" == "$(printf '%s\n' '# my rc' 'director-session() { echo shadowed; }')" ]] || miss "S3 the rc file must not be edited"
fresh
printf '%s\n' 'director-session() { echo shadowed; }' >"$home/.zshrc"
before="$(find "$home" -name '.zshrc' -exec shasum -a 256 {} +)"
run -- --entry-role supervisor
[[ "$(find "$home" -name '.zshrc' -exec shasum -a 256 {} +)" == "$before" ]] || miss "S3 the rc file hash must be unchanged after init"
run -- --check
[[ "$(find "$home" -name '.zshrc' -exec shasum -a 256 {} +)" == "$before" ]] || miss "S3 the rc file hash must be unchanged after --check"

# the operator's wrapper, at line 145, is intended: wraps, not drift
fresh
i=1; : >"$home/.zshrc"
while [[ $i -lt 145 ]]; do echo "# filler $i" >>"$home/.zshrc"; i=$((i+1)); done
printf '%s\n' 'director-session() { DIRECTOR_PRESENCE_WATCH=1 ~/.director/bin/director-session --receiver "$@"; }' >>"$home/.zshrc"
run -- --entry-role supervisor
[[ "$out" == *"$home/.zshrc:145: wraps"* && "$out" != *"drift"* ]] || miss "S2 the wrapper must read as wraps: rc=$rc out=$out"
run -- --check
[[ $rc -eq 0 && "$out" == *"$home/.zshrc:145: wraps"* && "$out" == *"clean"* ]] || miss "S2 a wrapper is not drift: rc=$rc out=$out"
for body in 'DIRECTOR_PRESENCE_WATCH=1 $HOME/.director/bin/director-session "$@"' '${DIRECTOR_HOME:-$HOME/.director}/bin/director-session "$@"'; do
  printf '%s\n' "director-session() { $body; }" >"$home/.zshrc"
  run -- --check
  [[ $rc -eq 0 && "$out" == *".zshrc:1: wraps"* ]] || miss "S2 wrapper variant '$body': rc=$rc out=$out"
done
printf '%s\n' 'director-session() { /opt/other/director-session "$@"; }' >"$home/.zshrc"
run -- --check
[[ $rc -eq 1 && "$out" == *"drift: $home/.zshrc:1: function"* ]] || miss "S2 a function that wraps another file is drift: rc=$rc out=$out"
printf '%s\n' 'function director-session {' '  echo shadowed' '}' >"$home/.zshrc"
run -- --check
[[ $rc -eq 1 && "$out" == *"drift: $home/.zshrc:1: function"* ]] || miss "S1 the function keyword form: rc=$rc out=$out"
printf '%s\n' "alias director-session='$home/.director/bin/director-session --receiver'" >"$home/.zshrc"
run -- --check
[[ $rc -eq 0 && "$out" == *".zshrc:1: wraps"* ]] || miss "S2 an alias that wraps the launcher: rc=$rc out=$out"

# a PATH entry that holds another director-session, earlier than the bin home
fresh
mkdir -p "$work/early"; printf '#!/bin/sh\nexit 0\n' >"$work/early/director-session"; chmod +x "$work/early/director-session"
run PATH="$work/early:$dbin:$pydir:/usr/bin:/bin" -- --check
[[ $rc -eq 1 && "$out" == *"drift: $work/early/director-session: path"* ]] || miss "S4 an earlier PATH entry: rc=$rc out=$out"
run PATH="$dbin:$work/early:$pydir:/usr/bin:/bin" -- --check
[[ "$out" != *"path"* ]] || miss "S4 a later PATH entry does not shadow: $out"

# ---- C1: --check ----
fresh
run -- --entry-role supervisor
run -- --check
[[ $rc -eq 0 && "$out" == *"clean"* ]] || miss "C2 a sound state is clean: rc=$rc out=$out"
before="$(hash_tree)"
run -- --check
[[ "$(hash_tree)" == "$before" ]] || miss "C1 --check must write nothing"
rm -f "$prof"
run -- --check
[[ $rc -eq 1 && "$out" == *"drift: no profile file at $prof"* ]] || miss "C1 no profile: rc=$rc out=$out"
printf 'schema_version = 1\ncolour = "red"\n' >"$prof"
run -- --check
[[ $rc -eq 1 && "$out" == *"drift: the profile does not pass director-profile check"* && "$out" == *"unknown key: colour"* ]] || miss "C1 invalid profile: rc=$rc out=$out"
fresh
run -- --entry-role supervisor
stub_marvel
run -- --check
[[ $rc -eq 1 && "$out" == *"drift: marvel is found but marvel-register is not in fleet_sources"* ]] || miss "C1 marvel found, not in the profile: rc=$rc out=$out"
fresh; stub_marvel
run -- --entry-role supervisor
rm -f "$stubs/marvel"
run -- --check
[[ $rc -eq 1 && "$out" == *"drift: fleet_sources has marvel-register but marvel is not found"* ]] || miss "C1 profile says marvel, none found: rc=$rc out=$out"
fresh
run -- --entry-role supervisor
rm -f "$dbin/director-session"
run -- --check
[[ $rc -eq 1 && "$out" == *"drift: the launcher is missing: $dbin/director-session"* ]] || miss "C1 launcher missing: rc=$rc out=$out"
fresh
run -- --entry-role supervisor
rm -f "$dbin/director-profile"
run -- --check
[[ $rc -eq 1 && "$out" == *"drift: director-profile is missing: $dbin/director-profile"* ]] || miss "C1 director-profile missing: rc=$rc out=$out"

# ---- the python rule ----
fresh
old_py() {
  set +e
  out="$(env -i HOME="$home" TMPDIR="$work/tmp" PATH="$1" "$PY" -c 'import runpy, sys; sys.version_info = (3, 9, 6, "final", 0); sys.argv = [sys.argv[1], "--check"]; runpy.run_path(sys.argv[0], run_name="__main__")' "$BIN" 2>&1 </dev/null)"
  rc=$?
  set -e
}
mkdir -p "$work/emptybin"
old_py "$work/emptybin"
[[ $rc -eq 2 && "$(printf '%s\n' "$out" | wc -l | tr -d ' ')" == "1" && "$out" == *"python3 3.11 or newer"* && "$out" == *"3.9"* ]] || miss "P20 old python3 must be refused with one line naming 3.11: rc=$rc out=$out"
mkdir -p "$work/newbin"
printf '%s\n' '#!/bin/sh' "echo found-python312 >\"$work/found\"" >"$work/newbin/python3.12"
chmod +x "$work/newbin/python3.12"
old_py "$work/newbin"
[[ -f "$work/found" ]] || miss "P21 a python3.12 on PATH must be used when python3 is too old: rc=$rc out=$out"

if [[ $fail -ne 0 ]]; then echo "FAIL"; exit 1; fi
echo "ok: director-init writes the first-build profile, reports what shadows the launcher, and --check reports drift without writing"
