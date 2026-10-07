#!/usr/bin/env bash
# Prove director-session.sh refuses to launch claude from a directory that is
# not inside an orc root, and launches from one (aae-orc-skw0b, shape b). An
# orc root is an ancestor of the cwd holding both CLAUDE.md and repos.yaml, the
# test aq and ax use to decide orc mode.
#
# Broker-free: claude and the shim are stubs, nothing connects. A refusal must
# exit nonzero, must not run claude, and must say what is wrong and what is
# required. The launch cases check claude ran from the launch directory.
#
# Usage: probe/nats-phase-0/id-a-demo/verify-director-session.sh   Exit nonzero on any miss.
#        DIRECTOR_SESSION=<path> to test another copy.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SESSION="${DIRECTOR_SESSION:-$here/director-session.sh}"
[[ -x "$SESSION" ]] || { echo "director-session.sh not executable at $SESSION"; exit 2; }

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin"
marker="$work/claude-ran"
cat >"$work/bin/claude" <<STUB
#!/usr/bin/env bash
pwd -P >"$marker"
STUB
chmod +x "$work/bin/claude"
printf '#!/bin/sh\n' >"$work/shim"
chmod +x "$work/shim"

fail=0
miss() { echo "MISS: $*"; fail=1; }

# run <dir> [env...]: launch from dir; sets rc, err, ran (marker contents or "")
run() {
  local dir="$1"; shift
  rm -f "$marker"
  set +e
  err="$(cd "$dir" && env "$@" PATH="$work/bin:$PATH" DIRECTOR_SHIM_BIN="$work/shim" "$SESSION" seat-a 2>&1 >/dev/null)"
  rc=$?
  set -e
  ran=""
  if [[ -f "$marker" ]]; then ran="$(<"$marker")"; fi
}

orc="$work/orc"
mkdir -p "$orc/sub/deeper" "$work/plain" "$work/claudeonly" "$work/reposonly"
: >"$orc/CLAUDE.md"; : >"$orc/repos.yaml"
: >"$work/claudeonly/CLAUDE.md"
: >"$work/reposonly/repos.yaml"

# Positive controls: the orc root, and a subdirectory of it, launch.
run "$orc" X=1
[[ $rc -eq 0 && -n "$ran" ]] || miss "orc root: rc=$rc ran='$ran' err=$err"
run "$orc/sub/deeper" X=1
[[ $rc -eq 0 && -n "$ran" ]] || miss "orc subdirectory: rc=$rc ran='$ran' err=$err"

# Refusals: no marker, one marker, the other marker.
for d in plain claudeonly reposonly; do
  run "$work/$d" X=1
  [[ $rc -ne 0 ]] || miss "$d: rc=0, want a refusal"
  [[ -z "$ran" ]] || miss "$d: claude ran from $ran"
  [[ "$err" == *"CLAUDE.md"* && "$err" == *"repos.yaml"* ]] || miss "$d: refusal does not name what is required: $err"
  [[ "$err" == *"refusing"* ]] || miss "$d: refusal does not say it refused: $err"
done

# ORC_ROOT does not rescue a wrong cwd: the seat's context comes from cwd.
run "$work/plain" ORC_ROOT="$orc"
[[ $rc -ne 0 && -z "$ran" ]] || miss "ORC_ROOT set, wrong cwd: rc=$rc ran='$ran'"

if [[ $fail -ne 0 ]]; then echo "FAIL"; exit 1; fi
echo "ok: director-session.sh refuses a non-orc cwd and launches from an orc root"
