#!/usr/bin/env bash
# Provision the global hub's streams and presence bucket (idempotent).
# Runs as the hub admin NKey. The hub is bare until this runs (finding-166).
#
# A stream that does not exist is created with the declared max age. One that
# does exist is only ever RAISED: a finite max age shorter than the declared one
# is edited up to it (max_age and nothing else), and a longer one, or 0
# (unlimited), is left alone. The same holds for every seat's director durable
# on those streams, meaning any consumer whose name starts with mcp_global_ (the
# shim names one per session; a hand-made consumer given that prefix would be
# raised too): a finite inactive_threshold shorter than the declared one is
# raised to it, a longer one or none (0) is left alone. Nothing
# here lowers a value, and each change prints one line. A durable is kept above
# its stream's max age so a live seat's durable outlasts its mail
# (aae-orc-t77rr).
#
# Nothing runs this at hub start: start.sh starts the server only. Whoever
# provisions the hub invokes it, with the admin NKey, on the hub host.
set -euo pipefail
HOME_DIR="${DIRECTOR_GLOBAL_HOME:-$HOME/.director/nats-global}"
URL="${DIRECTOR_GLOBAL_URL:-nats://127.0.0.1:4242}"
# After the TLS cutover (finding-001): DIRECTOR_GLOBAL_URL=tls://127.0.0.1:4242
# and HUB_CA=$HOME_DIR/tls/ca.pem (the default once that file exists).
HUB_CA="${HUB_CA:-}"; [[ -z "$HUB_CA" && -r "$HOME_DIR/tls/ca.pem" ]] && HUB_CA="$HOME_DIR/tls/ca.pem"
TLSCA=(); [[ -n "$HUB_CA" ]] && TLSCA=(--tlsca "$HUB_CA")
# The admin NKey. Unset means the hub's key; set to the empty string means none,
# which only a scratch server with no authorization wants (verify-provision-raise.sh).
ADMIN_NKEY="${DIRECTOR_GLOBAL_ADMIN_NKEY-$HOME_DIR/keys/admin.nk}"
AUTH=(); [[ -n "$ADMIN_NKEY" ]] && AUTH=(--nkey "$ADMIN_NKEY")
A=(nats -s "$URL" ${TLSCA[@]+"${TLSCA[@]}"} ${AUTH[@]+"${AUTH[@]}"})
command -v jq >/dev/null 2>&1 || { echo "jq not on PATH" >&2; exit 2; }
# The declared values. The durable's threshold is a day above the stream's max
# age, the same invariant director-mcp holds for new durables (globalConsumerInactive).
STREAM_MAX_AGE=168h
DURABLE_INACTIVE=192h
# The nanosecond forms are derived from the hours above, so the two cannot drift.
STREAM_MAX_AGE_NS=$(( ${STREAM_MAX_AGE%h} * 3600 * 1000000000 ))
DURABLE_INACTIVE_NS=$(( ${DURABLE_INACTIVE%h} * 3600 * 1000000000 ))
clusters=("$@")
[[ ${#clusters[@]} -gt 0 ]] || clusters=(kinu mokuzai)
hours() { awk -v n="$1" 'BEGIN { if (n == 0) print "unlimited"; else printf "%gh", n / 3600000000000 }'; }
# raise_durables lifts every consumer on one stream whose name starts with
# mcp_global_, which is how the shim names each session's durable. The test is a
# name prefix and nothing more, so a hand-made consumer that happened to carry
# that prefix would be raised too; one without it is not this script's to change.
raise_durables() {
  local stream="$1" names c info cur
  names="$("${A[@]}" consumer ls "$stream" --names --json)" || { echo "consumer ls $stream failed" >&2; exit 1; }
  for c in $(printf '%s' "$names" | jq -r '.[]? | select(startswith("mcp_global_"))'); do
    info="$("${A[@]}" consumer info "$stream" "$c" --json)" || { echo "consumer info $stream $c failed" >&2; exit 1; }
    cur="$(printf '%s' "$info" | jq -r '.config.inactive_threshold // 0')"
    if (( cur > 0 && cur < DURABLE_INACTIVE_NS )); then
      "${A[@]}" consumer edit "$stream" "$c" --inactive-threshold "$DURABLE_INACTIVE" --force >/dev/null
      echo "durable $c on $stream inactive_threshold raised $(hours "$cur") -> $DURABLE_INACTIVE"
    fi
  done
}
add_stream() {
  local name="$1" subj="$2" info cur
  if info="$("${A[@]}" stream info "$name" --json 2>/dev/null)"; then
    cur="$(printf '%s' "$info" | jq -r '.config.max_age // 0')"
    if (( cur > 0 && cur < STREAM_MAX_AGE_NS )); then
      "${A[@]}" stream edit "$name" --max-age "$STREAM_MAX_AGE" --force >/dev/null
      echo "stream $name max_age raised $(hours "$cur") -> $STREAM_MAX_AGE"
    else
      echo "stream $name exists (max_age $(hours "$cur"), unchanged)"
    fi
    raise_durables "$name"
    return
  fi
  "${A[@]}" stream add "$name" --subjects "$subj" --storage file --retention limits \
    --max-age "$STREAM_MAX_AGE" --max-msg-size 65536 --dupe-window 2m --defaults >/dev/null
  echo "stream $name created ($subj)"
}
add_stream GLOBAL_TO_DIRECTOR 'global.director.>'
for c in "${clusters[@]}"; do add_stream "GLOBAL_TO_$c" "global.$c.>"; done
if "${A[@]}" kv info GLOBAL_PRESENCE >/dev/null 2>&1; then echo "kv GLOBAL_PRESENCE exists"; else
  "${A[@]}" kv add GLOBAL_PRESENCE --ttl 90s --storage file >/dev/null; echo "kv GLOBAL_PRESENCE created (ttl 90s)"; fi
