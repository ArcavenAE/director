#!/usr/bin/env bash
# Check that every role a team manifest casts through cast-launch resolves to a
# wardrobe role, so a new manifest role that falls through cast-launch.sh's
# `*) WROLE="$MARVEL_ROLE"` row to a role the wardrobe does not have fails here
# and not at cast (director#99, #146, #162, #174, #622, #638, #721, #723).
#
# A role resolves when it is a row of the case table in cast-launch.sh, or when a
# same-name role file exists in the wardrobe roles directory. Roles whose command
# is not a cast-* launcher or wrapper do not go through the fallback and are
# counted as skipped. It reads files only: no marvel call, no write anywhere.
#
# Usage: sim/twin/verify-cast-roles.sh [--launcher FILE] [--wardrobe DIR] MANIFEST...
#   MANIFEST   a .yaml or .yml file (teams[].roles[]) or a .toml file (team[].role[]),
#              as marvel manifests are written; read with yq (v4.40 or newer)
#   --launcher cast-launch.sh to read the case table from (default: the one beside this file)
#   --wardrobe the wardrobe roles directory (default: $WARDROBE_ROOT/roles, and
#              WARDROBE_ROOT defaults to ~/.local/share/wardrobe/contents as in cast-launch.sh)
# Exit 0 when every role resolves, 1 when any does not (each is named), 2 on a usage error.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
launcher="$here/cast-launch.sh"
wroot="${WARDROBE_ROOT:-$HOME/.local/share/wardrobe/contents}"
wdir="$wroot/roles"
manifests=()
while (( $# )); do
  case "$1" in
    --launcher) launcher="${2:?--launcher needs a file}"; shift 2;;
    --wardrobe) wdir="${2:?--wardrobe needs a directory}"; shift 2;;
    -*) echo "verify-cast-roles: unknown option $1" >&2; exit 2;;
    *) manifests+=("$1"); shift;;
  esac
done
(( ${#manifests[@]} )) || { echo "verify-cast-roles: no manifest given" >&2; exit 2; }
[[ -f "$launcher" ]] || { echo "verify-cast-roles: no launcher at $launcher" >&2; exit 2; }
[[ -d "$wdir" ]] || { echo "verify-cast-roles: no wardrobe roles directory at $wdir" >&2; exit 2; }
command -v yq >/dev/null || { echo "verify-cast-roles: yq not on PATH" >&2; exit 2; }

# The rows of the first `case "$MARVEL_ROLE" in` block, one role name per line.
table="$(awk '
  /^case "\$MARVEL_ROLE" in/ { on=1; next }
  on && /^esac/ { exit }
  on && /^  [a-z0-9|-]+\)/ { s=$1; sub(/\).*/, "", s); n=split(s, a, "|"); for (i=1;i<=n;i++) print a[i] }
' "$launcher")"
[[ -n "$table" ]] || { echo "verify-cast-roles: no case table found in $launcher" >&2; exit 2; }

miss=0; seen=0; skipped=0
for m in "${manifests[@]}"; do
  [[ -f "$m" ]] || { echo "verify-cast-roles: no manifest at $m" >&2; exit 2; }
  case "$m" in
    *.toml) rows="$(yq -p toml -o tsv '.team[].role[] | [.name, (.runtime.command // "")]' "$m")";;
    *.yaml|*.yml) rows="$(yq -o tsv '.teams[].roles[] | [.name, (.runtime.command // "")]' "$m")";;
    *) echo "verify-cast-roles: $m is not .yaml, .yml or .toml" >&2; exit 2;;
  esac
  while IFS=$'\t' read -r role cmd; do
    [[ -n "$role" ]] || continue
    if [[ "$(basename "$cmd")" != cast-* ]]; then skipped=$((skipped+1)); continue; fi
    seen=$((seen+1))
    if grep -qxF -- "$role" <<<"$table" || [[ -f "$wdir/$role.md" ]]; then continue; fi
    echo "UNRESOLVED role '$role' in $m: not in the cast-launch case table and no $wdir/$role.md" >&2
    miss=$((miss+1))
  done <<<"$rows"
done
echo "verify-cast-roles: $seen cast roles checked, $skipped skipped (not a cast-* command), $miss unresolved"
(( miss == 0 ))
