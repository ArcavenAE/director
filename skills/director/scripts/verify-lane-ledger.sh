#!/usr/bin/env bash
# Prove lane-ledger: C1 of the flow pages regenerates from freebusy's files, the Desk
# snapshot and a marvel marry output; withholds every team not on the show list; prints
# GAP with a fix for each missing source; and gives the same bytes for the same inputs.
#
# Usage: skills/director/scripts/verify-lane-ledger.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LL="${LL:-$here/lane-ledger}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }
has() { if grep -qF -- "$2" "$3"; then ok "$1"; else bad "$1" "missing: $2"; fi; }
hasnt() { if grep -qF -- "$2" "$3"; then bad "$1" "found: $2"; else ok "$1"; fi; }

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
export DIRECTOR_STATE="$root/state"
fb="$DIRECTOR_STATE/freebusy"
out="$root/out/c1.html"

# --- no inputs at all: a page of GAPs, exit 0 --------------------------------
mkdir -p "$DIRECTOR_STATE"
rc=0; "$LL" --out "$out" >"$root/log" 2>&1 || rc=$?
if [[ "$rc" == 0 && -f "$out" ]]; then ok "no inputs: exit 0 and a page"; else bad "no inputs: exit 0 and a page" "rc=$rc"; fi
has "no inputs: seats GAP names its fix" "freebusy writes freebusy/seats.jsonl" "$out"
has "no inputs: GAP lines printed" "- GAP seats: seats.jsonl not found" "$root/log"
has "no inputs: says nothing acts" "Nothing here acts" "$out"

# --- fixture: two clusters, one shown team, one secret team -------------------
mkdir -p "$fb"
cat >"$fb/seats.jsonl" <<'J'
{"at":"2026-10-22T01:04:00Z","cluster":"kinu","workspace":"aae","team":"arcaven","role":"builder","name":"arcaven-builder-g1-0","state":"free","reason":"idle","last_active":"40m","active":"0","last_turn":null}
{"at":"2026-10-22T01:04:00Z","cluster":"kinu","workspace":"aae","team":"arcaven","role":"builder","name":"arcaven-builder-g1-1","state":"busy","reason":"active","last_active":"1m","active":"12","last_turn":null}
{"at":"2026-10-22T01:04:00Z","cluster":"kinu","workspace":"zzsecretws","team":"zzsecretteam","role":"reviewer","name":"zzsecretteam-reviewer-g2-0","state":"waiting","reason":"parked","last_active":null,"active":null,"last_turn":null}
{"at":"2026-10-22T01:04:00Z","cluster":"mokuzai","workspace":"aae","team":"reviewer","role":"reviewer","name":"reviewer-codex-g3-0","state":"unknown","reason":"cluster not read","last_active":null,"active":null,"last_turn":null}
J
cat >"$fb/lanes.jsonl" <<'J'
{"at":"2026-10-22T01:04:00Z","lane":"arcaven/builder","state":"busy","seats":{"free":1,"busy":1,"waiting":0,"unknown":0},"asks_open":2,"asks_unacked":1,"asks_oldest_s":300}
{"at":"2026-10-22T01:04:00Z","lane":"zzsecretteam/reviewer","state":"waiting","seats":{"free":0,"busy":0,"waiting":1,"unknown":0},"asks_open":0,"asks_unacked":0,"asks_oldest_s":0}
{"at":"2026-10-22T01:04:00Z","lane":"reviewer/reviewer","state":"unknown","seats":{"free":0,"busy":0,"waiting":0,"unknown":1},"asks_open":"GAP","asks_unacked":"GAP","asks_oldest_s":"GAP"}
J
cat >"$DIRECTOR_STATE/desk-snapshot.json" <<'J'
{"read_at":"2026-10-22T00:55:00Z","cards":5,"open":["a","b"],"by_status":{"open":2,"yours":3},"areas":[],"ids":{},"source_sha256":"x"}
J
cat >"$fb/marry.json" <<'J'
{"pairs":[{"item":"aae-orc-zz1","seat":"agent://arcaven/arcaven-builder-g1-0","lease":"L1"}],"unmatched":[{"item":"aae-orc-zz2","kind":"no-class"}],"count":{"ready":3,"eligible":1,"unmatched":1,"no_class":1,"client":1,"free":1,"would_pair":1}}
J
printf '# teams the page may name\naae/arcaven\naae/reviewer\n' >"$DIRECTOR_STATE/lane-ledger-show.txt"

