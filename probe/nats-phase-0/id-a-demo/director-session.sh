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
# Usage:
#   ./director-session.sh <distinct-agent-id> [extra claude args...]
# Example (two terminals):
#   ./director-session.sh planner
#   ./director-session.sh builder
#
# The director SEAT: pass a stable id and resume to keep context. The id is the
# durable handle (R-06), so use the same one every launch; it replaces the
# static DIRECTOR_AGENT_ID=michael baked into the project-scope config:
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
# project-scoped director-mcp (baked DIRECTOR_AGENT_ID=michael) does NOT also
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
# project-scoped director-mcp (baked DIRECTOR_AGENT_ID=michael) is bypassed.
# Caveat: if an enterprise managed-mcp.json is deployed on this host,
# --strict-mcp-config makes claude exit at startup by design; that is not
# present on a personal machine.
json="{\"mcpServers\":{\"director\":{\"command\":\"$BIN\",\"env\":{\"DIRECTOR_AGENT_ID\":\"$id\",\"DIRECTOR_TEAM\":\"$TEAM\",\"DIRECTOR_WORKSPACE\":\"$WORKSPACE\",\"NATS_URL\":\"$NATS_URL\"$global_env}}}}"

echo "director-session: launching claude as agent://$TEAM/$id (workspace $WORKSPACE) on $NATS_URL" >&2
exec claude --strict-mcp-config --mcp-config "$json" "$@"
