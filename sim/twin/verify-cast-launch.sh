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
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LAUNCH="$here/cast-launch.sh"
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

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
