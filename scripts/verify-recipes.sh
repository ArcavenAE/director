#!/usr/bin/env bash
# Prove director's recipes library (recipes/): the bus-seat recipe carries, as
# data, what cast-launch decides today for the global tier, and the checks over
# it can fail. Every check runs against the shipped library and against fixture
# libraries written here, so a check that passes on the shipped file is shown to
# fail on a file that breaks its rule (a positive control for each).
#
# Needs python3 (3.11 or newer, for tomllib), git and bash. Reads only the
# repository and a scratch directory. No live manifest, keychain or ~/.marvel
# file is read or written, and the cast-launch cases run with the stubs of
# sim/twin/cast-stubs.sh.
#
# Usage: scripts/verify-recipes.sh     Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LIB="$here/recipes"
LINT="$here/scripts/recipes_lint.py"
PARITY="$here/sim/twin/parity-table.sh"
STAMP="$here/scripts/stamp-recipes.sh"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

tmp="$(mktemp -d "${TMPDIR:-/tmp}/director-recipes.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT
export HOME="$tmp/home"; mkdir -p "$HOME"
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@example.invalid GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@example.invalid
PY="${PYTHON:-python3}"

lint() { "$PY" "$LINT" "$1" 2>&1; }
# lint_fails DIR CHECK TEXT: the lint exits nonzero and a FAIL line for CHECK names TEXT
lint_fails() {
  local out rc=0; out="$(lint "$1")" || rc=$?
  [[ "$rc" == 1 ]] && printf '%s\n' "$out" | grep -F "FAIL $2:" | grep -qF -- "$3"
}
lint_passes() { local out rc=0; out="$(lint "$1")" || rc=$?; [[ "$rc" == 0 && -z "$out" ]]; }

# a known-good library, written here, so a control does not depend on the shipped file
mklib() {
  local d="$1"; rm -rf "$d"; mkdir -p "$d/recipes"
  cat > "$d/company.toml" <<'TOML'
name = "director"
kind = "recipes"
schema = 1
requires_marvel = "0.9.0"
recipe_schema = 1
requires = { definition_schema = 1 }
TOML
  cat > "$d/recipes/bus-seat.toml" <<'TOML'
schema = 1
adapter = "claude"
args = ["-n", "{session}"]

[params.cue]
type = "bool"

[env]
DIRECTOR_ROLE = "{role}"

[mcp.director]
command = "tool:director-mcp"
strict = true

[preflight]
command = ["tool:director-mcp", "--preflight"]

[when.cue]
env = { DIRECTOR_CUE = "1" }
args = ["--dangerously-load-development-channels", "server:director"]

[when.global.non_holder]
unset = ["DIRECTOR_GLOBAL_DOMAIN", "DIRECTOR_GLOBAL_ROLE"]
TOML
  cat > "$d/switch-list.toml" <<'TOML'
[roles]
director = "not until marvel admits"
research-supervisor = "not until marvel admits"
builder = "switchable"
envoy = "switchable"
TOML
}
# edit DIR FILE OLD NEW: replace OLD with NEW in a fixture file, failing if OLD is absent
edit() {
  "$PY" - "$1/$2" "$3" "$4" <<'PY'
import sys
p, old, new = sys.argv[1:4]
s = open(p).read()
assert old in s, old
open(p, "w").write(s.replace(old, new, 1))
PY
}

mklib "$tmp/good"
lint_passes "$tmp/good" && ok "control: the known-good fixture library passes every check" || bad "control: the known-good fixture library passes every check" "$(lint "$tmp/good")"

# ---- the shipped library passes every check --------------------------------
if lint_passes "$LIB"; then ok "the shipped recipes/ library passes every check"; else bad "the shipped recipes/ library passes every check" "$(lint "$LIB" | head -6 | tr '\n' '|')"; fi

