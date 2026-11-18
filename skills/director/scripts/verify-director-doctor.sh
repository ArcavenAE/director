#!/usr/bin/env bash
# Prove `director doctor` (standing loop S9, sim/design/director-standing-loop.md section 7 and slice row 9;
# director's S9 rulings recorded on aae-orc-km3no). Red first: until the doctor exists every doctor case below
# fails on an assertion about what it printed or exited.
#
# What doctor must do:
#   - extend `director-init --check`: print its output verbatim first, then its own rows. --check itself is
#     untouched: its literal output and exit codes are pinned below to the bytes recorded from main
#     d604519cd5f9308e877cb1ca468184211a78c73a (director-init --check under env -i, scratch HOME, DIRECTOR_HOME
#     and DIRECTOR_STATE), before the doctor existed;
#   - exit 1 when --check exits 1, or when the standing hook is not wired in Claude Code settings; every age only
#     prints. "Wired" means a hooks.UserPromptSubmit command that names standing-hook or whose words resolve
#     (realpath) to the installed standing-hook, under another name too. The `hook:` row always prints the
#     settings paths it searched, so a result that depends on the directory doctor runs in is visible. A hook
#     wired through a wrapper script with its own name is a known false negative;
#   - with no readable Claude Code settings file (or another harness) say so on the hook row and do not fail;
#   - read only: write nothing, create no directory, call no tmux, nats or gh.
# Rows after --check's output, in this order:
#   hook: wired (<file>); searched: <path>[ (absent)], ...        | hook: not wired; searched: ...
#         | hook: not checked (no Claude Code settings file found); searched: ...
#   drift: the standing hook is not wired in Claude Code settings    (only when not wired)
#   ages:, then two-space rows: last drain, desk read, ledger, digest, tick  ("unknown" when absent; a mark
#         " (over 12h)" for the digest, " (over 24h)" for the ledger and " (stale, over 90m)" for the tick)
#   seats not live: <id> (<reason>), ... | none | unknown (no loop-status.json)
#
# Run conditions: every run is env -i with a scratch HOME and TMPDIR, no MARVEL_*, DIRECTOR_* (beyond
# DIRECTOR_HOME and DIRECTOR_STATE set inside scratch) or NATS_* variable; CLAUDE_CONFIG_DIR is scratch; install.sh
# runs against scratch homes only; no network, no write outside scratch.
#
# Usage: skills/director/scripts/verify-director-doctor.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
PY="$(command -v python3 || true)"
[[ -n "$PY" ]] || { echo "python3 not on PATH"; exit 2; }
pydir="$(dirname "$PY")"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(cd "$(mktemp -d "${TMPDIR:-/tmp}/verify-doctor.XXXXXX")" && pwd -P)"
trap 'chmod -R u+rwx "$root" 2>/dev/null; rm -rf "$root"' EXIT  # the write-protected state dir below must not block the cleanup
mkdir -p "$root/tmp"

fakes="$root/fakes"; mkdir -p "$fakes"; calls="$root/calls.log"; : >"$calls"
for t in tmux nats nats-server gh; do printf '#!/bin/sh\necho "%s $*" >>"%s"\nexit 0\n' "$t" "$calls" >"$fakes/$t"; chmod +x "$fakes/$t"; done

