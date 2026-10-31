#!/usr/bin/env bash
# Prove start.sh provisions the hub when it starts, and that a provision that
# fails does not stop the hub from serving (aae-orc-t77rr):
#   A. a restart applies the declared retention: a stream set back to 72h and a
#      director durable at 25h are at 168h and 192h after start.sh runs again;
#   B. a provision that always fails leaves the hub answering within a few
#      seconds, and logs exactly one failure line;
#   C. without jq the hub starts and the skip is logged.
# Then two mutants of start.sh must go red: one that never runs provision, and
# one that runs it in the foreground, before the server, so a failing provision
# delays the hub.
#
# Throwaway nats-server on a random loopback port, in a scratch home; the hub's
# ports 4222, 4242, 7422 and 7442 are refused. It never reaches the hub or the
# local broker.
#
# Usage: probe/nats-global-tier/hub/verify-start-provision.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
for tool in nats-server nats jq python3; do
  command -v "$tool" >/dev/null || { echo "$tool not on PATH"; exit 2; }
done

pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

work="$(mktemp -d "${TMPDIR:-/tmp}/start-provision.XXXXXX")"; pids=()
cleanup() { for p in "${pids[@]:-}"; do kill -TERM "$p" 2>/dev/null || true; done; rm -rf "$work"; }
trap cleanup EXIT

hour=3600000000000
port=""; url=""
newport() {
  port="$(python3 -I -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')"
  case "$port" in 4222|4242|7422|7442) echo "refusing port $port"; exit 2;; esac
  url="nats://127.0.0.1:$port"
}
N() { nats -s "$url" "$@"; }
up() { N server check connection >/dev/null 2>&1; }
wait_up() { local i; for i in $(seq 1 "${1:-50}"); do up && return 0; sleep 0.2; done; return 1; }
wait_for() { # seconds, command...: poll until the command succeeds
  local i s="$1"; shift
  for i in $(seq 1 $((s*5))); do "$@" && return 0; sleep 0.2; done; return 1
}

# mkhome DIR: a scratch DIRECTOR_GLOBAL_HOME with a no-auth config on $port.
mkhome() {
  mkdir -p "$1/store" "$1/keys" "$1/log"
  printf 'listen: 127.0.0.1:%s\njetstream { store_dir: $DIRECTOR_GLOBAL_STORE }\n' "$port" > "$1/nats-server.conf"
}
# start_hub DIR SCRIPT [env...]: run start.sh in the background; HUBPID is the server's pid.
HUBPID=""
start_hub() {
  local dir="$1" script="$2"; shift 2
  env DIRECTOR_GLOBAL_HOME="$dir" DIRECTOR_GLOBAL_URL="$url" DIRECTOR_GLOBAL_ADMIN_NKEY= "$@" bash "$script" >"$dir/start.out" 2>&1 &
  HUBPID=$!; pids+=($HUBPID)
}
stop_hub() { kill -TERM "$HUBPID" 2>/dev/null || true; wait "$HUBPID" 2>/dev/null || true; local i; for i in $(seq 1 50); do up || return 0; sleep 0.2; done; }
max_age() { N stream info "$1" --json 2>/dev/null | jq -r '.config.max_age' 2>/dev/null; }
thresh() { N consumer info "$1" "$2" --json 2>/dev/null | jq -r '.config.inactive_threshold // 0' 2>/dev/null; }
is_168() { [[ "$(max_age GLOBAL_TO_DIRECTOR)" == "$((168*hour))" ]]; }
is_192() { [[ "$(thresh GLOBAL_TO_DIRECTOR mcp_global_t_1)" == "$((192*hour))" ]]; }

# case_restart LABEL STARTSCRIPT: case A.
case_restart() {
  local label="$1" script="$2" dir="$work/$1"
  newport; mkhome "$dir"
  start_hub "$dir" "$script"
  wait_up 100 || { bad "$label: the hub did not come up"; stop_hub; return; }
  wait_for 20 is_168 && ok "$label: a first start provisions a bare hub at 168h" || bad "$label: a first start provisions a bare hub at 168h" "max_age $(max_age GLOBAL_TO_DIRECTOR)"
  # Set the stream back to 72h and add a director durable at 25h: an old hub.
  N stream edit GLOBAL_TO_DIRECTOR --max-age 72h --force >/dev/null 2>&1 || true
  N consumer add GLOBAL_TO_DIRECTOR mcp_global_t_1 --filter 'global.director.>' --pull --ack explicit --deliver all --inactive-threshold 25h --defaults >/dev/null 2>&1 || true
  stop_hub
  start_hub "$dir" "$script"
  wait_up 100 || { bad "$label: the hub did not come back up"; stop_hub; return; }
  wait_for 20 is_168 && ok "$label: a restart raises a 72h stream to 168h" || bad "$label: a restart raises a 72h stream to 168h" "max_age $(max_age GLOBAL_TO_DIRECTOR)"
  wait_for 20 is_192 && ok "$label: a restart raises a 25h director durable to 192h" || bad "$label: a restart raises a 25h director durable to 192h" "threshold $(thresh GLOBAL_TO_DIRECTOR mcp_global_t_1)"
  # start.sh writes provision.log only after provision.sh returns, which is after
  # the durable reads 192h, so poll for the line instead of reading it once.
  has_raised() { /usr/bin/grep -q 'raised' "$dir/log/provision.log" 2>/dev/null; }
  wait_for 10 has_raised && ok "$label: provision.log records the change" || bad "$label: provision.log records the change"
  stop_hub
}

