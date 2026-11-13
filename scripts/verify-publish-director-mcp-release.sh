#!/usr/bin/env bash
# Prove scripts/publish-director-mcp-release with a gh stub that logs its calls and answers
# `release view` by running the script's own --jq filter over a JSON fixture, so the
# filter is under test, not just the shell around it. Cases: a new tag is created once
# on the commit; a complete release is left alone; a release a failed run left short
# gets only its missing assets uploaded, with no create and no overwrite; a lookalike
# name is not the asset; an empty asset and a not-uploaded asset count as missing; a
# release that targets another commit stops with both named; a dist directory short of
# a file stops before gh is called. Then six mutants, each of which must fail a case.
#
# Usage: scripts/verify-publish-director-mcp-release.sh   Exit nonzero on any miss.
# Needs jq. Touches only a temp dir; gh is a stub on PATH and nothing reaches the network.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }
command -v jq > /dev/null || { echo "verify-publish-director-mcp-release: jq is required" >&2; exit 2; }

root="$(mktemp -d "${TMPDIR:-/tmp}/pub.XXXXXX")"
trap 'rm -rf "$root"' EXIT
mkdir -p "$root/bin" "$root/dist"
EXPECTED="director-mcp-darwin-arm64 director-mcp-darwin-amd64 director-mcp-linux-amd64 director-mcp-linux-arm64 director-mcp-seat checksums.txt attestations.sigstore.json"
for a in $EXPECTED; do echo "$a" > "$root/dist/$a"; done

tag="director-mcp-alpha-20261101-123456-abc1234"
sha="0123456789abcdef0123456789abcdef01234567"

# The stub: `release view` applies the caller's --jq to $GH_STATE (absent file = no release);
# create and upload only log. Every call is logged.
printf '%s\n' '#!/bin/sh' 'echo "$*" >> "$GH_LOG"' \
  'case "$1 $2" in' \
  '  "release view")' \
  '    [ -f "$GH_STATE" ] || exit 1' \
  '    expr=""; prev=""' \
  '    for a in "$@"; do [ "$prev" = "--jq" ] && expr="$a"; prev="$a"; done' \
  '    jq -r "$expr" "$GH_STATE" ;;' \
  '  "release create"|"release upload") ;;' \
  '  *) echo "gh stub: unexpected call: $*" >&2; exit 99 ;;' \
  'esac' > "$root/bin/gh"
chmod +x "$root/bin/gh"

# state TARGET NAME:SIZE:STATE...: write the release JSON the stub answers with.
state() {
  local target="$1"; shift
  local items="" a
  for a in "$@"; do
    IFS=: read -r n sz st <<<"$a"
    items="${items:+$items,}{\"name\":\"$n\",\"size\":$sz,\"state\":\"$st\"}"
  done
  printf '{"targetCommitish":"%s","assets":[%s]}\n' "$target" "$items" > "$root/state"
}
full() { local a; for a in $EXPECTED; do printf '%s:10:uploaded\n' "$a"; done; }

# publish SCRIPT [DIST]: runs it against $root/state if present; rc and the call log are left.
publish() {
  rm -f "$root/log"; rc=0
  PATH="$root/bin:$PATH" GH_LOG="$root/log" GH_STATE="$root/state" "$1" --tag "$tag" --sha "$sha" --dist "${2:-$root/dist}" --repo example/director > "$root/out" 2>&1 || rc=$?
  : >> "$root/log"
}
calls() { grep -c "^release $1 " "$root/log" || true; }
uploaded() { grep '^release upload' "$root/log" | tr ' ' '\n' | grep -c "/$1\$" || true; }

