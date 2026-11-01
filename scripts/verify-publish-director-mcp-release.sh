#!/usr/bin/env bash
# Prove scripts/publish-director-mcp-release with a gh stub that logs its calls: a
# new tag is created with all six assets; a complete release is left alone; a release
# a failed run left short gets only its missing assets, with no create and no
# overwrite; a dist directory short of a file stops before gh is called. Then two
# mutants (skip whenever the release exists, and re-create instead of upload) must
# each fail the same cases.
#
# Usage: scripts/verify-publish-director-mcp-release.sh   Exit nonzero on any miss.
# Touches only a temp dir; gh is a stub on PATH and nothing reaches the network.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d "${TMPDIR:-/tmp}/pub.XXXXXX")"
trap 'rm -rf "$root"' EXIT
mkdir -p "$root/bin" "$root/dist"
EXPECTED="director-mcp-darwin-arm64 director-mcp-darwin-amd64 director-mcp-linux-amd64 director-mcp-linux-arm64 checksums.txt attestations.sigstore.json"
for a in $EXPECTED; do echo "$a" > "$root/dist/$a"; done

# The stub answers `release view` from $STATE (absent file = no release) and logs every call.
printf '%s\n' '#!/bin/sh' 'echo "$*" >> "$GH_LOG"' \
  'case "$1 $2" in' \
  '  "release view") [ -f "$GH_STATE" ] || exit 1; cat "$GH_STATE" ;;' \
  '  "release create"|"release upload") ;;' \
  '  *) echo "gh stub: unexpected call: $*" >&2; exit 99 ;;' \
  'esac' > "$root/bin/gh"
chmod +x "$root/bin/gh"

tag="director-mcp-alpha-20261101-123456-abc1234"
sha="0123456789abcdef0123456789abcdef01234567"
# publish SCRIPT STATE-CONTENT|NONE [DIST]: runs it, leaving rc and the call log
publish() {
  rm -f "$root/log" "$root/state"
  [[ "$2" == NONE ]] || printf '%b' "$2" > "$root/state"
  rc=0
  PATH="$root/bin:$PATH" GH_LOG="$root/log" GH_STATE="$root/state" "$1" --tag "$tag" --sha "$sha" --dist "${3:-$root/dist}" --repo example/director > "$root/out" 2>&1 || rc=$?
  : >> "$root/log"
}
calls() { grep -c "^release $1 " "$root/log" || true; }

# verdict SCRIPT: 0 when all four cases behave.
verdict() {
  local v=0 all missing
  all="$(printf '%s\n' $EXPECTED)"
  publish "$1" NONE
  if [[ "$rc" -ne 0 || "$(calls create)" != 1 || "$(calls upload)" != 0 ]] || ! grep -q -- "--target $sha" "$root/log"; then echo "  a new tag was not created once on the commit (rc=$rc)"; v=1; fi
  n="$(grep '^release create' "$root/log" | tr ' ' '\n' | grep -c '/director-mcp-\|/checksums.txt\|/attestations' || true)"
  [[ "$n" == 6 ]] || { echo "  create did not carry six assets ($n)"; v=1; }
  publish "$1" "$all\n"
  if [[ "$rc" -ne 0 || "$(calls create)" != 0 || "$(calls upload)" != 0 ]]; then echo "  a complete release was touched (rc=$rc)"; v=1; fi
  missing="$(printf '%s\n' $EXPECTED | grep -v '^attestations.sigstore.json$' | grep -v '^director-mcp-linux-arm64$')"
  publish "$1" "$missing\n"
  if [[ "$rc" -ne 0 || "$(calls create)" != 0 || "$(calls upload)" != 1 ]]; then echo "  a short release was not finished by one upload (rc=$rc)"; v=1; fi
  if ! grep '^release upload' "$root/log" | grep -q 'attestations.sigstore.json' || ! grep '^release upload' "$root/log" | grep -q 'director-mcp-linux-arm64' \
     || grep '^release upload' "$root/log" | grep -q 'checksums.txt\|director-mcp-darwin\|director-mcp-linux-amd64' || grep -q -- '--clobber' "$root/log"; then
    echo "  the upload was not exactly the two missing files without --clobber"; v=1
  fi
  mkdir -p "$root/short"; cp "$root/dist"/* "$root/short/"; rm "$root/short/checksums.txt"
  publish "$1" NONE "$root/short"
  if [[ "$rc" -eq 0 || -s "$root/log" ]]; then echo "  a dist short of a file reached gh (rc=$rc)"; v=1; fi
  return "$v"
}

if verdict "$here/scripts/publish-director-mcp-release"; then ok "new, complete, short and incomplete-dist cases behave"; else bad "the publish script fails its cases"; fi

mutant() { # NAME SED-EXPR
  mkdir -p "$root/mut"; sed -e "$2" "$here/scripts/publish-director-mcp-release" > "$root/mut/$1"; chmod +x "$root/mut/$1"
  if cmp -s "$here/scripts/publish-director-mcp-release" "$root/mut/$1"; then bad "mutant $1 did not change the script"; return; fi
  if verdict "$root/mut/$1" > /dev/null; then bad "mutant $1 stayed green"; else ok "mutant $1 goes red"; fi
}
mutant skips-when-release-exists 's/if \[\[ ${#missing\[@\]} -eq 0 \]\]; then/if true; then/'
mutant recreates-instead-of-uploading 's/gh release upload "\$tag" --repo "\$repo" "\${missing\[@\]}"/gh release create "$tag" --repo "$repo" --target "$sha" "${missing[@]}"/'

echo "$pass passed, $fail failed"
[[ "$fail" == 0 ]]
