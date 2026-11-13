#!/usr/bin/env bash
# `director hub user add` replaces this script (sim/design/hub-user-lifecycle.md): superseded; if run before the command ships, use bash 5.
# At-host script for kinu (~/operator-steps). NOT RUN by its author.
# I-469: add the read-only observer user `observer.director` to the global hub.
# Design: sim/design/hub-observer.md (draft, architect seat, 2026-11-13).
# Run only after the builder PR's verify-hub-observer.sh has passed.
# Adds one FLEET user and reloads; no existing user's grant changes, so live
# leaves stay connected (finding-019 item 4). Issues one NKey seed on this host.
#
# The grant is not written here. It is read from the observer.director block of the
# hub conf template, so the template is the one copy; the script fills in the public
# key and inserts the block. After the conf is touched, any failure ends with a
# ROLLBACK line: the one command that restores the backup and reloads.
#
# Env: DIRECTOR_GLOBAL_HOME (default ~/.director/nats-global)
#      HUB_URL (default nats://127.0.0.1:4242; use tls://... if the client port has TLS)
#      HUB_CA  (CA file, only when the client port has TLS)
#      OBSERVER_TEMPLATE  the hub conf template (probe/nats-global-tier/hub/nats-server.conf
#                         in a director checkout). Default: the copy two directories up
#                         from this script. A script carried to ~/operator-steps has no
#                         such copy beside it, so set this to the checkout's file.
#                         A set path that is not a non-empty file stops the script; it never
#                         falls back to the default.
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
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
template=""
if [[ -n "${OBSERVER_TEMPLATE:-}" ]]; then
  # A template the caller named is the only one used: a missing file is an error, never a
  # quiet switch to another copy of the grant.
  [[ -s "$OBSERVER_TEMPLATE" ]] || { echo "OBSERVER_TEMPLATE=$OBSERVER_TEMPLATE is not a non-empty file; nothing was changed" >&2; exit 1; }
  template="$OBSERVER_TEMPLATE"
elif [[ -s "$here/../../probe/nats-global-tier/hub/nats-server.conf" ]]; then
  template="$here/../../probe/nats-global-tier/hub/nats-server.conf"
fi
stage=start      # start, edited (the conf is being or has been changed), reloaded
bak=""
before=""; after=""; block=""; new=""
rollback() {
  local rc=$?
  local f
  if [[ $rc -ne 0 && ( "$stage" == edited || "$stage" == reloaded ) && -n "$bak" ]]; then
    echo "FAILED after the hub conf was changed (stage: $stage)." >&2
    echo "ROLLBACK: cp -p '$bak' '$conf' && nats-server --signal reload=$pid && rm -f '$seed'" >&2
  fi
  for f in "$before" "$after" "$block" "$new"; do
    if [[ -n "$f" ]]; then rm -f "$f"; fi
  done
}
trap rollback EXIT

echo "== 1. preflight"
for c in nats-server nats nk jq; do command -v "$c" >/dev/null || { echo "missing $c" >&2; exit 1; }; done
[[ -s "$conf" ]] || { echo "missing $conf" >&2; exit 1; }
[[ -n "$template" ]] || { echo "no hub conf template found; set OBSERVER_TEMPLATE to probe/nats-global-tier/hub/nats-server.conf in a director checkout" >&2; exit 1; }
grant="$(awk '/# BEGIN observer.director/{f=1;next} /# END observer.director/{f=0} f{ sub(/^[[:space:]]*# ?/,""); print }' "$template")"
grep -q 'OBSERVER_DIRECTOR_PUBLIC_KEY' <<<"$grant" || { echo "$template has no observer.director block with its key placeholder" >&2; exit 1; }
echo "grant read from $template"
[[ -s "$keys/admin.nk" ]] || { echo "missing admin key" >&2; exit 1; }
pid="$(cat "$pidf")"
kill -0 "$pid" || { echo "hub pid $pid not running" >&2; exit 1; }
if grep -q 'observer.director' "$conf"; then echo "observer.director already in $conf; stop" >&2; exit 1; fi
[[ ! -e "$seed" ]] || { echo "$seed exists; stop" >&2; exit 1; }
n_users="$(grep -c -E '^[[:space:]]*users:[[:space:]]*\[' "$conf" || true)"
[[ "$n_users" == 1 ]] || { echo "expected one 'users: [' line, found $n_users; insert by hand" >&2; exit 1; }
"${admin[@]}" stream ls -n >/dev/null || { echo "admin cannot list streams at $url" >&2; exit 1; }

echo "== 2. consumer counts before"
before="$(mktemp "${TMPDIR:-/tmp}/hub-observer.XXXXXX")"; after="$(mktemp "${TMPDIR:-/tmp}/hub-observer.XXXXXX")"
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
block="$(mktemp "${TMPDIR:-/tmp}/hub-observer.XXXXXX")"
{
  echo "      # observer.director (I-469): read-only observer, director seat only."
  echo "      # Generated from the hub conf template by 2026-11-13-hub-observer-user.sh."
  sed "s/OBSERVER_DIRECTOR_PUBLIC_KEY/$pub/" <<<"$grant" | sed 's/^/      /'
} >"$block"
new="$(mktemp "${TMPDIR:-/tmp}/hub-observer.XXXXXX")"
awk -v f="$block" '{ print } /^[[:space:]]*users:[[:space:]]*\[/ { while ((getline l < f) > 0) print l }' "$conf" >"$new"
stage=edited     # set before the write: a cp that fails part way leaves a changed conf
cp "$new" "$conf"
if ! nats-server --config "$conf" -t; then
  cp -p "$bak" "$conf"; rm -f "$seed"; stage=start
  echo "config test failed; restored $bak and removed the seed" >&2; exit 1
fi

echo "== 5. reload"
nats-server --signal "reload=$pid"
stage=reloaded
sleep 2
tail -n 50 "$log" | grep -q 'Reloaded: authorization' || echo "WARN: no 'Reloaded: authorization' line in the last 50 log lines; read the log"

echo "== 6. positive reads as observer.director"
"${obs[@]}" stream ls -n
first="$("${obs[@]}" stream ls -n | grep '^GLOBAL_TO_' | head -n 1)"
"${obs[@]}" consumer ls "$first" -n
"${obs[@]}" stream get "$first" "$("${obs[@]}" stream info "$first" -j | jq -r '.state.first_seq')" >/dev/null && echo "MSG.GET ok on $first"

echo "== 7. negatives (each harmless even if wrongly allowed; each must be refused)"
# A negative passes only when the hub logs a Publish Violation naming that subject. An answer
# fails it; silence with no violation (no responders, a timeout, any other error) fails it too,
# because that is what an allowed request to a missing durable looks like.
neg() {
  local subj="$1" n0 out viol
  n0="$(wc -l <"$log")"
  out="$("${obs[@]}" req --timeout 3s "$subj" '' 2>&1 || true)"
  sleep 1
  if grep -q 'Received with rtt' <<<"$out"; then
    echo "FAIL: $subj answered (should be denied)"; bad=1
  elif viol="$(tail -n +"$((n0+1))" "$log" | grep -F 'Publish Violation' || true)"; grep -Fq "Subject \"$subj\"" <<<"$viol"; then
    echo "ok, refused with a permission violation: $subj"
  else
    echo "FAIL: $subj was not answered, but the hub logged no permission violation naming it"; bad=1
  fi
}
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
