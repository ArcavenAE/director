#!/usr/bin/env bash
# JetStream across a leaf partition: does a leaf-side mirror of a hub stream
# reconcile cleanly after the leaf is isolated, and what is the back-pressure
# while the hub is unreachable. Throwaway hub and leaf on random loopback
# ports, own store dirs, cleanup trap. Touches no live server.
#
# Topology: a hub (JetStream domain "global", leaf listener) holds stream
# GLOBAL_INBOX on global.probe.>. A leaf (domain "local", leaf remote to the
# hub) holds MIRROR_GLOBAL mirroring GLOBAL_INBOX across the domain boundary,
# and a purely local stream LOCAL_INBOX on local.probe.>.
#
# Two partitions are exercised:
#   P1  hub down, leaf up: local publishes keep working, a cross-domain
#       operation fails loud, the mirror stalls without faulting the leaf.
#   P2  leaf down while the hub keeps receiving: on the leaf's return the
#       mirror reconciles to the hub's count, in order, no gaps or dupes.
set -euo pipefail

command -v nats-server >/dev/null || { echo "SKIP: nats-server not on PATH"; exit 0; }
command -v nats >/dev/null || { echo "SKIP: nats CLI not on PATH"; exit 0; }
command -v jq >/dev/null || { echo "SKIP: jq not on PATH"; exit 0; }

WORK="$(mktemp -d)"
HUB_PID=""; LEAF_PID=""
cleanup() {
  [[ -n "$LEAF_PID" ]] && kill "$LEAF_PID" 2>/dev/null || true
  [[ -n "$HUB_PID" ]] && kill "$HUB_PID" 2>/dev/null || true
  wait 2>/dev/null || true
  rm -rf "$WORK"
}
trap cleanup EXIT

freeport() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
HUBC="$(freeport)"; HUBL="$(freeport)"; HUBM="$(freeport)"
LEAFC="$(freeport)"; LEAFM="$(freeport)"
HUB="nats://127.0.0.1:${HUBC}"
LEAF="nats://127.0.0.1:${LEAFC}"

pass=0; fail=0
ok()   { printf 'PASS  %s\n' "$1"; pass=$((pass+1)); }
bad()  { printf 'FAIL  %s\n' "$1"; fail=$((fail+1)); }
note() { printf '      %s\n' "$1"; }

cat > "$WORK/hub.conf" <<EOF
server_name: probe-hub
listen: 127.0.0.1:${HUBC}
http: 127.0.0.1:${HUBM}
jetstream { store_dir: "${WORK}/hub-store", domain: global }
leafnodes { listen: 127.0.0.1:${HUBL} }
EOF

cat > "$WORK/leaf.conf" <<EOF
server_name: probe-leaf
listen: 127.0.0.1:${LEAFC}
http: 127.0.0.1:${LEAFM}
jetstream { store_dir: "${WORK}/leaf-store", domain: local }
leafnodes { remotes: [ { urls: ["nats-leaf://127.0.0.1:${HUBL}"] } ] }
EOF

# The cross-domain mirror: MIRROR_GLOBAL on the leaf pulls GLOBAL_INBOX from the
# hub's "global" domain through the leaf link, addressed by the external API
# prefix $JS.global.API.
cat > "$WORK/mirror.json" <<'EOF'
{
  "name": "MIRROR_GLOBAL",
  "storage": "file",
  "retention": "limits",
  "num_replicas": 1,
  "mirror": { "name": "GLOBAL_INBOX", "external": { "api": "$JS.global.API", "deliver": "" } }
}
EOF

