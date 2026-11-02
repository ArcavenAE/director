#!/usr/bin/env bash
# Prove director-session, the installed launcher for a hand-started director
# session (ask I-91; the ID-A launcher promoted out of probe/).
#
# Broker-free: claude and the shim are stubs, nothing connects. The stub claude
# records its argv, one word per line, and the directory it ran from.
#
# Covered:
#  - own-config mode (--own-config ID): the launcher's own strict MCP config, a
#    distinct agent id per launch, the id class refused when wrong, a missing
#    shim named before claude starts;
#  - the orc-root refusal, now opt-in with --require-orc-root (aae-orc-skw0b):
#    refuses every other directory, and without the flag a plain directory runs;
#  - default mode: claude runs on the user's own config, and the channel flag's
#    server name is looked up, keys only, from the top-level mcpServers and the
#    current directory's project mcpServers in the user's Claude config;
#    none or several director-shaped names stop the launch and name what was
#    found; no value of any server entry reaches the output;
#  - the channel flag: absent unless asked, exactly one when asked, announced,
#    one prompt notice, never answered; --receiver refuses with no terminal.
#
# Usage: skills/director/scripts/verify-director-session.sh   Exit nonzero on any miss.
#        DIRECTOR_SESSION=<path> to test another copy.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SESSION="${DIRECTOR_SESSION:-$here/director-session}"
[[ -x "$SESSION" ]] || { echo "director-session not executable at $SESSION"; exit 2; }
command -v jq >/dev/null 2>&1 || { echo "jq not on PATH"; exit 2; }
command -v python3 >/dev/null 2>&1 || { echo "python3 not on PATH (the terminal case needs a pty)"; exit 2; }

work="$(cd "$(mktemp -d "${TMPDIR:-/tmp}/director-session-verify.XXXXXX")" && pwd -P)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin"
marker="$work/claude-ran"
argvf="$work/claude-argv"
# A stub harness records its argv, one word per line, and where it ran. It is
# written with printf, not a here-document, which bash 3.2 puts in a file under /tmp.
mkstub() {
  printf '%s\n' '#!/usr/bin/env bash' "pwd -P >\"$marker\"" "printf '%s\\n' \"\$@\" >\"$argvf\"" >"$work/bin/$1"
}
mkstub claude
chmod +x "$work/bin/claude"
printf '#!/bin/sh\n' >"$work/shim"
chmod +x "$work/shim"

fail=0
miss() { echo "MISS: $*"; fail=1; }

