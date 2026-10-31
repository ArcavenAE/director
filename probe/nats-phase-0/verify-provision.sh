#!/usr/bin/env bash
# Prove provision.sh only ever raises. An existing AGENT_INBOX with a finite max
# age below 168h is raised to it; a longer one and an unlimited one are left
# alone; a missing one is created at 168h. Each local durable (mcp_*) with a
# finite inactive_threshold below the stream's max age is raised to a day above
# it; a threshold at or above the max age, none, and a consumer that is not the
# shim's are left alone. Every stream and durable prints its before and after
# values, the second run changes nothing, and the anonymous and authorized
# configs both work. Then the cases run against two mutants of provision.sh that
# LOWER a value, and each must go red.
#
# Runs against throwaway nats-servers of its own on random loopback ports. It
# never dials the live broker, and refuses a port in the broker's, the hub's or
# the leaf's set. Run it under a localhost-only profile with a scratch home.
#
# Usage: probe/nats-phase-0/verify-provision.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
for tool in nats-server nats jq python3; do
  command -v "$tool" >/dev/null || { echo "$tool not on PATH"; exit 2; }
done

pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

work="$(mktemp -d "${TMPDIR:-/tmp}/phase0-provision.XXXXXX")"; pids=()
cleanup() { for p in "${pids[@]:-}"; do kill -TERM "$p" 2>/dev/null || true; done; rm -rf "$work"; }
trap cleanup EXIT

hour=3600000000000
SUBJ='agent.*.*.*.inbox,agent.*.*.role.*.inbox'
mk='--pull --ack explicit --deliver all --defaults'

newport() {
  local port
  port="$(python3 -I -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')"
  case "$port" in 4222|4242|7422|7442|7442|8222) echo "refusing port $port"; exit 2;; esac
  printf '%s' "$port"
}

# broker DIR PORT [AUTH]: start a scratch broker, anonymous or with one admin user.
broker() {
  local dir="$1" port="$2" auth="${3:-}" i
  mkdir -p "$dir/store"
  if [[ -n "$auth" ]]; then
    printf 'listen: 127.0.0.1:%s\njetstream { store_dir: "%s" }\nauthorization { users = [ { user: director_admin, password: scratch-pass } ] }\n' "$port" "$dir/store" > "$dir/nats.conf"
  else
    printf 'listen: 127.0.0.1:%s\njetstream { store_dir: "%s" }\n' "$port" "$dir/store" > "$dir/nats.conf"
  fi
  nats-server -c "$dir/nats.conf" -l "$dir/broker.log" &
  pids+=($!)
  local N=(nats -s "nats://127.0.0.1:$port"); [[ -n "$auth" ]] && N+=(--user director_admin --password scratch-pass)
  for i in $(seq 1 50); do "${N[@]}" server check connection >/dev/null 2>&1 && return 0; sleep 0.2; done
  echo "broker did not come up"; exit 2
}

