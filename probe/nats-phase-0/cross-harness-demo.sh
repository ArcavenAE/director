#!/usr/bin/env bash
# Cross-harness director-bus demo (sub-probe 5 follow-on, 2026-09-12).
#
# Proves the bus routes distinct identities across two harnesses. Launches two
# real harness sessions with DISTINCT launcher-assigned addresses:
#   A: a headless Claude Code session   as agent://ops/cc-planner
#   B: a codex exec session             as agent://ops/codex-a
# B sends a REQUEST to A; A replies AGREE (correlated by in_reply_to); B
# receives it. Durable inboxes make the ordering forgiving.
#
# Requires: a running broker (nats-server -c nats-server.conf), the director-mcp
# binary built alongside this script, and claude + codex on PATH.
#
# The point of the demo is the RECIPE as much as the result: it is the worked
# example of assigning identity at the launcher (distinct ids per session),
# which is the fix for the michael collision (see PROGRESS.md and requirements
# R-49). Do not give two sessions the same id here; that is the collision.
set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
BIN="${DIRECTOR_MCP_BIN:-$HERE/director-mcp/director-mcp}"
NATS="${NATS_URL:-nats://127.0.0.1:4222}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"; pkill -f "DIRECTOR_AGENT_ID=cc-planner" 2>/dev/null; pkill -f "DIRECTOR_AGENT_ID=codex-a" 2>/dev/null' EXIT

test -x "$BIN" || { echo "director-mcp binary not found at $BIN (build it first)"; exit 1; }
command -v claude >/dev/null || { echo "claude not on PATH"; exit 1; }
command -v codex  >/dev/null || { echo "codex not on PATH"; exit 1; }

# Participant A: headless Claude Code, distinct id cc-planner. Its MCP config is
# passed inline (--mcp-config), the tools are allowlisted so a non-interactive
# run can call them without a prompt, and the id is assigned HERE by the caller.
CC_MCP='{"mcpServers":{"director":{"command":"'"$BIN"'","env":{"DIRECTOR_AGENT_ID":"cc-planner","DIRECTOR_TEAM":"ops","DIRECTOR_WORKSPACE":"aae-orc","NATS_URL":"'"$NATS"'"}}}}'
(
  timeout 260 claude -p \
    --mcp-config "$CC_MCP" \
    --allowedTools "mcp__director__set_presence,mcp__director__wait_for_message,mcp__director__send_message,mcp__director__list_roster" \
    --output-format text \
    "You are on the director bus as agent://ops/cc-planner. Do exactly this, then stop: (1) mcp__director__set_presence state=busy; (2) mcp__director__wait_for_message timeout_seconds=120; (3) on receipt, mcp__director__send_message to the sender's address agent://ops/<sender agent_id>, performative=AGREE, in_reply_to=<received message_id>, text='cc-planner (Claude Code) agrees; distinct identities route correctly'; (4) report the sender, the received text, and the message_id you replied with." \
    > "$WORK/A_ccplanner.txt" 2>"$WORK/A_err.txt"
  echo "[A exit $?]" >> "$WORK/A_ccplanner.txt"
) &
APID=$!

sleep 3  # let A register presence and park in wait_for_message

# Participant B: codex exec, distinct id codex-a. director-mcp is injected via
# -c overrides on top of the user config (auth/model stay), never editing the
# user's config.toml. Gotchas encoded below: --skip-git-repo-check (non-git
# cwd), --dangerously-bypass-approvals-and-sandbox (else codex refuses the MCP
# tool call under approval_policy=never), and stdin from /dev/null (else codex
# exec blocks reading stdin when it is a pipe).
(
  timeout 260 codex exec \
    --skip-git-repo-check --dangerously-bypass-approvals-and-sandbox \
    -c "mcp_servers.director.command=\"$BIN\"" \
    -c 'mcp_servers.director.env.DIRECTOR_AGENT_ID="codex-a"' \
    -c 'mcp_servers.director.env.DIRECTOR_TEAM="ops"' \
    -c 'mcp_servers.director.env.DIRECTOR_WORKSPACE="aae-orc"' \
    -c "mcp_servers.director.env.NATS_URL=\"$NATS\"" \
    -C "$WORK" \
    "You have MCP tools from a server named 'director', on the bus as agent://ops/codex-a. Do exactly this then stop: (1) director set_presence state=busy; (2) director send_message to='agent://ops/cc-planner' performative=REQUEST text='codex-a asks cc-planner to confirm the bus routes distinct identities'; (3) director wait_for_message timeout_seconds=120; (4) report the AGREE (sender, text) and the message_id from step 2." \
    < /dev/null > "$WORK/B_codex.txt" 2>"$WORK/B_err.txt"
  echo "[B exit $?]" >> "$WORK/B_codex.txt"
) &
BPID=$!

wait "$APID" "$BPID"

echo "===== roster (presence KV) ====="
nats --server "$NATS" kv ls AGENT_STATE 2>/dev/null
echo "===== A (cc-planner, Claude Code) ====="; tail -12 "$WORK/A_ccplanner.txt"
echo "===== B (codex-a, codex) ====="; tail -12 "$WORK/B_codex.txt"
