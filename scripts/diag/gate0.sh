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
# The live cases (broker, hub, gh) run only under `diag run --live` or
# `--live-only`; a default run skips them. They only connect and read. Nothing here publishes to a bus,
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

# Stop what the case started and fold teardown into the result: a daemon that
# did not stop is an instrument fault, so a PASS never hides it.
finish() { # finish CASE_RC
  local rc="$1" down=0
  trap - EXIT
  diag_scratch_down || down=$?
  if [[ $down -ne 0 ]]; then echo "teardown failed (rc=$down); the scratch root may be kept for a person to read"; [[ $rc -eq 0 ]] && rc=3; fi
  exit "$rc"
}
trap 'diag_scratch_down >/dev/null 2>&1 || true' EXIT

case "$case_name" in
  daemon)
    diag_scratch_new || exit 3
    diag_scratch_daemon_up; rc=$?
    [[ $rc -eq 0 ]] && echo "scratch daemon answered on its own socket"
    finish "$rc"
    ;;
  seat)
    diag_scratch_new || exit 3
    diag_scratch_daemon_up; rc=$?
    if [[ $rc -eq 0 ]]; then
      diag_scratch_seat_up gate0-seat; rc=$?
      [[ $rc -eq 0 ]] && echo "simulator seat reached running"
    fi
    finish "$rc"
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
