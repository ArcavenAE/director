#!/usr/bin/env bash
# Mint a THROWAWAY CA and hub certificate for a scratch trial. Not the fleet
# root: that comes from the ceremony (CEREMONY.md, ca-ceremony.sh, finding-002),
# and this script refuses to write anywhere under ~/.director so the trial
# material can never be the material the live hub serves.
#
#   tls-mint.sh <dir>     writes <dir>/{ca.pem,ca-key.pem,hub.pem,hub-key.pem}
#
# SAN: the hub name, this host's short name, localhost, the LAN IP, 127.0.0.1.
# The root is deliberately short-lived (30 days): a trial root that outlives
# the trial is the finding-002 mistake this script used to make.
set -euo pipefail
tls="${1:-}"
[[ -n "$tls" ]] || { echo "usage: tls-mint.sh <scratch dir>" >&2; exit 2; }
case "$tls" in "$HOME/.director"*|~/.director*) echo "refusing to mint trial material under ~/.director; the live hub's CA comes from the ceremony (CEREMONY.md)" >&2; exit 2 ;; esac
HUB_NAME="${HUB_NAME:-global-hub}"
HUB_LAN_IP="${HUB_LAN_IP:-$(ifconfig 2>/dev/null | awk '/inet 192\.168\./{print $2; exit}')}"
[[ -n "$HUB_LAN_IP" ]] || { echo "no 192.168 address found; set HUB_LAN_IP" >&2; exit 2; }
[[ -e "$tls/ca.pem" || -e "$tls/hub.pem" ]] && { echo "$tls already holds a CA or cert; remove it by hand to re-mint" >&2; exit 1; }
mkdir -p "$tls"; chmod 700 "$tls"
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 30 \
  -subj "/CN=TRIAL director CA $(date -u +%Y%m%d) $(hostname -s)" -keyout "$tls/ca-key.pem" -out "$tls/ca.pem" \
  -addext "basicConstraints=critical,CA:TRUE" -addext "keyUsage=critical,keyCertSign,cRLSign" 2>/dev/null
openssl req -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes \
  -subj "/CN=$HUB_NAME" -keyout "$tls/hub-key.pem" -out "$tls/hub.csr" 2>/dev/null
printf 'subjectAltName=DNS:%s,DNS:%s,DNS:localhost,IP:%s,IP:127.0.0.1\nextendedKeyUsage=serverAuth\nkeyUsage=critical,digitalSignature\n' \
  "$HUB_NAME" "$(hostname -s)" "$HUB_LAN_IP" > "$tls/san.ext"
openssl x509 -req -in "$tls/hub.csr" -CA "$tls/ca.pem" -CAkey "$tls/ca-key.pem" -CAcreateserial \
  -days 30 -extfile "$tls/san.ext" -out "$tls/hub.pem" 2>/dev/null
rm -f "$tls/hub.csr" "$tls/san.ext" "$tls/ca.srl"
chmod 600 "$tls"/*-key.pem
echo "trial material in $tls (30 days, not for the live hub):"
openssl x509 -in "$tls/hub.pem" -noout -subject -issuer -enddate
