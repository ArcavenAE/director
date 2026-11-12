#!/usr/bin/env bash
# Prove unattached: the read-only sweep line that lists agent processes on the local host
# with no marvel parent. Each case builds a scratch dir with stubs for ps, lsof and hostname
# first on PATH, and runs the script under `env -i`. No real process table is read, and the
# stubs log any kill, which the script must never make.
#
# Usage: skills/director/scripts/verify-unattached.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
UN="${UN:-$here/unattached}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d "${TMPDIR:-/tmp}/un.XXXXXX")"
root="$(cd "$root" && pwd -P)"
trap 'rm -rf "$root"' EXIT
mkdir -p "$root/bin" "$root/fix" "$root/state"
FIX="$root/fix"

cat >"$root/bin/ps" <<'S'
#!/usr/bin/env bash
cat "$UN_FIX/ps.txt"
S
cat >"$root/bin/lsof" <<'S'
#!/usr/bin/env bash
if [[ "$*" == *"-d cwd"* ]]; then
  pid="${*: -1}"
  p="$(awk -v p="$pid" '$1==p {print $2}' "$UN_FIX/cwd.txt")"
  [[ -n "$p" ]] && printf 'p%s\nfcwd\nn%s\n' "$pid" "$p"
  exit 0
fi
if [[ "$*" == *"-sTCP:LISTEN"* ]]; then
  while read -r pid _; do [[ -n "$pid" ]] && printf 'p%s\n' "$pid"; done <"$UN_FIX/listen.txt"
  exit 0
fi
exit 1
S
cat >"$root/bin/hostname" <<'S'
#!/usr/bin/env bash
echo "${UN_HOSTNAME:-hosta}"
S
cat >"$root/bin/kill" <<'S'
#!/usr/bin/env bash
echo "$*" >>"$UN_FIX/kill.log"
S
chmod +x "$root/bin/"*

# run ps-lines cwd-lines listen-lines [extra env...]; prints the script's stdout.
run() {
  printf '%s\n' "$1" >"$FIX/ps.txt"; printf '%s\n' "$2" >"$FIX/cwd.txt"; printf '%s\n' "$3" >"$FIX/listen.txt"
  shift 3
  env -i HOME="$root/home" PATH="$root/bin:/usr/bin:/bin" TMPDIR="$root" UN_FIX="$FIX" \
    DIRECTOR_STATE="$root/state" "$@" "$UN" 2>"$root/err"
}
has() { grep -qF -- "$2" <<<"$1" && ok "$3" || bad "$3" "want [$2] in: $1"; }
hasnt() { grep -qF -- "$2" <<<"$1" && bad "$3" "did not want [$2] in: $1" || ok "$3"; }

PS_HEAD='  PID  PPID     ELAPSED COMMAND'

out="$(run "$PS_HEAD
  100     1  9-02:03:04 claude --resume
  200     1       05:00 /usr/bin/vim notes.txt
  300   310       01:00 codex exec
  310     1    1-00:00:00 /usr/local/bin/marvel daemon
  400   410       02:00 /bin/zsh -il
  410     1       03:00 /bin/zsh
  500   400       07:30 goose run
  600     1  9-00:00:00 /x/goosewatch.test -goosewatch-reexec tok wrapper
  700     1  9-00:00:00 /x/other.test -test.v
  800     1  1-00:00:00 python -m http.server 0 --bind 127.0.0.1
  900     1  1-00:00:00 python -m http.server 0 --bind 127.0.0.1" \
"100 /work/a
300 /work/b
500 /work/c
600 /work/d/.worktrees/g/internal
800 /work/e/.worktrees/h
900 /work/e/plain" \
"800
900")"
has "$out" "pid 100 " "a claude with parent pid 1 is listed"
has "$out" "age 9-02:03:04" "the line carries the age"
has "$out" "cwd /work/a" "the line carries the cwd"
has "$out" "parent 1" "the line carries the parent"
has "$out" "reason: parent is pid 1" "the reason names a pid 1 parent"
hasnt "$out" "pid 200 " "a process that matches no agent pattern is not listed"
hasnt "$out" "pid 300 " "a codex under a marvel parent is not listed"
has "$out" "pid 500 " "a goose under a non-marvel parent is listed"
has "$out" "reason: parent is not marvel (zsh)" "the reason names the non-marvel parent"
has "$out" "pid 600 " "a goosewatch re-exec test child is listed"
has "$out" "reason: test re-exec child" "the reason names a re-exec test child"
hasnt "$out" "pid 700 " "a test binary without the re-exec flag is not listed"
has "$out" "pid 800 " "a 127.0.0.1 listener under .worktrees is listed"
has "$out" "reason: 127.0.0.1 listener under .worktrees" "the reason names the listener"
hasnt "$out" "pid 900 " "a listener outside .worktrees is not listed"
has "$out" "unattached: 4 agent processes on hosta" "the count line names the count and the host"
[[ ! -e "$FIX/kill.log" ]] && ok "nothing was killed" || bad "nothing was killed" "$(cat "$FIX/kill.log")"
! grep -Eq '(^|[^a-z_-])kill( |$)' "$UN" && ok "the script has no kill command" || bad "the script has no kill command"

