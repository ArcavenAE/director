#!/usr/bin/env bash
# Prove director-session, the installed launcher for a hand-started director
# session (the ID-A launcher promoted out of probe/).
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
envf="$work/claude-natsurl"
# A stub harness records its argv, one word per line, and where it ran. It is
# written with printf, not a here-document, which bash 3.2 puts in a file under /tmp.
mkstub() {
  printf '%s\n' '#!/usr/bin/env bash' "pwd -P >\"$marker\"" "printf '%s\\n' \"\$@\" >\"$argvf\"" "printf '%s' \"\$NATS_URL\" >\"$envf\"" >"$work/bin/$1"
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
  rm -f "$marker" "$argvf" "$envf"
  set +e
  out="$(cd "$dir" && env ${envs[@]+"${envs[@]}"} PATH="$work/bin:$PATH" DIRECTOR_SHIM_BIN="$work/shim" "$SESSION" "$@" 2>&1 </dev/null)"
  rc=$?
  set -e
  ran=""; argv=""; seen_url=""
  if [[ -f "$marker" ]]; then ran="$(<"$marker")"; fi
  if [[ -f "$argvf" ]]; then argv="$(<"$argvf")"; fi
  if [[ -f "$envf" ]]; then seen_url="$(<"$envf")"; fi
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
  -u DIRECTOR_NATS_USER -u DIRECTOR_NATS_PASS -u DIRECTOR_NATS_CREDS -u MARVEL_BUS_STATE)
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

# A NATS_URL with an @ anywhere never goes on argv or into the output. It is left
# out of the MCP config, the launcher says so in one line without showing it, and the
# shim gets it from the environment: the stub harness records the NATS_URL it was
# launched with, which must be the URL unchanged. Nothing is parsed, so every form
# is covered: userinfo, a server list, an @ in the path, query or fragment, a raw
# / ? or # in a password, a password of digits, a leading space, tls://. Fixture
# URLs only. Under codex the name is forwarded with env_vars.
env_case() { # <url> <fragment of the credential that must not appear>
  local url="$1" frag="$2" form
  for form in claude codex own; do
    case "$form" in
      claude) run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" "NATS_URL=$url" -- --marvel-role;;
      codex)  run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" "NATS_URL=$url" -- --marvel-role --harness codex;;
      own)    run "$work/plain" "${unset_seat[@]}" "NATS_URL=$url" -- --own-config seat-a;;
    esac
    if [[ $rc -ne 0 || -z "$ran" ]]; then miss "NATS_URL '$url' ($form): did not start: rc=$rc out=$out"; fi
    if [[ "$argv" == *"$frag"* || "$out" == *"$frag"* ]]; then miss "NATS_URL '$url' ($form): the credential reached argv or the output: $argv / $out"; fi
    if [[ "$argv" == *"127.0.0."* || "$out" == *"127.0.0."* || "$argv" == *'"NATS_URL":'* || "$argv" == *'NATS_URL="'* ]]; then miss "NATS_URL '$url' ($form): the URL or its key is on argv or in the output: $argv / $out"; fi
    if [[ "$seen_url" != "$url" ]]; then miss "NATS_URL '$url' ($form): the shim's environment did not get the URL unchanged: '$seen_url'"; fi
    if [[ "$out" != *"not put on the command line"* ]]; then miss "NATS_URL '$url' ($form): no one-line notice: $out"; fi
    if [[ "$form" == codex && "$argv" != *'env_vars=["NATS_URL"]'* ]]; then miss "NATS_URL '$url' (codex): NATS_URL is not forwarded by name: $argv"; fi
    if grep -r -l -F --exclude=claude-natsurl -e "$frag" "$work" >/dev/null 2>&1; then miss "NATS_URL '$url' ($form): the credential reached a file under the scratch dir"; fi
  done
}
env_case 'nats://u:p@127.0.0.1:1' 'u:p'
env_case 'nats://u:p@ss@127.0.0.1:1' 'ss@'
env_case 'nats://u:p@127.0.0.1:1,nats://u:p@127.0.0.2:1' 'u:p'
env_case 'nats://127.0.0.1:1,nats://u:p@127.0.0.2:1' 'u:p'
env_case 'u:p@127.0.0.1:1,u:p@127.0.0.2:1' 'u:p'
env_case 'nats://127.0.0.1:1/p@th' 'p@th'
env_case 'nats://u:p@127.0.0.1:1/p@th?q=a@b' 'u:p'
env_case 'nats://127.0.0.1:1#fr@g' 'fr@g'
env_case 'nats://u:p@127.0.0.1:1#fr@g' 'u:p'
env_case 'nats://u-s:pw/s@127.0.0.1:1' 'u-s'
env_case 'nats://u-s:pw?s@127.0.0.1:1' 'u-s'
env_case 'nats://u-s:pw#s@127.0.0.1:1' 'u-s'
env_case 'nats://u:123/x@127.0.0.1:1' '123/x'
# Token auth is nats://<token>@host, and a base64 token can hold a raw / (a fixture token).
env_case 'nats://tok-s@127.0.0.1:1' 'tok-s'
env_case 'nats://tok-s/x@127.0.0.1:1' 'tok-s'
env_case 'nats://tok-s?x@127.0.0.1:1' 'tok-s'
env_case 'nats://tok-s#x@127.0.0.1:1' 'tok-s'
env_case 'nats://127.0.0.1:1, nats://u:p@127.0.0.2:1' 'u:p'
env_case ', nats://u:p@127.0.0.2:1' 'u:p'
env_case 'tls://u:p@127.0.0.1:1, tls://u:p@127.0.0.2:1' 'u:p'

