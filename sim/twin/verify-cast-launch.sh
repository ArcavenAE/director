#!/usr/bin/env bash
# Prove cast-launch.sh hands the global tier's three levers to the roles that
# hold a global address and clears them for every role that does not (R-86,
# R-94; design brief 8). Broker-free and cast-free: claude, the director shim,
# the wardrobe root and slice.sh are all stubs, so nothing connects, nothing is
# cast, and no live session is touched.
#
# The leak this exists to catch: the shim INHERITS the launcher's environment
# and mcp_json's env map is merged over it, so omitting the levers from
# mcp_json does not keep them out of a worker's session. A builder that sees
# DIRECTOR_GLOBAL_DOMAIN with no valid role is refused by the shim at spawn and
# marvel crash-loops it. Every case below therefore checks BOTH surfaces: the
# mcp-config claude is handed, and the environment claude and the pre-flight
# actually run with.
#
# Usage: sim/twin/verify-cast-launch.sh     Exit nonzero on any miss.
#        CAST_LAUNCH=<path> sim/twin/verify-cast-launch.sh
#   CAST_LAUNCH names the launcher to test instead of the one beside this file,
#   so an installed copy (~/.director/bin/cast-launch) is tested as installed.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LAUNCH="${CAST_LAUNCH:-$here/cast-launch.sh}"
[[ -x "$LAUNCH" ]] || { echo "cast-launch.sh not executable at $LAUNCH"; exit 2; }

pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT

# --- the stubs ----------------------------------------------------------------
# A wardrobe install root whose slice.sh prints a slice and nothing else.
mkdir -p "$root/wardrobe/contents/roles" "$root/wardrobe/scripts" "$root/bin" "$root/out"
cat > "$root/wardrobe/scripts/slice.sh" <<'STUB'
#!/usr/bin/env bash
echo "stub slice for $2"
STUB
# The shim stub records the environment the pre-flight really runs with.
cat > "$root/bin/director-mcp" <<STUB
#!/usr/bin/env bash
if [[ "\${1:-}" == "--preflight" ]]; then env > "$root/out/preflight.env"; fi
exit 0
STUB
# The claude stub records the environment it inherits and the args it is given,
# then exits instead of starting a session.
cat > "$root/bin/claude" <<STUB
#!/usr/bin/env bash
env > "$root/out/claude.env"
printf '%s\n' "\$@" > "$root/out/claude.args"
exit 0
STUB
chmod +x "$root/wardrobe/scripts/slice.sh" "$root/bin/director-mcp" "$root/bin/claude"
# The curl stub stands in for the local broker's monitor endpoint, so no case
# reaches a real broker on the host running this script. It records its args
# and prints $root/leafz.json when that file exists; otherwise it fails the way
# a closed monitor port does.
cat > "$root/bin/curl" <<STUB
#!/usr/bin/env bash
printf '%s\n' "\$@" > "$root/out/curl.args"
[[ -f "$root/leafz.json" ]] || exit 7
cat "$root/leafz.json"
STUB
chmod +x "$root/bin/curl"

# Run cast-launch.sh with a clean, fully controlled environment. `env -i` is the
# point: it guarantees the only DIRECTOR_* values in play are the ones each case
# names, so a pass cannot come from this shell's own environment.
cast() { # role, then extra KEY=VALUE pairs
  local role="$1"; shift
  rm -f "$root/out/claude.env" "$root/out/claude.args" "$root/out/preflight.env"
  env -i \
    PATH="$root/bin:/usr/bin:/bin" HOME="$root" \
    MARVEL_ROLE="$role" MARVEL_SESSION="verify-$role-0" \
    WARDROBE_ROOT="$root/wardrobe/contents" \
    DIRECTOR_SHIM_BIN="$root/bin/director-mcp" \
    TWIN_CWD="$root" \
    DIRECTOR_TEAM=fleet DIRECTOR_WORKSPACE=verifyws \
    "$@" \
    "$LAUNCH" >"$root/out/stdout" 2>"$root/out/stderr"
}

# Does a lever appear in the mcp-config claude was handed?
in_mcp()  { grep -q "\"$1\":\"$2\"" "$root/out/claude.args"; }
has_mcp() { grep -q "$1" "$root/out/claude.args"; }
# Does a lever appear in an environment dump? (anchored: DIRECTOR_CLUSTER must
# not match DIRECTOR_CLUSTER_SOMETHING, and absence must mean absence.)
in_env()  { grep -qx "$1=$2" "$root/out/$3"; }
has_env() { grep -q "^$1=" "$root/out/$2"; }

GLOBAL_ON=(DIRECTOR_GLOBAL_DOMAIN=global DIRECTOR_CLUSTER=mokuzai)

