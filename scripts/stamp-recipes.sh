#!/usr/bin/env bash
# Stamp a copy of the recipes library with the version and source the committed
# company.toml cannot carry (it cannot hold its own commit). The copy is the
# installable form: `sideshow ... --from <out-dir>`, or any installer that reads
# the schema.
#
#   version = "<commit date>-<short sha>"      of HEAD, the form marvel names the
#                                              version directory with
#   source  = { url, commit }                  the origin url (no userinfo, no .git
#                                              suffix) and the full sha of HEAD
#
# Usage: scripts/stamp-recipes.sh <library-dir> <out-dir>
# Refuses a library with uncommitted changes, and an out-dir that is the library,
# inside it, a parent of it or of the repository, or not empty; it writes nothing and
# removes nothing in those cases. The committed files are never edited. Needs git
# and python3 (3.11 or newer).
set -euo pipefail

lib="${1:?usage: stamp-recipes.sh <library-dir> <out-dir>}"
out="${2:?usage: stamp-recipes.sh <library-dir> <out-dir>}"
lib="$(cd "$lib" && pwd)"
[[ -f "$lib/company.toml" ]] || { echo "refusing: $lib has no company.toml" >&2; exit 1; }
repo="$(git -C "$lib" rev-parse --show-toplevel)" || { echo "refusing: $lib is not in a git work tree" >&2; exit 1; }
dirty="$(git -C "$repo" status --porcelain -- "$lib")"
if [[ -n "$dirty" ]]; then
  echo "refusing: $lib has uncommitted changes (commit them first):" >&2
  printf '%s\n' "$dirty" | sed 's/^/  /' >&2
  exit 1
fi
# The out-dir must be new or empty, and must not be the library, inside it, or a
# parent of it or of the repository: nothing is ever removed to make room.
out="$(python3 -c 'import os,sys; print(os.path.realpath(sys.argv[1]))' "$out")"
libp="$(cd "$lib" && pwd -P)"
repop="$(cd "$repo" && pwd -P)"
case "$out/" in
  "$libp"/*) echo "refusing: out-dir $out is the library or inside it" >&2; exit 1 ;;
esac
case "$libp/" in
  "$out"/*) echo "refusing: out-dir $out contains the library" >&2; exit 1 ;;
esac
case "$repop/" in
  "$out"/*) echo "refusing: out-dir $out contains the repository" >&2; exit 1 ;;
esac
if [[ -e "$out" && ( ! -d "$out" || -n "$(ls -A "$out")" ) ]]; then
  echo "refusing: out-dir $out exists and is not an empty directory" >&2; exit 1
fi
commit="$(git -C "$repo" rev-parse HEAD)"
short="$(git -C "$repo" rev-parse --short=7 HEAD)"
date="$(git -C "$repo" log -1 --format=%cs HEAD)"
url="$(git -C "$repo" remote get-url origin 2>/dev/null)" || { echo "refusing: no origin remote to name as the source" >&2; exit 1; }
if [[ "$url" =~ ^[A-Za-z][A-Za-z0-9+.-]*:// ]]; then
  url="$(printf '%s' "$url" | sed -E 's#^([A-Za-z][A-Za-z0-9+.-]*://)[^/@]*@#\1#')"
elif [[ "$url" =~ ^[A-Za-z0-9._-]+@([A-Za-z0-9.-]+):(.+)$ ]]; then
  url="https://${BASH_REMATCH[1]}/${BASH_REMATCH[2]}"
else
  echo "refusing: cannot read the origin url '$url'" >&2; exit 1
fi
url="${url%/}"; url="${url%.git}"

tmp="$(mktemp -d "${TMPDIR:-/tmp}/director-recipes-stamp.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/lib"
cp -R "$lib/." "$tmp/lib/"
python3 - "$tmp/lib/company.toml" "${date}-${short}" "$url" "$commit" <<'PY'
import re, sys, tomllib
path, version, url, commit = sys.argv[1:5]
text = open(path).read()
d = tomllib.loads(text)
for k in ("version", "source"):
    if k in d:
        sys.exit(f"company.toml already carries {k}; the committed file is not stamped")
# name, then version, at the top level; source after the other top-level keys
head, sep, rest = text.partition("\n")
lines = text.rstrip("\n").split("\n")
out = []
for line in lines:
    out.append(line)
    if re.match(r'^name\s*=', line):
        out.append(f'version = "{version}"')
out.append(f'source = {{ url = "{url}", commit = "{commit}" }}')
open(path, "w").write("\n".join(out) + "\n")
tomllib.loads(open(path).read())
PY
mkdir -p "$out"
cp -R "$tmp/lib/." "$out/"
echo "stamped $out: version ${date}-${short}, commit $commit"
