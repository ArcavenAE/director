#!/usr/bin/env bash
# Launcher for the marvel twin (director brief 7, S0). marvel runs this as the
# role's command with `image = "claude"`, so the claude adapter's flags arrive
# in "$@" and are passed through to claude unchanged. The wrapper does four
# things and nothing else:
#   1. slices the wardrobe role for MARVEL_ROLE (scripts/slice.sh prints the
#      slice and appends the spawn line; ruling 84: the helper prints and
#      appends, the launcher execs);
#   2. sets the session's bus identity from one source (DIRECTOR_AGENT_ID
#      from MARVEL_SESSION unless marvel already set it), and wires the
#      director shim in with --strict-mcp-config so no project-scoped shim
#      with a baked id loads beside it (R-49);
#   3. decides, per role, whether this session holds a global address at all,
#      and hands the global tier's three levers to the roles that do while
#      clearing them for every role that does not (R-86, R-94; see below);
#   4. execs claude with ONE --append-system-prompt: the slice, or a caller's full
#      cast, with any other caller prompt folded in (aae-orc-1vq6z).
# Refusals: slice.sh owns the wardrobe ones (dirty tree, writable root,
# retirement marker, proposal without the flag, write-floor proposal); this
# script owns the ones about the session it is about to start (the id class,
# TWIN_CWD, the global-tier levers, the bus pre-flight).
set -euo pipefail

: "${MARVEL_ROLE:?cast-launch: MARVEL_ROLE is set by marvel baseEnv; run under marvel}"
: "${MARVEL_SESSION:?cast-launch: MARVEL_SESSION is set by marvel baseEnv; run under marvel}"

# The wardrobe install root: a clean, read-only checkout at the commit the
# operator names (precondition 1 wants a tag; until one exists WARDROBE_REF
# records the sha the twin was cast from, and the spawn line carries tree=).
WARDROBE_ROOT="${WARDROBE_ROOT:-$HOME/.local/share/wardrobe/contents}"
# ~/.director is already this tool's on-host home: the leaf seed lives under
# ~/.director/nats/, the hub's own state under ~/.director/nats-global/. The
# built shim belongs beside them, so that is the default. It exists because the
# alternative measured on the live fleet was a path into a session scratchpad
# under /private/tmp: the moment that directory is reaped every later cast
# fails the -x check below, with 11 sessions already running against it.
# DIRECTOR_SHIM_BIN still wins, so a host that builds elsewhere names its path.
SHIM_BIN="${DIRECTOR_SHIM_BIN:-$HOME/.director/bin/director-mcp}"
NATS_URL="${NATS_URL:-nats://127.0.0.1:4222}"
DIRECTOR_TEAM="${DIRECTOR_TEAM:-${MARVEL_TEAM:-fleet}}"
DIRECTOR_WORKSPACE="${DIRECTOR_WORKSPACE:-${MARVEL_WORKSPACE:-ops2}}"

# Per-role backend overlay (mixed-mode fast-path). If an overlay exists for
# this manifest role under MARVEL_OVERLAY_ROOT/by-role/, it is attached to the
# child as --settings so its env block selects the backend. The root is fixed
# at the launcher's own directory, computed here before the cd to TWIN_CWD
# below, so a relative overlay path can never resolve against the post-cd cwd.
# The default root sits beside the launcher (normally empty, so no change);
# MARVEL_OVERLAY_ROOT names an out-of-tree overlay set.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MARVEL_OVERLAY_ROOT="${MARVEL_OVERLAY_ROOT:-$SCRIPT_DIR/overlays}"

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
[[ -x "$SHIM_BIN" ]] || { echo "cast-launch: director shim not executable at $SHIM_BIN; build and install it there (go build -o \"\$HOME/.director/bin/director-mcp\" ./probe/nats-phase-0/director-mcp) or set DIRECTOR_SHIM_BIN to its path. Never point it inside a temporary directory: a scratchpad path is reaped and every later cast fails here" >&2; exit 1; }
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
# The session's role, so the shim reads its role inbox and the roster can
# resolve role://<team>/<role> to a live holder. The manifest role is the name
# senders address; marvel wins if it already set DIRECTOR_ROLE.
export DIRECTOR_ROLE="${DIRECTOR_ROLE:-$MARVEL_ROLE}"
if [[ ! "$DIRECTOR_ROLE" =~ ^[A-Za-z0-9_-]+$ ]]; then
  echo "cast-launch: refusing role '$DIRECTOR_ROLE'; the class is [A-Za-z0-9_-] (R-76), never rewritten" >&2; exit 1
