#!/usr/bin/env bash
# The cast-launch test harness, sourced by sim/twin/verify-cast-launch.sh and
# sim/twin/parity-table.sh so both drive the launcher the same way: the stubs
# (a role-library root, the shim, claude, curl), cast(), and the lookups over what
# claude was handed and ran with. The caller sets LAUNCH first. Not run on its own.
root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT

# --- the stubs ----------------------------------------------------------------
# A wardrobe install root whose slice.sh prints a slice and nothing else.
mkdir -p "$root/wardrobe/contents/roles" "$root/wardrobe/scripts" "$root/bin" "$root/out"
cat > "$root/wardrobe/scripts/slice.sh" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "${WARDROBE_SPAWN_LOG:-unset}" > "$(dirname "$0")/../../out/slice.spawnlog"
printf '%s\n' "$@" > "$(dirname "$0")/../../out/slice.args"
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
launch_args=() # arguments handed to the launcher itself, as marvel passes a role's args
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
    "$LAUNCH" ${launch_args[@]+"${launch_args[@]}"} >"$root/out/stdout" 2>"$root/out/stderr"
}

# Does a lever appear in the mcp-config claude was handed?
in_mcp()  { grep -q "\"$1\":\"$2\"" "$root/out/claude.args"; }
has_mcp() { grep -q "$1" "$root/out/claude.args"; }
# Does a lever appear in an environment dump? (anchored: DIRECTOR_CLUSTER must
# not match DIRECTOR_CLUSTER_SOMETHING, and absence must mean absence.)
in_env()  { grep -qx "$1=$2" "$root/out/$3"; }
has_env() { grep -q "^$1=" "$root/out/$2"; }