out="$(run "$PS_HEAD
  200     1       05:00 /usr/bin/vim notes.txt" "" "")"
has "$out" "unattached: 0 agent processes on hosta" "no match prints a zero count, not nothing"

out="$(run "$PS_HEAD
  200     1       05:00 /usr/bin/vim notes.txt" "" "" UN_HOSTNAME=hosta UNATTACHED_HOSTS=hosta,hostb)"
has "$out" "GAP hostb" "another host prints a GAP line"
hasnt "$out" "GAP hosta" "the local host is not a GAP"

# Fix round 1 (review of the first head). With no host list, one GAP line says the other hosts went unread.
out="$(run "$PS_HEAD
  200     1       05:00 /usr/bin/vim notes.txt" "" "")"
has "$out" "- GAP other hosts (no host list; only this host was read)" "no host list prints a GAP for the unread hosts"
out="$(run "$PS_HEAD
  200     1       05:00 /usr/bin/vim notes.txt" "" "" UNATTACHED_HOSTS=hosta,hostb)"
hasnt "$out" "GAP other hosts" "a host list suppresses the no-host-list GAP"
printf 'hosta\n' >"$root/state/unattached.hosts"
out="$(run "$PS_HEAD
  200     1       05:00 /usr/bin/vim notes.txt" "" "")"
has "$out" "GAP other hosts" "a host file naming only this host is still no other host read"
rm -f "$root/state/unattached.hosts"

# A parent counts as marvel by its program name, or a tmux server started with -L marvel-, not by a substring.
out="$(run "$PS_HEAD
  910   911       01:00 claude -p fix
  911     1       02:00 /bin/bash /work/marvel/scripts/run-agent.sh
  920   921       01:00 codex exec
  921     1       02:00 tmux -L notes new -s marvel-notes
  930   931       01:00 claude --resume
  931     1       02:00 tmux -L marvel-1d new-session -d
  940   941       01:00 goose run
  941     1       02:00 /opt/bin/marvel daemon" \
"910 /w/a
920 /w/b
930 /w/c
940 /w/d" "")"
has "$out" "pid 910 " "a claude under a script path that contains marvel is listed"
has "$out" "pid 920 " "a codex under a tmux session named for marvel is listed"
hasnt "$out" "pid 930 " "a seat under a tmux server started with -L marvel- is not listed"
hasnt "$out" "pid 940 " "a goose under the marvel program is not listed"

# The second word of a command names an agent only when the first word is an interpreter.
out="$(run "$PS_HEAD
  950     1       01:00 vim claude
  951     1       01:00 grep codex
  952     1       01:00 node /usr/lib/claude
  953     1       01:00 python3 /x/goose" \
"950 /w/a
951 /w/b
952 /w/c
953 /w/d" "")"
hasnt "$out" "pid 950 " "vim claude is not an agent"
hasnt "$out" "pid 951 " "grep codex is not an agent"
has "$out" "pid 952 " "node running claude is an agent"
has "$out" "pid 953 " "python3 running goose is an agent"

echo "$pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
