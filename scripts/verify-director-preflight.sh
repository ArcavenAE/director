#!/usr/bin/env bash
# Prove scripts/director-preflight (I-473 gap 3): a check that names each tool a
# director install needs and does not find, so a new user learns what is missing
# from one run and not from a script that dies halfway.
#
# Each case builds a scratch PATH that holds symlinks to the core utilities the
# check itself uses plus stub tools that answer --version, then removes or ages
# one tool. Nothing outside the scratch directory is read or written, and no
# network is used.
#
# Usage: scripts/verify-director-preflight.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PF="${PF:-$here/director-preflight}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d "${TMPDIR:-/tmp}/pf.XXXXXX")"
root="$(cd "$root" && pwd -P)"
trap 'rm -rf "$root"' EXIT

# the tools the check reads from the declared manifest: required, then optional
REQUIRED="python3 jq git nats-server nats"
OPTIONAL="gh claude marvel tmux lsof go"

core="bash sh sort sed awk head tr cat uname grep cut printf dirname basename mkdir rm env ls wc tail"

new_path() { # new_path DIR: a PATH dir with the core utilities and every stub tool
  local d="$1" c t q
  rm -rf "$d"; mkdir -p "$d"
  for c in $core; do
    for q in /usr/bin /bin; do if [[ -x "$q/$c" ]]; then ln -s "$q/$c" "$d/$c"; break; fi; done
  done
  for t in $REQUIRED $OPTIONAL; do stub "$d" "$t" ""; done
  stub "$d" python3 "Python 3.12.1"
  stub "$d" nats-server "nats-server: v2.11.4"
  stub "$d" go "go version go1.26.5 darwin/arm64"
}
stub() { # stub DIR NAME [VERSION TEXT]
  local ver="${3:-$2 1.0.0}"
  printf '#!/bin/sh\necho "%s"\n' "$ver" >"$1/$2"; chmod +x "$1/$2"
}

run() { # run DIR: the check on that PATH only; output to $root/out, exit code to $root/rc
  set +e
  env -i HOME="$root/home" PATH="$1" LANG=C TMPDIR="$root" /bin/bash "$PF" >"$root/out" 2>&1
  echo $? >"$root/rc"
  set -e
}
rc() { cat "$root/rc"; }

mkdir -p "$root/home"

[[ -x "$PF" ]] && ok "0  the preflight script exists and is executable" || bad "0  the preflight script exists and is executable" "$PF"

# 1: every tool present: reports ok and exits 0
p="$root/p1"; new_path "$p"; run "$p"
[[ "$(rc)" == 0 ]] && ok "1  all tools present exits 0" || bad "1  all tools present exits 0" "rc $(rc): $(cat "$root/out")"
grep -q '^OK ' "$root/out" && ! grep -qiE '^(MISSING|OLD)' "$root/out" && ok "1  and reports each tool found, naming nothing missing" || bad "1  reports found, names nothing missing" "$(cat "$root/out")"

# 2: each required tool removed in turn: named, with its hints, exit nonzero
for t in $REQUIRED; do
  p="$root/p2"; new_path "$p"; rm -f "$p/$t"; run "$p"
  grep -qE "^MISSING +$t( |$)" "$root/out" && ok "2  a PATH without $t names $t as missing" || bad "2  a PATH without $t names it" "$(cat "$root/out")"
  [[ "$(rc)" == 1 ]] && ok "2  and exits 1 when required $t is missing" || bad "2  $t exit" "rc $(rc)"
  grep -qE "^MISSING +$t .*(brew|apt|dnf)" "$root/out" && ok "2  and gives an install hint for $t" || bad "2  $t hint" "$(cat "$root/out")"
done

# 3: each optional tool removed: named as optional, exit 0
for t in $OPTIONAL; do
  p="$root/p3"; new_path "$p"; rm -f "$p/$t"; run "$p"
  grep -qE "^MISSING +$t .*optional" "$root/out" && ok "3  a PATH without optional $t names it, marked optional" || bad "3  $t named" "$(cat "$root/out")"
  [[ "$(rc)" == 0 ]] && ok "3  and exits 0 when only optional $t is missing" || bad "3  $t exit" "rc $(rc)"
done

# 4: several missing: every one is named in one run
p="$root/p4"; new_path "$p"; rm -f "$p/jq" "$p/git" "$p/tmux"; run "$p"
n=0; for t in jq git tmux; do grep -qE "^MISSING +$t( |$)" "$root/out" && n=$((n+1)); done
[[ "$n" == 3 ]] && ok "4  three missing tools are all named in one run" || bad "4  three missing" "named $n: $(cat "$root/out")"

# 5: too old: named with the version found and the minimum
p="$root/p5"; new_path "$p"; stub "$p" python3 "Python 3.9.6"; run "$p"
grep -qE "^OLD +python3 .*3\.9\.6.*3\.11" "$root/out" && ok "5  python3 3.9.6 is named too old, with the found and the wanted version" || bad "5  old python3" "$(cat "$root/out")"
[[ "$(rc)" == 1 ]] && ok "5  and a required tool too old exits 1" || bad "5  old exit" "rc $(rc)"
p="$root/p5b"; new_path "$p"; stub "$p" nats-server "nats-server: v2.10.0"; run "$p"
grep -qE "^OLD +nats-server .*2\.10\.0.*2\.11" "$root/out" && ok "5  nats-server 2.10.0 is named too old" || bad "5  old nats-server" "$(cat "$root/out")"
p="$root/p5c"; new_path "$p"; stub "$p" go "go version go1.22.1 linux/amd64"; run "$p"
grep -qE "^OLD +go .*1\.22\.1.*1\.26" "$root/out" && [[ "$(rc)" == 0 ]] && ok "5  an optional go that is too old is named and does not fail the run" || bad "5  old go" "rc $(rc): $(cat "$root/out")"

# 6: an exact-minimum version passes
p="$root/p6"; new_path "$p"; stub "$p" python3 "Python 3.11.0"; run "$p"
[[ "$(rc)" == 0 ]] && ! grep -q '^OLD' "$root/out" && ok "6  python3 3.11.0 meets the minimum" || bad "6  exact minimum" "rc $(rc): $(cat "$root/out")"

# 7: the check reads and writes nothing outside its own output
p="$root/p7"; new_path "$p"; before="$(ls -A "$root/home" | wc -l | tr -d ' ')"; run "$p"
[[ "$(rc)" == 0 && "$(ls -A "$root/home" | wc -l | tr -d ' ')" == "$before" ]] && ok "7  the check writes nothing to the home" || bad "7  wrote to home"

echo "$pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
