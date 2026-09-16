#!/usr/bin/env bash
# Prove server TLS on the hub's client and leaf listeners (the infra component's
# default shape, brought forward from the cloud phase: global-bus-tier.md
# section 9, aae-orc-4vx98) on a SCRATCH hub, end to end on the leaf link and
# on the shim's global-mode client path.
#
# Isolation: it never reads or writes ~/.director/nats-global. It starts its
# own hub and its own leaf broker in a temp dir, on ports it picks as free at
# run time, with a per-run cluster token, fresh NKeys, and a CA it generates.
# The live hub on 4242/7442 carries the director bus and is not touched; the
# cutover of that hub is a separate, operator-gated step (the finding carries
# the config diff and the rollback).
#
# What it proves (exit nonzero on any miss):
#   client listener:  a client with the CA connects over TLS; a client with
#                     the system roots or a CA that did not sign the hub cert
#                     is refused; a plaintext client is refused
#   leaf listener:    a leaf remote on tls:// with ca_file establishes; a leaf
#                     carrying a CA that did not sign the hub cert does not
#   shim:             director-mcp --preflight in global mode passes through
#                     the TLS leaf link; a message published on the leaf side
#                     lands in the hub stream over that link
#   cutover mechanics: whether a leaf can carry its tls block ahead of the hub;
#                     the hub taking its tls blocks by config reload; the
#                     leaf re-linking after its own reload; the rollback by
#                     the same path; allow_non_tls on the client listener
#
# Usage, from anywhere:
#   probe/nats-global-tier/verify-global-tls.sh
# Knobs: HUB_NAME (SAN, default global-hub), HUB_LAN_IP (SAN, default the
# first 192.168 address on this host), KEEP=1 keeps the work dir.
set -euo pipefail

command -v nats-server >/dev/null || { echo "nats-server not on PATH"; exit 2; }
command -v nats >/dev/null || { echo "nats CLI not on PATH"; exit 2; }
command -v openssl >/dev/null || { echo "openssl not on PATH"; exit 2; }
command -v jq >/dev/null || { echo "jq not on PATH"; exit 2; }
command -v go >/dev/null || { echo "go not on PATH"; exit 2; }

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SHIM_SRC="$here/../nats-phase-0/director-mcp"
HUB_NAME="${HUB_NAME:-global-hub}"
HUB_LAN_IP="${HUB_LAN_IP:-$(ifconfig 2>/dev/null | awk '/inet 192\.168\./{print $2; exit}')}"
[[ -n "$HUB_LAN_IP" ]] || HUB_LAN_IP=127.0.0.1

work="$(mktemp -d "${TMPDIR:-/tmp}/tlstrial.XXXXXX")"
NONCE="$(printf '%04x' $RANDOM)"
CLUSTER="tlstrial$NONCE"
DOMAIN="g$NONCE"
pids=()

pick_port() {
  local p
  while :; do
    p=$((20000 + RANDOM % 20000))
    if ! lsof -nP -iTCP:"$p" -sTCP:LISTEN >/dev/null 2>&1; then echo "$p"; return; fi
  done
}
HUB_CLIENT="$(pick_port)"; HUB_LEAF="$(pick_port)"; HUB_MON="$(pick_port)"
LEAF_CLIENT="$(pick_port)"; LEAF2_CLIENT="$(pick_port)"; LEAF3_CLIENT="$(pick_port)"
PLAIN_CLIENT="$(pick_port)"; PLAIN_LEAF="$(pick_port)"; MIX_CLIENT="$(pick_port)"; MIX_LEAF="$(pick_port)"

cleanup() {
  for p in "${pids[@]:-}"; do kill "$p" 2>/dev/null || true; done
  if [[ "${KEEP:-0}" == 1 ]]; then echo "kept $work"; else rm -rf "$work"; fi
}
trap cleanup EXIT

pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

echo "scratch hub: client $HUB_CLIENT leaf $HUB_LEAF monitor $HUB_MON; cluster $CLUSTER domain $DOMAIN; work $work"

# --- keys and certificates ----------------------------------------------------
mkdir -p "$work"/{keys,tls,hub/store,leaf/store,leaf2/store,leaf3/store,plain/store,mix/store}
chmod 700 "$work/keys"
for k in admin director "leaf-$CLUSTER"; do nats auth nkey gen user > "$work/keys/$k.nk"; chmod 600 "$work/keys/$k.nk"; done
pub() { nats auth nkey show "$work/keys/$1.nk"; }
ADMIN_PUB="$(pub admin)"; DIRECTOR_PUB="$(pub director)"; LEAF_PUB="$(pub "leaf-$CLUSTER")"
LEAF_SEED="$(grep -m1 '^SU' "$work/keys/leaf-$CLUSTER.nk")"