# verdict SCRIPT: 0 when every case behaves; the misses print on stdout.
verdict() {
  local v=0 n
  # new tag
  rm -f "$root/state"; publish "$1"
  if [[ "$rc" -ne 0 || "$(calls create)" != 1 || "$(calls upload)" != 0 ]] || ! grep -q -- "--target $sha" "$root/log"; then echo "  a new tag was not created once on the commit (rc=$rc)"; v=1; fi
  n="$(grep '^release create' "$root/log" | tr ' ' '\n' | grep -c '/director-mcp-\|/checksums.txt\|/attestations' || true)"
  [[ "$n" == 7 ]] || { echo "  create did not carry seven assets ($n)"; v=1; }
  # complete
  state "$sha" $(full); publish "$1"
  if [[ "$rc" -ne 0 || "$(calls create)" != 0 || "$(calls upload)" != 0 ]]; then echo "  a complete release was touched (rc=$rc)"; v=1; fi
  # short: two missing
  state "$sha" $(full | grep -v '^attestations.sigstore.json:' | grep -v '^director-mcp-linux-arm64:'); publish "$1"
  if [[ "$rc" -ne 0 || "$(calls create)" != 0 || "$(calls upload)" != 1 \
        || "$(uploaded attestations.sigstore.json)" != 1 || "$(uploaded director-mcp-linux-arm64)" != 1 \
        || "$(grep '^release upload' "$root/log" | tr ' ' '\n' | grep -c "^$root/dist/")" != 2 ]] \
     || grep -q -- '--clobber' "$root/log"; then
    echo "  a short release was not finished by one upload of exactly the two missing files, no --clobber (rc=$rc)"; v=1
  fi
  # a release from before the wrapper: only the wrapper is uploaded
  state "$sha" $(full | grep -v '^director-mcp-seat:'); publish "$1"
  if [[ "$rc" -ne 0 || "$(calls create)" != 0 || "$(calls upload)" != 1 || "$(uploaded director-mcp-seat)" != 1 \
        || "$(grep '^release upload' "$root/log" | tr ' ' '\n' | grep -c "^$root/dist/")" != 1 ]]; then
    echo "  a release with no seat wrapper was not finished by one upload of the wrapper alone (rc=$rc)"; v=1
  fi
  # lookalike name
  state "$sha" $(full | grep -v '^checksums.txt:') checksums.txt.bak:10:uploaded; publish "$1"
  if [[ "$rc" -ne 0 || "$(calls upload)" != 1 || "$(uploaded checksums.txt)" != 1 ]]; then echo "  checksums.txt.bak was taken for checksums.txt (rc=$rc)"; v=1; fi
  # empty asset
  state "$sha" $(full | grep -v '^director-mcp-linux-amd64:') director-mcp-linux-amd64:0:uploaded; publish "$1"
  if [[ "$rc" -ne 0 || "$(calls upload)" != 1 || "$(uploaded director-mcp-linux-amd64)" != 1 ]]; then echo "  a zero-byte asset counted as present (rc=$rc)"; v=1; fi
  # not uploaded
  state "$sha" $(full | grep -v '^attestations.sigstore.json:') attestations.sigstore.json:10:starter; publish "$1"
  if [[ "$rc" -ne 0 || "$(calls upload)" != 1 || "$(uploaded attestations.sigstore.json)" != 1 ]]; then echo "  a not-uploaded asset counted as present (rc=$rc)"; v=1; fi
  # another commit
  state "fedcba9876543210fedcba9876543210fedcba98" $(full); publish "$1"
  if [[ "$rc" -eq 0 || "$(calls create)" != 0 || "$(calls upload)" != 0 ]] || ! grep -q 'fedcba9876543210' "$root/out" || ! grep -q "$sha" "$root/out"; then
    echo "  a release at another commit did not stop with both named (rc=$rc)"; v=1
  fi
  # dist short of a file
  mkdir -p "$root/short"; cp "$root/dist"/* "$root/short/"; rm "$root/short/checksums.txt"
  rm -f "$root/state"; publish "$1" "$root/short"
  if [[ "$rc" -eq 0 || -s "$root/log" ]]; then echo "  a dist short of a file reached gh (rc=$rc)"; v=1; fi
  return "$v"
}

if verdict "$here/scripts/publish-director-mcp-release"; then ok "new, complete, short, lookalike, empty, not-uploaded, other-commit and short-dist cases behave"; else bad "the publish script fails its cases"; fi

mutant() { # NAME SED-EXPR
  mkdir -p "$root/mut"; sed -e "$2" "$here/scripts/publish-director-mcp-release" > "$root/mut/$1"; chmod +x "$root/mut/$1"
  if cmp -s "$here/scripts/publish-director-mcp-release" "$root/mut/$1"; then bad "mutant $1 did not change the script"; return; fi
  if verdict "$root/mut/$1" > /dev/null; then bad "mutant $1 stayed green"; else ok "mutant $1 goes red"; fi
}
mutant skips-when-release-exists 's/if \[\[ ${#missing\[@\]} -eq 0 \]\]; then/if true; then/'
mutant recreates-instead-of-uploading 's/gh release upload "\$tag" --repo "\$repo" "\${missing\[@\]}"/gh release create "$tag" --repo "$repo" --target "$sha" "${missing[@]}"/'
mutant matches-by-substring 's/grep -qxF -- "\$a"/grep -qF -- "$a"/'
mutant ignores-size 's/select(\.size > 0 and \.state/select(.state/'
mutant ignores-state 's/select(\.size > 0 and \.state == "uploaded")/select(.size > 0)/'
mutant ignores-target 's/^  \[\[ "\$target" == "\$sha" \]\] || die .*$/  true/'

echo "$pass passed, $fail failed"
[[ "$fail" == 0 ]]
