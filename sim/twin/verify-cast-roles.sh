#!/usr/bin/env bash
# Check that every role a team manifest casts through cast-launch resolves to a
# wardrobe role, so a new manifest role that falls through cast-launch.sh's
# `*) WROLE="$MARVEL_ROLE"` row to a role the wardrobe does not have fails here
# and not at cast (director#99, #146, #162, #174, #622, #638, #721, #723).
#
# Which table applies depends on the command. Under cast-launch (or cast-launch.sh)
# a role resolves when it is a row of the case table in cast-launch.sh and the row's
# WROLE target exists in the wardrobe roles directory, or when a same-name role file
# exists there. Under any other cast-* wrapper the table is not credited, because a
# wrapper may slice MARVEL_ROLE directly or keep a table of its own; the role then
# needs a same-name role file, unless --table NAME=FILE names the file whose case table
# that wrapper applies (a wrapper that execs cast-launch after its own mapping keeps
# a table of its own; point --table at the file that holds it). A table row decides
# the role's wardrobe target; with no row, or no table, the role is its own name. Roles whose command is not a cast-* launcher or
# wrapper do not go through the fallback and are counted as skipped. It reads files
# only: no marvel call, no write anywhere.
#
# Usage: sim/twin/verify-cast-roles.sh [--launcher FILE] [--table NAME=FILE]... [--wardrobe DIR] MANIFEST...
#   MANIFEST   a .yaml or .yml file (teams[].roles[]) or a .toml file (team[].role[]),
#              as marvel manifests are written; read with yq (v4.40 or newer)
#   --launcher cast-launch.sh to read the case table from (default: the one beside this file)
#   --table    credit FILE's case table to commands whose basename is NAME (repeatable)
#   --wardrobe the wardrobe roles directory (default: $WARDROBE_ROOT/roles, and
#              WARDROBE_ROOT defaults to ~/.local/share/wardrobe/contents as in cast-launch.sh)
# Exit 0 when every role resolves, 1 when any does not (each is named), 2 on a usage error.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
launcher="$here/cast-launch.sh"
wroot="${WARDROBE_ROOT:-$HOME/.local/share/wardrobe/contents}"
wdir="$wroot/roles"
manifests=(); tables=()
while (( $# )); do
  case "$1" in
    --launcher) launcher="${2:?--launcher needs a file}"; shift 2;;
    --table) [[ "${2:-}" == ?*=?* ]] || { echo "verify-cast-roles: --table needs NAME=FILE" >&2; exit 2; }
             tables+=("$2"); shift 2;;
    --wardrobe) wdir="${2:?--wardrobe needs a directory}"; shift 2;;
    -*) echo "verify-cast-roles: unknown option $1" >&2; exit 2;;
    *) manifests+=("$1"); shift;;
  esac
done
(( ${#manifests[@]} )) || { echo "verify-cast-roles: no manifest given" >&2; exit 2; }
[[ -f "$launcher" ]] || { echo "verify-cast-roles: no launcher at $launcher" >&2; exit 2; }
[[ -d "$wdir" ]] || { echo "verify-cast-roles: no wardrobe roles directory at $wdir" >&2; exit 2; }
command -v yq >/dev/null || { echo "verify-cast-roles: yq not on PATH" >&2; exit 2; }

# The rows of the first `case "$MARVEL_ROLE" in` block of a file, one "role<TAB>target"
# per line, target being the WROLE the row sets (empty when the row sets none).
# A row may be a pattern such as ab852-*. A row label starts with a letter or a
# digit, so the `*)` catch-all is never read as a row.
extract_table() {
  awk '
    /^case "\$MARVEL_ROLE" in/ { on=1; next }
    on && /^esac/ { exit }
    on && /^  [a-z0-9][a-z0-9|*-]*\)/ {
      s=$1; sub(/\).*/, "", s); t=""
      if (match($0, /WROLE=[a-z0-9-]+/)) t=substr($0, RSTART+6, RLENGTH-6)
      n=split(s, a, "|"); for (i=1;i<=n;i++) print a[i] "\t" t
    }
  ' "$1"
}
launch_table="$(extract_table "$launcher")"
[[ -n "$launch_table" ]] || { echo "verify-cast-roles: no case table found in $launcher" >&2; exit 2; }
for t in ${tables[@]+"${tables[@]}"}; do
  [[ -f "${t#*=}" ]] || { echo "verify-cast-roles: --table file ${t#*=} does not exist" >&2; exit 2; }
  [[ -n "$(extract_table "${t#*=}")" ]] || { echo "verify-cast-roles: no case table found in ${t#*=}" >&2; exit 2; }
done
# The case table that applies to a command basename: cast-launch's own, a --table file's, or none.
table_for() {
  case "$1" in cast-launch|cast-launch.sh) printf '%s\n' "$launch_table"; return 0;; esac
  local t
  for t in ${tables[@]+"${tables[@]}"}; do
    [[ "${t%%=*}" == "$1" ]] && { extract_table "${t#*=}"; return 0; }
  done
  return 1
}

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
    base="$(basename "$cmd")"
    if table="$(table_for "$base")"; then
      # The first row that matches wins, as in a shell case; "*" is the only
      # pattern character a row label can carry.
      row="$(awk -F'\t' -v r="$role" '{ re=$1; gsub(/\*/, ".*", re) } r ~ ("^" re "$") { print "row\t" $2; exit }' <<<"$table")"
      if [[ -n "$row" ]]; then
        target="${row#row$'\t'}"
        if [[ -n "$target" && ! -f "$wdir/$target.md" ]]; then
          echo "UNRESOLVED role '$role' in $m: the $base case table maps it to '$target' and no $wdir/$target.md exists" >&2
          miss=$((miss+1))
        fi
        continue
      fi
      # no row: the role falls through to its own name
      [[ -f "$wdir/$role.md" ]] && continue
      echo "UNRESOLVED role '$role' in $m: not in the $base case table and no $wdir/$role.md" >&2
    else
      [[ -f "$wdir/$role.md" ]] && continue
      echo "UNRESOLVED role '$role' in $m: command $base is not cast-launch and no --table names its case table, and no $wdir/$role.md" >&2
    fi
    miss=$((miss+1))
  done <<<"$rows"
done
echo "verify-cast-roles: $seen cast roles checked, $skipped skipped (not a cast-* command), $miss unresolved"
(( miss == 0 ))
