#!/usr/bin/env bash
# Prove cast-stubs.sh keeps its harness root under TMPDIR (director#648, the
# same cause as #434). On macOS a template-less `mktemp -d` ignores TMPDIR and
# writes under the per-user temp directory, so the cast-launch verifier cannot
# run under a profile that allows writes only to a scratch TMPDIR. Two checks:
#   1. the root cast-stubs.sh makes lies under the TMPDIR it was given;
#   2. on macOS, sourcing it succeeds under a sandbox-exec profile that denies
#      every write outside that scratch directory (and /dev). macOS refuses a
#      sandbox-exec inside another sandbox ("sandbox_apply: Operation not
#      permitted"), so under an outer scratch-only profile this check is
#      skipped, with the refusal quoted, and check 1 carries the proof.
#   3. when the root cannot be made (TMPDIR unwritable), sourcing the stubs stops
#      with a message and a nonzero status, and creates none of the paths the
#      stubs would otherwise write at the top of the filesystem (/wardrobe, /out,
#      /bin/claude, /bin/curl, /bin/director-mcp). Skipped when run as root,
#      where an unguarded stub would really write them.
# Usage: sim/twin/verify-cast-stubs-tmpdir.sh     Exit nonzero on any miss.
#        A skipped check is neither a pass nor a fail and is counted apart.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
stubs="$here/cast-stubs.sh"
[[ -f "$stubs" ]] || { echo "cast-stubs.sh not found at $stubs"; exit 2; }

fail=0; skip=0
ok()  { echo "PASS $1"; }
skipped() { echo "SKIP $1${2:+ -- $2}"; skip=$((skip+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

scratch="$(mktemp -d "${TMPDIR:-/tmp}/cast-stubs-tmpdir.XXXXXX")"
trap 'rm -rf "$scratch"' EXIT
scratch="$(cd "$scratch" && pwd -P)"

# 1. the root lies under the given TMPDIR.
# The stubs run from scratch: macOS /bin/bash 3.2 writes here-document temp files
# in the working directory, not TMPDIR, so a read-only working directory under a
# scratch-only profile would stop the stubs for a reason that is not this one.
root="$(TMPDIR="$scratch" LAUNCH=/usr/bin/true bash -c 'cd "$2" && source "$1" && printf "%s" "$root"' _ "$stubs" "$scratch")"
if [[ "$root" == "$scratch"/* ]]; then
  ok "cast-stubs.sh root is under TMPDIR"
else
  bad "cast-stubs.sh root is under TMPDIR" "root='$root' is not under $scratch"
fi

# 2. sourcing it works when only the scratch directory is writable.
if [[ "$(uname -s)" == Darwin && -x /usr/bin/sandbox-exec ]]; then
  profile="(version 1)(allow default)(deny file-write*)(allow file-write* (subpath \"$scratch\") (subpath \"/dev\"))"
  # A nested sandbox-exec is refused by macOS whatever the stubs do, so probe
  # that first and skip, quoting the refusal, rather than fail on it.
  if refusal="$(/usr/bin/sandbox-exec -p '(version 1)(allow default)' /usr/bin/true 2>&1)"; then
    if err="$(TMPDIR="$scratch" LAUNCH=/usr/bin/true /usr/bin/sandbox-exec -p "$profile" bash -c 'cd "$2" && source "$1" && [[ -d "$root" ]]' _ "$stubs" "$scratch" 2>&1)"; then
      ok "cast-stubs.sh sources under a scratch-only write profile"
    else
      bad "cast-stubs.sh sources under a scratch-only write profile" "${err//$'\n'/ }"
    fi
  else
    skipped "cast-stubs.sh sources under a scratch-only write profile" "sandbox-exec refused: ${refusal//$'\n'/ }"
  fi
else
  skipped "cast-stubs.sh sources under a scratch-only write profile" "needs macOS sandbox-exec"
fi

# 3. a root that cannot be made stops the source before any write.
if (( EUID == 0 )); then
  skipped "cast-stubs.sh stops when the root cannot be made" "running as root, where an unguarded stub would write /wardrobe, /out and /bin"
else
  unwritable="$scratch/unwritable"
  mkdir "$unwritable"
  chmod 500 "$unwritable"
  stray=(/wardrobe /out /bin/claude /bin/curl /bin/director-mcp)
  pre=()
  for p in "${stray[@]}"; do [[ -e "$p" ]] && pre+=("$p"); done
  src_rc=0
  src_out="$(TMPDIR="$unwritable" LAUNCH=/usr/bin/true bash -c 'cd "$2" && source "$1"' _ "$stubs" "$scratch" 2>&1)" || src_rc=$?
  chmod 700 "$unwritable"
  made=()
  for p in "${stray[@]}"; do
    [[ -e "$p" ]] || continue
    already=0
    for q in ${pre[@]+"${pre[@]}"}; do [[ "$q" == "$p" ]] && already=1; done
    (( already == 1 )) || made+=("$p")
  done
  if (( src_rc != 0 )) && [[ "$src_out" == *"no harness root"* ]] && (( ${#made[@]} == 0 )); then
    ok "cast-stubs.sh stops when the root cannot be made"
  else
    bad "cast-stubs.sh stops when the root cannot be made" "source exit $src_rc; message: ${src_out//$'\n'/ }; paths created: ${made[*]:-none}"
  fi
fi

if (( fail > 0 )); then
  echo "$fail failed, $skip skipped"
  exit 1
fi
if (( skip > 0 )); then echo "all passed, $skip skipped"; else echo "all passed"; fi
