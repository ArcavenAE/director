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
has "$out" "unattached: 5 agent processes on hosta" "the count line names the count and the host"
[[ ! -e "$FIX/kill.log" ]] && ok "nothing was killed" || bad "nothing was killed" "$(cat "$FIX/kill.log")"
! grep -Eq '(^|[^a-z_-])kill( |$)' "$UN" && ok "the script has no kill command" || bad "the script has no kill command"

out="$(run "$PS_HEAD
  200     1       05:00 /usr/bin/vim notes.txt" "" "")"
has "$out" "unattached: 0 agent processes on hosta" "no match prints a zero count, not nothing"

out="$(run "$PS_HEAD
  200     1       05:00 /usr/bin/vim notes.txt" "" "" UN_HOSTNAME=hosta UNATTACHED_HOSTS=hosta,hostb)"
has "$out" "GAP hostb" "another host prints a GAP line"
hasnt "$out" "GAP hosta" "the local host is not a GAP"

echo "$pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