# run_case SCRIPT LABEL KIND: KIND is one of old (72h), long (200h), unlimited, missing, auth.
run_case() {
  local script="$1" label="$2" kind="$3" port dir="$work/$2-$3"
  port="$(newport)"
  broker "$dir" "$port" "$([[ "$kind" == auth ]] && echo auth)"
  local N=(nats -s "nats://127.0.0.1:$port"); [[ "$kind" == auth ]] && N+=(--user director_admin --password scratch-pass)
  local envs=(DIRECTOR_PHASE0_URL="nats://127.0.0.1:$port"); [[ "$kind" == auth ]] && envs+=(DIRECTOR_PHASE0_ADMIN_USER=director_admin DIRECTOR_PHASE0_ADMIN_PASS=scratch-pass)
  # A nats in front of the real one that records each argv, so a check can see
  # what a process listing would show.
  mkdir -p "$dir/shim"
  printf '#!/bin/bash\nprintf "%%s\\n" "$*" >> "$ARGV_LOG"\nexec "$REAL_NATS" "$@"\n' > "$dir/shim/nats"; chmod +x "$dir/shim/nats"
  envs+=(PATH="$dir/shim:$PATH" ARGV_LOG="$dir/argv.log" REAL_NATS="$(command -v nats)")
  local age
  case "$kind" in old|auth) age=72h;; long) age=200h;; unlimited) age=-1;; missing) age="";; esac
  if [[ -n "$age" ]]; then
    "${N[@]}" stream add AGENT_INBOX --subjects "$SUBJ" --storage file --retention limits --max-age=$age --max-msg-size 65536 --dupe-window 2m --defaults >/dev/null
  fi
  case "$kind" in
    old|auth)
      "${N[@]}" consumer add AGENT_INBOX mcp_a_1 --filter 'agent.w.t.a.inbox' $mk --inactive-threshold 25h >/dev/null
      "${N[@]}" consumer add AGENT_INBOX mcp_b_1 --filter 'agent.w.t.b.inbox' $mk --inactive-threshold 100h >/dev/null
      "${N[@]}" consumer add AGENT_INBOX mcp_c_1 --filter 'agent.w.t.c.inbox' $mk --inactive-threshold 170h >/dev/null
      "${N[@]}" consumer add AGENT_INBOX mcp_d_1 --filter 'agent.w.t.d.inbox' $mk >/dev/null
      "${N[@]}" consumer add AGENT_INBOX hand_made --filter 'agent.w.t.h.inbox' $mk --inactive-threshold 25h >/dev/null ;;
    long)
      "${N[@]}" consumer add AGENT_INBOX mcp_e_1 --filter 'agent.w.t.e.inbox' $mk --inactive-threshold 150h >/dev/null
      "${N[@]}" consumer add AGENT_INBOX mcp_f_1 --filter 'agent.w.t.f.inbox' $mk --inactive-threshold 210h >/dev/null ;;
    unlimited)
      "${N[@]}" consumer add AGENT_INBOX mcp_g_1 --filter 'agent.w.t.g.inbox' $mk --inactive-threshold 25h >/dev/null ;;
  esac

  local sj="$dir/stream.before" cj="$dir/cons.before"
  [[ -n "$age" ]] && "${N[@]}" stream info AGENT_INBOX --json | jq -S 'del(.config.max_age) | .config' > "$sj"
  [[ "$kind" == old || "$kind" == auth ]] && "${N[@]}" consumer info AGENT_INBOX mcp_a_1 --json | jq -S 'del(.config.inactive_threshold) | .config' > "$cj"

  local out1 out2 ma th
  out1="$(env "${envs[@]}" bash "$script" 2>&1)" || { bad "$label/$kind: provision.sh exited nonzero" "$out1"; return; }
  out2="$(env "${envs[@]}" bash "$script" 2>&1)" || { bad "$label/$kind: second provision.sh run exited nonzero" "$out2"; return; }
  ma() { "${N[@]}" stream info "$1" --json | jq -r '.config.max_age'; }
  th() { "${N[@]}" consumer info AGENT_INBOX "$1" --json | jq -r '.config.inactive_threshold // 0'; }
  local k="$label/$kind"
  case "$kind" in
    old|auth)
      [[ "$(ma AGENT_INBOX)" == "$((168*hour))" ]] && ok "$k: a 72h stream is raised to 168h" || bad "$k: a 72h stream is raised to 168h" "got $(ma AGENT_INBOX)"
      [[ "$(th mcp_a_1)" == "$((192*hour))" ]] && ok "$k: a 25h durable is raised to 192h" || bad "$k: a 25h durable is raised to 192h" "got $(th mcp_a_1)"
      [[ "$(th mcp_b_1)" == "$((192*hour))" ]] && ok "$k: a 100h durable is raised to 192h" || bad "$k: a 100h durable is raised to 192h" "got $(th mcp_b_1)"
      [[ "$(th mcp_c_1)" == "$((170*hour))" ]] && ok "$k: a 170h durable (above the 168h max age) is left at 170h" || bad "$k: a 170h durable is left at 170h" "got $(th mcp_c_1)"
      [[ "$(th mcp_d_1)" == "0" ]] && ok "$k: a durable with no threshold keeps none" || bad "$k: a durable with no threshold keeps none" "got $(th mcp_d_1)"
      [[ "$(th hand_made)" == "$((25*hour))" ]] && ok "$k: a consumer that is not the shim's is left alone" || bad "$k: a consumer that is not the shim's is left alone" "got $(th hand_made)"
      "${N[@]}" stream info AGENT_INBOX --json | jq -S 'del(.config.max_age) | .config' > "$dir/stream.after"
      cmp -s "$dir/stream.after" "$sj" && ok "$k: no other stream field changed" || bad "$k: no other stream field changed"
      "${N[@]}" consumer info AGENT_INBOX mcp_a_1 --json | jq -S 'del(.config.inactive_threshold) | .config' > "$dir/cons.after"
      cmp -s "$dir/cons.after" "$cj" && ok "$k: no other durable field changed" || bad "$k: no other durable field changed"
      local changes; changes="$(printf '%s\n' "$out1" | /usr/bin/grep -c ' raised ' || true)"
      [[ "$changes" == "3" ]] && ok "$k: one raised line per change (3 on the first run)" || bad "$k: one raised line per change (3 on the first run)" "got $changes: $out1"
      printf '%s\n' "$out1" | /usr/bin/grep -q 'stream AGENT_INBOX max_age raised 72h -> 168h' && ok "$k: the stream line shows before and after" || bad "$k: the stream line shows before and after" "$out1"
      printf '%s\n' "$out1" | /usr/bin/grep -q 'durable mcp_c_1 on AGENT_INBOX inactive_threshold 170h -> 170h (unchanged)' && ok "$k: an unchanged durable prints its value" || bad "$k: an unchanged durable prints its value" "$out1"
      printf '%s\n' "$out1" | /usr/bin/grep -q '^read-back: stream AGENT_INBOX max_age 168h$' && ok "$k: the run ends with a read-back" || bad "$k: the run ends with a read-back" "$out1"
      printf '%s\n' "$out2" | /usr/bin/grep -q ' raised ' && bad "$k: the second run changes nothing" "$out2" || ok "$k: the second run changes nothing"
      if [[ "$kind" == auth ]]; then
        [[ -s "$dir/argv.log" ]] || bad "$k: the nats shim recorded the calls" "argv.log empty"
        /usr/bin/grep -q -e 'scratch-pass' -e '--password' "$dir/argv.log" && bad "$k: no nats argv carries the password" "$(/usr/bin/grep -e 'scratch-pass' -e '--password' "$dir/argv.log" | head -1 | sed 's/scratch-pass/<password>/')" || ok "$k: no nats argv carries the password"
      fi ;;
    long)
      [[ "$(ma AGENT_INBOX)" == "$((200*hour))" ]] && ok "$k: a 200h stream is left at 200h" || bad "$k: a 200h stream is left at 200h" "got $(ma AGENT_INBOX)"
      [[ "$(th mcp_e_1)" == "$((224*hour))" ]] && ok "$k: a 150h durable is raised to a day above the 200h max age" || bad "$k: a 150h durable is raised to 224h" "got $(th mcp_e_1)"
      [[ "$(th mcp_f_1)" == "$((210*hour))" ]] && ok "$k: a 210h durable is left at 210h" || bad "$k: a 210h durable is left at 210h" "got $(th mcp_f_1)" ;;
    unlimited)
      [[ "$(ma AGENT_INBOX)" == "0" ]] && ok "$k: an unlimited stream is left unlimited" || bad "$k: an unlimited stream is left unlimited" "got $(ma AGENT_INBOX)"
      [[ "$(th mcp_g_1)" == "$((25*hour))" ]] && ok "$k: a durable on an unlimited stream is left alone" || bad "$k: a durable on an unlimited stream is left alone" "got $(th mcp_g_1)" ;;
    missing)
      [[ "$(ma AGENT_INBOX)" == "$((168*hour))" ]] && ok "$k: a missing stream is created at 168h" || bad "$k: a missing stream is created at 168h" "got $(ma AGENT_INBOX)"
      [[ "$("${N[@]}" stream info AGENT_INBOX --json | jq -r '.config.subjects | sort | join(",")')" == "agent.*.*.*.inbox,agent.*.*.role.*.inbox" ]] && ok "$k: the created stream carries both inbox subjects" || bad "$k: the created stream carries both inbox subjects" ;;
  esac
}