# 1. T-A-4, the director half: the non-holder block unsets exactly the two levers
if lint_passes "$tmp/good"; then ok "T-A-4 control: a block with both names passes"; else bad "T-A-4 control: a block with both names passes"; fi
mklib "$tmp/t1a"; edit "$tmp/t1a" recipes/bus-seat.toml 'unset = ["DIRECTOR_GLOBAL_DOMAIN", "DIRECTOR_GLOBAL_ROLE"]' 'unset = ["DIRECTOR_GLOBAL_DOMAIN"]'
lint_fails "$tmp/t1a" T-A-4 DIRECTOR_GLOBAL_ROLE && ok "T-A-4 a block missing DIRECTOR_GLOBAL_ROLE fails, naming it" || bad "T-A-4 a block missing DIRECTOR_GLOBAL_ROLE fails, naming it"
mklib "$tmp/t1b"; edit "$tmp/t1b" recipes/bus-seat.toml 'unset = ["DIRECTOR_GLOBAL_DOMAIN", "DIRECTOR_GLOBAL_ROLE"]' 'unset = ["DIRECTOR_GLOBAL_DOMAIN", "DIRECTOR_GLOBAL_ROLE", "DIRECTOR_CLUSTER"]'
lint_fails "$tmp/t1b" T-A-4 DIRECTOR_CLUSTER && ok "T-A-4 a block that adds DIRECTOR_CLUSTER fails, naming it" || bad "T-A-4 a block that adds DIRECTOR_CLUSTER fails, naming it"
mklib "$tmp/t1c"; edit "$tmp/t1c" recipes/bus-seat.toml 'unset = ["DIRECTOR_GLOBAL_DOMAIN", "DIRECTOR_GLOBAL_ROLE"]' 'unset = []'
lint_fails "$tmp/t1c" T-A-4 DIRECTOR_GLOBAL_DOMAIN && ok "T-A-4 the empty block of the stub fails, naming the missing levers" || bad "T-A-4 the empty block of the stub fails, naming the missing levers"

# 2. R2-B, one source for the domain
mklib "$tmp/t2a"; cat >> "$tmp/t2a/recipes/bus-seat.toml" <<'TOML'

[when.global.holder]
env = { DIRECTOR_GLOBAL_DOMAIN = "global" }
TOML
if lint_fails "$tmp/t2a" R2-B DIRECTOR_GLOBAL_DOMAIN && lint_fails "$tmp/t2a" R2-B "when.global.holder"; then ok "R2-B a holder block that sets the domain fails, naming the key"; else bad "R2-B a holder block that sets the domain fails, naming the key"; fi
mklib "$tmp/t2b"; edit "$tmp/t2b" recipes/bus-seat.toml 'DIRECTOR_ROLE = "{role}"' 'DIRECTOR_ROLE = "{var:director.role}"'
lint_fails "$tmp/t2b" R2-B "{var:" && ok "R2-B a {var: value fails" || bad "R2-B a {var: value fails"
mklib "$tmp/t2c"; edit "$tmp/t2c" recipes/bus-seat.toml 'DIRECTOR_ROLE = "{role}"' 'DIRECTOR_ROLE = "{role}"
DIRECTOR_GLOBAL_DOMAIN = "global"'
lint_fails "$tmp/t2c" R2-B DIRECTOR_GLOBAL_DOMAIN && ok "R2-B a base env that names the domain fails, naming the key" || bad "R2-B a base env that names the domain fails, naming the key"

# 3. R2-C, the switch list
mklib "$tmp/t3a"; edit "$tmp/t3a" switch-list.toml 'director = "not until marvel admits"' 'director = "switchable"'
lint_fails "$tmp/t3a" R2-C director && ok "R2-C a fixture that marks director switchable fails" || bad "R2-C a fixture that marks director switchable fails"
lint_passes "$tmp/good" && ok "R2-C control: the builder row passes as switchable" || bad "R2-C control: the builder row passes as switchable"
mklib "$tmp/t3b"; edit "$tmp/t3b" switch-list.toml 'research-supervisor = "not until marvel admits"' 'research-supervisor = "maybe"'
lint_fails "$tmp/t3b" R2-C research-supervisor && ok "R2-C research-supervisor must be marked not until marvel admits" || bad "R2-C research-supervisor must be marked not until marvel admits"

