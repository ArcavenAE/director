#!/usr/bin/env bash
# Start the global hub on kinu (interim placement, operator ruling 2026-09-14).
# Keys: $DIRECTOR_GLOBAL_HOME/keys/{admin,director,leaf-<cluster>}.nk, 0600,
# generated with: nats auth nkey gen user > <name>.nk
# Public keys are pasted into nats-server.conf; seeds never leave the keys dir.
set -euo pipefail
export DIRECTOR_GLOBAL_HOME="${DIRECTOR_GLOBAL_HOME:-$HOME/.director/nats-global}"
# nats-server substitutes an environment variable only as a whole unquoted
# value; "$VAR/store" stays literal (a ./$VAR directory under cwd, silently),
# so the conf reads the full store path from one variable.
export DIRECTOR_GLOBAL_STORE="$DIRECTOR_GLOBAL_HOME/store"
mkdir -p "$DIRECTOR_GLOBAL_HOME"/{store,keys,log}
chmod 700 "$DIRECTOR_GLOBAL_HOME/keys"
conf="$DIRECTOR_GLOBAL_HOME/nats-server.conf"
[[ -s "$conf" ]] || { echo "missing $conf (copy hub/nats-server.conf and paste the public keys)" >&2; exit 1; }
nats-server --config "$conf" -t
# Provision once the server answers, so a hub restart applies the declared
# retention (168h streams, 192h director durables) with no operator step.
# provision.sh only raises, and it uses the admin key under $DIRECTOR_GLOBAL_HOME/keys
# that the header above names. It runs in the background, started before the
# exec below, so the server is still this script's process; it retries while the
# server comes up. A provision that never succeeds logs one line to
# log/provision.log and never stops the hub from serving: start does not regress.
# Needs jq (provision.sh reads the stream and durable configs with it); without
# it the hub starts anyway and the skip is logged. DIRECTOR_GLOBAL_PROVISION and
# DIRECTOR_PROVISION_TRIES exist so verify-start-provision.sh can stand in a
# failing provision and a short retry window.
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
provision="${DIRECTOR_GLOBAL_PROVISION:-$here/provision.sh}"
tries="${DIRECTOR_PROVISION_TRIES:-30}"
plog="$DIRECTOR_GLOBAL_HOME/log/provision.log"
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
    printf '%s provision failed after %s tries (last exit %s); the hub keeps serving\n' "$(stamp)" "$tries" "$rc" >> "$plog"
  ) > /dev/null 2>&1 &
else
  printf '%s jq not on PATH: provision skipped, the hub starts without it\n' "$(stamp)" >> "$plog"
fi
exec nats-server --config "$conf" --pid "$DIRECTOR_GLOBAL_HOME/nats-server.pid" \
  --log "$DIRECTOR_GLOBAL_HOME/log/nats-server.log"
