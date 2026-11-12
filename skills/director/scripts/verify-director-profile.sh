#!/usr/bin/env bash
# Prove director-profile, the reader and structural check of the operating
# profile (design: sim/design/director-bootstrap.md section 2.3; first build,
# aae-orc-d2sol and director's rulings of 2026-11-12 recorded on that ticket).
#
# Broker-free and fleet-free: nothing connects, nothing reads a real ~/.director.
# Every run is env -i with a scratch HOME and TMPDIR, so no MARVEL_*, DIRECTOR_*
# or NATS_* variable reaches the script.
#
# Covered:
#  - the minimal built-in profile when no file is found, and the lookup order
#    ($DIRECTOR_PROFILE, then $DIRECTOR_STATE/operating-profile.toml);
#  - the accepted shape: schema_version = 1, the closed fleet_sources set, the
#    [[model]] entries printed and not interpreted, [[team]] with match and
#    entry_role and a default rules of ["has-entry"];
#  - refusals, each naming the key or rule: a missing or wrong schema_version,
#    an unknown key (top level, [[team]], [[model]]), a [[team]] without match or
#    entry_role, an unknown fleet source, an unknown team rule, a file that is
#    not TOML, several errors reported together;
#  - roles-from, same-cluster and check are refused as "not in the first build",
#    as a [[team]] key and as a rules entry;
#  - the entry for a team: fnmatch over "<workspace>/<team>", first match in
#    file order wins;
#  - path expansion: ~ and $DIRECTOR_STATE only, a relative path is relative to
#    the profile file's directory;
#  - the python rule: python3 older than 3.11 is refused with one line, and a
#    newer python3.N on PATH is used instead.
#
# Usage: skills/director/scripts/verify-director-profile.sh   Exit nonzero on any miss.
#        DIRECTOR_PROFILE_BIN=<path> to test another copy.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN="${DIRECTOR_PROFILE_BIN:-$here/director-profile}"
[[ -x "$BIN" ]] || { echo "director-profile not executable at $BIN"; exit 2; }
PY="$(command -v python3 || true)"
[[ -n "$PY" ]] || { echo "python3 not on PATH"; exit 2; }
"$PY" -c 'import sys; sys.exit(0 if sys.version_info >= (3, 11) else 1)' || { echo "python3 3.11 or newer is needed to run this check"; exit 2; }

work="$(cd "$(mktemp -d "${TMPDIR:-/tmp}/director-profile-verify.XXXXXX")" && pwd -P)"
trap 'rm -rf "$work"' EXIT
home="$work/home"
state="$work/state"
mkdir -p "$home" "$state" "$work/tmp"
pydir="$(dirname "$PY")"

fail=0
miss() { echo "MISS: $*"; fail=1; }