# --- 1. the supervisor gets all three, on both surfaces -----------------------
if cast supervisor "${GLOBAL_ON[@]}"; then
  miss=""
  in_mcp DIRECTOR_GLOBAL_DOMAIN global    || miss+=" mcp:domain"
  in_mcp DIRECTOR_CLUSTER mokuzai         || miss+=" mcp:cluster"
  in_mcp DIRECTOR_GLOBAL_ROLE supervisor  || miss+=" mcp:role"
  in_env DIRECTOR_GLOBAL_DOMAIN global claude.env   || miss+=" env:domain"
  in_env DIRECTOR_CLUSTER mokuzai claude.env        || miss+=" env:cluster"
  in_env DIRECTOR_GLOBAL_ROLE supervisor claude.env || miss+=" env:role"
  [[ -z "$miss" ]] && ok "supervisor: all three levers reach mcp_json and the session environment" \
                   || bad "supervisor levers" "missing:$miss"
else
  bad "supervisor cast" "$(cat "$root/out/stderr")"
fi

# --- 2. the pre-flight sees them too, because it runs the same shim -----------
in_env DIRECTOR_GLOBAL_ROLE supervisor preflight.env \
  && ok "supervisor: the pre-flight runs with the global role set" \
  || bad "supervisor pre-flight environment"

# --- 3. the derived role is the cast's, and the address is reported -----------
grep -q "global://mokuzai/supervisor" "$root/out/stderr" \
  && ok "supervisor: the cast log names the session's global address" \
  || bad "cast log global address" "$(cat "$root/out/stderr")"

# --- 4. every role without a global address gets all three CLEARED ------------
# This is the load-bearing case. maintainer, builder, marvel-builder and
# sideshow-builder all run on a daemon where the domain IS set.
for role in maintainer builder marvel-builder sideshow-builder envoy; do
  if cast "$role" "${GLOBAL_ON[@]}"; then
    leak=""
    has_mcp DIRECTOR_GLOBAL_DOMAIN              && leak+=" mcp:domain"
    has_mcp DIRECTOR_CLUSTER                    && leak+=" mcp:cluster"
    has_mcp DIRECTOR_GLOBAL_ROLE                && leak+=" mcp:role"
    has_env DIRECTOR_GLOBAL_DOMAIN claude.env   && leak+=" env:domain"
    has_env DIRECTOR_CLUSTER claude.env         && leak+=" env:cluster"
    has_env DIRECTOR_GLOBAL_ROLE claude.env     && leak+=" env:role"
    has_env DIRECTOR_GLOBAL_DOMAIN preflight.env && leak+=" preflight:domain"
    has_env DIRECTOR_CLUSTER preflight.env       && leak+=" preflight:cluster"
    has_env DIRECTOR_GLOBAL_ROLE preflight.env   && leak+=" preflight:role"
    [[ -z "$leak" ]] && ok "$role: the global levers are cleared from mcp_json, the session and the pre-flight" \
                     || bad "$role leaked global levers" "leaked:$leak"
  else
    bad "$role cast" "$(cat "$root/out/stderr")"
  fi
done

# --- 5. an inherited role cannot leak either ---------------------------------
# The daemon-set role is what would reach a worker today. It must be cleared,
# not merely omitted from mcp_json.
if cast builder "${GLOBAL_ON[@]}" DIRECTOR_GLOBAL_ROLE=supervisor; then
  has_env DIRECTOR_GLOBAL_ROLE claude.env \
    && bad "builder inherited an environment-set global role" \
    || ok "builder: an environment-set DIRECTOR_GLOBAL_ROLE is cleared, not inherited"
else
  bad "builder cast with an inherited role" "$(cat "$root/out/stderr")"
fi

# --- 6. the director seat is the second global role word (R-94) --------------
if cast director "${GLOBAL_ON[@]}"; then
  in_mcp DIRECTOR_GLOBAL_ROLE director && grep -q "global://director" "$root/out/stderr" \
    && ok "director: derives the director role and the single fleet-wide seat address" \
    || bad "director role derivation" "$(cat "$root/out/stderr")"
else
  bad "director cast" "$(cat "$root/out/stderr")"
fi

# --- 7. with the tier off, nothing changes for anyone ------------------------
if cast supervisor; then
  if has_mcp DIRECTOR_GLOBAL_ROLE || has_env DIRECTOR_GLOBAL_ROLE claude.env; then
    bad "global levers appeared with the tier off"
  else
    ok "tier off: a supervisor cast carries no global levers, unchanged behaviour"
  fi
else
  bad "supervisor cast with the tier off" "$(cat "$root/out/stderr")"
fi

# --- 8. half-configured is refused, and only for the role that would use it ---
if cast supervisor DIRECTOR_GLOBAL_DOMAIN=global; then
  bad "a supervisor cast with no DIRECTOR_CLUSTER was allowed"