# run <dir> [env...] -- [launcher args...]: launch from dir with stdin from
# /dev/null; sets rc, out (stdout and stderr), ran (marker contents or ""), and
# argv (claude's words, one per line, or "").
run() {
  local dir="$1"; shift
  local envs=()
  while [[ $# -gt 0 && "$1" != "--" ]]; do envs+=("$1"); shift; done
  shift
  rm -f "$marker" "$argvf"
  set +e
  out="$(cd "$dir" && env ${envs[@]+"${envs[@]}"} PATH="$work/bin:$PATH" DIRECTOR_SHIM_BIN="$work/shim" "$SESSION" "$@" 2>&1 </dev/null)"
  rc=$?
  set -e
  ran=""; argv=""
  if [[ -f "$marker" ]]; then ran="$(<"$marker")"; fi
  if [[ -f "$argvf" ]]; then argv="$(<"$argvf")"; fi
}

# runtty: the same, with a pty as stdin and stdout, so the terminal check sees
# one. TTYMODE=nostdin or nostdout points one of the two at /dev/null inside the
# pty, so each half of the check is pinned on its own. The pty is driven by
# pty.fork and a read loop that ends on EOF or EIO: pty.spawn blocks forever on
# macOS's stock python3 3.9, where the master returns b"" at EOF instead of
# raising EIO. The same loop runs on Linux.
PTY_PY='import os, pty, sys
pid, fd = pty.fork()
if pid == 0:
    m = os.environ.get("TTYMODE", "both")
    if m == "nostdin":
        os.dup2(os.open("/dev/null", os.O_RDONLY), 0)
    if m == "nostdout":
        os.dup2(os.open("/dev/null", os.O_WRONLY), 1)
    os.execv(sys.argv[1], sys.argv[1:])
while True:
    try:
        d = os.read(fd, 4096)
    except OSError:
        break
    if not d:
        break
    sys.stdout.buffer.write(d)
    sys.stdout.buffer.flush()
_, st = os.waitpid(pid, 0)
sys.exit(os.WEXITSTATUS(st) if os.WIFEXITED(st) else 128 + os.WTERMSIG(st))'
runtty() {
  local dir="$1"; shift
  local envs=()
  while [[ $# -gt 0 && "$1" != "--" ]]; do envs+=("$1"); shift; done
  shift
  rm -f "$marker" "$argvf"
  set +e
  out="$(cd "$dir" && env ${envs[@]+"${envs[@]}"} PATH="$work/bin:$PATH" DIRECTOR_SHIM_BIN="$work/shim" \
    python3 -c "$PTY_PY" "$SESSION" "$@" 2>&1)"
  rc=$?
  set -e
  ran=""; argv=""
  if [[ -f "$marker" ]]; then ran="$(<"$marker")"; fi
  if [[ -f "$argvf" ]]; then argv="$(<"$argvf")"; fi
}

count() { printf '%s\n' "$argv" | grep -c -x -- "$1" || true; }

orc="$work/orc"
mkdir -p "$orc/sub/deeper" "$work/plain" "$work/claudeonly" "$work/reposonly"
: >"$orc/CLAUDE.md"; : >"$orc/repos.yaml"
: >"$work/claudeonly/CLAUDE.md"
: >"$work/reposonly/repos.yaml"

# ---- own-config mode: the probe launcher's behavior ----

# Positive control: the orc root launches, with the launcher's own strict config.
run "$orc" X=1 -- --own-config seat-a --require-orc-root
[[ $rc -eq 0 && -n "$ran" ]] || miss "own-config at the orc root: rc=$rc ran='$ran' out=$out"
[[ "$(count --strict-mcp-config)" == 1 ]] || miss "own-config: want exactly one --strict-mcp-config: $argv"
[[ "$argv" == *'"DIRECTOR_AGENT_ID":"seat-a"'* ]] || miss "own-config: the id is not in the MCP config: $argv"

# Two launches get two distinct ids.
run "$orc" X=1 -- --own-config seat-b --require-orc-root
[[ "$argv" == *'"DIRECTOR_AGENT_ID":"seat-b"'* && "$argv" != *'"DIRECTOR_AGENT_ID":"seat-a"'* ]] || miss "own-config: second launch does not carry its own id: $argv"

# The id class is refused, not rewritten (R-76), and claude does not start.
run "$orc" X=1 -- --own-config 'bad id' --require-orc-root
[[ $rc -ne 0 && -z "$ran" && "$out" == *"R-76"* ]] || miss "own-config: bad id rc=$rc ran='$ran' out=$out"

# Arguments after the id pass through to claude.
run "$orc" X=1 -- --own-config seat-a --require-orc-root --resume seat-a
[[ "$(count --resume)" == 1 && "$(count seat-a)" -ge 1 ]] || miss "own-config: --resume seat-a not passed through: $argv"

# A missing shim is named, and the harness never starts.
rc=0; rm -f "$marker"
out="$(cd "$orc" && env X=1 PATH="$work/bin:$PATH" DIRECTOR_SHIM_BIN="$work/nope" "$SESSION" --own-config seat-a 2>&1 </dev/null)" || rc=$?
[[ $rc -ne 0 && ! -f "$marker" && "$out" == *"shim binary not found"* ]] || miss "own-config: missing shim rc=$rc out=$out"

# ---- the orc-root refusal, opt-in ----

# A subdirectory of the orc is refused: a seat started there does not load the
# orc's skills or settings and is limited to the subtree. The refusal names the
# root to cd to.
for sub in "$orc/sub" "$orc/sub/deeper"; do
  run "$sub" X=1 -- --own-config seat-a --require-orc-root
  [[ $rc -ne 0 && -z "$ran" ]] || miss "orc subdirectory $sub: rc=$rc ran='$ran'"
  [[ "$out" == *"$orc"* ]] || miss "orc subdirectory $sub: refusal does not name the root $orc: $out"
done

# Refusals: no marker, one marker, the other marker.
for d in plain claudeonly reposonly; do
  run "$work/$d" X=1 -- --own-config seat-a --require-orc-root
  [[ $rc -ne 0 ]] || miss "$d: rc=0, want a refusal"
  [[ -z "$ran" ]] || miss "$d: claude ran from $ran"
  [[ "$out" == *"CLAUDE.md"* && "$out" == *"repos.yaml"* ]] || miss "$d: refusal does not name what is required: $out"
  [[ "$out" == *"refusing"* ]] || miss "$d: refusal does not say it refused: $out"
done

# A symlink into a subdirectory of the orc: the logical path has no root above
# it, the physical one does. The refusal names the root.
ln -s "$orc/sub" "$work/linksub"
run "$work/linksub" X=1 -- --own-config seat-a --require-orc-root
[[ $rc -ne 0 && -z "$ran" ]] || miss "symlink to a subdirectory: rc=$rc ran='$ran'"
[[ "$out" == *"$orc"* ]] || miss "symlink to a subdirectory: refusal does not name the root $orc: $out"

# A symlink to the orc root itself holds both markers through the link, so it is
# a launch from the root, as for aq and ax.
ln -s "$orc" "$work/linkroot"
run "$work/linkroot" X=1 -- --own-config seat-a --require-orc-root
[[ $rc -eq 0 && -n "$ran" ]] || miss "symlink to the root: rc=$rc ran='$ran' out=$out"

# ORC_ROOT does not rescue a wrong cwd: the seat's context comes from cwd.
run "$work/plain" ORC_ROOT="$orc" -- --own-config seat-a --require-orc-root
[[ $rc -ne 0 && -z "$ran" ]] || miss "ORC_ROOT set, wrong cwd: rc=$rc ran='$ran'"

# Without the flag the check is off, so a plain directory launches (a newcomer's
# checkout holds neither marker).
run "$work/plain" X=1 -- --own-config seat-a
[[ $rc -eq 0 && -n "$ran" ]] || miss "no --require-orc-root, plain directory: rc=$rc ran='$ran' out=$out"

# ---- default mode: the user's own config, the server name looked up ----

mkcfg() { printf '%s\n' "$1" >"$work/claude.json"; }
SECRET="tok-SECRET-4242"
cfgenv=(DIRECTOR_CLAUDE_JSON="$work/claude.json")

# Top-level server, named director-mcp. Its env holds a fixture secret that must
# never reach the output.
mkcfg '{"mcpServers":{"director-mcp":{"command":"x","env":{"DIRECTOR_AGENT_ID":"op","TOKEN":"'$SECRET'"}},"other":{"command":"y"}}}'
run "$work/plain" "${cfgenv[@]}" -- --channel
[[ $rc -eq 0 && -n "$ran" ]] || miss "lookup, top-level: rc=$rc out=$out"
[[ "$(count --dangerously-load-development-channels)" == 1 && "$(count server:director-mcp)" == 1 ]] || miss "lookup, top-level: want one channel flag naming director-mcp: $argv"
[[ "$out" != *"$SECRET"* && "$argv" != *"$SECRET"* ]] || miss "lookup: a server entry's value reached the output"
[[ "$(count --strict-mcp-config)" == 0 && "$(count --mcp-config)" == 0 ]] || miss "default mode must run on the user's config, not its own: $argv"

# Project-scoped: the server lives under projects["<cwd>"].mcpServers, not at the top.
pp="$(cd "$work/plain" && pwd -P)"
mkcfg '{"mcpServers":{},"projects":{"'"$pp"'":{"mcpServers":{"director":{"command":"x","env":{"TOKEN":"'$SECRET'"}}}},"/elsewhere":{"mcpServers":{"director-other":{}}}}}'
run "$work/plain" "${cfgenv[@]}" -- --channel
[[ "$(count server:director)" == 1 ]] || miss "lookup, project-scoped: want server:director: $argv out=$out"
[[ "$out" != *"$SECRET"* ]] || miss "lookup, project-scoped: a value reached the output"

# A project entry for a different directory is not read.
mkcfg '{"projects":{"/elsewhere":{"mcpServers":{"director":{}}}}}'
run "$work/plain" "${cfgenv[@]}" -- --channel
[[ $rc -ne 0 && -z "$ran" && "$out" == *"no director"* ]] || miss "lookup, other project only: rc=$rc ran='$ran' out=$out"

# None found: stop, say so, claude never starts.
mkcfg '{"mcpServers":{"other":{"command":"y"}}}'
run "$work/plain" "${cfgenv[@]}" -- --channel
[[ $rc -ne 0 && -z "$ran" && "$out" == *"no director"* ]] || miss "lookup, none: rc=$rc ran='$ran' out=$out"

# More than one: stop and name each, never guess.
mkcfg '{"mcpServers":{"director":{},"director-mcp":{}}}'
run "$work/plain" "${cfgenv[@]}" -- --channel
[[ $rc -ne 0 && -z "$ran" && "$out" == *"director-mcp"* && "$out" == *"director"* && "$out" == *"more than one"* ]] || miss "lookup, two: rc=$rc ran='$ran' out=$out"

# The same name at both scopes is one server, not two.
mkcfg '{"mcpServers":{"director-mcp":{}},"projects":{"'"$pp"'":{"mcpServers":{"director-mcp":{}}}}}'
run "$work/plain" "${cfgenv[@]}" -- --channel
[[ $rc -eq 0 && "$(count server:director-mcp)" == 1 ]] || miss "lookup, same name at both scopes: rc=$rc argv=$argv out=$out"

# A value that is not an object makes jq quote part of it in its own error; that
# text must never reach the output, whatever the config holds.
SHORT="s3cr3t-9"   # jq quotes at most 10 characters of the value, so the fixture must fit
mkcfg '{"mcpServers":"'$SHORT'"}'
run "$work/plain" "${cfgenv[@]}" -- --channel
[[ $rc -ne 0 && -z "$ran" && "$out" != *"$SHORT"* ]] || miss "lookup, mcpServers is not an object: rc=$rc ran='$ran' out=$out"

# No config file at all: named, claude never starts.
run "$work/plain" DIRECTOR_CLAUDE_JSON="$work/absent.json" -- --channel
[[ $rc -ne 0 && -z "$ran" && "$out" == *"absent.json"* ]] || miss "lookup, no config file: rc=$rc ran='$ran' out=$out"

# ---- the channel flag ----

mkcfg '{"mcpServers":{"director-mcp":{}}}'

# Absent unless asked: no flag in the argv, and no lookup is needed to start.
run "$work/plain" "${cfgenv[@]}" -- --resume director
[[ $rc -eq 0 && "$(count --dangerously-load-development-channels)" == 0 && "$(count --resume)" == 1 ]] || miss "no channel flag requested: rc=$rc argv=$argv"
run "$work/plain" DIRECTOR_CLAUDE_JSON="$work/absent.json" --
[[ $rc -eq 0 && -n "$ran" ]] || miss "no channel flag requested and no config: the launch must not need a lookup: rc=$rc out=$out"

# Asked explicitly: exactly one, announced, with the one-prompt notice, and the
# launcher says it does not answer it.
run "$work/plain" "${cfgenv[@]}" -- --channel
[[ "$out" == *"channel flag ON"* && "$out" == *"one"* && "$out" == *"prompt"* && "$out" == *"does not answer"* ]] || miss "--channel: not announced with the prompt notice: $out"

# --receiver with no terminal is refused, and claude never starts.
run "$work/plain" "${cfgenv[@]}" -- --receiver
[[ $rc -ne 0 && -z "$ran" && "$out" == *"terminal"* ]] || miss "--receiver, no terminal: rc=$rc ran='$ran' out=$out"

# Each half of the terminal check is pinned on its own: a terminal on stdout but
# not stdin is refused, and so is one on stdin but not stdout.
runtty "$work/plain" "${cfgenv[@]}" TTYMODE=nostdin -- --receiver
[[ $rc -ne 0 && -z "$ran" ]] || miss "--receiver, no stdin terminal: rc=$rc ran='$ran' out=$out"
runtty "$work/plain" "${cfgenv[@]}" TTYMODE=nostdout -- --receiver
[[ $rc -ne 0 && -z "$ran" ]] || miss "--receiver, no stdout terminal: rc=$rc ran='$ran' out=$out"

# --receiver with a terminal turns the flag on, announces it, and starts.
runtty "$work/plain" "${cfgenv[@]}" -- --receiver
[[ $rc -eq 0 && -n "$ran" ]] || miss "--receiver, terminal: rc=$rc out=$out"
[[ "$(count --dangerously-load-development-channels)" == 1 && "$(count server:director-mcp)" == 1 ]] || miss "--receiver, terminal: want one channel flag: $argv"
[[ "$out" == *"channel flag ON"* && "$out" == *"does not answer"* ]] || miss "--receiver, terminal: not announced: $out"

# In own-config mode the server is the launcher's own, named director, and the
# shim gets DIRECTOR_CUE=1 so the cue fires.
runtty "$work/plain" X=1 -- --own-config seat-a --receiver
[[ "$(count server:director)" == 1 && "$argv" == *'"DIRECTOR_CUE":"1"'* ]] || miss "own-config receiver: want server:director and DIRECTOR_CUE=1: $argv"

# The launcher never answers the prompt: no keystroke tool in its source.
if grep -n -E 'send-keys|expect |xdotool|osascript|yes \|' "$SESSION" >/dev/null; then miss "the launcher source names a way to answer a prompt"; fi

# ---- marvel-role mode (director#461): a marvel role's own command, no role library ----

# A stub codex records its argv the same way claude does.
mkstub codex
chmod +x "$work/bin/codex"
# The caller's own marvel and director env must not leak into these cases.
unset_seat=(-u DIRECTOR_AGENT_ID -u DIRECTOR_TEAM -u DIRECTOR_WORKSPACE -u MARVEL_SESSION -u MARVEL_TEAM
  -u MARVEL_WORKSPACE -u DIRECTOR_CUE -u MARVEL_FIRST_PROMPT -u DIRECTOR_CLUSTER -u NATS_URL
  -u DIRECTOR_NATS_USER -u DIRECTOR_NATS_PASS)
seat_a=(DIRECTOR_AGENT_ID=demo-supervisor-g1-0 DIRECTOR_TEAM=demo DIRECTOR_WORKSPACE=first)

# Positive control: the seat starts under the shim named in its own env, with a
# strict config of the launcher's own and no channel flag.
run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" -- --marvel-role
[[ $rc -eq 0 && -n "$ran" ]] || miss "marvel-role: rc=$rc out=$out"
[[ "$(count --strict-mcp-config)" == 1 && "$argv" == *'"DIRECTOR_AGENT_ID":"demo-supervisor-g1-0"'* ]] || miss "marvel-role: id not in the config: $argv"
[[ "$argv" == *'"DIRECTOR_TEAM":"demo"'* && "$argv" == *'"DIRECTOR_WORKSPACE":"first"'* ]] || miss "marvel-role: team or workspace not in the config: $argv"
[[ "$(count --dangerously-load-development-channels)" == 0 && "$argv" != *DIRECTOR_CUE* ]] || miss "marvel-role: a cue with no request: $argv"

# Two seats from one manifest get two ids.
run "$work/plain" "${unset_seat[@]}" DIRECTOR_AGENT_ID=demo-worker-g1-0 DIRECTOR_TEAM=demo DIRECTOR_WORKSPACE=first -- --marvel-role
[[ "$argv" == *'"DIRECTOR_AGENT_ID":"demo-worker-g1-0"'* && "$argv" != *demo-supervisor* ]] || miss "marvel-role: the second seat does not carry its own id: $argv"

# marvel's own names stand in for the director ones.
run "$work/plain" "${unset_seat[@]}" MARVEL_SESSION=demo-worker-g1-1 MARVEL_TEAM=demo MARVEL_WORKSPACE=first -- --marvel-role
[[ $rc -eq 0 && "$argv" == *'"DIRECTOR_AGENT_ID":"demo-worker-g1-1"'* ]] || miss "marvel-role: MARVEL_SESSION not used: rc=$rc $argv"

# The role's own prompt is the first message, and the harness args pass through.
run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" "MARVEL_FIRST_PROMPT=You answer on the bus." -- --marvel-role --model sonnet
last="$(printf '%s\n' "$argv" | tail -1)"
[[ "$last" == "You answer on the bus." && "$(count --model)" == 1 ]] || miss "marvel-role: brief or passthrough wrong: $argv"
run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" -- --marvel-role
[[ "$(printf '%s\n' "$argv" | tail -1)" != *"answer"* ]] || miss "marvel-role: a brief with no prompt set: $argv"

# No cue unless asked: --channel and DIRECTOR_CUE=1 each give exactly one flag,
# the shim gets DIRECTOR_CUE=1, it is announced, and the prompt is not answered.
for ask in "--channel" "env"; do
  if [[ "$ask" == env ]]; then
    run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" DIRECTOR_CUE=1 -- --marvel-role
  else
    run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" -- --marvel-role --channel
  fi
  [[ "$(count --dangerously-load-development-channels)" == 1 && "$(count server:director)" == 1 && "$argv" == *'"DIRECTOR_CUE":"1"'* ]] || miss "marvel-role cue ($ask): want one flag and DIRECTOR_CUE=1: $argv"
  [[ "$out" == *"channel flag ON"* && "$out" == *"does not answer"* ]] || miss "marvel-role cue ($ask): not announced: $out"
done

# --receiver with a terminal carries the cue once and names the prompt; with no
# terminal it is one line and the harness never starts.
runtty "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" -- --marvel-role --receiver
[[ $rc -eq 0 && "$(count --dangerously-load-development-channels)" == 1 && "$out" == *"channel flag ON"* && "$out" == *"prompt"* ]] || miss "marvel-role receiver, terminal: rc=$rc $out"
run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" -- --marvel-role --receiver
[[ $rc -ne 0 && -z "$ran" && "$(printf '%s\n' "$out" | wc -l | tr -d ' ')" == 1 && "$out" == *"terminal"* ]] || miss "marvel-role receiver, no terminal: rc=$rc ran='$ran' out=$out"

# A missing shim is one line naming the install command, and the harness never starts.
rc=0; rm -f "$marker"
out="$(cd "$work/plain" && env "${unset_seat[@]}" "${seat_a[@]}" PATH="$work/bin:$PATH" DIRECTOR_SHIM_BIN="$work/nope" "$SESSION" --marvel-role 2>&1 </dev/null)" || rc=$?
[[ $rc -ne 0 && ! -f "$marker" && "$(printf '%s\n' "$out" | wc -l | tr -d ' ')" == 1 && "$out" == *"build-director-mcp"* ]] || miss "marvel-role: missing shim rc=$rc out=$out"

# A missing identity names the variable; a wrong class is refused (R-76); neither starts the harness.
run "$work/plain" "${unset_seat[@]}" DIRECTOR_TEAM=demo DIRECTOR_WORKSPACE=first -- --marvel-role
[[ $rc -ne 0 && -z "$ran" && "$out" == *"DIRECTOR_AGENT_ID"* ]] || miss "marvel-role: no id: rc=$rc out=$out"
run "$work/plain" "${unset_seat[@]}" 'DIRECTOR_AGENT_ID=bad id' DIRECTOR_TEAM=demo DIRECTOR_WORKSPACE=first -- --marvel-role
[[ $rc -ne 0 && -z "$ran" && "$out" == *"R-76"* ]] || miss "marvel-role: bad id: rc=$rc out=$out"

# The bus password never reaches the command line.
run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" DIRECTOR_NATS_PASS=hunter2-sentinel DIRECTOR_NATS_USER=u-sentinel -- --marvel-role
[[ $rc -eq 0 && "$argv" != *hunter2-sentinel* && "$argv" != *u-sentinel* ]] || miss "marvel-role: a bus credential reached argv: $argv"

# A NATS_URL with user:password@ is stripped before it reaches argv, the output or
# any written file, in all three launch forms. A fixture URL, never a real one;
# a password holding an @ is stripped through its last @.
for url in 'nats://u:p@127.0.0.1:1' 'nats://u:p@ss@127.0.0.1:1'; do
  for form in claude codex own; do
    case "$form" in
      claude) run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" "NATS_URL=$url" -- --marvel-role;;
      codex)  run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" "NATS_URL=$url" -- --marvel-role --harness codex;;
      own)    run "$work/plain" "${unset_seat[@]}" "NATS_URL=$url" -- --own-config seat-a;;
    esac
    [[ $rc -eq 0 && "$argv" == *127.0.0.1:1* ]] || miss "NATS_URL userinfo ($form, $url): the host did not reach the config: rc=$rc $argv"
    if [[ "$argv" == *"u:p"* || "$argv" == *"p@"* || "$out" == *"u:p"* || "$out" == *"p@"* || "$argv" == *"ss@"* ]]; then
      miss "NATS_URL userinfo ($form, $url): the credential reached argv or the output: $argv / $out"
    fi
    if grep -r -l -E 'u:p|p@' "$work" >/dev/null 2>&1; then miss "NATS_URL userinfo ($form, $url): the credential reached a file under the scratch dir"; fi
  done
done
run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" NATS_URL=nats://u:p@127.0.0.1:1 -- --marvel-role
[[ "$out" == *"left out of the shim"* ]] || miss "NATS_URL userinfo: no one-line notice: $out"
[[ "$out" == *DIRECTOR_NATS_USER* && "$out" == *DIRECTOR_NATS_PASS* ]] || miss "NATS_URL userinfo: the notice does not name where credentials go: $out"

# A server list is cleaned entry by entry: order and length kept, no server dropped,
# no entry's userinfo left (fixture hosts, never connected).
list_case() { # <input> <expected list in the config>
  local url="$1" want="$2" form
  for form in claude codex own; do
    case "$form" in
      claude) run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" "NATS_URL=$url" -- --marvel-role;;
      codex)  run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" "NATS_URL=$url" -- --marvel-role --harness codex;;
      own)    run "$work/plain" "${unset_seat[@]}" "NATS_URL=$url" -- --own-config seat-a;;
    esac
    if [[ $rc -ne 0 || "$argv" != *"\"$want\""* ]]; then miss "NATS_URL list ($form, $url): want \"$want\" in the config: rc=$rc $argv"; fi
    if [[ "$argv" == *"u:p"* || "$out" == *"u:p"* || "$argv" == *"p@"* ]]; then miss "NATS_URL list ($form, $url): userinfo reached argv or the output: $argv / $out"; fi
  done
}
list_case 'nats://u:p@127.0.0.1:1,nats://u:p@127.0.0.2:1' 'nats://127.0.0.1:1,nats://127.0.0.2:1'
list_case 'nats://127.0.0.1:1,nats://u:p@127.0.0.2:1' 'nats://127.0.0.1:1,nats://127.0.0.2:1'
list_case 'u:p@127.0.0.1:1,u:p@127.0.0.2:1' '127.0.0.1:1,127.0.0.2:1'
# The cut stays inside the authority: an @ in a path or query is not userinfo, so the
# host the user named is the host in the config. A leading space on an entry and
# its scheme (nats:// or tls://) are kept.
url_case() { # <input> <expected value in the config> <notice: yes|no>
  local url="$1" want="$2" notice="$3" form
  for form in claude codex own; do
    case "$form" in
      claude) run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" "NATS_URL=$url" -- --marvel-role;;
      codex)  run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" "NATS_URL=$url" -- --marvel-role --harness codex;;
      own)    run "$work/plain" "${unset_seat[@]}" "NATS_URL=$url" -- --own-config seat-a;;
    esac
    if [[ $rc -ne 0 || "$argv" != *"\"$want\""* ]]; then miss "NATS_URL ($form, '$url'): want \"$want\" in the config: rc=$rc $argv"; fi
    if [[ "$argv" == *"u:p"* || "$out" == *"u:p"* ]]; then miss "NATS_URL ($form, '$url'): userinfo reached argv or the output: $argv / $out"; fi
    if [[ "$notice" == yes && "$out" != *"left out of the shim"* ]]; then miss "NATS_URL ($form, '$url'): no strip notice: $out"; fi
    if [[ "$notice" == no && "$out" == *"left out of the shim"* ]]; then miss "NATS_URL ($form, '$url'): a false strip notice: $out"; fi
  done
}
url_case 'nats://127.0.0.1:1/p@th' 'nats://127.0.0.1:1/p@th' no
url_case 'nats://u:p@127.0.0.1:1/p@th?q=a@b' 'nats://127.0.0.1:1/p@th?q=a@b' yes
url_case 'nats://127.0.0.1:1, nats://u:p@127.0.0.2:1' 'nats://127.0.0.1:1, nats://127.0.0.2:1' yes
url_case ', nats://u:p@127.0.0.2:1' ', nats://127.0.0.2:1' yes
url_case 'tls://u:p@127.0.0.1:1, tls://u:p@127.0.0.2:1' 'tls://127.0.0.1:1, tls://127.0.0.2:1' yes

