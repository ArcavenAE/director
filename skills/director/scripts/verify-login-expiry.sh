#!/usr/bin/env bash
# Prove `login-expiry` (sim/design/login-expiry.md): the daily warning before a claude login lapses.
# It runs a stub `claude` that prints a fixture carrying a fake email and a fake organization, and a stub `marvel`
# that prints a sessions document. Checks:
#   - above 72 h: no output, one state line;
#   - at or under 72 h, and when loggedIn is false: a desk line and an introspect line;
#   - the fake email and organization appear in no output and in no file the run wrote;
#   - a later expiry than the stored one lists the logged-out seats, one line per seat; a first run lists none;
#   - sessions that cannot be read print a GAP and keep the stored expiry;
#   - the HOME path never appears in a line.
# Every run is under `env -i` with a scratch HOME. Fixture values are placeholders. Nothing is deleted: the scratch
# tree lives under $LOGIN_EXPIRY_TMP, else $TMPDIR, else /tmp. Written for bash 3.2 and bash 5.
#
# Usage: skills/director/scripts/verify-login-expiry.sh   Exit nonzero on any miss.
set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
L="${LOGIN_EXPIRY:-$here/login-expiry}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }
[[ -f "$L" ]] || { echo "FAIL login-expiry exists at $L"; echo "0 passed, 1 failed"; exit 1; }

root="$(mktemp -d "${LOGIN_EXPIRY_TMP:-${TMPDIR:-/tmp}}/lex.XXXXXX")" || { echo "FAIL scratch root"; exit 1; }
root="$(cd "$root" && pwd -P)"
mkdir -p "$root/home" "$root/bin" "$root/state"
PYDIR="$(dirname "$(command -v python3)")"
EMAIL="placeholder.person@example.invalid"
ORG="Placeholder Org Name"
NOW="2030-01-10T12:00:00Z"

# stub claude: prints $STUB_AUTH_FILE's content, exit 0
printf '#!/bin/sh\n[ "$1 $2 $3" = "auth status --json" ] || exit 9\ncat "$STUB_AUTH_FILE"\n' >"$root/bin/claude"
# stub marvel: prints $STUB_SESSIONS_FILE, or fails when it is absent
printf '#!/bin/sh\n[ "$1 $2 $3 $4 $5" = "get sessions --all-clusters -o json" ] || exit 9\n[ -f "$STUB_SESSIONS_FILE" ] || { echo "Error: refused" >&2; exit 1; }\ncat "$STUB_SESSIONS_FILE"\nexit ${STUB_SESSIONS_RC:-0}\n' >"$root/bin/marvel"
chmod +x "$root/bin/claude" "$root/bin/marvel"

auth() { # auth <expiresAt> <loggedIn true|false> [subscription]
  printf '{"loggedIn": %s, "authMethod": "claude.ai", "email": "%s", "orgName": "%s", "subscriptionType": "%s", "loginExpiresAt": "%s"}\n' \
    "$2" "$EMAIL" "$ORG" "${3:-max}" "$1" >"$root/auth.json"
}
sessions() {
  cat >"$root/sessions.json" <<'JSON'
{"clusters":[{"cluster":"alpha","outcome":"answered","as_of":"2030-01-10T12:00:00Z"}],
 "sessions":[
  {"cluster":"alpha","Name":"seat-b-g1-0","State":"running","harness_state":{"state":"logged-out","confidence":"high"}},
  {"cluster":"alpha","Name":"seat-a-g1-0","State":"running","harness_state":{"state":"logged-out","confidence":"high"}},
  {"cluster":"alpha","Name":"seat-c-g1-0","State":"running","harness_state":{"state":"ready"}},
  {"cluster":"alpha","Name":"seat-d-g1-0","State":"failed","harness_state":{"state":"logged-out"}},
  {"cluster":"alpha","Name":"seat-e-g1-0","State":"running"}]}
JSON
}
run() { # run [args...]
  env -i HOME="$root/home" PATH="$root/bin:$PYDIR:/usr/bin:/bin" STUB_AUTH_FILE="$root/auth.json" STUB_SESSIONS_FILE="$root/sessions.json" STUB_SESSIONS_RC="${SESSIONS_RC:-0}" \
    python3 -I "$L" --host hostx --home-label lab --state-dir "$root/state" --now "$NOW" "$@" >"$root/out" 2>"$root/err"
  rc=$?
}
fresh() { : >"$root/state/state.log"; printf '{}\n' >"$root/state/last-expiry.json"; hide_sessions; }
hide_sessions() { mv -f "$root/sessions.json" "$root/sessions.json.off" 2>/dev/null || true; }
nlines() { wc -l <"$root/out" | tr -d ' '; }