# newhome <name>: scratch HOME with a link install and a written profile; sets H, DH, ST, CC, PROJ
newhome() {
  H="$root/$1"; DH="$H/director"; ST="$H/state"; CC="$H/cc"; PROJ="$H/proj"
  rm -rf "$H"; mkdir -p "$H" "$CC" "$PROJ" "$ST"
  env -i HOME="$H" TMPDIR="$root/tmp" PATH="$pydir:/usr/bin:/bin" LANG=C CLAUDE_HOME="$H/claude" DIRECTOR_HOME="$DH" DIRECTOR_STATE="$ST" \
    "$here/install.sh" >"$H/install.out" 2>&1 || bad "the scratch install ran ($1)" "$(tail -n 3 "$H/install.out")"
  env -i HOME="$H" TMPDIR="$root/tmp" PATH="$pydir:/usr/bin:/bin" LANG=C DIRECTOR_HOME="$DH" DIRECTOR_STATE="$ST" \
    "$DH/bin/director-init" --yes >"$H/init.out" 2>&1 || bad "director-init wrote the scratch profile ($1)" "$(tail -n 3 "$H/init.out")"
}
# run <name> <program> [args]: env -i run from $PROJ; output in $root/<name>.{out,err,rc}
run() {
  local n="$1"; shift
  local rc=0
  (cd "$PROJ" && env -i HOME="$H" TMPDIR="$root/tmp" PATH="$fakes:$pydir:/usr/bin:/bin" LANG=C DIRECTOR_HOME="$DH" DIRECTOR_STATE="$ST" CLAUDE_CONFIG_DIR="$CC" "$@") \
    >"$root/$n.out" 2>"$root/$n.err" || rc=$?
  printf '%s' "$rc" >"$root/$n.rc"
}
rcof() { cat "$root/$1.rc"; }
settings() { # settings <file> <command>: a Claude Code settings file with one UserPromptSubmit hook command
  mkdir -p "$(dirname "$1")"
  printf '{"hooks": {"UserPromptSubmit": [{"hooks": [{"type": "command", "command": "%s"}]}]}}\n' "$2" >"$1"
}
treehash() { (cd "$1" 2>/dev/null && find . -type f -exec shasum -a 256 {} + | sort | shasum -a 256) || echo none; }
ago_min() { "$PY" -I -c 'import datetime as d,sys;print((d.datetime.now(d.timezone.utc)-d.timedelta(minutes=float(sys.argv[1]))).strftime("%Y-%m-%dT%H:%M:%SZ"))' "$1"; }
touch_ago() { "$PY" -I -c 'import os,sys,time;t=time.time()-float(sys.argv[2])*60;os.utime(sys.argv[1],(t,t))' "$1" "$2"; }

# --- director-init --check is untouched: main's bytes -------------------------------------------------------------
# recorded at main d604519c: an empty home prints three drift lines and exits 1; a home with the launcher
# installed and a profile written prints "clean" and exits 0
H="$root/bare"; DH="$H/director"; ST="$H/state"; CC="$H/cc"; PROJ="$H/proj"; mkdir -p "$H" "$PROJ"
run check-bare "$here/skills/director/scripts/director-init" --check
want=$'drift: no profile file at '"$ST"$'/operating-profile.toml (run director-init)\ndrift: the launcher is missing: '"$DH"$'/bin/director-session\ndrift: director-profile is missing: '"$DH"'/bin/director-profile'
{ [[ "$(rcof check-bare)" == 1 && "$(cat "$root/check-bare.out")" == "$want" && ! -s "$root/check-bare.err" ]]; } \
  && ok "director-init --check on an empty home: main's three drift lines, exit 1" || bad "director-init --check on an empty home" "rc=$(rcof check-bare) out=[$(cat "$root/check-bare.out")]"
newhome clean
run check-clean "$DH/bin/director-init" --check
{ [[ "$(rcof check-clean)" == 0 && "$(cat "$root/check-clean.out")" == "clean" && ! -s "$root/check-clean.err" ]]; } \
  && ok "director-init --check on a clean install: main's \"clean\", exit 0" || bad "director-init --check on a clean install" "rc=$(rcof check-clean) out=[$(cat "$root/check-clean.out")]"

# --- doctor, a clean install --------------------------------------------------------------------------------------
USER_JSON="$CC/settings.json"; USER_LOCAL="$CC/settings.local.json"; PROJ_JSON="$PROJ/.claude/settings.json"; PROJ_LOCAL="$PROJ/.claude/settings.local.json"
searched_all() { # every settings path appears on the hook row
  local row; row="$(grep '^hook: ' "$root/$1.out" || true)"
  [[ "$row" == *"$USER_JSON"* && "$row" == *"$USER_LOCAL"* && "$row" == *"$PROJ_JSON"* && "$row" == *"$PROJ_LOCAL"* ]]
}