else
  grep -q "DIRECTOR_CLUSTER" "$root/out/stderr" \
    && ok "supervisor: the domain without a cluster is refused, naming the missing lever" \
    || bad "cluster refusal message" "$(cat "$root/out/stderr")"
fi
cast builder DIRECTOR_GLOBAL_DOMAIN=global \
  && ok "builder: the same half-configured daemon casts a worker fine, the levers being cleared" \
  || bad "builder refused on a half-configured daemon" "$(cat "$root/out/stderr")"

# --- 9. a contradicting environment role is refused, never rewritten (R-76) ---
if cast supervisor "${GLOBAL_ON[@]}" DIRECTOR_GLOBAL_ROLE=director; then
  bad "a contradicting DIRECTOR_GLOBAL_ROLE was accepted"
else
  grep -q "contradicts the cast" "$root/out/stderr" \
    && ok "supervisor: an environment role contradicting the cast is refused" \
    || bad "contradiction refusal message" "$(cat "$root/out/stderr")"
fi

# --- 10. subject-token class is enforced before interpolation (R-76) ---------
if cast supervisor DIRECTOR_GLOBAL_DOMAIN=global 'DIRECTOR_CLUSTER=moku.zai'; then
  bad "a cluster outside the identity class was accepted"
else
  grep -q "DIRECTOR_CLUSTER" "$root/out/stderr" \
    && ok "supervisor: a cluster outside [A-Za-z0-9_-] is refused before it becomes a subject" \
    || bad "cluster class refusal" "$(cat "$root/out/stderr")"
fi

# --- 11. the shim default resolves under ~/.director, no env var needed ------
mkdir -p "$root/.director/bin"
cp "$root/bin/director-mcp" "$root/.director/bin/director-mcp"
if env -i PATH="$root/bin:/usr/bin:/bin" HOME="$root" \
     MARVEL_ROLE=builder MARVEL_SESSION=verify-default-0 \
     WARDROBE_ROOT="$root/wardrobe/contents" TWIN_CWD="$root" \
     DIRECTOR_TEAM=fleet DIRECTOR_WORKSPACE=verifyws \
     "$LAUNCH" >"$root/out/stdout" 2>"$root/out/stderr"; then
  grep -q "$root/.director/bin/director-mcp" "$root/out/claude.args" \
    && ok "DIRECTOR_SHIM_BIN unset: the shim resolves to ~/.director/bin/director-mcp" \
    || bad "shim default path" "$(cat "$root/out/claude.args")"
else
  bad "cast with DIRECTOR_SHIM_BIN unset" "$(cat "$root/out/stderr")"
fi

# --- 12. a missing shim still refuses, and says where to put one -------------
rm -f "$root/.director/bin/director-mcp"
if env -i PATH="$root/bin:/usr/bin:/bin" HOME="$root" \
     MARVEL_ROLE=builder MARVEL_SESSION=verify-noshim-0 \
     WARDROBE_ROOT="$root/wardrobe/contents" TWIN_CWD="$root" \
     DIRECTOR_TEAM=fleet DIRECTOR_WORKSPACE=verifyws \
     "$LAUNCH" >/dev/null 2>"$root/out/stderr"; then
  bad "a cast with no shim anywhere was allowed"
else
  grep -q "not executable" "$root/out/stderr" && grep -q "DIRECTOR_SHIM_BIN" "$root/out/stderr" \
    && ok "no shim: refused, naming both the default path and the override" \
    || bad "missing-shim refusal message" "$(cat "$root/out/stderr")"
fi

# --- the session's role reaches the shim, so it reads its role inbox ----------
# Without it the shim holds no role, role://<team>/<role> mail is accepted and
# never read, and the roster cannot resolve a holder.
if cast reviewer; then
  miss=""
  in_mcp DIRECTOR_ROLE reviewer          || miss+=" mcp"
  in_env DIRECTOR_ROLE reviewer claude.env || miss+=" env"
  [[ -z "$miss" ]] && ok "reviewer: DIRECTOR_ROLE reaches mcp_json and the session environment" \
                   || bad "role lever" "missing:$miss"
else
  bad "reviewer cast" "$(cat "$root/out/stderr")"
fi
if cast reviewer DIRECTOR_ROLE='rev.iewer'; then
  bad "malformed role" "a role with a dot was accepted"
else
  grep -q "refusing role" "$root/out/stderr" \
    && ok "a role outside [A-Za-z0-9_-] is refused before it becomes a subject" \
    || bad "malformed-role refusal message" "$(cat "$root/out/stderr")"
fi

