#!/usr/bin/env bash
# Prove cast-stubs.sh keeps its harness root under TMPDIR (director#648, the
# same cause as #434). On macOS a template-less `mktemp -d` ignores TMPDIR and
# writes under the per-user temp directory, so the cast-launch verifier cannot
# run under a profile that allows writes only to a scratch TMPDIR. Two checks:
#   1. the root cast-stubs.sh makes lies under the TMPDIR it was given;
#   2. on macOS, sourcing it succeeds under a sandbox-exec profile that denies
#      every write outside that scratch directory (and /dev).
# Usage: sim/twin/verify-cast-stubs-tmpdir.sh     Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
stubs="$here/cast-stubs.sh"
[[ -f "$stubs" ]] || { echo "cast-stubs.sh not found at $stubs"; exit 2; }

fail=0
ok()  { echo "PASS $1"; }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

scratch="$(mktemp -d "${TMPDIR:-/tmp}/cast-stubs-tmpdir.XXXXXX")"
trap 'rm -rf "$scratch"' EXIT
scratch="$(cd "$scratch" && pwd -P)"

# 1. the root lies under the given TMPDIR.
root="$(TMPDIR="$scratch" LAUNCH=/usr/bin/true bash -c 'source "$1"; printf "%s" "$root"' _ "$stubs")"
if [[ "$root" == "$scratch"/* ]]; then
  ok "cast-stubs.sh root is under TMPDIR"
else
  bad "cast-stubs.sh root is under TMPDIR" "root $root is not under $scratch"
fi

# 2. sourcing it works when only the scratch directory is writable.
if [[ "$(uname -s)" == Darwin && -x /usr/bin/sandbox-exec ]]; then
  profile="(version 1)(allow default)(deny file-write*)(allow file-write* (subpath \"$scratch\") (subpath \"/dev\"))"
  if err="$(TMPDIR="$scratch" LAUNCH=/usr/bin/true /usr/bin/sandbox-exec -p "$profile" bash -c 'source "$1"; [[ -d "$root" ]]' _ "$stubs" 2>&1)"; then
    ok "cast-stubs.sh sources under a scratch-only write profile"
  else
    bad "cast-stubs.sh sources under a scratch-only write profile" "${err//$'\n'/ }"
  fi
else
  echo "SKIP the scratch-only write profile needs macOS sandbox-exec"
fi

if (( fail > 0 )); then
  echo "$fail failed"
  exit 1
fi
echo "all passed"