# unwired-hook-exit1: a settings file that parses and names no hook
settings "$USER_JSON" "echo not-the-hook"
run unwired "$DH/bin/director" doctor
{ [[ "$(rcof unwired)" == 1 ]] && grep -q '^hook: not wired; searched: ' "$root/unwired.out" && grep -qx 'drift: the standing hook is not wired in Claude Code settings' "$root/unwired.out"; } \
  && ok "unwired-hook-exit1: a Claude Code settings file that does not name the hook is drift, exit 1" || bad "unwired-hook-exit1" "rc=$(rcof unwired) out=[$(head -c 300 "$root/unwired.out")] err=[$(head -c 100 "$root/unwired.err")]"
searched_all unwired && ok "the hook row lists all four searched settings paths when it is not wired" || bad "hook row paths, not wired" "$(grep '^hook: ' "$root/unwired.out" || true)"
# doctor extends --check: its output comes first, byte for byte
head -n 1 "$root/unwired.out" | grep -qx clean && ok "doctor prints --check's output first (clean), then its rows" || bad "doctor prints --check's output first" "$(head -n 2 "$root/unwired.out")"

# wired by name, with the paths listed, and the ages block
settings "$USER_JSON" "$DH/bin/standing-hook"
touch "$ST/digest.md"; touch_ago "$ST/digest.md" 785
run wired "$DH/bin/director" doctor
{ [[ "$(rcof wired)" == 0 ]] && grep -q "^hook: wired ($USER_JSON); searched: " "$root/wired.out"; } \
  && ok "a hook named in the user settings is wired, exit 0" || bad "wired by name" "rc=$(rcof wired) out=[$(head -c 300 "$root/wired.out")]"
searched_all wired && ok "the hook row lists all four searched settings paths when it is wired" || bad "hook row paths, wired" "$(grep '^hook: ' "$root/wired.out" || true)"
grep -q "^hook: .*$PROJ_JSON (absent)" "$root/wired.out" && ok "a settings path that does not exist is marked absent" || bad "absent mark" "$(grep '^hook: ' "$root/wired.out" || true)"

# stale-digest-exit0: an old digest is reported and never fails the command
{ [[ "$(rcof wired)" == 0 ]] && grep -qx '  digest: 13h (over 12h)' "$root/wired.out"; } \
  && ok "stale-digest-exit0: a 13 hour old digest is reported with its mark and the exit stays 0" || bad "stale-digest-exit0" "rc=$(rcof wired) out=[$(cat "$root/wired.out")]"
grep -qx 'ages:' "$root/wired.out" && grep -qx '  last drain: unknown' "$root/wired.out" && grep -qx '  tick: unknown' "$root/wired.out" \
  && ok "an age with no source reads unknown" || bad "unknown ages" "$(cat "$root/wired.out")"

# the ages that exist
printf '%s\n' "$(ago_min 7)" >"$ST/last-drain"
printf '{}\n' >"$ST/workstreams.jsonl"; touch_ago "$ST/workstreams.jsonl" 1500
: >"$ST/desk-reads.jsonl"; touch_ago "$ST/desk-reads.jsonl" 300
printf '{"schema": 1, "ran_at": "%s", "steps": {}, "due": {}, "seats": []}\n' "$(ago_min 150)" >"$ST/loop-status.json"
run ages "$DH/bin/director" doctor
{ [[ "$(rcof ages)" == 0 ]] && grep -qx '  last drain: 7m' "$root/ages.out" && grep -qx '  desk read: 5h' "$root/ages.out" \
  && grep -qx '  ledger: 25h (over 24h)' "$root/ages.out" && grep -qx '  tick: 2h (stale, over 90m)' "$root/ages.out"; } \
  && ok "last drain, desk read, ledger and tick print their ages and marks; the exit stays 0" || bad "ages block" "rc=$(rcof ages) out=[$(cat "$root/ages.out")]"

# seat-not-live-listed, and the other seat states
printf '{"schema": 1, "ran_at": "%s", "steps": {}, "due": {}, "seats": [{"id":"a1","cue":"live"},{"id":"b2","cue":"unverified","cue_reason":"no nonce echo after self-test"},{"id":"c3","cue":"failed"}]}\n' "$(ago_min 5)" >"$ST/loop-status.json"
run seats "$DH/bin/director" doctor
{ [[ "$(rcof seats)" == 0 ]] && grep -qx 'seats not live: b2 (no nonce echo after self-test), c3' "$root/seats.out"; } \
  && ok "seat-not-live-listed: every seat whose cue is not live is named with its reason; the exit stays 0" || bad "seat-not-live-listed" "rc=$(rcof seats) out=[$(grep seats "$root/seats.out" || true)]"
