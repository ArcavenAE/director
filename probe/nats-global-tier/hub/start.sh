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
exec nats-server --config "$conf" --pid "$DIRECTOR_GLOBAL_HOME/nats-server.pid" \
  --log "$DIRECTOR_GLOBAL_HOME/log/nats-server.log"
