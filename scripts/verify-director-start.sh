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
  { find "$home" -path "$home/.director/state" -prune -o -print | LC_ALL=C sort
    find "$home" -path "$home/.director/state" -prune -o -type f -exec shasum -a 256 {} + | LC_ALL=C sort; } | shasum -a 256
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
if [[ "$before" == "$after" ]]; then ok "the launcher wrote nothing under HOME but its launch record"; else bad "the launcher wrote nothing under HOME but its launch record"; fi

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

# review 5588408515 item 3: a V_ variable in the caller's environment is not identity
rm -f "$root/out" "$home/.director/seat.env"
if env -i HOME="$home" PATH="$PATH" LANG="${LANG:-C}" V_DIRECTOR_AGENT_ID=x V_DIRECTOR_TEAM=x V_DIRECTOR_WORKSPACE=x \
     CLAUDE_BIN="$root/claude-stub" STUB_OUT="$root/out" "$start" start >"$root/stdout" 2>&1; then
  bad "V_ variables in the environment do not supply identity" "exit 0"
else ok "V_ variables in the environment do not supply identity"; fi
check "claude was not run for V_ variables" bash -c "[[ ! -e '$root/out' ]]"

# review item 2: the README's own seat example is a valid seat file
readme_seat="$root/readme-seat.env"
awk '/^```seat-env$/{f=1;next} /^```$/{f=0} f' "$here/README.md" >"$readme_seat"
check "the README has a seat example" test -s "$readme_seat"
rm -f "$root/out"
if run_start start --seat-file "$readme_seat" >"$root/stdout" 2>&1; then ok "the README seat example passes the parser"; else bad "the README seat example passes the parser" "$(cat "$root/stdout")"; fi

# note: the config's command follows DIRECTOR_HOME, as the launcher does
check "the config's command is not tied to \$HOME" bash -c "! grep -q 'HOME}/.director' '$cfg'"
cat >"$home/.director/seat.env" <<SEAT
DIRECTOR_AGENT_ID=fake-director
DIRECTOR_TEAM=fake-team
DIRECTOR_WORKSPACE=fake-ws
SEAT
rm -f "$root/out"
env -i HOME="$home" PATH="$PATH" LANG="${LANG:-C}" DIRECTOR_HOME="$root/dh" CLAUDE_BIN="$root/claude-stub" STUB_OUT="$root/out" "$start" start --seat-file "$home/.director/seat.env" >"$root/stdout" 2>&1 || true
check "the launcher hands the config the seat launcher under DIRECTOR_HOME" bash -c "env -i HOME='$home' PATH='$PATH' DIRECTOR_HOME='$root/dh' CLAUDE_BIN='$root/claude-stub' STUB_OUT='$root/seatenv' '$start' start --seat-file '$home/.director/seat.env' >/dev/null 2>&1; grep -q '^DIRECTOR_MCP_SEAT=$root/dh/bin/director-mcp-seat\$' '$root/seatenv'"

# review item 1: a session the launcher started, whose director MCP server is
# running under it and carries the launch id, can write the proof
mkdir -p "$root/bin"
# a stand-in MCP server, run by whichever bash `env bash` finds
procmatch="$here/scripts/director-procmatch"
cat >"$root/bin/director-mcp-seat" <<'MCP'
#!/usr/bin/env bash
sleep 30; true   # the trailing command keeps this script's own process from being replaced by sleep
MCP
chmod +x "$root/bin/director-mcp-seat"
cp "$root/bin/director-mcp-seat" "$root/bin/director-mcp-evil"
spawn() { # run a stand-in in its own process group; $mcp is its pid, which is also its group
  perl -e 'setpgrp(0,0); exec @ARGV' "$@" & mcp=$!
}
# control: macOS hides the environment of system-protected binaries, and `prove` reads
# the server's environment. If the stand-in's interpreter is one (the system bash 3.2),
# the prove checks cannot run here, and are skipped rather than counted either way.
spawn env DIRECTOR_LAUNCH_VIA_CONFIG=control-id "$root/bin/director-mcp-seat"
sleep 1
if ps eww -o command= -p "$mcp" 2>/dev/null | grep -q "DIRECTOR_LAUNCH_VIA_CONFIG=control-id"; then can_prove=1; else can_prove=0; fi
kill -- "-$mcp" 2>/dev/null || true; wait "$mcp" 2>/dev/null || true
skipped=0
if [[ "$can_prove" == 0 ]]; then echo "SKIP the prove checks on real processes: this interpreter's process environment is hidden"; skipped=10; fi