printf '{"schema": 1, "ran_at": "%s", "steps": {}, "due": {}, "seats": [{"id":"a1","cue":"live"}]}\n' "$(ago_min 5)" >"$ST/loop-status.json"
run seats-none "$DH/bin/director" doctor
grep -qx 'seats not live: none' "$root/seats-none.out" && ok "all seats live: none" || bad "seats none" "$(grep seats "$root/seats-none.out" || true)"
rm -f "$ST/loop-status.json"
run seats-unknown "$DH/bin/director" doctor
grep -qx 'seats not live: unknown (no loop-status.json)' "$root/seats-unknown.out" && ok "no loop-status.json: unknown" || bad "seats unknown" "$(grep seats "$root/seats-unknown.out" || true)"

# the widened match: a command that resolves to the installed hook by path, under another name
mkdir -p "$root/links"; ln -s "$DH/bin/standing-hook" "$root/links/my-prompt-hook"
settings "$USER_JSON" "$root/links/my-prompt-hook"
run byresolve "$DH/bin/director" doctor
{ [[ "$(rcof byresolve)" == 0 ]] && grep -q "^hook: wired ($USER_JSON); searched: " "$root/byresolve.out"; } \
  && ok "a command that resolves to the installed hook by path, under another name, is wired" || bad "wired by resolved path" "rc=$(rcof byresolve) out=[$(grep '^hook' "$root/byresolve.out" || true)]"
# a project-level settings file counts too, and it is listed
settings "$USER_JSON" "echo not-the-hook"
settings "$PROJ_JSON" "$DH/bin/standing-hook"
run projwired "$DH/bin/director" doctor
{ [[ "$(rcof projwired)" == 0 ]] && grep -q "^hook: wired ($PROJ_JSON); searched: " "$root/projwired.out"; } \
  && ok "a hook wired in the project settings of the directory doctor runs in is wired" || bad "wired in project settings" "rc=$(rcof projwired) out=[$(grep '^hook' "$root/projwired.out" || true)]"
rm -rf "$PROJ/.claude"
# a hook wired through a wrapper with its own name stays a known false negative, and says not wired
printf '#!/bin/sh\nexec "%s"\n' "$DH/bin/standing-hook" >"$root/links/wrapper.sh"; chmod +x "$root/links/wrapper.sh"
settings "$USER_JSON" "$root/links/wrapper.sh"
run wrapper "$DH/bin/director" doctor
{ [[ "$(rcof wrapper)" == 1 ]] && grep -q '^hook: not wired; searched: ' "$root/wrapper.out"; } \
  && ok "a wrapper script with its own name is the known false negative: not wired, exit 1" || bad "wrapper false negative" "rc=$(rcof wrapper) out=[$(grep '^hook' "$root/wrapper.out" || true)]"

# no settings file at all, or none that parses: not checked, not a failure
rm -f "$USER_JSON" "$USER_LOCAL"
run nosettings "$DH/bin/director" doctor
{ [[ "$(rcof nosettings)" == 0 ]] && grep -q '^hook: not checked (no Claude Code settings file found); searched: ' "$root/nosettings.out" && ! grep -q '^drift:' "$root/nosettings.out"; } \
  && ok "no Claude Code settings file: the hook is not checked, no drift, exit 0" || bad "no settings file" "rc=$(rcof nosettings) out=[$(head -c 300 "$root/nosettings.out")]"
searched_all nosettings && ok "the hook row lists the searched paths when nothing was found" || bad "hook row paths, not checked" "$(grep '^hook: ' "$root/nosettings.out" || true)"
printf 'not json\n' >"$USER_JSON"
run badjson "$DH/bin/director" doctor
{ [[ "$(rcof badjson)" == 0 ]] && grep -q '^hook: not checked ' "$root/badjson.out"; } \
  && ok "a settings file that does not parse is not checked, exit 0" || bad "unparseable settings" "rc=$(rcof badjson) out=[$(grep '^hook' "$root/badjson.out" || true)]"

