#!/usr/bin/env bash
# P7b: does a parked INBOX keep storing sourced mail? (review question on 5.4.)
# Parking edits only the stream's own subjects; its sources still deliver. This
# shows the gap, then shows that rollback step 2 closes it by removing the
# sources as it parks. Needs P7_URL (an empty scratch server).
set -euo pipefail
U=${P7_URL:?scratch server url}
n() { nats --server "$U" "$@"; }
port=${U##*:}; port=${port%%/*}; [[ "$port" =~ ^[0-9]+$ ]] || port=4222
case " 4222 4242 7442 8222 8242 " in *" $port "*)
  echo "p7b: refusing $U, port $port is a live fleet port" >&2; exit 2 ;; esac
for s in OUTBOX INBOX; do
  if n stream info "$s" >/dev/null 2>&1; then
    echo "p7b: refusing $U, stream $s already exists" >&2; exit 2
  fi
done
msgs() { n stream info "$1" --json | jq .state.messages; }
cfg=$(mktemp); trap 'rm -f "$cfg"' EXIT

n stream add OUTBOX --subjects 'out.>' --storage file --retention work --defaults >/dev/null
cat > "$cfg" <<'JSON'
{"name":"INBOX","subjects":["agent.kc.*.*.*.inbox"],"retention":"workqueue","storage":"file",
 "discard":"old","num_replicas":1,"duplicate_window":120000000000,
 "sources":[{"name":"OUTBOX","subject_transforms":[{"src":"out.kc.>","dest":"agent.kc.>"}]}]}
JSON
n stream add --config "$cfg" >/dev/null
n pub out.kc.w.t.a.inbox one >/dev/null 2>&1; sleep 2
echo "sourced before park:            INBOX=$(msgs INBOX) OUTBOX=$(msgs OUTBOX)"

n stream edit INBOX --subjects 'legacy.parked.inbox' -f >/dev/null
n pub out.kc.w.t.a.inbox two >/dev/null 2>&1; sleep 2
echo "parked, sources kept:           INBOX=$(msgs INBOX) OUTBOX=$(msgs OUTBOX)   (mail lands where no shim reads)"

n stream info INBOX --json | jq '.config | .sources = []' > "$cfg"
n stream edit INBOX --config "$cfg" -f >/dev/null
n pub out.kc.w.t.a.inbox three >/dev/null 2>&1; sleep 2
echo "parked, sources removed:        INBOX=$(msgs INBOX) OUTBOX=$(msgs OUTBOX)   (mail waits in OUTBOX)"
