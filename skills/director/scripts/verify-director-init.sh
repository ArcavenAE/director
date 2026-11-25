#!/usr/bin/env bash
# Prove director-init (design: sim/design/director-bootstrap.md section 4a.2;
# first build, aae-orc-d2sol and director's rulings of 2026-11-12 recorded on
# that ticket). It writes an operating profile, reports what shadows the
# launcher, and with --check reports drift without writing.
#
# Broker-free and fleet-free: marvel and sideshow are stubs on PATH, nothing
# connects, and every run is env -i with a scratch HOME, so nothing under the
# real ~/.director, ~/.zshrc or ~/.claude.json is read or written.
#
# Covered:
#  - writing: detection through the
#    marvel and sideshow stubs, --entry-role and repeatable --team GLOB=ROLE,
#    no flags writing the minimal profile (there is no terminal prompt), an existing
#    profile (refused without a yes, a backup kept), an atomic write, the summary printed, --manifests refused as
#    not in the first build;
#  - the shadow scan, which reports and edits nothing: a function, an alias, a
#    PATH entry, and a wrapper that calls the launcher (reported as "wraps", not
#    drift);
#  - --check: clean on a sound state, one drift line per fault, exit 1 on
#    drift, and nothing written;
#  - the python rule: python3 older than 3.11 is refused with one line.
#
# Usage: skills/director/scripts/verify-director-init.sh   Exit nonzero on any miss.
#        DIRECTOR_INIT_BIN=<path> to test another copy.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN="${DIRECTOR_INIT_BIN:-$here/director-init}"
PROFILE_BIN="$here/director-profile"
[[ -x "$BIN" ]] || { echo "director-init not executable at $BIN"; exit 2; }
[[ -x "$PROFILE_BIN" ]] || { echo "director-profile not executable at $PROFILE_BIN"; exit 2; }
PY="$(command -v python3 || true)"
[[ -n "$PY" ]] || { echo "python3 not on PATH"; exit 2; }
"$PY" -c 'import sys; sys.exit(0 if sys.version_info >= (3, 11) else 1)' || { echo "python3 3.11 or newer is needed to run this check"; exit 2; }

work="$(cd "$(mktemp -d "${TMPDIR:-/tmp}/director-init-verify.XXXXXX")" && pwd -P)"
trap 'rm -rf "$work"' EXIT
pydir="$work/pybin"; mkdir -p "$pydir"; ln -s "$PY" "$pydir/python3"   # only python3, so no real marvel or sideshow is on the test PATH
fail=0
miss() { echo "MISS: $*"; fail=1; }

# fresh: a new scratch HOME with the installed bin home (the launcher and
# director-profile), an empty state directory, and a bin of stubs.
fresh() {
  rm -rf "$work/h" "$work/stubs" "$work/tmp"
  home="$work/h"; state="$home/.director/state"; dbin="$home/.director/bin"; stubs="$work/stubs"
  mkdir -p "$state" "$dbin" "$stubs" "$work/tmp"
  printf '#!/bin/sh\nexit 0\n' >"$dbin/director-session"; chmod +x "$dbin/director-session"
  cp "$PROFILE_BIN" "$dbin/director-profile"; chmod +x "$dbin/director-profile"
  prof="$state/operating-profile.toml"
}
stub_marvel() { printf '%s\n' '#!/bin/sh' 'echo cluster-a' 'echo cluster-b' >"$stubs/marvel"; chmod +x "$stubs/marvel"; }
stub_sideshow() { printf '%s\n' '#!/bin/sh' 'echo my-roles 0.2.0' 'echo bmad 6.12.4' >"$stubs/sideshow"; chmod +x "$stubs/sideshow"; }