# --- the claude reviewer seat is cast as wardrobe role/reviewer ---------------
# The manifest row is claude-reviewer (a claude seat beside the codex reviewer),
# and wardrobe has no role by that name. Without the mapping the slice lookup
# asks wardrobe for role/claude-reviewer and the seat gets no role text.
if cast claude-reviewer; then
  miss=""
  grep -qx "stub slice for reviewer" "$root/out/claude.args" || miss+=" slice"
  grep -q "wardrobe role/reviewer for the manifest role claude-reviewer" "$root/out/claude.args" || miss+=" cast-line"
  grep -q "Your scope" "$root/out/claude.args" && miss+=" unexpected-scope"
  [[ -z "$miss" ]] && ok "claude-reviewer: cast as role/reviewer with the reviewer slice and no scope" \
                   || bad "claude-reviewer mapping" "missing:$miss"
else
  bad "claude-reviewer cast" "$(cat "$root/out/stderr")"
fi

# --- the general builder's scope matches the wrapper's (director#161) ---------
# A seat launched without the cast wrapper reads its scope from this script, so
# the list must match the wrapper's row, director included.
builder_scope="general (kos, director, fleet CI, stave, sidestep, bloomctl, critic, beadle, curtain, ThreeDoors, BetterDials)"
if cast builder; then
  grep -qF "Your scope, set at cast time and recorded by the supervisor: $builder_scope." "$root/out/claude.args" \
    && ok "builder: cast with the general scope, director included" \
    || bad "builder scope" "$(grep -o 'Your scope[^.]*' "$root/out/claude.args")"
else
  bad "builder cast" "$(cat "$root/out/stderr")"
fi

# --- a research-supervisor is a supervisor at the global tier (R-94, amended) --
# R-94 as amended (RULED 2026-09-24, director#77); the operator reaffirmed on
# 2026-09-26 that research-supervisors hold the supervisor global role, so they
# hold a global address. The global role word stays supervisor: the shim accepts only
# supervisor and director. A worker beside it still gets nothing.
if cast research-supervisor "${GLOBAL_ON[@]}"; then
  miss=""
  in_mcp DIRECTOR_GLOBAL_ROLE supervisor               || miss+=" mcp:role"
  in_mcp DIRECTOR_CLUSTER mokuzai                      || miss+=" mcp:cluster"
  in_env DIRECTOR_GLOBAL_ROLE supervisor claude.env    || miss+=" env:role"
  in_env DIRECTOR_GLOBAL_ROLE supervisor preflight.env || miss+=" preflight:role"
  grep -q "global://mokuzai/supervisor" "$root/out/stderr" || miss+=" address"
  [[ -z "$miss" ]] && ok "research-supervisor: global role supervisor on mcp_json, the session and the pre-flight, address global://mokuzai/supervisor" \
                   || bad "research-supervisor global tier" "missing:$miss"
else
  bad "research-supervisor cast" "$(cat "$root/out/stderr")"
fi
if cast builder "${GLOBAL_ON[@]}"; then
  if has_mcp DIRECTOR_GLOBAL_ROLE || has_env DIRECTOR_GLOBAL_ROLE claude.env; then
    bad "builder beside research-supervisor" "a worker received a global role"
  else
    ok "builder beside research-supervisor: still no global role"
  fi
else
  bad "builder cast" "$(cat "$root/out/stderr")"
fi

# --- exactly one system prompt reaches claude (aae-orc-1vq6z) -----------------
# claude keeps only the LAST --append-system-prompt, so a second flag in "$@"
# (marvel's one-line identity, or a wrapper's full cast) silently replaces the
# launcher's slice. The launcher strips every pair from "$@" and passes one.
cast_with() { # role, then args for the launcher (marvel's trailing args)
  local role="$1"; shift
  rm -f "$root/out/claude.env" "$root/out/claude.args" "$root/out/preflight.env"
  env -i \
    PATH="$root/bin:/usr/bin:/bin" HOME="$root" \
    MARVEL_ROLE="$role" MARVEL_SESSION="verify-$role-0" \
    WARDROBE_ROOT="$root/wardrobe/contents" \
    DIRECTOR_SHIM_BIN="$root/bin/director-mcp" \
    TWIN_CWD="$root" \
    DIRECTOR_TEAM=fleet DIRECTOR_WORKSPACE=verifyws \
    "$LAUNCH" "$@" >"$root/out/stdout" 2>"$root/out/stderr"
}
# claude.args holds one argument per line; a prompt spans lines, a flag never.
flags() { grep -cx -- '--append-system-prompt' "$root/out/claude.args"; }
args_has() { grep -qF -- "$1" "$root/out/claude.args"; }
one_prompt() { # name, then args; then checks as has:TEXT or not:TEXT
  local name="$1"; shift
  local -a launch=() checks=()
  while (($#)) && [[ "$1" != --checks ]]; do launch+=("$1"); shift; done
  shift
  checks=("$@")
  if ! cast_with builder ${launch[@]+"${launch[@]}"}; then bad "$name" "$(cat "$root/out/stderr")"; return; fi
  local miss="" c
  [[ "$(flags)" == 1 ]] || miss+=" flags=$(flags)"
  for c in "${checks[@]}"; do
    case "$c" in
      has:*) args_has "${c#has:}" || miss+=" missing[${c#has:}]";;
      not:*) args_has "${c#not:}" && miss+=" unexpected[${c#not:}]";;
    esac
  done
  [[ -z "$miss" ]] && ok "$name" || bad "$name" "$miss"
}
wrapper_prompt="You are cast as wardrobe role/builder for the manifest role builder in team fleet. WRAPPER-SCOPE-MARK"$'\n\n'"wrapper-rendered slice"
one_prompt "no caller flag: one prompt, the launcher's slice" \
  --checks has:"stub slice for builder"
