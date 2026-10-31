#!/usr/bin/env bash
# Start the director Phase 0 probe broker: anonymous, localhost only, JetStream
# on. The base config (nats-server.conf) reads its store_dir from the
# environment, so this derives the default and exports it before exec; a second
# host places its store under its own home with no edit to the checked-in config.
#
# DIRECTOR_PHASE0_HOME is the operator knob (default ~/.director/nats), the same
# shape as the global tier's DIRECTOR_GLOBAL_HOME. NATS substitutes an env var
# only as an unquoted whole value, so the config reads DIRECTOR_PHASE0_STORE
# (the full store path) and this script derives it from HOME.
#
# This launches the ANONYMOUS base only. The authorized relaunch is a separate,
# coordinated activation (nats-server-auth.conf) and is deliberately not this
# path, so a routine restart never silently requires auth and drops a live fleet.
# Extra args pass through to nats-server (for example -p/-m on a throwaway port).
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export DIRECTOR_PHASE0_HOME="${DIRECTOR_PHASE0_HOME:-$HOME/.director/nats}"
export DIRECTOR_PHASE0_STORE="$DIRECTOR_PHASE0_HOME/store"
mkdir -p "$DIRECTOR_PHASE0_STORE" "$DIRECTOR_PHASE0_HOME/log"

# Provision the inbox stream once the server answers, so a restart applies the
# declared retention (168h inbox, local durables a day above it) with no
# operator step. provision.sh only raises. It runs in the background, started
# before the exec below, so the server is still this script's process; it
# retries while the server comes up. A provision that never succeeds logs one
# line to log/provision.log and never stops the broker from serving. Needs jq;
# without it the broker starts anyway and the skip is logged. The URL is
# DIRECTOR_PHASE0_URL (default nats://127.0.0.1:4222), so a throwaway port needs
# it set too. DIRECTOR_PHASE0_PROVISION, DIRECTOR_PROVISION_TRIES and
# DIRECTOR_PHASE0_CONF exist so verify-start-provision.sh can stand in a failing
# provision, a short retry window and a scratch config.
provision="${DIRECTOR_PHASE0_PROVISION:-$here/provision.sh}"
tries="${DIRECTOR_PROVISION_TRIES:-30}"
plog="$DIRECTOR_PHASE0_HOME/log/provision.log"
stamp() { date -u +%Y-%m-%dT%H:%M:%SZ; }
if command -v jq >/dev/null 2>&1; then
  (
    rc=0
    for _ in $(seq 1 "$tries"); do
      if out="$(bash "$provision" 2>&1)"; then
        printf '%s provision ok\n%s\n' "$(stamp)" "$out" >> "$plog"
        exit 0
      else
        rc=$?
      fi
      sleep 2
    done
    printf '%s provision failed after %s tries (last exit %s); the broker keeps serving\n' "$(stamp)" "$tries" "$rc" >> "$plog"
  ) > /dev/null 2>&1 &
else
  printf '%s jq not on PATH: provision skipped, the broker starts without it\n' "$(stamp)" >> "$plog"
fi

exec nats-server -c "${DIRECTOR_PHASE0_CONF:-$here/nats-server.conf}" \
  --pid "$DIRECTOR_PHASE0_HOME/nats-server.pid" \
  --log "$DIRECTOR_PHASE0_HOME/log/nats-server.log" \
  "$@"
