#!/usr/bin/env bash
# Operator step (read-only): list each durable's inactive_threshold on the hub
# streams and AGENT_INBOX, beside the stream's max_age, and mark any durable
# whose threshold is not above that max_age. A durable cleaned up while its
# stream still holds the mail it never read leaves a live seat with no consumer
# (aae-orc-t77rr). This reads which live durables were created under an older,
# shorter threshold, so the cause of a lost durable can be confirmed or ruled out.
#
#   2026-10-30-list-durable-inactive-thresholds.sh --check    names what it would read, connects to nothing
#   2026-10-30-list-durable-inactive-thresholds.sh --list     reads and prints
#
# Reads only: `nats stream info`, `nats consumer ls` and `nats consumer info`.
# It creates, updates and deletes nothing, and it never reads or prints a
# credential. Connection comes from your own nats context or NATS_URL and creds
# environment, the way the `nats` CLI always reads them; run it against the hub
# with the context that can read it.
#
# Streams: AGENT_INBOX, GLOBAL_TO_DIRECTOR and every GLOBAL_TO_* stream the
# server lists. Pass stream names as further arguments to name your own set.
#
# Exit: 0 every durable's threshold is above its stream's max_age (or none is
# set and the stream has no max_age), 1 at least one is not, 2 usage, 3 STOP.
set -euo pipefail

mode="${1:-}"
[[ "$mode" == --check || "$mode" == --list ]] || { printf 'usage: %s --check | --list [stream ...]\n' "$(basename "$0")" >&2; exit 2; }
shift
command -v nats >/dev/null 2>&1 || { printf 'STOP: nats CLI not found\n'; exit 3; }
command -v python3 >/dev/null 2>&1 || { printf 'STOP: python3 not found\n'; exit 3; }

if [[ "$mode" == --check ]]; then
  printf 'would read, with the nats context or NATS_URL you run it under:\n'
  printf '  nats stream ls --names --json\n  nats stream info <stream> --json\n  nats consumer ls <stream> --names --json\n  nats consumer info <stream> <consumer> --json\n'
  printf 'for AGENT_INBOX, GLOBAL_TO_DIRECTOR, every GLOBAL_TO_* stream, or the streams you name.\n'
  exit 0
fi

# Both programs are passed with -c and fed through pipes, so the script writes
# no temp file, which also lets it run under a profile that denies writes.
names_py='import json,sys
for n in json.loads(sys.stdin.read() or "[]"):
    print(n)'
hub_py='import json,sys
for n in json.loads(sys.stdin.read() or "[]"):
    if n == "AGENT_INBOX" or n.startswith("GLOBAL_TO_"):
        print(n)'
report_py='import json, os, sys
def human(ns):
    s = (ns or 0) / 1e9
    if s == 0:
        return "none"
    h = s / 3600
    return "%dh" % round(h) if h >= 1 else "%ds" % round(s)
name = os.environ["STREAM"]
max_age = json.loads(os.environ["SINFO"])["config"].get("max_age", 0)
print("stream %s  max_age=%s" % (name, human(max_age)))
below = 0
for line in os.environ["ROWS"].splitlines():
    if not line.strip():
        continue
    c = json.loads(line)
    th = c["config"].get("inactive_threshold", 0)
    pend = c.get("num_pending", 0) + c.get("num_ack_pending", 0)
    mark = ""
    if th == 0:
        mark = "  no threshold (never cleaned up)"
    elif max_age and th <= max_age:
        mark = "  THRESHOLD NOT ABOVE max_age"
        below += 1
    print("  %s  inactive_threshold=%s  unread=%d  ack_floor=%s  created=%s%s" % (c["name"], human(th), pend, c["ack_floor"]["stream_seq"], c["created"], mark))
if below:
    print("  %d durable(s) on %s would be cleaned up while the stream still holds their unread mail" % (below, name))
sys.exit(1 if below else 0)'

streams=("$@")
if [[ ${#streams[@]} -eq 0 ]]; then
  all="$(nats stream ls --names --json)" || { printf 'STOP: could not list streams\n'; exit 3; }
  while IFS= read -r s; do
    if [[ -n "$s" ]]; then streams+=("$s"); fi
  done < <(printf '%s\n' "$all" | python3 -c "$hub_py")
fi
[[ ${#streams[@]} -gt 0 ]] || { printf 'STOP: no matching stream found\n'; exit 3; }

bad=0
for s in "${streams[@]}"; do
  sinfo="$(nats stream info "$s" --json)" || { printf 'STOP: could not read stream %s\n' "$s"; exit 3; }
  names="$(nats consumer ls "$s" --names --json)" || { printf 'STOP: could not list consumers of %s\n' "$s"; exit 3; }
  rows=""
  while IFS= read -r c; do
    if [[ -z "$c" ]]; then continue; fi
    info="$(nats consumer info "$s" "$c" --json | tr -d '\n')" || { printf 'STOP: could not read consumer %s on %s\n' "$c" "$s"; exit 3; }
    rows+="$info"$'\n'
  done < <(printf '%s\n' "$names" | python3 -c "$names_py")
  if ! STREAM="$s" SINFO="$sinfo" ROWS="$rows" python3 -c "$report_py"; then bad=1; fi
done
exit "$bad"