fi
# The global tier (R-86, R-94; design brief 8, sim/design/global-bus-tier.md).
# Off unless the operator sets DIRECTOR_GLOBAL_DOMAIN and DIRECTOR_CLUSTER on
# the marvel daemon. That is a per-DAEMON switch, and the tier is a per-ROLE
# property, so this is where the two meet: exactly two role words exist at the
# global tier, supervisor and director, and a worker never holds a global
# address (R-94).
#
# Applying them blanket breaks the fleet. The shim refuses a DIRECTOR_GLOBAL_ROLE
# outside those two words at spawn, before it opens a connection, so a builder or
# an envoy that saw the domain would fail the pre-flight below and marvel would
# crash-loop it.
#
# Leaving them out of mcp_json is NOT enough to prevent that. The shim inherits
# this process's environment and mcp_json's env map is merged over it, so a value
# set on the daemon reaches every session cast from it whatever mcp_json says.
# The roles that hold no global address therefore get the levers UNSET here, and
# the unset happens before the pre-flight, which runs the same shim.
#
# The role is derived from the cast, never read from the environment: the
# operator names the cluster, the wardrobe role decides whether this session has
# a global address at all.
case "$WROLE" in
  supervisor) GROLE=supervisor;;
  director)   GROLE=director;;
  *)          GROLE="";;
esac

if [[ -n "${DIRECTOR_GLOBAL_DOMAIN:-}" && -n "$GROLE" ]]; then
  : "${DIRECTOR_CLUSTER:?cast-launch: DIRECTOR_GLOBAL_DOMAIN is set and this cast holds a global address, so DIRECTOR_CLUSTER must name this cluster (R-94); set both on the marvel daemon or neither}"
  # Both become subject tokens ("$JS.<domain>.API.>", "global.<cluster>.>") and
  # both are interpolated into the mcp_json below, so each takes the same class
  # check the id takes: rejected, never rewritten (R-76).
  if [[ ! "$DIRECTOR_GLOBAL_DOMAIN" =~ ^[A-Za-z0-9_-]+$ ]]; then
    echo "cast-launch: refusing DIRECTOR_GLOBAL_DOMAIN '$DIRECTOR_GLOBAL_DOMAIN'; the class is [A-Za-z0-9_-] (R-76)" >&2; exit 1
  fi
  if [[ ! "$DIRECTOR_CLUSTER" =~ ^[A-Za-z0-9_-]+$ ]]; then
    echo "cast-launch: refusing DIRECTOR_CLUSTER '$DIRECTOR_CLUSTER'; the class is [A-Za-z0-9_-] (R-76)" >&2; exit 1
  fi
  if [[ -n "${DIRECTOR_GLOBAL_ROLE:-}" && "${DIRECTOR_GLOBAL_ROLE}" != "$GROLE" ]]; then
    echo "cast-launch: DIRECTOR_GLOBAL_ROLE='$DIRECTOR_GLOBAL_ROLE' in the environment contradicts the cast (role/$WROLE derives '$GROLE'); the global role comes from the cast, not from the daemon" >&2; exit 1
  fi
  export DIRECTOR_GLOBAL_DOMAIN DIRECTOR_CLUSTER
  export DIRECTOR_GLOBAL_ROLE="$GROLE"
  global_env="$(printf ',"DIRECTOR_GLOBAL_DOMAIN":"%s","DIRECTOR_CLUSTER":"%s","DIRECTOR_GLOBAL_ROLE":"%s"' \
    "$DIRECTOR_GLOBAL_DOMAIN" "$DIRECTOR_CLUSTER" "$DIRECTOR_GLOBAL_ROLE")"
  # The director is one seat for the whole fleet, not one per cluster (R-94).
  if [[ "$DIRECTOR_GLOBAL_ROLE" == director ]]; then
    GLOBAL_ADDR="global://director"
  else
    GLOBAL_ADDR="global://$DIRECTOR_CLUSTER/$DIRECTOR_GLOBAL_ROLE"
  fi
