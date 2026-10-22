#!/usr/bin/env bash
# Prove idle-shadow (idle-scheduler M2, read side): the shadow run that follows freebusy in
# the sweep. Each case builds a scratch home and a scratch DIRECTOR_STATE, puts a stub bd
# first on PATH (it answers `ready` only, and logs every call), makes marvel, director-mcp,
# gh and ssh forbidden stubs, and runs the script under `env -i` with no MARVEL_SOCKET. No
# real bd, marvel or gh is reached, and nothing is written outside the scratch root.
#
# The marry step calls `marvel marry --input FILE` (marvel#992, read-only). The marvel stub
# answers that one call only: with a canned answer, with "unknown command", with a failure
# or with a hang, and it saves the input it was given so the tests can check its shape. Any
# other marvel call is a forbidden call. Set IS_REAL_MARVEL to a marvel binary built from
# marvel 493909ec to run the same cases against the real verb: the canned answers are then
# ignored and every assertion must hold on what the real marry step returns. Without it the
# real-verb cases print SKIP.
#
# Usage: skills/director/scripts/verify-idle-shadow.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
IS="${IS:-$here/idle-shadow}"
BASHES="${BASHES:-/bin/bash bash}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d "${TMPDIR:-/tmp}/is.XXXXXX")"
root="$(cd "$root" && pwd -P)"
cleanup() { rm -rf "$root"; }
trap cleanup EXIT

mkdir -p "$root/bin" "$root/fix" "$root/home" "$root/work"
FIX="$root/fix"

# --- stubs ------------------------------------------------------------------
cat >"$root/bin/bd" <<'S'
#!/usr/bin/env bash
echo "$*" >>"$IS_FIX/bd.log"
if [[ "${1:-}" != ready ]]; then echo "bd $*" >>"$IS_FIX/forbidden.log"; exit 1; fi
if [[ -e "$IS_FIX/bd.fail" ]]; then echo "Error: dolt server unreachable" >&2; echo "second stderr line" >&2; exit 1; fi
if [[ -e "$IS_FIX/bd.slow" ]]; then echo "$$" >>"$IS_FIX/slow.pids"; sleep "$(cat "$IS_FIX/bd.slow")"; fi
json=0; excl=0; lab=0; prev=""
for a in "$@"; do
  if [[ "$a" == --json ]]; then json=1; fi
  if [[ "$prev" == --exclude-label && "$a" == scope:client ]]; then excl=1; fi
  if [[ "$prev" == --label && "$a" == scope:client ]]; then lab=1; fi
  prev="$a"
done
[[ "$json" == 1 ]] || { echo "stub bd: no --json in: $*" >&2; exit 2; }
if [[ -e "$IS_FIX/bd.nojson" ]]; then echo "this is not json"; exit 0; fi
if [[ "$lab" == 1 ]]; then
  if [[ -e "$IS_FIX/bd.clientfail" ]]; then echo "Error: client read failed" >&2; exit 1; fi
  cat "$IS_FIX/client.json"; exit 0
