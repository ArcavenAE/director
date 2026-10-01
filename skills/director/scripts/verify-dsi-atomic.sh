#!/usr/bin/env bash
# Prove dsi writes sessions.json and roster.md atomically (SH1,
# sim/design/board-current-state.md): each is written to a temp file and
# renamed into place, so a reader sees the old file or the new one, never a
# truncated one. The check is a hard link: a rename leaves the old inode, and
# so the link, holding the old content; a write in place would change it.
#
# Usage: skills/director/scripts/verify-dsi-atomic.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DSI="${DSI:-$here/dsi}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
state="$root/state"; mkdir -p "$state" "$root/home"

for f in sessions.json roster.md; do
  echo "OLD $f" >"$state/$f"
  ln "$state/$f" "$root/link-$f"
done

if HOME="$root/home" DIRECTOR_STATE="$state" "$DSI" >"$root/out" 2>&1; then
  ok "dsi runs against an empty scratch home"
else
  bad "dsi runs against an empty scratch home" "$(cat "$root/out")"
fi

for f in sessions.json roster.md; do
  [[ "$(cat "$root/link-$f")" == "OLD $f" ]] \
    && ok "$f replaced by rename, not written in place" \
    || bad "$f replaced by rename, not written in place" "the old inode now holds new content"
done
python3 -c 'import json,sys; json.load(open(sys.argv[1]))' "$state/sessions.json" \
  && ok "sessions.json is complete JSON" || bad "sessions.json is complete JSON"
grep -q '^# Session roster' "$state/roster.md" && ok "roster.md is the new roster" || bad "roster.md is the new roster"
left="$(find "$state" -name '.*tmp*' -o -name '*.tmp' | wc -l | tr -d ' ')"
[[ "$left" == 0 ]] && ok "no temp files left behind" || bad "no temp files left behind" "$left"

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