for kind in old long unlimited missing auth; do run_case "$here/provision.sh" real "$kind"; done

# The anonymous script against an authorized broker must fail, not pass quietly.
port="$(newport)"; broker "$work/noauth" "$port" auth
if DIRECTOR_PHASE0_URL="nats://127.0.0.1:$port" bash "$here/provision.sh" >/dev/null 2>&1; then
  bad "real: provision.sh without credentials against an authorized broker fails"
else
  ok "real: provision.sh without credentials against an authorized broker fails"
fi

# Mutants: each LOWERS a value, so the cases must fail against it.
mutant() {
  local label="$1" from="$2" to="$3" kinds="$4" file="$work/provision-$1.sh" p0 f0 kind
  sed "s|$from|$to|" "$here/provision.sh" > "$file"
  if cmp -s "$file" "$here/provision.sh"; then echo "mutant $label changed nothing"; exit 2; fi
  p0=$pass; f0=$fail
  for kind in $kinds; do run_case "$file" "$label" "$kind" > "$work/$label-$kind.out" 2>&1 || true; done
  if (( $(cat "$work"/$label-*.out | /usr/bin/grep -c '^FAIL') > 0 )); then
    pass=$p0; fail=$f0
    ok "mutant $label goes red; failing: $(cat "$work"/$label-*.out | /usr/bin/grep '^FAIL' | sed -e "s/^FAIL $label\///" | paste -sd ';' - )"
  else
    pass=$p0; fail=$f0
    bad "mutant $label goes red" "every case still passed"
  fi
}
# A stream edit that lowers: edit whenever the max age is not the declared one.
mutant lowering-stream 'cur > 0 \&\& cur < STREAM_MAX_AGE_NS' 'cur > 0 \&\& cur != STREAM_MAX_AGE_NS' "long"
# The password on the nats command line, where ps shows it.
mutant password-on-argv 'A=(nats -s "$URL")' 'A=(nats -s "$URL" --user "${DIRECTOR_PHASE0_ADMIN_USER:-x}" --password "${DIRECTOR_PHASE0_ADMIN_PASS:-x}")' "auth"
# A durable edit that lowers.
mutant edits-durable-at-or-above-max-age 'th > 0 \&\& th < cur' 'th > 0 \&\& th != cur' "old"

echo "$pass passed, $fail failed"
(( fail == 0 ))
