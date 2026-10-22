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
occ() { { grep -o -F -- "$1" "$2" || true; } | wc -l | tr -d ' '; }  # occurrences, not lines
modeof() { stat -c '%a' "$1" 2>/dev/null || stat -f '%Lp' "$1"; }  # GNU first: GNU stat accepts -f too and prints a report

root="$(mktemp -d "${TMPDIR:-/tmp}/ll.XXXXXX")"  # macOS mktemp -d alone ignores TMPDIR
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
has "no inputs: lanes GAP line" "- GAP lanes: lanes.jsonl not found" "$root/log"
has "no inputs: desk GAP line" "- GAP desk: desk-snapshot.json not found" "$root/log"
has "no inputs: marry GAP line" "- GAP marry: marry.json not found" "$root/log"
hasnt "no inputs: no 0 of 0 for a source never read" "0 of 0" "$out"
hasnt "no inputs: no free 0 for a source never read" "free 0" "$out"
hasnt "no inputs: no (none now) for a source never read" "(none now)" "$out"
has "no inputs: seats by state is a GAP cell" "seats by state</span><span>GAP" "$out"
has "no inputs: desk is a GAP cell" "Desk GAP" "$out"
has "no inputs: ready work is a GAP" "Ready work: GAP" "$out"

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
{"pairs":[{"item":"aae-orc-zz1","seat":"agent://arcaven/arcaven-builder-g1-0","lease":"L1"}],"unmatched":[{"item":"aae-orc-zz2","kind":"no-class"}],"count":{"ready":3,"eligible":1,"unmatched":1,"no_class":1,"no_component":0,"no_eligible_seat":0,"operator_only":0,"client":1,"free":1,"would_pair":1}}
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

# --- withheld means masked, for every field of the lane ------------------------
# The reviewer's fixture: a withhold word in a withheld team's role and reason. It must appear 0 times in
# the page, the embedded JSON included; an unreadable withhold list withholds every name.
cp "$fb/seats.jsonl" "$root/seats.keep"
printf 'zzhold\n' >"$DIRECTOR_STATE/dash-withhold.txt"
cat >"$fb/seats.jsonl" <<'J'
{"at":"2026-10-22T01:04:00Z","cluster":"kinu","workspace":"wws","team":"wteam","role":"build-zzhold","name":"wteam-build-g1-0","state":"waiting","reason":"parked zzhold","last_active":"3m","active":null,"last_turn":null}
{"at":"2026-10-22T01:04:00Z","cluster":"kinu","workspace":"aae","team":"arcaven","role":"builder","name":"arcaven-builder-g1-0","state":"free","reason":"idle zzhold","last_active":"1m","active":"0","last_turn":null}
J
"$LL" --out "$out" >/dev/null 2>&1
if [[ "$(occ zzhold "$out")" == 0 ]]; then ok "withheld lane: a withhold word in role and reason shows 0 times (page and JSON)"
else bad "withheld lane: a withhold word in role and reason shows 0 times" "$(occ zzhold "$out") times"; fi
has "withheld lane: still counted by cluster and counts" "team withheld" "$out"
rm -f "$DIRECTOR_STATE/dash-withhold.txt"; mkdir "$DIRECTOR_STATE/dash-withhold.txt"
cat >"$fb/seats.jsonl" <<'J'
{"at":"2026-10-22T01:04:00Z","cluster":"kinu","workspace":"wws","team":"wteam","role":"zzrolew","name":"wteam-x-g1-0","state":"waiting","reason":"zzreasonw","last_active":"3m","active":null,"last_turn":null}
J
"$LL" --out "$out" >"$root/log" 2>&1
if [[ "$(occ zzrolew "$out")" == 0 && "$(occ zzreasonw "$out")" == 0 ]]; then ok "unreadable withhold list: role and reason show 0 times (page and JSON)"
else bad "unreadable withhold list: role and reason show 0 times" "role $(occ zzrolew "$out"), reason $(occ zzreasonw "$out")"; fi
has "unreadable withhold list: GAP line" "dash-withhold.txt unreadable; every name withheld" "$root/log"
rmdir "$DIRECTOR_STATE/dash-withhold.txt"; cp "$root/seats.keep" "$fb/seats.jsonl"