one_prompt "marvel's one-liner is folded in after the slice, one flag" \
  --append-system-prompt "You are verify-builder-0 (role: builder)" \
  --checks has:"stub slice for builder" has:"You are verify-builder-0 (role: builder)"
one_prompt "a wrapper's full cast is used as the one prompt, not duplicated" \
  --append-system-prompt "$wrapper_prompt" \
  --checks has:WRAPPER-SCOPE-MARK not:"stub slice for builder"
one_prompt "wrapper cast plus marvel's one-liner: still one flag, both texts" \
  --append-system-prompt "$wrapper_prompt" --append-system-prompt "You are verify-builder-0 (role: builder)" \
  --checks has:WRAPPER-SCOPE-MARK has:"You are verify-builder-0 (role: builder)"
one_prompt "the --append-system-prompt=value form is stripped too" \
  "--append-system-prompt=You are verify-builder-0 (role: builder)" \
  --checks has:"stub slice for builder" has:"You are verify-builder-0 (role: builder)" not:"--append-system-prompt="
one_prompt "other caller args pass through untouched" \
  --settings /tmp/policy.json --append-system-prompt "x" --session-id abc \
  --checks has:"--settings" has:"/tmp/policy.json" has:"--session-id" has:abc

# --- overlays are state, not code: they live under the director home ---------
# The default overlay root is ${DIRECTOR_HOME:-$HOME/.director}/overlays, never
# the launcher's own directory. An installed launcher is a versioned directory
# behind a symlink; an overlay beside it would vanish at the next install, and
# a launcher run through a symlink would look for it in ~/.director/bin.
# The launcher needs jq to attach an overlay; env -i narrows PATH, so link it in.
if jqbin="$(command -v jq)"; then ln -sf "$jqbin" "$root/bin/jq"; fi
printf '{"env":{"OVERLAY_MARK":"%s"}}\n' home > "$root/overlay-home.json"
printf '{"env":{"OVERLAY_MARK":"%s"}}\n' dirhome > "$root/overlay-dirhome.json"
printf '{"env":{"OVERLAY_MARK":"%s"}}\n' explicit > "$root/overlay-explicit.json"
printf '{"env":{"OVERLAY_MARK":"%s"}}\n' beside > "$root/overlay-beside.json"
merged_mark() { # the OVERLAY_MARK in the --settings file claude was handed, or none
  local f
  f="$(grep -A1 -x -- '--settings' "$root/out/claude.args" | tail -1)"
  [[ -n "$f" && -f "$f" ]] && jq -r '.env.OVERLAY_MARK // "none"' "$f" || echo none
}
overlay_case() { # name, expected mark, then extra KEY=VALUE pairs for cast
  local name="$1" want="$2"; shift 2
  if cast builder "$@"; then
    local got; got="$(merged_mark)"
    [[ "$got" == "$want" ]] && ok "$name" || bad "$name" "overlay mark $got, want $want"
  else
    bad "$name" "$(cat "$root/out/stderr")"
  fi
}
# A launcher copy with an overlays/ directory beside it, as an install tree or a
# checkout would have. Its overlay must NOT be picked up.
mkdir -p "$root/inst/overlays/by-role" "$root/.director/overlays/by-role" \
         "$root/dhome/overlays/by-role" "$root/explicit/by-role"
cp "$LAUNCH" "$root/inst/cast-launch.sh"
cp "$root/overlay-beside.json" "$root/inst/overlays/by-role/builder.json"
LAUNCH_SAVED="$LAUNCH"; LAUNCH="$root/inst/cast-launch.sh"

