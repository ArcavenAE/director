#!/usr/bin/env bash
# Prove scripts/package-director-mcp-release: four binaries and a checksums.txt
# that verifies, the same bytes on a second run, a version that names the
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
git -C "$here" archive HEAD cmd/director-mcp | tar -x -C "$repo"
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
[[ "$(wc -l < "$root/a/checksums.txt" | tr -d ' ')" == 4 ]] && ok "checksums.txt lists the four binaries and nothing else" || bad "checksums.txt lists four"

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
