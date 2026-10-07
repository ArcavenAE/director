#!/usr/bin/env bash
# director-session.sh: ID-A launcher (design brief 1, sim/design/identity-at-spawn.md;
# ratified by the session-identity party 2026-09-18).
#
# Assigns a DISTINCT director agent id at spawn and launches an interactive
# Claude Code session whose director-mcp shim uses only that id. This is the
# "director spawn wrapper" from ID-A: the session never picks its own name any
# more than a process picks its own pid. Combined with the shim's per-session
# durable (R-50), two sessions launched this way get two distinct addresses,
# two distinct inboxes, and two distinct presence keys, so mail is never raced.
#
# Run it from the orc root: claude starts in the cwd, and the launcher refuses
# any other directory, naming the root to cd to (aae-orc-skw0b).
# Check: probe/nats-phase-0/id-a-demo/verify-director-session.sh
#
# Usage:
#   ./director-session.sh <distinct-agent-id> [extra claude args...]
# Example (two terminals):
#   ./director-session.sh planner
#   ./director-session.sh builder
#
# The director SEAT: pass a stable id and resume to keep context. The id is the
# durable handle (R-06), so use the same one every launch; it replaces the
# static DIRECTOR_AGENT_ID=operator baked into the project-scope config:
#   ./director-session.sh director --resume director
#
# Global tier (the crossing, Path B): when DIRECTOR_GLOBAL_DOMAIN is set in the
# environment (with DIRECTOR_CLUSTER and DIRECTOR_GLOBAL_ROLE), the levers are
# passed into the shim's env so the seat comes up in global mode. They are inert
# unless set, so this same launcher serves the plain ID-A seat now and the
# crossing later. Do NOT set them until the crossing is re-cleared:
#   DIRECTOR_GLOBAL_DOMAIN=global DIRECTOR_CLUSTER=<label> DIRECTOR_GLOBAL_ROLE=director \
#     ./director-session.sh director --resume director
#
# --strict-mcp-config makes Claude Code load ONLY the server below, so the
# project-scoped director-mcp (baked DIRECTOR_AGENT_ID=operator) does NOT also
# load and re-introduce the collision.
set -euo pipefail

id="${1:?usage: director-session.sh <distinct-agent-id> [claude args...]}"
shift || true

# The id is a subject token, rejected not rewritten (R-76), same class the shim
# enforces at spawn. Fail early with a clear message rather than at connect.
if [[ ! "$id" =~ ^[A-Za-z0-9_-]+$ ]]; then
  echo "director-session: refusing agent id '$id'; the class is [A-Za-z0-9_-] (R-76)" >&2
  exit 1
fi

# claude is exec'd in the inherited cwd, and a seat launched from a directory
# that is not the orc root comes up without the orc's skills and settings, and
# limited to that subtree; from outside the orc it also loses CLAUDE.md, the
# rules, bd, aq and ax. It runs, looks healthy, and is context-broken
# (aae-orc-skw0b). So refuse, on the operator's own terms, rather than move
# them somewhere they did not ask to be. The orc root is found the way aq and
# ax find it: the nearest ancestor of the cwd, the cwd included, holding both
# CLAUDE.md and repos.yaml (find_orc_root in tools/aq and tools/ax). The walk
# is repeated here because the launcher cannot ask aq, which answers in repo
# mode inside any git checkout. The launch is allowed only when the root found
# IS the cwd. aq honours ORC_ROOT; this does not, because a seat's context comes
# from where claude starts.
find_orc_root() {
  local dir="${1:-$PWD}"
  while [[ "$dir" != "/" ]]; do
    if [[ -f "$dir/CLAUDE.md" && -f "$dir/repos.yaml" ]]; then
      printf '%s\n' "$dir"
      return 0
    fi
    dir="$(dirname "$dir")"
  done
  return 1
}
if root="$(find_orc_root)"; then
  if [[ "$root" != "$PWD" ]]; then
    echo "director-session: refusing to launch from '$PWD'; it is below the orc root, so the seat would start without the orc's skills and settings and limited to this subtree" >&2
    echo "director-session: cd '$root' and run it again; the launch directory must be the orc root, the one holding CLAUDE.md and repos.yaml" >&2
    exit 1
  fi
