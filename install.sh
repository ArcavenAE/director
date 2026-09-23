#!/usr/bin/env bash
# Install the director skill and command into a Claude Code configuration.
#
#   ./install.sh              symlink into ~/.claude (development default)
#   ./install.sh --copy       copy instead, for a machine that does not have
#                             this repo checked out
#   ./install.sh --target DIR install somewhere other than ~/.claude
#
# The repo is the authoritative source. A symlinked install edits live.
set -euo pipefail

SRC="$(cd "$(dirname "$0")" && pwd)"
TARGET="${CLAUDE_HOME:-$HOME/.claude}"
MODE=link

while [ $# -gt 0 ]; do
  case "$1" in
    --copy) MODE=copy ;;
    --link) MODE=link ;;
    --target) TARGET="$2"; shift ;;
    -h|--help) sed -n '2,10p' "$0"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
  shift
done

mkdir -p "$TARGET/skills" "$TARGET/commands"

install_one() {
  src="$1"; dst="$2"
  if [ -e "$dst" ] && [ ! -L "$dst" ]; then
    echo "refusing to replace non-symlink: $dst" >&2
    echo "move it aside first; this script never deletes your files" >&2
    exit 1
  fi
  rm -f "$dst"
  if [ "$MODE" = link ]; then
    ln -s "$src" "$dst"
  else
    cp -R "$src" "$dst"
  fi
  printf '  %s -> %s\n' "$dst" "$src"
}

echo "installing director ($MODE) into $TARGET"
install_one "$SRC/skills/director" "$TARGET/skills/director"
install_one "$SRC/skills/stansfield" "$TARGET/skills/stansfield"
install_one "$SRC/commands/director.md" "$TARGET/commands/director.md"

STATE="${DIRECTOR_STATE:-$HOME/.director/state}"
mkdir -p "$STATE"
echo "state root: $STATE  (operational; never committed)"
echo "done. /director for a sweep, /director standing to adopt the role."