# --check's verdict still decides the exit: a drifting install fails even with the hook wired
settings "$USER_JSON" "$DH/bin/standing-hook"
# (the profile removed, so --check drifts)
rm -f "$ST/operating-profile.toml"
run drifting "$DH/bin/director" doctor
{ [[ "$(rcof drifting)" == 1 ]] && head -n 1 "$root/drifting.out" | grep -q '^drift: no profile file at ' && grep -q '^hook: wired ' "$root/drifting.out"; } \
  && ok "when --check drifts doctor exits 1, prints --check's drift lines first and still prints its own rows" || bad "check drift decides the exit" "rc=$(rcof drifting) out=[$(head -c 300 "$root/drifting.out")]"

# the hook seen row (standing loop S8, km3no's remaining row): doctor reads $DIRECTOR_STATE/hook-seen, which
# `standing-hook --seen` writes, and prints how long ago a hook last ran as "hook seen: <age>", "hook seen: never"
# with no stamp, "hook seen: unknown" for a stamp it cannot read or that lies in the future. It sits after the hook
# row(s) and before the ages block, only prints, and never changes the exit status. It is the only thing that tells
# a hook that is wired but never reached from one that is not wired.
newhome seen
settings "$CC/settings.json" "$DH/bin/standing-hook"
seen_row() { { grep -n '^hook seen: ' "$root/$1.out" || true; } | head -n 1 | cut -d: -f2-; }
run seen-never "$DH/bin/director" doctor
{ [[ "$(rcof seen-never)" == 0 && "$(seen_row seen-never)" == "hook seen: never" ]]; } \
  && ok "hook seen: never when there is no stamp, exit stays 0" || bad "hook seen never" "rc=$(rcof seen-never) row=[$(seen_row seen-never)]"
printf '%s\n' "$(ago_min 7)" >"$ST/hook-seen"
run seen-7 "$DH/bin/director" doctor
{ [[ "$(rcof seen-7)" == 0 && "$(seen_row seen-7)" == "hook seen: 7m" ]]; } \
  && ok "hook seen: a stamp 7 minutes old reads 7m" || bad "hook seen 7m" "rc=$(rcof seen-7) row=[$(seen_row seen-7)]"
n_hook="$({ grep -n '^hook: ' "$root/seen-7.out" || true; } | head -n 1 | cut -d: -f1)"; n_seen="$({ grep -n '^hook seen: ' "$root/seen-7.out" || true; } | head -n 1 | cut -d: -f1)"; n_ages="$({ grep -n '^ages:' "$root/seen-7.out" || true; } | head -n 1 | cut -d: -f1)"
{ [[ -n "$n_hook" && -n "$n_seen" && -n "$n_ages" ]] && ((n_hook < n_seen && n_seen < n_ages)); } \
  && ok "the hook seen row sits after the hook row and before the ages block" || bad "hook seen row order" "hook=$n_hook seen=$n_seen ages=$n_ages"
printf '%s\n' "$(ago_min 190)" >"$ST/hook-seen"
run seen-old "$DH/bin/director" doctor
{ [[ "$(rcof seen-old)" == 0 && "$(seen_row seen-old)" == "hook seen: 3h" ]]; } \
  && ok "hook seen: an old stamp reads its age and never fails the command" || bad "hook seen old" "rc=$(rcof seen-old) row=[$(seen_row seen-old)]"
printf 'garbage\n' >"$ST/hook-seen"
run seen-bad "$DH/bin/director" doctor
{ [[ "$(rcof seen-bad)" == 0 && "$(seen_row seen-bad)" == "hook seen: unknown" ]]; } \
  && ok "hook seen: a stamp that does not parse reads unknown" || bad "hook seen garbled" "rc=$(rcof seen-bad) row=[$(seen_row seen-bad)]"
printf '2999-01-01T00:00:00Z\n' >"$ST/hook-seen"
run seen-future "$DH/bin/director" doctor
[[ "$(seen_row seen-future)" == "hook seen: unknown" ]] && ok "hook seen: a stamp in the future reads unknown" || bad "hook seen future" "row=[$(seen_row seen-future)]"
printf '%s\n' "$(ago_min 7)" >"$ST/hook-seen"
settings "$CC/settings.json" "echo not-the-hook"
run seen-unwired "$DH/bin/director" doctor
{ [[ "$(rcof seen-unwired)" == 1 && "$(seen_row seen-unwired)" == "hook seen: 7m" ]]; } \
  && ok "hook seen: printed even when the settings do not wire the hook (the plugin may have); the exit is the unwired verdict" || bad "hook seen, unwired settings" "rc=$(rcof seen-unwired) row=[$(seen_row seen-unwired)]"
