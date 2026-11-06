#!/usr/bin/env bash
# Prove provision.sh only ever raises (aae-orc-t77rr): an existing stream's finite
# max age below 168h is raised to it, a longer one and an unlimited one are
# left alone, the director's own durables (mcp_global_*) below 192h are raised to
# it, and a longer threshold, none, and a durable of someone else's are left
# alone. A run with no arguments also visits a GLOBAL_TO_ stream the script was never
# told about (aae-orc-acasl), and a stream listing that fails or is not a JSON array
# of names stops it before it creates anything. Then the same
# cases run against mutants of provision.sh, each of which breaks one of those, and
# each must go red.
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
  case "$port" in 3307|4222|4242|7422|7442) echo "refusing port $port"; exit 2;; esac
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

# free_port: a random loopback port that is not the hub's or the local broker's.
free_port() {
  local port
  port="$(python3 -I -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')"
  case "$port" in 3307|4222|4242|7422|7442) echo "refusing port $port" >&2; exit 2;; esac
  echo "$port"
}

# run_noarg SCRIPT LABEL: a fresh broker holding GLOBAL_TO_DIRECTOR and GLOBAL_TO_c3
# (c3 is in no hard-coded list), both at 72h. SCRIPT runs with no arguments, twice.
run_noarg() {
  local script="$1" label="$2" port dir out1 out2
  port="$(free_port)"
  dir="$work/$label"; mkdir -p "$dir/store"
  printf 'listen: 127.0.0.1:%s\njetstream { store_dir: "%s" }\n' "$port" "$dir/store" > "$dir/nats.conf"
  nats-server -c "$dir/nats.conf" -l "$dir/broker.log" &
  pids+=($!)
  local i; for i in $(seq 1 50); do nats -s "nats://127.0.0.1:$port" server check connection >/dev/null 2>&1 && break; sleep 0.2; done

  local N=(nats -s "nats://127.0.0.1:$port")
  "${N[@]}" stream add GLOBAL_TO_DIRECTOR --subjects 'global.director.>' --storage file --retention limits --max-age 72h --max-msg-size 65536 --defaults >/dev/null
  "${N[@]}" stream add GLOBAL_TO_c3 --subjects 'global.c3.>' --storage file --retention limits --max-age 72h --max-msg-size 65536 --defaults >/dev/null

  out1="$(DIRECTOR_GLOBAL_URL="nats://127.0.0.1:$port" DIRECTOR_GLOBAL_ADMIN_NKEY= DIRECTOR_GLOBAL_HOME="$dir" bash "$script" 2>&1)" || { bad "$label: a no-argument run exits 0" "$out1"; return; }
  ok "$label: a no-argument run exits 0"
  out2="$(DIRECTOR_GLOBAL_URL="nats://127.0.0.1:$port" DIRECTOR_GLOBAL_ADMIN_NKEY= DIRECTOR_GLOBAL_HOME="$dir" bash "$script" 2>&1)" || { bad "$label: a second no-argument run exits 0" "$out2"; return; }

  local age; age="$("${N[@]}" stream info GLOBAL_TO_c3 --json | jq -r '.config.max_age')"
  [[ "$age" == "$((168*hour))" ]] && ok "$label: a stream named in no list is raised to 168h" || bad "$label: a stream named in no list is raised to 168h" "got $age"
  printf '%s\n' "$out1" | /usr/bin/grep -q '^stream GLOBAL_TO_c3 found on the hub (not named), visited$' && ok "$label: the run says it found GLOBAL_TO_c3" || bad "$label: the run says it found GLOBAL_TO_c3" "$out1"
  printf '%s\n' "$out1" | /usr/bin/grep -q '^stream GLOBAL_TO_c3 max_age raised ' && ok "$label: the run prints a raised line for GLOBAL_TO_c3" || bad "$label: the run prints a raised line for GLOBAL_TO_c3" "$out1"
  local s; for s in kinu mokuzai; do
    "${N[@]}" stream info "GLOBAL_TO_$s" >/dev/null 2>&1 && ok "$label: GLOBAL_TO_$s exists after the run" || bad "$label: GLOBAL_TO_$s exists after the run" "$out1"
  done
  printf '%s\n' "$out2" | /usr/bin/grep -q ' raised ' && bad "$label: the second no-argument run changes nothing" "$out2" || ok "$label: the second no-argument run changes nothing"
}

# The real script.
run_case "$here/provision.sh" real
run_noarg "$here/provision.sh" real-noarg

