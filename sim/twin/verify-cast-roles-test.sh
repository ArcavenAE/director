#!/usr/bin/env bash
# Cases for verify-cast-roles.sh over the fixtures in fixtures/cast-roles: the
# resolving manifests pass, each unresolvable one fails and names its role, and a
# usage error exits 2. Reads files only. Exit nonzero on any miss.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
V="$here/verify-cast-roles.sh"
fx="$here/fixtures/cast-roles"
W=(--wardrobe "$fx/wardrobe-roles")
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

for f in resolves.yaml resolves.toml; do
  if out="$("$V" "${W[@]}" "$fx/$f" 2>&1)"; then ok "$f: every cast role resolves, exit 0"; else bad "$f" "$out"; fi
done
if out="$("$V" "${W[@]}" "$fx/resolves.yaml" 2>&1)" && grep -q '3 cast roles checked, 1 skipped' <<<"$out"; then
  ok "resolves.yaml: the non-cast role is skipped, not checked"; else bad "resolves.yaml counts" "$out"; fi
for pair in unresolved.yaml:nonesuch-builder unresolved.toml:nonesuch-worker; do
  f="${pair%%:*}"; r="${pair##*:}"
  rc=0; out="$("$V" "${W[@]}" "$fx/$f" 2>&1)" || rc=$?
  if [[ "$rc" == 1 ]] && grep -q "UNRESOLVED role '$r'" <<<"$out" && ! grep -q "UNRESOLVED role 'supervisor'" <<<"$out"; then
    ok "$f: exit 1 and only '$r' is named"; else bad "$f" "rc=$rc $out"; fi
done
rc=0; "$V" "${W[@]}" "$fx/resolves.yaml" "$fx/unresolved.yaml" >/dev/null 2>&1 || rc=$?
[[ "$rc" == 1 ]] && ok "one bad manifest among several fails the run" || bad "mixed run" "rc=$rc"
rc=0; "$V" "${W[@]}" >/dev/null 2>&1 || rc=$?
[[ "$rc" == 2 ]] && ok "no manifest is a usage error, exit 2" || bad "no manifest" "rc=$rc"
rc=0; "$V" --wardrobe "$fx/no-such-dir" "$fx/resolves.yaml" >/dev/null 2>&1 || rc=$?
[[ "$rc" == 2 ]] && ok "a missing wardrobe directory is exit 2, not a pass" || bad "missing wardrobe dir" "rc=$rc"
echo "cast-roles: $pass passed, $fail failed"
[[ "$fail" == 0 ]]