overlay_case "no overlay anywhere but beside the launcher: none is attached" none
cp "$root/overlay-home.json" "$root/.director/overlays/by-role/builder.json"
overlay_case "the default overlay root is \$HOME/.director/overlays" home
cp "$root/overlay-dirhome.json" "$root/dhome/overlays/by-role/builder.json"
overlay_case "DIRECTOR_HOME moves the default overlay root" dirhome DIRECTOR_HOME="$root/dhome"
cp "$root/overlay-explicit.json" "$root/explicit/by-role/builder.json"
overlay_case "MARVEL_OVERLAY_ROOT still wins over the default" explicit \
  DIRECTOR_HOME="$root/dhome" MARVEL_OVERLAY_ROOT="$root/explicit"

# The same launcher run through a symlink, the way ~/.director/bin/cast-launch
# is: it still resolves the home overlay and nothing beside the link.
mkdir -p "$root/linkbin"
ln -s "$root/inst/cast-launch.sh" "$root/linkbin/cast-launch"
cp "$root/overlay-beside.json" "$root/linkbin/builder.json"
LAUNCH="$root/linkbin/cast-launch"
overlay_case "a launcher run through a symlink uses the home overlay" home
LAUNCH="$LAUNCH_SAVED"

# --- infra-builder: the builder role, with a scope from the environment --------
# A manifest role with no row falls through to WROLE=$MARVEL_ROLE, and there is
# no wardrobe role named infra-builder, so the cast would slice nothing. The row
# maps it to builder. Its default scope is generic on purpose: this repo is
# public, and the exact scope lives in the private seat file, which passes it
# in CAST_SCOPE.
if cast infra-builder; then
  grep -q "stub slice for builder" "$root/out/claude.args" \
    && ok "infra-builder: slices the wardrobe builder role" \
    || bad "infra-builder: slices the wardrobe builder role" "$(cat "$root/out/claude.args")"
  grep -q "Your scope, set at cast time and recorded by the supervisor: the infra checkout and its MCP, as named in the private seat file; production is read-only." "$root/out/claude.args" \
    && ok "infra-builder: the default scope is the generic one" \
    || bad "infra-builder: the default scope is the generic one"
else
  bad "infra-builder cast" "$(cat "$root/out/stderr")"
fi
if cast infra-builder CAST_SCOPE="CAST-SCOPE-MARK only"; then
  grep -q "recorded by the supervisor: CAST-SCOPE-MARK only." "$root/out/claude.args" \
    && ok "CAST_SCOPE replaces the default scope" \
    || bad "CAST_SCOPE replaces the default scope" "$(cat "$root/out/claude.args")"
  grep -q "as named in the private seat file" "$root/out/claude.args" \
    && bad "CAST_SCOPE leaves no trace of the default" || ok "CAST_SCOPE leaves no trace of the default"
else
  bad "infra-builder cast with CAST_SCOPE" "$(cat "$root/out/stderr")"
fi
if cast builder CAST_SCOPE="BUILDER-SCOPE-MARK"; then
  grep -q "recorded by the supervisor: BUILDER-SCOPE-MARK." "$root/out/claude.args" \
    && ok "CAST_SCOPE works for any role, builder included" \
    || bad "CAST_SCOPE works for any role, builder included"
else
  bad "builder cast with CAST_SCOPE" "$(cat "$root/out/stderr")"
fi

# --- C-5: the channel cue is opt-in per seat (sim/design/channel-cue.md) -------
# Operator rulings 2026-10-01: the development-channels flag is adopted per
# seat. Only DIRECTOR_CUE=1, the value the shim itself honors, turns it on.
# Unset, the launch line must be exactly today's.
today_head="$(printf '%s\n' -n verify-builder-0 --strict-mcp-config --mcp-config \
  "{\"mcpServers\":{\"director\":{\"command\":\"$root/bin/director-mcp\",\"env\":{\"DIRECTOR_AGENT_ID\":\"verify-builder-0\",\"DIRECTOR_ROLE\":\"builder\",\"DIRECTOR_TEAM\":\"fleet\",\"DIRECTOR_WORKSPACE\":\"verifyws\",\"NATS_URL\":\"nats://127.0.0.1:4222\"}}}}" \
  --append-system-prompt)"
if cast builder; then
  [[ "$(head -6 "$root/out/claude.args")" == "$today_head" ]] \
    && ok "cue unset: the launch line is today's, byte for byte" \
    || bad "cue unset: the launch line is today's, byte for byte" "$(head -6 "$root/out/claude.args")"
  grep -q -- "--dangerously-load-development-channels" "$root/out/claude.args" \
    && bad "cue unset: no development-channels flag" || ok "cue unset: no development-channels flag"
  grep -qi "channel cue" "$root/out/stderr" \
    && bad "cue unset: the spawn line says nothing about a cue" || ok "cue unset: the spawn line says nothing about a cue"
else
  bad "builder cast, cue unset" "$(cat "$root/out/stderr")"
