#!/usr/bin/env bash
# The scratch-daemon helper. Source it from a check; it is not run on its own.
#
# A destructive or interventional case never touches the live fleet (battery
# rule 4), so it runs against a marvel daemon of its own: its own state root,
# socket, bolt file, log, pid file and tmux server, all inside one scratch
# directory. This file builds that directory, starts and stops the daemon, and
# refuses to act on anything it did not create.
#
# Functions (each returns 0 on success; a failure prints one line to stderr):
#   diag_scratch_new        make the scratch root and export its environment
#   diag_scratch_daemon_up  start a daemon under it and wait for its socket
#   diag_scratch_seat_up    start one simulator seat in it and wait for running
#   diag_scratch_down       stop what this helper started, then remove the root
#   diag_scratch_marvel     run the marvel CLI against the scratch daemon
#
# Binaries: DIAG_MARVEL (default: marvel on PATH) and DIAG_SIMULATOR (default:
# simulator on PATH; marvel builds it from cmd/simulator). A missing binary is
# an INSTRUMENT fault of the check, never a FAIL of its subject: the function
# returns 3 so a check can pass the code straight through.
#
# Teardown never matches a process by pattern. It signals the one pid it
# recorded and confirms with kill -0, and it ends the tmux server by the exact
# socket name it chose.

DIAG_SCRATCH=""
DIAG_SCRATCH_DAEMON_PID=""
DIAG_SCRATCH_TMUX=""

_diag_say() { echo "diag-scratch: $*" >&2; }

# The names the scratch environment sets. Everything marvel or tmux reads that
# could point at the live fleet is set or cleared here.
_diag_scratch_env() {
  export HOME="$DIAG_SCRATCH/h"
  export MARVEL_HOME="$DIAG_SCRATCH/m"
  export DIRECTOR_HOME="$DIAG_SCRATCH/d"
  export TMUX_TMPDIR="$DIAG_SCRATCH/t"
  export MARVEL_TMUX_SOCKET="$DIAG_SCRATCH_TMUX"
  unset MARVEL_SOCKET MARVEL_CLUSTER TMUX TMUX_PANE
  unset MARVEL_WORKSPACE MARVEL_SESSION MARVEL_SEAT_URI MARVEL_PREDECESSOR
}

# A scratch path never resolves into the real home, the real marvel root or
# the real director home. The real home is read before HOME is replaced.
_diag_real_home="${DIAG_REAL_HOME:-$HOME}"

diag_scratch_assert() {
  [[ -n "$DIAG_SCRATCH" && -d "$DIAG_SCRATCH" ]] || { _diag_say "no scratch root"; return 3; }
  local real; real="$(cd "$DIAG_SCRATCH" && pwd -P)"
  case "$real" in
    "$_diag_real_home"/.marvel*|"$_diag_real_home"/.director*|"$_diag_real_home"/.claude*)
      _diag_say "scratch root resolves under a real state directory"; return 3 ;;
  esac
  [[ "$(basename "$real")" == dg.* ]] || { _diag_say "scratch root is not one this helper made"; return 3; }
  return 0
}

diag_scratch_new() {
  # A short root: the control socket path has a hard length limit.
  local base="${DIAG_TMP:-/private/tmp}"
  DIAG_SCRATCH="$(mktemp -d "$base/dg.XXXXXX")" || { _diag_say "mktemp failed"; return 3; }
  DIAG_SCRATCH="$(cd "$DIAG_SCRATCH" && pwd -P)"
  DIAG_SCRATCH_TMUX="dg$$$RANDOM"
  mkdir -p "$DIAG_SCRATCH/h" "$DIAG_SCRATCH/m" "$DIAG_SCRATCH/d" "$DIAG_SCRATCH/t"
  diag_scratch_assert || return 3
  _diag_scratch_env
  return 0
}

diag_scratch_marvel() {
  local bin="${DIAG_MARVEL:-$(command -v marvel || true)}"
  [[ -n "$bin" && -x "$bin" ]] || { _diag_say "no marvel binary (set DIAG_MARVEL or put marvel on PATH)"; return 3; }
  "$bin" --marvel-home "$MARVEL_HOME" --socket "$MARVEL_HOME/run/marvel.sock" "$@"
}