# 4. R2-D, the parity table can fail
ids="case4-maintainer case4-builder case4-marvel-builder case4-sideshow-builder case4-envoy case6-director"
parity() { # run the table with CAST_LAUNCH set (or unset), output to $tmp/parity.out
  rc=0; env -i HOME="$HOME" PATH="$PATH" ${CAST_LAUNCH:+CAST_LAUNCH="$CAST_LAUNCH"} bash "$PARITY" > "$tmp/parity.out" 2>&1 || rc=$?
}
parity
allpass=1; for id in $ids; do grep -q "^PASS $id" "$tmp/parity.out" || allpass=0; done
[[ "$rc" == 0 && "$allpass" == 1 ]] && ok "R2-D the cases 4 and 6 table passes every row against the unmodified cast-launch" || bad "R2-D the cases 4 and 6 table passes every row against the unmodified cast-launch (rc $rc)" "$(head -5 "$tmp/parity.out" | tr '\n' '|')"
printf '#!/usr/bin/env bash\necho "not implemented" >&2\nexit 3\n' > "$tmp/notimpl.sh"; chmod +x "$tmp/notimpl.sh"
CAST_LAUNCH="$tmp/notimpl.sh" parity
allfail=1; for id in $ids; do grep -q "^FAIL $id" "$tmp/parity.out" || allfail=0; done
[[ "$rc" != 0 && "$allfail" == 1 ]] && ok "R2-D red and control: the same table fails every row against a not-implemented runner" || bad "R2-D red and control: the same table fails every row against a not-implemented runner (rc $rc)"
# a copy of cast-launch with the unset of the three levers removed (the else branch of the global tier)
grep -c '^  unset DIRECTOR_GLOBAL_DOMAIN DIRECTOR_CLUSTER DIRECTOR_GLOBAL_ROLE$' "$here/sim/twin/cast-launch.sh" | grep -qx 1 || bad "R2-D the unset line is not in cast-launch.sh once; the mutant below would test nothing"
grep -v '^  unset DIRECTOR_GLOBAL_DOMAIN DIRECTOR_CLUSTER DIRECTOR_GLOBAL_ROLE$' "$here/sim/twin/cast-launch.sh" > "$tmp/cast-launch-noleak.sh"; chmod +x "$tmp/cast-launch-noleak.sh"
CAST_LAUNCH="$tmp/cast-launch-noleak.sh" parity
leakrows=1
for id in case4-maintainer case4-builder case4-marvel-builder case4-sideshow-builder case4-envoy; do
  grep "^FAIL $id" "$tmp/parity.out" | grep -q 'DIRECTOR_GLOBAL_DOMAIN' || leakrows=0
done
[[ "$rc" != 0 && "$leakrows" == 1 ]] && ok "R2-D with the unset removed from a copy of cast-launch, every case 4 row fails, naming the leak" || bad "R2-D with the unset removed from a copy of cast-launch, every case 4 row fails, naming the leak (rc $rc)" "$(grep '^FAIL' "$tmp/parity.out" | head -3 | tr '\n' '|')"
grep -q '^PASS case6-director' "$tmp/parity.out" && ok "R2-D control: the director row still passes on that copy" || bad "R2-D control: the director row still passes on that copy"

# 5. C7, the credential screen
for name in DIRECTOR_NATS_USER DIRECTOR_NATS_PASS DIRECTOR_NATS_CREDS MARVEL_BUS_STATE; do
  mklib "$tmp/c7e"; edit "$tmp/c7e" recipes/bus-seat.toml 'DIRECTOR_ROLE = "{role}"' "DIRECTOR_ROLE = \"{role}\"
$name = \"x\""
  lint_fails "$tmp/c7e" C7 "$name" && ok "C7 an env that names $name fails, naming it" || bad "C7 an env that names $name fails, naming it"
done
mklib "$tmp/c7u"; edit "$tmp/c7u" recipes/bus-seat.toml 'unset = ["DIRECTOR_GLOBAL_DOMAIN", "DIRECTOR_GLOBAL_ROLE"]' 'unset = ["DIRECTOR_GLOBAL_DOMAIN", "DIRECTOR_GLOBAL_ROLE", "DIRECTOR_NATS_CREDS"]'
lint_fails "$tmp/c7u" C7 DIRECTOR_NATS_CREDS && ok "C7 an unset that names DIRECTOR_NATS_CREDS fails, naming it" || bad "C7 an unset that names DIRECTOR_NATS_CREDS fails, naming it"
mklib "$tmp/c7m"; edit "$tmp/c7m" recipes/bus-seat.toml 'strict = true' 'strict = true
args = ["--creds", "MARVEL_BUS_STATE"]'
lint_fails "$tmp/c7m" C7 MARVEL_BUS_STATE && ok "C7 an mcp table that names MARVEL_BUS_STATE fails, naming it" || bad "C7 an mcp table that names MARVEL_BUS_STATE fails, naming it"
lint_passes "$LIB" && ok "C7 control: the shipped recipe passes" || bad "C7 control: the shipped recipe passes"

# 6. A4-2a, known keys
mklib "$tmp/k1"; edit "$tmp/k1" recipes/bus-seat.toml '[preflight]' '[evn]
X = "1"

