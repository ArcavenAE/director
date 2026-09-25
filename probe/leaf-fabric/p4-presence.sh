#!/usr/bin/env bash
# P4: seat-keyed presence per cluster, sourced into a fleet view on the hub
# with its own short age; restart, compare-and-set, delete, and link-cut aging.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
PA=$("$here/rig.sh" url pa); PB=$("$here/rig.sh" url pb); HUB=$("$here/rig.sh" url hub)
FLEET_AGE=${FLEET_AGE:-20s}
for s in "$PA" "$PB"; do nats --server "$s" kv add PRESENCE --history 1 --ttl 60s >/dev/null 2>&1 || true; done
age_ns=$(( ${FLEET_AGE%s} * 1000000000 ))
cfg=$(mktemp)
jq -n --argjson age "$age_ns" '{
  name:"KV_FLEET_PRESENCE", subjects:["$KV.FLEET_PRESENCE.>"], storage:"file",
  num_replicas:1, retention:"limits", max_msgs_per_subject:1, max_age:$age,
  discard:"new", allow_rollup_hdrs:true, deny_delete:true, allow_direct:true,
  max_msgs:-1, max_bytes:-1, max_consumers:-1, max_msg_size:-1,
  sources:[ ("pa","pb") | {name:"KV_PRESENCE", external:{api:("$JS."+.+".API")},
    subject_transforms:[{src:"$KV.PRESENCE.>", dest:"$KV.FLEET_PRESENCE.>"}]} ]}' > "$cfg"
nats --server "$HUB" stream add --config "$cfg" >/dev/null 2>&1 || true; rm -f "$cfg"
keys() { nats --server "$1" kv ls "$2" 2>/dev/null | sort | tr '\n' ' '; }

echo "== restart the pa seat's shim five times (same seat key, new instance each time)"
for i in 1 2 3 4 5; do nats --server "$PA" kv put PRESENCE pa.w.t.x "{\"instance\":\"i$i\"}" >/dev/null; done
nats --server "$PB" kv put PRESENCE pb.w.t.y '{"instance":"j1"}' >/dev/null
sleep 3
echo "pa keys:  $(keys "$PA" PRESENCE)"
echo "hub keys: $(keys "$HUB" FLEET_PRESENCE)"
echo "hub value for pa.w.t.x: $(nats --server "$HUB" kv get FLEET_PRESENCE pa.w.t.x --raw 2>&1)"

echo "== compare-and-set: the holder updates at its revision; a stale second writer is refused"
rev=$(nats --server "$PA" kv get PRESENCE pa.w.t.x 2>/dev/null | sed -n 's/.*revision: \([0-9]*\).*/\1/p' | head -1)
echo "holder update at rev $rev: $(nats --server "$PA" kv update PRESENCE pa.w.t.x '{"instance":"i5"}' "$rev" 2>&1 | tail -1)"
echo "rival update at stale rev $rev: $(nats --server "$PA" kv update PRESENCE pa.w.t.x '{"instance":"rival"}' "$rev" 2>&1 | tail -1)"

echo "== delete propagates"
nats --server "$PB" kv put PRESENCE pb.w.t.gone '{"instance":"g"}' >/dev/null; sleep 2
echo "hub before delete: $(keys "$HUB" FLEET_PRESENCE)"
nats --server "$PB" kv del PRESENCE pb.w.t.gone -f >/dev/null; sleep 2
echo "hub after delete:  $(keys "$HUB" FLEET_PRESENCE)"

echo "== link cut ages a cluster out of the fleet view; pb keeps heartbeating"
( for i in $(seq 1 15); do nats --server "$PB" kv put PRESENCE pb.w.t.y "{\"instance\":\"j1\",\"beat\":$i}" >/dev/null 2>&1; sleep 2; done ) &
hb=$!
"$here/rig.sh" kill pa
echo "pa killed $(date -u +%T); fleet age $FLEET_AGE"
sleep $(( ${FLEET_AGE%s} + 6 ))
echo "hub keys after $(( ${FLEET_AGE%s} + 6 ))s: $(keys "$HUB" FLEET_PRESENCE)"
wait "$hb"
"$here/rig.sh" start pa