# --- a source that is missing or short prints a GAP cell and a GAP line, never a 0 --
cp "$fb/marry.json" "$root/marry.keep"; cp "$DIRECTOR_STATE/desk-snapshot.json" "$root/desk.keep"
printf '{"pairs":[]}\n' >"$fb/marry.json"
"$LL" --out "$out" >"$root/log" 2>&1
has "marry with no count object: ready work is a GAP" "Ready work: GAP" "$out"
hasnt "marry with no count object: no would-pair 0" "would-pair 0" "$out"
has "marry with no count object: GAP line" "- GAP marry: marry.json has no count object" "$root/log"
printf '{"pairs":[],"count":{"ready":3,"eligible":1,"unmatched":1,"no_class":1,"no_component":0,"no_eligible_seat":0,"operator_only":0,"would_pair":1}}\n' >"$fb/marry.json"
"$LL" --out "$out" >"$root/log" 2>&1
has "marry count lacking client: that cell is a GAP" "client GAP" "$out"
has "marry count lacking client: GAP line names it" "- GAP marry: count lacks client" "$root/log"
printf '{"pairs":[],"count":{}}\n' >"$fb/marry.json"
"$LL" --out "$out" >"$root/log" 2>&1
hasnt "marry with an empty count: no ready 0" "ready 0" "$out"
cp "$root/marry.keep" "$fb/marry.json"
printf '{"read_at":"2026-10-22T00:55:00Z","open":["a"]}\n' >"$DIRECTOR_STATE/desk-snapshot.json"
"$LL" --out "$out" >"$root/log" 2>&1
hasnt "desk with no by_status: no yours 0" "yours 0" "$out"
has "desk with no by_status: yours is a GAP" "yours GAP" "$out"
has "desk with no by_status: GAP line" "- GAP desk: by_status.yours missing" "$root/log"
printf '{"by_status":{"yours":1}}\n' >"$DIRECTOR_STATE/desk-snapshot.json"
"$LL" --out "$out" >"$root/log" 2>&1
has "desk with no open list: GAP line" "- GAP desk: open list missing" "$root/log"
cp "$root/desk.keep" "$DIRECTOR_STATE/desk-snapshot.json"
: >"$fb/seats.jsonl"
"$LL" --out "$out" >"$root/log" 2>&1
hasnt "seats file with no rows: no 0 of 0" "0 of 0" "$out"
hasnt "seats file with no rows: no (none now)" "(none now)" "$out"
has "seats file with no rows: GAP line" "- GAP seats: seats.jsonl has no rows" "$root/log"
cp "$root/seats.keep" "$fb/seats.jsonl"
mv "$fb/seats.jsonl" "$root/seats.moved"; mkdir "$fb/seats.jsonl"
"$LL" --out "$out" >"$root/log" 2>&1
hasnt "unreadable seats: no 0 of 0" "0 of 0" "$out"
has "unreadable seats: GAP line" "- GAP seats: seats.jsonl unreadable" "$root/log"
rmdir "$fb/seats.jsonl"; mv "$root/seats.moved" "$fb/seats.jsonl"
rc=0; "$LL" --out "$out" >/dev/null 2>&1 || rc=$?
if [[ "$rc" == 0 ]]; then ok "a GAP stays exit 0 (a diagnostic, not a gate)"; else bad "a GAP stays exit 0" "rc=$rc"; fi

# --- closes the surviving mutants ----------------------------------------------
# a missing or unreadable JSON source is a GAP, never read as {}
rm -f "$fb/marry.json" "$DIRECTOR_STATE/desk-snapshot.json"
"$LL" --out "$out" >"$root/log" 2>&1
has "missing desk and marry: both GAP lines" "- GAP desk: desk-snapshot.json not found" "$root/log"
has "missing desk and marry: marry GAP line" "- GAP marry: marry.json not found" "$root/log"
mkdir "$fb/marry.json"; printf '[1]\n' >"$DIRECTOR_STATE/desk-snapshot.json"
"$LL" --out "$out" >"$root/log" 2>&1
has "unreadable marry: GAP line" "- GAP marry: marry.json unreadable" "$root/log"
has "desk that is not an object: GAP line" "- GAP desk: desk-snapshot.json is not an object" "$root/log"
rmdir "$fb/marry.json"; cp "$root/marry.keep" "$fb/marry.json"; cp "$root/desk.keep" "$DIRECTOR_STATE/desk-snapshot.json"

# nothing is written outside the output path, and the output directory holds only the page
odir="$root/wout"; rm -rf "$odir"; mkdir -p "$odir"
snap() { { find "$root" -path "$odir" -prune -o -type d -print; find "$root" -path "$odir" -prune -o -type f -exec cksum {} +; } | LC_ALL=C sort; }
# a fresh state directory built only from the known inputs, so a stray file from an earlier run cannot hide
st2="$root/state2"; rm -rf "$st2"; mkdir -p "$st2/freebusy" "$root/home"
cp "$fb/seats.jsonl" "$fb/lanes.jsonl" "$fb/marry.json" "$st2/freebusy/"
cp "$DIRECTOR_STATE/desk-snapshot.json" "$DIRECTOR_STATE/lane-ledger-show.txt" "$st2/"
before="$(snap)"
# Apple's system python3 caches stdlib bytecode under $HOME/Library/Caches; this check is about the script's own writes
PYTHONDONTWRITEBYTECODE=1 HOME="$root/home" DIRECTOR_STATE="$st2" "$LL" --out "$odir/c1.html" >/dev/null 2>&1
after="$(snap)"
if [[ "$before" == "$after" ]]; then ok "a run writes nothing outside its output directory (inputs, state and HOME unchanged)"
else bad "a run writes nothing outside its output directory" "$(diff <(echo "$before") <(echo "$after") | head -3)"; fi
if [[ "$(ls -A "$odir")" == "c1.html" ]]; then ok "the output directory holds only the page (no temp file left)"; else bad "the output directory holds only the page" "$(ls -A "$odir")"; fi