[preflight]'
lint_fails "$tmp/k1" A4-2a evn && ok "A4-2a a fixture with [evn] fails, naming the key" || bad "A4-2a a fixture with [evn] fails, naming the key"
mklib "$tmp/k2"; edit "$tmp/k2" company.toml 'schema = 1' 'schema = 1
requries_marvel = "0.9.0"'
lint_fails "$tmp/k2" A4-2a requries_marvel && ok "A4-2a a misspelled company.toml key fails, naming it" || bad "A4-2a a misspelled company.toml key fails, naming it"
mklib "$tmp/k3"; edit "$tmp/k3" recipes/bus-seat.toml '[preflight]' '[preflight]
args = ["--preflight"]'
lint_fails "$tmp/k3" A4-2a args && ok "A4-2a a preflight args key (not in the schema) fails, naming it" || bad "A4-2a a preflight args key (not in the schema) fails, naming it"

# 7. A4-2b, tool commands
mklib "$tmp/b1"; edit "$tmp/b1" recipes/bus-seat.toml 'command = "tool:director-mcp"' 'command = "/usr/local/bin/director-mcp"'
lint_fails "$tmp/b1" A4-2b "/usr/local/bin/director-mcp" && ok "A4-2b a fixture with an absolute path command fails" || bad "A4-2b a fixture with an absolute path command fails"
mklib "$tmp/b2"; edit "$tmp/b2" recipes/bus-seat.toml '["tool:director-mcp", "--preflight"]' '["bin/director-mcp", "--preflight"]'
lint_fails "$tmp/b2" A4-2b "bin/director-mcp" && ok "A4-2b a preflight command without the tool: prefix fails" || bad "A4-2b a preflight command without the tool: prefix fails"

# 8. the stamp step: version and source are written into a copy, never into the committed file
for f in version source; do
  ! grep -q "^$f" "$LIB/company.toml" && ok "the committed company.toml carries no $f" || bad "the committed company.toml carries no $f"
done
S="$tmp/stampsrc"; mkdir -p "$S"; git -C "$S" init -q
cp -R "$LIB" "$S/recipes"
git -C "$S" remote add origin "https://user:token@example.invalid/org/director.git"
git -C "$S" add -A; GIT_COMMITTER_DATE="2026-11-09T12:00:00Z" GIT_AUTHOR_DATE="2026-11-09T12:00:00Z" git -C "$S" commit -q -m lib
full="$(git -C "$S" rev-parse HEAD)"; short="$(git -C "$S" rev-parse --short=7 HEAD)"
rc=0; "$STAMP" "$S/recipes" "$tmp/stamped" >"$tmp/stamp.out" 2>&1 || rc=$?
stamped="$tmp/stamped/company.toml"
if [[ "$rc" == 0 && -f "$stamped" ]]; then
  vv="$("$PY" -c 'import sys,tomllib; d=tomllib.load(open(sys.argv[1],"rb")); print(d.get("version",""))' "$stamped")"
  sc="$("$PY" -c 'import sys,tomllib; d=tomllib.load(open(sys.argv[1],"rb")); print(d.get("source",{}).get("commit",""))' "$stamped")"
  su="$("$PY" -c 'import sys,tomllib; d=tomllib.load(open(sys.argv[1],"rb")); print(d.get("source",{}).get("url",""))' "$stamped")"
  [[ "$vv" == "2026-11-09-$short" ]] && ok "stamp: version is <commit date>-<short sha>" || bad "stamp: version is <commit date>-<short sha> (got '$vv', want 2026-11-09-$short)"
  [[ "$sc" == "$full" ]] && ok "stamp: source.commit is the full commit sha" || bad "stamp: source.commit is the full commit sha (got '$sc')"
  [[ "$su" == "https://example.invalid/org/director" ]] && ok "stamp: source.url has no userinfo and no .git suffix" || bad "stamp: source.url has no userinfo and no .git suffix (got '$su')"
  lint_passes "$tmp/stamped" && ok "stamp: the stamped copy passes every check" || bad "stamp: the stamped copy passes every check" "$(lint "$tmp/stamped" | head -3 | tr '\n' '|')"
  [[ -f "$tmp/stamped/recipes/bus-seat.toml" ]] && cmp -s "$tmp/stamped/recipes/bus-seat.toml" "$S/recipes/recipes/bus-seat.toml" && ok "stamp: the recipe is copied byte for byte" || bad "stamp: the recipe is copied byte for byte"
  ! grep -q '^version' "$S/recipes/company.toml" && ok "stamp: the committed company.toml is left unchanged" || bad "stamp: the committed company.toml is left unchanged"
