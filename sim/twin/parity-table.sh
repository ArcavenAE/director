#!/usr/bin/env bash
# Run recipes/parity-table.toml against a launcher: cases 4 and 6 of
# verify-cast-launch.sh as data. Broker-free and cast-free: the stubs of
# cast-stubs.sh stand in for claude, the shim and the role-library root.
#
# Usage: sim/twin/parity-table.sh [table.toml]     Exit nonzero on any failing row.
#        CAST_LAUNCH=<path> sim/twin/parity-table.sh
#   CAST_LAUNCH names the launcher to test instead of cast-launch.sh beside this
#   file, as in verify-cast-launch.sh. marvel's build runs the same table against
#   its own launch of the recipe.
# Each row prints PASS <id>, or FAIL <id> -- naming each surface and lever that
# was wrong (a leak is a lever present where the row says absent; a miss is a
# lever absent where the row says present). Needs python3 3.11 or newer.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TABLE="${1:-$here/../../recipes/parity-table.toml}"
LAUNCH="${CAST_LAUNCH:-$here/cast-launch.sh}"
[[ -x "$LAUNCH" ]] || { echo "launcher not executable at $LAUNCH" >&2; exit 2; }
[[ -f "$TABLE" ]] || { echo "table not found at $TABLE" >&2; exit 2; }
PY="${PYTHON:-python3}"

pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

# shellcheck source=cast-stubs.sh
source "$here/cast-stubs.sh"

# surface_has SURFACE NAME: is NAME on that surface?
surface_has() {
  case "$1" in
    mcp) has_mcp "$2";;
    claude) has_env "$2" claude.env;;
    preflight) has_env "$2" preflight.env;;
    *) return 2;;
  esac
}
# surface_is SURFACE NAME VALUE
surface_is() {
  case "$1" in
    mcp) in_mcp "$2" "$3";;
    claude) in_env "$2" "$3" claude.env;;
    preflight) in_env "$2" "$3" preflight.env;;
    *) return 2;;
  esac
}

rows="$("$PY" - "$TABLE" <<'PY'
import sys, tomllib
t = tomllib.load(open(sys.argv[1], "rb"))
if t.get("schema") != 1:
    sys.exit("parity table: schema must be 1")
for r in t.get("rows", []):
    env = ",".join(f"{k}={v}" for k, v in r.get("env", {}).items())
    print("\t".join([r["id"], r["role"], env, " ".join(r.get("absent", [])), " ".join(r.get("present", []))]))
PY
)" || { echo "cannot read the table $TABLE" >&2; exit 2; }
[[ -n "$rows" ]] || { echo "FAIL the table holds no rows"; exit 1; }

while IFS=$'\t' read -r id role envs absent present; do
  args=(); IFS=',' read -r -a kv <<< "$envs"; for p in ${kv[@]+"${kv[@]}"}; do [[ -n "$p" ]] && args+=("$p"); done
  if ! cast "$role" ${args[@]+"${args[@]}"}; then
    bad "$id" "the launcher refused the cast of $role: $(head -c 200 "$root/out/stderr" | tr '\n' ' ')"
    continue
  fi
  problems=""
  for a in $absent; do
    s="${a%%:*}"; n="${a#*:}"
    if surface_has "$s" "$n"; then problems+=" leak $a"; fi
  done
  for p in $present; do
    s="${p%%:*}"; rest="${p#*:}"; n="${rest%%=*}"; v="${rest#*=}"
    if ! surface_is "$s" "$n" "$v"; then problems+=" miss $p"; fi
  done
  if [[ -z "$problems" ]]; then ok "$id"; else bad "$id" "${problems# }"; fi
done <<< "$rows"

echo "$pass passed, $fail failed"
(( fail == 0 ))
