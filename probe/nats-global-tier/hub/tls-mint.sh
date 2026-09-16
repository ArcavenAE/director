#!/usr/bin/env bash
# Mint the hub's local CA and server certificate (finding-001). Run ONCE on the
# hub host, before the operator-gated cutover; it refuses to overwrite.
# Writes $DIRECTOR_GLOBAL_HOME/tls/{ca.pem,ca-key.pem,hub.pem,hub-key.pem}.
# ca.pem is public and is what every leaf host and direct client trusts;
# the two -key.pem files never leave this directory.
# SAN: the hub name, this host's short name, localhost, the LAN IP, 127.0.0.1.
# The cloud phase replaces this CA with the fleet CA (global-bus-tier.md
# section 9); nothing here is that.
set -euo pipefail
HOME_DIR="${DIRECTOR_GLOBAL_HOME:-$HOME/.director/nats-global}"
HUB_NAME="${HUB_NAME:-global-hub}"
HUB_LAN_IP="${HUB_LAN_IP:-$(ifconfig 2>/dev/null | awk '/inet 192\.168\./{print $2; exit}')}"
[[ -n "$HUB_LAN_IP" ]] || { echo "no 192.168 address found; set HUB_LAN_IP" >&2; exit 2; }
tls="$HOME_DIR/tls"
[[ -e "$tls/ca.pem" || -e "$tls/hub.pem" ]] && { echo "$tls already holds a CA or cert; remove it by hand to re-mint" >&2; exit 1; }
mkdir -p "$tls"; chmod 700 "$tls"
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 3650 \
  -subj "/CN=director fleet CA (local, $(hostname -s))" -keyout "$tls/ca-key.pem" -out "$tls/ca.pem" \
  -addext "basicConstraints=critical,CA:TRUE" -addext "keyUsage=critical,keyCertSign,cRLSign"
openssl req -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes \
  -subj "/CN=$HUB_NAME" -keyout "$tls/hub-key.pem" -out "$tls/hub.csr"
printf 'subjectAltName=DNS:%s,DNS:%s,DNS:localhost,IP:%s,IP:127.0.0.1\nextendedKeyUsage=serverAuth\nkeyUsage=critical,digitalSignature\n' \
  "$HUB_NAME" "$(hostname -s)" "$HUB_LAN_IP" > "$tls/san.ext"
openssl x509 -req -in "$tls/hub.csr" -CA "$tls/ca.pem" -CAkey "$tls/ca-key.pem" -CAcreateserial \
  -days 825 -extfile "$tls/san.ext" -out "$tls/hub.pem"
rm -f "$tls/hub.csr" "$tls/san.ext" "$tls/ca.srl"
chmod 600 "$tls"/*-key.pem
echo "minted in $tls:"
openssl x509 -in "$tls/hub.pem" -noout -subject -ext subjectAltName -enddate
echo "ship $tls/ca.pem to every leaf host and direct client; the -key.pem files stay here"
