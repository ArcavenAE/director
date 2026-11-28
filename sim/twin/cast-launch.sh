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
# child as --settings so its env block selects the backend. Overlays are state,
# not code, so the default root is under the director home, never beside the
# launcher: an installed launcher is a versioned directory behind a symlink
# (director-install), and an overlay kept beside it would vanish at the next
# install. The root is absolute, so it cannot resolve against the cd to
# TWIN_CWD below. MARVEL_OVERLAY_ROOT names an out-of-tree overlay set.
MARVEL_OVERLAY_ROOT="${MARVEL_OVERLAY_ROOT:-${DIRECTOR_HOME:-$HOME/.director}/overlays}"

# Manifest role name -> wardrobe role id, plus the cast-time scope the
# supervisor's cast record carries (brief 7, 2.2). One wardrobe builder role
# serves every builder row; the scope is a parameter, not a role. CAST_SCOPE,
# when set, replaces the row's scope for any role, so a scope that must not be
# published (this file is public) is passed in from the private seat file.
case "$MARVEL_ROLE" in
  maintainer)        WROLE=reader;  SCOPE="";;
  mythos-architect)  WROLE=architect; SCOPE="";;  # I-797 operator 2026-11-25 (Mythos architect)
  builder)           WROLE=builder; SCOPE="general (kos, director, fleet CI, stave, sidestep, bloomctl, critic, beadle, curtain, ThreeDoors, BetterDials)";;
  marvel-builder)    WROLE=builder; SCOPE="marvel";;
  sideshow-builder)  WROLE=builder; SCOPE="sideshow and sideshow-packs";;
  infra-builder)     WROLE=builder; SCOPE="the infra checkout and its MCP, as named in the private seat file; production is read-only";;
  scoped-builder)    WROLE=builder; SCOPE="";;
  usher-track-2-builder|usher-track-3-builder) WROLE=builder; SCOPE="";;
  builder-i804)      WROLE=builder; SCOPE="";;  # I-804 director 2026-11-25 (/ship A/B seats; scope from CAST_SCOPE)
  ab852-*)           WROLE=builder; SCOPE="";;  # I-852 director 2026-11-28 (/ship A/B seats; scope from CAST_SCOPE)
  test-architect-b)  WROLE=test-architect; SCOPE="";;
  claude-reviewer)   WROLE=reviewer; SCOPE="";;
  daily-sweep)       WROLE=worker; SCOPE="";;
  musthaq-pr-watch)  WROLE=worker; SCOPE="";;
  beadle-board-refresh) WROLE=worker; SCOPE="";;
  *)                 WROLE="$MARVEL_ROLE"; SCOPE="";;
esac
[[ -n "${CAST_SCOPE:-}" ]] && SCOPE="$CAST_SCOPE"
# scoped-builder, the usher track builders and the ab852-* seats carry no scope
# text of their own, so they need CAST_SCOPE; a cast without one would be an
# unscoped builder, which is refused.
if [[ ( "$MARVEL_ROLE" == scoped-builder || "$MARVEL_ROLE" == usher-track-2-builder || "$MARVEL_ROLE" == usher-track-3-builder || "$MARVEL_ROLE" == ab852-* ) && -z "$SCOPE" ]]; then
  echo "cast-launch: role $MARVEL_ROLE needs CAST_SCOPE (its scope comes only from the private seat file); set CAST_SCOPE and cast again" >&2
  exit 1