# --- the warning window ----------------------------------------------------------------------------------------
fresh; auth "2030-01-15T12:00:00Z" true      # 120 h
run
[[ $rc -eq 0 && ! -s "$root/out" ]] && ok "above 72 h prints nothing to the operator" || bad "above 72 h" "rc=$rc out=$(cat "$root/out")"
grep -qF 'hostx/lab expires 2030-01-15T12:00:00Z hours_left 120' "$root/state/state.log" && ok "above 72 h appends one state line" || bad "state line" "$(cat "$root/state/state.log")"

fresh; auth "2030-01-13T12:00:00Z" true pro   # exactly 72 h
run
[[ $rc -eq 0 && "$(nlines)" == 2 ]] && ok "at 72 h exactly: two lines" || bad "72 h" "rc=$rc out=$(cat "$root/out")"
grep -qF 'desk: login-expiry hostx lab expires 2030-01-13T12:00:00Z, 72 h left, subscription pro' "$root/out" && ok "the desk line names host, label, expiry, hours left and subscription" || bad "desk line" "$(cat "$root/out")"
grep -qF 'introspect: login-expiry hostx/lab expires 2030-01-13T12:00:00Z, 72 h left | id: 2030-01-13T12:00:00Z | remedy: renew the claude login | owner: operator' "$root/out" && ok "the introspect line carries id, remedy and owner" || bad "introspect line" "$(cat "$root/out")"

fresh; auth "2030-01-13T12:00:01Z" true
run; [[ ! -s "$root/out" ]] && ok "72 h and one second prints nothing" || bad "73 h" "$(cat "$root/out")"
fresh; auth "2030-01-10T10:00:00Z" true
run; grep -qF 'expired 2 h ago' "$root/out" && ok "an expiry in the past reads expired N h ago" || bad "past" "$(cat "$root/out")"
fresh; auth "2030-01-10T11:30:00Z" true
run; grep -qF 'expired under 1 h ago' "$root/out" && ok "a login that lapsed 30 minutes ago reads under 1 h, not 1 h" || bad "under 1 h" "$(cat "$root/out")"
fresh; auth "2030-01-20T12:00:00Z" false
run; [[ "$(nlines)" == 2 ]] && grep -qF 'logged out' "$root/out" && ok "loggedIn false prints the two lines whatever the expiry" || bad "logged out" "$(cat "$root/out")"
fresh; auth "2030-01-20T12:00:00.000+00:00" true
run; [[ ! -s "$root/out" ]] && ok "an expiry with a fraction and an offset parses" || bad "offset" "$(cat "$root/out")"

# --- the two other keys are never printed -----------------------------------------------------------------------
fresh; auth "2030-01-11T12:00:00Z" true
run
leak=0
for s in "$EMAIL" "$ORG"; do
  grep -rqF "$s" "$root/out" "$root/err" "$root/state" && leak=1
done
[[ $leak -eq 0 ]] && ok "the fake email and organization appear in no output and in no state file" || bad "a fixture string leaked"
[[ "$(nlines)" == 2 ]] && ok "the warning run still printed its two lines" || bad "warn lines" "$(cat "$root/out")"
! grep -qF "$root/home" "$root/out" && ok "the HOME path never appears in a line" || bad "home path in a line"

# --- an unreadable answer ----------------------------------------------------------------------------------------
fresh; printf 'not json at all %s\n' "$EMAIL" >"$root/auth.json"
run
grep -qF 'UNKNOWN (claude auth status gave no readable answer)' "$root/out" && ok "an unreadable answer prints UNKNOWN, never silence" || bad "unreadable" "$(cat "$root/out")"
! grep -rqF "$EMAIL" "$root/out" "$root/err" "$root/state" && ok "an unreadable answer is not echoed" || bad "unreadable answer echoed"