# case_failure LABEL STARTSCRIPT: case B.
case_failure() {
  local label="$1" script="$2" dir="$work/$1-failure"
  printf '#!/usr/bin/env bash\necho "provision stand-in failing" >&2\nexit 3\n' > "$work/failing-provision.sh"
  newport; mkhome "$dir"
  start_hub "$dir" "$script" DIRECTOR_GLOBAL_PROVISION="$work/failing-provision.sh" DIRECTOR_PROVISION_TRIES=5
  # A provision that keeps failing for about ten seconds must not hold the hub back.
  wait_up 20 && ok "$label: the hub answers within four seconds while provision keeps failing" || bad "$label: the hub answers within four seconds while provision keeps failing"
  has_line() { /usr/bin/grep -q 'provision failed after 5 tries (last exit 3)' "$dir/log/provision.log" 2>/dev/null; }
  if wait_for 25 has_line; then
    local lines; lines="$(wc -l < "$dir/log/provision.log" | tr -d ' ')"
    [[ "$lines" == "1" ]] && ok "$label: a failed provision logs exactly one line" || bad "$label: a failed provision logs exactly one line" "$lines lines"
  else
    bad "$label: a failed provision logs one failure line" "no such line in provision.log"
  fi
  up && ok "$label: the hub is still serving after the failure" || bad "$label: the hub is still serving after the failure"
  stop_hub
}

# case_nojq: case C. A PATH with no jq: only what start.sh and the server need.
case_nojq() {
  local dir="$work/nojq" bin="$work/nojq-bin" t
  mkdir -p "$bin"
  for t in nats-server bash seq dirname date mkdir chmod env sleep; do ln -sf "$(command -v "$t")" "$bin/$t"; done
  newport; mkhome "$dir"
  env -i HOME="$work" PATH="$bin" DIRECTOR_GLOBAL_HOME="$dir" DIRECTOR_GLOBAL_URL="$url" DIRECTOR_GLOBAL_ADMIN_NKEY= "$bin/bash" "$here/start.sh" >"$dir/start.out" 2>&1 &
  HUBPID=$!; pids+=($HUBPID)
  wait_up 100 && ok "nojq: the hub starts without jq" || bad "nojq: the hub starts without jq"
  /usr/bin/grep -q 'jq not on PATH: provision skipped' "$dir/log/provision.log" 2>/dev/null && ok "nojq: the skip is logged" || bad "nojq: the skip is logged"
  stop_hub
}

case_restart real "$here/start.sh"
case_failure real "$here/start.sh"
case_nojq

# The log line lands after provision.sh returns, so a start.sh that writes it a
# little later must still pass: the check polls for the line. This variant
# writes it two seconds late. A check that read the log once, as this script
# used to, failed on it (and on 3 of 9 plain runs).
ddir="$work/late-log"; mkdir -p "$ddir"; cp "$here/provision.sh" "$ddir/provision.sh"
sed "s|printf '%s provision ok|sleep 2; printf '%s provision ok|" "$here/start.sh" > "$ddir/start.sh"
if cmp -s "$ddir/start.sh" "$here/start.sh"; then echo "late-log variant changed nothing"; exit 2; fi
case_restart late-log "$ddir/start.sh"

# Mutants of start.sh, each beside a copy of provision.sh.
mutant() { # label from to casefn
  local label="$1" from="$2" to="$3" fn="$4" mdir="$work/m-$1" p0 f0
  mkdir -p "$mdir"; cp "$here/provision.sh" "$mdir/provision.sh"
  sed "s|$from|$to|" "$here/start.sh" > "$mdir/start.sh"
  if cmp -s "$mdir/start.sh" "$here/start.sh"; then echo "mutant $label changed nothing"; exit 2; fi
  p0=$pass; f0=$fail
  "$fn" "m-$label" "$mdir/start.sh" > "$work/m-$label.out" 2>&1 || true
  if (( fail > f0 )); then
    pass=$p0; fail=$f0
    ok "mutant $label goes red; failing: $(/usr/bin/grep '^FAIL' "$work/m-$label.out" | sed -e "s/^FAIL m-$label: //" | paste -sd ';' - )"
  else
    pass=$p0; fail=$f0
    bad "mutant $label goes red" "every case still passed"
  fi
}
mutant never-provisions 'if out="$(bash "$provision" 2>&1)"; then' 'if out="$(true 2>\&1)"; then' case_restart
mutant provision-in-foreground ') > /dev/null 2>&1 &' ') > /dev/null 2>\&1' case_failure

echo "$pass passed, $fail failed"
(( fail == 0 ))
