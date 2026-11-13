#!/usr/bin/env bash
# Prove scripts/director-preflight (I-473 gap 3): a check that names each tool a
# director install needs and does not find, so a new user learns what is missing
# from one run and not from a script that dies halfway.
#
# Contract the cases fix:
#   - the manifest scripts/director-preflight.tsv declares every tool below, with
#     its class (required or optional); the check reads that manifest. The lists
#     REQUIRED and OPTIONAL here are the contract the manifest must meet (case 0b),
#     not a second source the check reads.
#   - output, one line per tool: "OK <tool> <version>", "MISSING <tool> (<class>) ...
#     install: <hint>", "OLD <tool> (<class>) found X, wanted >= Y ...", and
#     "UNREADABLE <tool> (<class>) ..." for a present tool whose version cannot be
#     read while the manifest sets a minimum for it.
#   - the install hint is the one for the running OS (uname -s: Darwin gives the
#     macOS hint, Linux the Linux hint).
#   - exit 1 when a required tool is missing, too old or unreadable against its
#     minimum; exit 0 otherwise. An optional tool never changes the exit status.
#   - a present tool with no declared minimum is OK whatever its --version prints
#     (jq and git carry no minimum, so their real version formats are exercised).
#   - the check may call only: bash, awk, grep, head, uname, dirname, cat, tr, sort,
#     sed, printf (and the shell's own command and read). The scratch PATH holds
#     exactly those plus the verifier's own helpers; a check that needs another
#     utility fails here on purpose.
#   - it writes nothing: not to the home, the cwd or the TMPDIR; no network.
#
# Not covered, on purpose: the check opens no network socket (nothing here traps a
# bash /dev/tcp connection) and writes to no fixed path outside the scratch dirs
# (only the home, cwd and TMPDIR it is given are inspected).
#
# Each case builds a scratch PATH of symlinks to those utilities plus stub tools
# that answer --version, then removes, ages or breaks one tool.
#
# Usage: scripts/verify-director-preflight.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PF="${PF:-$here/director-preflight}"
MANIFEST="${MANIFEST:-$here/director-preflight.tsv}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d "${TMPDIR:-/tmp}/pf.XXXXXX")"
root="$(cd "$root" && pwd -P)"
trap 'rm -rf "$root"' EXIT

REQUIRED="python3 jq git nats-server nats"
OPTIONAL="gh claude marvel tmux lsof go"

# the utilities the check may call, and only those, are linked into the scratch PATH.
# The verifier's own helpers (ls, wc, rm, mkdir, env, tail, cut, basename) run under
# the verifier's PATH, outside the check's, so a check that calls one of them fails here.
allowed="bash awk grep head uname dirname cat tr sort sed printf"

# real --version first lines, so a stub reads as the tool does
ver_text() {
  case "$1" in
    python3) echo "Python 3.12.1" ;;
    jq) echo "jq-1.7.1" ;;
    git) echo "git version 2.50.1" ;;
    nats-server) echo "nats-server: v2.11.4" ;;
    nats) echo "0.4.0" ;;
    gh) echo "gh version 2.102.0 (2026-11-01)" ;;
    claude) echo "2.1.323 (Claude Code)" ;;
    marvel) echo "marvel 0.1.0 (alpha)" ;;
    tmux) echo "tmux 3.5a" ;;
    lsof) echo "lsof version information: revision 4.99.4" ;;
    go) echo "go version go1.26.5 darwin/arm64" ;;
  esac
}

stub() { # stub DIR NAME [TEXT]: a tool that prints TEXT (default: its real format) and exits 0
  local text="${3-}"
  [[ -n "$text" ]] || text="$(ver_text "$2")"
  printf '#!/bin/sh\necho "%s"\n' "$text" >"$1/$2"; chmod +x "$1/$2"
}
stub_broken() { # stub_broken DIR NAME: present, prints nothing, exits 1
  printf '#!/bin/sh\nexit 1\n' >"$1/$2"; chmod +x "$1/$2"
}
stub_uname() { # stub_uname DIR OS
  printf '#!/bin/sh\necho "%s"\n' "$2" >"$1/uname"; chmod +x "$1/uname"
}

new_path() { # new_path DIR [OS]: a PATH dir with the utilities, every stub tool, and uname answering OS (default Darwin)
  local d="$1" c t q
  rm -rf "$d"; mkdir -p "$d"
  for c in $allowed; do
    for q in /usr/bin /bin; do if [[ -x "$q/$c" ]]; then ln -s "$q/$c" "$d/$c"; break; fi; done
  done
  for t in $REQUIRED $OPTIONAL; do stub "$d" "$t"; done
  rm -f "$d/uname"; stub_uname "$d" "${2:-Darwin}"
}

