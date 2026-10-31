#!/usr/bin/env bash
# Prove provision.sh only ever raises (aae-orc-t77rr): an existing stream's finite
# max age below 168h is raised to it, a longer one and an unlimited one are
# left alone, the director's own durables (mcp_global_*) below 192h are raised to
# it, and a longer threshold, none, and a durable of someone else's are left
# alone. Then the same cases run against two mutants of provision.sh that LOWER a
# value, and each must go red.
#
# Runs against a throwaway nats-server of its own on a random loopback port. It
# never dials the hub or the local broker, and refuses a port in the hub's or the
# local broker's set. Run it under a localhost-only profile with a scratch home.
#
# Usage: probe/nats-global-tier/hub/verify-provision-raise.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
for tool in nats-server nats jq python3; do
  command -v "$tool" >/dev/null || { echo "$tool not on PATH"; exit 2; }
done

pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

work="$(mktemp -d "${TMPDIR:-/tmp}/provision-raise.XXXXXX")"; pids=()
cleanup() { for p in "${pids[@]:-}"; do kill -TERM "$p" 2>/dev/null || true; done; rm -rf "$work"; }
trap cleanup EXIT

hour=3600000000000

# run_case SCRIPT: a fresh broker, the cases built, SCRIPT run twice. Prints
# PASS/FAIL lines for the real script; returns the number of misses in $misses.
run_case() {
  local script="$1" label="$2" port
  port="$(python3 -I -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')"
  case "$port" in 4222|4242|7422|7442) echo "refusing port $port"; exit 2;; esac
  local dir="$work/$label"; mkdir -p "$dir/store"
  printf 'listen: 127.0.0.1:%s\njetstream { store_dir: "%s" }\n' "$port" "$dir/store" > "$dir/nats.conf"
  nats-server -c "$dir/nats.conf" -l "$dir/broker.log" &
  pids+=($!)
  local i; for i in $(seq 1 50); do nats -s "nats://127.0.0.1:$port" server check connection >/dev/null 2>&1 && break; sleep 0.2; done

  local N=(nats -s "nats://127.0.0.1:$port")
  # 72h (raise), 200h (leave), unlimited (leave).
  "${N[@]}" stream add GLOBAL_TO_DIRECTOR --subjects 'global.director.>' --storage file --retention limits --max-age 72h --max-msg-size 65536 --defaults >/dev/null
  "${N[@]}" stream add GLOBAL_TO_c1 --subjects 'global.c1.>' --storage file --retention limits --max-age 200h --max-msg-size 65536 --defaults >/dev/null
  "${N[@]}" stream add GLOBAL_TO_c2 --subjects 'global.c2.>' --storage file --retention limits --max-age=-1 --max-msg-size 65536 --defaults >/dev/null
  local mk='--pull --ack explicit --deliver all --defaults'
  # on the 72h stream: director durables at 25h (raise), 200h (leave), none (leave), and one that is not the director's at 25h (leave).
  "${N[@]}" consumer add GLOBAL_TO_DIRECTOR mcp_global_a_1 --filter 'global.director.>' $mk --inactive-threshold 25h >/dev/null
  "${N[@]}" consumer add GLOBAL_TO_DIRECTOR mcp_global_b_1 --filter 'global.director.>' $mk --inactive-threshold 200h >/dev/null
  "${N[@]}" consumer add GLOBAL_TO_DIRECTOR mcp_global_c_1 --filter 'global.director.>' $mk >/dev/null
  "${N[@]}" consumer add GLOBAL_TO_DIRECTOR hand_made --filter 'global.director.>' $mk --inactive-threshold 25h >/dev/null

  local sj="$dir/stream.before" cj="$dir/cons.before"
  "${N[@]}" stream info GLOBAL_TO_DIRECTOR --json | jq -S 'del(.config.max_age) | .config' > "$sj"
  "${N[@]}" consumer info GLOBAL_TO_DIRECTOR mcp_global_a_1 --json | jq -S 'del(.config.inactive_threshold) | .config' > "$cj"

  local out1 out2
  out1="$(DIRECTOR_GLOBAL_URL="nats://127.0.0.1:$port" DIRECTOR_GLOBAL_ADMIN_NKEY= DIRECTOR_GLOBAL_HOME="$dir" bash "$script" c1 c2 2>&1)" || { bad "$label: provision.sh exited nonzero" "$out1"; return; }
  out2="$(DIRECTOR_GLOBAL_URL="nats://127.0.0.1:$port" DIRECTOR_GLOBAL_ADMIN_NKEY= DIRECTOR_GLOBAL_HOME="$dir" bash "$script" c1 c2 2>&1)" || { bad "$label: second provision.sh run exited nonzero" "$out2"; return; }

  local ma th
  ma() { "${N[@]}" stream info "$1" --json | jq -r '.config.max_age'; }
  th() { "${N[@]}" consumer info "$1" "$2" --json | jq -r '.config.inactive_threshold // 0'; }
  [[ "$(ma GLOBAL_TO_DIRECTOR)" == "$((168*hour))" ]] && ok "$label: a 72h stream is raised to 168h" || bad "$label: a 72h stream is raised to 168h" "got $(ma GLOBAL_TO_DIRECTOR)"
  [[ "$(ma GLOBAL_TO_c1)" == "$((200*hour))" ]] && ok "$label: a 200h stream is left at 200h" || bad "$label: a 200h stream is left at 200h" "got $(ma GLOBAL_TO_c1)"
  [[ "$(ma GLOBAL_TO_c2)" == "0" ]] && ok "$label: an unlimited stream is left unlimited" || bad "$label: an unlimited stream is left unlimited" "got $(ma GLOBAL_TO_c2)"
  [[ "$(th GLOBAL_TO_DIRECTOR mcp_global_a_1)" == "$((192*hour))" ]] && ok "$label: a 25h director durable is raised to 192h" || bad "$label: a 25h director durable is raised to 192h" "got $(th GLOBAL_TO_DIRECTOR mcp_global_a_1)"
  [[ "$(th GLOBAL_TO_DIRECTOR mcp_global_b_1)" == "$((200*hour))" ]] && ok "$label: a 200h durable is left at 200h" || bad "$label: a 200h durable is left at 200h" "got $(th GLOBAL_TO_DIRECTOR mcp_global_b_1)"
  [[ "$(th GLOBAL_TO_DIRECTOR mcp_global_c_1)" == "0" ]] && ok "$label: a durable with no threshold keeps none" || bad "$label: a durable with no threshold keeps none" "got $(th GLOBAL_TO_DIRECTOR mcp_global_c_1)"
  [[ "$(th GLOBAL_TO_DIRECTOR hand_made)" == "$((25*hour))" ]] && ok "$label: a durable that is not the director's is left alone" || bad "$label: a durable that is not the director's is left alone" "got $(th GLOBAL_TO_DIRECTOR hand_made)"

  "${N[@]}" stream info GLOBAL_TO_DIRECTOR --json | jq -S 'del(.config.max_age) | .config' > "$dir/stream.after"
  cmp -s "$dir/stream.after" "$sj" && ok "$label: no other stream field changed" || bad "$label: no other stream field changed"
  "${N[@]}" consumer info GLOBAL_TO_DIRECTOR mcp_global_a_1 --json | jq -S 'del(.config.inactive_threshold) | .config' > "$dir/cons.after"
  cmp -s "$dir/cons.after" "$cj" && ok "$label: no other durable field changed" || bad "$label: no other durable field changed"

  local changes; changes="$(printf '%s\n' "$out1" | /usr/bin/grep -c ' raised ' || true)"
  [[ "$changes" == "2" ]] && ok "$label: one line per change (2 on the first run)" || bad "$label: one line per change (2 on the first run)" "got $changes: $out1"
  printf '%s\n' "$out2" | /usr/bin/grep -q ' raised ' && bad "$label: the second run changes nothing" "$out2" || ok "$label: the second run changes nothing"
}