fi
if cast builder DIRECTOR_CUE=1; then
  in_mcp DIRECTOR_CUE 1 && ok "cue on: the shim env in mcp_json carries DIRECTOR_CUE=1" \
    || bad "cue on: the shim env in mcp_json carries DIRECTOR_CUE=1" "$(cat "$root/out/claude.args")"
  grep -A1 -x -- "--dangerously-load-development-channels" "$root/out/claude.args" | tail -1 | grep -qx "server:director" \
    && ok "cue on: the flag names the director server" \
    || bad "cue on: the flag names the director server" "$(cat "$root/out/claude.args")"
  # A dangerous-shaped flag that is on must be visible (review of #175): the
  # variable can be inherited from whatever shell started the tmux server.
  grep -q "^cast-launch: verify-builder-0 -> .*, channel cue ON (DIRECTOR_CUE=1)" "$root/out/stderr" \
    && ok "cue on: the spawn line says the channel cue is on" \
    || bad "cue on: the spawn line says the channel cue is on" "$(cat "$root/out/stderr")"
else
  bad "builder cast, cue on" "$(cat "$root/out/stderr")"
fi
for v in 0 true yes; do
  if cast builder DIRECTOR_CUE="$v"; then
    if has_mcp DIRECTOR_CUE || grep -q -- "--dangerously-load-development-channels" "$root/out/claude.args"; then
      bad "cue=$v: only the value 1 opts in"
    else
      ok "cue=$v: only the value 1 opts in"
    fi
  else
    bad "builder cast, cue=$v" "$(cat "$root/out/stderr")"
  fi
done

# --- a supervisor cast without levers on a leafed host is loud, not refused --
# director#180. The global tier stays optional: the launcher warns only when the
# local broker's monitor reports a leaf remote up, and a closed monitor port, a
# timeout or an unreadable reply all read as "not connected" (no warning).
leafz() { rm -f "$root/leafz.json" "$root/out/curl.args"; [[ -n "${1:-}" ]] && printf '%s' "$1" > "$root/leafz.json"; return 0; }
LEAF_UP='{"server_id":"x","now":"t","leafnodes":1,"leafs":[{"name":"hub"}]}'
LEAF_NONE='{"server_id":"x","now":"t","leafnodes":0,"leafs":[]}'

for role in supervisor research-supervisor; do
  leafz "$LEAF_UP"
  if cast "$role"; then
    miss=""
    grep -q "global tier is connected" "$root/out/stderr"        || miss+=" stderr-warning"
    grep -q "monitor port closed" "$root/out/stderr"             || miss+=" stderr-closed-port-caveat"
    grep -q "no global address" "$root/out/claude.args"          || miss+=" prompt-note"
    has_mcp DIRECTOR_GLOBAL_ROLE                                 && miss+=" levers-appeared"
    grep -qx "http://127.0.0.1:8222/leafz" "$root/out/curl.args" || miss+=" loopback-url"
    [[ -z "$miss" ]] && ok "$role: no levers on a leafed host warns on stderr and in the prompt, and still casts" \
                     || bad "$role lever warning" "missing:$miss"
  else
    bad "$role cast without levers on a leafed host was refused; it must warn only" "$(cat "$root/out/stderr")"
  fi
done

leafz ""
if cast supervisor; then
  grep -q "global tier is connected" "$root/out/stderr" \
    && bad "supervisor: a closed monitor port produced a warning" \
    || ok "supervisor: a closed monitor port reads as not connected, no warning"
else
  bad "supervisor cast with the monitor closed" "$(cat "$root/out/stderr")"
fi

leafz "$LEAF_NONE"
if cast supervisor; then
  grep -q "global tier is connected" "$root/out/stderr" \
    && bad "supervisor: zero leaf remotes produced a warning" \
    || ok "supervisor: zero leaf remotes reads as not connected, no warning"
else
  bad "supervisor cast with zero leaf remotes" "$(cat "$root/out/stderr")"
fi

leafz "$LEAF_UP"
if cast supervisor DIRECTOR_NATS_MONITOR_URL=http://127.0.0.1:9999; then
  grep -qx "http://127.0.0.1:9999/leafz" "$root/out/curl.args" \
    && ok "supervisor: DIRECTOR_NATS_MONITOR_URL moves the monitor endpoint" \
    || bad "monitor URL override" "$(cat "$root/out/curl.args" 2>/dev/null)"
else
  bad "supervisor cast with a monitor override" "$(cat "$root/out/stderr")"
fi

leafz "$LEAF_UP"
if cast supervisor DIRECTOR_NATS_MONITOR_URL=http://192.0.2.1:8222; then
  if [[ -f "$root/out/curl.args" ]] || grep -q "global tier is connected" "$root/out/stderr"; then
    bad "supervisor: a non-loopback monitor URL was queried"
  else
    ok "supervisor: a non-loopback monitor URL is never queried and reads as not connected"
  fi