# run DIR: the check on that PATH only, from a fresh cwd, home and TMPDIR.
# Output to $root/out, exit code to $root/rc. The three scratch dirs are left for the caller to inspect.
run() {
  rm -rf "$root/home" "$root/cwd" "$root/tmp"; mkdir -p "$root/home" "$root/cwd" "$root/tmp"
  set +e
  (cd "$root/cwd" && env -i HOME="$root/home" PATH="$1" LANG=C TMPDIR="$root/tmp" /bin/bash "$PF" >"$root/out" 2>&1)
  echo $? >"$root/rc"
  set -e
}
rc() { cat "$root/rc"; }
line() { grep -E "^$1 +$2( |\$)" "$root/out" | head -n 1 || true; }

[[ -x "$PF" ]] && ok "0  the preflight script exists and is executable" || bad "0  the preflight script exists and is executable" "$PF"

# 0b: the manifest declares every tool with the right class
if [[ -r "$MANIFEST" ]]; then
  for t in $REQUIRED; do
    awk -F'\t' -v t="$t" '$1 == t && $2 == "required" { f = 1 } END { exit !f }' "$MANIFEST" && ok "0b the manifest declares $t as required" || bad "0b the manifest declares $t as required"
  done
  for t in $OPTIONAL; do
    awk -F'\t' -v t="$t" '$1 == t && $2 == "optional" { f = 1 } END { exit !f }' "$MANIFEST" && ok "0b the manifest declares $t as optional" || bad "0b the manifest declares $t as optional"
  done
else
  bad "0b the manifest exists" "$MANIFEST"
fi

# 1: every tool present: each is reported OK with its version, exit 0
p="$root/p1"; new_path "$p"; run "$p"
[[ "$(rc)" == 0 ]] && ok "1  all tools present exits 0" || bad "1  all tools present exits 0" "rc $(rc): $(cat "$root/out")"
for t in $REQUIRED $OPTIONAL; do
  grep -qE "^OK +$t( |\$)" "$root/out" && ok "1  $t is reported OK" || bad "1  $t is reported OK" "$(cat "$root/out")"
done
grep -q '^OK ' "$root/out" && ! grep -qE '^(MISSING|OLD|UNREADABLE)' "$root/out" && ok "1  and nothing is named missing, old or unreadable" || bad "1  nothing named" "$(cat "$root/out")"
grep -qE '^OK +python3 +3\.12\.1' "$root/out" && grep -qE '^OK +nats-server +2\.11\.4' "$root/out" && ok "1  the version found is printed" || bad "1  version printed" "$(cat "$root/out")"

# 2: each required tool removed in turn: named MISSING with the macOS hint, exit 1
for t in $REQUIRED; do
  p="$root/p2"; new_path "$p"; rm -f "$p/$t"; run "$p"
  l="$(line MISSING "$t")"
  [[ -n "$l" ]] && ok "2  a PATH without $t names $t as MISSING" || bad "2  a PATH without $t names it" "$(cat "$root/out")"
  [[ "$(rc)" == 1 ]] && ok "2  and exits 1 when required $t is missing" || bad "2  $t exit" "rc $(rc)"
  grep -q "(required)" <<<"$l" && grep -q "install: .*brew" <<<"$l" && ok "2  and says required, with the macOS install hint for $t" || bad "2  $t hint" "$l"
done

# 3: each optional tool removed: named, marked optional, with a hint, exit 0
for t in $OPTIONAL; do
  p="$root/p3"; new_path "$p"; rm -f "$p/$t"; run "$p"
  l="$(line MISSING "$t")"
  grep -q "(optional)" <<<"$l" && ok "3  a PATH without optional $t names it, marked optional" || bad "3  $t named" "$(cat "$root/out")"
  grep -qE "install: .+" <<<"$l" && ok "3  and gives an install hint for $t" || bad "3  $t hint" "$l"
  [[ "$(rc)" == 0 ]] && ok "3  and exits 0 when only optional $t is missing" || bad "3  $t exit" "rc $(rc)"
done

# 3b: the hint follows the OS: Linux gets the apt or dnf hint and no brew
p="$root/p3b"; new_path "$p" Linux; rm -f "$p/jq" "$p/tmux"; run "$p"
l="$(line MISSING jq)"
grep -qE "install: .*(^|[^[:alpha:]])(apt|dnf)([^[:alpha:]]|\$)" <<<"$l" && ! grep -q "brew" <<<"$l" && ok "3b on Linux a missing jq gets the apt or dnf hint and no brew" || bad "3b linux jq hint" "$l"
l="$(line MISSING tmux)"
grep -qE "install: .*(^|[^[:alpha:]])(apt|dnf)([^[:alpha:]]|\$)" <<<"$l" && ! grep -q "brew" <<<"$l" && ok "3b on Linux a missing tmux gets the apt or dnf hint and no brew" || bad "3b linux tmux hint" "$l"
p="$root/p3c"; new_path "$p" Darwin; rm -f "$p/jq"; run "$p"
l="$(line MISSING jq)"
grep -q "brew" <<<"$l" && ! grep -qE "(^|[^[:alpha:]])(apt|dnf)([^[:alpha:]]|\$)" <<<"$l" && ok "3b on macOS a missing jq gets the brew hint and no apt or dnf" || bad "3b mac jq hint" "$l"