# the matcher on its own, over argv and environment lists, so the cases a real
# process cannot show (another interpreter, an entry inside another variable) are run
pm() { # id, json; the matcher's exit status
  printf '%s' "$2" | "$procmatch" --id "$1" --stdin
}
pm_yes() { local name="$1"; shift; if pm "$@"; then ok "$name"; else bad "$name"; fi; }
pm_no()  { local name="$1"; shift; if pm "$@"; then bad "$name" "matched"; else ok "$name"; fi; }
pm_yes "matcher: the server binary with the config's variable matches" id1 '{"argv":["/x/director-mcp"],"env":["A=b","DIRECTOR_LAUNCH_VIA_CONFIG=id1"]}'
pm_yes "matcher: the seat script under bash matches" id1 '{"argv":["/bin/bash","/x/director-mcp-seat"],"env":["DIRECTOR_LAUNCH_VIA_CONFIG=id1"]}'
pm_no  "matcher: a longer basename is not the server" id1 '{"argv":["/x/director-mcp-evil"],"env":["DIRECTOR_LAUNCH_VIA_CONFIG=id1"]}'
pm_no  "matcher: ssh is not an interpreter" id1 '{"argv":["ssh","/x/director-mcp-seat"],"env":["DIRECTOR_LAUNCH_VIA_CONFIG=id1"]}'
pm_no  "matcher: an extra argument is refused" id1 '{"argv":["/x/director-mcp","DIRECTOR_LAUNCH_VIA_CONFIG=id1"],"env":[]}'
pm_no  "matcher: two extra arguments are refused" id1 '{"argv":["/x/director-mcp","a","b"],"env":["DIRECTOR_LAUNCH_VIA_CONFIG=id1"]}'
pm_no  "matcher: the inherited variable alone is not the config's" id1 '{"argv":["/x/director-mcp"],"env":["DIRECTOR_LAUNCH_ID=id1"]}'
pm_no  "matcher: a longer id is not ours" id1 '{"argv":["/x/director-mcp"],"env":["DIRECTOR_LAUNCH_VIA_CONFIG=id19"]}'
pm_no  "matcher: trailing text is not ours" id1 '{"argv":["/x/director-mcp"],"env":["DIRECTOR_LAUNCH_VIA_CONFIG=id1 trailing"]}'
pm_no  "matcher: an entry inside another variable is not ours" id1 '{"argv":["/x/director-mcp"],"env":["OTHER=x DIRECTOR_LAUNCH_VIA_CONFIG=id1"]}'
pm_no  "matcher: an id that contains a dot does not match a different id" id.1 '{"argv":["/x/director-mcp"],"env":["DIRECTOR_LAUNCH_VIA_CONFIG=idx1"]}'

cat >"$root/claude-session" <<'STUB'
#!/usr/bin/env bash
# a stand-in session: starts the director MCP server the way claude would (the config's
# env block gives it DIRECTOR_LAUNCH_VIA_CONFIG, expanded from DIRECTOR_LAUNCH_ID), then
# runs `director prove` from inside, as the session's shell tool would
spawn() { perl -e 'setpgrp(0,0); exec @ARGV' "$@" & mcp=$!; }
srv="$STUB_BIN/director-mcp-seat"; id="$DIRECTOR_LAUNCH_ID"
case "${STUB_MCP:-yes}" in
  yes)       spawn env DIRECTOR_LAUNCH_VIA_CONFIG="$id" "$srv" ;;
  inherited) spawn "$srv" ;;
  noid)      spawn env -u DIRECTOR_LAUNCH_ID "$srv" ;;
  other)     spawn env DIRECTOR_LAUNCH_VIA_CONFIG=some-other-launch "$srv" ;;
  argid)     spawn env -u DIRECTOR_LAUNCH_ID "$srv" "DIRECTOR_LAUNCH_VIA_CONFIG=$id" ;;
  prefix)    spawn env DIRECTOR_LAUNCH_VIA_CONFIG="${id}9" "$srv" ;;
  trailing)  spawn env DIRECTOR_LAUNCH_VIA_CONFIG="$id trailing" "$srv" ;;
  contained) spawn env -u DIRECTOR_LAUNCH_ID "OTHER=x DIRECTOR_LAUNCH_VIA_CONFIG=$id" "$srv" ;;
  evil)      spawn env DIRECTOR_LAUNCH_VIA_CONFIG="$id" "$STUB_BIN/director-mcp-evil" ;;
  none)      mcp= ;;
