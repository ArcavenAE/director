#!/usr/bin/env bash
# Make the hub's server key and certificate request on the hub host. The key
# is born here and never leaves; only hub.csr goes to the root ceremony
# (ca-ceremony.sh), and hub.pem comes back. Runbook: CEREMONY.md.
#
# Writes $DIRECTOR_GLOBAL_HOME/tls/hub-key.pem (0600) and hub.csr. Refuses to
# overwrite an existing key: a renewal reuses the key and only needs a new
# CSR, so pass RENEW=1 to write hub.csr from the existing key.
#
# SAN: the hub name, this host's short name, localhost, the LAN IP, 127.0.0.1.
# The LAN IP is the name mokuzai dials today, so it is the entry that matters;
# the DNS names are for the loopback admin path and the cloud phase's URL.
set -euo pipefail
HOME_DIR="${DIRECTOR_GLOBAL_HOME:-$HOME/.director/nats-global}"
HUB_NAME="${HUB_NAME:-global-hub}"
HUB_LAN_IP="${HUB_LAN_IP:-$(ifconfig 2>/dev/null | awk '/inet 192\.168\./{print $2; exit}')}"
[[ -n "$HUB_LAN_IP" ]] || { echo "no 192.168 address found; set HUB_LAN_IP" >&2; exit 2; }
tls="$HOME_DIR/tls"
mkdir -p "$tls"; chmod 700 "$tls"
if [[ -e "$tls/hub-key.pem" && "${RENEW:-0}" != 1 ]]; then
  echo "$tls/hub-key.pem exists; RENEW=1 writes a new CSR from it, or remove it by hand to start over" >&2; exit 1
fi
[[ -e "$tls/hub-key.pem" ]] || openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "$tls/hub-key.pem" 2>/dev/null
chmod 600 "$tls/hub-key.pem"
san="DNS:$HUB_NAME,DNS:$(hostname -s),DNS:localhost,IP:$HUB_LAN_IP,IP:127.0.0.1"
openssl req -new -key "$tls/hub-key.pem" -subj "/CN=$HUB_NAME" \
  -addext "subjectAltName=$san" -addext "extendedKeyUsage=serverAuth" \
  -addext "keyUsage=critical,digitalSignature" -out "$tls/hub.csr" 2>/dev/null
echo "wrote $tls/hub.csr (key stays in $tls/hub-key.pem)"
openssl req -in "$tls/hub.csr" -noout -subject
openssl req -in "$tls/hub.csr" -noout -text | grep -A1 'Subject Alternative Name' | tail -1 | sed 's/^ *//' 
echo "take hub.csr to the ceremony; hub.pem and ca.pem come back into $tls"