fi
if [[ "$excl" == 1 && ! -e "$IS_FIX/bd.ignoreexclude" ]]; then cat "$IS_FIX/items.json"; exit 0; fi
# a bd that ignores --exclude-label: its answer carries the client items too
cat "$IS_FIX/items.all.json"
S
cat >"$root/bin/marvel" <<'S'
#!/usr/bin/env bash
echo "$*" >>"$IS_FIX/marvel.log"
echo "MARVEL_SOCKET=${MARVEL_SOCKET-unset} MARVEL_HOME=${MARVEL_HOME-unset}" >>"$IS_FIX/marvel.env"
if [[ "${1:-}" != marry || "${2:-}" != --input || -z "${3:-}" || $# -ne 3 ]]; then echo "marvel $*" >>"$IS_FIX/forbidden.log"; exit 1; fi
if [[ -n "${IS_REAL_MARVEL:-}" ]]; then cp "$3" "$IS_FIX/marry.in"; exec "$IS_REAL_MARVEL" "$@"; fi
cp "$3" "$IS_FIX/marry.in"
if [[ -e "$IS_FIX/marvel.fail" ]]; then echo "marry input: boom" >&2; echo "second stderr line" >&2; exit 1; fi
if [[ -e "$IS_FIX/marvel.slow" ]]; then echo "$$" >>"$IS_FIX/slow.pids"; sleep "$(cat "$IS_FIX/marvel.slow")"; fi
if [[ -e "$IS_FIX/marvel.noterm" ]]; then trap '' TERM; echo "$$" >>"$IS_FIX/slow.pids"; sleep 25; fi
if [[ -e "$IS_FIX/marry.out" ]]; then cat "$IS_FIX/marry.out"; exit 0; fi
echo 'Error: unknown command "marry" for "marvel"' >&2
exit 1
S
for t in director-mcp gh ssh; do
  cat >"$root/bin/$t" <<'S'
#!/usr/bin/env bash
echo "$(basename "$0") $*" >>"$IS_FIX/forbidden.log"
exit 1
S
done
chmod +x "$root/bin/"*

# --- fixtures ---------------------------------------------------------------
CLIENT_ID="zz-clientid-7"
CLIENT_TITLE="CLIENTTITLE-secret-text"
NOW="$(jq -nr '"2026-10-21T08:05:00Z" | fromdateiso8601')"
SEATS_AT="2026-10-21T08:04:00Z"

item() { # id labels-json priority created
  jq -n -c --arg id "$1" --argjson l "$2" --argjson p "${3:-2}" --arg c "${4:-2026-10-01T00:00:00Z}" \
    '{id: $id, title: ("title of " + $id), description: "body", labels: $l, priority: $p, created_at: $c, status: "open", issue_type: "task"}'
}
seat() { # cluster team role name state
  jq -n -c --arg at "$SEATS_AT" --arg c "$1" --arg t "$2" --arg r "$3" --arg n "$4" --arg s "$5" \
    '{at: $at, cluster: $c, workspace: "wsa", team: $t, role: $r, name: $n, state: $s, reason: "", last_active: null, active: null, last_turn: null}'
}
client_item() {
  jq -n -c --arg id "$CLIENT_ID" --arg t "$CLIENT_TITLE" '{id: $id, title: $t, description: $t, labels: ["scope:client", "marvel"], priority: 1, created_at: "2026-10-02T00:00:00Z", status: "open"}'
}

reset() { # the default scene: the five worked shapes, no class on any, three seats
  rm -rf "$root/state" "$FIX"; mkdir -p "$FIX" "$root/state/freebusy"
  {
    item i-1 '["aae-orc","marvel","source:session"]' 1
    item i-2 '["aae-orc"]' 2
    item i-3 '["aae-orc","source:finding"]' 2
    item i-4 '["aae-orc","marvel","actor:operator","host:alpha"]' 1
    item i-5 '[]' 3
  } | jq -s '.' >"$FIX/items.json"
  cp "$FIX/items.json" "$FIX/items.all.json"
  echo '[]' >"$FIX/client.json"
  cat >"$FIX/convention.md" <<'MD'
## Labels

### Axes

| Axis | Label format | Examples |
|------|--------------|----------|
| **Orchestrator scope** | orchestrator name, lowercase | `aae-orc` (NOT `orc`) |
| **Component** | flat, lowercase | `marvel`, `director`, `compa`, `ThreeX` |
| **Scope modifier** | `scope:<qualifier>` | `scope:cross-cutting` |
| **Origin** | `source:<kind>` | `source:session`, `source:finding` |
| **Lifecycle** | single word | `inbox`, `kos-bridge`, `fleet-gap` |
MD
  {
    seat alpha crew builder b1 free
    seat alpha crew builder b2 busy
    seat alpha crew reviewer r1 waiting
    seat beta desk builder d1 unknown
  } >"$root/state/freebusy/seats.jsonl"
}
set_items() { jq -s '.' >"$FIX/items.json"; cp "$FIX/items.json" "$FIX/items.all.json"; }

rc=0
runis() { # bash, then env assignments
  local b="$1"; shift
  rc=0
  ( cd "$root/work" && env -i HOME="$root/home" PATH="$root/bin:$PATH" LANG=C TMPDIR="$root" \
      DIRECTOR_STATE="$root/state" IS_FIX="$FIX" IDLE_SHADOW_TEST_CLOCK=1 IDLE_SHADOW_NOW="$NOW" \
      IDLE_SHADOW_CALL_TIMEOUT=3 IDLE_SHADOW_LABEL_CONVENTION="$FIX/convention.md" "$@" "$b" "$IS" ) >"$root/out" 2>"$root/err" || rc=$?
}
out() { cat "$root/out"; }
has() { /usr/bin/grep -q -F -- "$1" "$root/out"; }
count_line() { /usr/bin/grep '^idle-shadow: ready' "$root/out" || true; }
st="$root/state/idle-shadow"
# GNU stat first: on Linux `stat -f %Lp` is a filesystem report that succeeds and prints something else, so a BSD-first chain never reaches -c
mode_of() { stat -c %a "$1" 2>/dev/null || stat -f %Lp "$1"; }

# --- the vendored M1 vectors, pinned by sha ---------------------------------
vec="$here/fixtures/marry-vectors.6e305d4e.json"
want_sha="7e492f56c8e08baf78a9068ac1f79f324a3787e1ee6aab11072e1baabad7bfe1"
if [[ -f "$vec" ]]; then
  got_sha="$(shasum -a 256 "$vec" | cut -d' ' -f1)"
  [[ "$got_sha" == "$want_sha" ]] && ok "V1 the vendored marry vectors match the pinned sha" || bad "V1 vector sha" "$got_sha"
  [[ "$(jq -r '.rows | length' "$vec")" == 10 && "$(jq -r '.rows | index("client isolation") != null and index("strict free") != null' "$vec")" == true ]] \
    && ok "V2 the vectors name the ten properties, strict free and client isolation among them" || bad "V2 vector rows"
else
  bad "V1 the vendored marry vectors are missing ($vec)"
fi
[[ -f "$here/fixtures/marry-vectors.6e305d4e.json.pin" ]] \
  && /usr/bin/grep -q "6e305d4e" "$here/fixtures/marry-vectors.6e305d4e.json.pin" && /usr/bin/grep -q "$want_sha" "$here/fixtures/marry-vectors.6e305d4e.json.pin" \
  && ok "V3 the pin file records the marvel commit and the sha" || bad "V3 pin file"


# --- the marry step: marvel's verb, called once, read-only ---------------------------------
# $1 is the mode: stub (canned answers, the default) or real (IS_REAL_MARVEL, answers ignored).
# the fixture eligibility file: one seat that covers component marvel, class story-implementation and duty first-turn
ELIG_FIXTURE="$here/fixtures/seat-eligibility.one-pair.json"
ELIG_OK="$(jq -c . "$ELIG_FIXTURE")"
RUN_ID="idle-shadow-$NOW"
one_item() { item e-1 '["class:story-implementation","marvel","aae-orc"]' 1 | set_items; }
canned() { if [[ "$MODE" == stub ]]; then printf '%s\n' "$1" >"$FIX/marry.out"; fi; }
runm() { # bash, then env assignments; adds the eligibility file and the sender cluster
  local b="$1"; shift
  if [[ "$MODE" == real ]]; then runis "$b" IDLE_SHADOW_SEAT_ELIGIBILITY="${ELIG_PATH:-$FIX/elig.json}" IDLE_SHADOW_SENDER_CLUSTER=alpha IS_REAL_MARVEL="$REAL" "$@"
  else runis "$b" IDLE_SHADOW_SEAT_ELIGIBILITY="${ELIG_PATH:-$FIX/elig.json}" IDLE_SHADOW_SENDER_CLUSTER=alpha "$@"; fi
}
marry_cases() {
  local B="$1" MODE="$2" cl tag="M[$2]"

  # the one-would-pair vector: a class, a component, one free in-scope same-cluster seat
  reset; one_item
  ELIG_PATH="$ELIG_FIXTURE"
  canned '{"pairs":[{"item":"e-1","seat":"agent://crew/b1","lease":"'"$RUN_ID"'/e-1"}],"leases":[{"id":"'"$RUN_ID"'/e-1","item":"e-1","seat":"agent://crew/b1","valid_until":"2026-10-21T08:35:00Z","state":"open"}],"count":{"ready":1,"eligible":1,"free":1,"would_pair":1}}'
  runm "$B"
  ELIG_PATH=""
  cl="$(count_line)"
  [[ "$rc" == 0 ]] && ok "$tag 1 exits 0" || bad "$tag 1 exit $rc" "$(out)"
  [[ "$(wc -l <"$FIX/marvel.log" | tr -d ' ')" == 1 && "$(sed -e 's|--input .*|--input FILE|' "$FIX/marvel.log")" == "marry --input FILE" ]] \
    && ok "$tag 1 marvel was called once, as marry --input FILE, and nothing else" || bad "$tag 1 marvel calls" "$(cat "$FIX/marvel.log" 2>&1)"
  [[ "$(cat "$FIX/marvel.env")" == "MARVEL_SOCKET=unset MARVEL_HOME=unset" ]] && ok "$tag 1 marvel is run with no MARVEL_SOCKET and no MARVEL_HOME of the script's making" || bad "$tag 1 env" "$(cat "$FIX/marvel.env")"
  [[ "$cl" == *"would-pair 1"* && "$cl" == *"leases sent 0"* ]] && ok "$tag 1 would-pair 1, leases sent 0" || bad "$tag 1 count" "$cl"
  [[ "$(wc -l <"$st/would-pair.jsonl" | tr -d ' ')" == 1 ]] && ok "$tag 1 exactly one would-pair row" || bad "$tag 1 rows" "$(cat "$st/would-pair.jsonl")"
  [[ "$(jq -r '[.item, .seat, .lease] | join(" ")' "$st/would-pair.jsonl")" == "e-1 agent://crew/b1 $RUN_ID/e-1" ]] && ok "$tag 1 the row names the item, the seat and the lease" || bad "$tag 1 row" "$(cat "$st/would-pair.jsonl")"
  has "- would-pair e-1 agent://crew/b1 (lease $RUN_ID/e-1; shadow, nothing is sent)" && ok "$tag 1 the pair is printed" || bad "$tag 1 print" "$(out)"
  ! has "- GAP marry" && ok "$tag 1 no marry GAP" || bad "$tag 1 gap" "$(out)"
  [[ ! -e "$FIX/forbidden.log" ]] && ok "$tag 1 no send, inject, bd write or gh call" || bad "$tag 1 forbidden" "$(cat "$FIX/forbidden.log")"
  [[ "$(jq -r '.would_pair' "$st/flow.jsonl")" == 1 ]] && ok "$tag 1 the flow line says 1" || bad "$tag 1 flow"
  [[ -z "$(jq -r 'select(.id == "e-1")' "$st/unmatched.jsonl")" ]] && ok "$tag 1 a paired item is not unmatched" || bad "$tag 1 unmatched"
  # what marvel was given
  jq -e '(.items | length) == 1 and .items[0].id == "e-1" and .items[0].classes == {label: "story-implementation"} and .items[0].component == "marvel"
         and .items[0].duties == ["first-turn"] and .items[0].status == "open" and .items[0].priority == 1' "$FIX/marry.in" >/dev/null \
    && ok "$tag 1 the item goes in with its class under the label source, its component and its duties" || bad "$tag 1 item input" "$(cat "$FIX/marry.in")"
  jq -e '.rulings.work_class_source == "label" and .placement.sender_cluster == "alpha" and .lease_cap == 1 and .lease_minutes == 30 and .run_id == "'"$RUN_ID"'" and .now == "2026-10-21T08:05:00Z" and .stop == {}' "$FIX/marry.in" >/dev/null \
    && ok "$tag 1 run id, clock, ruling, placement, lease cap 1 and 30 minutes" || bad "$tag 1 envelope" "$(cat "$FIX/marry.in")"
  jq -e '(.seats | length) == 4
         and ([.seats[] | select(.address == "agent://crew/b1")][0] | .availability == "free" and .team == "crew" and .cluster == "alpha" and .classes == ["story-implementation"] and .scopes == ["marvel"] and (.cells | has("duty.first-turn")))
         and ([.seats[] | select(.address == "agent://crew/b2")][0].availability == "working")
         and ([.seats[] | select(.address == "agent://crew/r1")][0].availability == "parked")
         and ([.seats[] | select(.address == "agent://desk/d1")][0].availability == "unknown")' "$FIX/marry.in" >/dev/null \
    && ok "$tag 1 seats keep freebusy's states: free, working, parked, unknown, with the eligibility joined by address" || bad "$tag 1 seat input" "$(jq -c .seats "$FIX/marry.in")"
  ! /usr/bin/grep -q -e "title of" -e "description" "$FIX/marry.in" && ok "$tag 1 no title or description is passed to marvel" || bad "$tag 1 title in input"

  # a seat outside the item's component never pairs
  reset; one_item
  jq -c '.seats[0].scopes = ["kos"]' <<<"$ELIG_OK" >"$FIX/elig.json"
  canned '{"unmatched":[{"item":"e-1","kind":"no-eligible-seat"}],"count":{"ready":1,"unmatched":1,"no_eligible_seat":1,"free":1}}'
  runm "$B"
  cl="$(count_line)"
  [[ "$rc" == 0 && "$cl" == *"eligible 1"* && "$cl" == *"unmatched 1 (no class 0, no component 0, operator-only 0, no eligible seat 1)"* && "$cl" == *"would-pair 0"* ]] \
    && ok "$tag 2 an out-of-scope free seat gives no pair and one unmatched, no eligible seat" || bad "$tag 2 count" "$cl"
  has "- unmatched no eligible seat (1): e-1" && ok "$tag 2 the list groups no eligible seat" || bad "$tag 2 list" "$(out)"
  [[ "$(jq -r 'select(.id == "e-1") | .missing | join(",")' "$st/unmatched.jsonl")" == "seat" ]] && ok "$tag 2 the unmatched row names the missing seat" || bad "$tag 2 row" "$(cat "$st/unmatched.jsonl")"
  [[ ! -s "$st/would-pair.jsonl" ]] && ok "$tag 2 no would-pair row" || bad "$tag 2 pair row"

  # a seat that is not free never pairs, and is not told as free
  for stt in waiting unknown busy; do
    reset; one_item; printf '%s\n' "$ELIG_OK" >"$FIX/elig.json"
    seat alpha crew builder b1 "$stt" >"$root/state/freebusy/seats.jsonl"
    canned '{"count":{"ready":1,"eligible":1,"parked":1}}'
    runm "$B"
    [[ "$rc" == 0 && ! -s "$st/would-pair.jsonl" && "$(count_line)" == *"would-pair 0"* ]] && jq -e '.seats[0].availability != "free"' "$FIX/marry.in" >/dev/null \
      && ok "$tag 3 a $stt seat is passed as not free and never pairs" || bad "$tag 3 $stt" "$(cat "$FIX/marry.in" 2>&1)"
  done
  reset; one_item; printf '%s\n' "$ELIG_OK" >"$FIX/elig.json"
  runm "$B" IDLE_SHADOW_NOW="$NOW_OLD2"
  jq -e '[.seats[].availability] | all(. == "unknown")' "$FIX/marry.in" >/dev/null && ok "$tag 3 an old seats file reaches marvel as all unknown" || bad "$tag 3 old seats" "$(jq -c '[.seats[].availability]' "$FIX/marry.in" 2>&1)"

  # a free seat on another cluster than the sender: unplaced
  reset; one_item; printf '%s\n' "$ELIG_OK" >"$FIX/elig.json"
  seat beta crew builder b1 free >"$root/state/freebusy/seats.jsonl"
  canned '{"unplaced":[{"item":"e-1","seat":"agent://crew/b1","reason":"no sender ruled"}],"count":{"ready":1,"eligible":1,"free":1}}'
  runm "$B"
  has "- unplaced: no sender ruled: e-1 agent://crew/b1" && [[ "$(count_line)" == *"would-pair 0"* && ! -s "$st/would-pair.jsonl" ]] \
    && ok "$tag 4 a seat on another cluster prints unplaced: no sender ruled, and no pair" || bad "$tag 4 unplaced" "$(out)"
  # no sender cluster named: the input carries none
  reset; one_item; printf '%s\n' "$ELIG_OK" >"$FIX/elig.json"
  canned '{"unplaced":[{"item":"e-1","seat":"agent://crew/b1","reason":"no sender ruled"}],"count":{"ready":1,"eligible":1,"free":1}}'
  rc=0
  ( cd "$root/work" && env -i HOME="$root/home" PATH="$root/bin:$PATH" LANG=C TMPDIR="$root" DIRECTOR_STATE="$root/state" IS_FIX="$FIX" IDLE_SHADOW_TEST_CLOCK=1 IDLE_SHADOW_NOW="$NOW" \
      IDLE_SHADOW_CALL_TIMEOUT=3 IDLE_SHADOW_LABEL_CONVENTION="$FIX/convention.md" IDLE_SHADOW_SEAT_ELIGIBILITY="$FIX/elig.json" ${REAL:+IS_REAL_MARVEL="$REAL"} "$B" "$IS" ) >"$root/out" 2>"$root/err" || rc=$?
  jq -e '.placement.sender_cluster == ""' "$FIX/marry.in" >/dev/null && has "- unplaced: no sender ruled: e-1" && ok "$tag 4 with no sender cluster set nothing is placed" || bad "$tag 4 no sender" "$(out)"

  # the eligibility file: absent and malformed are GAPs, never a guess
  reset; one_item; rm -f "$FIX/elig.json"
  canned '{"unmatched":[{"item":"e-1","kind":"no-eligible-seat"}],"count":{"ready":1,"unmatched":1,"no_eligible_seat":1,"free":1}}'
  runm "$B"
  has "- GAP seat-eligibility: " && [[ "$rc" == 0 && "$(count_line)" == *"no eligible seat 1"* ]] && jq -e '[.seats[] | has("classes") or has("scopes") or has("cells")] | all(. == false)' "$FIX/marry.in" >/dev/null \
    && ok "$tag 5 no eligibility file is a GAP, seats go in with no classes, scopes or cells, and the item reads no eligible seat" || bad "$tag 5 absent" "$(out)"
  for badjson in 'not json' '[]' '{"seats": "x"}' '{"seats":[{"classes":["a"]}]}'; do
    reset; one_item; printf '%s\n' "$badjson" >"$FIX/elig.json"
    canned '{"unmatched":[{"item":"e-1","kind":"no-eligible-seat"}],"count":{}}'
    runm "$B"
    has "- GAP seat-eligibility: " && [[ "$rc" == 0 ]] && jq -e '[.seats[] | has("classes")] | all(. == false)' "$FIX/marry.in" >/dev/null \
      && ok "$tag 5 a malformed eligibility file ($badjson) is a GAP and none of it is used" || bad "$tag 5 malformed $badjson" "$(out)"
  done
  # the default path lives in the state directory
  reset; one_item; mkdir -p "$st"; chmod 700 "$st"; printf '%s\n' "$ELIG_OK" >"$st/seat-eligibility.json"
  canned '{"pairs":[{"item":"e-1","seat":"agent://crew/b1","lease":"'"$RUN_ID"'/e-1"}],"count":{"would_pair":1}}'
  runis "$B" IDLE_SHADOW_SENDER_CLUSTER=alpha ${REAL:+IS_REAL_MARVEL="$REAL"}
  ! has "- GAP seat-eligibility" && jq -e '.seats[0].classes == ["story-implementation"] or ([.seats[] | select(.address == "agent://crew/b1")][0].classes == ["story-implementation"])' "$FIX/marry.in" >/dev/null \
    && ok "$tag 5 with no variable set the file in the state directory is read" || bad "$tag 5 default path" "$(out)"

  # marry is not run on a reading it cannot trust
  reset; one_item; printf '%s\n' "$ELIG_OK" >"$FIX/elig.json"; rm -f "$root/state/freebusy/seats.jsonl"
  runm "$B"
  [[ ! -e "$FIX/marvel.log" ]] && has "- GAP marry: not run, the seats were not read" && [[ "$(count_line)" == *"no eligible seat GAP"* ]] \
    && ok "$tag 6 unread seats: marvel is not called, and no eligible seat is GAP" || bad "$tag 6 seats" "$(out)"
  reset; one_item; printf '%s\n' "$ELIG_OK" >"$FIX/elig.json"
  runm "$B" IDLE_SHADOW_LABEL_CONVENTION=
  [[ ! -e "$FIX/marvel.log" ]] && has "- GAP marry: not run, the component set was not read" \
    && ok "$tag 6 an unread component set: marvel is not called" || bad "$tag 6 components" "$(out)"
  reset; echo '[]' >"$FIX/items.json"; cp "$FIX/items.json" "$FIX/items.all.json"; printf '%s\n' "$ELIG_OK" >"$FIX/elig.json"
  runm "$B"
  [[ ! -e "$FIX/marvel.log" && ! -s "$st/would-pair.jsonl" && "$rc" == 0 ]] && ok "$tag 6 no eligible item: marvel is not called" || bad "$tag 6 none" "$(out)"
  reset; touch "$FIX/bd.fail"; printf '%s\n' "$ELIG_OK" >"$FIX/elig.json"
  runm "$B"
  [[ ! -e "$FIX/marvel.log" && "$rc" == 0 ]] && ok "$tag 6 items unread: marvel is not called" || bad "$tag 6 items" "$(out)"

  # client isolation: nothing of a client item reaches marvel
  reset; one_item; printf '%s\n' "$ELIG_OK" >"$FIX/elig.json"
  { cat "$FIX/items.json" | jq -c '.[]'; client_item; } | jq -s '.' >"$FIX/items.all.json"
  echo "[$(client_item)]" >"$FIX/client.json"
  touch "$FIX/bd.ignoreexclude"
  canned '{"count":{}}'
  runm "$B"
  ! /usr/bin/grep -q -F -e "$CLIENT_ID" -e "$CLIENT_TITLE" "$FIX/marry.in" && ok "$tag 7 no part of a client item is in what marvel was given" || bad "$tag 7 client in input"
  rm -f "$FIX/bd.ignoreexclude"

  # an operator item and an unlabelled item are not sent to marvel at all
  reset; printf '%s\n' "$ELIG_OK" >"$FIX/elig.json"
  { item o-1 '["class:story-implementation","marvel","actor:operator"]'; item n-1 '["marvel"]'; item e-1 '["class:story-implementation","marvel"]'; } | set_items
  canned '{"count":{"ready":1}}'
  runm "$B"
  jq -e '[.items[].id] == ["e-1"]' "$FIX/marry.in" >/dev/null && ok "$tag 8 only items with a class and a component, and not the operator's, go to marvel" || bad "$tag 8 items" "$(jq -c '[.items[].id]' "$FIX/marry.in")"

  if [[ "$MODE" == stub ]]; then
    # a marvel without the verb, one that fails, one that hangs, one that is missing
    reset; one_item; printf '%s\n' "$ELIG_OK" >"$FIX/elig.json"
    runm "$B"
    cl="$(count_line)"
    [[ "$rc" == 0 ]] && has "- GAP marry: this marvel has no marry verb (marvel#992 is merged; update marvel on this host)" && [[ "$cl" == *"would-pair 0"* && "$cl" == *"no eligible seat GAP"* && ! -s "$st/would-pair.jsonl" ]] \
      && ok "$tag 9 a marvel with no marry verb: one GAP, exit 0, no pair, no eligible seat is GAP" || bad "$tag 9 noverb" "$(out)"
    reset; one_item; printf '%s\n' "$ELIG_OK" >"$FIX/elig.json"; touch "$FIX/marvel.fail"
    runm "$B"
    [[ "$rc" == 0 ]] && has "- GAP marry: marvel marry failed (marry input: boom)" && ! has "second stderr line" \
      && ok "$tag 9 a failing marry is one GAP with its first stderr line, exit 0" || bad "$tag 9 fail" "$(out)"
    reset; one_item; printf '%s\n' "$ELIG_OK" >"$FIX/elig.json"; echo 30 >"$FIX/marvel.slow"
    s0=$SECONDS
    runm "$B"
    el=$((SECONDS - s0))
    [[ "$rc" == 0 && "$el" -lt 20 ]] && has "- GAP marry: marvel marry failed (no answer within 3s)" && ok "$tag 9 a hung marry is stopped at the bound, took ${el}s" || bad "$tag 9 hang" "${el}s $(out)"
    alive=0
    if [[ -f "$FIX/slow.pids" ]]; then while read -r p; do if kill -0 "$p" 2>/dev/null; then alive=1; fi; done <"$FIX/slow.pids"; fi
    [[ "$alive" == 0 ]] && ok "$tag 9 the hung call's process is gone" || bad "$tag 9 survivor"
    reset; one_item; printf '%s\n' "$ELIG_OK" >"$FIX/elig.json"
    runm "$B" IDLE_SHADOW_MARVEL="$root/no-such-marvel"
    [[ "$rc" == 0 ]] && has "- GAP marry: marvel was not found ($root/no-such-marvel)" && ok "$tag 9 a missing marvel is a GAP, exit 0" || bad "$tag 9 missing" "$(out)"
    reset; one_item; printf '%s\n' "$ELIG_OK" >"$FIX/elig.json"; canned 'not json'
    runm "$B"
    [[ "$rc" == 0 ]] && has "- GAP marry: marvel marry answered with something that is not a JSON object" && [[ ! -s "$st/would-pair.jsonl" ]] && ok "$tag 9 an answer that is not JSON is a GAP and no pair" || bad "$tag 9 nojson" "$(out)"
    # a marvel that ignores TERM does not hold the run past the bound plus the grace
    reset; one_item; printf '%s\n' "$ELIG_OK" >"$FIX/elig.json"; touch "$FIX/marvel.noterm"
    s0=$SECONDS
    runm "$B" IDLE_SHADOW_KILL_GRACE=1
    el=$((SECONDS - s0))
    [[ "$rc" == 0 && "$el" -lt 15 ]] && has "- GAP marry: marvel marry failed (no answer within 3s)" && ok "$tag 10 a marvel that ignores TERM is killed after the grace, took ${el}s" || bad "$tag 10 noterm" "${el}s $(out)"
    alive=0
    if [[ -f "$FIX/slow.pids" ]]; then while read -r p; do if kill -0 "$p" 2>/dev/null; then alive=1; kill -KILL "$p" 2>/dev/null || true; fi; done <"$FIX/slow.pids"; fi
    [[ "$alive" == 0 ]] && ok "$tag 10 the TERM-ignoring process is gone" || bad "$tag 10 survivor"
    # MARVEL_SOCKET in the caller's environment never reaches the marry call
    reset; one_item; printf '%s\n' "$ELIG_OK" >"$FIX/elig.json"; canned '{"count":{}}'
    runm "$B" MARVEL_SOCKET=/nowhere/marvel.sock
    [[ "$(cat "$FIX/marvel.env" 2>/dev/null)" == "MARVEL_SOCKET=unset MARVEL_HOME=unset" ]] && ok "$tag 11 a MARVEL_SOCKET in the environment is cleared for the marry call" || bad "$tag 11 socket" "$(cat "$FIX/marvel.env" 2>&1)"
    # a pair whose seat is not an agent:// address is never written
    reset; one_item; printf '%s\n' "$ELIG_OK" >"$FIX/elig.json"
    canned '{"pairs":[{"item":"e-1","seat":"role://crew/builder","lease":"x/e-1"}],"count":{"would_pair":1}}'
    runm "$B"
    [[ "$rc" == 0 && ! -s "$st/would-pair.jsonl" ]] && has "- GAP marry: " && ok "$tag 9 a row naming anything but an agent:// seat is refused, never written" || bad "$tag 9 role row" "$(cat "$st/would-pair.jsonl"; out)"
  fi
}

NOW_OLD2="$(jq -nr '"2026-10-21T10:00:00Z" | fromdateiso8601')"
REAL="${IS_REAL_MARVEL:-}"
for B in $BASHES; do
  echo "== $B"
  "$B" -n "$IS" 2>/dev/null && ok "0 bash -n passes ($B)" || bad "0 bash -n ($B)"

  # ---- 1 the five worked shapes: 0 pairs, 5 unmatched, each with its missing field
  reset
  runis "$B"
  [[ "$rc" == 0 ]] && ok "1 exits 0" || bad "1 exit $rc"
  cl="$(count_line)"
  [[ "$cl" == *"ready 5"* ]] && ok "1 the count line says 5 ready" || bad "1 ready" "$cl"
  [[ "$cl" == *"eligible 0"* ]] && ok "1 eligible 0" || bad "1 eligible" "$cl"
  [[ "$cl" == *"unmatched 5 (no class 5, no component 3, operator-only 1, no eligible seat 0)"* ]] && ok "1 unmatched 5, by kind; with no eligible item no seat is wanted, so no eligible seat is 0" || bad "1 unmatched kinds" "$cl"
  [[ "$cl" == *"would-pair 0"* ]] && ok "1 would-pair 0" || bad "1 would-pair" "$cl"
  [[ "$(wc -l <"$st/unmatched.jsonl" | tr -d ' ')" == 5 ]] && ok "1 five unmatched rows" || bad "1 unmatched rows" "$(cat "$st/unmatched.jsonl" 2>&1)"
  [[ "$(jq -r 'select(.id == "i-2") | .missing | join(",")' "$st/unmatched.jsonl")" == "class,component" ]] && ok "1 i-2 misses class and component" || bad "1 i-2 missing"
  [[ "$(jq -r 'select(.id == "i-1") | .missing | join(",")' "$st/unmatched.jsonl")" == "class" ]] && ok "1 i-1 (a component, no class) misses class only" || bad "1 i-1 missing"
  [[ "$(jq -r 'select(.id == "i-4") | .operator' "$st/unmatched.jsonl")" == true ]] && ok "1 i-4 (actor:operator) is marked operator-only" || bad "1 i-4 operator"
  has "- unmatched no class (5): i-1 i-2 i-3 i-4 i-5" && ok "1 the list groups no class" || bad "1 no class group" "$(out)"
  has "- unmatched no component (3): i-2 i-3 i-5" && ok "1 the list groups no component" || bad "1 no component group" "$(out)"
  has "- unmatched operator-only (1): i-4" && ok "1 the list groups operator-only" || bad "1 operator group" "$(out)"
  [[ ! -s "$st/would-pair.jsonl" && -f "$st/would-pair.jsonl" ]] && ok "1 would-pair.jsonl exists and holds no row" || bad "1 would-pair file"
  has "no item carries a class label" && ok "1 the expected first reading is said plainly" || bad "1 plain note"
  ! has "ERROR" && ! has "FAIL" && ok "1 the empty result is not framed as an error" || bad "1 framed as error"
  ! has "- GAP marry" && [[ ! -e "$FIX/marvel.log" ]] && ok "1 with no eligible item marvel is not called and there is no marry GAP" || bad "1 marry not needed" "$(out)"
  [[ "$cl" == *"pane veto"* ]] && ok "1 the count line carries the strict-free caveat" || bad "1 caveat" "$cl"
  [[ "$cl" == *"seats: 1 free, 1 busy, 1 waiting, 1 unknown"* ]] && ok "1 seat states are freebusy's, never coerced" || bad "1 seats" "$cl"

  # ---- 2 an item with a class and a component and a free seat: one candidate, no send
  reset
  { item e-1 '["class:story-implementation","marvel","aae-orc"]' 1; item i-2 '["aae-orc"]' 2; } | set_items
  runis "$B"
  cl="$(count_line)"
  [[ "$rc" == 0 && "$cl" == *"eligible 1"* ]] && ok "2 an item with a class and a component is eligible" || bad "2 eligible" "$cl"
  [[ "$cl" == *"unmatched 1 (no class 1, no component 1,"* ]] && ok "2 the other item is the only unmatched one" || bad "2 unmatched" "$cl"
  has "- GAP marry: this marvel has no marry verb" && [[ "$cl" == *"would-pair 0"* && ! -s "$st/would-pair.jsonl" ]] \
    && ok "2 a marvel without the verb holds the GAP and no row" || bad "2 would-pair" "$(out)"
  [[ ! -e "$FIX/forbidden.log" ]] && ok "2 nothing was sent, injected or written" || bad "2 forbidden" "$(cat "$FIX/forbidden.log")"
  ! has "e-1" && ok "2 an eligible item is not on the unmatched list" || bad "2 eligible listed" "$(out)"

  # ---- 3 class label rules
  reset
  {
    item c-ok '["class:design-brief","marvel"]'
    item c-bogus '["class:not-a-class","marvel"]'
    item c-two '["class:design-brief","class:pr-review","marvel"]'
    item c-none '["marvel"]'
    item c-orc '["class:research-local","aae-orc"]'
  } | set_items
  runis "$B"
  [[ "$(jq -r 'select(.id == "c-ok")' "$st/unmatched.jsonl")" == "" ]] && ok "3 one class from the six, with a component, is not unmatched" || bad "3 c-ok"
  [[ "$(jq -r 'select(.id == "c-bogus") | .missing | join(",")' "$st/unmatched.jsonl")" == class ]] && ok "3 a class outside the six is no class" || bad "3 c-bogus"
  [[ "$(jq -r 'select(.id == "c-two") | .missing | join(",")' "$st/unmatched.jsonl")" == class ]] && ok "3 two class labels are no class, never a pick" || bad "3 c-two"
  [[ "$(jq -r 'select(.id == "c-orc") | .missing | join(",")' "$st/unmatched.jsonl")" == component ]] && ok "3 aae-orc alone is not a component" || bad "3 c-orc"
  for c in story-implementation design-brief human-decision pr-review research-local gate-definition; do
    item "k-$c" "[\"class:$c\",\"marvel\"]"
  done | set_items
  runis "$B"
  [[ "$(count_line)" == *"eligible 6"* ]] && ok "3 each of the six class ids is accepted" || bad "3 six ids" "$(count_line)"

  # ---- 3b the component set is named, not "any label without a colon"
  reset
  {
    item k-inbox '["class:pr-review","aae-orc","inbox"]'
    item k-gap '["class:pr-review","aae-orc","fleet-gap"]'
    item k-unknown '["class:pr-review","aae-orc","not-a-listed-thing"]'
    item k-comp '["class:pr-review","aae-orc","compa"]'
    item k-upper '["class:pr-review","aae-orc","ThreeX"]'
    item k-lower '["class:pr-review","aae-orc","threex"]'
    item k-orc '["class:pr-review","aae-orc"]'
    item k-scope '["class:pr-review","aae-orc","scope:cross-cutting"]'
  } | set_items
  runis "$B"
  cl="$(count_line)"
  [[ "$cl" == *"eligible 2"* ]] && ok "3b only the two items with a named component are eligible" || bad "3b eligible" "$cl"
  for id in k-inbox k-gap k-unknown k-lower k-orc k-scope; do
    [[ "$(jq -r --arg id "$id" 'select(.id == $id) | .missing | join(",")' "$st/unmatched.jsonl")" == component ]] \
      && ok "3b $id reads no component" || bad "3b $id" "$(cat "$st/unmatched.jsonl")"
  done
  [[ -z "$(jq -r 'select(.id == "k-comp" or .id == "k-upper")' "$st/unmatched.jsonl")" ]] && ok "3b a listed component, in its own case, is a component" || bad "3b listed"

  # ---- 3c no readable convention: a GAP, never "any label"
  reset
  { item g-1 '["class:pr-review","marvel"]'; item g-2 '["marvel"]'; } | set_items
  runis "$B" IDLE_SHADOW_LABEL_CONVENTION=
  cl="$(count_line)"
  [[ "$rc" == 0 ]] && has "- GAP components: " && [[ "$cl" == *"eligible GAP"* && "$cl" == *"no component GAP"* ]] \
    && ok "3c no convention source gives a GAP, and eligible and no-component read GAP" || bad "3c unset" "$(out)"
  [[ "$cl" == *"no class 1"* ]] && ok "3c the class reading still stands without the set" || bad "3c class" "$cl"
  runis "$B" IDLE_SHADOW_LABEL_CONVENTION="$root/does-not-exist.md"
  [[ "$rc" == 0 ]] && has "- GAP components: " && [[ "$(count_line)" == *"eligible GAP"* ]] && ok "3c a missing file is a GAP" || bad "3c missing" "$(out)"
  printf 'no table here\n' >"$FIX/empty-conv.md"
  runis "$B" IDLE_SHADOW_LABEL_CONVENTION="$FIX/empty-conv.md"
  [[ "$rc" == 0 ]] && has "- GAP components: " && [[ "$(count_line)" == *"eligible GAP"* ]] && ok "3c a file with no Component row is a GAP" || bad "3c norow" "$(out)"
  # the default source: the convention file in the checkout the run starts in
  mkdir -p "$root/repo2/.claude/skills/task-workflow"
  ( cd "$root/repo2" && git init -q . ) >/dev/null 2>&1 || mkdir -p "$root/repo2/.git"
  cp "$FIX/convention.md" "$root/repo2/.claude/skills/task-workflow/SKILL.md"
  rc=0
  ( cd "$root/repo2" && env -i HOME="$root/home" PATH="$root/bin:$PATH" LANG=C TMPDIR="$root" DIRECTOR_STATE="$root/state" IS_FIX="$FIX" \
      IDLE_SHADOW_TEST_CLOCK=1 IDLE_SHADOW_NOW="$NOW" IDLE_SHADOW_CALL_TIMEOUT=3 "$B" "$IS" ) >"$root/out" 2>"$root/err" || rc=$?
  [[ "$rc" == 0 && "$(count_line)" == *"eligible 1"* ]] && ! has "- GAP components" && ok "3c with no variable set the run reads the convention file of the checkout it starts in" || bad "3c default" "$(out)"
  rm -rf "$root/repo2"

  # ---- 4 client items are a count only
  reset
  { cat "$FIX/items.json" | jq -c '.[]'; client_item; } | jq -s '.' >"$FIX/items.all.json"
  { client_item; client_item; } | jq -s '.' >"$FIX/client.json"
  runis "$B"
  cl="$(count_line)"
  [[ "$cl" == *"ready 7 (client 2, counted only)"* ]] && ok "4 client items are counted: 5 plus 2 ready" || bad "4 client count" "$cl"
  leak=0
  for f in "$root/out" "$root/err"; do
    if /usr/bin/grep -q -F -e "$CLIENT_ID" -e "$CLIENT_TITLE" -e "zz-client" "$f"; then leak=1; fi
  done
  if /usr/bin/grep -r -q -F -e "$CLIENT_ID" -e "$CLIENT_TITLE" "$root/state"; then leak=1; fi
  [[ "$leak" == 0 ]] && ok "4 no part of a client id or title reaches the output or any file" || bad "4 client leak"
  /usr/bin/grep -q -F -- "--exclude-label scope:client" "$FIX/bd.log" 2>/dev/null && ok "4 the item read excludes the client label at bd" || bad "4 exclude flag" "$(cat "$FIX/bd.log")"
  touch "$FIX/bd.ignoreexclude"
  { cat "$FIX/items.json" | jq -c '.[]'; client_item; } | jq -s '.' >"$FIX/items.all.json"
  runis "$B"
  leak=0
  if /usr/bin/grep -q -F -e "$CLIENT_ID" -e "$CLIENT_TITLE" "$root/out"; then leak=1; fi
  if /usr/bin/grep -r -q -F -e "$CLIENT_ID" -e "$CLIENT_TITLE" "$root/state"; then leak=1; fi
  [[ "$leak" == 0 && "$(count_line)" == *"ready 7 (client 2, counted only)"* ]] \
    && ok "4 a bd that ignores the exclude flag still leaks no client item, and none is counted twice" || bad "4 ignored exclude" "$(count_line)"
  rm -f "$FIX/bd.ignoreexclude" "$FIX/bd.log"
  : >"$FIX/bd.clientfail"; touch "$FIX/bd.clientfail"
  runis "$B"
  [[ "$rc" == 0 && "$(count_line)" == *"client GAP"* ]] && has "- GAP client: " && ok "4 a client count that cannot be read is a GAP, not 0" || bad "4 client GAP" "$(out)"

  # ---- 5 a seat that is not free never counts as free
  reset
  { seat alpha crew builder b1 unknown; seat alpha crew builder b2 waiting; seat alpha crew builder b3 busy; } >"$root/state/freebusy/seats.jsonl"
  { item e-1 '["class:story-implementation","marvel"]'; } | set_items
  runis "$B"
  cl="$(count_line)"
  [[ "$cl" == *"seats: 0 free, 1 busy, 1 waiting, 1 unknown"* ]] && ok "5 unknown and waiting are never coerced to free" || bad "5 strict free" "$cl"
  [[ ! -s "$st/would-pair.jsonl" ]] && ok "5 a seat that reads unknown never appears in a would-pair row" || bad "5 would-pair row" "$(cat "$st/would-pair.jsonl")"

  # ---- 6 seat reads that fail are GAPs
  reset; rm -f "$root/state/freebusy/seats.jsonl"
  runis "$B"
  [[ "$rc" == 0 ]] && has "- GAP seats: " && [[ "$(count_line)" == *"seats: GAP"* ]] && ok "6 no seats file is a GAP, exit 0" || bad "6 no seats" "$(out)"
  reset; : >"$root/state/freebusy/seats.jsonl"
  runis "$B"
  [[ "$rc" == 0 ]] && has "- GAP seats: " && ok "6 an empty seats file is a GAP" || bad "6 empty seats" "$(out)"
  reset; echo "not json" >"$root/state/freebusy/seats.jsonl"
  runis "$B"
  [[ "$rc" == 0 ]] && has "- GAP seats: " && ok "6 an unparseable seats file is a GAP" || bad "6 junk seats" "$(out)"
  reset
  NOW_OLD="$(jq -nr '"2026-10-21T10:00:00Z" | fromdateiso8601')"
  runis "$B" IDLE_SHADOW_NOW="$NOW_OLD"
  [[ "$rc" == 0 ]] && has "- GAP seats: freebusy seats are" && [[ "$(count_line)" == *"seats: 0 free, 0 busy, 0 waiting, 4 unknown"* ]] \
    && ok "6 an old seats file is a GAP and every seat reads unknown" || bad "6 old seats" "$(out)"

  # ---- 7 bd failures are GAPs and the run still ends 0
  reset
  runis "$B"
  [[ -f "$st/unmatched.jsonl" ]] || bad "7 setup: a good run left no unmatched list"
  touch "$FIX/bd.fail"
  runis "$B"
  [[ ! -e "$st/unmatched.jsonl" ]] && ok "7 a failed item read leaves no old unmatched list to be mistaken for this run's" || bad "7 stale unmatched list"
  reset; touch "$FIX/bd.fail"
  runis "$B"
  [[ "$rc" == 0 ]] && has "- GAP items: bd ready failed (Error: dolt server unreachable)" && [[ "$(count_line)" == *"ready GAP"* ]] \
    && ok "7 bd down gives one GAP with its first stderr line, and ready is GAP, never 0" || bad "7 bd down" "$(out)"
  ! has "second stderr line" && ok "7 only the first stderr line is shown" || bad "7 stderr"
  reset; touch "$FIX/bd.nojson"
  runis "$B"
  [[ "$rc" == 0 ]] && has "- GAP items: " && ok "7 bd that answers with no JSON is a GAP" || bad "7 nojson" "$(out)"
  reset; echo 30 >"$FIX/bd.slow"
  s0=$SECONDS
  runis "$B"
  el=$((SECONDS - s0))
  [[ "$rc" == 0 && "$el" -lt 20 ]] && has "- GAP items: bd ready failed (no answer within 3s)" && ok "7 a hung bd is stopped at the bound, took ${el}s" || bad "7 hung bd" "${el}s $(out)"
  alive=0
  if [[ -f "$FIX/slow.pids" ]]; then
    while read -r p; do if kill -0 "$p" 2>/dev/null; then alive=1; fi; done <"$FIX/slow.pids"
  fi
  [[ "$alive" == 0 ]] && ok "7 the hung call's process is gone" || bad "7 survivor"

  # ---- 8 no send path: stubs and grep
  reset
  runis "$B"
  [[ ! -e "$FIX/forbidden.log" ]] && ok "8 no bd write, marvel, director-mcp, gh or ssh call was made" || bad "8 forbidden" "$(cat "$FIX/forbidden.log")"
  [[ -z "$(/usr/bin/grep -v '^ready ' "$FIX/bd.log" 2>/dev/null || true)" ]] && ok "8 every bd call was a ready read" || bad "8 bd calls" "$(cat "$FIX/bd.log")"
  /usr/bin/grep -q -F -- "--json" "$FIX/bd.log" 2>/dev/null && ok "8 bd was asked for JSON" || bad "8 json"

  # ---- 10 a state directory that cannot be written is one GAP line and exit 0
  if [[ "$(id -u)" != 0 ]]; then
    reset
    mkdir -p "$root/ro-parent"; chmod 555 "$root/ro-parent"
    runis "$B" DIRECTOR_STATE="$root/ro-parent/state"
    [[ "$rc" == 0 && "$(wc -l <"$root/out" | tr -d ' ')" == 1 ]] && /usr/bin/grep -q '^- GAP state: ' "$root/out" \
      && ok "10 a state directory that cannot be created is one GAP line and exit 0" || bad "10 mkdir fails" "rc=$rc $(out)"
    chmod 755 "$root/ro-parent"; rm -rf "$root/ro-parent"
    : >"$root/afile"
    runis "$B" DIRECTOR_STATE="$root/afile"
    [[ "$rc" == 0 && "$(wc -l <"$root/out" | tr -d ' ')" == 1 ]] && /usr/bin/grep -q '^- GAP state: ' "$root/out" \
      && ok "10 a state path that is a file is one GAP line and exit 0" || bad "10 state is a file" "rc=$rc $(out)"
    ! has "idle-shadow: ready" && ok "10 no count line is printed from a run that could not write" || bad "10 count line" "$(out)"
  else
    echo "SKIP 10 unwritable state cases: running as root"
  fi

  marry_cases "$B" stub
done

# the same cases against the real verb, when a marvel binary built from 493909ec is given
if [[ -n "$REAL" ]]; then
  echo "== real marvel marry ($REAL)"
  [[ -x "$REAL" ]] || bad "R0 IS_REAL_MARVEL is not an executable"
  marry_cases /bin/bash real
  # R1 the shared vectors: the real verb gives each marry step's want, after decoding
  r_n=0; r_bad=0
  while IFS= read -r stepjson; do
    r_n=$((r_n+1))
    got="$(printf '%s' "$stepjson" | jq -c '.input' | "$REAL" marry --input - 2>/dev/null || echo FAIL)"
    same="$(jq -n --argjson g "${got:-null}" --argjson w "$(printf '%s' "$stepjson" | jq -c '.want')" '
      def strip: walk(if type == "object" then with_entries(select(.value != null and .value != [] and .value != {} and .value != 0 and .value != false and .value != "")) else . end);
      ($g | strip) == ($w | strip)' 2>/dev/null || echo false)"
    [[ "$same" == true ]] || { r_bad=$((r_bad+1)); echo "  vector step $r_n differs"; }
  done < <(jq -c '.vectors[].steps[] | select(.op == "marry" and has("want"))' "$vec")
  [[ "$r_n" -ge 20 && "$r_bad" == 0 ]] && ok "R1 the real marry verb gives the shared vectors' answers ($r_n marry steps)" || bad "R1 vectors" "$r_n steps, $r_bad differ"
else
  echo "SKIP real marry verb cases: set IS_REAL_MARVEL to a marvel built from marvel 493909ec"
fi

# --- the script's own text: no send, inject, bd write or gh call -------------
code="$(/usr/bin/grep -v '^[[:space:]]*#' "$IS" 2>/dev/null || true)"
hits="$(printf '%s\n' "$code" | /usr/bin/grep -n -E 'director-mcp|marvel inject|marvel send|broadcast|(^|[^a-z_-])gh (api|pr|issue)|bd (create|update|close|claim|remember|dep|label|comment|note|set)|NATS|nats |ssh |curl' || true)"
[[ -z "$hits" ]] && ok "P1 the script names no send, inject, bd write, gh call, ssh or bus" || bad "P1 send path" "$hits"
n_bd="$(printf '%s\n' "$code" | /usr/bin/grep -c -E '(^|[^a-zA-Z_])bd ' || true)"
[[ "$(printf '%s\n' "$code" | /usr/bin/grep -E '(^|[^a-zA-Z_])\$?\{?bd\}? ' | /usr/bin/grep -v -E 'ready' | /usr/bin/grep -c . || true)" == 0 ]] \
  && ok "P2 every bd invocation in the script is a ready read" || bad "P2 bd verbs" "$n_bd"
[[ "$(printf '%s\n' "$code" | /usr/bin/grep -c -E '^marry_step\(\)' || true)" == 1 ]] \
  && ok "P3 the marry step is one function" || bad "P3 marry function"
# marvel is called only as `marry --input`, and only from the marry step
mcalls="$(printf '%s\n' "$code" | /usr/bin/grep -n -E '"?\$\{?marvel_bin\}?"? ' | /usr/bin/grep -v -E 'marry --input' || true)"
[[ -z "$mcalls" && "$(printf '%s\n' "$code" | /usr/bin/grep -c -E '"\$marvel_bin" marry --input' || true)" -ge 1 ]] \
  && ok "P5 every marvel call in the script is marry --input" || bad "P5 marvel calls" "$mcalls"
# nothing outside marry_step produces a would-pair row: no append, no jq, no echo to the file
outside="$(printf '%s\n' "$code" | awk '/^marry_step\(\)/{f=1} f&&/^}/{f=0; next} !f' | /usr/bin/grep 'would-pair.jsonl' | /usr/bin/grep -c -E '>>|jq|echo|printf' || true)"
[[ "$outside" == 0 ]] && ok "P4 no row is produced outside the marry step" || bad "P4 would-pair writes" "$outside"

# the header says what the timeout bounds
/usr/bin/grep -q 'IDLE_SHADOW_CALL_TIMEOUT (seconds for each bd read and for the marry call' "$IS" \
  && ok "D1 the header says the timeout bounds the marry call as well as each bd read" || bad "D1 timeout doc"
/usr/bin/grep -q '^# IDLE_SHADOW_KILL_GRACE (seconds a call that ignores TERM' "$IS" && ok "D2 the header names the kill grace" || bad "D2 kill grace doc"

# --- state files, modes, checkout refusal -------------------------------------
reset
runis /bin/bash
[[ "$(mode_of "$st")" == 700 ]] && ok "S1 the directory is mode 700" || bad "S1 dir mode"
m_ok=1
for f in unmatched.jsonl would-pair.jsonl flow.jsonl; do
  [[ "$(mode_of "$st/$f")" == 600 ]] || m_ok=0
done
[[ "$m_ok" == 1 ]] && ok "S2 each state file is mode 600" || bad "S2 file modes"
[[ "$(find "$st" -mindepth 1 | wc -l | tr -d ' ')" == 3 ]] && ok "S3 nothing else is left in the directory" || bad "S3 leftovers" "$(find "$st")"
runis /bin/bash
[[ "$(wc -l <"$st/flow.jsonl" | tr -d ' ')" == 2 ]] && ok "S4 flow.jsonl gains one line per run" || bad "S4 flow"
jq -e '.ready == 5 and .client == 0 and .would_pair == 0 and .unmatched.no_class == 5' "$st/flow.jsonl" >/dev/null && ok "S5 the flow line carries the counts" || bad "S5 flow fields" "$(cat "$st/flow.jsonl")"
h1="$(shasum -a 256 "$root/state/freebusy/seats.jsonl")"; runis /bin/bash; h2="$(shasum -a 256 "$root/state/freebusy/seats.jsonl")"
[[ "$h1" == "$h2" ]] && ok "S6 freebusy's seats file is read, never changed" || bad "S6 seats changed"
snap() { find "$root" \( -type f -o -type d \) ! -path "$root/state" ! -path "$root/state/*" ! -path "$root/fix" ! -path "$root/fix/*" ! -path "$root/out" ! -path "$root/err" | LC_ALL=C sort | shasum -a 256; }
s1="$(snap)"; runis /bin/bash; s2="$(snap)"
[[ "$s1" == "$s2" ]] && ok "S7 nothing is written outside the state directory" || bad "S7 a file was written outside the state directory"

reset; mkdir -p "$root/repo/.git"; mv "$root/state" "$root/repo/state"; mkdir -p "$root/state"
sed_state="$root/repo/state"
rc=0
( cd "$root/work" && env -i HOME="$root/home" PATH="$root/bin:$PATH" LANG=C TMPDIR="$root" DIRECTOR_STATE="$sed_state" IS_FIX="$FIX" /bin/bash "$IS" ) >"$root/out" 2>"$root/err" || rc=$?
[[ "$rc" == 0 ]] && has "not run: " && [[ ! -d "$sed_state/idle-shadow" ]] && ok "S8 a state directory inside a checkout is refused and nothing is written" || bad "S8 checkout" "$(out)"
rm -rf "$root/repo"

# --- a long group is capped on the screen and whole in the file ---------------
reset
for n in $(seq 1 25); do item "m-$(printf %02d "$n")" '["marvel"]'; done | set_items
runis /bin/bash
has "- unmatched no class (25): m-01 m-02 m-03 m-04 m-05 m-06 m-07 m-08 m-09 m-10 and 15 more" && ok "L1 the screen lists ten ids and counts the rest" || bad "L1 cap" "$(out)"
[[ "$(wc -l <"$st/unmatched.jsonl" | tr -d ' ')" == 25 ]] && ok "L2 the file holds all 25" || bad "L2 file"

# --- the first-line helper: ids come from labels only, titles never printed --------
reset
runis /bin/bash
! has "title of" && ok "T1 no item title is printed" || bad "T1 title printed"
! /usr/bin/grep -r -q "title of" "$root/state" && ok "T2 no item title is written" || bad "T2 title written"

echo "$pass passed, $fail failed"
[[ "$fail" == 0 ]]
