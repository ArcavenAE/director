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

exec nats-server -c "$here/nats-server.conf" \
  --pid "$DIRECTOR_PHASE0_HOME/nats-server.pid" \
  --log "$DIRECTOR_PHASE0_HOME/log/nats-server.log" \
  "$@"
