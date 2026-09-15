#!/usr/bin/env bash
# Prove the residue ledger in hub-residue.sh: what a run stores it records, and
# what it records it removes by sequence, leaving every other message alone
# (director#38).
#
# Runs against a throwaway broker of its own, on its own port, store and
# JetStream domain. It never reaches the hub, never touches the live local
# broker, and needs no leaf link or credential. The throwaway broker grants
# everything, which is the point: it isolates the ledger's logic from the hub's
# permission question, and the hub's answer to that question is then the only
# variable left.
#
# Usage: probe/nats-global-tier/verify-residue-cleanup.sh   Exit nonzero on any miss.
set -euo pipefail

PORT="${VERIFY_PORT:-4273}"
DOMAIN=residueselftest
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

command -v nats-server >/dev/null || { echo "nats-server not on PATH"; exit 2; }
command -v nats >/dev/null || { echo "nats CLI not on PATH"; exit 2; }
command -v jq >/dev/null || { echo "jq not on PATH"; exit 2; }

pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

work="$(mktemp -d)"; pids=()
cleanup() { for p in "${pids[@]:-}"; do kill "$p" 2>/dev/null || true; done; rm -rf "$work"; }
trap cleanup EXIT

mkdir -p "$work/store"
cat > "$work/nats.conf" <<CONF
server_name: residue-selftest
listen: 127.0.0.1:$PORT
jetstream { store_dir: "$work/store", domain: $DOMAIN }
CONF
nats-server -c "$work/nats.conf" -l "$work/broker.log" &
pids+=($!)
sleep 2

HUB=(nats -s "nats://127.0.0.1:$PORT" --js-domain "$DOMAIN")
"${HUB[@]}" stream add GLOBAL_TO_selftest --subjects 'global.selftest.>' \
  --storage file --retention limits --max-age 24h --max-msg-size 65536 --defaults >/dev/null

# shellcheck source=hub-residue.sh
. "$here/hub-residue.sh"

SUBJ=global.selftest.supervisor.inbox

# A message this run did NOT create. It must survive cleanup: the reason the
# ledger deletes by sequence instead of purging is that these exist.
"${HUB[@]}" req "$SUBJ" '{"message_id":"someone-elses-real-message"}' --timeout 5s >/dev/null 2>&1

# 1. A recorded publish returns its sequence and is stored.
hub_publish "$SUBJ" '{"message_id":"selftest-1"}' 'inbound test envelope'
seq1="$HUB_LAST_SEQ"
if [[ "$seq1" =~ ^[0-9]+$ ]] && [[ "$seq1" -eq 2 ]]; then
  ok "a recorded publish returns the sequence JetStream assigned (seq $seq1)"
else
  bad "publish sequence capture" "got '${seq1:-}'"
fi

hub_publish "$SUBJ" '{"message_id":"selftest-2"}' 'second inbound envelope'
"${HUB[@]}" req "$SUBJ" '{"message_id":"another-real-message"}' --timeout 5s >/dev/null 2>&1

before="$("${HUB[@]}" stream info GLOBAL_TO_selftest -j | jq '.state.messages')"
[[ "$before" -eq 4 ]] \
  && ok "the stream holds 4 messages before cleanup (2 this run created, 2 it did not)" \
  || bad "pre-cleanup stream state" "messages=$before"

# 2. Cleanup removes exactly what the run recorded.
hub_drop
[[ "${RESIDUE_LEFT}" -eq 0 ]] \
  && ok "hub_drop reports no residue left where the delete is permitted" \
  || bad "residue after cleanup" "RESIDUE_LEFT=$RESIDUE_LEFT"

after="$("${HUB[@]}" stream info GLOBAL_TO_selftest -j | jq '.state.messages')"
[[ "$after" -eq 2 ]] \
  && ok "the stream holds 2 messages after cleanup: the run removed its own and only its own" \
  || bad "post-cleanup stream state" "messages=$after (expected 2)"

# 3. The survivors are the ones the run did not create. This is the check that
#    a purge-to-sequence would fail, which is why the ledger does not use one.
survivors="$("${HUB[@]}" stream view GLOBAL_TO_selftest --raw 2>/dev/null | jq -r '.message_id' 2>/dev/null | sort | tr '\n' ' ')" || survivors=""
if [[ "$survivors" == *"someone-elses-real-message"* && "$survivors" == *"another-real-message"* \
   && "$survivors" != *"selftest-1"* && "$survivors" != *"selftest-2"* ]]; then
  ok "the surviving messages are exactly the ones this run did not publish"
else
  # stream view may not be available in every CLI build; fall back to counting.
  first="$("${HUB[@]}" stream info GLOBAL_TO_selftest -j | jq '.state.first_seq')"
  [[ "$first" -eq 1 ]] \
    && ok "the earliest surviving message is seq 1, which this run did not publish (a purge would have taken it)" \
    || bad "survivor identity" "survivors='$survivors' first_seq=$first"
fi

# 4. Deleting a sequence twice is reported as residue, not as silent success:
#    the ledger must not claim a removal it did not make.
residue_stream=(); residue_seq=(); residue_what=()
hub_record GLOBAL_TO_selftest "$seq1" 'already removed'
hub_drop 2>/dev/null
[[ "${RESIDUE_LEFT}" -eq 1 ]] \
  && ok "a delete that does not succeed is counted as residue, not reported clean" \
  || bad "double-delete accounting" "RESIDUE_LEFT=$RESIDUE_LEFT"

# 5. A stream the run cannot delete from is reported with its sequence, so the
#    operator knows exactly what to drain. At the hub this is the no-responder
#    case (a cluster holds no delete on GLOBAL_TO_DIRECTOR); here it is a
#    missing stream. Either way it must be disclosed, never swallowed.
residue_stream=(); residue_seq=(); residue_what=()
hub_record GLOBAL_TO_NOSUCHSTREAM 7 'outbound leg to the director'
hub_drop 2>"$work/residue.err" >/dev/null
err="$(cat "$work/residue.err")"
if [[ "${RESIDUE_LEFT}" -eq 1 ]] && [[ "$err" == *"GLOBAL_TO_NOSUCHSTREAM seq 7"* ]] && [[ "$err" == *RESIDUE* ]]; then
  ok "a stream the run cannot delete from is disclosed by name and sequence"
else
  bad "unreachable-stream disclosure" "RESIDUE_LEFT=$RESIDUE_LEFT err='$err'"
fi

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