# the page is replaced atomically from a temp file in its own directory
i1="$(ls -i "$odir/c1.html" | cut -d' ' -f1)"
printf 'old page\n' >"$odir/c1.html"; i2="$(ls -i "$odir/c1.html" | cut -d' ' -f1)"
"$LL" --out "$odir/c1.html" >/dev/null 2>&1
i3="$(ls -i "$odir/c1.html" | cut -d' ' -f1)"
if [[ "$i3" != "$i2" ]]; then ok "an existing page is replaced by a new file (os.replace), not rewritten in place"; else bad "an existing page is replaced by a new file" "same inode $i2"; fi
# a directory that refuses a new file leaves the old page untouched: an in-place write would still succeed
printf 'old page\n' >"$odir/c1.html"; chmod 644 "$odir/c1.html"; chmod 555 "$odir"
rc=0; "$LL" --out "$odir/c1.html" >/dev/null 2>&1 || rc=$?
chmod 755 "$odir"
if [[ "$rc" != 0 && "$(cat "$odir/c1.html")" == "old page" ]]; then ok "an unwritable output directory fails and leaves the old page unchanged"
else bad "an unwritable output directory fails and leaves the old page unchanged" "rc=$rc page=$(head -c 40 "$odir/c1.html")"; fi
rm -rf "$odir"

# the mode check reads GNU stat first: GNU stat accepts -f (filesystem status) and exits 0
fk="$root/fakegnu"; mkdir -p "$fk"
printf '#!/bin/sh\ncase "$1" in -c) echo 600;; -f) printf "  File: x\\n    ID: 1 Namelen: 255\\n"; exit 0;; esac\n' >"$fk/stat"; chmod +x "$fk/stat"
if [[ "$(PATH="$fk:$PATH" modeof "$root/a.html")" == 600 ]]; then ok "mode check: GNU stat is tried first (stat -f would print a report and exit 0)"
else bad "mode check: GNU stat is tried first" "$(PATH="$fk:$PATH" modeof "$root/a.html" | head -2)"; fi
fb2="$root/fakebsd"; mkdir -p "$fb2"
printf '#!/bin/sh\ncase "$1" in -c) echo "stat: illegal option" >&2; exit 1;; -f) echo 600;; esac\n' >"$fb2/stat"; chmod +x "$fb2/stat"
if [[ "$(PATH="$fb2:$PATH" modeof "$root/a.html")" == 600 ]]; then ok "mode check: BSD stat is the fallback"; else bad "mode check: BSD stat is the fallback"; fi

# --- deterministic, ASCII, self-contained ------------------------------------
"$LL" --out "$root/a.html" >/dev/null 2>&1
"$LL" --out "$root/b.html" >/dev/null 2>&1
if cmp -s "$root/a.html" "$root/b.html"; then ok "same inputs, same bytes"; else bad "same inputs, same bytes"; fi
if LC_ALL=C grep -q '[^ -~]' <(tr -d '\n' <"$root/a.html"); then bad "ASCII only"; else ok "ASCII only"; fi
if grep -qE 'src=|https?://|<form|<button|<script(>| src)' "$root/a.html"; then bad "no external loads, forms or live scripts"
else ok "no external loads, forms or live scripts"; fi
mode="$(modeof "$root/a.html")"
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

# --- the default marry output is idle-shadow's ---------------------------------
# with no --marry, a marry.json only at idle-shadow/marry.json is the one read
st3="$root/state3"; rm -rf "$st3"; mkdir -p "$st3/idle-shadow"
printf '%s\n' '{"pairs":[{"item":"it-1","seat":"agent://teama/seat-a","lease":"L1"}],"count":{"ready":2,"eligible":1,"unmatched":1,"no_class":1,"no_component":0,"no_eligible_seat":0,"operator_only":0,"client":0,"free":1,"would_pair":1}}' >"$st3/idle-shadow/marry.json"
DIRECTOR_STATE="$st3" "$LL" --out "$root/c3.html" >"$root/log" 2>&1
hasnt "default marry path: ready work is read from idle-shadow/marry.json, not GAP" "Ready work: GAP" "$root/c3.html"
has "default marry path: the marry count line" "ready 2, eligible 1, would-pair 1" "$root/c3.html"
hasnt "default marry path: no marry GAP line" "- GAP marry" "$root/log"

# --- usage error -------------------------------------------------------------
rc=0; "$LL" --bogus >/dev/null 2>&1 || rc=$?
if [[ "$rc" == 2 ]]; then ok "usage error exits 2"; else bad "usage error exits 2" "rc=$rc"; fi

echo "lane-ledger: $pass passed, $fail failed"
[[ "$fail" == 0 ]]
