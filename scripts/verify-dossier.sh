#!/usr/bin/env bash
# Prove the dossier subcommand end to end with the built binary: the contract
# the Go tests hold in process (no body, nothing else written, files 0600 in a
# 0700 directory, the nonce lines, no handoff marker, exit 0 with every gap
# shown, exit 2 for usage only), checked again from outside with a scratch HOME
# and a scratch DIRECTOR_STATE. Runs under bash 5 and bash 3.2.
#
#   scripts/verify-dossier.sh
#
# Everything it creates lives in one temp dir removed by a trap. It reaches no
# live bus: the broker URL is a closed loopback port, then, when nats-server and
# the nats CLI are on PATH, a scratch broker on a random loopback port. It never
# sets MARVEL_SOCKET and never reads the real home. Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
work="$(mktemp -d "${TMPDIR:-/tmp}/verify-dossier.XXXXXX")"
pids=()
cleanup() {
  local p
  for p in ${pids[@]+"${pids[@]}"}; do kill -TERM "$p" 2>/dev/null || true; done
  rm -rf "$work"
}
trap cleanup EXIT

pass=0 fail=0 skipped=0
ok() { pass=$((pass + 1)); echo "ok   $1"; }
bad() { fail=$((fail + 1)); echo "FAIL $1" >&2; }
check() { # check NAME CMD...
  local name="$1"; shift
  if "$@"; then ok "$name"; else bad "$name"; fi
}
mode() { stat -f '%Lp' "$1" 2>/dev/null || stat -c '%a' "$1"; }
sentinel="SENTINEL-BODY-verify-3c1d"

bin="$work/director-mcp"
(cd "$here/../cmd/director-mcp" && go build -o "$bin" .) || { echo "build failed" >&2; exit 1; }

home="$work/home"; state="$work/state"
mkdir -p "$home" "$state"
run() { # run URL ARGS...: scratch environment, no MARVEL_SOCKET
  local url="$1"; shift
  env -i HOME="$home" PATH="$PATH" LANG="${LANG:-C}" TMPDIR="$work" \
    DIRECTOR_STATE="$state" DIRECTOR_AGENT_ID=verify-new NATS_URL="$url" \
    DIRECTOR_DWS=/nonexistent/dws "$bin" dossier "$@"
}
tree_hash() { (cd "$1" && find . -path ./dossier -prune -o -type f -print | LC_ALL=C sort | xargs shasum -a 256 2>/dev/null | shasum -a 256); }

echo "bash ${BASH_VERSION}"

# usage errors are exit 2 and nothing else is
rc=0; run nats://127.0.0.1:1 >/dev/null 2>&1 || rc=$?
check "no --role is a usage error (exit 2)" test "$rc" -eq 2
rc=0; run nats://127.0.0.1:1 --role ops/builder --bogus >/dev/null 2>&1 || rc=$?
check "an unknown flag is a usage error (exit 2)" test "$rc" -eq 2

# broker down: exit 0, gaps shown, files written
before="$(tree_hash "$state")"
rc=0; out="$(run nats://127.0.0.1:1 --role ops/builder --predecessor verify-old 2>"$work/err")" || rc=$?
check "bus down exits 0" test "$rc" -eq 0
check "bus down shows UNREAD" grep -q 'UNREAD' <<<"$out"
dir="$state/dossier/verify-new"
check "dossier directory is 0700" test "$(mode "$dir")" = 700
n=0
for f in "$dir"/*; do
  n=$((n + 1))
  check "$(basename "$f") is 0600" test "$(mode "$f")" = 600
  check "$(basename "$f") first line is the nonce" test "$(head -n1 "$f" | cut -c1-21)" = "director-dossier-id: "
  check "$(basename "$f") second line is reconstructed: true" test "$(sed -n 2p "$f")" = "reconstructed: true"
  check "$(basename "$f") has no handoff marker" bash -c '! grep -q HANDOFF-COMPLETE "$1"' _ "$f"
done
check "two files written" test "$n" -eq 2
check "nothing outside dossier/ changed" test "$(tree_hash "$state")" = "$before"
check "the home is empty" test -z "$(ls -A "$home")"

# a scratch broker with a body sentinel in the audit stream
if command -v nats-server >/dev/null && command -v nats >/dev/null; then
  port="$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])')"
  nats-server -a 127.0.0.1 -p "$port" -js -sd "$work/js" >"$work/nats.log" 2>&1 &
  pids+=("$!")
  url="nats://127.0.0.1:$port"
  for _ in $(seq 1 50); do nats --server "$url" account info >/dev/null 2>&1 && break; sleep 0.1; done
  nats --server "$url" stream add AGENT_AUDIT --subjects agent.audit --defaults >/dev/null 2>&1
  env='{"schema_version":1,"message_id":"v-ask-1","performative":"REQUEST","sender":{"agent_id":"verify-old","workspace":"w","principal":null},"recipient":{"address":"agent://ops/other"},"content":{"type":"text","data":"'"$sentinel"'"},"sent_at":"2026-10-20T00:00:00Z","trace":{"otel_traceparent":null}}'
  nats --server "$url" pub agent.audit "$env" >/dev/null 2>&1
  before="$(tree_hash "$state")"
  rc=0; out="$(run "$url" --role ops/builder --predecessor verify-old 2>/dev/null)" || rc=$?
  check "scratch broker exits 0" test "$rc" -eq 0
  check "the open ask is listed" grep -q 'v-ask-1' <<<"$out"
  check "no body in stdout" bash -c '! grep -q "$1" <<<"$2"' _ "$sentinel" "$out"
  check "no body in any file" bash -c '! grep -rq "$1" "$2"' _ "$sentinel" "$state/dossier"
  check "nothing outside dossier/ changed (scratch broker)" test "$(tree_hash "$state")" = "$before"
else
  skipped=$((skipped + 1)); echo "skip scratch broker checks (nats-server and nats not both on PATH)"
fi

echo "passed $pass, failed $fail, skipped $skipped"
test "$fail" -eq 0