else
  # The logical path has no root above it. A symlink into the orc can still
  # resolve to one, so retry on the physical path to name the root to cd to.
  phys="$(pwd -P)"
  if [[ "$phys" != "$PWD" ]] && root="$(find_orc_root "$phys")"; then
    echo "director-session: refusing to launch from '$PWD'; it resolves to '$phys', below the orc root, so the seat would start without the orc's skills and settings and limited to that subtree" >&2
    echo "director-session: cd '$root' and run it again; the launch directory must be the orc root, the one holding CLAUDE.md and repos.yaml" >&2
    exit 1
  fi
  echo "director-session: refusing to launch from '$PWD'; it is not inside an orc root, so the seat would start without the orc context" >&2
  echo "director-session: cd to the orc checkout and run it again; the launch directory must be the orc root, the one holding CLAUDE.md and repos.yaml" >&2
  exit 1
fi

# The shim binary defaults to the sibling director-mcp build relative to this
# script, so the launcher is portable across hosts and checkouts; set
# DIRECTOR_SHIM_BIN to override. No hardcoded home path.
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN="${DIRECTOR_SHIM_BIN:-$here/../director-mcp/director-mcp}"
TEAM="${DIRECTOR_TEAM:-ops}"
WORKSPACE="${DIRECTOR_WORKSPACE:-aae-orc}"
NATS_URL="${NATS_URL:-nats://127.0.0.1:4222}"

if [[ ! -x "$BIN" ]]; then
  echo "director-session: shim binary not found or not executable: $BIN" >&2
  echo "build it: ( cd $(dirname "$BIN") && go build -o director-mcp . )" >&2
  exit 1
fi

# Global-tier levers, injected ONLY when DIRECTOR_GLOBAL_DOMAIN is set, so this
# launcher is inert on the local tier and crossing-ready when the operator opts
# in. The shim validates them too; validating here fails early with a clear
# message. DIRECTOR_GLOBAL_ROLE is required (director or supervisor) because the
# launcher is generic and does not derive the role from a cast the way marvel's
# cast-launch does.
global_env=""
if [[ -n "${DIRECTOR_GLOBAL_DOMAIN:-}" ]]; then
  : "${DIRECTOR_CLUSTER:?DIRECTOR_GLOBAL_DOMAIN is set, so DIRECTOR_CLUSTER must name this cluster (R-94)}"
  : "${DIRECTOR_GLOBAL_ROLE:?DIRECTOR_GLOBAL_DOMAIN is set, so DIRECTOR_GLOBAL_ROLE must be director or supervisor (R-94)}"
  for v in DIRECTOR_GLOBAL_DOMAIN DIRECTOR_CLUSTER DIRECTOR_GLOBAL_ROLE; do
    if [[ ! "${!v}" =~ ^[A-Za-z0-9_-]+$ ]]; then
      echo "director-session: refusing $v='${!v}'; the class is [A-Za-z0-9_-] (R-76)" >&2
      exit 1
    fi
  done
  global_env=",\"DIRECTOR_GLOBAL_DOMAIN\":\"$DIRECTOR_GLOBAL_DOMAIN\",\"DIRECTOR_CLUSTER\":\"$DIRECTOR_CLUSTER\",\"DIRECTOR_GLOBAL_ROLE\":\"$DIRECTOR_GLOBAL_ROLE\""
  echo "director-session: global tier ON, domain=$DIRECTOR_GLOBAL_DOMAIN cluster=$DIRECTOR_CLUSTER role=$DIRECTOR_GLOBAL_ROLE" >&2
fi

# Pass the config as a JSON STRING (the confirmed --mcp-config form); no temp
# file, nothing to clean up. --strict-mcp-config loads ONLY this server, so the
# project-scoped director-mcp (baked DIRECTOR_AGENT_ID=operator) is bypassed.
# Caveat: if an enterprise managed-mcp.json is deployed on this host,
# --strict-mcp-config makes claude exit at startup by design; that is not
# present on a personal machine.
json="{\"mcpServers\":{\"director\":{\"command\":\"$BIN\",\"env\":{\"DIRECTOR_AGENT_ID\":\"$id\",\"DIRECTOR_TEAM\":\"$TEAM\",\"DIRECTOR_WORKSPACE\":\"$WORKSPACE\",\"NATS_URL\":\"$NATS_URL\"$global_env}}}}"

echo "director-session: launching claude as agent://$TEAM/$id (workspace $WORKSPACE) on $NATS_URL" >&2
exec claude --strict-mcp-config --mcp-config "$json" "$@"