# 4: several missing: every one is named in one run, and the required ones make it exit 1
p="$root/p4"; new_path "$p"; rm -f "$p/jq" "$p/git" "$p/tmux"; run "$p"
n=0; for t in jq git tmux; do [[ -n "$(line MISSING "$t")" ]] && n=$((n+1)); done
[[ "$n" == 3 ]] && ok "4  three missing tools are all named in one run" || bad "4  three missing" "named $n: $(cat "$root/out")"
[[ "$(rc)" == 1 ]] && ok "4  and the run exits 1 because jq and git are required" || bad "4  exit" "rc $(rc)"
grep -qE '^OK +python3' "$root/out" && ok "4  and the tools that are present are still reported" || bad "4  present reported" "$(cat "$root/out")"

# 5: too old: named OLD with the version found and the minimum; a required one exits 1, an optional one does not
p="$root/p5"; new_path "$p"; stub "$p" python3 "Python 3.9.6"; run "$p"
grep -qE "^OLD +python3 .*3\.9\.6.*3\.11" "$root/out" && ok "5  python3 3.9.6 is named OLD, with the found and the wanted version" || bad "5  old python3" "$(cat "$root/out")"
[[ "$(rc)" == 1 ]] && ok "5  and a required tool too old exits 1" || bad "5  old exit" "rc $(rc)"
p="$root/p5b"; new_path "$p"; stub "$p" nats-server "nats-server: v2.10.0"; run "$p"
grep -qE "^OLD +nats-server .*2\.10\.0.*2\.11" "$root/out" && ok "5  nats-server 2.10.0 is named OLD" || bad "5  old nats-server" "$(cat "$root/out")"
[[ "$(rc)" == 1 ]] && ok "5  and nats-server too old exits 1" || bad "5  old nats-server exit" "rc $(rc)"
p="$root/p5c"; new_path "$p"; stub "$p" go "go version go1.22.1 linux/amd64"; run "$p"
grep -qE "^OLD +go .*1\.22\.1.*1\.26" "$root/out" && ok "5  an optional go that is too old is named OLD" || bad "5  old go" "$(cat "$root/out")"
[[ "$(rc)" == 0 ]] && ok "5  and does not fail the run" || bad "5  old go exit" "rc $(rc)"
p="$root/p5d"; new_path "$p"; stub "$p" python3 "Python 3.10.14"; run "$p"
grep -qE "^OLD +python3 .*3\.10\.14" "$root/out" && ok "5  python3 3.10.14 is older than 3.11 by minor version, not by string order" || bad "5  3.10 vs 3.11" "$(cat "$root/out")"

# 6: the exact minimum passes, for each tool that has one
p="$root/p6"; new_path "$p"; stub "$p" python3 "Python 3.11.0"; stub "$p" nats-server "nats-server: v2.11.0"; stub "$p" go "go version go1.26.0 linux/amd64"; run "$p"
[[ "$(rc)" == 0 ]] && ok "6  the exact minimums exit 0" || bad "6  exact minimum exit" "rc $(rc): $(cat "$root/out")"
grep -qE '^OK +python3 +3\.11\.0' "$root/out" && ok "6  python3 3.11.0 is OK" || bad "6  python3 minimum" "$(cat "$root/out")"
grep -qE '^OK +nats-server +2\.11\.0' "$root/out" && ok "6  nats-server 2.11.0 is OK" || bad "6  nats-server minimum" "$(cat "$root/out")"
grep -qE '^OK +go +1\.26\.0' "$root/out" && ok "6  go 1.26.0 is OK" || bad "6  go minimum" "$(cat "$root/out")"
grep -q '^OK ' "$root/out" && ! grep -qE '^OLD' "$root/out" && ok "6  and none is named OLD" || bad "6  none old" "$(cat "$root/out")"