before_s="$(treehash "$ST")"; : >"$calls"
run seen-ro "$DH/bin/director" doctor
{ [[ "$before_s" == "$(treehash "$ST")" ]]; } && ok "reading the stamp writes nothing" || bad "doctor wrote while reading the stamp"

# the launcher's usage: main's lines, plus the status line S7 added, plus one doctor line right after it
run usage "$DH/bin/director" nosuchword
want_usage=$'usage: director start [--cluster <name>] [--resume <session name>] [--seat-file <path>]\n       director env [--seat-file <path>]\n       director prove\n       director status\n       director doctor\n       director start --plugin-dir <path>   (load a Claude Code plugin tree for this session)\n       director start --no-plugin   (do not load the installed plugin for this session)\n       director hub user|settings ...   (run on the hub host; see sim/design/hub-user-lifecycle.md)'
{ [[ "$(rcof usage)" == 2 && "$(cat "$root/usage.err")" == "$want_usage" ]]; } \
  && ok "the launcher's usage gains exactly the status, doctor, plugin-dir and no-plugin lines, in that order" || bad "usage text" "rc=$(rcof usage) err=[$(cat "$root/usage.err")]"

# read only
newhome ro
settings "$CC/settings.json" "$DH/bin/standing-hook"
touch "$ST/digest.md"; printf '%s\n' "$(ago_min 3)" >"$ST/last-drain"
before_s="$(treehash "$ST")"; before_h="$(treehash "$H")"; : >"$calls"
run readonly "$DH/bin/director" doctor
{ [[ "$(rcof readonly)" == 0 && "$before_s" == "$(treehash "$ST")" && "$before_h" == "$(treehash "$H")" ]]; } \
  && ok "doctor writes nothing: the state tree and the home tree are unchanged" || bad "doctor wrote something, or failed" "rc=$(rcof readonly)"
[[ ! -s "$calls" ]] && ok "doctor calls no tmux, nats or gh" || bad "doctor called a pane, bus or GitHub tool" "$(cat "$calls")"
rm -rf "$root/absent-state"
(cd "$PROJ" && env -i HOME="$H" TMPDIR="$root/tmp" PATH="$fakes:$pydir:/usr/bin:/bin" LANG=C DIRECTOR_HOME="$DH" DIRECTOR_STATE="$root/absent-state/s" CLAUDE_CONFIG_DIR="$CC" "$DH/bin/director" doctor) >"$root/nostate.out" 2>/dev/null || true
{ [[ ! -e "$root/absent-state" ]] && grep -qx '  digest: unknown' "$root/nostate.out"; } \
  && ok "a missing state directory reads unknown ages and is not created" || bad "missing state directory" "exists=$([[ -e "$root/absent-state" ]] && echo yes || echo no) out=[$(grep digest "$root/nostate.out" || true)]"

# the third exit rule: an existing state directory that is not writable is drift, exit 1 (everything else
# stays as for a clean install). Skipped as root, where a mode bit does not stop a write.
if [[ "$(id -u)" == 0 ]]; then
  echo "SKIP the state directory is not writable: running as root, where a mode bit does not stop a write"
else
  newhome nw
  settings "$CC/settings.json" "$DH/bin/standing-hook"
  chmod 555 "$ST"
  run notwritable "$DH/bin/director" doctor
  chmod 755 "$ST"
  { [[ "$(rcof notwritable)" == 1 ]] && grep -qxF "drift: the state directory is not writable: $ST" "$root/notwritable.out" && grep -q '^hook: wired ' "$root/notwritable.out"; } \
    && ok "an existing state directory that is not writable is drift, exit 1, and the other rows still print" || bad "unwritable state directory" "rc=$(rcof notwritable) out=[$(head -c 400 "$root/notwritable.out")]"
fi

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
