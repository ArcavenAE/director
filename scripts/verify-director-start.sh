#!/usr/bin/env bash
# Prove the director launcher and the operator script that clears the project-local
# director-mcp entry. Everything runs against a scratch HOME, a stub claude and a
# scratch copy of the config file's shape with fake values. Nothing reads or
# writes the real ~/.claude.json or any live session.
#
# Usage: scripts/verify-director-start.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
start="$here/scripts/director"
step="$here/scripts/operator-steps/2026-10-20-remove-project-local-director-mcp.sh"
cfg="$here/config/director-mcp.json"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }
check() { # name, command...
  local name="$1"; shift
  if "$@"; then ok "$name"; else bad "$name"; fi
}

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT

# ---- the launcher -------------------------------------------------------

home="$root/home"; mkdir -p "$home/.director"
cat >"$root/claude-stub" <<'STUB'
#!/usr/bin/env bash
# records what the launcher handed claude; never starts a session
{ printf 'ARGS:'; printf ' [%s]' "$@"; printf '\n'; } >"$STUB_OUT"
env | grep -E '^(DIRECTOR_|NATS_URL=)' | LC_ALL=C sort >>"$STUB_OUT"
STUB
chmod +x "$root/claude-stub"
cat >"$home/.director/seat.env" <<'SEAT'
# fake values for the test
DIRECTOR_AGENT_ID=fake-director
DIRECTOR_CLUSTER=fake-cluster
DIRECTOR_GLOBAL_DOMAIN=fake-domain
DIRECTOR_GLOBAL_ROLE=director
DIRECTOR_TEAM=fake-team
DIRECTOR_WORKSPACE=fake-ws
NATS_URL=nats://127.0.0.1:4999
SEAT

run_start() { # args...; runs in a clean env, scratch HOME, no MARVEL_SOCKET
  env -i HOME="$home" PATH="$PATH" LANG="${LANG:-C}" TMPDIR="$root" \
    CLAUDE_BIN="$root/claude-stub" STUB_OUT="$root/out" "$start" "$@"
}
tree() { # names and contents under HOME, so any write shows
  { find "$home" | LC_ALL=C sort; find "$home" -type f -exec shasum -a 256 {} + | LC_ALL=C sort; } | shasum -a 256
}
before="$(tree)"

rm -f "$root/out"
if run_start start >"$root/stdout" 2>&1; then ok "first start exits 0"; else bad "first start exits 0" "$(cat "$root/stdout")"; fi
check "first start passes the repo config" grep -q -- "\[--mcp-config\] \[$cfg\]" "$root/out"
check "first start names the session director" grep -q -- '\[-n\] \[director\]' "$root/out"
check "first start does not resume" bash -c "! grep -q -- '--resume' '$root/out'"
check "the seat file's identity reaches claude" grep -q '^DIRECTOR_TEAM=fake-team$' "$root/out"
check "the cluster comes from the seat file" grep -q '^DIRECTOR_CLUSTER=fake-cluster$' "$root/out"

rm -f "$root/out"
run_start start --cluster other-cluster >"$root/stdout" 2>&1 || true
check "--cluster overrides the seat file" grep -q '^DIRECTOR_CLUSTER=other-cluster$' "$root/out"

rm -f "$root/out"
run_start start --resume my-director >"$root/stdout" 2>&1 || true
check "--resume resumes the named session" grep -q -- '\[--resume\] \[my-director\]' "$root/out"
check "--resume does not rename it" bash -c "! grep -q -- '\[-n\]' '$root/out'"
after="$(tree)"
if [[ "$before" == "$after" ]]; then ok "the launcher wrote nothing under HOME"; else bad "the launcher wrote nothing under HOME"; fi

# no seat file and no flags: stop, name the missing keys, never run claude
rm -f "$root/out" "$home/.director/seat.env"
if run_start start >"$root/stdout" 2>&1; then bad "a missing seat file stops the launcher" "exit 0"; else ok "a missing seat file stops the launcher"; fi
check "the stop names a missing key" grep -q 'DIRECTOR_TEAM' "$root/stdout"
check "claude was not run" bash -c "[[ ! -e '$root/out' ]]"