# 7: a tool that is present but whose --version cannot be read
p="$root/p7"; new_path "$p"; stub "$p" python3 "no version here"; run "$p"
l="$(line UNREADABLE python3)"
grep -qE "wanted >= 3\.11" <<<"$l" && ok "7  python3 with an unparseable version is UNREADABLE against its minimum" || bad "7  python3 unreadable" "$(cat "$root/out")"
[[ "$(rc)" == 1 ]] && ok "7  and a required tool that cannot show it meets its minimum exits 1" || bad "7  unreadable exit" "rc $(rc)"
p="$root/p7b"; new_path "$p"; stub "$p" go "no version here"; run "$p"
grep -qE "^UNREADABLE +go " "$root/out" && [[ "$(rc)" == 0 ]] && ok "7  an optional go with an unparseable version is named and does not fail the run" || bad "7  go unreadable" "rc $(rc): $(cat "$root/out")"
p="$root/p7c"; new_path "$p"; stub_broken "$p" jq; run "$p"
grep -qE "^OK +jq" "$root/out" && [[ "$(rc)" == 0 ]] && ok "7  jq, which has no minimum, is OK even when its --version fails" || bad "7  jq broken" "rc $(rc): $(cat "$root/out")"

# 7d: a tool that prints its version on stderr, as some do
p="$root/p7d"; new_path "$p"; printf '#!/bin/sh\necho "Python 3.12.1" >&2\n' >"$p/python3"; chmod +x "$p/python3"; run "$p"
grep -qE '^OK +python3 +3\.12\.1' "$root/out" && ok "7  a version printed on stderr is read" || bad "7  stderr version" "$(cat "$root/out")"

# 7e: a file that is not executable, or a directory, named like a tool is not that tool
p="$root/p7e"; new_path "$p"; rm -f "$p/jq" "$p/gh"; printf '#!/bin/sh\n' >"$p/jq"; chmod -x "$p/jq"; mkdir "$p/gh"; run "$p"
[[ -n "$(line MISSING jq)" ]] && ok "7  a non-executable file named jq is MISSING" || bad "7  non-exec jq" "$(cat "$root/out")"
[[ -n "$(line MISSING gh)" ]] && ok "7  a directory named gh is MISSING" || bad "7  dir gh" "$(cat "$root/out")"

# 9: the check reads the manifest: a copy of the check beside an edited manifest follows the edit
copy_with() { # copy_with AWK_PROGRAM: copy the check and the manifest into a fresh dir, edit the manifest with awk, echo the check's path
  local d="$root/copy"
  rm -rf "$d"; mkdir -p "$d"
  cp "$PF" "$d/director-preflight"; chmod +x "$d/director-preflight"
  if [[ -r "$MANIFEST" ]]; then awk -F'\t' -v OFS='\t' "$1" "$MANIFEST" >"$d/director-preflight.tsv"; else : >"$d/director-preflight.tsv"; fi
  echo "$d/director-preflight"
}
real_pf="$PF"
p="$root/p9"; new_path "$p"; rm -f "$p/tmux"
PF="$(copy_with '$1 == "tmux" { $2 = "required" } { print }')"; run "$p"
l="$(line MISSING tmux)"
grep -q "(required)" <<<"$l" && [[ "$(rc)" == 1 ]] && ok "9  tmux marked required in the manifest makes its absence exit 1" || bad "9  tmux required" "rc $(rc): $(cat "$root/out")"
p="$root/p9b"; new_path "$p"
PF="$(copy_with '$1 == "python3" { $3 = "3.13" } { print }')"; run "$p"
grep -qE "^OLD +python3 .*3\.12\.1.*3\.13" "$root/out" && [[ "$(rc)" == 1 ]] && ok "9  python3 minimum raised to 3.13 in the manifest names 3.12.1 OLD" || bad "9  python3 minimum" "rc $(rc): $(cat "$root/out")"
p="$root/p9c"; new_path "$p"; rm -f "$p/jq"
PF="$(copy_with '$1 != "jq" { print }')"; run "$p"
[[ -z "$(line MISSING jq)" && "$(rc)" == 0 ]] && grep -q '^OK ' "$root/out" && ok "9  a tool removed from the manifest is no longer checked" || bad "9  jq removed" "rc $(rc): $(cat "$root/out")"
PF="$real_pf"
# the minimums the other cases rely on are the manifest's own
for pair in python3:3.11 nats-server:2.11 go:1.26; do
  t="${pair%%:*}"; m="${pair##*:}"
  awk -F'\t' -v t="$t" -v m="$m" '$1 == t && $3 == m { f = 1 } END { exit !f }' "$MANIFEST" 2>/dev/null && ok "9  the manifest sets $t minimum $m" || bad "9  $t minimum $m"
done

# 8: the check writes nothing: not to the home, the cwd or the TMPDIR
p="$root/p8"; new_path "$p"; run "$p"
[[ "$(rc)" == 0 ]] && ok "8  the clean run exits 0" || bad "8  clean run" "rc $(rc)"
for d in home cwd tmp; do
  [[ "$(rc)" == 0 && -s "$root/out" && -z "$(ls -A "$root/$d")" ]] && ok "8  the check wrote nothing to the $d" || bad "8  wrote to $d" "$(ls -A "$root/$d")"
done

echo "$pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
