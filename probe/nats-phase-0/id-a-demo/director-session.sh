#!/usr/bin/env bash
# director-session.sh: ID-A launcher (design brief 1, sim/design/identity-at-spawn.md).
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
# --strict-mcp-config makes Claude Code load ONLY the server below, so the
# project-scoped director-mcp (baked DIRECTOR_AGENT_ID=michael) does NOT also
# load and re-introduce the collision.
set -euo pipefail

id="${1:?usage: director-session.sh <distinct-agent-id> [claude args...]}"
shift || true

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

# Pass the config as a JSON STRING (the confirmed --mcp-config form); no temp
# file, nothing to clean up. --strict-mcp-config loads ONLY this server, so the
# project-scoped director-mcp (baked DIRECTOR_AGENT_ID=michael) is bypassed.
# Caveat: if an enterprise managed-mcp.json is deployed on this host,
# --strict-mcp-config makes claude exit at startup by design; that is not
# present on a personal machine.
json="{\"mcpServers\":{\"director\":{\"command\":\"$BIN\",\"env\":{\"DIRECTOR_AGENT_ID\":\"$id\",\"DIRECTOR_TEAM\":\"$TEAM\",\"DIRECTOR_WORKSPACE\":\"$WORKSPACE\",\"NATS_URL\":\"$NATS_URL\"}}}}"

echo "director-session: launching claude as agent://$TEAM/$id (workspace $WORKSPACE) on $NATS_URL" >&2
exec claude --strict-mcp-config --mcp-config "$json" "$@"
