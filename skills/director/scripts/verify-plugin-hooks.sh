#!/usr/bin/env bash
# Prove the director plugin tree that wires the standing hook (standing loop S8,
# sim/design/director-standing-loop.md section 3 and slice row 8; director's S8 rulings recorded on
# aae-orc-rtz9w). Red first: until plugin/director exists every case below fails on an assertion.
#
# The tree, in the repo at plugin/director (hooks only: no skill, no .mcp.json, no settings file):
#   .claude-plugin/plugin.json   name "director", a version
#   hooks/hooks.json             two events, one script, each command quoting the plugin root:
#                                  UserPromptSubmit  "${CLAUDE_PLUGIN_ROOT}/scripts/standing-hook" --seen
#                                  SessionStart      "${CLAUDE_PLUGIN_ROOT}/scripts/standing-hook" --loop-line --seen
#   scripts/standing-hook        a symlink to skills/director/scripts/standing-hook: one source of truth
# `--seen` stamps $DIRECTOR_STATE/hook-seen and is the only way the hook writes it; `--loop-line` prints only the
# loop line, so a session start adds nothing when there is no tick. SessionStart passes both: a start that
# reached the hook proves the wiring before any prompt.
#
# Run conditions: every run is env -i with a scratch HOME and TMPDIR, no MARVEL_*, NATS_* or DIRECTOR_* variable
# beyond the scratch DIRECTOR_STATE, no write outside scratch. `claude plugin validate` reads the tree and runs no
# model call; it is skipped, with the reason, when claude is not on PATH. It writes only to HOME (scratch here) and,
# measured under a write-deny and network-deny sandbox profile with its controls, tries first-party telemetry
# unless DISABLE_TELEMETRY and CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC are set, which this script sets.
#
# Usage: skills/director/scripts/verify-plugin-hooks.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
T="$here/plugin/director"
HOOK="$here/skills/director/scripts/standing-hook"
PY="$(command -v python3 || true)"
[[ -n "$PY" ]] || { echo "python3 not on PATH"; exit 2; }
pass=0; fail=0; skipped=0
ok()   { echo "PASS $1"; pass=$((pass+1)); }
bad()  { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }
skip() { echo "SKIP $1"; skipped=$((skipped+1)); }

root="$(cd "$(mktemp -d "${TMPDIR:-/tmp}/verify-plugin.XXXXXX")" && pwd -P)"
trap 'rm -rf "$root"' EXIT
mkdir -p "$root/tmp" "$root/home" "$root/state"

WANT_PROMPT='"${CLAUDE_PLUGIN_ROOT}/scripts/standing-hook" --seen'
WANT_START='"${CLAUDE_PLUGIN_ROOT}/scripts/standing-hook" --loop-line --seen'

# jq-free reader: pj <file> <python expression over d>
pj() { "$PY" -I -c 'import json,sys;d=json.load(open(sys.argv[1]));print(eval(sys.argv[2]))' "$1" "$2" 2>/dev/null; }

# --- names-standing-hook: the manifest and the hooks file --------------------------------------------------------
M="$T/.claude-plugin/plugin.json"; HJ="$T/hooks/hooks.json"
{ [[ -f "$M" ]] && [[ "$(pj "$M" 'd["name"]')" == director ]] && [[ -n "$(pj "$M" 'd["version"]')" ]]; } \
  && ok "the manifest names the plugin director and carries a version" || bad "manifest" "$M"
{ [[ -f "$HJ" ]] && [[ "$(pj "$HJ" 'sorted(d["hooks"])')" == "['SessionStart', 'UserPromptSubmit']" ]]; } \
  && ok "names-standing-hook: hooks.json wires exactly UserPromptSubmit and SessionStart" || bad "hooks.json events" "$(pj "$HJ" 'sorted(d["hooks"])' || true)"
[[ "$(pj "$HJ" 'd["hooks"]["UserPromptSubmit"][0]["hooks"][0]["command"]')" == "$WANT_PROMPT" ]] \
  && ok "UserPromptSubmit runs the plugin's standing-hook with --seen, the placeholder quoted" || bad "UserPromptSubmit command" "$(pj "$HJ" 'd["hooks"]["UserPromptSubmit"][0]["hooks"][0]["command"]' || true)"
[[ "$(pj "$HJ" 'd["hooks"]["SessionStart"][0]["hooks"][0]["command"]')" == "$WANT_START" ]] \
  && ok "SessionStart runs it with --loop-line --seen, the placeholder quoted" || bad "SessionStart command" "$(pj "$HJ" 'd["hooks"]["SessionStart"][0]["hooks"][0]["command"]' || true)"
[[ "$(pj "$HJ" 'len(d["hooks"]["UserPromptSubmit"])+len(d["hooks"]["SessionStart"])+len(d["hooks"]["UserPromptSubmit"][0]["hooks"])+len(d["hooks"]["SessionStart"][0]["hooks"])')" == 4 ]] \
  && ok "each event carries one group with one hook" || bad "hook counts"

