#!/usr/bin/env bash
# Prove the presence watch (sim/design/presence-watch.md, build item 7) against a
# scratch hub and leaf on loopback: a send to an absent cluster is held, the
# cluster's row expires and returns, and the held send is released once, as one
# stream message. Nothing here reaches a live bus, a live hub or the real home.
#
#   scripts/verify-presence-watch.sh                 the automated proof (about 4 minutes)
#   scripts/verify-presence-watch.sh --sleep-probe   the manual section, below
#
# Everything it creates lives in one temp dir: two nats-server processes on
# random loopback ports, a built copy of the shim, a scratch HOME and a scratch
# DIRECTOR_STATE. A cleanup trap stops the processes and removes the dir. It
# writes nothing outside it. The watch runs on the shim's own 30 second heartbeat
# (cadence bound 1 in presencewatch_shim.go), so the waits below are real: absence
# is two passes after the row expires, and a return is two passes after it
# is written again. The scratch presence bucket has an 8 second TTL so a row
# expires inside the run.
#
# What it checks, in order:
#   control  a send to a cluster with a live, beating row is accepted for delivery
#            and lands as one stream message (so an absent trial means something)
#   1        the row stops beating and expires; the watch records the cluster absent
#   2        a send to it is "held, not delivered", the stream does not grow, and
#            held.jsonl names the hold id
#   3        the row is written again; the watch records it back
#   4        the held send is released: the stream gains exactly one message, whose
#            message id is the hold id, and it does not grow again
#
# Manual section (D3, the one claim the design infers and does not measure): that
# Go's monotonic clock stops while the host sleeps on darwin, which is how the
# watch tells a host sleep from lateness. Run it on the operator's host:
#   scripts/verify-presence-watch.sh --sleep-probe
# In another terminal, `pmset sleepnow`, wake the host, and read the last lines:
# wall elapsed, monotonic elapsed and their difference. A difference above 30
# seconds is what the watch records as "host-sleep". It builds a small Go program
# in the temp dir and prints both clocks every 5 seconds for 5 minutes.
#
# Needs nats-server, the nats CLI, jq, go and python3. Exit nonzero on any miss.
#
# It bounds its own run. A first failure in the setup, the control or the first
# sighting stops it at once (the later waits mean nothing without them), each wait
# prints its limit before it starts, the go build is limited to
# VERIFY_BUILD_TIMEOUT seconds (default 180), and the whole run to
# VERIFY_DEADLINE seconds (default 600; a clean run took 194 seconds here).
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d "${TMPDIR:-/tmp}/pwv.XXXXXX")"
pids=()
cleanup() {
  local p
  for p in "${pids[@]:-}"; do kill "$p" 2>/dev/null || true; done
  rm -rf "$work"
}
trap cleanup EXIT
# A signal that would end the script outright (SIGPIPE from a write to a fifo whose
# reader is gone, a hangup, an interrupt) skips the EXIT trap, so each one is
# turned into an exit, which runs cleanup and stops the servers by their pids.
trap 'exit 124' TERM
trap 'exit 141' PIPE
trap 'exit 129' HUP
trap 'exit 130' INT

# The overall deadline: a watchdog that ends the script, so nothing waits forever.
deadline="${VERIFY_DEADLINE:-600}"
self=$$
(
  while (( SECONDS < deadline )); do
    kill -0 "$self" 2>/dev/null || exit 0
    sleep 2
  done
  echo "FAIL the overall deadline of ${deadline}s was reached (VERIFY_DEADLINE)" >&2
  kill -TERM "$self" 2>/dev/null || true
) &
pids+=($!)
progress() { echo "... $* [$SECONDS of ${deadline}s used]"; }
bounded() { # seconds command...: run, and stop it when the seconds are up
  local t=$1 p i
  shift
  "$@" & p=$!
  for ((i = 0; i < t; i++)); do
    if ! kill -0 "$p" 2>/dev/null; then wait "$p"; return $?; fi
    sleep 1
  done
  kill -TERM "$p" 2>/dev/null || true
  return 124
}

for c in go python3; do command -v "$c" >/dev/null || { echo "$c not on PATH"; exit 2; }; done