fi
# The clean-room mode (wardrobe finding-wardrobe-keku items 1, 3 and 4). Chosen by
# CAST_CLEAN_ROOM_DIR, an environment variable and not a flag, because every flag
# in "$@" goes on to claude. It does three things and nothing else:
#   1. a per-seat CLAUDE_CONFIG_DIR at the path the caller gives, which must be
#      absolute, fresh (absent or empty, or holding only the settings file this
#      mode wrote before) and outside the operator's config directory, so the seat
#      shares none of the operator's CLAUDE.md, imports, memory or projects;
#   2. no director shim: claude is handed an empty strict MCP config, the bus
#      pre-flight, the relay log and the global tier are skipped, no DIRECTOR_*,
#      NATS_URL or RELAY_LOG variable reaches claude, and no channel cue is set
#      (the cue loads the shim);
#   3. CAST_CLEAN_ROOM_DRY_RUN=1 writes the seat's settings file, prints its path
#      and content, and ends before the role is sliced or claude is started, so
#      a memory check can read the file before any cast.
# The seat's settings file is marvel's projected policy (the last --settings in
# "$@"), or an empty object when there is none, written to
# $CAST_CLEAN_ROOM_DIR/cast-settings.json and passed as the last --settings. No
# backend overlay is applied: an overlay is a settings file, so it can carry more
# than backend env, and it is operator state. Sign-in is the Bedrock key block
# below, unchanged (the operator's ruling for clean-room sign-in: "use bedrock"):
# with CLAUDE_CODE_USE_BEDROCK=1 the seat signs in through the host's own key,
# brokered by item name, with no login and no copied credential, and nothing is
# read from the operator's config directory. This mode reads or creates no other
# credential.
CR_DIR="${CAST_CLEAN_ROOM_DIR:-}"
CR_SETTINGS=""
cr_fail() { echo "cast-launch: CAST_CLEAN_ROOM_DIR: $1; not starting the harness" >&2; exit 1; }
if [[ -z "$CR_DIR" && "${CAST_CLEAN_ROOM_DRY_RUN:-0}" == 1 ]]; then
  echo "cast-launch: CAST_CLEAN_ROOM_DRY_RUN=1 needs CAST_CLEAN_ROOM_DIR, the per-seat config directory the settings file is written in" >&2; exit 1