# --- renewal ------------------------------------------------------------------------------------------------------
fresh; auth "2030-01-20T12:00:00Z" true; sessions
run
[[ ! -s "$root/out" ]] && ok "a first run has nothing stored to compare and lists no seats" || bad "first run" "$(cat "$root/out")"
grep -qF '"hostx/lab": "2030-01-20T12:00:00Z"' "$root/state/last-expiry.json" && ok "the first run stores the expiry per host and label" || bad "stored" "$(cat "$root/state/last-expiry.json")"
auth "2030-01-20T12:00:00Z" true; run
[[ ! -s "$root/out" ]] && ok "the same expiry again lists nothing" || bad "same expiry" "$(cat "$root/out")"
auth "2030-02-20T12:00:00Z" true; run
[[ "$(nlines)" == 2 ]] && ok "a later expiry lists one line per running logged-out seat (two, not the failed one)" || bad "renewal count" "$(cat "$root/out")"
[[ "$(sed -n 1p "$root/out")" == "renewal: nudge alpha/seat-a-g1-0 (logged-out)" && "$(sed -n 2p "$root/out")" == "renewal: nudge alpha/seat-b-g1-0 (logged-out)" ]] && ok "the lines are sorted and name cluster and seat" || bad "renewal lines" "$(cat "$root/out")"
grep -qF '"hostx/lab": "2030-02-20T12:00:00Z"' "$root/state/last-expiry.json" && ok "the new expiry is stored after the listing" || bad "stored after" "$(cat "$root/state/last-expiry.json")"
auth "2030-03-20T12:00:00Z" true; hide_sessions; run
grep -qF 'renewal: GAP (marvel gave no readable answer: Error: refused)' "$root/out" && ok "unreadable sessions print a renewal GAP, not zero seats" || bad "gap" "$(cat "$root/out")"
grep -qF '"hostx/lab": "2030-02-20T12:00:00Z"' "$root/state/last-expiry.json" && ok "a GAP leaves the stored expiry so the next run lists again" || bad "kept" "$(cat "$root/state/last-expiry.json")"
auth "2030-04-20T12:00:00Z" true; sessions; cp "$root/sessions.json" "$root/sessions.base"
sed 's/"clusters":\[/"clusters":[{"cluster":"omega","outcome":"no_answer_in_time","as_of":"2030-01-10T12:00:00Z"},/' "$root/sessions.base" >"$root/sessions.json"
SESSIONS_RC=10 run
grep -qF 'renewal: nudge alpha/seat-a-g1-0 (logged-out)' "$root/out" && grep -qF 'renewal: GAP (cluster not read in full: omega no_answer_in_time)' "$root/out" && ok "a cluster that did not answer is a GAP naming it, after the seats that were seen" || bad "silent cluster" "$(cat "$root/out")"
grep -qF '"hostx/lab": "2030-02-20T12:00:00Z"' "$root/state/last-expiry.json" && ok "a silent cluster leaves the stored expiry so the next run lists again" || bad "silent cluster kept" "$(cat "$root/state/last-expiry.json")"
sed 's/"outcome":"answered","as_of":"2030-01-10T12:00:00Z"}\]/"outcome":"starting","as_of":"2030-01-10T12:00:00Z"}]/' "$root/sessions.base" >"$root/sessions.json"
SESSIONS_RC=10 run
grep -qF 'renewal: GAP (cluster not read in full: alpha starting)' "$root/out" && ok "a starting cluster is a GAP" || bad "starting" "$(cat "$root/out")"
sessions; SESSIONS_RC=10 run
grep -qF 'renewal: GAP (marvel exited 10 with no cluster to explain it)' "$root/out" && ok "a nonzero marvel exit that no cluster explains is a GAP" || bad "exit status" "$(cat "$root/out")"
sessions; run
grep -qF 'renewal: nudge alpha/seat-a-g1-0 (logged-out)' "$root/out" && ! grep -qF 'GAP' "$root/out" && ok "a full answer with every cluster answered lists and stores" || bad "full answer" "$(cat "$root/out")"
printf '{"hostx/lab": "2030-02-20T12:00:00Z"}\n' >"$root/state/last-expiry.json"
printf '{"hostx/lab": "2030-02-20T12:00:00Z", "other/lab": "2031-01-01T00:00:00Z"}\n' >"$root/state/last-expiry.json"
auth "2030-02-20T12:00:00Z" true; run; sessions
grep -qF '"other/lab": "2031-01-01T00:00:00Z"' "$root/state/last-expiry.json" && ok "another host and label's stored expiry is kept" || bad "other kept" "$(cat "$root/state/last-expiry.json")"

# --- state files and usage -----------------------------------------------------------------------------------------
m="$(python3 -I -c 'import os, sys; print(format(os.stat(sys.argv[1]).st_mode & 0o777, "o"))' "$root/state/last-expiry.json")"  # same on BSD and GNU, unlike stat
[[ "$m" == 600 ]] && ok "the state files are mode 600" || bad "file mode" "$m"
run --warn-hours x; [[ $rc -eq 2 ]] && ok "a bad --warn-hours is exit 2" || bad "warn-hours" "rc=$rc"
run --bogus 1; [[ $rc -eq 2 ]] && ok "an unknown option is exit 2" || bad "bogus" "rc=$rc"
run --home-label '/Users/x'; [[ $rc -eq 2 ]] && ok "a label that is a path is refused" || bad "label" "rc=$rc"
run --now soon; [[ $rc -eq 2 ]] && ok "a bad --now is exit 2" || bad "now" "rc=$rc"

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