# The real script.
run_case "$here/provision.sh" real

# Mutants: each LOWERS a value, so the cases above must fail against it. A
# mutant's own PASS/FAIL lines go to a file and its counters are put back; the
# only result that counts is whether the cases noticed.
mutant() {
  local label="$1" from="$2" to="$3" file="$work/provision-$1.sh" p0 f0
  sed "s|$from|$to|" "$here/provision.sh" > "$file"
  if cmp -s "$file" "$here/provision.sh"; then echo "mutant $label changed nothing"; exit 2; fi
  p0=$pass; f0=$fail
  run_case "$file" "$label" > "$work/$label.out" 2>&1 || true
  if (( fail > f0 )); then
    pass=$p0; fail=$f0
    ok "mutant $label goes red; failing: $(/usr/bin/grep '^FAIL' "$work/$label.out" | sed -e "s/^FAIL $label: //" | paste -sd ';' - )"
  else
    pass=$p0; fail=$f0
    bad "mutant $label goes red" "every case still passed"
  fi
}
# A stream edit that lowers: treat "not the declared age" as the reason to edit.
mutant lowering-stream 'cur > 0 \&\& cur < STREAM_MAX_AGE_NS' 'cur > 0 \&\& cur != STREAM_MAX_AGE_NS'
# A durable edit that lowers.
mutant lowering-durable 'cur > 0 \&\& cur < DURABLE_INACTIVE_NS' 'cur > 0 \&\& cur != DURABLE_INACTIVE_NS'

echo "$pass passed, $fail failed"
(( fail == 0 ))