# a seat file is data: a line that is not an allowed KEY=value is refused, never run
cat >"$home/.director/seat.env" <<SEAT
DIRECTOR_AGENT_ID=fake-director
DIRECTOR_TEAM=\$(touch $root/pwned)
PATH=/nowhere
SEAT
rm -f "$root/out"
if run_start start >"$root/stdout" 2>&1; then bad "a bad seat file is refused" "exit 0"; else ok "a bad seat file is refused"; fi
# a well-formed line for a key that is not on the list is refused too
cat >"$home/.director/seat.env" <<SEAT
DIRECTOR_AGENT_ID=fake-director
DIRECTOR_TEAM=fake-team
DIRECTOR_WORKSPACE=fake-ws
LD_PRELOAD=/nowhere
SEAT
rm -f "$root/out"
if run_start start >"$root/stdout" 2>&1; then bad "a key off the list is refused" "exit 0"; else ok "a key off the list is refused"; fi
check "a command in a seat file is never run" bash -c "[[ ! -e '$root/pwned' ]]"
check "claude was not run for a bad seat file" bash -c "[[ ! -e '$root/out' ]]"

# the repo config carries variable references only, never a value
check "the config is valid JSON with a director server" python3 -c "
import json,sys
d=json.load(open('$cfg'))
assert 'director' in d['mcpServers']
"
check "the config holds no literal identity" python3 -c "
import json,re
d=json.load(open('$cfg'))
env=d['mcpServers']['director'].get('env',{})
assert env, "env block missing"
bad=[k for k,v in env.items() if not re.fullmatch(r'\\$\\{[A-Z_]+\\}', v)]
assert not bad, bad
"

# ---- the operator script ------------------------------------------------

scratch="$root/claude.json"
mk() { # a scratch copy of the config file's shape, with fake values
  python3 - "$scratch" "$home" <<'PY'
import json,sys
p,h=sys.argv[1:3]
d={"numStartups":7,"mcpServers":{"slack":{"type":"http","url":"https://fake.invalid"}},
 "projects":{
  h+"/work/aae-orc":{"allowedTools":[],"mcpServers":{
     "director-mcp":{"type":"stdio","command":"/fake/bin","args":[],
        "env":{"DIRECTOR_AGENT_ID":"SENTINEL-agent","DIRECTOR_TEAM":"SENTINEL-team","NATS_URL":"nats://SENTINEL:1"}},
     "other":{"type":"stdio","command":"/fake/other"}}},
  h+"/work/elsewhere":{"mcpServers":{"director-mcp":{"type":"stdio","command":"/fake/keep"}}}}}
json.dump(d,open(p,"w"),indent=2)
PY
}
run_step() { env -i HOME="$home" PATH="$PATH" LANG="${LANG:-C}" CLAUDE_JSON="$scratch" PROJECT_PATH="$home/work/aae-orc" "$step" "$@"; }

mk; cp "$scratch" "$root/claude.json.orig"
if run_step >"$root/o" 2>&1; then bad "no mode is a usage error" "exit 0"; else ok "no mode is a usage error"; fi

if run_step --check >"$root/o" 2>&1; then ok "--check on a file with the entry exits 0"; else bad "--check exits 0" "$(cat "$root/o")"; fi
check "--check prints PASS lines" grep -q '^PASS' "$root/o"
check "--check never prints a value" bash -c "! grep -q SENTINEL '$root/o'"
check "--check changes nothing" cmp -s "$scratch" "$root/claude.json.orig"

if run_step --apply >"$root/o" 2>&1; then ok "--apply exits 0"; else bad "--apply exits 0" "$(cat "$root/o")"; fi
check "--apply never prints a value" bash -c "! grep -q SENTINEL '$root/o'"
bak="$(ls "$scratch".bak-* 2>/dev/null | head -1 || true)"
check "--apply leaves a backup equal to the original" bash -c "[[ -n '$bak' ]] && cmp -s '$bak' '$root/claude.json.orig'"
check "only the project-local entry is gone, everything else is equal" python3 - "$scratch" "$root/claude.json.orig" "$home" <<'PY'
import json,sys
new,old,h=json.load(open(sys.argv[1])),json.load(open(sys.argv[2])),sys.argv[3]
p=h+"/work/aae-orc"
del old["projects"][p]["mcpServers"]["director-mcp"]
assert new==old, "other keys changed"
assert "other" in new["projects"][p]["mcpServers"]
assert "director-mcp" in new["projects"][h+"/work/elsewhere"]["mcpServers"]
PY

if run_step --apply >"$root/o" 2>&1; then ok "--apply on a file without the entry exits 0"; else bad "second --apply exits 0" "$(cat "$root/o")"; fi
check "an absent entry is reported as nothing to remove" grep -qi 'nothing to remove' "$root/o"

printf '{ not json' >"$scratch"; cp "$scratch" "$root/bad.orig"
if run_step --apply >"$root/o" 2>&1; then bad "invalid JSON stops --apply" "exit 0"; else ok "invalid JSON stops --apply"; fi
check "invalid JSON is reported as STOP" grep -q '^STOP' "$root/o"
check "invalid JSON leaves the file untouched" cmp -s "$scratch" "$root/bad.orig"

echo "---"
echo "$pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
