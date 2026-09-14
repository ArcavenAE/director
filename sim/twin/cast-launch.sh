#!/usr/bin/env bash
# Launcher for the marvel twin (director brief 7, S0). marvel runs this as the
# role's command with `image = "claude"`, so the claude adapter's flags arrive
# in "$@" and are passed through to claude unchanged. The wrapper does three
# things and nothing else:
#   1. slices the wardrobe role for MARVEL_ROLE (scripts/slice.sh prints the
#      slice and appends the spawn line; ruling 84: the helper prints and
#      appends, the launcher execs);
#   2. sets the session's bus identity from one source (DIRECTOR_AGENT_ID
#      from MARVEL_SESSION unless marvel already set it), and wires the
#      director shim in with --strict-mcp-config so no project-scoped shim
#      with a baked id loads beside it (R-49);
#   3. execs claude with the slice as --append-system-prompt.
# Refusals come from slice.sh (dirty tree, writable root, retirement marker,
# proposal without the flag, write-floor proposal); this script adds none.
set -euo pipefail

: "${MARVEL_ROLE:?cast-launch: MARVEL_ROLE is set by marvel baseEnv; run under marvel}"
: "${MARVEL_SESSION:?cast-launch: MARVEL_SESSION is set by marvel baseEnv; run under marvel}"

# The wardrobe install root: a clean, read-only checkout at the commit the
# operator names (precondition 1 wants a tag; until one exists WARDROBE_REF
# records the sha the twin was cast from, and the spawn line carries tree=).
WARDROBE_ROOT="${WARDROBE_ROOT:-$HOME/.local/share/wardrobe/contents}"
SHIM_BIN="${DIRECTOR_SHIM_BIN:-/Users/michael.pursifull/work/aae-orc/director/probe/nats-phase-0/director-mcp/director-mcp}"
NATS_URL="${NATS_URL:-nats://127.0.0.1:4222}"
DIRECTOR_TEAM="${DIRECTOR_TEAM:-${MARVEL_TEAM:-fleet}}"
DIRECTOR_WORKSPACE="${DIRECTOR_WORKSPACE:-${MARVEL_WORKSPACE:-ops2}}"

# Manifest role name -> wardrobe role id, plus the cast-time scope the
# supervisor's cast record carries (brief 7, 2.2). One wardrobe builder role
# serves three manifest rows; the scope is a parameter, not a role.
case "$MARVEL_ROLE" in
  maintainer)        WROLE=reader;  SCOPE="";;
  builder)           WROLE=builder; SCOPE="general (kos, fleet CI, stave, sidestep, bloomctl, critic, beadle, curtain, ThreeDoors, BetterDials)";;
  marvel-builder)    WROLE=builder; SCOPE="marvel";;
  sideshow-builder)  WROLE=builder; SCOPE="sideshow and sideshow-packs";;
  *)                 WROLE="$MARVEL_ROLE"; SCOPE="";;
esac
# Identities that compose with a role (sole_fit at proposal): director and envoy.
case "$WROLE" in
  director) IDENTITY=assistive-agent;;
  envoy)    IDENTITY=front-office;;
  *)        IDENTITY="";;
esac

[[ -d "$WARDROBE_ROOT/roles" ]] || { echo "cast-launch: no wardrobe root at $WARDROBE_ROOT (set WARDROBE_ROOT)" >&2; exit 1; }
[[ -x "$SHIM_BIN" ]] || { echo "cast-launch: director shim not executable at $SHIM_BIN (set DIRECTOR_SHIM_BIN)" >&2; exit 1; }
slice_sh="$(dirname "$WARDROBE_ROOT")/scripts/slice.sh"
[[ -x "$slice_sh" ]] || { echo "cast-launch: $slice_sh missing; the install root is a full checkout" >&2; exit 1; }

# The slice, and the spawn line as a side effect (precondition 3: a failed
# log write is a refusal inside slice.sh). ALLOW_PROPOSAL=0 to cast ratified only.
slice_args=("$WARDROBE_ROOT" "$WROLE")
[[ -n "$IDENTITY" ]] && slice_args+=("$IDENTITY")
[[ "${ALLOW_PROPOSAL:-1}" == 1 ]] && slice_args+=(--allow-proposal)
[[ "${ALLOW_DIRTY:-0}" == 1 ]] && slice_args+=(--allow-dirty)
slice="$("$slice_sh" "${slice_args[@]}")"

# One source for the bus id (R-73). marvel's computed per-replica name until
# the declared identity block exists; marvel wins if it already set it.
export DIRECTOR_AGENT_ID="${DIRECTOR_AGENT_ID:-$MARVEL_SESSION}"
if [[ ! "$DIRECTOR_AGENT_ID" =~ ^[a-z][a-z0-9]*(-[a-z0-9]+)*$ ]]; then
  echo "cast-launch: refusing id '$DIRECTOR_AGENT_ID'; it must match the closed class (R-76), never rewritten" >&2; exit 1
fi
mcp_json="$(printf '{"mcpServers":{"director":{"command":"%s","env":{"DIRECTOR_AGENT_ID":"%s","DIRECTOR_TEAM":"%s","DIRECTOR_WORKSPACE":"%s","NATS_URL":"%s"}}}}' \
  "$SHIM_BIN" "$DIRECTOR_AGENT_ID" "$DIRECTOR_TEAM" "$DIRECTOR_WORKSPACE" "$NATS_URL")"

cast_line="You are cast as wardrobe role/$WROLE for the manifest role $MARVEL_ROLE in team $DIRECTOR_TEAM, address agent://$DIRECTOR_TEAM/$DIRECTOR_AGENT_ID."
[[ -n "$SCOPE" ]] && cast_line+=" Your scope, set at cast time and recorded by the supervisor: $SCOPE."
cast_line+=" Your first act is to echo the last line of the spawn log (ruling 84)."

echo "cast-launch: $MARVEL_SESSION -> role/$WROLE identity=${IDENTITY:-none} as agent://$DIRECTOR_TEAM/$DIRECTOR_AGENT_ID on $NATS_URL" >&2
exec claude -n "$DIRECTOR_AGENT_ID" \
  --strict-mcp-config --mcp-config "$mcp_json" \
  --append-system-prompt "$cast_line"$'\n\n'"$slice" \
  "$@"
