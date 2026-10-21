#!/usr/bin/env bash
# Prove freebusy (idle-scheduler B3): the read-only free/busy table that runs in
# sweep step 3, after dbi. Each case builds a scratch home and a scratch
# DIRECTOR_STATE, puts stubs for marvel and director-mcp first on PATH, and runs the
# script under `env -i` with no MARVEL_SOCKET or MARVEL_HOME. No real cluster is reached:
# the marvel stub answers `config list` and `get ... --cluster NAME` from fixtures and logs
# every call and the MARVEL_* variables it saw. ssh, bd and gh stubs record any call; the
# script must make none.
#
# Usage: skills/director/scripts/verify-freebusy.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FB="${FB:-$here/freebusy}"
BASHES="${BASHES:-/bin/bash bash}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d "${TMPDIR:-/tmp}/fb.XXXXXX")"
root="$(cd "$root" && pwd -P)"
bgpids=()
cleanup() {
  local p
  for p in ${bgpids[@]+"${bgpids[@]}"}; do
    if kill -0 "$p" 2>/dev/null; then kill "$p" 2>/dev/null || true; fi
  done
  rm -rf "$root"
}
trap cleanup EXIT

mkdir -p "$root/bin" "$root/fix" "$root/home"
FIX="$root/fix"

# --- stubs ------------------------------------------------------------------
cat >"$root/bin/marvel" <<'S'
#!/usr/bin/env bash
echo "$*" >>"$FB_FIX/marvel.log"
echo "MARVEL_SOCKET=${MARVEL_SOCKET-unset} MARVEL_HOME=${MARVEL_HOME-unset}" >>"$FB_FIX/marvel.env"
usage17() { local i; for i in $(seq 1 17); do echo "usage line $i" >&2; done; }
for a in "$@"; do
  case "$a" in --socket|--socket=*) echo "stub marvel: --socket is forbidden" >&2; exit 2 ;; esac
done
if [[ "$1 $2" == "config list" ]]; then
  if [[ -e "$FB_FIX/config.listslow" ]]; then echo "$$" >>"$FB_FIX/slow.pids"; sleep "$(cat "$FB_FIX/config.listslow")"; fi
  if [[ -e "$FB_FIX/config.listfail" ]]; then echo "Error: cannot read the cluster config" >&2; echo "second stderr line" >&2; usage17; exit 1; fi
  cat "$FB_FIX/config.list"; exit 0
fi
h=""; prev=""
for a in "$@"; do
  if [[ "$prev" == --cluster ]]; then h="$a"; fi
  prev="$a"
done
[[ -n "$h" ]] || { echo "stub marvel: no --cluster in: $*" >&2; exit 2; }
if [[ -e "$FB_FIX/$h.down" ]]; then echo "Error: dial mrvl://$h: connection refused" >&2; usage17; exit 1; fi
if [[ -e "$FB_FIX/$h.slow" && ! -e "$FB_FIX/$h.slept" ]]; then
  echo "$$" >>"$FB_FIX/slow.pids"
  sleep "$(cat "$FB_FIX/$h.slow")"
fi
if [[ "$1 $2" == "get sessions" ]]; then
  if [[ "$*" == *last-turn* && -e "$FB_FIX/$h.noturn" ]]; then echo 'Error: unknown column "last-turn"' >&2; exit 1; fi
  if [[ "$*" == *last-turn* ]]; then
    if [[ -e "$FB_FIX/$h.sessions.turn" ]]; then cat "$FB_FIX/$h.sessions.turn"; exit 0; fi
    echo 'Error: unknown column "last-turn"' >&2; exit 1
  fi
  cat "$FB_FIX/$h.sessions"; exit 0
fi
if [[ "$1 $2" == "get teams" ]]; then
  if [[ -e "$FB_FIX/$h.teamsfail" ]]; then echo 'Error: daemon busy' >&2; exit 1; fi
  cat "$FB_FIX/$h.teams"; exit 0
fi
echo "stub marvel: unexpected $*" >&2; exit 2
S
cat >"$root/bin/director-mcp" <<'S'
#!/usr/bin/env bash
echo "$*" >>"$FB_FIX/mcp.log"
if [[ "$*" != "asks --json" ]]; then echo "stub director-mcp: unexpected $*" >&2; exit 2; fi
if [[ -e "$FB_FIX/asks.old" ]]; then echo "director-mcp: unknown mode asks" >&2; exit 2; fi
cat "$FB_FIX/asks.json"
S
for t in bd gh ssh; do
  cat >"$root/bin/$t" <<'S'
#!/usr/bin/env bash
echo "$(basename "$0") $*" >>"$FB_FIX/forbidden.log"
exit 1
S
done
chmod +x "$root/bin/"*

