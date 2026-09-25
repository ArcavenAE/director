#!/usr/bin/env bash
# P2: cut the link (kill the hub), publish through the outage, restore, check
# that every distinct message arrives once, in order, and the outbox drains.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
F=${FABTOOL:?path to built fabtool}
OUTAGE=${OUTAGE:-600}
PA=$("$here/rig.sh" url pa); PB=$("$here/rig.sh" url pb); HUB=$("$here/rig.sh" url hub)
echo "p2 start $(date -u +%FT%TZ) outage=${OUTAGE}s"
"$here/rig.sh" kill hub
sleep 2
echo "during outage, pa publishes 500 distinct plus 50 repeats to pb, and 20 to the director:"
"$F" pub -s "$PA" -subj out.pb.w.t.x.inbox -n 500 -repeat 50 -prefix ${PREFIX:-p2}
"$F" pub -s "$PA" -subj out.director.inbox -n 20 -prefix ${PREFIX:-p2}d
echo "pa outbox during outage:"; "$F" verify -s "$PA" -stream OUTBOX
echo "pb inbox during outage:"; "$F" verify -s "$PB" -stream INBOX -prefix ${PREFIX:-p2}
sleep "$OUTAGE"
"$here/rig.sh" start hub
echo "hub restored $(date -u +%FT%TZ)"
for i in $(seq 1 90); do
  n=$("$F" verify -s "$PB" -stream INBOX -prefix ${PREFIX:-p2} | jq .counted)
  d=$("$F" verify -s "$HUB" -stream DIRECTOR_INBOX -prefix ${PREFIX:-p2}d | jq .counted)
  [[ -z "${pb_at:-}" && "$n" -ge 500 ]] && pb_at=$(( i * 2 )) && echo "pb leg complete ~${pb_at}s after restore"
  [[ -z "${dir_at:-}" && "$d" -ge 20 ]] && dir_at=$(( i * 2 )) && echo "director leg complete ~${dir_at}s after restore"
  [[ -n "${pb_at:-}" && -n "${dir_at:-}" ]] && break; sleep 2
done
sleep 5
echo "after restore:"
echo -n "pb inbox:       "; "$F" verify -s "$PB" -stream INBOX -prefix ${PREFIX:-p2}
echo -n "director inbox: "; "$F" verify -s "$HUB" -stream DIRECTOR_INBOX -prefix ${PREFIX:-p2}d
echo -n "pa outbox:      "; "$F" verify -s "$PA" -stream OUTBOX
echo "p2 end $(date -u +%FT%TZ)"
