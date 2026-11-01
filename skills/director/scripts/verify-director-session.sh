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

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin"
marker="$work/claude-ran"
argvf="$work/claude-argv"
cat >"$work/bin/claude" <<STUB
#!/usr/bin/env bash
pwd -P >"$marker"
printf '%s\n' "\$@" >"$argvf"
STUB
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
# one. python3's pty module works the same on macOS and Linux.
runtty() {
  local dir="$1"; shift
  local envs=()
  while [[ $# -gt 0 && "$1" != "--" ]]; do envs+=("$1"); shift; done
  shift
  rm -f "$marker" "$argvf"
  set +e
  out="$(cd "$dir" && env ${envs[@]+"${envs[@]}"} PATH="$work/bin:$PATH" DIRECTOR_SHIM_BIN="$work/shim" \
    python3 -c 'import os,pty,sys; sys.exit(os.waitstatus_to_exitcode(pty.spawn(sys.argv[1:])))' "$SESSION" "$@" 2>&1)"
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

# ---- missing claude ----
rc=0
out="$(cd "$work/plain" && env -i HOME="$work" PATH="/usr/bin:/bin" "$SESSION" 2>&1 </dev/null)" || rc=$?
[[ $rc -ne 0 && "$out" == *"claude"* ]] || miss "no claude on PATH: rc=$rc out=$out"

if [[ $fail -ne 0 ]]; then echo "FAIL"; exit 1; fi
echo "ok: director-session looks the channel server up from the user's config, keys only, and keeps every ID-A launch check"