if [[ "${1:-}" == "--sleep-probe" ]]; then
  mkdir -p "$work/probe"
  cat > "$work/probe/main.go" <<'GO'
package main

import (
	"fmt"
	"time"
)

func main() {
	start := time.Now() // carries the monotonic reading
	fmt.Println("run `pmset sleepnow` in another terminal, wake the host, and read the last lines")
	for i := 0; i < 60; i++ {
		time.Sleep(5 * time.Second)
		now := time.Now()
		wall := now.Round(0).Sub(start.Round(0)) // wall clock only
		mono := now.Sub(start)                   // monotonic
		lead := wall - mono
		verdict := "no sleep seen"
		if lead > 30*time.Second {
			verdict = "HOST SLEEP SEEN (the watch records host-sleep above 30s)"
		}
		fmt.Printf("wall elapsed %v  monotonic elapsed %v  wall minus monotonic %v  %s\n", wall.Round(time.Second), mono.Round(time.Second), lead.Round(time.Second), verdict)
	}
}
GO
  ( cd "$work/probe" && cat > go.mod <<'MOD'
module probe

go 1.26
MOD
    go run . )
  exit 0
fi

for c in nats-server nats jq; do command -v "$c" >/dev/null || { echo "$c not on PATH"; exit 2; }; done

pass=0; fail=0

# The shim, built into the temp dir (or DIRECTOR_MCP_BIN, to run the proof
# against another build).
if [[ -n "${DIRECTOR_MCP_BIN:-}" ]]; then
  SHIM="$DIRECTOR_MCP_BIN"
else
  progress "building the shim (limit ${VERIFY_BUILD_TIMEOUT:-180}s)"
  # shellcheck disable=SC2016  # the program is meant to expand in the child shell
  bounded "${VERIFY_BUILD_TIMEOUT:-180}" bash -c 'cd "$1/cmd/director-mcp" && go build -o "$2/director-mcp" .' _ "$here" "$work" \
    || { echo "the go build failed or did not finish in ${VERIFY_BUILD_TIMEOUT:-180}s"; exit 2; }
  SHIM="$work/director-mcp"
fi
[[ -x "$SHIM" ]] || { echo "the shim $SHIM is not an executable file"; exit 2; }

ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }
stop_here() { # a failure after which the later steps would only wait
  echo "$pass passed, $((fail)) failed; stopping at the first failed prerequisite"
  exit 1
}

# Scratch identity: nothing below reads or writes the real home or state.
export HOME="$work/home" DIRECTOR_STATE="$work/state"
mkdir -p "$HOME" "$DIRECTOR_STATE"
unset MARVEL_SOCKET

ports() { python3 - <<'PY'
import socket
s = [socket.socket() for _ in range(3)]
for x in s: x.bind(("127.0.0.1", 0))
print(*[x.getsockname()[1] for x in s])
for x in s: x.close()
PY
}
read -r HUBP LEAFP HUBLEAFP <<<"$(ports)"
cat > "$work/hub.conf" <<CONF
listen: 127.0.0.1:$HUBP
jetstream { store_dir: "$work/hubjs", domain: global }
leafnodes { listen: 127.0.0.1:$HUBLEAFP }
CONF
cat > "$work/leaf.conf" <<CONF
listen: 127.0.0.1:$LEAFP
jetstream { store_dir: "$work/leafjs", domain: leaf }
leafnodes { remotes: [ { url: "nats-leaf://127.0.0.1:$HUBLEAFP" } ] }
CONF
nats-server -c "$work/hub.conf" -l "$work/hub.log" & pids+=($!)
nats-server -c "$work/leaf.conf" -l "$work/leaf.log" & pids+=($!)

LEAF=(nats -s "nats://127.0.0.1:$LEAFP")
HUB=(nats -s "nats://127.0.0.1:$LEAFP" --js-domain global)
# Both JetStream domains must answer before anything is provisioned: the leaf's
# own domain starts a moment after its port opens, and a stream add that lands
# first fails with "no responders".
for _ in $(seq 1 60); do
  "${HUB[@]}" account info >/dev/null 2>&1 && "${LEAF[@]}" account info >/dev/null 2>&1 && break
  sleep 0.5