else bad "stamp: writes a company.toml with version and source (rc $rc)" "$(head -3 "$tmp/stamp.out" | tr '\n' '|')"; fi
# 8b. the stamp refuses an out-dir that would delete or overwrite committed files
before="$(git -C "$S" status --porcelain; git -C "$S" rev-parse HEAD)"
rc=0; "$STAMP" "$S/recipes" "$S/recipes" >/dev/null 2>&1 || rc=$?
[[ "$rc" != 0 && -f "$S/recipes/recipes/bus-seat.toml" && -z "$(git -C "$S" status --porcelain)" ]] && ok "stamp: refuses the library itself as the out-dir and leaves it intact" || bad "stamp: refuses the library itself as the out-dir and leaves it intact (rc $rc)"
rc=0; "$STAMP" "$S/recipes" "$S/recipes/stamped" >/dev/null 2>&1 || rc=$?
[[ "$rc" != 0 && ! -e "$S/recipes/stamped" ]] && ok "stamp: refuses an out-dir inside the library and writes nothing" || bad "stamp: refuses an out-dir inside the library and writes nothing (rc $rc)"
rc=0; "$STAMP" "$S/recipes" "$S" >/dev/null 2>&1 || rc=$?
[[ "$rc" != 0 && -f "$S/recipes/recipes/bus-seat.toml" && -d "$S/.git" ]] && ok "stamp: refuses a parent of the library as the out-dir and leaves it intact" || bad "stamp: refuses a parent of the library as the out-dir and leaves it intact (rc $rc)"
mkdir -p "$tmp/occupied"; echo keep > "$tmp/occupied/keep.txt"
rc=0; "$STAMP" "$S/recipes" "$tmp/occupied" >/dev/null 2>&1 || rc=$?
[[ "$rc" != 0 && "$(cat "$tmp/occupied/keep.txt" 2>/dev/null)" == keep && ! -e "$tmp/occupied/company.toml" ]] && ok "stamp: refuses an existing non-empty out-dir and leaves it intact" || bad "stamp: refuses an existing non-empty out-dir and leaves it intact (rc $rc)"
mkdir -p "$tmp/emptyout"
rc=0; "$STAMP" "$S/recipes" "$tmp/emptyout" >/dev/null 2>&1 || rc=$?
[[ "$rc" == 0 && -f "$tmp/emptyout/company.toml" ]] && ok "stamp: control: an existing empty out-dir is accepted" || bad "stamp: control: an existing empty out-dir is accepted (rc $rc)"
[[ "$before" == "$(git -C "$S" status --porcelain; git -C "$S" rev-parse HEAD)" ]] && ok "stamp: the source repository is unchanged by every refusal" || bad "stamp: the source repository is unchanged by every refusal"

# 9. the supervisor stays off the switch list until its cue default is decided
sup="$("$PY" -c 'import sys,tomllib; print(tomllib.load(open(sys.argv[1],"rb")).get("roles",{}).get("supervisor","absent"))' "$LIB/switch-list.toml")"
[[ "$sup" != "switchable" ]] && ok "the shipped switch list does not mark supervisor switchable (it is '$sup')" || bad "the shipped switch list does not mark supervisor switchable"
[[ "$sup" == "not until marvel admits" || "$sup" == "absent" ]] && ok "supervisor is held or unlisted, not given a new status word" || bad "supervisor is held or unlisted, not given a new status word (got '$sup')"
for r in builder marvel-builder sideshow-builder maintainer envoy reviewer claude-reviewer; do
  v="$("$PY" -c 'import sys,tomllib; print(tomllib.load(open(sys.argv[1],"rb")).get("roles",{}).get(sys.argv[2],"absent"))' "$LIB/switch-list.toml" "$r")"
  [[ "$v" == "switchable" ]] && ok "control: $r is still switchable" || bad "control: $r is still switchable (got '$v')"
done
echo "dirty" > "$S/recipes/extra.txt"
rc=0; "$STAMP" "$S/recipes" "$tmp/stamped2" >/dev/null 2>&1 || rc=$?
[[ "$rc" != 0 && ! -e "$tmp/stamped2/company.toml" ]] && ok "stamp: refuses a library with uncommitted changes and writes nothing" || bad "stamp: refuses a library with uncommitted changes and writes nothing (rc $rc)"

echo
echo "$pass passed, $fail failed"
(( fail == 0 ))