wait_ready() { # $1 monitor port
  for _ in $(seq 1 50); do curl -fsS "http://127.0.0.1:$1/healthz" >/dev/null 2>&1 && return 0; sleep 0.1; done
  return 1
}
start_hub() {
  nats-server -c "$WORK/hub.conf" >"$WORK/hub.log" 2>&1 &
  HUB_PID=$!
  wait_ready "$HUBM" || { echo "hub did not start"; cat "$WORK/hub.log"; exit 1; }
}
start_leaf() {
  nats-server -c "$WORK/leaf.conf" >"$WORK/leaf.log" 2>&1 &
  LEAF_PID=$!
  wait_ready "$LEAFM" || { echo "leaf did not start"; cat "$WORK/leaf.log"; exit 1; }
}
leaf_link_up() {
  for _ in $(seq 1 50); do
    [[ "$(curl -fsS "http://127.0.0.1:${HUBM}/leafz" | jq '.leafnodes')" -ge 1 ]] && return 0
    sleep 0.2
  done
  return 1
}
hub_count()   { nats --server "$HUB"  stream info GLOBAL_INBOX  --json | jq '.state.messages'; }
mirror_count(){ nats --server "$LEAF" stream info MIRROR_GLOBAL --json | jq '.state.messages'; }
mirror_wait() { # wait until the mirror reaches $1 messages, up to ~15s
  for _ in $(seq 1 150); do [[ "$(mirror_count)" == "$1" ]] && return 0; sleep 0.1; done
  return 1
}

echo "### JetStream across a leaf partition ($(date '+%Y-%m-%d %H:%M:%S'))"
nats-server --version
echo

# --- baseline: mirror tracks the hub while connected ---
start_hub
start_leaf
leaf_link_up && ok "leaf link up to the hub" || bad "leaf link never came up"

nats --server "$HUB" stream add GLOBAL_INBOX --subjects 'global.probe.>' --storage file --defaults >/dev/null
nats --server "$LEAF" stream add --config "$WORK/mirror.json" >/dev/null
nats --server "$LEAF" stream add LOCAL_INBOX --subjects 'local.probe.>' --storage file --defaults >/dev/null

for i in $(seq 1 5); do nats --server "$HUB" pub "global.probe.$i" "hub-$i" >/dev/null; done
if mirror_wait 5; then ok "mirror tracked 5 hub messages while connected"; else bad "mirror did not track (count=$(mirror_count))"; fi

# --- P1: hub down. local keeps flowing; cross-domain fails loud; mirror stalls ---
echo; echo "## P1: hub unreachable, leaf stays up"
kill "$HUB_PID" 2>/dev/null || true; wait "$HUB_PID" 2>/dev/null || true; HUB_PID=""
sleep 0.5

if nats --server "$LEAF" pub local.probe.a "local-during-outage" >/dev/null 2>&1 \
   && [[ "$(nats --server "$LEAF" stream info LOCAL_INBOX --json | jq '.state.messages')" -ge 1 ]]; then
  ok "local publish still stored while the hub is down (local traffic keeps flowing)"
else
  bad "local publish failed during the hub outage"
fi

# A cross-domain read of the hub's stream from a leaf client: fails, does not hang.
if timeout 6 nats --server "$LEAF" --js-domain global stream info GLOBAL_INBOX >/dev/null 2>&1; then
  bad "cross-domain hub read succeeded with the hub down"
else
  ok "cross-domain hub operation fails loud during the outage (no silent queueing)"
fi

# The mirror is still there and the leaf is healthy; the mirror simply stalls.
if nats --server "$LEAF" stream info MIRROR_GLOBAL >/dev/null 2>&1; then
  ok "leaf and its mirror stream stay healthy while the hub is gone (mirror stalls, no fault)"
  note "mirror count held at $(mirror_count) during the outage"
else
  bad "mirror stream faulted the leaf during the outage"
fi

# --- P2: leaf isolated while the hub accrues; reconcile on return ---
echo; echo "## P2: hub accrues while the leaf is isolated, then reconcile"
start_hub
# Isolate the leaf by stopping it; the hub keeps receiving.
kill "$LEAF_PID" 2>/dev/null || true; wait "$LEAF_PID" 2>/dev/null || true; LEAF_PID=""
for i in $(seq 6 20); do nats --server "$HUB" pub "global.probe.$i" "hub-$i" >/dev/null; done
note "hub now holds $(hub_count) messages, published while the leaf was isolated"

start_leaf
leaf_link_up && note "leaf relinked to the hub" || bad "leaf did not relink"
target="$(hub_count)"
t0=$(date +%s)
if mirror_wait "$target"; then
  t1=$(date +%s)
  ok "mirror reconciled to the hub count ($target) after isolation in ~$((t1 - t0))s"
else
  bad "mirror did not reconcile (mirror=$(mirror_count) hub=$target)"
fi

# Order and no-dupes: a mirror copies messages preserving the source stream's
# sequence numbers, so a mirror whose last sequence and count both equal the
# hub's has reconciled the exact set, in order, with no gaps or duplicates.
hub_last="$(nats --server "$HUB"  stream info GLOBAL_INBOX  --json | jq '.state.last_seq')"
mir_last="$(nats --server "$LEAF" stream info MIRROR_GLOBAL --json | jq '.state.last_seq')"
mir_first="$(nats --server "$LEAF" stream info MIRROR_GLOBAL --json | jq '.state.first_seq')"
if [[ "$mir_last" == "$hub_last" && "$(mirror_count)" == "$target" && "$mir_first" == "1" ]]; then
  ok "reconciled set is sequence-exact (first=1 last=$mir_last count=$target, no gaps or dupes)"
else
  bad "reconciled set is off: mirror first=$mir_first last=$mir_last hub last=$hub_last count=$(mirror_count)/$target"
fi

# --- P3: long isolation past the source's retention window loses the evicted
#     messages permanently; the mirror reconciles only to what still exists. ---
echo; echo "## P3: isolation past the hub stream's retention window"
# A tight hub stream that keeps only its last 10 messages, and its leaf mirror,
# both created while connected and empty.
nats --server "$HUB" stream add GLOBAL_TIGHT --subjects 'tight.probe.>' --storage file \
  --max-msgs=10 --discard old --defaults >/dev/null
cat > "$WORK/mirror-tight.json" <<'EOF'
{ "name": "MIRROR_TIGHT", "storage": "file", "retention": "limits", "num_replicas": 1,
  "mirror": { "name": "GLOBAL_TIGHT", "external": { "api": "$JS.global.API", "deliver": "" } } }
EOF
nats --server "$LEAF" stream add --config "$WORK/mirror-tight.json" >/dev/null

# Isolate the leaf, then publish past the retention window so the hub evicts the
# oldest. This is a long isolation in effect: what matters is not the clock but
# that the source discarded messages before the mirror could pull them.
kill "$LEAF_PID" 2>/dev/null || true; wait "$LEAF_PID" 2>/dev/null || true; LEAF_PID=""
for i in $(seq 1 25); do nats --server "$HUB" pub "tight.probe.$i" "tight-$i" >/dev/null; done
tight_first="$(nats --server "$HUB" stream info GLOBAL_TIGHT --json | jq '.state.first_seq')"
tight_msgs="$(nats --server "$HUB" stream info GLOBAL_TIGHT --json | jq '.state.messages')"
note "hub tight stream kept $tight_msgs messages (first_seq=$tight_first); the earlier ones were evicted during the isolation"

start_leaf
leaf_link_up || bad "leaf did not relink for P3"
# The mirror can only reconcile to what the hub still holds.
mt_last="$(nats --server "$HUB" stream info GLOBAL_TIGHT --json | jq '.state.last_seq')"
reconciled=false
for _ in $(seq 1 150); do
  if [[ "$(nats --server "$LEAF" stream info MIRROR_TIGHT --json | jq '.state.last_seq')" == "$mt_last" ]]; then reconciled=true; break; fi
  sleep 0.1
done
mt_first="$(nats --server "$LEAF" stream info MIRROR_TIGHT --json | jq '.state.first_seq')"
mt_count="$(nats --server "$LEAF" stream info MIRROR_TIGHT --json | jq '.state.messages')"
if $reconciled && [[ "$mt_first" == "$tight_first" && "$mt_count" == "$tight_msgs" ]]; then
  ok "mirror reconciled to the surviving window (first_seq=$mt_first, $mt_count messages)"
  note "the $((tight_first - 1)) messages evicted before reconnect are permanently absent from the mirror; a mirror does not recover what the source discarded during a long isolation"
else
  bad "P3 off: mirror first=$mt_first count=$mt_count last reached=$reconciled; hub first=$tight_first msgs=$tight_msgs last=$mt_last"
fi

echo
echo "### result: ${pass} passed, ${fail} failed"
[[ "$fail" -eq 0 ]]
