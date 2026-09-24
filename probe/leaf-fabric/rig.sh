#!/usr/bin/env bash
# Scratch rig for design brief 11 (the leaf fabric): one hub (domain ghub) and
# two leaf clusters (domains pa, pb), each its own nats-server with its own
# ports and store dir under FAB_HOME. Never touches a live broker: every port
# is drawn at random above 20000 and refused if it equals a live fleet port.
#
#   FAB_HOME=<dir> rig.sh up        write confs, start all three, create streams
#   FAB_HOME=<dir> rig.sh down      stop all three (store dirs kept for reading)
#   FAB_HOME=<dir> rig.sh kill <n>  SIGKILL one server (hub|pa|pb)
#   FAB_HOME=<dir> rig.sh start <n> start one server again from its conf
#   FAB_HOME=<dir> rig.sh url <n>   print a server's client url
#
# Subject root: mail./out. per brief 11 section 2.1. The root choice (mail.
# versus agent.<cluster>.) is pending an operator ruling; the probe does not
# depend on it beyond not overlapping a live stream.
set -euo pipefail

: "${FAB_HOME:?set FAB_HOME to a scratch directory}"
LIVE_PORTS=" 4222 4242 7442 8222 8242 "
FAB_MAX_AGE="${FAB_MAX_AGE:-2419200000000000}" # 28 days in ns
FAB_DUP="${FAB_DUP:-1200000000000}"            # 20 minute dedupe window

free_port() {
  local p
  while :; do
    p=$(( 20000 + RANDOM % 20000 ))
    [[ "$LIVE_PORTS" == *" $p "* ]] && continue
    if ! nc -z 127.0.0.1 "$p" 2>/dev/null; then echo "$p"; return; fi
  done
}

ports() { cat "$FAB_HOME/ports.env"; }
load() { source "$FAB_HOME/ports.env"; }

write_confs() {
  mkdir -p "$FAB_HOME"/{hub,pa,pb}
  {
    echo "HUB_C=$(free_port)"; echo "HUB_L=$(free_port)"; echo "HUB_M=$(free_port)"
    echo "PA_C=$(free_port)"; echo "PA_M=$(free_port)"
    echo "PB_C=$(free_port)"; echo "PB_M=$(free_port)"
  } > "$FAB_HOME/ports.env"
  load
  cat > "$FAB_HOME/hub/nats.conf" <<EOF
server_name: fab-hub
host: 127.0.0.1
port: $HUB_C
http: 127.0.0.1:$HUB_M
jetstream { store_dir: "$FAB_HOME/hub/store", domain: ghub }
leafnodes { host: 127.0.0.1, port: $HUB_L }
EOF
  for c in pa pb; do
    local cp mp
    if [[ $c == pa ]]; then cp=$PA_C mp=$PA_M; else cp=$PB_C mp=$PB_M; fi
    cat > "$FAB_HOME/$c/nats.conf" <<EOF
server_name: fab-$c
host: 127.0.0.1
port: $cp
http: 127.0.0.1:$mp
jetstream { store_dir: "$FAB_HOME/$c/store", domain: $c }
leafnodes {
  reconnect: 1
  remotes: [ {
    urls: ["nats-leaf://127.0.0.1:$HUB_L"]
    # Raw mail and outbox subjects never cross the link (brief 11 2.3);
    # only the sourcing API, delivery, and flow-control traffic does.
    deny_exports: ["mail.>", "out.>"]
    deny_imports: ["mail.>", "out.>"]
  } ]
}
EOF
  done
}

start() {
  local n=$1
  nats-server -c "$FAB_HOME/$n/nats.conf" -P "$FAB_HOME/$n/pid" -l "$FAB_HOME/$n/log" >/dev/null 2>&1 &
  disown
  load
  local m
  case $n in hub) m=$HUB_M;; pa) m=$PA_M;; pb) m=$PB_M;; esac
  for _ in $(seq 1 50); do curl -sf "http://127.0.0.1:$m/healthz" >/dev/null && return 0; sleep 0.2; done
  echo "rig: $n did not come up" >&2; return 1
}

kill_one() { kill -9 "$(cat "$FAB_HOME/$1/pid")" 2>/dev/null || true; }

url() {
  load
  case $1 in hub) echo "nats://127.0.0.1:$HUB_C";; pa) echo "nats://127.0.0.1:$PA_C";; pb) echo "nats://127.0.0.1:$PB_C";; esac
}

stream_json() { # name subjects-json retention extra-json
  jq -n --arg n "$1" --argjson s "$2" --arg r "$3" --argjson x "$4" \
    --argjson age "$FAB_MAX_AGE" --argjson dup "$FAB_DUP" '{
      name:$n, subjects:$s, retention:$r, storage:"file", num_replicas:1,
      max_age:$age, duplicate_window:$dup, discard:"new", max_bytes:67108864,
      max_msgs:-1, max_consumers:-1, max_msg_size:-1, allow_direct:true
    } + $x'
}

add_stream() { # server json
  local f; f=$(mktemp "$FAB_HOME/stream.XXXX")
  echo "$2" > "$f"
  nats --server "$(url "$1")" stream add --config "$f" >/dev/null
  rm -f "$f"
}

streams() {
  for c in pa pb; do
    local other; [[ $c == pa ]] && other=pb || other=pa
    add_stream "$c" "$(stream_json OUTBOX '["out.>"]' workqueue '{}')"
    add_stream "$c" "$(stream_json INBOX "[\"mail.$c.>\"]" workqueue "$(jq -n --arg o "$other" --arg c "$c" '{
      sources:[{name:"OUTBOX", external:{api:("$JS."+$o+".API")},
        subject_transforms:[{src:("out."+$c+".>"), dest:("mail."+$c+".>")}]}]}')")"
  done
  add_stream hub "$(stream_json DIRECTOR_INBOX '["director.inbox"]' workqueue "$(jq -n '{
    sources:[ ("pa","pb") | {name:"OUTBOX", external:{api:("$JS."+.+".API")},
      subject_transforms:[{src:"out.director.inbox", dest:"director.inbox"}]}]}')")"
}

case "${1:-}" in
  up) write_confs; start hub; start pa; start pb; sleep 2; streams; ports ;;
  down) for n in pa pb hub; do [[ -f "$FAB_HOME/$n/pid" ]] && kill "$(cat "$FAB_HOME/$n/pid")" 2>/dev/null || true; done ;;
  kill) kill_one "$2" ;;
  start) start "$2" ;;
  url) url "$2" ;;
  *) sed -n '2,12p' "$0"; exit 2 ;;
esac