# run [env...] -- args...: runs the script env -i with a scratch HOME, TMPDIR
# and DIRECTOR_STATE; sets rc and out (stdout and stderr together).
run() {
  local envs=()
  while [[ $# -gt 0 && "$1" != "--" ]]; do envs+=("$1"); shift; done
  shift
  set +e
  out="$(env -i HOME="$home" TMPDIR="$work/tmp" PATH="$pydir:/usr/bin:/bin" DIRECTOR_STATE="$state" ${envs[@]+"${envs[@]}"} "$BIN" "$@" 2>&1 </dev/null)"
  rc=$?
  set -e
}

# wprof <file> <line>...: write a profile, one argument per line (printf, not a here-document).
wprof() { local f="$1"; shift; printf '%s\n' "$@" >"$f"; }

good=(
  'schema_version = 1'
  'name = "example"'
  'fleet_sources = ["marvel-register", "bus-presence"]'
  'handoff = "$DIRECTOR_STATE/handoff.md"'
  'doc_domain = ["**/*.md"]'
  'write_domain = ["~/notes/*"]'
  'rules_text = ["rules.md"]'
  ''
  '[[model]]'
  'kind = "role-catalog"'
  'pack = "my-roles@0.2.0"'
  'scope = "deployment"'
  ''
  '[[model]]'
  'kind = "anything-at-all"'
  'path = "/some/where"'
  'scope = "weird:scope"'
  ''
  '[[team]]'
  'match = "aae/*"'
  'entry_role = "supervisor"'
)

# ---- P1: the accepted shape, printed ----
wprof "$work/good.toml" "${good[@]}"
run -- check "$work/good.toml"
[[ $rc -eq 0 && "$out" == *"profile ok"* && "$out" == *"name: example"* ]] || miss "P1 good profile: rc=$rc out=$out"
[[ "$out" == *"fleet_sources: marvel-register, bus-presence"* ]] || miss "P1 fleet_sources not printed: $out"
[[ "$out" == *"team aae/*: entry_role supervisor, rules has-entry"* ]] || miss "P1 team line (rules default has-entry): $out"
[[ "$out" == *"model role-catalog pack my-roles@0.2.0 scope deployment"* && "$out" == *"model anything-at-all path /some/where scope weird:scope"* ]] || miss "P1 [[model]] entries must be printed as written, not interpreted: $out"

# ---- P2: no profile, the minimal built-in ----
run -- check
[[ $rc -eq 0 && "$out" == *"minimal profile"* && "$out" == *"fleet_sources: bus-presence"* && "$out" == *"entry_role: unset"* ]] || miss "P2 minimal profile: rc=$rc out=$out"
[[ "$out" == *"routing into a team is refused until an entry role is named"* ]] || miss "P2 minimal profile must say what it means: $out"

# ---- P3 and P4: lookup order ----
wprof "$state/operating-profile.toml" 'schema_version = 1' 'name = "from-state"'
run -- check
[[ $rc -eq 0 && "$out" == *"name: from-state"* ]] || miss "P4 \$DIRECTOR_STATE/operating-profile.toml not used: rc=$rc out=$out"
wprof "$work/env.toml" 'schema_version = 1' 'name = "from-env"'
run DIRECTOR_PROFILE="$work/env.toml" -- check
[[ $rc -eq 0 && "$out" == *"name: from-env"* && "$out" != *"from-state"* ]] || miss "P3 \$DIRECTOR_PROFILE must win: rc=$rc out=$out"
rm -f "$state/operating-profile.toml"

# ---- P5 to P9: refusals name the key ----
refuse() { # <label> <expected text> <profile line>...
  local label="$1" want="$2"; shift 2
  wprof "$work/bad.toml" "$@"
  run -- check "$work/bad.toml"
  [[ $rc -eq 1 && "$out" == *"$want"* ]] || miss "$label: rc=$rc want '$want' in: $out"
}
refuse "P5 missing schema_version" "schema_version" 'name = "x"'
refuse "P6 schema_version 2" "schema_version" 'schema_version = 2'
refuse "P7 unknown top-level key" "unknown key: colour" 'schema_version = 1' 'colour = "red"'
refuse "P8 unknown key in [[team]]" "unknown key: team[1].colour" 'schema_version = 1' '[[team]]' 'match = "*"' 'entry_role = "s"' 'colour = "red"'
refuse "P8 unknown key in [[model]]" "unknown key: model[1].colour" 'schema_version = 1' '[[model]]' 'kind = "k"' 'colour = "red"'
refuse "P9 team without entry_role" "team[1]: entry_role is required" 'schema_version = 1' '[[team]]' 'match = "*"'
refuse "P9 team without match" "team[1]: match is required" 'schema_version = 1' '[[team]]' 'entry_role = "s"'
refuse "P13 unknown fleet source" "unknown fleet source: carrier-pigeon" 'schema_version = 1' 'fleet_sources = ["bus-presence", "carrier-pigeon"]'
refuse "P12 unknown team rule" "unknown team rule: shiny" 'schema_version = 1' '[[team]]' 'match = "*"' 'entry_role = "s"' 'rules = ["has-entry", "shiny"]'
refuse "P17 name must be a string" "name must be a string" 'schema_version = 1' 'name = 7'
refuse "P17 fleet_sources must be a list" "fleet_sources must be a list" 'schema_version = 1' 'fleet_sources = "bus-presence"'

# ---- P18: not TOML (a repeated key is refused by the parser) ----
wprof "$work/dup.toml" 'schema_version = 1' 'name = "a"' 'name = "b"'
run -- check "$work/dup.toml"
[[ $rc -eq 1 && "$out" == *"not valid TOML"* && "$out" == *"dup.toml"* ]] || miss "P18 invalid TOML: rc=$rc out=$out"

# ---- P19: several errors are reported together ----
wprof "$work/two.toml" 'schema_version = 1' 'colour = "red"' 'shape = "round"'
run -- check "$work/two.toml"
[[ $rc -eq 1 && "$out" == *"unknown key: colour"* && "$out" == *"unknown key: shape"* ]] || miss "P19 both unknown keys must be named: rc=$rc out=$out"

# ---- P11: roles-from, same-cluster and check are not in the first build ----
for r in roles-from same-cluster check; do
  refuse "P11 $r as a [[team]] key" "not in the first build: $r" 'schema_version = 1' '[[team]]' 'match = "*"' 'entry_role = "s"' "$r = \"x\""
  refuse "P11 $r as a rules entry" "not in the first build: $r" 'schema_version = 1' '[[team]]' 'match = "*"' 'entry_role = "s"' "rules = [\"has-entry\", \"$r\"]"
done

# ---- P14: the entry for a team, first match in file order wins ----
wprof "$work/entry.toml" \
  'schema_version = 1' \
  '[[team]]' 'match = "aae/res*"' 'entry_role = "lead"' \
  '[[team]]' 'match = "aae/*"' 'entry_role = "supervisor"' \
  '[[team]]' 'match = "*"' 'entry_role = "catchall"'
run -- entry "$work/entry.toml" aae/research
[[ $rc -eq 0 && "$out" == "lead" ]] || miss "P14 first match in file order: rc=$rc out=$out"
run -- entry "$work/entry.toml" aae/builders
[[ $rc -eq 0 && "$out" == "supervisor" ]] || miss "P14 second entry: rc=$rc out=$out"
run -- entry "$work/entry.toml" other/team
[[ $rc -eq 0 && "$out" == "catchall" ]] || miss "P14 third entry (the * crosses the slash): rc=$rc out=$out"
wprof "$work/entry2.toml" 'schema_version = 1' '[[team]]' 'match = "aae/*"' 'entry_role = "supervisor"'
run -- entry "$work/entry2.toml" other/team
[[ $rc -eq 1 && "$out" == *"no entry role for other/team"* ]] || miss "P14 no match must say so and exit 1: rc=$rc out=$out"
run -- entry "$work/entry.toml" aae
[[ $rc -eq 2 && "$out" == *"WORKSPACE/TEAM"* ]] || miss "P14 a team without a workspace is a usage error: rc=$rc out=$out"

# ---- P15: path expansion, only ~ and $DIRECTOR_STATE ----
mkdir -p "$work/prof"
wprof "$work/prof/paths.toml" \
  'schema_version = 1' \
  'handoff = "$DIRECTOR_STATE/handoff.md"' \
  'rules_text = ["notes.md", "~/rules.md", "$HOME/literal.md"]' \
  'write_domain = ["~/w/*", "sub/*"]'
run -- check "$work/prof/paths.toml"
[[ $rc -eq 0 && "$out" == *"handoff: $state/handoff.md"* ]] || miss "P15 \$DIRECTOR_STATE not expanded: rc=$rc out=$out"
[[ "$out" == *"rules_text: $work/prof/notes.md"* && "$out" == *"rules_text: $home/rules.md"* ]] || miss "P15 relative path must be relative to the profile directory and ~ expanded: $out"
[[ "$out" == *"rules_text: $work/prof/\$HOME/literal.md"* ]] || miss "P15 \$HOME is not expanded in the first build: $out"
[[ "$out" == *"write_domain: $home/w/*"* && "$out" == *"write_domain: $work/prof/sub/*"* ]] || miss "P15 write_domain patterns expanded: $out"

# ---- P20 and P21: the python rule ----
# The script is run by a python3 whose version reads 3.9.6, through runpy, so a
# host with a newer python3 can still prove the refusal.
old_py() { # <path dir>: run the script as a 3.9.6 interpreter would see it
  set +e
  out="$(env -i HOME="$home" TMPDIR="$work/tmp" PATH="$1" "$PY" -c 'import runpy, sys; sys.version_info = (3, 9, 6, "final", 0); sys.argv = [sys.argv[1], "check"]; runpy.run_path(sys.argv[0], run_name="__main__")' "$BIN" 2>&1 </dev/null)"
  rc=$?
  set -e
}
mkdir -p "$work/emptybin"
old_py "$work/emptybin"
[[ $rc -eq 2 && "$(printf '%s\n' "$out" | wc -l | tr -d ' ')" == "1" && "$out" == *"python3 3.11 or newer"* && "$out" == *"3.9"* ]] || miss "P20 old python3 must be refused with one line naming 3.11: rc=$rc out=$out"
mkdir -p "$work/newbin"
printf '%s\n' '#!/bin/sh' "echo found-python312 >\"$work/found\"" >"$work/newbin/python3.12"
chmod +x "$work/newbin/python3.12"
old_py "$work/newbin"
[[ -f "$work/found" ]] || miss "P21 a python3.12 on PATH must be used when python3 is too old: rc=$rc out=$out"

if [[ $fail -ne 0 ]]; then echo "FAIL"; exit 1; fi
echo "ok: director-profile reads the first-build profile, refuses what is not in it, and names every key it refuses"
