#!/usr/bin/env bash
# diag-id: G0
# diag-gate: 0
# diag-subject: aae-orc-kdin1
# diag-mode: scratch
# diag-case: daemon expect=pass
# diag-case: seat expect=pass
# diag-case: broker mode=live expect=pass
# diag-case: hub mode=live expect=pass
# diag-case: gh mode=live expect=pass
#
# Gate 0, the positive control. It runs first, every time, and asks whether the
# instrument works before any gate is read: a scratch marvel daemon starts and
# runs a simulator seat, the local broker and the hub accept a connection, and
# gh answers. If any case here fails, every later case reports INSTRUMENT, never
# FAIL, so a broken instrument cannot read as a broken subject.
#
# The two live cases only connect and read. Nothing here publishes to a bus,
# changes a daemon or reads a credential.
#
#   DIAG_BROKER_ADDR  host:port of the local broker (default 127.0.0.1:4222)
#   DIAG_HUB_ADDR     host:port of the hub; unset means the case is skipped,
#                     because a hub address is a fact of the cluster, not of
#                     this file
#   DIAG_MARVEL, DIAG_SIMULATOR   see lib/scratch.sh
set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
lib="${DIAG_LIB:-$here/lib}"
# shellcheck source=lib/scratch.sh
source "$lib/scratch.sh"

case_name="${1:?usage: gate0.sh <case>}"

tcp_up() { # host:port -> 0 reachable, 1 refused or unreachable
  local hp="$1" h p
  h="${hp%:*}"; p="${hp##*:}"
  (exec 3<>"/dev/tcp/$h/$p") 2>/dev/null
}

case "$case_name" in
  daemon)
    trap 'diag_scratch_down' EXIT
    diag_scratch_new || exit 3
    diag_scratch_daemon_up; rc=$?
    [[ $rc -eq 0 ]] && echo "scratch daemon answered on its own socket"
    exit "$rc"
    ;;
  seat)
    trap 'diag_scratch_down' EXIT
    diag_scratch_new || exit 3
    diag_scratch_daemon_up || exit $?
    diag_scratch_seat_up gate0-seat; rc=$?
    [[ $rc -eq 0 ]] && echo "simulator seat reached running"
    exit "$rc"
    ;;
  broker)
    addr="${DIAG_BROKER_ADDR:-127.0.0.1:4222}"
    if tcp_up "$addr"; then echo "broker accepted a connection at $addr"; exit 0; fi
    echo "broker did not accept a connection at $addr"; exit 1
    ;;
  hub)
    if [[ -z "${DIAG_HUB_ADDR:-}" ]]; then echo "DIAG_HUB_ADDR is not set, so the hub case has nothing to connect to"; exit 2; fi
    if tcp_up "$DIAG_HUB_ADDR"; then echo "hub accepted a connection at $DIAG_HUB_ADDR"; exit 0; fi
    echo "hub did not accept a connection at $DIAG_HUB_ADDR"; exit 1
    ;;
  gh)
    command -v gh >/dev/null 2>&1 || { echo "gh is not on PATH"; exit 3; }
    if out="$(gh api rate_limit --jq .resources.core.limit 2>&1)" && [[ "$out" =~ ^[0-9]+$ ]]; then
      echo "gh answered; core limit $out"; exit 0
    fi
    echo "gh did not answer: $out"; exit 1
    ;;
  *) echo "gate0.sh: unknown case $case_name"; exit 3 ;;
esac