# --- fixtures ---------------------------------------------------------------
NOW=1792541100
HDR='WORKSPACE  TEAM  ROLE  AGENT NAME  LAST-ACTIVE  ACTIVE%  STATE  HEALTH'
sess() { # sess FILE LINES...
  local f="$FIX/$1"; shift
  { echo "$HDR"; printf '%s\n' "$@"; } >"$f"
}
teams() { # teams FILE LINES...
  local f="$FIX/$1"; shift
  { echo 'WORKSPACE  TEAM  ROLE  REPLICAS  RUNTIME'; printf '%s\n' "$@"; } >"$f"
}
fixtures() {
  rm -f "$FIX"/config.list* "$FIX"/*.exclude "$FIX"/marvel.env "$FIX"/forbidden.log "$FIX"/*.slept "$FIX"/*.teamsfail "$FIX"/*.down "$FIX"/*.slow "$FIX"/*.noturn "$FIX"/*.turn "$FIX"/asks.old "$FIX"/*.log "$FIX/slow.pids"
  sess alpha.sessions \
    'wsa crew builder crew-builder-1 6s* 100%* running healthy' \
    'wsa crew builder crew-builder-2 4m 0% running healthy' \
    'wsa crew architect crew-architect-1 9m 0% running healthy'
  teams alpha.teams \
    'wsa crew builder 2 agent' \
    'wsa crew architect 1 agent'
  sess beta.sessions \
    'wsb desk builder desk-builder-1 3s 60% limited healthy' \
    'wsb desk reviewer desk-reviewer-1 20m 0% running healthy'
  teams beta.teams \
    'wsb desk builder 1 agent' \
    'wsb desk reviewer 1 agent' \
    'wsb desk spare 0 agent'
  sess gamma.sessions \
    'wsc ops supervisor ops-supervisor-1 1m 12% running healthy' \
    'wsc ops envoy ops-envoy-1 - - running healthy'
  teams gamma.teams \
    'wsc ops supervisor 1 agent' \
    'wsc ops envoy 1 agent'
  printf '%s\n' 'NAME  ADDRESS  IDENTITY' '* alpha  mrvl://alpha.invalid:7600  /keys/a' 'beta  mrvl://beta.invalid:7600  /keys/b' 'gamma  mrvl://gamma.invalid:7600  /keys/c' >"$FIX/config.list"
  printf '%s\n' \
    "{\"generated_at\":\"2026-10-21T00:00:00Z\",\"partial\":false,\"rollup_by_owner\":[{\"key\":\"crew/architect\",\"open\":0,\"unacked\":0,\"blocked\":0,\"oldest_open_seconds\":0}],\"rollup_by_asker\":[],\"gaps\":{\"streams_not_read\":[],\"reader_down\":[]}}" \
    >"$FIX/asks.json"
}

# Run the script the way a sweep does: scrubbed environment, scratch home and
# state, no MARVEL_SOCKET, the stubs first on PATH. Prints stdout; stderr to
# $root/err; exit code to $root/rc.
state="$root/state"
run() { # run [VAR=value ...]
  local b="${FB_BASH:-/bin/bash}"
  set +e
  env -i HOME="$root/home" PATH="$root/bin:/usr/bin:/bin:/opt/homebrew/bin" LANG=C TMPDIR="$root" \
    DIRECTOR_STATE="$state" FB_FIX="$FIX" FREEBUSY_TEST_CLOCK=1 FREEBUSY_NOW="$NOW" \
    FREEBUSY_ASKS_MAX_AGE=900 FREEBUSY_CALL_TIMEOUT=4 "$@" "$b" "$FB" 2>"$root/err"
  echo $? >"$root/rc"
  set -e
}
conf3() { :; }  # the three clusters come from the fixture config list
new_state() { rm -rf "$state"; mkdir -p "$state"; }
jqf() { jq "$@" 2>/dev/null || true; }
count() { grep -c -- "$1" <<<"$2" || true; }
mode_of() { stat -c %a "$1" 2>/dev/null || stat -f %Lp "$1"; }
seat() { jq -r --arg n "$2" 'select(.name==$n) | .state' "$state/freebusy/$1.jsonl" 2>/dev/null || true; }

# --- cases ------------------------------------------------------------------
case_all_read() {
  fixtures; new_state; conf3
  out="$(run FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp")"
  [[ "$(cat "$root/rc")" == 0 ]] && ok "1  exits 0" || bad "1  exits 0" "$(cat "$root/rc")"
  grep -q 'clusters: 3 of 3 read' <<<"$out" && ok "1  count line says 3 of 3 read" || bad "1  count line" "$out"
  [[ "$(seat seats crew-builder-1)" == busy ]] && ok "1  active seat reads busy" || bad "1  active seat" "$(seat seats crew-builder-1)"
  [[ "$(seat seats crew-builder-2)" == free ]] && ok "1  idle seat reads free" || bad "1  idle seat" "$(seat seats crew-builder-2)"
  [[ "$(seat seats ops-envoy-1)" == unknown ]] && ok "1  seat with no activity reading is unknown, not free" || bad "1  unknown seat" "$(seat seats ops-envoy-1)"
  [[ "$(seat seats desk-builder-1)" == waiting ]] && ok "1  rate-limited seat reads waiting" || bad "1  limited seat" "$(seat seats desk-builder-1)"
}

case_parked() {
  fixtures; new_state; conf3
  run FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp" >/dev/null
  local n
  n="$(jqf -r 'select(.role=="spare") | .state' "$state/freebusy/seats.jsonl")"
  [[ "$n" == waiting ]] && ok "3  parked role (replicas 0) reads waiting" || bad "3  parked" "$n"
  n="$(jqf -r 'select(.role=="spare") | .reason' "$state/freebusy/seats.jsonl")"
  [[ "$n" == parked ]] && ok "3  parked seat carries its reason" || bad "3  parked reason" "$n"
}

case_cluster_down() {
  fixtures; new_state; conf3
  run FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp" >/dev/null
  touch "$FIX/gamma.down"
  out="$(run FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp")"
  grep -q 'clusters: 2 of 3 read' <<<"$out" && ok "2  one cluster down prints 2 of 3 read" || bad "2  count" "$out"
  grep -q 'GAP cluster gamma' <<<"$out" && ok "2  the unread cluster is named" || bad "2  gap named" "$out"
  grep -q 'dial mrvl://gamma: connection refused' <<<"$out" && ! grep -q 'usage line' <<<"$out" && ok "2  the GAP carries the first stderr line only, none of the usage block" || bad "2  first line only" "$out"
  [[ "$(grep -c 'GAP cluster gamma' <<<"$out")" == 1 ]] && ok "2  one GAP line for the unread cluster" || bad "2  one line" "$out"
  [[ "$(seat seats ops-supervisor-1)" == unknown ]] && ok "2  its seats read unknown" || bad "2  seat unknown" "$(seat seats ops-supervisor-1)"
  [[ "$(seat seats crew-builder-2)" == free ]] && ok "2  readable clusters unchanged" || bad "2  alpha seat" "$(seat seats crew-builder-2)"
  # a cluster whose teams read fails after its sessions read is not read
  rm -f "$FIX/gamma.down"; touch "$FIX/gamma.teamsfail"
  out="$(run FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp")"
  grep -q 'clusters: 2 of 3 read' <<<"$out" && ok "2  a failed teams read leaves the cluster unread" || bad "2  teams fail" "$out"
  rm -f "$FIX/gamma.teamsfail"; touch "$FIX/gamma.down"
  run FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp" >/dev/null
  [[ "$(jqf -r 'select(.lane=="ops/supervisor") | .state' "$state/freebusy/lanes.jsonl")" == unknown ]] \
    && ok "2  its lane reads unknown" || bad "2  lane unknown"
  # first run with a cluster down and no history: no row and no zero count for it
  new_state
  out="$(run FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp")"
  grep -q 'GAP cluster gamma' <<<"$out" && ok "2  no history: GAP, not a zero" || bad "2  no history" "$out"
  grep -q 'not listed: gamma' <<<"$out" && ok "2  no history: count line says which cluster is not listed" || bad "2  not listed" "$out"
  [[ -z "$(jqf -r 'select(.cluster=="gamma")|.name' "$state/freebusy/seats.jsonl")" ]] && ok "2  no invented gamma rows" || bad "2  invented rows"
}

case_asks() {
  fixtures; new_state; conf3
  local base=(FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp")
  # queued asks, idle panes
  printf '%s\n' "{\"generated_at\":\"2026-10-21T00:00:00Z\",\"partial\":false,\"rollup_by_owner\":[{\"key\":\"crew/architect\",\"open\":2,\"unacked\":1,\"blocked\":0,\"oldest_open_seconds\":300}],\"rollup_by_asker\":[],\"gaps\":{}}" >"$FIX/asks.json"
  out="$(run "${base[@]}")"
  [[ "$(seat seats crew-architect-1)" == free ]] && ok "5  the architect pane reads free" || bad "5  pane" "$(seat seats crew-architect-1)"
  [[ "$(jqf -r 'select(.lane=="crew/architect") | .state' "$state/freebusy/lanes.jsonl")" == busy ]] \
    && ok "5  a lane with queued asks reads busy while its panes read idle" || bad "5  lane busy" "$(cat "$state/freebusy/lanes.jsonl")"
  ! grep -q 'GAP asks' <<<"$out" && ok "4  good asks read prints no GAP asks" || bad "4  clean" "$out"
  # a director-mcp too old to know the asks subcommand
  touch "$FIX/asks.old"
  out="$(run "${base[@]}")"
  grep -q 'GAP asks' <<<"$out" && ok "4  stale director-mcp prints GAP asks" || bad "4  stale" "$out"
  grep -q 'director-mcp exited 2' <<<"$out" && ok "4  and names its exit status" || bad "4  exit named" "$out"
  [[ "$(jqf -r 'select(.lane=="crew/architect") | .asks_open' "$state/freebusy/lanes.jsonl")" == GAP ]] \
    && ok "4  the lane's ask cells read GAP, not 0" || bad "4  asks cell" "$(cat "$state/freebusy/lanes.jsonl")"
  [[ "$(jqf -r 'select(.lane=="crew/architect") | .state' "$state/freebusy/lanes.jsonl")" == unknown ]] \
    && ok "4  an idle lane with no ask reading is unknown, not free" || bad "4  lane unknown"
  rm -f "$FIX/asks.old"
  # a missing binary
  out="$(run FREEBUSY_DIRECTOR_MCP="$root/nope")"
  grep -q 'GAP asks' <<<"$out" && ok "4  missing director-mcp prints GAP asks" || bad "4  missing" "$out"
  # a partial ledger, an old ledger, and output that is not JSON
  printf '%s\n' "{\"generated_at\":\"2026-10-21T00:00:00Z\",\"partial\":true,\"rollup_by_owner\":[]}" >"$FIX/asks.json"
  out="$(run "${base[@]}")"; grep -q 'GAP asks' <<<"$out" && ok "4  partial ledger prints GAP asks" || bad "4  partial" "$out"
  printf '%s\n' "{\"generated_at\":\"2026-10-20T00:00:00Z\",\"partial\":false,\"rollup_by_owner\":[]}" >"$FIX/asks.json"
  out="$(run "${base[@]}")"; grep -q 'GAP asks' <<<"$out" && ok "4  an old ledger prints GAP asks" || bad "4  old" "$out"
  echo 'not json' >"$FIX/asks.json"
  out="$(run "${base[@]}")"; grep -q 'GAP asks' <<<"$out" && ok "4  unparseable output prints GAP asks" || bad "4  junk" "$out"
}

case_modes() {
  fixtures; new_state; conf3
  run FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp" >/dev/null
  local d="$state/freebusy" f
  [[ "$(mode_of "$d")" == 700 ]] && ok "6  directory is mode 700" || bad "6  dir mode" "$(mode_of "$d")"
  for f in seats.jsonl lanes.jsonl flow.jsonl; do
    [[ -f "$d/$f" && "$(mode_of "$d/$f")" == 600 ]] && ok "6  $f is mode 600" || bad "6  $f mode" "$(mode_of "$d/$f" 2>&1)"
  done
  [[ -z "$(find "$d" -mindepth 1 ! -name seats.jsonl ! -name lanes.jsonl ! -name flow.jsonl)" ]] \
    && ok "6  nothing else is left in the directory (lock and temp files gone)" || bad "6  leftovers" "$(ls -A "$d")"
  git -C "$state" rev-parse --is-inside-work-tree >/dev/null 2>&1 \
    && bad "6  scratch state is inside a checkout" || ok "6  scratch state is outside any checkout"
  # a state directory inside a checkout is refused before anything is written
  rm -rf "$root/repo"; mkdir -p "$root/repo/state"; git -C "$root/repo" init -q
  out="$(run DIRECTOR_STATE="$root/repo/state" FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp")"
  grep -q 'not run: .*inside a checkout' <<<"$out" && ok "6  a state directory inside a checkout is refused" || bad "6  checkout" "$out"
  [[ ! -e "$root/repo/state/freebusy" ]] && ok "6  and nothing was written there" || bad "6  wrote in checkout"
  # an existing directory with loose modes is tightened
  chmod 755 "$d" 2>/dev/null || true; chmod 644 "$d/seats.jsonl" 2>/dev/null || true
  run FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp" >/dev/null
  [[ "$(mode_of "$d")" == 700 && "$(mode_of "$d/seats.jsonl")" == 600 ]] && ok "6  loose modes are tightened on the next run" || bad "6  tighten"
}

case_no_writes() {
  fixtures; new_state; conf3
  run FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp" >/dev/null
  [[ ! -e "$FIX/forbidden.log" ]] && ok "7  no bd, gh or ssh call was made" || bad "7  forbidden call" "$(cat "$FIX/forbidden.log")"
  if grep -qvE '^(config list$|get (sessions|teams) --cluster (alpha|beta|gamma)( |$))' "$FIX/marvel.log"; then bad "7  marvel used for more than config list and get --cluster" "$(cat "$FIX/marvel.log")"; else ok "7  marvel was only asked config list and get --cluster NAME"; fi
  [[ "$(grep -c '^get ' "$FIX/marvel.log")" == "$(grep -c '^get .*--cluster ' "$FIX/marvel.log")" ]] && ok "7  every get names its cluster" || bad "7  a get without --cluster"
  grep -q -- '--socket' "$FIX/marvel.log" && bad "7  a call carried --socket" || ok "7  no call carries --socket"
  [[ "$(sort -u "$FIX/marvel.env")" == "MARVEL_SOCKET=unset MARVEL_HOME=unset" ]] && ok "7  marvel saw no MARVEL_SOCKET or MARVEL_HOME that the script set" || bad "7  marvel env" "$(sort -u "$FIX/marvel.env")"
  [[ "$(sort -u "$FIX/mcp.log")" == "asks --json" ]] && ok "7  director-mcp was only asked for asks --json" || bad "7  mcp" "$(cat "$FIX/mcp.log")"
  if grep -vE '^[[:space:]]*#' "$FB" | grep -qE 'MARVEL_SOCKET|MARVEL_HOME|--socket'; then bad "7  the script text sets or names MARVEL_SOCKET, MARVEL_HOME or --socket"; else ok "7  the script text never sets MARVEL_SOCKET, MARVEL_HOME or --socket"; fi
  # the script text: no bus send, inject, bd write or gh call (comments excluded)
  local code hits
  code="$(grep -vE '^[[:space:]]*#' "$FB" || true)"
  hits="$(grep -nE 'send_message|broadcast|nats |inject|\bbd\b|\bgh\b|marvel (inject|work|scale|kill|delete|shift)|git (push|commit)' <<<"$code" || true)"
  [[ -z "$hits" ]] && ok "7  grep finds no bus send, inject, bd write or gh call" || bad "7  grep" "$hits"
}

case_concurrent() {
  fixtures; new_state; conf3
  echo 3 >"$FIX/alpha.slow"
  ( env -i HOME="$root/home" PATH="$root/bin:/usr/bin:/bin:/opt/homebrew/bin" LANG=C TMPDIR="$root" DIRECTOR_STATE="$state" \
      FB_FIX="$FIX" FREEBUSY_TEST_CLOCK=1 FREEBUSY_NOW="$NOW" FREEBUSY_CALL_TIMEOUT=10 \
      FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp" /bin/bash "$FB" >"$root/first.out" 2>&1 ) &
  local p=$!; bgpids+=("$p")
  local i=0
  while [[ ! -s "$FIX/slow.pids" && $i -lt 50 ]]; do
    sleep 0.1
    i=$((i+1))
  done
  out="$(run FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp")"
  grep -q 'not run: sweep in progress' <<<"$out" && ok "8  a second concurrent run prints not run: sweep in progress" || bad "8  concurrent" "$out"
  [[ "$(cat "$root/rc")" == 0 ]] && ok "8  and exits 0" || bad "8  rc" "$(cat "$root/rc")"
  wait "$p" || true
  grep -q 'clusters: 3 of 3 read' "$root/first.out" && ok "8  the first run finished normally" || bad "8  first run" "$(cat "$root/first.out")"
  [[ "$(wc -l <"$state/freebusy/flow.jsonl" 2>/dev/null | tr -d ' ')" == 1 ]] && ok "8  the refused run wrote no flow line" || bad "8  flow lines"
  rm -f "$FIX/alpha.slow"
  out="$(run FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp")"
  grep -q 'clusters: 3 of 3 read' <<<"$out" && ok "8  the lock is released after a run" || bad "8  lock released" "$out"
  # a lock left by a dead process is taken over
  mkdir -p "$state/freebusy/.lock"; echo 999999 >"$state/freebusy/.lock/pid"
  out="$(run FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp")"
  grep -q 'clusters: 3 of 3 read' <<<"$out" && ok "8  a lock whose process is gone is taken over" || bad "8  stale lock" "$out"
}

case_hung_read() {
  fixtures; new_state; conf3
  echo 30 >"$FIX/beta.slow"
  local t0 t1
  t0=$(date +%s)
  out="$(run FREEBUSY_CALL_TIMEOUT=2 FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp")"
  t1=$(date +%s)
  grep -q 'clusters: 2 of 3 read' <<<"$out" && ok "9  a hung read reads as unread" || bad "9  hung" "$out"
  [[ $((t1-t0)) -lt 15 ]] && ok "9  the run is bounded by the call timeout" || bad "9  bound" "$((t1-t0))s"
  local pid alive=0
  while read -r pid; do
    if kill -0 "$pid" 2>/dev/null; then alive=1; fi
  done < <(cat "$FIX/slow.pids" 2>/dev/null || true)
  [[ "$alive" == 0 ]] && ok "9  the hung call's process is gone" || bad "9  process left behind"
  rm -f "$FIX/beta.slow"
}

case_flow_and_turn() {
  fixtures; new_state; conf3
  local base=(FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp")
  run "${base[@]}" >/dev/null
  local first; first="$(head -1 "$state/freebusy/flow.jsonl" 2>/dev/null || true)"
  run "${base[@]}" >/dev/null
  [[ "$(wc -l <"$state/freebusy/flow.jsonl" 2>/dev/null | tr -d ' ')" == 2 ]] && ok "10 flow.jsonl gains one line per run" || bad "10 flow count"
  [[ "$(head -1 "$state/freebusy/flow.jsonl" 2>/dev/null || true)" == "$first" ]] && ok "10 earlier flow lines are never rewritten" || bad "10 flow rewritten"
  jq -e '.at and (.clusters_read|type=="number") and .seats' <<<"$first" >/dev/null && ok "10 the flow line carries time, clusters read and seat counts" || bad "10 flow fields" "$first"
  # LAST-TURN: where installed it is carried; where it is not, the read falls back
  [[ "$(jqf -r 'select(.name=="crew-builder-2")|.last_turn' "$state/freebusy/seats.jsonl")" == null ]] && ok "11 no LAST-TURN column: last_turn is null, not 0" || bad "11 null turn"
  { echo 'WORKSPACE  TEAM  ROLE  AGENT NAME  LAST-ACTIVE  ACTIVE%  LAST-TURN  STATE  HEALTH'; echo 'wsa crew builder crew-builder-2 4m 0% 7m running healthy'; } >"$FIX/alpha.sessions.turn"
  run "${base[@]}" >/dev/null
  [[ "$(jqf -r 'select(.name=="crew-builder-2")|.last_turn' "$state/freebusy/seats.jsonl")" == 7m ]] && ok "11 LAST-TURN is carried where installed" || bad "11 turn carried"
  [[ "$(seat seats crew-builder-2)" == free ]] && ok "11 and the state after the LAST-TURN cell is still read as state" || bad "11 state after turn" "$(seat seats crew-builder-2)"
  touch "$FIX/alpha.noturn"
  out="$(run "${base[@]}")"
  grep -q 'clusters: 3 of 3 read' <<<"$out" && ok "11 a marvel without the column still reads" || bad "11 fallback" "$out"
}

case_conf() {
  fixtures; new_state
  touch "$FIX/config.listfail"
  out="$(run FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp")"
  grep -q 'GAP clusters: marvel config list failed (Error: cannot read the cluster config)' <<<"$out" && ok "12 a failing config list prints the clusters GAP with its first stderr line" || bad "12 list fails" "$out"
  ! grep -qE 'second stderr line|usage line' <<<"$out" && ok "12 and none of the rest of its stderr" || bad "12 stderr" "$out"
  [[ "$(cat "$root/rc")" == 0 ]] && ok "12 and exits 0" || bad "12 rc"
  # the exclude file's trailing comment or blank line must not end the run early
  fixtures; new_state
  printf '%s\n' 'beta' '' '# trailing comment' >"$state/freebusy-clusters.exclude"
  out="$(run FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp")"
  grep -q 'clusters: 2 of 2 read' <<<"$out" && ok "12 a trailing comment line in the exclude file does not end the run" || bad "12 trailing comment" "$out"
  # the current-cluster marker is not a name
  fixtures; new_state
  run FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp" >/dev/null
  { grep -q -- '--cluster alpha ' "$FIX/marvel.log" && ! grep -qE -- "--cluster \*" "$FIX/marvel.log"; } && ok "12 the * marker beside the current cluster is not read as a name" || bad "12 marker" "$(head -3 "$FIX/marvel.log")"
  [[ "$(grep -c '^get sessions' "$FIX/marvel.log")" -ge 3 ]] && ok "12 and every listed cluster is read" || bad "12 three clusters"
}

case_discovery() {
  fixtures; new_state
  # exclude: a listed name is skipped with its NOTE, an unknown name gives its NOTE
  printf '%s\n' '# the alias' 'beta' 'nope' >"$state/freebusy-clusters.exclude"
  out="$(run FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp")"
  grep -q 'NOTE cluster beta: excluded' <<<"$out" && ok "17 an excluded cluster is skipped with its NOTE" || bad "17 excluded" "$out"
  grep -q 'NOTE exclude: nope is not a configured cluster' <<<"$out" && ok "17 an unknown excluded name gives its NOTE" || bad "17 unknown" "$out"
  grep -q 'clusters: 2 of 2 read' <<<"$out" && ok "17 the count is over the clusters not excluded" || bad "17 count" "$out"
  grep -q -- '--cluster beta ' "$FIX/marvel.log" && bad "17 the excluded cluster was read" || ok "17 the excluded cluster was never read"
  # every configured cluster excluded
  fixtures; new_state
  printf '%s\n' alpha beta gamma >"$state/freebusy-clusters.exclude"
  out="$(run FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp")"
  grep -q 'GAP clusters: every configured cluster is excluded' <<<"$out" && ok "17 all clusters excluded prints a GAP, not an empty count" || bad "17 all excluded" "$out"
  # the old list is retired: a NOTE, and its names are not read
  fixtures; new_state
  printf '%s\n' 'zeta zeta' >"$state/freebusy-clusters.conf"
  out="$(run FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp")"
  grep -q 'NOTE clusters: freebusy-clusters.conf is no longer read' <<<"$out" && ok "17 an old freebusy-clusters.conf prints a NOTE" || bad "17 retired" "$out"
  grep -q 'zeta' "$FIX/marvel.log" && bad "17 a name from the old list was read" || ok "17 and its names are not read"
  # no old list, no exclude file: no NOTE
  fixtures; new_state
  out="$(run FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp")"
  ! grep -q 'NOTE' <<<"$out" && ok "17 with neither file there is no NOTE" || bad "17 stray note" "$out"
  # positive control: one reachable cluster reads 1 of 1
  fixtures; new_state
  printf '%s\n' 'NAME  ADDRESS  IDENTITY' 'alpha  mrvl://alpha.invalid:7600  /keys/a' >"$FIX/config.list"
  out="$(run FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp")"
  grep -q 'clusters: 1 of 1 read' <<<"$out" && ok "17 positive control: one reachable cluster reads 1 of 1" || bad "17 control" "$out"
  # daemon down answers at once: one GAP line, and no wait for the bound
  fixtures; new_state
  touch "$FIX/gamma.down"
  local t0 t1; t0=$(date +%s)
  out="$(run FREEBUSY_CALL_TIMEOUT=20 FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp")"; t1=$(date +%s)
  { grep -q 'clusters: 2 of 3 read' <<<"$out" && [[ $((t1-t0)) -lt 10 ]]; } && ok "17 a daemon that is down is read as unread at once" || bad "17 down" "$((t1-t0))s $out"
  # a hang after connect: bounded, named
  fixtures; new_state
  echo 30 >"$FIX/beta.slow"
  out="$(run FREEBUSY_CALL_TIMEOUT=2 FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp")"
  grep -q 'GAP cluster beta: not read (no answer within 2s)' <<<"$out" && ok "17 a hang after connect prints no answer within the bound" || bad "17 hang" "$out"
  # the config list itself hangs
  fixtures; new_state
  echo 30 >"$FIX/config.listslow"
  out="$(run FREEBUSY_CALL_TIMEOUT=2 FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp")"
  grep -q 'GAP clusters: marvel config list failed (no answer within 2s)' <<<"$out" && ok "17 a config list that hangs is bounded by the call timeout" || bad "17 list hang" "$out"
}

case_formats() {
  fixtures; new_state; conf3
  # marvel's own cell forms: HEALTH suffixes and STATE suffixes add words to a row
  printf '%s\n' \
    'wsa crew qa crew-qa-1 2m 0% running healthy (stalled)' \
    'wsa crew qa crew-qa-2 5m 0% running healthy (parked: waiting for a person)' \
    'wsa crew qa crew-qa-3 1m 30% running unhealthy (logged-out)' \
    'wsa crew qa crew-qa-4 - - failed (crash) unhealthy' \
    'wsa crew job crew-job-1 - - succeeded (exit 0) healthy' >>"$FIX/alpha.sessions"
  run FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp" >/dev/null
  [[ "$(seat seats desk-builder-1)" == waiting ]] && ok "13 limited is read from STATE" || bad "13 limited" "$(seat seats desk-builder-1)"
  [[ "$(seat seats crew-qa-1)" == free ]] && ok "13 a (stalled) suffix does not hide the reading" || bad "13 stalled" "$(seat seats crew-qa-1)"
  [[ "$(jqf -r 'select(.name=="crew-qa-1")|.last_active' "$state/freebusy/seats.jsonl")" == 2m ]] && ok "13 last_active holds the cell, not the suffix" || bad "13 stalled last_active"
  [[ "$(seat seats crew-qa-2)" == waiting ]] && ok "13 a (parked: reason) seat reads waiting" || bad "13 parked" "$(seat seats crew-qa-2)"
  [[ "$(jqf -r 'select(.name=="crew-qa-2")|.last_active' "$state/freebusy/seats.jsonl")" == 5m ]] && ok "13 and last_active is still the cell" || bad "13 parked last_active"
  [[ "$(seat seats crew-qa-3)" == busy ]] && ok "13 a (logged-out) suffix does not shift the fields" || bad "13 logged-out" "$(seat seats crew-qa-3)"
  [[ "$(seat seats crew-qa-4)" == unknown ]] && ok "13 a failed (tag) STATE reads unknown" || bad "13 failed" "$(seat seats crew-qa-4)"
  [[ "$(seat seats crew-job-1)" == unknown ]] && ok "13 a succeeded (exit n) STATE reads unknown" || bad "13 succeeded" "$(seat seats crew-job-1)"
  [[ -z "$(jqf -r 'select((.last_active // "") | startswith("("))|.name' "$state/freebusy/seats.jsonl")" ]] && ok "13 no seat stores a suffix word as last_active" || bad "13 suffix stored"
  grep -q 'state,health' "$FIX/marvel.log" && ok "13 the read ends its columns with state,health" || bad "13 columns" "$(head -2 "$FIX/marvel.log" | tail -1)"
  grep -q "rate" "$FIX/marvel.log" && bad "13 the read asks for the RATE column" || ok "13 the read does not use the RATE column"
}

case_lock_age() {
  local v
  for v in nopid emptypid junkpid; do
    fixtures; new_state; conf3
    mkdir -p "$state/freebusy"; chmod 700 "$state/freebusy"; mkdir "$state/freebusy/.lock"
    case "$v" in
      emptypid) : >"$state/freebusy/.lock/pid" ;;
      junkpid) echo abc >"$state/freebusy/.lock/pid" ;;
    esac
    out="$(run FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp")"
    grep -q 'not run: sweep in progress' <<<"$out" && ok "14 $v: a young lock reads as a run in progress" || bad "14 $v young" "$out"
    touch -t 202001010000 "$state/freebusy/.lock"
    out="$(run FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp")"
    grep -q 'clusters: 3 of 3 read' <<<"$out" && ok "14 $v: an old lock is taken over" || bad "14 $v old" "$out"
  done
}

case_empty_list() {
  fixtures; new_state
  printf '%s\n' 'NAME  ADDRESS  IDENTITY' >"$FIX/config.list"
  out="$(run FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp")"
  grep -q 'GAP clusters: marvel config list names no cluster' <<<"$out" && ok "15 an empty config list prints GAP clusters naming its source" || bad "15 empty list" "$out"
}

age_lock() { # age_lock DIR MINUTES: set a lock's mtime that far back (the lock is recreated if a run took it over)
  mkdir -p "$1"; chmod 700 "$(dirname "$1")"
  python3 -c 'import os, sys, time; t = time.time() - float(sys.argv[2]) * 60; os.utime(sys.argv[1], (t, t))' "$1" "$2"
}

case_lock_min() {
  local bad
  for bad in abc 1.5 -5 0 ""; do
    fixtures; new_state; conf3
    mkdir -p "$state/freebusy"; chmod 700 "$state/freebusy"; mkdir "$state/freebusy/.lock"
    out="$(run FREEBUSY_LOCK_STALE_MIN="$bad" FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp")"
    grep -q 'GAP lock-stale' <<<"$out" && ok "16 [$bad]: a bad FREEBUSY_LOCK_STALE_MIN prints a GAP" || bad "16 [$bad] gap" "$out"
    grep -q 'not run: sweep in progress' <<<"$out" && ok "16 [$bad]: and the default holds a young lock" || bad "16 [$bad] young" "$out"
    age_lock "$state/freebusy/.lock" 1
    out="$(run FREEBUSY_LOCK_STALE_MIN="$bad" FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp")"
    grep -q 'not run: sweep in progress' <<<"$out" && ok "16 [$bad]: the default of 2 minutes holds a 1 minute old lock" || bad "16 [$bad] one minute" "$out"
    age_lock "$state/freebusy/.lock" 10
    out="$(run FREEBUSY_LOCK_STALE_MIN="$bad" FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp")"
    grep -q 'clusters: 3 of 3 read' <<<"$out" && ok "16 [$bad]: and the default takes an old lock over" || bad "16 [$bad] old" "$out"
  done
  # a good value is used and prints no GAP
  fixtures; new_state; conf3
  mkdir -p "$state/freebusy"; chmod 700 "$state/freebusy"; mkdir "$state/freebusy/.lock"; age_lock "$state/freebusy/.lock" 5
  out="$(run FREEBUSY_LOCK_STALE_MIN=10 FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp")"
  { grep -q 'not run: sweep in progress' <<<"$out" && ! grep -q 'GAP lock-stale' <<<"$out"; } && ok "16 10 minutes: a 5 minute old lock is still held, no GAP" || bad "16 10 minutes" "$out"
  out="$(run FREEBUSY_LOCK_STALE_MIN=1 FREEBUSY_DIRECTOR_MCP="$root/bin/director-mcp")"
  { grep -q 'clusters: 3 of 3 read' <<<"$out" && ! grep -q 'GAP lock-stale' <<<"$out"; } && ok "16 1 minute: a 5 minute old lock is taken over, no GAP" || bad "16 1 minute" "$out"
}

for FB_BASH in $BASHES; do
  export FB_BASH
  echo "== $FB_BASH =="
  if ! "$FB_BASH" -n "$FB"; then bad "0  bash -n ($FB_BASH)"; else ok "0  bash -n passes ($FB_BASH)"; fi
  case_all_read; case_parked; case_cluster_down; case_asks; case_modes
  case_no_writes; case_concurrent; case_hung_read; case_flow_and_turn; case_conf; case_formats; case_lock_age; case_empty_list; case_discovery; case_lock_min
done

echo "$pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
