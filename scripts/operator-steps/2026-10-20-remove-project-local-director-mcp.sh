#!/usr/bin/env bash
# Operator step: remove the project-local director-mcp entry for the aae-orc path
# from ~/.claude.json, so a Claude session started in that folder no longer joins
# the bus as the director. Start the director with `scripts/director start`.
#
#   2026-10-20-remove-project-local-director-mcp.sh --check    reads, changes nothing
#   2026-10-20-remove-project-local-director-mcp.sh --apply    runs the checks, backs up, removes
#
# Changes exactly one thing: projects[<path>].mcpServers["director-mcp"]. Every
# other key, the same entry under any other path, and any other server in this
# project stay as they are. Values (the entry's env) are never read out or printed;
# only key names are shown.
#
# Defaults: CLAUDE_JSON=$HOME/.claude.json, PROJECT_PATH=$HOME/work/aae-orc.
# Run it yourself. Claude Code rewrites that file while it runs, so the script
# checks the result after writing and tells you the backup path if anything
# differs from what it intended.
#
# Exit: 0 PASS, 2 usage, 3 STOP.
set -euo pipefail

file="${CLAUDE_JSON:-$HOME/.claude.json}"
project="${PROJECT_PATH:-$HOME/work/aae-orc}"
mode="${1:-}"
[[ "$mode" == --check || "$mode" == --apply ]] || { printf 'usage: %s --check | --apply\n' "$(basename "$0")" >&2; exit 2; }
command -v python3 >/dev/null 2>&1 || { printf 'STOP: python3 not found\n'; exit 3; }

printf 'file:    %s\nproject: %s\n' "${file/#$HOME/\$HOME}" "${project/#$HOME/\$HOME}"
FILE="$file" PROJECT="$project" MODE="$mode" python3 - <<'PY'
import json, os, shutil, sys, time

file, project, mode = os.environ["FILE"], os.environ["PROJECT"], os.environ["MODE"]

def stop(msg):
    print("STOP: " + msg)
    sys.exit(3)

if not os.path.isfile(file):
    stop("no such file")
try:
    with open(file, encoding="utf-8") as f:
        raw = f.read()
    data = json.loads(raw)
except (OSError, ValueError):
    stop("the file is not readable JSON; nothing was changed")
print("PASS the file is valid JSON (%d top-level keys)" % len(data))

proj = (data.get("projects") or {}).get(project)
if proj is None:
    print("PASS nothing to remove: the project path has no entry in the file")
    sys.exit(0)
servers = proj.get("mcpServers") or {}
if "director-mcp" not in servers:
    print("PASS nothing to remove: no director-mcp entry for this project (servers here: %s)" % (", ".join(sorted(servers)) or "none"))
    sys.exit(0)
entry = servers["director-mcp"]
print("PASS found projects[<path>].mcpServers.director-mcp, keys: " + ", ".join(sorted(entry)))
print("PASS its env names: " + ", ".join(sorted((entry.get("env") or {}))))
print("PASS other servers in this project stay: " + (", ".join(sorted(s for s in servers if s != "director-mcp")) or "none"))
others = sorted(p for p, v in data["projects"].items() if p != project and "director-mcp" in (v.get("mcpServers") or {}))
print("PASS the same entry under %d other project path(s) stays" % len(others))
if mode == "--check":
    print("PASS --check done; nothing was changed. Run with --apply to remove it.")
    sys.exit(0)

backup = "%s.bak-%s" % (file, time.strftime("%Y%m%d-%H%M%S"))
shutil.copy2(file, backup)
os.chmod(backup, 0o600)
print("PASS backup written: " + backup.replace(os.path.expanduser("~"), "$HOME"))

del servers["director-mcp"]
tmp = file + ".tmp-%d" % os.getpid()
with open(tmp, "w", encoding="utf-8") as f:
    json.dump(data, f, indent=2, ensure_ascii=False)
    f.write("\n" if raw.endswith("\n") else "")
os.chmod(tmp, os.stat(file).st_mode & 0o777)
os.replace(tmp, file)

with open(file, encoding="utf-8") as f:
    after = json.load(f)
expect = json.loads(raw)
del expect["projects"][project]["mcpServers"]["director-mcp"]
if after != expect:
    stop("the file differs from what was intended (it may have been rewritten by a running session); the backup is " + backup)
print("PASS removed; every other key is as it was")
PY