else
  bad "supervisor cast with a non-loopback monitor" "$(cat "$root/out/stderr")"
fi

# Userinfo and a path suffix must not steer the probe off the loopback host:
# curl connects to the host after the "@", so a prefix match is not a guard.
for url in 'http://127.0.0.1:1@example.invalid:8222' 'http://localhost:1@other.invalid:8222' 'http://127.0.0.1:8222/x'; do
  leafz "$LEAF_UP"
  if cast supervisor "DIRECTOR_NATS_MONITOR_URL=$url"; then
    if [[ -f "$root/out/curl.args" ]] || grep -q "global tier is connected" "$root/out/stderr"; then
      bad "supervisor: the monitor URL $url was queried"
    else
      ok "supervisor: the monitor URL $url is refused by the loopback guard and never queried"
    fi
  else
    bad "supervisor cast with monitor $url" "$(cat "$root/out/stderr")"
  fi
done

# A trailing slash on a loopback override is still the same endpoint.
leafz "$LEAF_UP"
if cast supervisor DIRECTOR_NATS_MONITOR_URL=http://127.0.0.1:9999/; then
  grep -qx "http://127.0.0.1:9999/leafz" "$root/out/curl.args" && grep -q "global tier is connected" "$root/out/stderr" \
    && ok "supervisor: a loopback override with a trailing slash is still queried" \
    || bad "trailing-slash override" "$(cat "$root/out/curl.args" 2>/dev/null)"
else
  bad "supervisor cast with a trailing-slash monitor" "$(cat "$root/out/stderr")"
fi

# A proxy in the environment or in ~/.curlrc must not carry the probe off-box:
# curl is told to skip .curlrc (-q, which must come first) and to use no proxy.
leafz "$LEAF_UP"
if cast supervisor http_proxy=http://127.0.0.1:18224; then
  miss=""
  [[ "$(head -1 "$root/out/curl.args" 2>/dev/null)" == "-q" ]] || miss+=" -q-first"
  grep -qx -- "--noproxy" "$root/out/curl.args" 2>/dev/null   || miss+=" --noproxy"
  [[ -z "$miss" ]] && ok "supervisor: the probe skips .curlrc and every proxy" \
                   || bad "probe proxy isolation" "missing:$miss"
else
  bad "supervisor cast with a proxy set" "$(cat "$root/out/stderr")"
fi

leafz "$LEAF_UP"
if cast builder; then
  if [[ -f "$root/out/curl.args" ]] || grep -q "global tier is connected" "$root/out/stderr"; then
    bad "builder: a worker cast probed the monitor or warned"
  else
    ok "builder: a worker cast on a leafed host never probes and never warns"
  fi
else
  bad "builder cast on a leafed host" "$(cat "$root/out/stderr")"
fi

# --- a supervisor holding a global address tests its reach once at startup ---
leafz ""
if cast supervisor "${GLOBAL_ON[@]}"; then
  miss=""
  grep -q "send one reach test to global://director" "$root/out/claude.args"   || miss+=" reach-test"
  grep -q "report .* to global://director" "$root/out/claude.args"            || miss+=" report-target"
  [[ -f "$root/out/curl.args" ]]                                               && miss+=" probed-with-levers"
  [[ -z "$miss" ]] && ok "supervisor with levers: a startup reach test to global://director, reported to the director" \
                   || bad "supervisor reach test" "missing:$miss"
else
  bad "supervisor cast with levers" "$(cat "$root/out/stderr")"
fi

if cast supervisor "${GLOBAL_ON[@]}" DIRECTOR_PEER_CLUSTER=peerc; then
  grep -q "send one reach test to global://peerc/supervisor" "$root/out/claude.args" \
    && ok "supervisor with a peer cluster: the reach test goes to the peer's supervisor" \
    || bad "peer reach test" "$(grep -o 'reach test[^.]*' "$root/out/claude.args")"
else
  bad "supervisor cast with a peer cluster" "$(cat "$root/out/stderr")"
fi

if cast supervisor "${GLOBAL_ON[@]}" 'DIRECTOR_PEER_CLUSTER=peer.c'; then
  bad "a peer cluster outside the identity class was accepted"
else
  grep -q "DIRECTOR_PEER_CLUSTER" "$root/out/stderr" \
    && ok "supervisor: a peer cluster outside [A-Za-z0-9_-] is refused before it becomes an address" \
    || bad "peer cluster class refusal" "$(cat "$root/out/stderr")"
fi

for role in builder director; do
  if cast "$role" "${GLOBAL_ON[@]}"; then
    grep -q "reach test" "$root/out/claude.args" \
      && bad "$role: carries a reach test it should not" \
      || ok "$role: no supervisor reach test"
  else
    bad "$role cast with levers" "$(cat "$root/out/stderr")"
  fi
done

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
