#!/usr/bin/env bash
# Prove vector-pin-drift: a matching pin prints nothing, a differing pin prints
# one line naming both shas, an unreachable or empty marvel prints a GAP and
# never a pass, a missing pinned copy is a GAP, and nothing exits nonzero.
# Network-free: gh is a stub that serves canned bytes and misbehaves on request.
#
# Usage: skills/director/scripts/verify-vector-pin-drift.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VPD="${VPD:-$here/vector-pin-drift}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d "${TMPDIR:-/tmp}/vpd-test.XXXXXX")"
trap 'rm -rf "$root"' EXIT
mkdir -p "$root/pin" "$root/up" "$root/tmp"
printf '%s\n' '{"vectors":["marry"]}' >"$root/pin/marry-vectors.json"
printf '%s\n' '{"vectors":["routing"]}' >"$root/pin/routing-vectors.json"
cp "$root/pin/"*.json "$root/up/"
sha() { shasum -a 256 <"$1" | cut -d' ' -f1; }

cat >"$root/gh" <<'S'
#!/usr/bin/env bash
echo "$*" >>"$VPD_ROOT/gh.log"
[[ "$1" == api ]] || { echo "forbidden: $*" >>"$VPD_ROOT/forbidden.log"; exit 1; }
if [[ -e "$VPD_ROOT/gh.fail" ]]; then echo "gh: HTTP 502" >&2; exit 1; fi
if [[ -e "$VPD_ROOT/gh.slow" ]]; then sleep 30; fi
url="${!#}"; f="${url##*/readiness/}"; f="${f%%\?*}"
if [[ -e "$VPD_ROOT/gh.empty" ]]; then exit 0; fi
cat "$VPD_ROOT/up/$f"
S
chmod +x "$root/gh"

run() { rc=0; env -i HOME="$root" PATH="$PATH" TMPDIR="$root/tmp" VPD_ROOT="$root" VECTOR_PIN_DIR="$root/pin" VECTOR_PIN_GH="$root/gh" "$@" "$VPD" >"$root/out" 2>"$root/err" || rc=$?; }

# 1 a matching pin prints nothing
run
[[ "$rc" == 0 && ! -s "$root/out" ]] && ok "1 matching pins print nothing, exit 0" || bad "1 match" "rc=$rc $(cat "$root/out")"
[[ "$(wc -l <"$root/gh.log" | tr -d ' ')" == 2 ]] && ok "1 marvel main was read once per file" || bad "1 reads" "$(cat "$root/gh.log")"
[[ ! -e "$root/forbidden.log" ]] && ok "1 only gh api was called" || bad "1 forbidden" "$(cat "$root/forbidden.log")"

# 2 a differing pin prints one line naming both shas
printf '%s\n' '{"vectors":["marry","new"]}' >"$root/up/marry-vectors.json"
run
want="vector-pin-drift: marry-vectors.json differs: pinned $(sha "$root/pin/marry-vectors.json"), marvel main $(sha "$root/up/marry-vectors.json")"
[[ "$rc" == 0 && "$(cat "$root/out")" == "$want" ]] && ok "2 one differing file prints one line naming both shas, exit 0" || bad "2 differ" "rc=$rc $(cat "$root/out")"
cp "$root/pin/marry-vectors.json" "$root/up/marry-vectors.json"

# 3 an unreachable marvel is a GAP for each file, never a pass
touch "$root/gh.fail"; run; rm -f "$root/gh.fail"
[[ "$rc" == 0 && "$(grep -c '^GAP vector-pin-drift: .* marvel main was read\|^GAP vector-pin-drift: .*was not read' "$root/out")" == 2 && "$(cat "$root/out")" == *"HTTP 502"* ]] \
  && ok "3 a failing gh is one GAP per file, with the error, exit 0" || bad "3 unreachable" "rc=$rc $(cat "$root/out")"

# 4 an empty answer is a GAP
touch "$root/gh.empty"; run; rm -f "$root/gh.empty"
[[ "$rc" == 0 && "$(grep -c '^GAP .*answered with nothing' "$root/out")" == 2 ]] && ok "4 an empty answer is a GAP, not a match" || bad "4 empty" "rc=$rc $(cat "$root/out")"

# 5 a hung gh is stopped at the bound
echo 30 >"$root/gh.slow"; start=$(date +%s); run VECTOR_PIN_TIMEOUT=1; el=$(( $(date +%s) - start )); rm -f "$root/gh.slow"
[[ "$rc" == 0 && "$el" -lt 15 && "$(grep -c '^GAP' "$root/out")" == 2 ]] && ok "5 a hung gh is stopped at the bound (${el}s) and is a GAP" || bad "5 hang" "rc=$rc ${el}s $(cat "$root/out")"

# 6 a missing pinned copy is a GAP, and the other file is still compared
mv "$root/pin/routing-vectors.json" "$root/pin/routing-vectors.json.off"; run; mv "$root/pin/routing-vectors.json.off" "$root/pin/routing-vectors.json"
[[ "$rc" == 0 && "$(cat "$root/out")" == "GAP vector-pin-drift: routing-vectors.json: the pinned copy is not readable at $root/pin" ]] && ok "6 a missing pinned copy is a GAP, exit 0" || bad "6 missing pin" "rc=$rc $(cat "$root/out")"

# 7 a gh that does not exist is a GAP (exit 127 from exec), not a match
run VECTOR_PIN_GH="$root/no-such-gh"
[[ "$rc" == 0 && "$(grep -c '^GAP' "$root/out")" == 2 && "$(cat "$root/out")" == *"exec"* ]] && ok "7 a missing gh is a GAP, not a pass" || bad "7 no gh" "rc=$rc $(cat "$root/out")"

# 8 the script writes nothing and never exits nonzero on a difference
printf '%s\n' '{"x":1}' >"$root/up/marry-vectors.json"; printf '%s\n' '{"y":2}' >"$root/up/routing-vectors.json"
run
[[ "$rc" == 0 && "$(wc -l <"$root/out" | tr -d ' ')" == 2 ]] && ok "8 two differing files print two lines and still exit 0" || bad "8 both differ" "rc=$rc $(cat "$root/out")"

# 9 an unwritable or missing TMPDIR is a GAP and exit 0, never a silent exit 1
run TMPDIR="$root/no-such-dir"
[[ "$rc" == 0 && "$(cat "$root/out")" == "GAP vector-pin-drift: no scratch directory could be made under $root/no-such-dir, so no pin was compared" ]] \
  && ok "9 a missing TMPDIR prints a GAP and exits 0" || bad "9 tmpdir" "rc=$rc out=$(cat "$root/out") err=$(head -c 200 "$root/err")"
mkdir -p "$root/ro"; chmod 500 "$root/ro"; run TMPDIR="$root/ro"; chmod 700 "$root/ro"
[[ "$rc" == 0 && "$(head -n 1 "$root/out")" == "GAP vector-pin-drift: no scratch directory could be made under $root/ro, so no pin was compared" ]] \
  && ok "9 an unwritable TMPDIR prints a GAP and exits 0" || bad "9 tmpdir ro" "rc=$rc out=$(cat "$root/out")"

echo "pass $pass fail $fail"
[[ "$fail" == 0 ]] || exit 1
