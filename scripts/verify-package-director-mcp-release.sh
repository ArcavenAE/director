#!/usr/bin/env bash
# Prove scripts/package-director-mcp-release: four binaries, the seat wrapper
# and a checksums.txt that lists and verifies all five, the same bytes on a second run, a version that names the
# commit, and a tag from the commit's own time. Then a mutant that corrupts the
# checksums must make the same checks fail.
#
# Usage: scripts/verify-package-director-mcp-release.sh   Exit nonzero on any miss.
# Builds from a fixture repo holding this checkout's cmd/director-mcp at HEAD;
# touches only a temp dir. Needs go (the toolchain the recipe pins) and git.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d "${TMPDIR:-/tmp}/pkg.XXXXXX")"
trap 'rm -rf "$root"' EXIT

# A repo whose only content is the shim's source, committed at a fixed time, so
# the version and tag are known. Signed with a key made for this run.
repo="$root/repo"; mkdir -p "$repo"
git -C "$here" archive HEAD cmd/director-mcp probe/nats-phase-0/director-mcp-seat | tar -x -C "$repo"
ssh-keygen -q -t ed25519 -N '' -C verify -f "$root/key" > /dev/null
printf '%s\n' '[user]' '	name = verify' '	email = verify@example.invalid' "	signingkey = $root/key" \
  '[gpg]' '	format = ssh' '[commit]' '	gpgsign = true' > "$root/gitconfig"
gitf() { env -u SSH_AUTH_SOCK GIT_CONFIG_GLOBAL="$root/gitconfig" GIT_CONFIG_SYSTEM=/dev/null git -C "$repo" "$@"; }
gitf init -q -b main
gitf add -A
GIT_AUTHOR_DATE="2026-11-01T12:34:56Z" GIT_COMMITTER_DATE="2026-11-01T12:34:56Z" gitf commit -qm fixture
sha="$(gitf rev-parse HEAD)"

pkg() { "$here/scripts/package-director-mcp-release" --git-dir "$repo/.git" --sha "$sha" --out "$1" | tail -n 1; }

tag="$(pkg "$root/a")"
[[ "$tag" == "tag=director-mcp-alpha-20261101-123456-${sha:0:7}" ]] && ok "the tag comes from the commit time" || bad "the tag comes from the commit time" "$tag"

n=0; for f in darwin-arm64 darwin-amd64 linux-amd64 linux-arm64; do [[ -s "$root/a/director-mcp-$f" ]] && n=$((n+1)); done
[[ "$n" == 4 ]] && ok "four binaries" || bad "four binaries" "$n"
(cd "$root/a" && shasum -a 256 -c checksums.txt > /dev/null 2>&1) && ok "checksums.txt verifies" || bad "checksums.txt verifies"
[[ "$(wc -l < "$root/a/checksums.txt" | tr -d ' ')" == 5 ]] && grep -q '  director-mcp-seat$' "$root/a/checksums.txt" \
  && ok "checksums.txt lists the four binaries and the seat wrapper and nothing else" || bad "checksums.txt lists five"
git -C "$here" show "HEAD:probe/nats-phase-0/director-mcp-seat" | cmp -s - "$root/a/director-mcp-seat" \
  && ok "the seat wrapper is the commit's own bytes" || bad "the seat wrapper is the commit's own bytes"
[[ -x "$root/a/director-mcp-seat" ]] && ok "the seat wrapper is executable" || bad "the seat wrapper is executable"
# A commit with no wrapper stops the packager and names the path.
nowrap="$root/nowrap"; mkdir -p "$nowrap"; git -C "$here" archive HEAD cmd/director-mcp | tar -x -C "$nowrap"
env -u SSH_AUTH_SOCK GIT_CONFIG_GLOBAL="$root/gitconfig" GIT_CONFIG_SYSTEM=/dev/null git -C "$nowrap" init -q -b main
env -u SSH_AUTH_SOCK GIT_CONFIG_GLOBAL="$root/gitconfig" GIT_CONFIG_SYSTEM=/dev/null git -C "$nowrap" add -A
GIT_AUTHOR_DATE="2026-11-01T12:34:56Z" GIT_COMMITTER_DATE="2026-11-01T12:34:56Z" env -u SSH_AUTH_SOCK GIT_CONFIG_GLOBAL="$root/gitconfig" GIT_CONFIG_SYSTEM=/dev/null git -C "$nowrap" commit -qm nowrap
nsha="$(git -C "$nowrap" rev-parse HEAD)"
if "$here/scripts/package-director-mcp-release" --git-dir "$nowrap/.git" --sha "$nsha" --out "$root/n" > "$root/n.out" 2>&1; then
  bad "a commit with no seat wrapper was packaged"
else
  grep -q 'probe/nats-phase-0/director-mcp-seat' "$root/n.out" && ok "a commit with no seat wrapper stops the packager and names the path" || bad "no-wrapper refusal text" "$(cat "$root/n.out")"
fi
# The workflow publishes when the wrapper changes.
grep -q "probe/nats-phase-0/director-mcp-seat" "$here/.github/workflows/release-director-mcp.yml" \
  && ok "the release workflow runs when the seat wrapper changes" || bad "the release workflow does not watch the seat wrapper"

pkg "$root/b" > /dev/null
cmp -s "$root/a/checksums.txt" "$root/b/checksums.txt" && ok "a second run gives the same bytes" || bad "a second run gives the same bytes"

host="$(go env GOOS)-$(go env GOARCH)"
line="$("$root/a/director-mcp-$host" --version 2>&1 || true)"
want="director-mcp 0.1.0-alpha.20261101.123456.${sha:0:7} rev $sha channel alpha"
[[ "$line" == "$want" ]] && ok "the host binary's --version names the commit" || bad "the host binary's --version names the commit" "$line"

# Mutant: a copy of the packager that writes a wrong sum for one binary. The
# checksum check above must catch it.
mkdir -p "$root/mut/scripts"; cp "$here/scripts/build-director-mcp" "$root/mut/scripts/"
sed -e "s|shasum -a 256 director-mcp-\* |shasum -a 256 director-mcp-* \| sed '1s/^./0/' |" "$here/scripts/package-director-mcp-release" > "$root/mut/scripts/package-director-mcp-release"
if cmp -s "$here/scripts/package-director-mcp-release" "$root/mut/scripts/package-director-mcp-release"; then
  bad "the mutant differs from the packager"
else
  chmod +x "$root/mut/scripts/"*
  "$root/mut/scripts/package-director-mcp-release" --git-dir "$repo/.git" --sha "$sha" --out "$root/m" > /dev/null
  if (cd "$root/m" && shasum -a 256 -c checksums.txt > /dev/null 2>&1); then bad "a corrupted checksum goes red" "the mutant verified"; else ok "a corrupted checksum goes red"; fi
fi

echo "$pass passed, $fail failed"
[[ "$fail" == 0 ]]