# The CA that signs the hub, and a second CA that signs nothing the hub uses.
mkcert() { # dir cn-suffix
  local d="$1" tag="$2"
  openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 3650 \
    -subj "/CN=director fleet CA $tag" -keyout "$d/ca-key.pem" -out "$d/ca.pem" \
    -addext "basicConstraints=critical,CA:TRUE" -addext "keyUsage=critical,keyCertSign,cRLSign" >/dev/null 2>&1
  openssl req -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes \
    -subj "/CN=$HUB_NAME" -keyout "$d/hub-key.pem" -out "$d/hub.csr" >/dev/null 2>&1
  printf 'subjectAltName=DNS:%s,DNS:%s,DNS:localhost,IP:%s,IP:127.0.0.1\nextendedKeyUsage=serverAuth\nkeyUsage=critical,digitalSignature\n' \
    "$HUB_NAME" "$(hostname -s)" "$HUB_LAN_IP" > "$d/san.ext"
  openssl x509 -req -in "$d/hub.csr" -CA "$d/ca.pem" -CAkey "$d/ca-key.pem" -CAcreateserial \
    -days 825 -extfile "$d/san.ext" -out "$d/hub.pem" >/dev/null 2>&1
  chmod 600 "$d"/*-key.pem
}
mkdir -p "$work/tls/real" "$work/tls/rogue"
mkcert "$work/tls/real" "$NONCE"
mkcert "$work/tls/rogue" "rogue-$NONCE"
openssl x509 -in "$work/tls/real/hub.pem" -noout -ext subjectAltName | grep -q "IP Address:$HUB_LAN_IP" \
  && ok "hub cert SAN covers $HUB_NAME, $(hostname -s), localhost, $HUB_LAN_IP, 127.0.0.1" \
  || bad "hub cert SAN" "$(openssl x509 -in "$work/tls/real/hub.pem" -noout -ext subjectAltName)"

# --- the scratch hub, TLS on both listeners -----------------------------------
# Same users, streams, and subjects as hub/nats-server.conf; only the tls
# blocks are new (design section 3: the cloud move is a reconfiguration).
hub_conf() { # out-file client-port leaf-port mon-port tls-dir extra-top-level
  local out="$1" cp="$2" lp="$3" mp="$4" tdir="$5" extra="${6:-}"
  {
    cat <<CONF
server_name: $HUB_NAME-$NONCE
listen: 127.0.0.1:$cp
http: 127.0.0.1:$mp
$extra
CONF
    if [[ -n "$tdir" ]]; then cat <<CONF
tls {
  cert_file: "$tdir/hub.pem"
  key_file: "$tdir/hub-key.pem"
  timeout: 2
}
CONF
    fi
    cat <<CONF
jetstream {
  store_dir: "$work/hub/store"
  domain: $DOMAIN
  max_memory_store: 67108864
  max_file_store: 268435456
}
leafnodes {
  listen: 127.0.0.1:$lp
CONF
    if [[ -n "$tdir" ]]; then cat <<CONF
  tls {
    cert_file: "$tdir/hub.pem"
    key_file: "$tdir/hub-key.pem"
    timeout: 2
  }
CONF
    fi
    cat <<CONF
}
accounts {
  FLEET {
    jetstream: enabled
    users: [
      { nkey: $ADMIN_PUB }
      { nkey: $DIRECTOR_PUB
        permissions {
          publish   { allow: [ "global.*.supervisor.inbox", "global.director.>",
                               "\$JS.API.>", "\$JS.ACK.>", "\$KV.GLOBAL_PRESENCE.>", "_INBOX.>" ] }
          subscribe { allow: [ "global.director.>", "\$KV.GLOBAL_PRESENCE.>", "_INBOX.>" ] }
        } }
      { nkey: $LEAF_PUB
        permissions {
          publish   { allow: [ "global.director.inbox", "global.$CLUSTER.>",
                               "\$JS.$DOMAIN.API.CONSUMER.CREATE.GLOBAL_TO_$CLUSTER.>",
                               "\$JS.$DOMAIN.API.CONSUMER.DURABLE.CREATE.GLOBAL_TO_$CLUSTER.>",
                               "\$JS.$DOMAIN.API.CONSUMER.INFO.GLOBAL_TO_$CLUSTER.>",
                               "\$JS.$DOMAIN.API.CONSUMER.MSG.NEXT.GLOBAL_TO_$CLUSTER.>",
                               "\$JS.$DOMAIN.API.CONSUMER.DELETE.GLOBAL_TO_$CLUSTER.>",
                               "\$JS.$DOMAIN.API.STREAM.INFO.GLOBAL_TO_$CLUSTER",
                               "\$JS.$DOMAIN.API.STREAM.MSG.DELETE.GLOBAL_TO_$CLUSTER",
                               "\$JS.$DOMAIN.API.INFO",
                               "\$JS.$DOMAIN.API.\$KV.GLOBAL_PRESENCE.>",
                               "\$JS.$DOMAIN.API.STREAM.INFO.KV_GLOBAL_PRESENCE",
                               "\$JS.$DOMAIN.API.DIRECT.GET.KV_GLOBAL_PRESENCE.>",
                               "\$JS.$DOMAIN.API.STREAM.MSG.GET.KV_GLOBAL_PRESENCE",
                               "\$JS.$DOMAIN.API.CONSUMER.CREATE.KV_GLOBAL_PRESENCE.>",
                               "\$JS.$DOMAIN.API.CONSUMER.INFO.KV_GLOBAL_PRESENCE.>",
                               "\$JS.$DOMAIN.API.CONSUMER.DELETE.KV_GLOBAL_PRESENCE.>",
                               "\$JS.ACK.>" ] }
          subscribe { allow: [ "global.$CLUSTER.>", "_INBOX.>" ] }
        } }
    ]
  }
}
CONF
  } > "$out"
}
hub_conf "$work/hub/nats-server.conf" "$HUB_CLIENT" "$HUB_LEAF" "$HUB_MON" "$work/tls/real"
nats-server --config "$work/hub/nats-server.conf" -t >/dev/null 2>&1 \
  && ok "hub config with tls on both listeners passes nats-server -t" \
  || bad "hub config check" "$(nats-server --config "$work/hub/nats-server.conf" -t 2>&1)"
nats-server --config "$work/hub/nats-server.conf" -l "$work/hub/nats-server.log" &
pids+=($!)
sleep 1
varz="$(curl -s "127.0.0.1:$HUB_MON/varz")"
[[ "$(jq -r .tls_required <<<"$varz")" == true && "$(jq -r .leaf.tls_required <<<"$varz")" == true ]] \
  && ok "/varz reports tls_required on the client listener and on the leaf listener" \
  || bad "/varz tls_required" "client=$(jq -r .tls_required <<<"$varz") leaf=$(jq -r .leaf.tls_required <<<"$varz")"

# --- the client listener ------------------------------------------------------
HUBURL="tls://127.0.0.1:$HUB_CLIENT"
A=(nats -s "$HUBURL" --tlsca "$work/tls/real/ca.pem" --nkey "$work/keys/admin.nk")
D=(nats -s "$HUBURL" --tlsca "$work/tls/real/ca.pem" --nkey "$work/keys/director.nk")

if "${A[@]}" account info >/dev/null 2>"$work/admin.err"; then
  conn="$(curl -s "127.0.0.1:$HUB_MON/connz?state=closed" | jq -r '.connections[-1] | "\(.tls_version) \(.tls_cipher_suite)"')"
  [[ "$conn" == 1.* ]] \
    && ok "admin connects over TLS with the CA; /connz records TLS $conn" \
    || bad "/connz tls fields on the admin connection" "$conn"
else
  bad "admin over TLS with the CA" "$(cat "$work/admin.err")"
fi

if nats -s "$HUBURL" --nkey "$work/keys/admin.nk" account info >/dev/null 2>"$work/sysroots.err"; then
  bad "a client with the system roots connected" "a private CA must not be in the system trust"
else
  grep -q -i -E 'unknown authority|certificate' "$work/sysroots.err" \
    && ok "a client with the system roots is refused: $(grep -o -i -E 'x509:[^"]*' "$work/sysroots.err" | head -1)" \
    || bad "system-roots refusal reason" "$(cat "$work/sysroots.err")"
fi

if nats -s "$HUBURL" --tlsca "$work/tls/rogue/ca.pem" --nkey "$work/keys/admin.nk" account info >/dev/null 2>"$work/rogueca.err"; then
  bad "a client trusting a CA that did not sign the hub cert connected"
else
  grep -q -i 'unknown authority' "$work/rogueca.err" \
    && ok "a client trusting a CA that did not sign the hub cert is refused" \
    || bad "rogue-CA refusal reason" "$(cat "$work/rogueca.err")"
fi

# nats:// against a TLS-required listener: the client is told tls_required in
# INFO and upgrades, so the refusal is the CA again, not a protocol error. The
# point of the check is that no plaintext session exists on the wire.
if nats -s "nats://127.0.0.1:$HUB_CLIENT" --nkey "$work/keys/admin.nk" account info >/dev/null 2>"$work/plain.err"; then
  bad "a nats:// client without the CA connected"
else
  ok "a nats:// client without the CA is refused (the listener upgrades to TLS, then the CA check fails)"
fi

# Provision over TLS, as the admin NKey, the same streams and bucket as
# hub/provision.sh.
"${A[@]}" stream add GLOBAL_TO_DIRECTOR --subjects 'global.director.>' --storage file --retention limits \
  --max-age 24h --max-msg-size 65536 --dupe-window 2m --defaults >/dev/null
"${A[@]}" stream add "GLOBAL_TO_$CLUSTER" --subjects "global.$CLUSTER.>" --storage file --retention limits \
  --max-age 24h --max-msg-size 65536 --dupe-window 2m --defaults >/dev/null
"${A[@]}" kv add GLOBAL_PRESENCE --ttl 90s --storage file >/dev/null
"${D[@]}" stream info GLOBAL_TO_DIRECTOR >/dev/null 2>"$work/director.err" \
  && ok "the director NKey user reads its stream over TLS; users and streams are the live hub's" \
  || bad "director user over TLS" "$(cat "$work/director.err")"

# --- the leaf listener --------------------------------------------------------
leaf_conf() { # out-file client-port store domain-suffix hub-leaf-port ca-file url-scheme
  local out="$1" cp="$2" store="$3" dsuf="$4" hlp="$5" ca="$6" scheme="$7"
  cat > "$out" <<CONF
server_name: verify-tls-leaf-$dsuf-$NONCE
listen: 127.0.0.1:$cp
jetstream { store_dir: "$store", domain: leaf$dsuf$NONCE }
leafnodes {
  remotes: [
    { urls: ["$scheme://127.0.0.1:$hlp"], nkey: \$DIRECTOR_LEAF_NKEY,
      tls { ca_file: "$ca", timeout: 2 } }
  ]
}
CONF
}
leaf_conf "$work/leaf/nats.conf" "$LEAF_CLIENT" "$work/leaf/store" a "$HUB_LEAF" "$work/tls/real/ca.pem" tls
DIRECTOR_LEAF_NKEY="$LEAF_SEED" nats-server -c "$work/leaf/nats.conf" -l "$work/leaf/broker.log" &
pids+=($!)
sleep 2
if grep -q "JetStream using domains: local \"leafa$NONCE\", remote \"$DOMAIN\"" "$work/leaf/broker.log"; then
  leafz="$(curl -s "127.0.0.1:$HUB_MON/leafz")"
  [[ "$(jq -r .leafnodes <<<"$leafz")" == 1 && "$(jq -r '.leafs[0].name' <<<"$leafz")" == "verify-tls-leaf-a-$NONCE" ]] \
    && ok "leaf remote on tls:// with ca_file links; hub /leafz lists it" \
    || bad "hub /leafz after the TLS leaf linked" "$leafz"
else
  bad "TLS leaf link" "$(tail -5 "$work/leaf/broker.log")"
fi

leaf_conf "$work/leaf2/nats.conf" "$LEAF2_CLIENT" "$work/leaf2/store" b "$HUB_LEAF" "$work/tls/rogue/ca.pem" tls
DIRECTOR_LEAF_NKEY="$LEAF_SEED" nats-server -c "$work/leaf2/nats.conf" -l "$work/leaf2/broker.log" &
pids+=($!)
sleep 2
if grep -q -i 'unknown authority' "$work/leaf2/broker.log" \
   && [[ "$(curl -s "127.0.0.1:$HUB_MON/leafz" | jq -r .leafnodes)" == 1 ]]; then
  ok "a leaf trusting a CA that did not sign the hub cert never links: $(grep -o -i 'x509:[^"]*' "$work/leaf2/broker.log" | head -1)"
else
  bad "rogue-CA leaf should not link" "$(grep -i -E 'error|x509' "$work/leaf2/broker.log" | head -3)"
fi
kill "${pids[-1]}" 2>/dev/null || true

# --- the shim's global-mode client path ---------------------------------------
LOCAL=(nats -s "nats://127.0.0.1:$LEAF_CLIENT")
"${LOCAL[@]}" stream add AGENT_INBOX --subjects 'agent.*.*.*.inbox,agent.*.*.role.*.inbox' \
  --storage file --retention limits --max-age 24h --max-msg-size 65536 --dupe-window 2m --defaults >/dev/null
"${LOCAL[@]}" stream add AGENT_AUDIT --subjects 'agent.audit' --storage file --retention limits \
  --max-age 30d --defaults >/dev/null
"${LOCAL[@]}" kv add AGENT_STATE --ttl 90s >/dev/null

( cd "$SHIM_SRC" && go build -o "$work/director-mcp" . )
SHIM="$work/director-mcp"
AGENT="verifytls-$RANDOM"
shim_env=(
  "DIRECTOR_AGENT_ID=$AGENT" "DIRECTOR_TEAM=fleet" "DIRECTOR_WORKSPACE=verifyws"
  "NATS_URL=nats://127.0.0.1:$LEAF_CLIENT"
  "DIRECTOR_GLOBAL_DOMAIN=$DOMAIN" "DIRECTOR_CLUSTER=$CLUSTER" "DIRECTOR_GLOBAL_ROLE=supervisor"
)
env "${shim_env[@]}" "$SHIM" --preflight </dev/null >/dev/null 2>"$work/pf.err" \
  && ok "director-mcp --preflight in global mode passes through the TLS leaf link (domain $DOMAIN)" \
  || bad "shim preflight through the TLS leaf" "$(cat "$work/pf.err")"

if env "${shim_env[@]}" DIRECTOR_CLUSTER=nosuchcluster "$SHIM" --preflight </dev/null >/dev/null 2>"$work/pf2.err"; then
  bad "preflight passed for an unprovisioned cluster over TLS"
else
  grep -q "GLOBAL_TO_nosuchcluster" "$work/pf2.err" \
    && ok "preflight still refuses an unprovisioned cluster over the TLS link, naming the stream" \
    || bad "preflight refusal over TLS" "$(cat "$work/pf2.err")"
fi

# Data crosses the link: published on the leaf side, stored on the hub.
"${LOCAL[@]}" pub global.director.inbox "tls trial $NONCE" >/dev/null 2>&1
sleep 1
stored="$("${A[@]}" stream get GLOBAL_TO_DIRECTOR --last-for global.director.inbox -j 2>/dev/null | jq -r '.data' | base64 -d 2>/dev/null || true)"
[[ "$stored" == "tls trial $NONCE" ]] \
  && ok "a message published on the leaf side is stored in the hub's GLOBAL_TO_DIRECTOR over the TLS link" \
  || bad "publish across the TLS link" "${stored:-nothing stored}"

# --- cutover mechanics --------------------------------------------------------
# These decide the order of the live cutover (the finding's section on it).
reload_srv() { nats-server --signal reload="$1" >/dev/null 2>&1; }
linked() { grep -q "JetStream using domains: local \"$1\", remote \"$DOMAIN\"" "$2"; }

# 1. Pre-staging a leaf ahead of the hub: a remote that carries tls { ca_file }
#    against a hub with NO tls block. If the leaf refused to link, the tls
#    block cannot be shipped early; the leaf side flips in the same window as
#    the hub, and the leaf keeps retrying until then.
PLAIN_MON="$(pick_port)"
hub_conf "$work/plain/nats-server.conf" "$PLAIN_CLIENT" "$PLAIN_LEAF" "$PLAIN_MON" ""
sed -i.bak "s#$work/hub/store#$work/plain/store#" "$work/plain/nats-server.conf"
nats-server --config "$work/plain/nats-server.conf" -l "$work/plain/nats-server.log" &
pids+=($!); PLAIN_PID=$!
sleep 1
leaf_conf "$work/leaf3/nats.conf" "$LEAF3_CLIENT" "$work/leaf3/store" c "$PLAIN_LEAF" "$work/tls/real/ca.pem" nats-leaf
DIRECTOR_LEAF_NKEY="$LEAF_SEED" nats-server -c "$work/leaf3/nats.conf" -l "$work/leaf3/broker-pre.log" &
pids+=($!); LEAF3_PID=$!
sleep 3
if linked "leafc$NONCE" "$work/leaf3/broker-pre.log"; then
  ok "pre-staging works: a remote carrying tls { ca_file } links to a plaintext hub"
  PRESTAGE=yes
else
  retries="$(grep -c 'Leafnode connection closed: TLS Handshake Failure' "$work/leaf3/broker-pre.log" || true)"
  [[ "$retries" -ge 2 ]] && grep -q 'authentication error' "$work/plain/nats-server.log" \
    && ok "pre-staging does NOT work: a remote carrying tls { ca_file } insists on TLS against a plaintext hub (leaf: TLS Handshake Failure, retried $retries times in 3s; hub: authentication error); it links on its own once the hub flips" \
    || bad "pre-staged leaf against a plaintext hub" "$(grep -i -E 'error|tls' "$work/leaf3/broker-pre.log" | head -3)"
  PRESTAGE=no
fi
kill "$LEAF3_PID" 2>/dev/null || true

# 2. The live sequence, on the scratch pair: a plaintext leaf is linked to the
#    plaintext hub; the hub gains its tls blocks by config reload (no restart);
#    the leaf loses the link and says why; the leaf gains tls { ca_file } by
#    config reload; the link is back. This is the cutover the finding
#    recommends for ~/.director/nats-global, so it is proven here first.
leaf_conf "$work/leaf3/nats.conf" "$LEAF3_CLIENT" "$work/leaf3/store" c "$PLAIN_LEAF" "" nats-leaf
sed -i.bak '/tls { ca_file: "", timeout: 2 }/d; s/, nkey: \$DIRECTOR_LEAF_NKEY,$/, nkey: $DIRECTOR_LEAF_NKEY }/' "$work/leaf3/nats.conf"
DIRECTOR_LEAF_NKEY="$LEAF_SEED" nats-server -c "$work/leaf3/nats.conf" -l "$work/leaf3/broker.log" &
pids+=($!); LEAF3_PID=$!
sleep 2
linked "leafc$NONCE" "$work/leaf3/broker.log" \
  && ok "cutover step 0: a plaintext leaf is linked to the plaintext hub" \
  || bad "plaintext leaf against plaintext hub" "$(tail -3 "$work/leaf3/broker.log")"

# 1a. The client listener alone, by reload.
hub_conf "$work/plain/nats-server.conf" "$PLAIN_CLIENT" "$PLAIN_LEAF" "$PLAIN_MON" "$work/tls/real"
sed -i.bak "s#$work/hub/store#$work/plain/store#" "$work/plain/nats-server.conf"
awk 'BEGIN{inleaf=0} /^leafnodes \{/{inleaf=1} inleaf && /^  tls \{/{skip=1} skip{ if (/^  \}/) {skip=0}; next } /^\}/{inleaf=0} {print}' \
  "$work/plain/nats-server.conf" > "$work/plain/client-only.conf"
cp "$work/plain/nats-server.conf" "$work/plain/both.conf"
cp "$work/plain/client-only.conf" "$work/plain/nats-server.conf"
# A plaintext client connection held open across the reload (the seat's live
# connections at cutover time).
nats -s "nats://127.0.0.1:$PLAIN_CLIENT" --nkey "$work/keys/admin.nk" sub -r "held.$NONCE" >"$work/plain/held.out" 2>&1 &
pids+=($!); HELD_PID=$!
sleep 1
reload_srv "$PLAIN_PID"
sleep 2
varz="$(curl -s "127.0.0.1:$PLAIN_MON/varz")"
if [[ "$(jq -r .tls_required <<<"$varz")" == true ]] && grep -q 'Reloaded: tls = enabled' "$work/plain/nats-server.log" \
   && [[ "$(curl -s "127.0.0.1:$PLAIN_MON/leafz" | jq -r .leafnodes)" == 1 ]]; then
  ok "cutover step 1a: the client listener takes its tls block by config reload (Reloaded: tls = enabled); the plaintext leaf stays linked"
else
  bad "client-listener tls by reload" "$(grep -i -E 'reload' "$work/plain/nats-server.log" | tail -2)"
fi
nats -s "tls://127.0.0.1:$PLAIN_CLIENT" --tlsca "$work/tls/real/ca.pem" --nkey "$work/keys/admin.nk" pub "held.$NONCE" "still here" >/dev/null 2>&1
sleep 1
grep -q "still here" "$work/plain/held.out" \
  && ok "cutover step 1a: a plaintext client connection opened before the reload stays open and keeps receiving" \
  || bad "held plaintext connection across the reload" "$(cat "$work/plain/held.out")"
kill "$HELD_PID" 2>/dev/null || true
# 1b. The leaf listener: reload is refused, so this one is a restart.
cp "$work/plain/both.conf" "$work/plain/nats-server.conf"
reload_srv "$PLAIN_PID"
sleep 2
if grep -q 'config reload not supported for LeafNode' "$work/plain/nats-server.log" \
   && [[ "$(curl -s "127.0.0.1:$PLAIN_MON/varz" | jq -r .leaf.tls_required)" != true ]]; then
  ok "cutover step 1b: the leaf listener does NOT take its tls block by reload ($(grep -o 'config reload not supported for LeafNode: field "[A-Za-z]*"' "$work/plain/nats-server.log" | tail -1)); that half is a restart"
  HUB_RELOAD=no
else
  ok "cutover step 1b: the leaf listener took its tls block by reload"
  HUB_RELOAD=yes
fi
if [[ "$HUB_RELOAD" == no ]]; then
  kill "$PLAIN_PID" 2>/dev/null || true; sleep 1
  nats-server --config "$work/plain/nats-server.conf" -l "$work/plain/nats-server.log" &
  pids+=($!); PLAIN_PID=$!
  sleep 2
fi
varz="$(curl -s "127.0.0.1:$PLAIN_MON/varz")"
[[ "$(jq -r .tls_required <<<"$varz")" == true && "$(jq -r .leaf.tls_required <<<"$varz")" == true ]] \
  && ok "cutover step 1c: after the restart both listeners require TLS" \
  || bad "both listeners after restart" "$(jq -c '{tls_required, leaf: .leaf.tls_required}' <<<"$varz")"
# Did the plaintext leaf survive the hub flip, or drop and fail to re-link?
sleep 3
if grep -q -i -E 'unknown authority|not trusted|handshake' "$work/leaf3/broker.log" \
   && [[ "$(curl -s "127.0.0.1:$PLAIN_MON/leafz" | jq -r .leafnodes)" == 0 ]]; then
  ok "cutover step 2: the plaintext leaf drops when the hub flips and says why: $(grep -o -i -m1 'x509:[^"]*' "$work/leaf3/broker.log" | head -1)"
else
  bad "plaintext leaf after the hub flipped" "leafz=$(curl -s "127.0.0.1:$PLAIN_MON/leafz" | jq -c '{leafnodes}') log=$(tail -2 "$work/leaf3/broker.log")"
fi

# 3a. The tls block added to the remote with its URL UNCHANGED (nats-leaf://):
#     reload compares remotes by URL, so this edit is not applied. A fresh
#     start with the same file links (the leaf listener check above used the
#     tls:// form; the nats-leaf:// form was checked by hand the same way).
leaf_conf "$work/leaf3/nats.conf" "$LEAF3_CLIENT" "$work/leaf3/store" c "$PLAIN_LEAF" "$work/tls/real/ca.pem" nats-leaf
reload_srv "$LEAF3_PID"
sleep 3
if ! grep -q 'Reloaded: LeafNode Remote' "$work/leaf3/broker.log" \
   && [[ "$(curl -s "127.0.0.1:$PLAIN_MON/leafz" | jq -r .leafnodes)" == 0 ]]; then
  ok "cutover step 3a: a tls block added to a remote whose URL is unchanged is IGNORED by reload (remotes are diffed by URL); the leaf stays down with the old config"
else
  bad "reload with an unchanged remote URL" "$(grep -i -E 'Reloaded|error' "$work/leaf3/broker.log" | tail -3)"
fi
# 3b. The same edit with the URL scheme changed to tls://: the remote is
#     removed and re-added, the tls block is applied, the link is back.
leaf_conf "$work/leaf3/nats.conf" "$LEAF3_CLIENT" "$work/leaf3/store" c "$PLAIN_LEAF" "$work/tls/real/ca.pem" tls
reload_srv "$LEAF3_PID"
sleep 4
linked_after="$(grep -c "JetStream using domains: local \"leafc$NONCE\", remote \"$DOMAIN\"" "$work/leaf3/broker.log")"
if [[ "$linked_after" -ge 2 ]] && grep -q 'Reloaded: LeafNode Remote' "$work/leaf3/broker.log" \
   && [[ "$(curl -s "127.0.0.1:$PLAIN_MON/leafz" | jq -r .leafnodes)" == 1 ]]; then
  ok "cutover step 3b: with the URL changed to tls:// the reload applies the tls block and the leaf re-links over TLS, no leaf restart"
else
  bad "leaf re-link after its own reload" "$(grep -i -E 'reload|error|tls' "$work/leaf3/broker.log" | tail -3)"
fi

# 3. Rollback by the same path: the hub drops its tls blocks by reload; the
#    TLS leaf drops; the leaf drops its tls block by reload; the link is back
#    in plaintext. What the finding calls the rollback, proven.
hub_conf "$work/plain/nats-server.conf" "$PLAIN_CLIENT" "$PLAIN_LEAF" "$PLAIN_MON" ""
sed -i.bak "s#$work/hub/store#$work/plain/store#" "$work/plain/nats-server.conf"
if [[ "$HUB_RELOAD" == yes ]]; then reload_srv "$PLAIN_PID"; else
  kill "$PLAIN_PID" 2>/dev/null || true; sleep 1
  nats-server --config "$work/plain/nats-server.conf" -l "$work/plain/nats-server.log" &
  pids+=($!); PLAIN_PID=$!
fi
sleep 3
leaf_conf "$work/leaf3/nats.conf" "$LEAF3_CLIENT" "$work/leaf3/store" c "$PLAIN_LEAF" "" nats-leaf
sed -i.bak '/tls { ca_file: "", timeout: 2 }/d; s/, nkey: \$DIRECTOR_LEAF_NKEY,$/, nkey: $DIRECTOR_LEAF_NKEY }/' "$work/leaf3/nats.conf"
reload_srv "$LEAF3_PID"
sleep 4
[[ "$(curl -s "127.0.0.1:$PLAIN_MON/varz" | jq -r .tls_required)" != true ]] \
  && [[ "$(curl -s "127.0.0.1:$PLAIN_MON/leafz" | jq -r .leafnodes)" == 1 ]] \
  && ok "rollback: the hub drops its tls blocks (restart), the leaf drops its tls block (reload), and the link is back in plaintext" \
  || bad "rollback" "varz tls_required=$(curl -s "127.0.0.1:$PLAIN_MON/varz" | jq -r .tls_required) leafz=$(curl -s "127.0.0.1:$PLAIN_MON/leafz" | jq -c '{leafnodes}')"
kill "$LEAF3_PID" "$PLAIN_PID" 2>/dev/null || true

# 4. Mixed mode on the client listener: allow_non_tls (top level) admits a
#    plaintext client beside TLS clients. Recorded as a transitional posture
#    for the seat's direct client only; the leaf listener has no such switch.
hub_conf "$work/mix/nats-server.conf" "$MIX_CLIENT" "$MIX_LEAF" "$(pick_port)" "$work/tls/real" "allow_non_tls: true"
sed -i.bak "s#$work/hub/store#$work/mix/store#" "$work/mix/nats-server.conf"
if nats-server --config "$work/mix/nats-server.conf" -t >/dev/null 2>"$work/mix.err"; then
  nats-server --config "$work/mix/nats-server.conf" -l "$work/mix/nats-server.log" &
  pids+=($!)
  sleep 1
  if nats -s "nats://127.0.0.1:$MIX_CLIENT" --nkey "$work/keys/admin.nk" account info >/dev/null 2>&1 \
     && nats -s "tls://127.0.0.1:$MIX_CLIENT" --tlsca "$work/tls/real/ca.pem" --nkey "$work/keys/admin.nk" account info >/dev/null 2>&1; then
    ok "allow_non_tls on the client listener admits a plaintext client and a TLS client side by side (transitional only)"
  else
    bad "allow_non_tls mixed mode"
  fi
  kill "${pids[-1]}" 2>/dev/null || true
else
  bad "allow_non_tls config" "$(cat "$work/mix.err")"
fi

echo "$pass passed, $fail failed (leaf pre-staging: $PRESTAGE; hub leaf listener takes tls by reload: $HUB_RELOAD)"
[[ $fail -eq 0 ]]
