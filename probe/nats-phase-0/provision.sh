#!/usr/bin/env bash
# Provision the Phase 0 broker's inbox stream (idempotent, raise-only).
#
# AGENT_INBOX is created here when it does not exist, at the declared max age.
# One that exists is only ever RAISED: a finite max age shorter than the
# declared one is edited up to it (max_age and nothing else), and a longer one,
# or 0 (unlimited), is left alone. The same holds for every local durable on
# the stream, meaning any consumer whose name starts with mcp_ (the shim names
# one per session: mcp_<agent>_<instance>): a finite inactive_threshold below
# the stream's max age is raised to a day above it, and a threshold at or above
# the stream's max age, or none (0), is left alone. Nothing here lowers a value.
# A durable is kept above its stream's max age so a live seat's durable
# outlasts its mail (aae-orc-t77rr), the same invariant the global hub holds.
#
# Every stream and durable prints its value before and after on one line, and
# the run ends with a read-back of the stream, so whoever runs it on the broker
# gets the result from the same command. AGENT_AUDIT and AGENT_STATE are not
# touched.
#
# Runs against the broker on DIRECTOR_PHASE0_URL (default nats://127.0.0.1:4222).
# The default is the anonymous base config. For the authorized config
# (nats-server-auth.conf) set DIRECTOR_PHASE0_ADMIN_USER and
# DIRECTOR_PHASE0_ADMIN_PASS (director_admin and its password).
#
# start.sh runs this once the server answers, so a restart applies it. It can
# also be run by hand on the broker host:
#   probe/nats-phase-0/provision.sh
set -euo pipefail
URL="${DIRECTOR_PHASE0_URL:-nats://127.0.0.1:4222}"
AUTH=()
if [[ -n "${DIRECTOR_PHASE0_ADMIN_USER:-}" ]]; then
  AUTH=(--user "$DIRECTOR_PHASE0_ADMIN_USER" --password "${DIRECTOR_PHASE0_ADMIN_PASS:?DIRECTOR_PHASE0_ADMIN_PASS is required with DIRECTOR_PHASE0_ADMIN_USER}")
fi
A=(nats -s "$URL" ${AUTH[@]+"${AUTH[@]}"})
command -v jq >/dev/null 2>&1 || { echo "jq not on PATH" >&2; exit 2; }
# The declared values. A durable's threshold is a day above the stream's max
# age, the same invariant director-mcp holds for new durables
# (localConsumerInactive, 8 days against the 7-day inbox).
STREAM=AGENT_INBOX
STREAM_MAX_AGE=168h
STREAM_MAX_AGE_NS=$(( ${STREAM_MAX_AGE%h} * 3600 * 1000000000 ))
DAY_NS=$(( 24 * 3600 * 1000000000 ))
hours() { awk -v n="$1" 'BEGIN { if (n == 0) print "unlimited"; else printf "%gh", n / 3600000000000 }'; }

if info="$("${A[@]}" stream info "$STREAM" --json 2>/dev/null)"; then
  cur="$(printf '%s' "$info" | jq -r '.config.max_age // 0')"
  if (( cur > 0 && cur < STREAM_MAX_AGE_NS )); then
    "${A[@]}" stream edit "$STREAM" --max-age "$STREAM_MAX_AGE" --force >/dev/null
    echo "stream $STREAM max_age raised $(hours "$cur") -> $STREAM_MAX_AGE"
    cur="$STREAM_MAX_AGE_NS"
  else
    echo "stream $STREAM max_age $(hours "$cur") -> $(hours "$cur") (unchanged)"
  fi
else
  "${A[@]}" stream add "$STREAM" --subjects 'agent.*.*.*.inbox,agent.*.*.role.*.inbox' --storage file \
    --retention limits --max-age "$STREAM_MAX_AGE" --max-msg-size 65536 --dupe-window 2m --defaults >/dev/null
  echo "stream $STREAM created, max_age $STREAM_MAX_AGE"
  cur="$STREAM_MAX_AGE_NS"
fi

# The durables are measured against the stream's max age after the edit above.
# An unlimited stream (0) expires nothing, so no threshold is below it.
if (( cur > 0 )); then
  want_ns=$(( cur + DAY_NS ))
  want="$(hours "$want_ns")"
  names="$("${A[@]}" consumer ls "$STREAM" --names --json)" || { echo "consumer ls $STREAM failed" >&2; exit 1; }
  for c in $(printf '%s' "$names" | jq -r '.[]? | select(startswith("mcp_"))'); do
    cinfo="$("${A[@]}" consumer info "$STREAM" "$c" --json)" || { echo "consumer info $STREAM $c failed" >&2; exit 1; }
    th="$(printf '%s' "$cinfo" | jq -r '.config.inactive_threshold // 0')"
    if (( th > 0 && th < cur )); then
      "${A[@]}" consumer edit "$STREAM" "$c" --inactive-threshold "$want" --force >/dev/null
      echo "durable $c on $STREAM inactive_threshold raised $(hours "$th") -> $want"
    else
      echo "durable $c on $STREAM inactive_threshold $(hours "$th") -> $(hours "$th") (unchanged)"
    fi
  done
fi
echo "read-back: stream $STREAM max_age $(hours "$("${A[@]}" stream info "$STREAM" --json | jq -r '.config.max_age // 0')")"
