#!/usr/bin/env bash
# Prove the global tier with two throwaway leaf brokers on one host against a
# running hub (default 127.0.0.1:7442 for leaves, 127.0.0.1:4242 for admin).
# Nothing here touches the phase-0 broker on :4222. Exit nonzero on any miss.
# Run as the hub operator (needs the leaf seeds and the admin seed).
set -euo pipefail
HOME_DIR="${DIRECTOR_GLOBAL_HOME:-$HOME/.director/nats-global}"
HUB_LEAF="${HUB_LEAF:-127.0.0.1:7442}"
HUB_URL="${HUB_URL:-nats://127.0.0.1:4242}"
work="$(mktemp -d)"; pids=()
cleanup() { for p in "${pids[@]:-}"; do kill "$p" 2>/dev/null || true; done; rm -rf "$work"; }
trap cleanup EXIT
start_leaf() { # name port
  mkdir -p "$work/$1/store"
  cat > "$work/$1/nats.conf" <<CONF
server_name: verify-$1
listen: 127.0.0.1:$2
jetstream { store_dir: "$work/$1/store", domain: verify$1 }
leafnodes { remotes: [ { urls: ["nats-leaf://$HUB_LEAF"], nkey: \$LEAF_NKEY } ] }
CONF
  LEAF_NKEY="$(grep -m1 '^SU' "$HOME_DIR/keys/leaf-$1.nk")" nats-server -c "$work/$1/nats.conf" -l "$work/$1/log" &
  pids+=($!)
}
start_leaf kinu 4262; start_leaf mokuzai 4263; sleep 2
K=(nats -s nats://127.0.0.1:4262 --js-domain global)
M=(nats -s nats://127.0.0.1:4263 --js-domain global)
A=(nats -s "$HUB_URL" --nkey "$HOME_DIR/keys/admin.nk")
pass=0; fail=0
ok()   { echo "PASS $1"; pass=$((pass+1)); }
bad()  { echo "FAIL $1"; fail=$((fail+1)); }
n="$RANDOM"
grep -q 'JetStream using domains: local "verifymokuzai", remote "global"' "$work/mokuzai/log" && ok "leaf link up with distinct domains" || bad "leaf link"
"${M[@]}" kv put GLOBAL_PRESENCE "presence.mokuzai.supervisor.V$n" '{"role":"supervisor"}' >/dev/null 2>&1 \
  && "${K[@]}" kv get GLOBAL_PRESENCE "presence.mokuzai.supervisor.V$n" --raw 2>/dev/null | grep -q supervisor \
  && ok "presence written from mokuzai, read from kinu" || bad "presence across the hub"
"${M[@]}" consumer add GLOBAL_TO_mokuzai "v$n" --pull --deliver new --ack explicit --filter global.mokuzai.supervisor.inbox --defaults >/dev/null 2>&1 || true
"${K[@]}" pub global.mokuzai.supervisor.inbox "N1-$n" --jetstream >/dev/null 2>&1 \
  && "${M[@]}" consumer next GLOBAL_TO_mokuzai "v$n" --raw --timeout 5s 2>/dev/null | grep -q "N1-$n" \
  && ok "director to supervisor across the hub" || bad "director to supervisor"
"${K[@]}" consumer add GLOBAL_TO_DIRECTOR "v$n" --pull --deliver new --ack explicit --filter global.director.inbox --defaults >/dev/null 2>&1 || true
"${M[@]}" pub global.director.inbox "N2-$n" --jetstream >/dev/null 2>&1 \
  && "${K[@]}" consumer next GLOBAL_TO_DIRECTOR "v$n" --raw --timeout 5s 2>/dev/null | grep -q "N2-$n" \
  && ok "supervisor to director across the hub" || bad "supervisor to director"
before="$("${A[@]}" stream info GLOBAL_TO_kinu 2>/dev/null | awk '/^ +Messages:/{print $2}')"
if "${M[@]}" pub global.kinu.supervisor.inbox FORGED --jetstream --timeout 3s >/dev/null 2>&1; then bad "forged cluster prefix accepted"; else
  after="$("${A[@]}" stream info GLOBAL_TO_kinu 2>/dev/null | awk '/^ +Messages:/{print $2}')"
  [[ "$before" == "$after" ]] && ok "forged cluster prefix refused, nothing stored" || bad "forged prefix stored"; fi
if "${M[@]}" consumer add GLOBAL_TO_DIRECTOR spy --pull --deliver all --ack explicit --defaults --timeout 3s >/dev/null 2>&1; then bad "mokuzai read the director stream"; else ok "mokuzai cannot consume the director stream"; fi
if "${M[@]}" pub global.director.escalation X --jetstream --timeout 3s >/dev/null 2>&1; then bad "subject outside allow accepted"; else ok "subject outside allow refused"; fi
hubin="$(curl -s 127.0.0.1:8242/varz | jq .in_msgs)"; nats -s nats://127.0.0.1:4263 pub agent.x.y.z.inbox LOCAL >/dev/null 2>&1
[[ "$(curl -s 127.0.0.1:8242/varz | jq .in_msgs)" == "$hubin" ]] && ok "local publish never reaches the hub" || bad "local traffic crossed"
"${A[@]}" consumer rm GLOBAL_TO_mokuzai "v$n" -f >/dev/null 2>&1 || true; "${A[@]}" consumer rm GLOBAL_TO_DIRECTOR "v$n" -f >/dev/null 2>&1 || true
echo "$pass passed, $fail failed"; [[ $fail -eq 0 ]]
