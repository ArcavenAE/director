#!/usr/bin/env bash
# Prove a fresh install carries no other person's paths (standing-loop design,
# sim/design/director-standing-loop.md section 3, slice 4; case no-foreign-roots).
#
# The shipped idea-roots.conf used to name seven roots under one install's
# ~/work/aae-orc. A fresh install now writes it comment-only, and says so in one
# line that names the file to edit. An existing file is never touched, so an
# install that already has roots (or an edit of any kind) is unchanged.
#
# Covered:
#  - no-foreign-roots: a fresh install's idea-roots.conf has no root line, and no
#    line of it names a work/aae-orc path;
#  - install-names-the-file: the install's output has one line that names that file
#    and says it holds no roots yet;
#  - the existing-install controls: a second install leaves an edited conf byte for
#    byte as it was and prints no such line; a conf with a root line keeps it.
#
# Every run is env -i with a scratch HOME, TMPDIR, CLAUDE_HOME, DIRECTOR_HOME and
# DIRECTOR_STATE under one mktemp directory, so no MARVEL_*, DIRECTOR_* (beyond those
# three) or NATS_* variable reaches install.sh, nothing is written outside it, and
# nothing reads a real ~/.director. No network. The scratch directory is the only
# thing removed, by the trap.
#
# Usage: scripts/verify-install-fresh-home.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
root="$(cd "$(mktemp -d "${TMPDIR:-/tmp}/vifh.XXXXXX")" && pwd -P)"
trap 'rm -rf "${root:?}"' EXIT
mkdir -p "$root/tmp"

fail=0
miss() { echo "MISS: $*"; fail=1; }
ok() { echo "ok: $*"; }

run_install() { # <scratch home>: sets rc and out (stdout and stderr together)
  set +e
  out="$(env -i HOME="$1/home" TMPDIR="$root/tmp" PATH="/usr/bin:/bin" CLAUDE_HOME="$1/claude" \
    DIRECTOR_HOME="$1/director" DIRECTOR_STATE="$1/director/state" "$here/install.sh" 2>&1 </dev/null)"
  rc=$?
  set -e
}

# ---- positive control: a fresh install runs and writes the file ----
fresh="$root/fresh"; mkdir -p "$fresh/home"
run_install "$fresh"
conf="$fresh/director/state/idea-roots.conf"
[[ $rc -eq 0 && -f "$conf" ]] || miss "control: a fresh install must exit 0 and write idea-roots.conf: rc=$rc, last lines: $(printf '%s\n' "$out" | tail -3)"

# ---- no-foreign-roots ----
roots="$(grep -vE '^[[:space:]]*(#|$)' "$conf" || true)"
[[ -z "$roots" ]] || miss "no-foreign-roots: the shipped idea-roots.conf must name no root, it names: $roots"
if grep -q 'work/aae-orc' "$conf"; then miss "no-foreign-roots: no line of the shipped file may name work/aae-orc"; fi

# ---- install-names-the-file ----
line="$(printf '%s\n' "$out" | grep -F "$conf" | grep -iF 'no roots' || true)"
[[ -n "$line" ]] || miss "install-names-the-file: the install must print a line naming $conf and saying it has no roots yet; the lines that name it are: $(printf '%s\n' "$out" | grep -F "$conf" || echo none)"
[[ "$(printf '%s\n' "$out" | grep -cF "$conf")" -le 1 ]] || miss "install-names-the-file: the file is named on one line, not several"

# ---- existing installs are unchanged ----
printf '%s\n' '# mine' '~/mine/*.md' >"$conf"
before="$(shasum -a 256 "$conf")"
run_install "$fresh"
[[ "$(shasum -a 256 "$conf")" == "$before" ]] || miss "existing: a second install must leave an edited idea-roots.conf byte for byte as it was"
[[ "$out" != *"no roots"* ]] || miss "existing: a second install writes nothing here, so it prints no no-roots line"
[[ "$(grep -vcE '^[[:space:]]*(#|$)' "$conf")" == 1 ]] || miss "existing: the operator's root line is kept"

if [[ $fail -ne 0 ]]; then echo "FAIL"; exit 1; fi
echo "ok: a fresh install writes a comment-only idea-roots.conf, names it, and leaves an existing one alone"