esac
sleep 1
"$STUB_START" prove >"$STUB_PROVE_OUT" 2>&1; echo "rc=$?" >>"$STUB_PROVE_OUT"
if [[ -n "$mcp" ]]; then kill -- "-$mcp" 2>/dev/null || true; fi
STUB
chmod +x "$root/claude-session"
sess() { # STUB_MCP value; runs start with the stand-in session
  rm -f "$root/prove.out" "$home/.director/state/launch-proof"
  env -i HOME="$home" PATH="$PATH" LANG="${LANG:-C}" CLAUDE_BIN="$root/claude-session" STUB_BIN="$root/bin" \
    STUB_START="$start" STUB_PROVE_OUT="$root/prove.out" STUB_MCP="$1" "$start" start >"$root/stdout" 2>&1 || true
}
stops() { bash -c "grep -q '^STOP' '$root/prove.out' && [[ ! -e '$home/.director/state/launch-proof' ]]"; }
rm -rf "$home/.director/state"
run_start start >"$root/stdout" 2>&1 || true
check "start leaves a launch record" bash -c "ls '$home'/.director/state/launches/* >/dev/null 2>&1"
if [[ "$can_prove" == 1 ]]; then
  sess yes
  check "prove inside a launched session with its MCP server writes the proof" bash -c "grep -q 'rc=0' '$root/prove.out' && [[ -s '$home/.director/state/launch-proof' ]]"
  sess none;      check "prove with no MCP server under the session is a STOP" stops
  sess inherited; check "prove with only the inherited launch id (the config's server never started) is a STOP" stops
  sess noid;      check "prove with an MCP server that lacks the launch id is a STOP" stops
  sess other;     check "prove with an MCP server carrying another launch id is a STOP" stops
  sess argid;     check "prove with the launch id passed as an argument is a STOP" stops
  sess prefix;    check "prove with a launch id that only starts with ours is a STOP" stops
  sess trailing;  check "prove with trailing text after the launch id is a STOP" stops
  sess contained; check "prove with the entry inside another variable is a STOP" stops
  sess evil;      check "prove with a server named director-mcp-evil is a STOP" stops
fi
rm -f "$home/.director/state/launch-proof"
if env -i HOME="$home" PATH="$PATH" LANG="${LANG:-C}" "$start" prove >"$root/o" 2>&1; then bad "prove outside a launched session is refused" "exit 0"; else ok "prove outside a launched session is refused"; fi

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
sha() { shasum -a 256 "$1" | cut -d' ' -f1; }
mkproof() { # id, config sha, age in seconds: a launch record and the proof that cites it
  local d="$home/.director/state"; mkdir -p "$d/launches"
  printf 'id=%s\npid=1\nstarted=%s\nconfig_sha256=%s\nsession=director\n' "$1" "$(( $(date +%s) - $3 ))" "$2" >"$d/launches/$1"
  printf 'id=%s\nproved=%s\nmcp_pid=2\nconfig_sha256=%s\n' "$1" "$(( $(date +%s) - $3 ))" "$2" >"$d/launch-proof"
}
rm -rf "$home/.director/state"

mk; cp "$scratch" "$root/claude.json.orig"
if run_step >"$root/o" 2>&1; then bad "no mode is a usage error" "exit 0"; else ok "no mode is a usage error"; fi

