#!/usr/bin/env bash
# At-host script for kinu (~/operator-steps). NOT RUN by its author.
# I-469: add the read-only observer user `observer.director` to the global hub.
# Design: sim/design/hub-observer.md (draft, architect seat, 2026-11-13).
# Run only after the builder PR's verify-hub-observer.sh has passed.
# Adds one FLEET user and reloads; no existing user's grant changes, so live
# leaves stay connected (finding-019 item 4). Issues one NKey seed on this host.
#
# Env: DIRECTOR_GLOBAL_HOME (default ~/.director/nats-global)
#      HUB_URL (default nats://127.0.0.1:4242; use tls://... if the client port has TLS)
#      HUB_CA  (CA file, only when the client port has TLS)
set -euo pipefail

H="${DIRECTOR_GLOBAL_HOME:-$HOME/.director/nats-global}"
conf="$H/nats-server.conf"
keys="$H/keys"
pidf="$H/nats-server.pid"
log="$H/log/nats-server.log"
seed="$keys/observer-director.nk"
url="${HUB_URL:-nats://127.0.0.1:4242}"
tls=()
if [[ -n "${HUB_CA:-}" ]]; then tls=(--tlsca "$HUB_CA"); fi
admin=(nats -s "$url" "${tls[@]}" --nkey "$keys/admin.nk")
obs=(nats -s "$url" "${tls[@]}" --nkey "$seed" --inbox-prefix _INBOX_observer_director)

echo "== 1. preflight"
for c in nats-server nats nk jq; do command -v "$c" >/dev/null || { echo "missing $c" >&2; exit 1; }; done
[[ -s "$conf" ]] || { echo "missing $conf" >&2; exit 1; }
[[ -s "$keys/admin.nk" ]] || { echo "missing admin key" >&2; exit 1; }
pid="$(cat "$pidf")"
kill -0 "$pid" || { echo "hub pid $pid not running" >&2; exit 1; }
if grep -q 'observer.director' "$conf"; then echo "observer.director already in $conf; stop" >&2; exit 1; fi
[[ ! -e "$seed" ]] || { echo "$seed exists; stop" >&2; exit 1; }
n_users="$(grep -c -E '^[[:space:]]*users:[[:space:]]*\[' "$conf" || true)"
[[ "$n_users" == 1 ]] || { echo "expected one 'users: [' line, found $n_users; insert by hand" >&2; exit 1; }
"${admin[@]}" stream ls -n >/dev/null || { echo "admin cannot list streams at $url" >&2; exit 1; }

echo "== 2. consumer counts before"
before="$(mktemp)"; after="$(mktemp)"
for s in $("${admin[@]}" stream ls -n); do
  printf '%s %s\n' "$s" "$("${admin[@]}" stream info "$s" -j | jq -r '.state.consumer_count')"
done | LC_ALL=C sort >"$before"
cat "$before"

echo "== 3. backup and mint"
bak="$conf.bak-$(date -u +%Y%m%dT%H%M%SZ)"
cp -p "$conf" "$bak"; echo "backup: $bak"
( umask 077; nk -gen user >"$seed" )
pub="$(nk -inkey "$seed" -pubout)"
[[ "$pub" == U* ]] || { echo "bad public key" >&2; exit 1; }
echo "public key: $pub"

echo "== 4. insert the user block"
block="$(mktemp)"
cat >"$block" <<BLOCK
      # observer.director (I-469): read-only observer, director seat only.
      # Reads every hub stream; takes, sends, acks and deletes nothing.
      { nkey: $pub
        permissions {
          publish {
            allow: [ "\$JS.API.INFO", "\$JS.API.STREAM.NAMES", "\$JS.API.STREAM.LIST",
                     "\$JS.API.STREAM.INFO.*", "\$JS.API.CONSUMER.NAMES.*",
                     "\$JS.API.CONSUMER.LIST.*", "\$JS.API.CONSUMER.INFO.*.*",
                     "\$JS.API.STREAM.MSG.GET.*", "\$JS.API.DIRECT.GET.*",
                     "\$JS.API.DIRECT.GET.*.>" ]
            deny:  [ "\$JS.API.CONSUMER.CREATE.>", "\$JS.API.CONSUMER.DURABLE.CREATE.>",
                     "\$JS.API.CONSUMER.DELETE.>", "\$JS.API.CONSUMER.MSG.NEXT.>",
                     "\$JS.API.CONSUMER.PAUSE.>", "\$JS.API.STREAM.MSG.DELETE.>",
                     "\$JS.API.STREAM.PURGE.>", "\$JS.ACK.>", "global.>", "\$KV.>" ]
          }
          subscribe { allow: [ "_INBOX_observer_director.>" ] }
        } }
      # observer.majordomo: reserved, not issued (needs an operator ruling).
      # observer.reserved-2 (a third reader; unnamed here): reserved, not issued (needs an operator ruling).
BLOCK
new="$(mktemp)"
awk -v f="$block" '{ print } /^[[:space:]]*users:[[:space:]]*\[/ { while ((getline l < f) > 0) print l }' "$conf" >"$new"
cp "$new" "$conf"
if ! nats-server --config "$conf" -t; then
  cp -p "$bak" "$conf"; rm -f "$seed"
  echo "config test failed; restored $bak and removed the seed" >&2; exit 1
fi

echo "== 5. reload"
nats-server --signal "reload=$pid"
sleep 2
tail -n 50 "$log" | grep -q 'Reloaded: authorization' || echo "WARN: no 'Reloaded: authorization' line in the last 50 log lines; read the log"

echo "== 6. positive reads as observer.director"
"${obs[@]}" stream ls -n
first="$("${obs[@]}" stream ls -n | grep '^GLOBAL_TO_' | head -n 1)"
"${obs[@]}" consumer ls "$first" -n
"${obs[@]}" stream get "$first" "$("${obs[@]}" stream info "$first" -j | jq -r '.state.first_seq')" >/dev/null && echo "MSG.GET ok on $first"

echo "== 7. negatives (each harmless even if wrongly allowed; each must be refused)"
neg() { if "${obs[@]}" req --timeout 3s "$1" '' >/dev/null 2>&1; then echo "FAIL: $1 answered (should be denied)"; bad=1; else echo "ok, refused: $1"; fi; }
bad=0
neg "\$JS.API.CONSUMER.MSG.NEXT.$first.no-such-durable-observer-check"
neg "\$JS.API.CONSUMER.CREATE.KV_GLOBAL_PRESENCE"
neg "\$JS.API.CONSUMER.DELETE.$first.no-such-durable-observer-check"
neg "\$JS.API.STREAM.MSG.DELETE.$first"

echo "== 8. consumer counts after (must equal before)"
for s in $("${admin[@]}" stream ls -n); do
  printf '%s %s\n' "$s" "$("${admin[@]}" stream info "$s" -j | jq -r '.state.consumer_count')"
done | LC_ALL=C sort >"$after"
if cmp -s "$before" "$after"; then echo "consumer counts unchanged"; else echo "FAIL: consumer counts changed"; diff "$before" "$after" || true; bad=1; fi

echo "== rollback, if wanted: cp -p '$bak' '$conf' && nats-server --signal reload=$pid && rm -f '$seed'"
echo "== next: give the director seat DIRECTOR_HUB_OBSERVER_URL=$url DIRECTOR_HUB_OBSERVER_NKEY=$seed${HUB_CA:+ DIRECTOR_HUB_OBSERVER_CA=$HUB_CA} at its next launch"
[[ "$bad" == 0 ]] || exit 1
