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
# A pattern row (ab852-*) resolves the names it matches and no near miss of them.
if out="$("$V" "${W[@]}" "$fx/resolves-pattern.yaml" 2>&1)" && grep -q '2 cast roles checked, 0 skipped' <<<"$out"; then
  ok "resolves-pattern.yaml: a pattern row of the case table resolves the names it matches"; else bad "resolves-pattern.yaml" "$out"; fi
rc=0; out="$("$V" "${W[@]}" "$fx/unresolved-pattern.yaml" 2>&1)" || rc=$?
if [[ "$rc" == 1 ]] && grep -q "UNRESOLVED role 'ab852'" <<<"$out" && grep -q "UNRESOLVED role 'xab852-p1a'" <<<"$out" \
   && grep -q '2 unresolved' <<<"$out"; then
  ok "unresolved-pattern.yaml: near misses of a pattern row exit 1 and are named"; else bad "unresolved-pattern.yaml" "rc=$rc $out"; fi
rc=0; out="$("$V" --wardrobe "$fx/wardrobe-no-builder" "$fx/resolves-pattern.yaml" 2>&1)" || rc=$?
if [[ "$rc" == 1 ]] && grep -q "UNRESOLVED role 'ab852-p1a'.*maps it to 'builder'" <<<"$out"; then
  ok "a pattern row whose WROLE target is not in the wardrobe exits 1"; else bad "pattern row missing WROLE target" "rc=$rc $out"; fi
# A table-only role under a wrapper that is not cast-launch is not credited the table.
rc=0; out="$("$V" "${W[@]}" "$fx/unresolved-wrapper.yaml" 2>&1)" || rc=$?
if [[ "$rc" == 1 ]] && grep -q "UNRESOLVED role 'scoped-builder'.*not cast-launch" <<<"$out" && ! grep -q "UNRESOLVED role 'supervisor'" <<<"$out"; then
  ok "unresolved-wrapper.yaml: a table-only role under a wrapper exits 1 and only it is named"; else bad "unresolved-wrapper.yaml" "rc=$rc $out"; fi
# --table credits a wrapper's own case table to its basename, and only to it.
if out="$("$V" "${W[@]}" --table cast-fixture.sh="$fx/wrapper-table.sh" "$fx/unresolved-wrapper.yaml" 2>&1)"; then
  ok "--table: the wrapper's own case table resolves the table-only role"; else bad "--table credit" "$out"; fi
rc=0; "$V" "${W[@]}" --table cast-other.sh="$fx/wrapper-table.sh" "$fx/unresolved-wrapper.yaml" >/dev/null 2>&1 || rc=$?
[[ "$rc" == 1 ]] && ok "--table for another wrapper name credits nothing here" || bad "--table other name" "rc=$rc"
rc=0; "$V" "${W[@]}" --table "cast-fixture.sh=$fx/no-such-file" "$fx/resolves.yaml" >/dev/null 2>&1 || rc=$?
[[ "$rc" == 2 ]] && ok "--table with a missing file is exit 2" || bad "--table missing file" "rc=$rc"
# A table row whose wardrobe target is missing is unresolved.
rc=0; out="$("$V" --wardrobe "$fx/wardrobe-no-builder" "$fx/resolves.yaml" 2>&1)" || rc=$?
if [[ "$rc" == 1 ]] && grep -q "UNRESOLVED role 'builder'.*maps it to 'builder'" <<<"$out"; then
  ok "a table row whose WROLE target is not in the wardrobe exits 1"; else bad "missing WROLE target" "rc=$rc $out"; fi
rc=0; "$V" "${W[@]}" "$fx/resolves.yaml" "$fx/unresolved.yaml" >/dev/null 2>&1 || rc=$?
[[ "$rc" == 1 ]] && ok "one bad manifest among several fails the run" || bad "mixed run" "rc=$rc"
rc=0; "$V" "${W[@]}" >/dev/null 2>&1 || rc=$?
[[ "$rc" == 2 ]] && ok "no manifest is a usage error, exit 2" || bad "no manifest" "rc=$rc"
rc=0; "$V" --wardrobe "$fx/no-such-dir" "$fx/resolves.yaml" >/dev/null 2>&1 || rc=$?
[[ "$rc" == 2 ]] && ok "a missing wardrobe directory is exit 2, not a pass" || bad "missing wardrobe dir" "rc=$rc"
echo "cast-roles: $pass passed, $fail failed"
[[ "$fail" == 0 ]]