done
"${HUB[@]}" account info >/dev/null 2>&1 || { echo "the hub domain is not reachable through the leaf"; exit 2; }
"${LEAF[@]}" account info >/dev/null 2>&1 || { echo "the leaf domain is not reachable"; exit 2; }

# Provision what the broker setup and the hub provisioning create.
"${LEAF[@]}" stream add AGENT_INBOX --subjects 'agent.*.*.*.inbox' --subjects 'agent.*.*.role.*.inbox' --storage memory --defaults >/dev/null
"${LEAF[@]}" kv add AGENT_STATE --ttl 90s --storage memory >/dev/null
"${HUB[@]}" stream add GLOBAL_TO_DIRECTOR --subjects 'global.director.>' --storage memory --defaults >/dev/null
"${HUB[@]}" stream add GLOBAL_TO_c1 --subjects 'global.c1.>' --storage memory --defaults >/dev/null
"${HUB[@]}" kv add GLOBAL_PRESENCE --ttl 8s --storage memory >/dev/null


# --- drive the shim over its own stdio ---------------------------------------
mkfifo "$work/in" "$work/out"
notify() { printf '{"jsonrpc":"2.0","method":"%s","params":%s}\n' "$1" "$2" >&3; }
rpc() { # id method params -> the response line
  printf '{"jsonrpc":"2.0","id":%s,"method":"%s","params":%s}\n' "$1" "$2" "$3" >&3
  local line
  read -r -t "${RPC_TIMEOUT:-30}" -u 4 line || { echo '{"error":"rpc timeout"}'; return 0; }
  printf '%s\n' "$line"
}
call() { rpc "$1" tools/call "$(jq -nc --arg n "$2" --argjson a "$3" '{name:$n,arguments:$a}')"; }
text_of() { jq -r '.result.content[0].text // "NO RESULT"'; }
env DIRECTOR_AGENT_ID=verifydirector DIRECTOR_TEAM=ops DIRECTOR_WORKSPACE=verifyws \
    NATS_URL="nats://127.0.0.1:$LEAFP" DIRECTOR_GLOBAL_DOMAIN=global DIRECTOR_CLUSTER=kinu \
    DIRECTOR_GLOBAL_ROLE=director DIRECTOR_PRESENCE_WATCH=1 \
    "$SHIM" <"$work/in" >"$work/out" 2>"$work/shim.err" &
pids+=($!)
exec 3>"$work/in" 4<"$work/out"
rpc 1 initialize '{}' >/dev/null
notify notifications/initialized '{}'

# The scratch supervisor's row, beaten every 3 seconds by a background loop; the
# bucket TTL is 8 seconds, so killing the loop lets the row expire.
beat_loop() {
  while true; do
    beat_once >/dev/null 2>&1 || true
    sleep 3
  done
}
beat_pid=""
beat_once() { "${HUB[@]}" kv put GLOBAL_PRESENCE presence.c1.supervisor.s1 \
  "$(jq -nc --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" '{cluster:"c1",role:"supervisor",agent_id:"sup",instance:"s1",state:"working",ts:$ts}')" >/dev/null; }
start_beat() { beat_loop & beat_pid=$!; pids+=("$beat_pid"); }
stop_beat() { [[ -n "$beat_pid" ]] && kill "$beat_pid" 2>/dev/null || true; beat_pid=""; }
c1_msgs() { "${HUB[@]}" stream info GLOBAL_TO_c1 -j | jq '.state.messages'; }
events() { cat "$DIRECTOR_STATE/presence-events.jsonl" 2>/dev/null || true; }
wait_event() { # kind unit timeout-seconds
  local t=$3 i
  progress "waiting for the $1 event of $2 (limit ${t}s)"
  for ((i = 0; i < t; i += 5)); do
    jq -e --arg k "$1" --arg u "$2" 'select(.kind==$k and .unit==$u)' <<<"$(events)" >/dev/null 2>&1 && return 0
    sleep 5
  done
  return 1
}