# review item 1: --apply needs proof that a launcher-started session had its MCP server up
for mode in --apply --check; do
  if run_step "$mode" >"$root/o" 2>&1; then bad "$mode without a launcher proof is refused" "exit 0"; else ok "$mode without a launcher proof is refused"; fi
  check "$mode without proof says so" grep -q '^STOP.*proof' "$root/o"
done
check "a refused --apply changes nothing and leaves no backup" bash -c "cmp -s '$scratch' '$root/claude.json.orig' && ! ls '$scratch'.bak-* >/dev/null 2>&1"
mkproof old-config "0000000000000000000000000000000000000000000000000000000000000000" 60
if run_step --apply >"$root/o" 2>&1; then bad "a proof for a different config is refused" "exit 0"; else ok "a proof for a different config is refused"; fi
mkproof stale "$(sha "$cfg")" 864000
if run_step --apply >"$root/o" 2>&1; then bad "a proof older than 7 days is refused" "exit 0"; else ok "a proof older than 7 days is refused"; fi
mkproof fresh "$(sha "$cfg")" 60
rm "$home/.director/state/launches/fresh"
if run_step --apply >"$root/o" 2>&1; then bad "a proof with no launch record is refused" "exit 0"; else ok "a proof with no launch record is refused"; fi
# a proof id that is a path to a hand-written file is not a launch record
printf 'id=x\npid=1\nstarted=1\nconfig_sha256=%s\nsession=d\n' "$(sha "$cfg")" >"$root/forged-record"
printf 'id=%s\nproved=%s\nmcp_pid=2\nconfig_sha256=%s\n' "$root/forged-record" "$(date +%s)" "$(sha "$cfg")" >"$home/.director/state/launch-proof"
if run_step --apply >"$root/o" 2>&1; then bad "a proof id that is a path is refused" "exit 0"; else ok "a proof id that is a path is refused"; fi
printf 'id=%s\nproved=%s\nmcp_pid=2\nconfig_sha256=%s\n' "../launches/../../../forged" "$(date +%s)" "$(sha "$cfg")" >"$home/.director/state/launch-proof"
if run_step --apply >"$root/o" 2>&1; then bad "a proof id with .. is refused" "exit 0"; else ok "a proof id with .. is refused"; fi
mkproof fresh "$(sha "$cfg")" 60
check "none of the refusals changed the file" cmp -s "$scratch" "$root/claude.json.orig"

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

python3 - "$scratch" "$home" <<'PY'
import json,sys
p,h=sys.argv[1:3]
json.dump({"projects":{h+"/work/aae-orc":{"mcpServers":{"director-mcp":"SENTINELSTRING"}}}},open(p,"w"))
PY
if run_step --check >"$root/o" 2>&1; then bad "a string entry is a STOP" "exit 0"; else ok "a string entry is a STOP"; fi
check "a string entry's letters are not printed" bash -c "! grep -qE 'keys: .?[A-Z], ' '$root/o' && grep -q '^STOP' '$root/o'"
python3 - "$scratch" "$home" <<'PY'
import json,sys
p,h=sys.argv[1:3]
json.dump({"projects":{h+"/work/aae-orc":{"mcpServers":{"director-mcp":{"type":"stdio","env":["SENTINELITEM"]}}}}},open(p,"w"))
PY
if run_step --check >"$root/o" 2>&1; then bad "a list env is a STOP" "exit 0"; else ok "a list env is a STOP"; fi
check "a list env's items are not printed" bash -c "! grep -q SENTINEL '$root/o'"

printf '{ not json' >"$scratch"; cp "$scratch" "$root/bad.orig"
if run_step --apply >"$root/o" 2>&1; then bad "invalid JSON stops --apply" "exit 0"; else ok "invalid JSON stops --apply"; fi
check "invalid JSON is reported as STOP" grep -q '^STOP' "$root/o"
check "invalid JSON leaves the file untouched" cmp -s "$scratch" "$root/bad.orig"

echo "---"
echo "$pass passed, $fail failed, $skipped skipped"
[[ "$fail" -eq 0 ]]