# The fragment ends the authority too: an @ after # is not userinfo.
url_case 'nats://127.0.0.1:1#fr@g' 'nats://127.0.0.1:1#fr@g' no
url_case 'nats://u:p@127.0.0.1:1#fr@g' 'nats://127.0.0.1:1#fr@g' yes

# A raw / ? or # in a password ends the authority early and leaves part of the
# credential there with no @ to cut at. The launcher refuses, in one line that
# shows neither the URL nor the credential, before the harness starts, in all
# three launch forms (fixture credentials).
for pw in 'u-s:pw/s' 'u-s:pw?s' 'u-s:pw#s'; do
  for form in claude codex own; do
    case "$form" in
      claude) run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" "NATS_URL=nats://$pw@127.0.0.1:1" -- --marvel-role;;
      codex)  run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" "NATS_URL=nats://$pw@127.0.0.1:1" -- --marvel-role --harness codex;;
      own)    run "$work/plain" "${unset_seat[@]}" "NATS_URL=nats://$pw@127.0.0.1:1" -- --own-config seat-a;;
    esac
    if [[ $rc -eq 0 || -n "$ran" || "$(printf '%s\n' "$out" | wc -l | tr -d ' ')" != 1 || "$out" != *"percent-encode"* ]]; then
      miss "NATS_URL password form '$pw' ($form): want one refusal line before the harness: rc=$rc ran='$ran' out=$out"
    fi
    if [[ "$out" == *"u-s"* || "$out" == *"pw"* || "$out" == *"127.0.0.1"* ]]; then miss "NATS_URL password form '$pw' ($form): the refusal shows the URL or the credential: $out"; fi
  done
