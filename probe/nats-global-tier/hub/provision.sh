#!/usr/bin/env bash
# Provision the global hub's streams and presence bucket (idempotent).
# Runs as the hub admin NKey. The hub is bare until this runs (finding-166).
set -euo pipefail
HOME_DIR="${DIRECTOR_GLOBAL_HOME:-$HOME/.director/nats-global}"
URL="${DIRECTOR_GLOBAL_URL:-nats://127.0.0.1:4242}"
# After the TLS cutover (finding-001): DIRECTOR_GLOBAL_URL=tls://127.0.0.1:4242
# and HUB_CA=$HOME_DIR/tls/ca.pem (the default once that file exists).
HUB_CA="${HUB_CA:-}"; [[ -z "$HUB_CA" && -r "$HOME_DIR/tls/ca.pem" ]] && HUB_CA="$HOME_DIR/tls/ca.pem"
TLSCA=(); [[ -n "$HUB_CA" ]] && TLSCA=(--tlsca "$HUB_CA")
A=(nats -s "$URL" "${TLSCA[@]}" --nkey "$HOME_DIR/keys/admin.nk")
clusters=("$@")
[[ ${#clusters[@]} -gt 0 ]] || clusters=(kinu mokuzai)
add_stream() {
  local name="$1" subj="$2"
  if "${A[@]}" stream info "$name" >/dev/null 2>&1; then echo "stream $name exists"; return; fi
  "${A[@]}" stream add "$name" --subjects "$subj" --storage file --retention limits \
    --max-age 24h --max-msg-size 65536 --dupe-window 2m --defaults >/dev/null
  echo "stream $name created ($subj)"
}
add_stream GLOBAL_TO_DIRECTOR 'global.director.>'
for c in "${clusters[@]}"; do add_stream "GLOBAL_TO_$c" "global.$c.>"; done
if "${A[@]}" kv info GLOBAL_PRESENCE >/dev/null 2>&1; then echo "kv GLOBAL_PRESENCE exists"; else
  "${A[@]}" kv add GLOBAL_PRESENCE --ttl 90s --storage file >/dev/null; echo "kv GLOBAL_PRESENCE created (ttl 90s)"; fi