# A stand-in nats ahead of the real one on PATH. It passes every call through except
# the one named in STANDIN_MATCH, which prints STANDIN_OUT and exits STANDIN_RC. That
# keeps every other call working, so a script that carries on after the failure would
# create streams, and the cases below can tell.
real_nats="$(command -v nats)"
mkdir -p "$work/bin"
# (printf, not a here document: bash 3.2 writes those under /tmp, which a scratch-only
# sandbox refuses.)
printf '%s\n' '#!/usr/bin/env bash' \
  'if [[ -n "${STANDIN_MATCH:-}" && "$*" == *"$STANDIN_MATCH"* ]]; then' \
  '  printf "%s" "${STANDIN_OUT:-}"; exit "${STANDIN_RC:-1}"' \
  'fi' \
  "exec \"$real_nats\" \"\$@\"" > "$work/bin/nats"
chmod +x "$work/bin/nats"

# run_standin SCRIPT LABEL MATCH OUT RC: a bare broker, SCRIPT run with no arguments
# and the stand-in in force. Leaves the run's exit status in $standin_rc, its
# output in $standin_out, and the port in $standin_port.
run_standin() {
  local script="$1" label="$2" match="$3" out="$4" rc="$5" dir
  standin_port="$(free_port)"
  dir="$work/$label"; mkdir -p "$dir/store"
  printf 'listen: 127.0.0.1:%s\njetstream { store_dir: "%s" }\n' "$standin_port" "$dir/store" > "$dir/nats.conf"
  nats-server -c "$dir/nats.conf" -l "$dir/broker.log" &
  pids+=($!)
  local i; for i in $(seq 1 50); do nats -s "nats://127.0.0.1:$standin_port" server check connection >/dev/null 2>&1 && break; sleep 0.2; done
  standin_out="$(PATH="$work/bin:$PATH" STANDIN_MATCH="$match" STANDIN_OUT="$out" STANDIN_RC="$rc" DIRECTOR_GLOBAL_URL="nats://127.0.0.1:$standin_port" DIRECTOR_GLOBAL_ADMIN_NKEY= DIRECTOR_GLOBAL_HOME="$dir" bash "$script" 2>&1)" && standin_rc=0 || standin_rc=$?
}

# run_badlist SCRIPT LABEL: the stream listing fails, or comes back as something that is
# not a JSON array of names. The stand-in leaves stream add working, so only a script
# that stops at the listing leaves the hub bare. Each case needs a nonzero exit, the
# problem named, and nothing created.
run_badlist() {
  local script="$1" label="$2" name out rc
  local -a names=(failed notjson truncated number error-object)
  local -a outs=("" "not json{" '["GLOBAL_TO_c3"' '[1]' '{"error":"no responders"}')
  local -a rcs=(1 0 0 0 0)
  local n
  for n in 0 1 2 3 4; do
    name="${names[$n]}"
    run_standin "$script" "$label-$name" "stream ls" "${outs[$n]}" "${rcs[$n]}"
    local made; made="$(nats -s "nats://127.0.0.1:$standin_port" stream ls --names --json 2>&1)"
    if (( standin_rc != 0 )); then ok "$label: a $name listing fails the run"; else bad "$label: a $name listing fails the run" "exit 0: $standin_out"; fi
    if printf '%s\n' "$standin_out" | /usr/bin/grep -q 'stream ls \(failed\|returned something other than a JSON array of names\)'; then ok "$label: a $name listing is named"; else bad "$label: a $name listing is named" "$standin_out"; fi
    if [[ "$made" == "null" ]]; then ok "$label: a $name listing creates nothing"; else bad "$label: a $name listing creates nothing" "$made"; fi
  done
}