done
# A list refuses on any one bad entry, and a good list still starts.
run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" 'NATS_URL=nats://127.0.0.1:1,nats://u-s:pw/s@127.0.0.2:1' -- --marvel-role
[[ $rc -ne 0 && -z "$ran" && "$out" != *"u-s"* ]] || miss "NATS_URL list with one bad entry: rc=$rc ran='$ran' out=$out"

# The URL and its password never pass through a shell temp file: the helper uses no
# here-string or here-document (bash 3.2 writes both to a file under /tmp).
if sed -n '/^bus_url_plain()/,/^}/p' "$SESSION" | grep -q '<<'; then miss "bus_url_plain uses a here-string or here-document"; fi

run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" NATS_URL=nats://127.0.0.1:1,nats://127.0.0.2:1 -- --marvel-role
[[ "$out" != *"left out of the shim"* ]] || miss "a clean server list drew a strip notice: $out"

# codex with DIRECTOR_CUE=1 in the env still starts, and says the cue does not apply.
run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" DIRECTOR_CUE=1 -- --marvel-role --harness codex
[[ $rc -eq 0 && -n "$ran" && "$out" == *"DIRECTOR_CUE=1"*"no effect"* && "$argv" != *DIRECTOR_CUE* ]] || miss "marvel-role codex with DIRECTOR_CUE=1: rc=$rc out=$out argv=$argv"
run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" NATS_URL=nats://127.0.0.1:1 -- --marvel-role
[[ "$out" != *"left out of the shim"* && "$argv" == *'"NATS_URL":"nats://127.0.0.1:1"'* ]] || miss "NATS_URL without userinfo changed: $out $argv"