# control: a live, beating row is accepted and lands as one message. The first
# beat is written once in the open, so a CLI that cannot write it stops the run
# here with its own error and not after the later waits.
progress "control: writing the first beat"
beat_once || { echo "the first presence write failed (the nats CLI or the bucket); nothing later can pass"; exit 2; }
start_beat
sleep 4
resp="$(call 2 send_message '{"to":"global://c1/supervisor","performative":"INFORM","text":"positive control"}')"
out="$(text_of <<<"$resp")"
if [[ "$(jq -r '.status // empty' <<<"$out" 2>/dev/null)" == "accepted for delivery" && "$(c1_msgs)" == 1 ]]; then
  ok "control: a send to a live, beating cluster is accepted and stored once"
else
  bad "positive control" "$out [stream $(c1_msgs)]"
  stop_here
fi

# The watch has to see the row before it can miss it: a cluster first seen at
# the end of an absence is "appeared", never "back".
if wait_event appeared c1 120; then
  ok "control: the watch saw the beating cluster (appeared)"
else
  bad "control: the watch never saw the beating cluster" "$(events | tail -3)"
  stop_here
fi

# 1. The row stops beating and expires; two missed passes later the watch says absent.
stop_beat
if wait_event absent c1 180; then
  ok "1: the watch recorded the cluster absent after its row expired"
else
  bad "1: no absent event for c1 within 180s" "$(events | tail -3)"
fi

# 2. A send to the absent cluster is held, not delivered.
before="$(c1_msgs)"
resp="$(call 3 send_message '{"to":"global://c1/supervisor","performative":"INFORM","text":"held trial"}')"
out="$(text_of <<<"$resp")"
hold_id="$(jq -r '.hold_id // empty' <<<"$out" 2>/dev/null || true)"
if [[ "$(jq -r '.status // empty' <<<"$out" 2>/dev/null)" == "held, not delivered" && -n "$hold_id" && "$(c1_msgs)" == "$before" ]]; then
  ok "2: the send is held, not delivered, and the stream did not grow (hold $hold_id)"
else
  bad "2: the send was not held" "$out [stream $(c1_msgs), was $before]"
fi
if jq -e --arg id "$hold_id" 'select(.id==$id and .state=="held")' "$DIRECTOR_STATE/held.jsonl" >/dev/null 2>&1; then
  ok "2: held.jsonl names the hold id"
else
  bad "2: held.jsonl has no held line for $hold_id"
fi

# 3. The row is written again; two sightings later the watch says back.
start_beat
if wait_event back c1 180; then
  ok "3: the watch recorded the cluster back"
else
  bad "3: no back event for c1 within 180s" "$(events | tail -3)"
fi

# 4. The held send is released: one more stream message, carrying the hold id.
progress "waiting for the release (limit 180s)"
for _ in $(seq 1 36); do
  [[ "$(c1_msgs)" -ge $((before + 1)) ]] && break
  sleep 5
done
after="$(c1_msgs)"
mid="$("${HUB[@]}" stream get GLOBAL_TO_c1 --last-for global.c1.supervisor.inbox -j 2>/dev/null | jq -r '.data' | base64 -d 2>/dev/null | jq -r '.message_id // empty' 2>/dev/null || true)"
if [[ "$after" == $((before + 1)) && "$mid" == "$hold_id" ]]; then
  ok "4: the held send was released once, under its hold id"
else
  bad "4: release" "stream $after (want $((before + 1))), last message id '$mid' (want $hold_id)"
fi
sleep 40 # a further watch pass or two: a released record must not go out again
if [[ "$(c1_msgs)" == "$after" ]]; then
  ok "4: the release did not repeat"
else
  bad "4: the stream grew after the release" "$(c1_msgs)"
fi
if jq -e --arg id "$hold_id" 'select(.id==$id and .state=="released")' "$DIRECTOR_STATE/held.jsonl" >/dev/null 2>&1; then
  ok "4: held.jsonl records the release"
else
  bad "4: held.jsonl has no released line for $hold_id"
fi

stop_beat
exec 3>&- 4<&- || true
echo "$pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