fi
if [[ -n "$CR_DIR" ]]; then
  [[ "$CR_DIR" == /* ]] || cr_fail "'$CR_DIR' is not an absolute path"
  [[ "$CR_DIR" != *$'\n'* && "$CR_DIR" != *'..'* ]] || cr_fail "the path holds a newline or '..'"
  [[ ! -L "$CR_DIR" ]] || cr_fail "'$CR_DIR' is a symlink"
  # The physical path of CR_DIR, which may not exist yet: resolve its nearest existing
  # ancestor and append the rest, so a symlinked parent cannot hide a path inside the
  # operator's directory.
  cr_phys() {
    local p="$1" rest="" base
    while [[ ! -d "$p" ]]; do base="$(basename "$p")"; rest="/$base$rest"; p="$(dirname "$p")"; done
    printf '%s%s' "$(cd -P "$p" && pwd -P)" "$rest"
  }
  cr_real="$(cr_phys "$CR_DIR")"
  cr_home="$(cr_phys "$HOME")"
  # Both places the operator's config can live: $HOME/.claude, and CLAUDE_CONFIG_DIR
  # when it is set. Checking only the second let a directory under $HOME/.claude
  # through whenever CLAUDE_CONFIG_DIR pointed elsewhere.
  for cr_op in "$(cr_phys "$HOME/.claude")" ${CLAUDE_CONFIG_DIR:+"$(cr_phys "$CLAUDE_CONFIG_DIR")"}; do
    if [[ "$cr_real" == "$cr_op" || "$cr_real" == "$cr_op"/* || "$cr_op" == "$cr_real"/* ]]; then
      cr_fail "'$CR_DIR' is the operator's config directory, inside it or around it ($cr_op)"
    fi
  done
  [[ "$cr_real" != / && "$cr_real" != "$cr_home" && "$cr_home" != "$cr_real"/* ]] \
    || cr_fail "'$CR_DIR' is the home directory or an ancestor of it"
  if [[ -e "$CR_DIR" ]]; then
    [[ -d "$CR_DIR" ]] || cr_fail "'$CR_DIR' exists and is not a directory"
    cr_extra="$(find "$CR_DIR" -mindepth 1 -maxdepth 1 ! -name cast-settings.json -print -quit)"
    [[ -z "$cr_extra" ]] || cr_fail "'$CR_DIR' is not fresh: it is not empty"
    # The one entry allowed is this mode's own settings file from an earlier dry run, and
    # only as a regular file: a symlink there would send the write below out of the directory.
    if [[ -e "$CR_DIR/cast-settings.json" || -L "$CR_DIR/cast-settings.json" ]]; then
      [[ -f "$CR_DIR/cast-settings.json" && ! -L "$CR_DIR/cast-settings.json" ]] \
        || cr_fail "'$CR_DIR/cast-settings.json' is not a regular file"
    fi
  fi
  CR_DIR="$cr_real"
  (umask 077; mkdir -p "$CR_DIR") || cr_fail "cannot create '$CR_DIR'"
  CR_SETTINGS="$CR_DIR/cast-settings.json"
  cr_policy=""; cr_prev=""
  for a in "$@"; do [[ "$cr_prev" == "--settings" ]] && cr_policy="$a"; cr_prev="$a"; done
  # Written to a new file in the directory and renamed over the settings file, so the
  # write never follows a link that appeared at the final name.
  cr_tmp="$(umask 077; mktemp "$CR_DIR/.cast-settings.XXXXXX")" || cr_fail "cannot create a file in '$CR_DIR'"
  if [[ -n "$cr_policy" && -f "$cr_policy" ]]; then
    cat "$cr_policy" > "$cr_tmp" || cr_fail "cannot write $CR_SETTINGS"
  else
    printf '{}\n' > "$cr_tmp" || cr_fail "cannot write $CR_SETTINGS"
  fi
  # Never into a directory the final name has become a link to: GNU mv takes -T, BSD
  # mv takes -h; either renames onto the name itself. A link planted after the check
  # above is replaced, not followed, and the name is checked once more afterwards.
  if mv --version >/dev/null 2>&1; then mv -fT "$cr_tmp" "$CR_SETTINGS"; else mv -fh "$cr_tmp" "$CR_SETTINGS"; fi \
    || cr_fail "cannot write $CR_SETTINGS"
  [[ -f "$CR_SETTINGS" && ! -L "$CR_SETTINGS" ]] || cr_fail "'$CR_SETTINGS' is not a regular file after the write"
  if [[ "${CAST_CLEAN_ROOM_DRY_RUN:-0}" == 1 ]]; then
    echo "cast-launch: clean-room dry run for $MARVEL_SESSION: CLAUDE_CONFIG_DIR=$CR_DIR; settings file $CR_SETTINGS follows; nothing was launched"
    cat "$CR_SETTINGS"
    exit 0
  fi
fi

# Identities that compose with a role (sole_fit at proposal): director and envoy.
case "$WROLE" in
  director) IDENTITY=assistive-agent;;
  envoy)    IDENTITY=front-office;;
  *)        IDENTITY="";;
esac

[[ -d "$WARDROBE_ROOT/roles" ]] || { echo "cast-launch: no wardrobe root at $WARDROBE_ROOT (set WARDROBE_ROOT)" >&2; exit 1; }
if [[ -z "$CR_DIR" ]]; then
[[ -x "$SHIM_BIN" ]] || { echo "cast-launch: director shim not executable at $SHIM_BIN; build and install it there (cd cmd/director-mcp in the director checkout, then go build -o \"\$HOME/.director/bin/director-mcp\" .) or set DIRECTOR_SHIM_BIN to its path. Never point it inside a temporary directory: a scratchpad path is reaped and every later cast fails here" >&2; exit 1; }
fi
slice_sh="$(dirname "$WARDROBE_ROOT")/scripts/slice.sh"
[[ -x "$slice_sh" ]] || { echo "cast-launch: $slice_sh missing; the install root is a full checkout" >&2; exit 1; }

# The slice, and the spawn line as a side effect (precondition 3: a failed
# log write is a refusal inside slice.sh). ALLOW_PROPOSAL=0 to cast ratified only.
slice_args=("$WARDROBE_ROOT" "$WROLE")
[[ -n "$IDENTITY" ]] && slice_args+=("$IDENTITY")
# The seat leads the spawn line (ruling 44). wardrobe's slice.sh must be the
# version that takes --seat; an older install refuses it as an unknown flag.
slice_args+=(--seat "$MARVEL_SESSION")
[[ "${ALLOW_PROPOSAL:-1}" == 1 ]] && slice_args+=(--allow-proposal)
[[ "${ALLOW_DIRTY:-0}" == 1 ]] && slice_args+=(--allow-dirty)
# Resolved once, as slice.sh would (ruling 59), and exported so slice.sh writes
# exactly the path the cast line names to the seat.
spawn_log="${WARDROBE_SPAWN_LOG:-$HOME/.local/state/wardrobe/spawn.log}"
export WARDROBE_SPAWN_LOG="$spawn_log"
# The operator's memory-off declaration comes back through a file (wardrobe
# slice.sh --env-out): one line, CLAUDE_CODE_DISABLE_AUTO_MEMORY=1, or nothing.
# slice.sh's exit status is read first and ends the launch on a refusal, because a
# refused cast leaves an older file untouched. Only then is the file read, and only
# that exact line is exported; any other content is a refusal, never ignored.
envout="$(mktemp "${TMPDIR:-/tmp}/cast-env-out.XXXXXX")" \
  || { echo "cast-launch: cannot create the --env-out file; not starting the harness" >&2; exit 1; }
slice_args+=(--env-out "$envout")
if ! slice="$("$slice_sh" "${slice_args[@]}")"; then
  rm -f "$envout"; echo "cast-launch: slice.sh refused the cast; not starting the harness" >&2; exit 1
fi
# The script ends in exec, so no EXIT trap would run: the file goes as soon as it is read.
# A cat that fails is a refusal: the substitution would otherwise take printf's status
# and read an unreadable file as empty, starting the harness with auto memory on.
envout_text="$(cat "$envout" && printf x)" \
  || { rm -f "$envout"; echo "cast-launch: cannot read the --env-out file; not starting the harness" >&2; exit 1; }
envout_text="${envout_text%x}"; rm -f "$envout"
case "$envout_text" in
  "") ;;
  "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1"$'\n') export CLAUDE_CODE_DISABLE_AUTO_MEMORY=1;;
  *) echo "cast-launch: slice.sh wrote something other than CLAUDE_CODE_DISABLE_AUTO_MEMORY=1 to the --env-out file; refusing, not starting the harness" >&2; exit 1;;
esac

# In the clean-room mode none of the bus wiring below runs (no id, relay log, role, global
# tier, channel cue, shim): see the clean-room block above.
if [[ -z "$CR_DIR" ]]; then
# One source for the bus id (R-73). marvel's computed per-replica name until
# the declared identity block exists; marvel wins if it already set it.
export DIRECTOR_AGENT_ID="${DIRECTOR_AGENT_ID:-$MARVEL_SESSION}"
if [[ ! "$DIRECTOR_AGENT_ID" =~ ^[a-z][a-z0-9]*(-[a-z0-9]+)*$ ]]; then
  echo "cast-launch: refusing id '$DIRECTOR_AGENT_ID'; it must match the closed class (R-76), never rewritten" >&2; exit 1
fi
# The relay log (director#634). The shim's inbox_summary joins a reply to its ask
# through the relay log and warns, on every call, when it finds none. The seat is
# known only at cast, a --copy install has no checkout to find a log in, and one
# install-time path would be shared by every seat on the host, so each seat gets its
# own file under its own state directory. A RELAY_LOG the caller already set is kept
# as given and nothing is created for it. A directory or file that exists is left as it
# is (no chmod, no truncate); only a missing directory is made (with its missing parents, all
# at 0700: mkdir -m would set the mode on the last one only) and a missing
# file is made empty, at 0600. A failure here warns and casts without the variable,
# which is how every seat cast before this ran.
if [[ -z "${RELAY_LOG:-}" ]]; then
  rl_dir="${DIRECTOR_STATE:-${DIRECTOR_HOME:-$HOME/.director}/state}/$DIRECTOR_AGENT_ID"
  rl_file="$rl_dir/relay-log.md"
  if { [[ -d "$rl_dir" ]] || (umask 077; mkdir -p "$rl_dir"); } \
     && { [[ -e "$rl_file" ]] || (umask 077; : >> "$rl_file"); }; then
    export RELAY_LOG="$rl_file"
  else
    echo "cast-launch: WARNING: could not make the relay log $rl_file; casting without RELAY_LOG, so this seat's inbox_summary will warn that replies cannot be joined to asks" >&2
  fi
  unset rl_dir rl_file
fi
# The session's role, so the shim reads its role inbox and the roster can
# resolve role://<team>/<role> to a live holder. The manifest role is the name
# senders address; marvel wins if it already set DIRECTOR_ROLE.
export DIRECTOR_ROLE="${DIRECTOR_ROLE:-$MARVEL_ROLE}"
if [[ ! "$DIRECTOR_ROLE" =~ ^[A-Za-z0-9_-]+$ ]]; then
  echo "cast-launch: refusing role '$DIRECTOR_ROLE'; the class is [A-Za-z0-9_-] (R-76), never rewritten" >&2; exit 1
fi
# The seat's lineage (B2-R1, sim/design/asks-die-unread.md section 3). marvel
# sets MARVEL_PREDECESSOR to "<workspace>/<id>" for a seat a shift, a wake or a
# crash repair started; the shim reads an optional DIRECTOR_PREDECESSOR, the id
# alone, and does not parse instance names to guess one. The workspace part is
# dropped here, the same way MARVEL_ROLE becomes DIRECTOR_ROLE. Unset or empty
# adds nothing, which is today's behavior. MARVEL_PREDECESSOR itself reaches the
# shim by inheritance, so it is left alone. A value that is not "<workspace>/<id>"
# is skipped with a warning, never refused (director ruling on #220 U1): lineage is
# optional, so a bad link must not stop the seat. The warning names the variable and
# the reason and never the value.
pred_env=""
if [[ -n "${MARVEL_PREDECESSOR:-}" ]]; then
  if [[ "$MARVEL_PREDECESSOR" =~ ^[A-Za-z0-9_-]+/([a-z][a-z0-9]*(-[a-z0-9]+)*)$ ]]; then
    pred_env=",\"DIRECTOR_PREDECESSOR\":\"${BASH_REMATCH[1]}\""
  else
    echo "cast-launch: WARNING: MARVEL_PREDECESSOR is not <workspace>/<agent id> (the id must match the closed class, R-76), so no DIRECTOR_PREDECESSOR is set and this seat casts without lineage" >&2
  fi
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
# research-supervisors are supervisors at the global tier: R-94 as amended
# (RULED 2026-09-24, director#77); the operator reaffirmed on 2026-09-26 that
# research-supervisors hold the supervisor global role. The global role word
# stays supervisor.
case "$WROLE" in
  supervisor|research-supervisor) GROLE=supervisor;;
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

# The channel cue (sim/design/channel-cue.md, C-5). The shim turns the cue on
# when DIRECTOR_CUE is unset, and only the value 1 keeps it on when set. So the
# launcher always tells it: DIRECTOR_CUE=1 in the shim's env with the director
# server loaded as a development channel, and DIRECTOR_CUE=0 without the
# channel, so the shim's answer and the channel flag cannot disagree
# (director#719).
# It was opt-in per seat by the operator's ruling of 2026-10-01. The operator's
# ruling of 2026-11-05 replaces that for supervisor casts: a supervisor
# (GROLE=supervisor, so research-supervisors too) gets the cue when DIRECTOR_CUE
# is unset and the launch is not headless. A SET value always wins, so
# DIRECTOR_CUE=0, or a set-but-empty value, keeps it off for a seat that opts
# out. Director casts and every other role are unchanged in what they bind:
# unset, they load no channel and carry DIRECTOR_CUE=0.
# The value can be inherited from the shell that started the tmux server, not
# only set in the role's manifest, so a cue that is on is named in the spawn
# line below, and says which way it came on. The flag goes last: a bare -- in a
# role's args would make it a positional, and a headless (-p or --print) launch
# gets a channel it cannot use, so the default does not apply to it.
cue_env=""
cue_args=()
cue_note=""
cue_how=""
if [[ "${DIRECTOR_CUE:-}" == 1 ]]; then
  cue_how="DIRECTOR_CUE=1"
elif [[ -z "${DIRECTOR_CUE+x}" && "$GROLE" == supervisor ]]; then
  cue_headless=""
  for a in "$@"; do
    [[ "$a" == -p || "$a" == --print ]] && cue_headless=1
  done
  [[ -z "$cue_headless" ]] && cue_how="default for supervisor casts"
fi
if [[ -n "$cue_how" ]]; then
  cue_env=',"DIRECTOR_CUE":"1"'
  cue_args=(--dangerously-load-development-channels server:director)
  cue_note=", channel cue ON ($cue_how)"
else
  # The shim turns the cue on when DIRECTOR_CUE is unset, so a seat launched
  # without the channel must be told 0 outright, or it is cued on a channel it
  # never bound (director#719). The launcher decides once; the shim follows.
  cue_env=',"DIRECTOR_CUE":"0"'
fi

# A supervisor-type cast with no global address on a host whose broker IS
# leafed to the global tier is the silent case director#180 names: the cast
# succeeds, the seat never appears at global://<cluster>/supervisor, and mail
# sent there waits unread. The tier stays optional (operator ruling), so this
# warns and never refuses, and only when the local broker's monitor reports a
# leaf remote up. The probe is loopback only with a 1s timeout, and every
# failure (no curl, monitor port closed, timeout, unreadable reply) reads as
# "not connected", so a host with no global tier casts exactly as before.
global_tier_connected() {
  local url="${DIRECTOR_NATS_MONITOR_URL:-}" hostport host body
  if [[ -z "$url" ]]; then
    hostport="${NATS_URL#*://}"; hostport="${hostport##*@}"
    if [[ "$hostport" == \[* ]]; then host="${hostport%%]*}]"; else host="${hostport%%:*}"; fi
    url="http://$host:8222"
  fi
  url="${url%/}"
  # An exact match, not a prefix: curl connects to the host after any "@", so
  # http://127.0.0.1:1@elsewhere would pass a prefix match and leave the box.
  [[ "$url" =~ ^http://(127\.0\.0\.1|localhost|\[::1\]):[0-9]{1,5}$ ]] || return 1
  command -v curl >/dev/null 2>&1 || return 1
  # -q (first) skips ~/.curlrc and --noproxy skips every proxy variable, so
  # neither can carry the probe off the loopback host.
  body="$(curl -q -s --noproxy '*' -m 1 "$url/leafz" 2>/dev/null)" || return 1
  [[ "$body" =~ \"leafnodes\":[[:space:]]*([0-9]+) ]] || return 1
  (( BASH_REMATCH[1] > 0 ))
}

startup_note=""
if [[ "$GROLE" == supervisor && -z "$GLOBAL_ADDR" ]] && global_tier_connected; then
  echo "cast-launch: WARNING: the global tier is connected on this host (the local broker reports a leaf remote up), but this $WROLE cast carries no global levers (DIRECTOR_GLOBAL_DOMAIN, DIRECTOR_CLUSTER), so it holds no global address and mail to its global://<cluster>/supervisor address will wait unread. Set both on the marvel daemon and recast; casting anyway (director#180). This check reads the local broker's monitor port: a monitor port closed, a timeout or an unreadable reply reads as not connected and gives no warning." >&2
  startup_note="Note from the launcher: the global tier is connected on this host, but you were cast with no global levers, so you hold no global address. Tell the operator through the director so you can be recast with DIRECTOR_GLOBAL_DOMAIN and DIRECTOR_CLUSTER set (director#180)."
fi
# A supervisor that does hold a global address proves its reach once at
# startup. It is a duty, not a gate on routing. DIRECTOR_PEER_CLUSTER names
# another cluster whose supervisor answers the test; without one, the test
# goes to the director seat. Either way the result is reported to the director.
if [[ "$GROLE" == supervisor && -n "$GLOBAL_ADDR" ]]; then
  peer="${DIRECTOR_PEER_CLUSTER:-}"
  if [[ -n "$peer" && ! "$peer" =~ ^[A-Za-z0-9_-]+$ ]]; then
    echo "cast-launch: refusing DIRECTOR_PEER_CLUSTER '$peer'; the class is [A-Za-z0-9_-] (R-76)" >&2; exit 1
  fi
  if [[ -n "$peer" && "$peer" != "$DIRECTOR_CLUSTER" ]]; then reach="global://$peer/supervisor"; else reach="global://director"; fi
  startup_note="After the ruling 84 echo, as a startup duty: send one reach test to $reach, note whether an ack came back and how long it took, and report both to global://director. It is a check, not a gate: route as usual whatever it shows (director#180)."
fi

mcp_json="$(printf '{"mcpServers":{"director":{"command":"%s","env":{"DIRECTOR_AGENT_ID":"%s","DIRECTOR_ROLE":"%s","DIRECTOR_TEAM":"%s","DIRECTOR_WORKSPACE":"%s","NATS_URL":"%s"%s%s%s}}}}' \
  "$SHIM_BIN" "$DIRECTOR_AGENT_ID" "$DIRECTOR_ROLE" "$DIRECTOR_TEAM" "$DIRECTOR_WORKSPACE" "$NATS_URL" "$global_env" "$cue_env" "$pred_env")"

else
  DIRECTOR_AGENT_ID="$MARVEL_SESSION"; GROLE=""; GLOBAL_ADDR=""; startup_note=""
  cue_how=""; cue_note=""; cue_args=()
  mcp_json='{"mcpServers":{}}'
fi

cast_line="You are cast as wardrobe role/$WROLE for the manifest role $MARVEL_ROLE in team $DIRECTOR_TEAM, address agent://$DIRECTOR_TEAM/$DIRECTOR_AGENT_ID."
[[ -n "$CR_DIR" ]] && cast_line="You are cast as wardrobe role/$WROLE for the manifest role $MARVEL_ROLE in team $DIRECTOR_TEAM, in a clean-room run: you hold no director address and have no bus."
[[ -n "$SCOPE" ]] && cast_line+=" Your scope, set at cast time and recorded by the supervisor: $SCOPE."
# Ruling 84 means the seat's OWN line (director). In a team cast the last line
# of the log belongs to whichever seat spawned last, so the seat greps for its
# own session name, the same one slice.sh was handed as --seat.
cast_line+=" Your first act is to echo your own line of the spawn log at $spawn_log (ruling 84): grep -F 'seat=$MARVEL_SESSION ' on it, echo the newest match, and say which line you echoed; if no line for seat=$MARVEL_SESSION exists, say so and echo no other seat's line."

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
# After the caller's text, so a wrapper-rendered cast carries it too.
[[ -n "$startup_note" ]] && prompt+=$'\n\n'"$startup_note"
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
if [[ -z "$CR_DIR" ]]; then
DIRECTOR_TEAM="$DIRECTOR_TEAM" DIRECTOR_WORKSPACE="$DIRECTOR_WORKSPACE" NATS_URL="$NATS_URL" \
  "$SHIM_BIN" --preflight \
  || { echo "cast-launch: bus pre-flight failed for agent://$DIRECTOR_TEAM/$DIRECTOR_AGENT_ID on $NATS_URL; not starting the harness (finding-166)" >&2; exit 1; }
fi

# The Bedrock key (the operator's ruling, relayed by director on 2026-10-08). When
# the session runs on Bedrock, claude takes its credential from
# AWS_BEARER_TOKEN_BEDROCK. A token already in the environment wins and nothing
# is read. Otherwise the key comes from the macOS keychain item that
# CLAUDE_BEDROCK_KEY_ITEM names: looked up by name only, exported to claude
# (and so inherited by everything claude starts: Bash tool calls, MCP servers),
# never stored, never printed. A missing, empty or badly named item
# fails the launch, so a seat never starts on the wrong credential. The lookup
# is /usr/bin/security by absolute path: a PATH entry a seat or a pane can
# write must not decide what supplies the key. There is no override; the
# verify script tests a copy of this file with that one path substituted. The spawn
# line carries the item's name (bedrock_note), never its value.
bedrock_prog=cast-launch
bedrock_note=""
# xtrace is suspended around the block and restored after it, so a launcher run
# under bash -x or SHELLOPTS=xtrace never traces the key value.
case $- in *x*) bedrock_xtrace=1;; *) bedrock_xtrace=0;; esac
set +x
if [[ "${CLAUDE_CODE_USE_BEDROCK:-}" == 1 ]]; then
  if [[ -n "${AWS_BEARER_TOKEN_BEDROCK:-}" ]]; then
    bedrock_note=", Bedrock key from the environment"
  else
    bedrock_item="${CLAUDE_BEDROCK_KEY_ITEM:-}"
    [[ "$bedrock_item" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] \
      || { echo "$bedrock_prog: CLAUDE_CODE_USE_BEDROCK=1 needs CLAUDE_BEDROCK_KEY_ITEM to name a keychain item ([A-Za-z0-9._-], no leading punctuation); not starting the harness" >&2; exit 1; }
    bedrock_key="$(/usr/bin/security find-generic-password -s "$bedrock_item" -w 2>/dev/null)" \
      || { echo "$bedrock_prog: keychain item '$bedrock_item' not readable; not starting the harness" >&2; exit 1; }
    [[ -n "$bedrock_key" ]] \
      || { echo "$bedrock_prog: keychain item '$bedrock_item' is empty; not starting the harness" >&2; exit 1; }
    export AWS_BEARER_TOKEN_BEDROCK="$bedrock_key"
    unset bedrock_key
    bedrock_note=", Bedrock key from keychain item $bedrock_item"
  fi
fi
if [[ "$bedrock_xtrace" == 1 ]]; then set -x; fi
unset bedrock_xtrace

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
if [[ -z "$CR_DIR" && -f "$overlay_file" ]]; then
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

[[ -n "$CR_DIR" ]] && settings_args=(--settings "$CR_SETTINGS")

# Supervisor casts (research-supervisors too) may read and type into the panes
# they supervise without a classifier prompt: the operator's ruling of
# 2026-11-25, "I want you to get ALL supervisors the ability to marvel
# capture/marvel inject". An allow rule, so the auto mode classifier is not
# asked; a deny rule elsewhere still wins.
sup_args=()
if [[ "$GROLE" == supervisor ]]; then
  sup_args=(--allowedTools "Bash(marvel capture:*)" --allowedTools "Bash(marvel inject:*)")
fi

# The development-channels prompt (aae-orc-d3rel, route R4; the operator's ruling of
# 2026-11-09). A seat started with the dev-channels flag stops at a startup prompt until
# someone answers it. This script ends in exec, so it cannot press a key afterwards: it
# starts dismiss-devch, a separate file so this launcher names no keystroke tool, in the
# background first. The helper presses option 1 only on the exact prompt, once, and
# reports into a log beside the spawn log. A seat with no cue starts no helper, and its
# launch and spawn lines are unchanged.
devch_note=""
if [[ -n "$cue_how" ]]; then
  # An installed launcher is a symlink into lib/cast-launch/<sha>/, where the helper sits beside it.
  devch_src="${BASH_SOURCE[0]}"
  while [[ -L "$devch_src" ]]; do
    devch_t="$(readlink "$devch_src")"
    if [[ "$devch_t" == /* ]]; then devch_src="$devch_t"; else devch_src="$(dirname "$devch_src")/$devch_t"; fi
  done
  devch="$(cd "$(dirname "$devch_src")" && pwd)/dismiss-devch"
  if [[ -x "$devch" && -n "${TMUX_PANE:-}" && -n "${TMUX:-}" ]] && command -v tmux >/dev/null 2>&1; then
    devch_log="$(dirname "$spawn_log")/devch-$DIRECTOR_AGENT_ID.log"
    mkdir -p "$(dirname "$devch_log")" 2>/dev/null || true
    ( "$devch" --pane "$TMUX_PANE" --tmux-arg=-S "--tmux-arg=${TMUX%%,*}" >>"$devch_log" 2>&1 </dev/null & )
    devch_note=", devch helper armed"
  else
    devch_note=", devch helper not armed (it needs dismiss-devch beside this launcher, a tmux pane and tmux on PATH)"
  fi
fi

if [[ -n "$CR_DIR" ]]; then
  echo "cast-launch: $MARVEL_SESSION -> role/$WROLE CLEAN ROOM, no director shim, CLAUDE_CONFIG_DIR $CR_DIR, cwd $TWIN_CWD$bedrock_note" >&2
else
echo "cast-launch: $MARVEL_SESSION -> role/$WROLE identity=${IDENTITY:-none} as agent://$DIRECTOR_TEAM/$DIRECTOR_AGENT_ID${GLOBAL_ADDR:+ and $GLOBAL_ADDR} on $NATS_URL, cwd $TWIN_CWD$cue_note$devch_note$bedrock_note" >&2
fi
# The role mark (aae-orc-20062): this pid survives the exec, so it is the
# shim's parent pid; the shim holds a director identity only with it.
export DIRECTOR_ROLE_MARK=$$
# In the clean-room mode claude starts without any DIRECTOR_*, NATS_*, RELAY_LOG
# or MARVEL_* variable (the daemon socket, the heartbeat token file and the rest), whether
# this script exported it or marvel and the shell passed it down.
cr_env=()
if [[ -n "$CR_DIR" ]]; then
  export CLAUDE_CONFIG_DIR="$CR_DIR"
  cr_env=(env -u RELAY_LOG)
  while IFS= read -r cr_v; do cr_env+=(-u "$cr_v"); done < <(compgen -v DIRECTOR_; compgen -v NATS_; compgen -v MARVEL_)
fi
exec ${cr_env[@]+"${cr_env[@]}"} claude -n "$DIRECTOR_AGENT_ID" \
  --strict-mcp-config --mcp-config "$mcp_json" \
  --append-system-prompt "$prompt" \
  "$@" \
  ${settings_args[@]+"${settings_args[@]}"} \
  ${sup_args[@]+"${sup_args[@]}"} \
  ${cue_args[@]+"${cue_args[@]}"}