# Flag combinations that make no sense are one-line refusals.
run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" -- --marvel-role --own-config x
[[ $rc -ne 0 && -z "$ran" ]] || miss "marvel-role with --own-config: rc=$rc"
run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" -- --harness codex
[[ $rc -ne 0 && -z "$ran" ]] || miss "--harness without --marvel-role: rc=$rc"
run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" -- --marvel-role --harness nope
[[ $rc -ne 0 && -z "$ran" ]] || miss "--harness nope: rc=$rc"

# codex: the shim and its env go in as -c overrides, the prompt is last, and a
# channel request is refused because codex has none.
run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" "MARVEL_FIRST_PROMPT=You answer on the bus." -- --marvel-role --harness codex
[[ $rc -eq 0 && "$argv" == *"mcp_servers.director.command=\"$work/shim\""* && "$argv" == *'mcp_servers.director.env={'* ]] || miss "marvel-role codex: config not passed: rc=$rc $argv"
[[ "$argv" == *'DIRECTOR_AGENT_ID="demo-supervisor-g1-0"'* && "$(printf '%s\n' "$argv" | tail -1)" == "You answer on the bus." ]] || miss "marvel-role codex: id or brief missing: $argv"
run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" -- --marvel-role --harness codex --channel
[[ $rc -ne 0 && -z "$ran" && "$out" == *"codex has no channel flag"* ]] || miss "marvel-role codex --channel: rc=$rc out=$out"

# The mode needs no role library: the launcher names none.
if grep -n -E 'OVERLAY_ROOT|slice\.sh' "$SESSION" >/dev/null; then miss "the launcher reaches for the fleet's role-library launcher"; fi

# ---- missing claude ----
rc=0
out="$(cd "$work/plain" && env -i HOME="$work" PATH="/usr/bin:/bin" "$SESSION" 2>&1 </dev/null)" || rc=$?
[[ $rc -ne 0 && "$out" == *"claude"* ]] || miss "no claude on PATH: rc=$rc out=$out"

if [[ $fail -ne 0 ]]; then echo "FAIL"; exit 1; fi
echo "ok: director-session looks the channel server up from the user's config, keys only, and keeps every ID-A launch check"