else
  unset DIRECTOR_GLOBAL_DOMAIN DIRECTOR_CLUSTER DIRECTOR_GLOBAL_ROLE
  global_env=""
  GLOBAL_ADDR=""
fi

mcp_json="$(printf '{"mcpServers":{"director":{"command":"%s","env":{"DIRECTOR_AGENT_ID":"%s","DIRECTOR_ROLE":"%s","DIRECTOR_TEAM":"%s","DIRECTOR_WORKSPACE":"%s","NATS_URL":"%s"%s}}}}' \
  "$SHIM_BIN" "$DIRECTOR_AGENT_ID" "$DIRECTOR_ROLE" "$DIRECTOR_TEAM" "$DIRECTOR_WORKSPACE" "$NATS_URL" "$global_env")"

cast_line="You are cast as wardrobe role/$WROLE for the manifest role $MARVEL_ROLE in team $DIRECTOR_TEAM, address agent://$DIRECTOR_TEAM/$DIRECTOR_AGENT_ID."
[[ -n "$SCOPE" ]] && cast_line+=" Your scope, set at cast time and recorded by the supervisor: $SCOPE."
cast_line+=" Your first act is to echo the last line of the spawn log (ruling 84)."

# Exactly one system prompt (aae-orc-1vq6z). claude keeps only the LAST
# --append-system-prompt, so any flag in "$@" would silently replace the slice.
# Strip every pair (and the =value form) from "$@" and pass one prompt:
#   - a caller prompt that begins with this launcher's own cast sentence is a
#     full cast a per-workspace wrapper already rendered (with the true scope
#     and its standing-instructions pointer), so it IS the prompt, and the
#     slice is not repeated;
#   - any other stripped text (marvel's one-line "You are <session> (role:
#     ...)") is folded in after the slice rather than replacing it.
prompt="$cast_line"$'\n\n'"$slice"
caller_cast=""
caller_extra=()
passthrough=()
take_next=0
for a in "$@"; do
  if ((take_next)); then
    take_next=0
    value="$a"
  elif [[ "$a" == --append-system-prompt ]]; then
    take_next=1
    continue
  elif [[ "$a" == --append-system-prompt=* ]]; then
    value="${a#--append-system-prompt=}"
  else
    passthrough+=("$a")
    continue
  fi
  if [[ "$value" == "You are cast as wardrobe role/"* ]]; then
    caller_cast="$value"
  else
    caller_extra+=("$value")
  fi
done
[[ -n "$caller_cast" ]] && prompt="$caller_cast"
for x in ${caller_extra[@]+"${caller_extra[@]}"}; do
  prompt+=$'\n\n'"$x"
done
set -- ${passthrough[@]+"${passthrough[@]}"}

# The session's working directory must be one the harness already trusts on
# this host: a pane inherits the tmux server's directory (marvel passes no
# start directory today), so an untrusted daemon cwd would stop claude at the
# trust dialog before the shim ever loads. TWIN_CWD is required, with no
# default, so a second host names its own trusted path rather than inheriting
# one operator's home; marvel#255 (MARVEL_WORKDIR) retires this shim cd once it
# ships. Never bypass the dialog with dangerous permissions: the twin runs at
# the read floor.
TWIN_CWD="${TWIN_CWD:?cast-launch: set TWIN_CWD to a directory the harness trusts on THIS host; there is no default, so a wrong same-named path cannot launch a session in the wrong place silently}"
cd "$TWIN_CWD" || { echo "cast-launch: cannot cd to $TWIN_CWD (set TWIN_CWD to a trusted workspace)" >&2; exit 1; }