# A failure on an unnamed stream must not stop the bucket the script always provisions.
run_unnamed_fail() {
  local script="$1" label="$2" N
  local dir="$work/$label-pre"
  # c3 exists on the hub and is in no list; listing its consumers fails.
  standin_port="$(free_port)"; mkdir -p "$dir/store"
  printf 'listen: 127.0.0.1:%s\njetstream { store_dir: "%s" }\n' "$standin_port" "$dir/store" > "$dir/nats.conf"
  nats-server -c "$dir/nats.conf" -l "$dir/broker.log" &
  pids+=($!)
  local i; for i in $(seq 1 50); do nats -s "nats://127.0.0.1:$standin_port" server check connection >/dev/null 2>&1 && break; sleep 0.2; done
  N=(nats -s "nats://127.0.0.1:$standin_port")
  "${N[@]}" stream add GLOBAL_TO_c3 --subjects 'global.c3.>' --storage file --retention limits --max-age 72h --max-msg-size 65536 --defaults >/dev/null
  local out rc
  out="$(PATH="$work/bin:$PATH" STANDIN_MATCH="consumer ls GLOBAL_TO_c3" STANDIN_OUT= STANDIN_RC=1 DIRECTOR_GLOBAL_URL="nats://127.0.0.1:$standin_port" DIRECTOR_GLOBAL_ADMIN_NKEY= DIRECTOR_GLOBAL_HOME="$dir" bash "$script" 2>&1)" && rc=0 || rc=$?
  (( rc != 0 )) && ok "$label: a failure on an unnamed stream still fails the run" || bad "$label: a failure on an unnamed stream still fails the run" "$out"
  local s; for s in GLOBAL_TO_DIRECTOR GLOBAL_TO_kinu GLOBAL_TO_mokuzai; do
    "${N[@]}" stream info "$s" >/dev/null 2>&1 && ok "$label: $s is provisioned before the unnamed stream" || bad "$label: $s is provisioned before the unnamed stream" "$out"
  done
  "${N[@]}" kv info GLOBAL_PRESENCE >/dev/null 2>&1 && ok "$label: GLOBAL_PRESENCE is provisioned before the unnamed stream" || bad "$label: GLOBAL_PRESENCE is provisioned before the unnamed stream" "$out"
}

# A stream named GLOBAL_TO_ with nothing after it has no cluster to visit; the run says
# so and carries on.
run_emptysuffix() {
  local script="$1" label="$2"
  run_standin "$script" "$label" "stream ls" '["GLOBAL_TO_"]' 0
  if (( standin_rc == 0 )); then ok "$label: an empty cluster name does not fail the run"; else bad "$label: an empty cluster name does not fail the run" "$standin_out"; fi
  if printf '%s\n' "$standin_out" | /usr/bin/grep -q '^stream GLOBAL_TO_ has no cluster name, not visited$'; then ok "$label: an empty cluster name is reported"; else bad "$label: an empty cluster name is reported" "$standin_out"; fi
}

run_badlist "$here/provision.sh" real-badlist
run_emptysuffix "$here/provision.sh" real-emptysuffix
run_unnamed_fail "$here/provision.sh" real-unnamed

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

# mutant_with RUNNER LABEL FROM TO: a mutant judged by RUNNER alone (run_noarg,
# run_badlist or run_unnamed_fail), because run_case names every stream it builds and
# would not notice. The mutant's own PASS/FAIL lines go to a file and its counters are
# put back; the only result that counts is whether the cases noticed.
mutant_with() {
  local runner="$1" label="$2" from="$3" to="$4" file="$work/provision-$2.sh" p0 f0
  sed "s|$from|$to|" "$here/provision.sh" > "$file"
  if cmp -s "$file" "$here/provision.sh"; then echo "mutant $label changed nothing"; exit 2; fi
  p0=$pass; f0=$fail
  "$runner" "$file" "$label" > "$work/$label.out" 2>&1 || true
  if (( fail > f0 )); then
    pass=$p0; fail=$f0
    ok "mutant $label goes red; failing: $(/usr/bin/grep '^FAIL' "$work/$label.out" | sed -e "s/^FAIL $label[a-z-]*: //" | cut -c1-90 | paste -sd ';' - )"
  else
    pass=$p0; fail=$f0
    bad "mutant $label goes red" "every case still passed"
  fi
}
# Discovery removed: the listing is read as an empty array.
mutant_with run_noarg hardcoded-default '"\$listing"' '"[]"'
# A failed listing prints its message and carries on with an empty listing.
mutant_with run_badlist print-and-carry-on 'stream ls failed" >\&2; exit 1;' 'stream ls failed" >\&2; listing=null;'
# A listing that is not an array of names is read as empty instead of an error.
mutant_with run_badlist shape-unchecked 'else error("stream ls did not return an array of names")' 'else empty'
# The unnamed streams are visited before the presence bucket again.
mutant_with run_unnamed_fail visit-before-bucket '^if "\${A\[@\]}" kv info' 'for c in ${extra[@]+"${extra[@]}"}; do add_stream "GLOBAL_TO_$c" x; done; if "${A[@]}" kv info'

echo "$pass passed, $fail failed"
(( fail == 0 ))