# --- the script is the real standing-hook -----------------------------------------------------------------------
{ [[ -L "$T/scripts/standing-hook" && -x "$T/scripts/standing-hook" ]] && [[ "$("$PY" -I -c 'import os,sys;print(os.path.realpath(sys.argv[1]))' "$T/scripts/standing-hook")" == "$("$PY" -I -c 'import os,sys;print(os.path.realpath(sys.argv[1]))' "$HOOK")" ]]; } \
  && ok "scripts/standing-hook is an executable link to skills/director/scripts/standing-hook" || bad "scripts/standing-hook link"

# --- hooks only ------------------------------------------------------------------------------------------------
got="$( [[ -d "$T" ]] && (cd "$T" && find . \( -type f -o -type l \) | LC_ALL=C sort | tr '\n' ' ') || true)"
[[ "$got" == "./.claude-plugin/plugin.json ./hooks/hooks.json ./scripts/standing-hook " ]] \
  && ok "the tree holds the manifest, hooks.json and the script and nothing else (no skill, no .mcp.json, no settings file)" || bad "tree contents" "[$got]"

# --- each command finds its script through the plugin root ------------------------------------------------------
run_cmd() { # run_cmd <name> <command> : the command as Claude Code runs it, plugin root set, scratch state
  local rc=0
  env -i HOME="$root/home" TMPDIR="$root/tmp" PATH=/usr/bin:/bin LANG=C CLAUDE_PLUGIN_ROOT="$T" DIRECTOR_STATE="$root/state" \
    DWS_TEST_CLOCK=1 DWS_NOW=2026-11-18T12:00:00Z sh -c "$2" >"$root/$1.out" 2>"$root/$1.err" </dev/null || rc=$?
  printf '%s' "$rc" >"$root/$1.rc"
}
rm -rf "$root/state"; mkdir -p "$root/state"
run_cmd prompt "$WANT_PROMPT"
{ [[ "$(cat "$root/prompt.rc")" == 0 ]] && [[ -s "$root/state/hook-seen" ]] && grep -q '^drain age: ' "$root/prompt.out"; } \
  && ok "the UserPromptSubmit command runs from the plugin root, prints the hook's text and stamps hook-seen" || bad "UserPromptSubmit run" "rc=$(cat "$root/prompt.rc") err=[$(head -c 200 "$root/prompt.err")]"
rm -rf "$root/state"; mkdir -p "$root/state"
run_cmd start "$WANT_START"
{ [[ "$(cat "$root/start.rc")" == 0 ]] && [[ -s "$root/state/hook-seen" ]] && [[ ! -s "$root/start.out" ]]; } \
  && ok "the SessionStart command prints nothing with no tick and still stamps hook-seen" || bad "SessionStart run" "rc=$(cat "$root/start.rc") out=[$(head -c 200 "$root/start.out")]"

# --- claude plugin validate, no model call ----------------------------------------------------------------------
CL="$(command -v claude || true)"
if [[ -z "$CL" ]]; then
  skip "claude plugin validate: claude is not on PATH"
else
  rc=0
  env -i HOME="$root/home" TMPDIR="$root/tmp" PATH="$(dirname "$CL"):/usr/bin:/bin" LANG=C \
    DISABLE_TELEMETRY=1 CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 DISABLE_AUTOUPDATER=1 \
    "$CL" plugin validate "$T" --json >"$root/validate.json" 2>"$root/validate.err" || rc=$?
  [[ "$rc" == 0 ]] && ok "claude plugin validate exits 0 on the tree" || bad "claude plugin validate exit" "rc=$rc $(head -c 200 "$root/validate.err") $(head -c 200 "$root/validate.json")"
  [[ "$(pj "$root/validate.json" 'd["success"] and not d["manifest"]["errors"]')" == True ]] \
    && ok "validate reports success and no manifest error" || bad "validate success"
  [[ "$(pj "$root/validate.json" 'sorted((h["event"],h["command"]) for h in d["provides"]["hooks"])')" == "[('SessionStart', '$WANT_START'), ('UserPromptSubmit', '$WANT_PROMPT')]" ]] \
    && ok "validate finds both hooks, with the commands pinned above" || bad "validate provides.hooks" "$(pj "$root/validate.json" 'd["provides"]["hooks"]' || true)"
  [[ "$(pj "$root/validate.json" 'sum(len(c["errors"])+len(c["warnings"]) for c in d["contents"] if c["type"]=="hooks")')" == 0 ]] \
    && ok "validate has no error or warning on the hooks file" || bad "validate hooks warnings"
fi

echo "$pass passed, $fail failed, $skipped skipped"
[[ $fail -eq 0 ]]