diag_scratch_daemon_up() {
  diag_scratch_assert || return 3
  local bin="${DIAG_MARVEL:-$(command -v marvel || true)}" i
  [[ -n "$bin" && -x "$bin" ]] || { _diag_say "no marvel binary (set DIAG_MARVEL or put marvel on PATH)"; return 3; }
  if [[ ${#MARVEL_HOME} -gt 60 ]]; then _diag_say "scratch root is too long for a socket path"; return 3; fi
  mkdir -p "$MARVEL_HOME/run" "$MARVEL_HOME/log" "$MARVEL_HOME/state"
  # Start this root's own tmux server first, on the exact socket name chosen
  # above. A daemon that finds no server decides "absent" by reading the
  # process list, which a sandboxed run cannot do; with a server already
  # there the daemon takes the ordinary path and creates its sessions. The
  # keepalive session is not a marvel-* name, so the daemon neither adopts nor
  # reaps it, and teardown ends the server by its socket name.
  tmux -L "$DIAG_SCRATCH_TMUX" -f /dev/null new-session -d -s diag-keepalive "sleep 3600" >/dev/null 2>&1 \
    || { _diag_say "could not start the scratch tmux server"; return 3; }
  # Every path is explicit and inside the scratch root, so no default can
  # reach ~/.marvel even if a layout rule changes.
  "$bin" --marvel-home "$MARVEL_HOME" daemon \
    --socket "$MARVEL_HOME/run/marvel.sock" \
    --state-bolt "$MARVEL_HOME/state/marvel.bolt" \
    --log-file "$MARVEL_HOME/log/daemon.log" \
    --pidfile "$MARVEL_HOME/run/daemon.pid" \
    > "$DIAG_SCRATCH/daemon.out" 2>&1 &
  DIAG_SCRATCH_DAEMON_PID=$!
  echo "$DIAG_SCRATCH_DAEMON_PID" > "$DIAG_SCRATCH/daemon.pid.mine"
  i=0
  while [[ $i -lt 60 ]]; do
    if ! kill -0 "$DIAG_SCRATCH_DAEMON_PID" 2>/dev/null; then
      _diag_say "the scratch daemon exited before its socket came up"; tail -n 20 "$DIAG_SCRATCH/daemon.out" >&2 || true; return 1
    fi
    if [[ -S "$MARVEL_HOME/run/marvel.sock" ]] && diag_scratch_marvel get sessions >/dev/null 2>&1; then return 0; fi
    sleep 0.5; i=$((i + 1))
  done
  _diag_say "the scratch daemon did not answer within 30s"; tail -n 20 "$DIAG_SCRATCH/daemon.out" >&2 || true
  return 1
}

diag_scratch_seat_up() {
  local sim="${DIAG_SIMULATOR:-$(command -v simulator || true)}" name="${1:-diag-seat}" i out
  if [[ -z "$sim" || ! -x "$sim" ]]; then
    _diag_say "no simulator binary (set DIAG_SIMULATOR, or build marvel's cmd/simulator and put it on PATH)"; return 3
  fi
  diag_scratch_marvel run "$sim" --experimental --name "$name" --workspace diag --team diag --role seat \
    --workdir "$DIAG_SCRATCH" > "$DIAG_SCRATCH/seat.out" 2>&1 || { cat "$DIAG_SCRATCH/seat.out" >&2; return 1; }
  i=0
  while [[ $i -lt 40 ]]; do
    out="$(diag_scratch_marvel get sessions 2>&1 || true)"
    if grep -q "$name" <<<"$out" && grep -q 'running' <<<"$out"; then return 0; fi
    sleep 0.5; i=$((i + 1))
  done
  _diag_say "the simulator seat did not reach running within 20s"; echo "$out" >&2
  return 1
}

diag_scratch_down() {
  local rc=0 i
  [[ -n "$DIAG_SCRATCH" ]] || return 0
  diag_scratch_assert || return 3
  if [[ -n "$DIAG_SCRATCH_DAEMON_PID" ]] && kill -0 "$DIAG_SCRATCH_DAEMON_PID" 2>/dev/null; then
    # Delete the sessions this daemon recorded and its tmux session, then stop it.
    diag_scratch_marvel stop --teardown >/dev/null 2>&1 || true
    kill -TERM "$DIAG_SCRATCH_DAEMON_PID" 2>/dev/null || true
    i=0
    while [[ $i -lt 20 ]] && kill -0 "$DIAG_SCRATCH_DAEMON_PID" 2>/dev/null; do sleep 0.5; i=$((i + 1)); done
    if kill -0 "$DIAG_SCRATCH_DAEMON_PID" 2>/dev/null; then
      _diag_say "the scratch daemon (pid $DIAG_SCRATCH_DAEMON_PID) did not stop; the root is kept for a person to read"; return 1
    fi
  fi
  # The tmux server this root named, by its exact socket name.
  if [[ -n "$DIAG_SCRATCH_TMUX" ]]; then
    tmux -L "$DIAG_SCRATCH_TMUX" kill-server >/dev/null 2>&1 || true
  fi
  case "$DIAG_SCRATCH" in /private/tmp/dg.*|"${DIAG_TMP:-/private/tmp}"/dg.*) rm -rf "$DIAG_SCRATCH" || rc=1 ;; *) _diag_say "refusing to remove $DIAG_SCRATCH"; rc=3 ;; esac
  DIAG_SCRATCH=""; DIAG_SCRATCH_DAEMON_PID=""
  return "$rc"
}