# run [env...] -- args...: env -i, scratch HOME, no terminal; sets rc and out.
run() {
  local envs=()
  while [[ $# -gt 0 && "$1" != "--" ]]; do envs+=("$1"); shift; done
  shift
  set +e
  out="$(env -i HOME="$home" TMPDIR="$work/tmp" PATH="$stubs:$pydir:/usr/bin:/bin" ${envs[@]+"${envs[@]}"} "$BIN" "$@" 2>&1 </dev/null)"
  rc=$?
  set -e
}

hash_tree() { find "$home" -type f -exec shasum -a 256 {} + | LC_ALL=C sort | shasum -a 256 | cut -d' ' -f1; }
check_profile() { env -i HOME="$home" PATH="$pydir:/usr/bin:/bin" "$PROFILE_BIN" check "$1" 2>&1; }

# ---- I1: no flags: the minimal profile, said plainly, and --check reads it clean ----
fresh
run --
[[ $rc -eq 0 && -f "$prof" ]] || miss "I1 minimal profile not written: rc=$rc out=$out"
[[ "$out" == *"routing into a team is refused until an entry role is named"* ]] || miss "I1 must say what the minimal profile means: $out"
check_profile "$prof" | grep -qs 'profile ok' || miss "I1 the written profile must pass director-profile check"
grep -qs '^\[\[team\]\]' "$prof" && miss "I1 the minimal profile has no [[team]]"
grep -qs 'fleet_sources = \["bus-presence"\]' "$prof" || miss "I1 minimal fleet_sources: $(cat "$prof" 2>/dev/null)"
run -- --check
[[ $rc -eq 0 && "$out" == *"clean"* ]] || miss "I1 a director-only user must reach a clean --check: rc=$rc out=$out"
run --
[[ $rc -eq 1 && "$out" == *"already exists"* ]] || miss "I1 no flags over an existing profile needs --yes: rc=$rc out=$out"

# (I-476) A fresh-home init writes no seat.env value: the file is absent or holds no KEY= line.
seatenv="$home/.director/seat.env"
if [[ -e "$seatenv" ]] && grep -qE '^[[:space:]]*[A-Z_]+=' "$seatenv"; then miss "I1b init wrote a seat.env value: $(cat "$seatenv")"; fi

# ---- I2: detection through the stubs; sideshow is printed, never written ----
fresh; stub_marvel; stub_sideshow
run -- --entry-role supervisor
[[ $rc -eq 0 && "$out" == *"marvel: found"* && "$out" == *"my-roles 0.2.0"* && "$out" == *"bmad 6.12.4"* ]] || miss "I2 detection output: rc=$rc out=$out"
grep -qs 'fleet_sources = \["marvel-register", "bus-presence"\]' "$prof" || miss "I2 marvel found sets marvel-register: $(cat "$prof" 2>/dev/null)"
grep -qs -E 'my-roles|bmad' "$prof" && miss "I2 sideshow packs must not be written to the profile"
fresh
run -- --entry-role supervisor
[[ "$out" == *"marvel: not found"* && "$out" == *"sideshow: not found"* ]] || miss "I2 absent tools must be reported: $out"

# ---- I3: entry roles by flags ----
fresh
run -- --entry-role supervisor
grep -qs 'match = "\*"' "$prof" && grep -qs 'entry_role = "supervisor"' "$prof" || miss "I3 --entry-role: $(cat "$prof" 2>/dev/null)"
fresh
run -- --team 'aae/res*=lead' --team '*=sup'
out2="$(check_profile "$prof")"
[[ $rc -eq 0 && "$out2" == *"team aae/res*: entry_role lead, rules has-entry"*"team *: entry_role sup, rules has-entry"* ]] || miss "I3 two --team flags in order: rc=$rc out=$out2"
fresh
run -- --team 'no-equals-sign'
[[ $rc -eq 2 && "$out" == *"GLOB=ROLE"* && ! -e "$prof" ]] || miss "I3 bad --team: rc=$rc out=$out"
run -- --entry-role ''
[[ $rc -eq 2 && ! -e "$prof" ]] || miss "I3 empty role: rc=$rc out=$out"
fresh
run -- --entry-role 'a"b'
check_profile "$prof" | grep -qsF 'team *: entry_role a"b, rules has-entry' || miss "I8 a quote in a role must be escaped: $(cat "$prof" 2>/dev/null)"

# ---- I4: an existing profile ----
fresh
printf 'schema_version = 1\nname = "mine"\n' >"$prof"; before="$(shasum -a 256 <"$prof")"
run -- --entry-role supervisor
[[ $rc -eq 1 && "$out" == *"already exists"* && "$out" == *"--yes"* ]] || miss "I4 existing profile without yes: rc=$rc out=$out"
[[ "$(shasum -a 256 <"$prof")" == "$before" ]] || miss "I4 the existing profile must be unchanged"
ls "$state" | grep -qs '\.bak-' && miss "I4 no backup without a write"
run -- --entry-role supervisor --yes
[[ $rc -eq 0 ]] && grep -qs 'entry_role = "supervisor"' "$prof" || miss "I4 --yes must replace: rc=$rc out=$out"
bak="$(ls "$state"/operating-profile.toml.bak-* 2>/dev/null | head -1 || true)"
[[ -n "$bak" && "$(shasum -a 256 <"$bak")" == "$before" ]] || miss "I4 the backup must hold the old bytes: $bak"
run -- --entry-role other --yes
[[ "$(ls "$state"/operating-profile.toml.bak-* | wc -l | tr -d ' ')" == "2" ]] || miss "I4 a second replace must keep both backups: $(ls "$state")"

# ---- I5 and I6: atomic write, summary printed ----
fresh
run -- --entry-role supervisor
[[ "$out" == *"profile ok:"* && "$out" == *"team *: entry_role supervisor"* ]] || miss "I6 the profile summary must be printed: $out"
[[ "$(ls -A "$state" | tr '\n' ' ')" == "operating-profile.toml " ]] || miss "I5 no temp file may be left: $(ls -A "$state")"

# ---- I7: --manifests is not in the first build ----
fresh
run -- --manifests /some/dir
[[ $rc -eq 2 && "$out" == *"not in the first build: --manifests"* && ! -e "$prof" ]] || miss "I7 --manifests: rc=$rc out=$out"

# ---- S1 to S4: the shadow scan reports and edits nothing ----
fresh
printf '%s\n' '# my rc' 'director-session() { echo shadowed; }' >"$home/.zshrc"
printf '%s\n' 'alias director-session="echo shadowed"' >"$home/.bashrc"
before="$(hash_tree)"
run -- --entry-role supervisor --yes
[[ "$out" == *"$home/.zshrc:2: function"* && "$out" == *"$home/.bashrc:1: alias"* ]] || miss "S1 function and alias must be reported with file and line: rc=$rc out=$out"
rm -f "$state"/operating-profile.toml*
[[ "$(cat "$home/.zshrc")" == "$(printf '%s\n' '# my rc' 'director-session() { echo shadowed; }')" ]] || miss "S3 the rc file must not be edited"
fresh
printf '%s\n' 'director-session() { echo shadowed; }' >"$home/.zshrc"
before="$(find "$home" -name '.zshrc' -exec shasum -a 256 {} +)"
run -- --entry-role supervisor
[[ "$(find "$home" -name '.zshrc' -exec shasum -a 256 {} +)" == "$before" ]] || miss "S3 the rc file hash must be unchanged after init"
run -- --check
[[ "$(find "$home" -name '.zshrc' -exec shasum -a 256 {} +)" == "$before" ]] || miss "S3 the rc file hash must be unchanged after --check"

# a wrapper that calls the installed launcher is wraps, not drift
fresh
printf '%s\n' 'director-session() { DIRECTOR_PRESENCE_WATCH=1 ~/.director/bin/director-session --receiver "$@"; }' >"$home/.zshrc"
run -- --entry-role supervisor
[[ "$out" == *"$home/.zshrc:1: wraps"* && "$out" != *"drift"* ]] || miss "S2 the wrapper must read as wraps: rc=$rc out=$out"
run -- --check
[[ $rc -eq 0 && "$out" == *"$home/.zshrc:1: wraps"* && "$out" == *"clean"* ]] || miss "S2 a wrapper is not drift: rc=$rc out=$out"
for body in 'DIRECTOR_PRESENCE_WATCH=1 $HOME/.director/bin/director-session "$@"' '"$HOME/.director/bin/director-session" "$@"' '~/.director/bin/director-session'; do
  printf '%s\n' "director-session() { $body; }" >"$home/.zshrc"
  run -- --check
  [[ $rc -eq 0 && "$out" == *".zshrc:1: wraps"* ]] || miss "S2 wrapper variant '$body': rc=$rc out=$out"
done
printf '%s\n' 'director-session() { /opt/other/director-session "$@"; }' >"$home/.zshrc"
run -- --check
[[ $rc -eq 1 && "$out" == *"drift: $home/.zshrc:1: function"* ]] || miss "S2 a function that wraps another file is drift: rc=$rc out=$out"
# $DIRECTOR_HOME in either form, under a set DIRECTOR_HOME, names the launcher
for body in '${DIRECTOR_HOME}/bin/director-session "$@"' '$DIRECTOR_HOME/bin/director-session "$@"'; do
  printf '%s\n' "director-session() { $body; }" >"$home/.zshrc"
  run DIRECTOR_HOME="$home/.director" -- --check
  [[ $rc -eq 0 && "$out" == *".zshrc:1: wraps"* ]] || miss "S2 DIRECTOR_HOME wrapper ($body): rc=$rc out=$out"
done
# the exact path, not a longer name that starts with it
for body in '~/.director/bin/director-session-old "$@"' '$HOME/.director/bin/director-session.bak "$@"' '$HOME/.director/bin/director-session/x "$@"'; do
  printf '%s\n' "director-session() { $body; }" >"$home/.zshrc"
  run -- --check
  [[ $rc -eq 1 && "$out" == *"drift: $home/.zshrc:1: function"* && "$out" != *"wraps"* ]] || miss "S2 a longer name is not the launcher ($body): rc=$rc out=$out"
done
printf '%s\n' 'function director-session {' '  echo shadowed' '}' >"$home/.zshrc"
run -- --check
[[ $rc -eq 1 && "$out" == *"drift: $home/.zshrc:1: function"* ]] || miss "S1 the function keyword form: rc=$rc out=$out"
printf '%s\n' "alias director-session='$home/.director/bin/director-session --receiver'" >"$home/.zshrc"
run -- --check
[[ $rc -eq 0 && "$out" == *".zshrc:1: wraps"* ]] || miss "S2 an alias that wraps the launcher: rc=$rc out=$out"

# a PATH entry that holds another director-session, earlier than the bin home
fresh
mkdir -p "$work/early"; printf '#!/bin/sh\nexit 0\n' >"$work/early/director-session"; chmod +x "$work/early/director-session"
run PATH="$work/early:$dbin:$pydir:/usr/bin:/bin" -- --check
[[ $rc -eq 1 && "$out" == *"drift: $work/early/director-session: path"* ]] || miss "S4 an earlier PATH entry: rc=$rc out=$out"
run PATH="$dbin:$work/early:$pydir:/usr/bin:/bin" -- --check
[[ "$out" != *"path"* ]] || miss "S4 a later PATH entry does not shadow: $out"

# ---- C1: --check ----
fresh
run -- --entry-role supervisor
run -- --check
[[ $rc -eq 0 && "$out" == *"clean"* ]] || miss "C2 a sound state is clean: rc=$rc out=$out"
before="$(hash_tree)"
run -- --check
[[ "$(hash_tree)" == "$before" ]] || miss "C1 --check must write nothing"
rm -f "$prof"
run -- --check
[[ $rc -eq 1 && "$out" == *"drift: no profile file at $prof"* ]] || miss "C1 no profile: rc=$rc out=$out"
printf 'schema_version = 1\ncolour = "red"\n' >"$prof"
run -- --check
[[ $rc -eq 1 && "$out" == *"drift: the profile does not pass director-profile check"* && "$out" == *"unknown key: colour"* ]] || miss "C1 invalid profile: rc=$rc out=$out"
fresh
run -- --entry-role supervisor
stub_marvel
run -- --check
[[ $rc -eq 1 && "$out" == *"drift: marvel is found but marvel-register is not in fleet_sources"* ]] || miss "C1 marvel found, not in the profile: rc=$rc out=$out"
fresh; stub_marvel
run -- --entry-role supervisor
rm -f "$stubs/marvel"
run -- --check
[[ $rc -eq 1 && "$out" == *"drift: fleet_sources has marvel-register but marvel is not found"* ]] || miss "C1 profile says marvel, none found: rc=$rc out=$out"
fresh
run -- --entry-role supervisor
rm -f "$dbin/director-session"
run -- --check
[[ $rc -eq 1 && "$out" == *"drift: the launcher is missing: $dbin/director-session"* ]] || miss "C1 launcher missing: rc=$rc out=$out"
fresh
run -- --entry-role supervisor
rm -f "$dbin/director-profile"
run -- --check
[[ $rc -eq 1 && "$out" == *"drift: director-profile is missing: $dbin/director-profile"* ]] || miss "C1 director-profile missing: rc=$rc out=$out"

# ---- L1: the check that follows the write must not be ignored ----
# A copy of the script with a director-profile beside it that passes the temporary
# file and refuses the written one: init must report the refusal and exit 1.
mkdir -p "$work/copy"
cp "$BIN" "$work/copy/director-init"; chmod +x "$work/copy/director-init"
printf '%s\n' '#!/usr/bin/env python3' 'import sys' 'if sys.argv[-1].endswith(".tmp"):' '    print("profile ok: stub"); sys.exit(0)' 'sys.stderr.write("stub: refused after the write\n"); sys.exit(1)' >"$work/copy/director-profile"
chmod +x "$work/copy/director-profile"
fresh
real_bin="$BIN"; BIN="$work/copy/director-init"
run -- --entry-role supervisor
BIN="$real_bin"
[[ $rc -eq 1 && "$out" == *"stub: refused after the write"* ]] || miss "L1 a failing check after the write must exit 1: rc=$rc out=$out"

# ---- L2: director-profile is run with a timeout, and a timeout is a refusal, not a traceback ----
fresh
harness='
import runpy, subprocess, sys
real = subprocess.run
seen = []
def fake(argv, *a, **k):
    if len(argv) > 1 and str(argv[1]).endswith("director-profile"):
        seen.append(k.get("timeout"))
        raise subprocess.TimeoutExpired(argv, k.get("timeout") or 1)
    return real(argv, *a, **k)
subprocess.run = fake
bin_path = sys.argv[1]
sys.argv = [bin_path, "--entry-role", "supervisor"]
try:
    runpy.run_path(bin_path, run_name="__main__")
except SystemExit as exc:
    print("exit", exc.code)
print("timeouts", seen)
'
set +e
out="$(env -i HOME="$home" TMPDIR="$work/tmp" PATH="$stubs:$pydir:/usr/bin:/bin" "$PY" -c "$harness" "$BIN" 2>&1 </dev/null)"
set -e
[[ "$out" == *"timeouts [30]"* && "$out" == *"exit 1"* && "$out" != *"Traceback"* ]] || miss "L2 director-profile needs a 30 s timeout and a timeout must exit 1: $out"

# ---- slice 16: --schedule prints the entry and installs it only on an explicit --yes ----
# (operator card i652r-2, choice 1: opt-in per install; the profile's scheduler stays "none" until then.)
# launchctl, crontab and systemctl are fakes that only log; the crontab fake keeps its table in a scratch file.
# Nothing installs on a real host: every run is env -i with the scratch HOME, and the plist path is under it.
tick_path="$(cd "$here" && pwd -P)/director-tick"
# The entry names the interpreter that ran director-init and a PATH captured at install (director ruling 01M887991M):
# absolute entries only, nothing under /tmp, /private/tmp or /var/folders, so it never points into a dead session's scratch.
filter_path() { local out="" e; local IFS=:; for e in $1; do case "$e" in /*) ;; *) continue ;; esac; case "$e" in /tmp|/tmp/*|/private/tmp|/private/tmp/*|/var/folders|/var/folders/*|/private/var/folders|/private/var/folders/*) continue ;; esac; out="${out:+$out:}$e"; done; printf '%s' "${out:-/usr/bin:/bin}"; }
run_path="$stubs:$pydir:/usr/bin:/bin"
py_abs="$(env -i PATH="$pydir" python3 -c 'import os, sys; print(os.path.realpath(sys.executable))')"
sched_fakes() {
  : >"$work/sched.log"; rm -f "$work/cron.store"
  printf '%s\n' '#!/bin/sh' 'echo "launchctl $*" >>"$SCHED_LOG"' '[ -n "$LAUNCHCTL_FAIL" ] && { echo "launchctl: load failed" >&2; exit 1; }' 'exit 0' >"$stubs/launchctl"
  printf '%s\n' '#!/bin/sh' 'echo "systemctl $*" >>"$SCHED_LOG"' 'exit 0' >"$stubs/systemctl"
  printf '%s\n' '#!/bin/sh' 'echo "crontab $*" >>"$SCHED_LOG"' 'case "$1" in' \
    '  -l) if [ -n "$CRON_FAIL" ]; then echo "crontab: permission denied" >&2; exit 1; elif [ -f "$CRON_STORE" ]; then cat "$CRON_STORE"; else echo "no crontab for user" >&2; exit 1; fi ;;' \
    '  -) cat >"$CRON_STORE" ;;' '  *) exit 2 ;;' 'esac' >"$stubs/crontab"
  chmod +x "$stubs/launchctl" "$stubs/systemctl" "$stubs/crontab"
}
senv=(SCHED_LOG="$work/sched.log" CRON_STORE="$work/cron.store")
plist="$home/Library/LaunchAgents/com.arcaven.director-tick.plist"
calls() { grep -c "^$1" "$work/sched.log" || true; }
sum_of() { shasum -a 256 <"$1" 2>/dev/null || echo none; }

# S1: launchd, no --yes: the entry is printed and nothing is installed or written
fresh; stub_marvel; sched_fakes; run -- --entry-role builder; plist="$home/Library/LaunchAgents/com.arcaven.director-tick.plist"
h0="$(hash_tree)"; : >"$work/sched.log"
run "${senv[@]}" -- --schedule --scheduler launchd
{ [[ $rc -eq 0 && "$out" == *"com.arcaven.director-tick"* && "$out" == *"<key>StartInterval</key>"* && "$out" == *"<integer>1800</integer>"* && "$out" == *"$tick_path"* ]]; } \
  || miss "S1 --schedule on launchd prints the entry (label, StartInterval 1800, the tick path $tick_path) and exits 0: rc=$rc out=$out"
[[ "$out" == *"<string>$py_abs</string>"*"<string>$tick_path</string>"* ]] || miss "F1 the plist runs the interpreter that ran director-init ($py_abs) on the tick: $out"
[[ "$out" == *"<key>EnvironmentVariables</key>"* && "$out" == *"<key>PATH</key>"* && "$out" == *"<string>$(filter_path "$stubs:$pydir:/usr/bin:/bin")</string>"* ]] || miss "F1 the plist carries the captured PATH, filtered: $out"
{ [[ ! -e "$plist" && ! -d "$home/Library" && "$(hash_tree)" == "$h0" && ! -s "$work/sched.log" ]]; } \
  || miss "S1 without --yes nothing is written and no scheduler tool is called: $(tr '\n' ';' <"$work/sched.log")"

# S2: cron, no --yes
fresh; sched_fakes; run -- --entry-role builder; h0="$(hash_tree)"; : >"$work/sched.log"
run "${senv[@]}" -- --schedule --scheduler cron
cronline="*/30 * * * * PATH=$(filter_path "$stubs:$pydir:/usr/bin:/bin") $py_abs $tick_path # director-tick"
{ [[ $rc -eq 0 && "$out" == *"$cronline"* ]]; } || miss "S2 --schedule on cron prints one marked line every 30 minutes, with the interpreter ($py_abs) and the captured PATH: rc=$rc out=$out want=$cronline"
{ [[ ! -e "$work/cron.store" && ! -s "$work/sched.log" && "$(hash_tree)" == "$h0" ]]; } || miss "S2 without --yes no scheduler tool is called and nothing is written (F4): $(tr '\n' ';' <"$work/sched.log")"

# S3: --schedule alone picks the host's scheduler: launchd on Darwin, cron elsewhere
fresh; sched_fakes; run -- --entry-role builder; : >"$work/sched.log"
run "${senv[@]}" -- --schedule
if [[ "$(uname -s)" == Darwin ]]; then want=com.arcaven.director-tick; else want='# director-tick'; fi
[[ $rc -eq 0 && "$out" == *"$want"* ]] || miss "S3 --schedule alone prints the host's entry ($want): rc=$rc out=$out"

# S4: --yes installs on launchd, records scheduler = "launchd" in the profile, keeps a backup, and the profile still checks clean
fresh; sched_fakes; run -- --entry-role builder; plist="$home/Library/LaunchAgents/com.arcaven.director-tick.plist"; : >"$work/sched.log"
run "${senv[@]}" -- --schedule --scheduler launchd --yes
{ [[ $rc -eq 0 && -f "$plist" ]] && grep -q "$tick_path" "$plist" && grep -q '<integer>1800</integer>' "$plist"; } || miss "S4 --yes writes the plist with the tick path and 1800 seconds: rc=$rc out=$out"
[[ "$(calls "launchctl load -w $plist")" == 1 ]] || miss "S4 launchctl load -w <plist> is called once: $(tr '\n' ';' <"$work/sched.log")"
grep -q '^scheduler = "launchd"' "$prof" && grep -q '^\[loop\]' "$prof" || miss "S4 the profile records scheduler = \"launchd\" under [loop]: $(cat "$prof")"
[[ "$(check_profile "$prof")" == "profile ok"* && "$(check_profile "$prof")" != *"must be"* ]] || miss "S4 the profile still passes director-profile check: $(check_profile "$prof")"
ls "$state"/operating-profile.toml.bak-* >/dev/null 2>&1 || miss "S4 a backup of the profile is kept before it changes"
[[ "$(calls systemctl)" == 0 ]] || miss "S4 systemctl is never called"

# S5: --yes on cron appends one marked line and keeps every other line
fresh; sched_fakes; run -- --entry-role builder; printf '0 3 * * * /usr/bin/true\n' >"$work/cron.store"; : >"$work/sched.log"
cronline="*/30 * * * * PATH=$(filter_path "$stubs:$pydir:/usr/bin:/bin") $py_abs $tick_path # director-tick"
run "${senv[@]}" -- --schedule --scheduler cron --yes
{ [[ $rc -eq 0 && "$(grep -c '# director-tick' "$work/cron.store")" == 1 && "$(grep -c '/usr/bin/true' "$work/cron.store")" == 1 && "$(grep -cF "$cronline" "$work/cron.store")" == 1 ]]; } \
  || miss "S5 --yes on cron adds exactly one marked line and keeps the others: rc=$rc store=$(cat "$work/cron.store" 2>/dev/null)"
grep -q '^scheduler = "cron"' "$prof" || miss "S5 the profile records scheduler = \"cron\""

# S6: a second --yes adds neither a second crontab line nor a second plist (director's idempotence case)
cs="$(sum_of "$work/cron.store")"; run "${senv[@]}" -- --schedule --scheduler cron --yes
{ [[ $rc -eq 0 && "$(sum_of "$work/cron.store")" == "$cs" && "$(grep -c '# director-tick' "$work/cron.store")" == 1 ]]; } || miss "S6 a second cron install changes nothing: rc=$rc out=$out"
fresh; sched_fakes; run -- --entry-role builder; plist="$home/Library/LaunchAgents/com.arcaven.director-tick.plist"
run "${senv[@]}" -- --schedule --scheduler launchd --yes; ps1="$(sum_of "$plist")"; : >"$work/sched.log"
run "${senv[@]}" -- --schedule --scheduler launchd --yes
{ [[ $rc -eq 0 && "$(ls "$home/Library/LaunchAgents" | wc -l | tr -d ' ')" == 1 && "$(sum_of "$plist")" == "$ps1" && "$(calls 'launchctl load')" == 0 ]]; } \
  || miss "S6 a second launchd install adds no plist, changes none and loads nothing again: rc=$rc out=$out $(tr '\n' ';' <"$work/sched.log")"

# S7: a plain init, with or without --yes, never touches a scheduler and leaves scheduler at none
fresh; sched_fakes; run "${senv[@]}" -- --entry-role builder --yes
{ [[ $rc -eq 0 && ! -s "$work/sched.log" && ! -d "$home/Library" ]] && ! grep -q 'scheduler = "\(launchd\|cron\)"' "$prof"; } || miss "S7 plain init calls no scheduler tool and does not set a scheduler: $(tr '\n' ';' <"$work/sched.log")"

# S8: --schedule needs an existing profile; with none, even --yes installs nothing
fresh; sched_fakes; run "${senv[@]}" -- --schedule --scheduler launchd --yes
{ [[ $rc -eq 1 && "$out" == *"no profile"* && ! -s "$work/sched.log" && ! -d "$home/Library" ]]; } || miss "S8 with no profile --schedule --yes is refused and installs nothing: rc=$rc out=$out"

# S9: tick_interval from [loop] sets the period; cron cannot express more than 59 minutes and says so
fresh; sched_fakes; run -- --entry-role builder; printf '\n[loop]\ntick_interval = 10\n' >>"$prof"
run "${senv[@]}" -- --schedule --scheduler launchd; [[ "$out" == *"<integer>600</integer>"* ]] || miss "S9 tick_interval = 10 prints StartInterval 600: $out"
run "${senv[@]}" -- --schedule --scheduler cron; [[ "$out" == *"*/10 * * * * "* ]] || miss "S9 tick_interval = 10 prints */10: $out"
fresh; sched_fakes; run -- --entry-role builder; printf '\n[loop]\ntick_interval = 90\n' >>"$prof"; : >"$work/sched.log"
run "${senv[@]}" -- --schedule --scheduler cron --yes
{ [[ $rc -eq 1 && "$out" == *"tick_interval"* && "$out" == *"59"* && ! -e "$work/cron.store" ]]; } || miss "S9 cron with tick_interval = 90 is refused naming the 59 minute limit and installs nothing: rc=$rc out=$out"

# S10: a different entry already at the plist path is not overwritten
fresh; sched_fakes; run -- --entry-role builder; plist="$home/Library/LaunchAgents/com.arcaven.director-tick.plist"
mkdir -p "$home/Library/LaunchAgents"; printf 'someone else\n' >"$plist"; : >"$work/sched.log"
run "${senv[@]}" -- --schedule --scheduler launchd --yes
{ [[ $rc -eq 1 && "$(cat "$plist")" == "someone else" && "$(calls 'launchctl load')" == 0 ]]; } || miss "S10 a different plist at the path is refused and left alone: rc=$rc out=$out"

# F1: the PATH captured in the entry keeps absolute entries and drops relative ones and scratch directories.
# The run goes through $pydir/python3 and is compared with the interpreter that same command reports, so the case
# agrees on a Mac (symlink resolved) and on Linux (symlink kept). $stubs is first on PATH, so no real scheduler
# binary can be reached from these runs even if a regression called one without --yes.
raw_path() { local out="" e; local IFS=:; for e in $1; do case "$e" in /*) ;; *) continue ;; esac; case "$e" in /tmp|/tmp/*|/private/tmp|/private/tmp/*|/var/folders|/var/folders/*|/private/var/folders|/private/var/folders/*) continue ;; esac; out="${out:+$out:}$e"; done; printf '%s' "$out"; }
fresh; sched_fakes; run -- --entry-role builder
fpy="$(env -i PATH="$stubs" "$pydir/python3" -c 'import os, sys; print(os.path.realpath(sys.executable))')"
fpath_in="$stubs:/usr/bin:/opt/x/bin:rel/bin:.::/tmp/a:/private/tmp/b:/var/folders/c:/tmp:/bin:/Users/x/bin"
fwant="$(raw_path "$stubs")"; fwant="${fwant:+$fwant:}/usr/bin:/opt/x/bin:/bin:/Users/x/bin"
set +e; fout="$(env -i HOME="$home" TMPDIR="$work/tmp" PATH="$fpath_in" "$pydir/python3" "$BIN" --schedule --scheduler cron 2>&1 </dev/null)"; frc=$?; set -e
{ [[ $frc -eq 0 && "$fout" == *"PATH=$fwant $fpy $tick_path # director-tick"* ]]; } || miss "F1 the captured PATH drops relative entries, empty entries and anything under /tmp, /private/tmp or /var/folders: rc=$frc want PATH=$fwant $fpy out=$fout"
# an all-scratch PATH falls back to /usr/bin:/bin
fwant="$(raw_path "$stubs")"; fwant="${fwant:-/usr/bin:/bin}"
set +e; fout="$(env -i HOME="$home" TMPDIR="$work/tmp" PATH="$stubs:/tmp/a:rel" "$pydir/python3" "$BIN" --schedule --scheduler cron 2>&1 </dev/null)"; frc=$?; set -e
[[ "$fout" == *"PATH=$fwant "* ]] || miss "F1 a PATH with nothing usable falls back to /usr/bin:/bin: rc=$frc want PATH=$fwant out=$fout"

# F2: the profile edit is checked before anything is installed
fresh; sched_fakes; run -- --entry-role builder; sed -i.bak 's/^schema_version = 1$/schema_version = 1\nloop.tick_interval = 5/' "$prof"; rm -f "$prof.bak"
[[ "$(check_profile "$prof")" == "profile ok"* ]] || miss "F2 setup: the dotted-key profile is valid on its own: $(check_profile "$prof")"
plist="$home/Library/LaunchAgents/com.arcaven.director-tick.plist"; p0="$(sum_of "$prof")"; : >"$work/sched.log"
run "${senv[@]}" -- --schedule --scheduler launchd --yes
{ [[ $rc -eq 1 && ! -e "$plist" && "$(calls 'launchctl load')" == 0 && "$(sum_of "$prof")" == "$p0" ]]; } || miss "F2 a profile edit that fails director-profile check installs nothing and leaves the profile alone: rc=$rc out=$out $(tr '\n' ';' <"$work/sched.log")"

# F3: cron with no crontab yet installs; a crontab -l that fails for another reason touches nothing
fresh; sched_fakes; run -- --entry-role builder; : >"$work/sched.log"
run "${senv[@]}" -- --schedule --scheduler cron --yes
{ [[ $rc -eq 0 && "$(grep -c '# director-tick' "$work/cron.store" 2>/dev/null || true)" == 1 ]]; } || miss "F3 with no crontab at all, --yes installs one marked line: rc=$rc out=$out"
fresh; sched_fakes; run -- --entry-role builder; printf '0 3 * * * /usr/bin/true\n' >"$work/cron.store"; c0="$(sum_of "$work/cron.store")"; p0="$(sum_of "$prof")"; : >"$work/sched.log"
run "${senv[@]}" CRON_FAIL=1 -- --schedule --scheduler cron --yes
{ [[ $rc -eq 1 && "$(sum_of "$work/cron.store")" == "$c0" && "$(calls 'crontab -$')" == 0 && "$(sum_of "$prof")" == "$p0" && "$out" == *"permission denied"* ]]; } || miss "F3 a failing crontab -l is refused with its message, writes no crontab and no profile: rc=$rc out=$out"

# a commented-out or renamed user line that merely mentions the mark does not block the install
fresh; sched_fakes; run -- --entry-role builder; printf '# old note: # director-tick was here\n#*/5 * * * * /x/director-tick # director-tick\n0 3 * * * /usr/bin/true # director-tick-cleanup\n' >"$work/cron.store"
run "${senv[@]}" -- --schedule --scheduler cron --yes
{ [[ $rc -eq 0 && "$(grep -c ' # director-tick$' "$work/cron.store")" == 2 && "$(grep -c 'director-tick-cleanup' "$work/cron.store")" == 1 ]]; } || miss "F-open a line that only mentions the mark (commented out, or director-tick-cleanup) does not block the install: rc=$rc out=$out store=$(cat "$work/cron.store")"

# Review 6051212942 (director#755): a failing launchctl removes the plist this run wrote and leaves the profile alone;
# a differing marked crontab line is refused with the store untouched. Each is a function of $BIN so a mutant can run it.
case_load_fails() {
  fresh; sched_fakes; run -- --entry-role builder
  local pl="$home/Library/LaunchAgents/com.arcaven.director-tick.plist" p0; p0="$(sum_of "$prof")"; : >"$work/sched.log"
  run "${senv[@]}" LAUNCHCTL_FAIL=1 -- --schedule --scheduler launchd --yes
  [[ $rc -eq 1 && ! -e "$pl" && "$(sum_of "$prof")" == "$p0" && "$(calls 'launchctl load')" == 1 && "$out" == *"load failed"* ]]
}
case_differing_line() {
  fresh; sched_fakes; run -- --entry-role builder
  printf '0 3 * * * /usr/bin/true\n*/5 * * * * /other/tick # director-tick\n' >"$work/cron.store"
  local c0 p0; c0="$(sum_of "$work/cron.store")"; p0="$(sum_of "$prof")"; : >"$work/sched.log"
  run "${senv[@]}" -- --schedule --scheduler cron --yes
  [[ $rc -eq 1 && "$(sum_of "$work/cron.store")" == "$c0" && "$(calls 'crontab -$')" == 0 && "$(sum_of "$prof")" == "$p0" && "$out" == *"differs"* ]]
}
case_load_fails && : || miss "R1 a failing launchctl load exits 1, removes the plist this run wrote, leaves the profile unchanged and says so: rc=$rc out=$out"
case_differing_line && : || miss "R2 a differing marked crontab line is refused (rc 1), the store and the profile stay as they were, and crontab - is not called: rc=$rc out=$out"
init_mutant() {  # name old new: a copy of director-init with one fault, beside the scripts it needs
  local m="$work/imut-$1"; mkdir -p "$m"; cp "$here"/director-profile "$m/" 2>/dev/null || true
  "$PY" -I -c '
import sys
src=open(sys.argv[1]).read()
if src.count(sys.argv[2]) != 1: sys.exit(1)
open(sys.argv[4],"w").write(src.replace(sys.argv[2], sys.argv[3]))' "$BIN" "$2" "$3" "$m/director-init" || { miss "mutant $1: the text to mutate is not in director-init"; return 1; }
  chmod +x "$m/director-init"; printf '%s' "$m/director-init"
}
saved_bin="$BIN"
mpath="$(init_mutant keeps_plist '        os.unlink(path)
        err("launchctl load -w' '        err("launchctl load -w')" && { BIN="$mpath"; if case_load_fails; then miss "mutant keeps_plist survives: R1 did not catch a plist kept after a failed load"; else echo "ok  mutant keeps_plist is caught by R1"; fi; BIN="$saved_bin"; }
mpath="$(init_mutant appends_differing '        err("the crontab already has a %s line that differs from this entry; it is left alone" % CRON_MARK)
        return False' '        pass')" && { BIN="$mpath"; if case_differing_line; then miss "mutant appends_differing survives: R2 did not catch a differing line appended"; else echo "ok  mutant appends_differing is caught by R2"; fi; BIN="$saved_bin"; }

# director ruling 01M88ACQWC on director#755 (a) and (b): the interpreter is resolved with realpath and refused when it is
# still under a scratch prefix; /private/var/folders joins the scratch prefixes; and the captured PATH entries are
# compared after realpath too, so a symlinked scratch directory cannot slip through. The functions are exercised by
# loading director-init as a module (nothing runs at import), so the refusals are pinned without a scratch python.
pyfn() {  # <python code using the module m>: prints its result; an exception prints ERR:<type>
  env -i HOME="$home" PATH="$pydir:/usr/bin:/bin" "$PY" -I -c '
import importlib.machinery, importlib.util, os, sys
loader = importlib.machinery.SourceFileLoader("dinit", sys.argv[1])
spec = importlib.util.spec_from_loader("dinit", loader); m = importlib.util.module_from_spec(spec); loader.exec_module(m)
try:
    exec(sys.argv[2])
except Exception as e:
    print("ERR:" + type(e).__name__)' "$BIN" "$1" 2>&1
}
fresh
# (a) a scratch interpreter is refused, naming the resolved path and what to do
for bad in /private/var/folders/aa/scr4tch/python3 /var/folders/aa/scr4tch/python3 /tmp/scr4tch/python3 /private/tmp/scr4tch/python3; do
  got="$(pyfn "print(m.resolve_interpreter(\"$bad\"))")"
  { [[ "$got" == "(None, "*"scr4tch/python3"*"run director-init with the python the tick should keep"* ]]; } || miss "R3 (a) an interpreter at $bad is refused with its path and the instruction: $got"
done
# (a) a symlink to a real interpreter is resolved, so the entry names the real binary and not the link
mkdir -p "$work/lnk"; ln -sf "$(env -i PATH="$pydir" python3 -c 'import os,sys; print(os.path.realpath(sys.executable))')" "$work/lnk/python3"
want_real="$(env -i PATH="$pydir" python3 -c 'import os,sys; print(os.path.realpath(sys.executable))')"
got="$(pyfn "print(m.resolve_interpreter(\"$work/lnk/python3\"))")"
[[ "$got" == "('$want_real', None)" ]] || miss "R3 (a) a symlinked interpreter resolves to the real binary $want_real: $got"
# (b) /private/var/folders is a scratch prefix, in the printed entry too
fresh; sched_fakes; run -- --entry-role builder
set +e; fout="$(env -i HOME="$home" TMPDIR="$work/tmp" PATH="$stubs:/usr/bin:/private/var/folders/d:/private/var/folders:/bin" "$pydir/python3" "$BIN" --schedule --scheduler cron 2>&1 </dev/null)"; frc=$?; set -e
fwant="$(raw_path "$stubs")"; fwant="${fwant:+$fwant:}/usr/bin:/bin"
[[ "$fout" == *"PATH=$fwant "* ]] || miss "R4 (b) /private/var/folders and below are dropped from the captured PATH: rc=$frc want PATH=$fwant out=$fout"
# (b) a PATH entry that is a symlink into a scratch directory is dropped, though its own name is not scratch
got="$(pyfn 'real = os.path.realpath
os.path.realpath = lambda p: {"/opt/link/bin": "/private/var/folders/real/bin", "/usr/bin": "/usr/bin"}.get(p, real(p))
os.environ["PATH"] = "/opt/link/bin:/usr/bin:/opt/ok/bin"
print(m.captured_path())')"
[[ "$got" == "/usr/bin:/opt/ok/bin" ]] || miss "R5 (b) a PATH entry whose realpath is under a scratch prefix is dropped: $got"
# (b) the entry as written is kept for a clean entry, and a PATH of only scratch links falls back
got="$(pyfn 'os.path.realpath = lambda p: "/private/tmp/x"
os.environ["PATH"] = "/opt/a/bin:/opt/b/bin"
print(m.captured_path())')"
[[ "$got" == "/usr/bin:/bin" ]] || miss "R5 (b) a PATH whose entries all resolve into scratch falls back to /usr/bin:/bin: $got"

# ---- the python rule ----
fresh
old_py() {
  set +e
  out="$(env -i HOME="$home" TMPDIR="$work/tmp" PATH="$1" "$PY" -c 'import runpy, sys; sys.version_info = (3, 9, 6, "final", 0); sys.argv = [sys.argv[1], "--check"]; runpy.run_path(sys.argv[0], run_name="__main__")' "$BIN" 2>&1 </dev/null)"
  rc=$?
  set -e
}
mkdir -p "$work/emptybin"
old_py "$work/emptybin"
[[ $rc -eq 2 && "$(printf '%s\n' "$out" | wc -l | tr -d ' ')" == "1" && "$out" == *"python3 3.11 or newer"* && "$out" == *"3.9"* ]] || miss "P20 old python3 must be refused with one line naming 3.11: rc=$rc out=$out"
mkdir -p "$work/newbin"
printf '%s\n' '#!/bin/sh' "echo found-python312 >\"$work/found\"" >"$work/newbin/python3.12"
chmod +x "$work/newbin/python3.12"
old_py "$work/newbin"
[[ -f "$work/found" ]] || miss "P21 a python3.12 on PATH must be used when python3 is too old: rc=$rc out=$out"
mkdir -p "$work/badbin"
printf 'not a program\n' >"$work/badbin/python3.12"; chmod +x "$work/badbin/python3.12"
old_py "$work/badbin"
[[ $rc -eq 2 && "$(printf '%s\n' "$out" | wc -l | tr -d ' ')" == "1" && "$out" == *"python3 3.11 or newer"* ]] || miss "L3 a python3.N that cannot be executed must fall through to the one-line refusal: rc=$rc out=$out"

if [[ $fail -ne 0 ]]; then echo "FAIL"; exit 1; fi
echo "ok: director-init writes the first-build profile, reports what shadows the launcher, and --check reports drift without writing"