# The bus credential names the shim reads are forwarded by NAME under codex and never put
# on argv (fixture values). Under claude the launching environment reaches the shim
# itself, and the value is on neither the command line nor in the output.
for name in DIRECTOR_NATS_CREDS DIRECTOR_NATS_USER DIRECTOR_NATS_PASS MARVEL_BUS_STATE; do
  sentinel="sentinel-$name-value"
  run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" "$name=$sentinel" -- --marvel-role --harness codex
  [[ $rc -eq 0 && "$argv" == *"env_vars="*"\"$name\""* ]] || miss "codex: $name not forwarded by name: rc=$rc $argv"
  [[ "$argv" != *"$sentinel"* && "$out" != *"$sentinel"* ]] || miss "codex: the value of $name is on argv or in the output: $argv / $out"
  run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" "$name=$sentinel" -- --marvel-role
  [[ $rc -eq 0 && "$argv" != *"$sentinel"* && "$out" != *"$sentinel"* && "$argv" != *"$name"* ]] || miss "claude: $name reached argv or the output: rc=$rc $argv / $out"
  run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" -- --marvel-role --harness codex
  [[ "$argv" != *"$name"* ]] || miss "codex: $name forwarded though unset: $argv"
done
# All four and NATS_URL together are forwarded in one list, by name.
run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" DIRECTOR_NATS_CREDS=c1 DIRECTOR_NATS_USER=u1 DIRECTOR_NATS_PASS=p1 MARVEL_BUS_STATE=/b1 NATS_URL=nats://u:p@127.0.0.1:1 -- --marvel-role --harness codex
[[ "$argv" == *'env_vars=["NATS_URL","DIRECTOR_NATS_CREDS","DIRECTOR_NATS_USER","DIRECTOR_NATS_PASS","MARVEL_BUS_STATE"]'* ]] || miss "codex: the five names are not in one list: $argv"

# The launcher marks every seat it launches, so the shim fails loudly (exit 2, one line)
# instead of joining the local broker when no NATS_URL arrives: the marker is in the
# config of the claude and --own-config forms and in the codex env table.
run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" -- --marvel-role
[[ "$argv" == *'"DIRECTOR_BUS_REQUIRED":"1"'* ]] || miss "marvel-role (claude): the bus-required marker is not in the config: $argv"
run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" -- --marvel-role --harness codex
[[ "$argv" == *'DIRECTOR_BUS_REQUIRED="1"'* ]] || miss "marvel-role (codex): the bus-required marker is not in the env table: $argv"
run "$work/plain" "${unset_seat[@]}" -- --own-config seat-a
[[ "$argv" == *'"DIRECTOR_BUS_REQUIRED":"1"'* ]] || miss "--own-config: the bus-required marker is not in the config: $argv"
run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" 'NATS_URL=nats://u:p@127.0.0.1:1' -- --marvel-role
[[ "$argv" == *'"DIRECTOR_BUS_REQUIRED":"1"'* && "$argv" != *'"NATS_URL"'* ]] || miss "an @ URL: the marker is set and NATS_URL is left to the environment: $argv"

# With no @ the URL (or the local default) goes in the config as before, with no
# notice and no forwarding.
for form in claude codex own; do
  case "$form" in
    claude) run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" NATS_URL=nats://127.0.0.1:1,nats://127.0.0.2:1 -- --marvel-role;;
    codex)  run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" NATS_URL=nats://127.0.0.1:1,nats://127.0.0.2:1 -- --marvel-role --harness codex;;
    own)    run "$work/plain" "${unset_seat[@]}" NATS_URL=nats://127.0.0.1:1,nats://127.0.0.2:1 -- --own-config seat-a;;
  esac
  [[ $rc -eq 0 && "$out" != *"not put on the command line"* && "$argv" != *env_vars* ]] || miss "clean NATS_URL list ($form): rc=$rc out=$out argv=$argv"
  [[ "$argv" == *nats://127.0.0.1:1,nats://127.0.0.2:1* ]] || miss "clean NATS_URL list ($form): not in the config: $argv"
  case "$form" in
    claude) run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" -- --marvel-role;;
    codex)  run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" -- --marvel-role --harness codex;;
    own)    run "$work/plain" "${unset_seat[@]}" -- --own-config seat-a;;
  esac
  [[ "$argv" == *nats://127.0.0.1:4222* ]] || miss "no NATS_URL ($form): the local default is not in the config: $argv"
done

# codex with DIRECTOR_CUE=1 in the env still starts, and says the cue does not apply.
run "$work/plain" "${unset_seat[@]}" "${seat_a[@]}" DIRECTOR_CUE=1 -- --marvel-role --harness codex
[[ $rc -eq 0 && -n "$ran" && "$out" == *"DIRECTOR_CUE=1"*"no effect"* && "$argv" != *DIRECTOR_CUE* ]] || miss "marvel-role codex with DIRECTOR_CUE=1: rc=$rc out=$out argv=$argv"

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