# Pre-flight the bus before the harness starts (finding-166, candidate R-93):
# the shim as an MCP server exits nonzero on an unreachable or unprovisioned
# broker, but --strict-mcp-config lets claude start without it, and the session
# then reports running with no presence. Crash here instead, so marvel sees a
# failed session and backs off loudly. --preflight creates no consumer and
# writes no presence.
DIRECTOR_TEAM="$DIRECTOR_TEAM" DIRECTOR_WORKSPACE="$DIRECTOR_WORKSPACE" NATS_URL="$NATS_URL" \
  "$SHIM_BIN" --preflight \
  || { echo "cast-launch: bus pre-flight failed for agent://$DIRECTOR_TEAM/$DIRECTOR_AGENT_ID on $NATS_URL; not starting the harness (finding-166)" >&2; exit 1; }

# Resolve the per-role overlay just before exec. Missing file: no change
# (default behavior). Malformed JSON: crash non-zero, matching the bus
# pre-flight loud-failure precedent (finding-166), so a broken backend
# selector fails the launch rather than starting a session on the wrong
# backend silently.
#
# marvel already projects the session's policy as a --settings file that
# arrives in "$@", and claude takes the LAST --settings wholesale (no merge
# across two --settings). So the overlay cannot be a second bare --settings: as
# an earlier flag it is overridden by marvel's policy; as a later flag it drops
# that policy. Merge instead: marvel's policy first, the overlay on top so it
# wins on the backend keys while the policy is preserved, and pass the merged
# file as the last --settings. jq is required for the merge; its absence with an
# overlay present is itself a loud failure.
settings_args=()
overlay_file="$MARVEL_OVERLAY_ROOT/by-role/$MARVEL_ROLE.json"
if [[ -f "$overlay_file" ]]; then
  command -v jq >/dev/null 2>&1 \
    || { echo "cast-launch: jq is required to attach a backend overlay but is not installed; not starting the harness (finding-166)" >&2; exit 1; }
  jq -e . "$overlay_file" >/dev/null 2>&1 \
    || { echo "cast-launch: backend overlay $overlay_file is not valid JSON; not starting the harness (finding-166)" >&2; exit 1; }
  # marvel's projected policy is the last --settings already in "$@" (if any).
  policy_settings=""; prev=""
  for a in "$@"; do [[ "$prev" == "--settings" ]] && policy_settings="$a"; prev="$a"; done
  merged="$(mktemp "${TMPDIR:-/tmp}/cast-overlay.XXXXXX")"
  if [[ -n "$policy_settings" && -f "$policy_settings" ]]; then
    jq -s '.[0] * .[1]' "$policy_settings" "$overlay_file" > "$merged" \
      || { echo "cast-launch: failed to merge marvel policy $policy_settings with backend overlay $overlay_file; not starting the harness (finding-166)" >&2; exit 1; }
  else
    cp "$overlay_file" "$merged"
  fi
  settings_args=(--settings "$merged")
  echo "cast-launch: attaching backend overlay $overlay_file for role $MARVEL_ROLE (merged over marvel policy as --settings $merged)" >&2
fi

echo "cast-launch: $MARVEL_SESSION -> role/$WROLE identity=${IDENTITY:-none} as agent://$DIRECTOR_TEAM/$DIRECTOR_AGENT_ID${GLOBAL_ADDR:+ and $GLOBAL_ADDR} on $NATS_URL, cwd $TWIN_CWD" >&2
exec claude -n "$DIRECTOR_AGENT_ID" \
  --strict-mcp-config --mcp-config "$mcp_json" \
  --append-system-prompt "$prompt" \
  "$@" \
  ${settings_args[@]+"${settings_args[@]}"}