"$LL" --out "$out" >"$root/log" 2>&1
has "fixture: shown team named" "arcaven builder" "$out"
has "fixture: second cluster read" "mokuzai" "$out"
hasnt "fixture: unlisted team never named" "zzsecretteam" "$out"
hasnt "fixture: unlisted workspace never named" "zzsecretws" "$out"
has "fixture: unlisted team prints withheld" "team withheld reviewer" "$out"
has "fixture: measured count" "3 of 4" "$out"
has "fixture: asks open from lanes" '<td class="n" data-h="asks open">2</td>' "$out"
has "fixture: asks GAP from lanes" "the asks rollup from director-mcp" "$out"
has "fixture: ready for the lane from marry" '<td class="n" data-h="ready">1</td>' "$out"
has "fixture: marry count line" "would-pair 1" "$out"
hasnt "fixture: no marry item id on the page" "aae-orc-zz1" "$out"
has "fixture: desk count" "Desk open 2, yours 3" "$out"
has "fixture: last turn GAP with fix" "LAST-TURN column" "$out"
has "fixture: unread GAP with fix" "consumer pending per seat (S11)" "$out"
has "fixture: codex/opencode/crush GAP with fix" "codex, opencode and crush" "$out"
has "fixture: team-to-host GAP with fix" "team-to-host map" "$out"

# --- the withhold list wins over the show list -------------------------------
printf 'arcaven\n' >"$DIRECTOR_STATE/dash-withhold.txt"
"$LL" --out "$out" >/dev/null 2>&1
hasnt "withhold: a listed team holding a withhold word is not named" "arcaven" "$out"
rm -f "$DIRECTOR_STATE/dash-withhold.txt"

# --- deterministic, ASCII, self-contained ------------------------------------
"$LL" --out "$root/a.html" >/dev/null 2>&1
"$LL" --out "$root/b.html" >/dev/null 2>&1
if cmp -s "$root/a.html" "$root/b.html"; then ok "same inputs, same bytes"; else bad "same inputs, same bytes"; fi
if LC_ALL=C grep -q '[^ -~]' <(tr -d '\n' <"$root/a.html"); then bad "ASCII only"; else ok "ASCII only"; fi
if grep -qE 'src=|https?://|<form|<button|<script(>| src)' "$root/a.html"; then bad "no external loads, forms or live scripts"
else ok "no external loads, forms or live scripts"; fi
mode="$(stat -c '%a' "$root/a.html" 2>/dev/null || stat -f '%Lp' "$root/a.html")"
if [[ "$mode" == 600 ]]; then ok "page is mode 600"; else bad "page is mode 600" "$mode"; fi

# --- a torn seats file is not used -------------------------------------------
printf '{"cluster":"kinu",' >>"$fb/seats.jsonl"
"$LL" --out "$out" >"$root/log" 2>&1
has "torn seats: GAP, file not used" "is not JSON; the file is not used" "$root/log"
hasnt "torn seats: no partial rows" "arcaven builder" "$out"

# --- refuses to write inside a checkout --------------------------------------
mkdir -p "$root/repo/.git"
"$LL" --out "$root/repo/c1.html" >"$root/log" 2>&1
if [[ ! -e "$root/repo/c1.html" ]] && grep -q "inside a checkout" "$root/log"; then ok "refuses inside a checkout"
else bad "refuses inside a checkout"; fi

# --- usage error -------------------------------------------------------------
rc=0; "$LL" --bogus >/dev/null 2>&1 || rc=$?
if [[ "$rc" == 2 ]]; then ok "usage error exits 2"; else bad "usage error exits 2" "rc=$rc"; fi

echo "lane-ledger: $pass passed, $fail failed"
[[ "$fail" == 0 ]]
