#!/usr/bin/env bash
# Prove director-mcp-seat treats a session's bus credentials as present when it
# brings DIRECTOR_NATS_USER with either DIRECTOR_NATS_PASS or
# DIRECTOR_NATS_PASS_FILE, and falls back to the seat password file only when it
# brings neither:
#   A. USER + PASS: passed through, the seat file is not read;
#   B. USER + PASS_FILE: passed through, nothing is exported, the file is not read;
#   C. neither, with a seat file: user director and the seat password;
#   D. neither, no seat file: exit 1 with the "no bus credentials" message.
#
# The launcher runs under env -i in a scratch home, with a stub in place of
# director-mcp. The stub prints which variables it saw by NAME only, and whether
# the password equals the seat fixture, never a value. All passwords are
# fixtures. No broker, no marvel, no real home.
#
# Usage: probe/nats-phase-0/verify-director-mcp-seat.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
seat="$here/director-mcp-seat"
[[ -f "$seat" ]] || { echo "no launcher at $seat"; exit 2; }

pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

work="$(mktemp -d "${TMPDIR:-/tmp}/seat-verify.XXXXXX")"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/home" "$work/state"
SEAT_FIXTURE="seat-fixture-pw"
FILE_FIXTURE="file-fixture-pw"

# The stub: names of the credential variables it sees, and whether the password
# equals the seat fixture. It prints no value.
cat > "$work/stub" <<'STUB'
#!/usr/bin/env bash
seen=""
for v in DIRECTOR_NATS_USER DIRECTOR_NATS_PASS DIRECTOR_NATS_PASS_FILE; do
  [[ -n "${!v:-}" ]] && seen="$seen $v"
done
echo "SAW:$seen"
[[ "${DIRECTOR_NATS_USER:-}" == director ]] && echo "USER_IS_DIRECTOR"
[[ "${DIRECTOR_NATS_PASS:-}" == "seat-fixture-pw" ]] && echo "PASS_IS_SEAT_FIXTURE"
exit 0
STUB
chmod +x "$work/stub"
printf '%s\n' "$FILE_FIXTURE" > "$work/pass.file"; chmod 600 "$work/pass.file"

# run NAME=VALUE...: the launcher under env -i with only the given variables.
out=""; rc=0
run() {
  rc=0
  out="$(env -i HOME="$work/home" PATH="$PATH" TMPDIR="$work" \
    MARVEL_BUS_STATE="$work/state" DIRECTOR_MCP_BIN="$work/stub" \
    DIRECTOR_AGENT_ID=verify DIRECTOR_TEAM=ops DIRECTOR_WORKSPACE=w \
    "$@" bash "$seat" 2>&1)" || rc=$?
}
has()  { grep -qF -- "$1" <<<"$out"; }
saw()  { grep -qxF -- "SAW: $1" <<<"$out"; }   # the exact line of names the stub saw

# A. USER + PASS
rm -f "$work/state/director.pass"
run DIRECTOR_NATS_USER=ops DIRECTOR_NATS_PASS=env-fixture-pw
if [[ $rc -eq 0 ]] && saw "DIRECTOR_NATS_USER DIRECTOR_NATS_PASS" && ! has USER_IS_DIRECTOR; then
  ok "A. USER + PASS passes through"; else bad "A. USER + PASS" "rc=$rc out=$out"; fi

# B. USER + PASS_FILE, no seat file: must pass through, export no PASS, print no value
run DIRECTOR_NATS_USER=ops DIRECTOR_NATS_PASS_FILE="$work/pass.file"
if [[ $rc -eq 0 ]] && saw "DIRECTOR_NATS_USER DIRECTOR_NATS_PASS_FILE" \
   && ! has USER_IS_DIRECTOR && ! has "$FILE_FIXTURE"; then
  ok "B. USER + PASS_FILE passes through, no PASS exported, file value not printed"
else bad "B. USER + PASS_FILE" "rc=$rc out=$out"; fi

# B2. the same with a seat file present: the session's file wins, the seat is not taken
printf '%s\n' "$SEAT_FIXTURE" > "$work/state/director.pass"
run DIRECTOR_NATS_USER=ops DIRECTOR_NATS_PASS_FILE="$work/pass.file"
if [[ $rc -eq 0 ]] && saw "DIRECTOR_NATS_USER DIRECTOR_NATS_PASS_FILE" && ! has USER_IS_DIRECTOR && ! has PASS_IS_SEAT_FIXTURE; then
  ok "B2. USER + PASS_FILE ignores a seat file"; else bad "B2. USER + PASS_FILE with a seat file" "rc=$rc out=$out"; fi

# C. neither, with a seat file
run
if [[ $rc -eq 0 ]] && has USER_IS_DIRECTOR && has PASS_IS_SEAT_FIXTURE; then
  ok "C. neither, with a seat file: user director and the seat password"; else bad "C. seat fallback" "rc=$rc out=$out"; fi

# D. neither, no seat file
rm -f "$work/state/director.pass"
run
if [[ $rc -eq 1 ]] && has "director-mcp-seat: no bus credentials in the environment, and no seat" && has "$work/state/director.pass"; then
  ok "D. neither, no seat file: exit 1 with the same message"; else bad "D. no credentials" "rc=$rc out=$out"; fi

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
