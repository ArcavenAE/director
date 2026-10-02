#!/usr/bin/env bash
# Prove dws, the workstream ledger CLI, keeps its W1 contract
# (sim/design/board-workstream-ledger.md, sections 2, 3, 6 and 11 items 1-2):
# instance ids refused in owner, blocked on and next action; a stale move
# refused with nothing appended; concurrent appends all land whole; a torn
# last line skipped and counted, never repaired; rows sorted by stage, oldest
# last-moved first, parked last; the writer allowlist over --actor.
#
# Usage: skills/director/scripts/verify-dws.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DWS="${DWS:-$here/dws}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
export DIRECTOR_STATE="$root/state"
mkdir -p "$DIRECTOR_STATE"
LEDGER="$DIRECTOR_STATE/workstreams.jsonl"
lines() { if [[ -f "$LEDGER" ]]; then wc -l <"$LEDGER" | tr -d ' '; else echo 0; fi; }

# --- instance-id refusal (section 2), on open and on set -------------------
"$DWS" open base --title "Base row" --owner "team-a/architect" >/dev/null 2>&1 \
  && ok "open accepts a role owner" || bad "open accepts a role owner"

ids=(
  'x-g9-9' 'x-g9-9 ' 'x-g9-9 (acting)' 'x-g9-9/' 'X-G9-9'
  $'x-g9\xe2\x80\x909' $'x-g9\xe2\x80\x939' $'x-g9\xe2\x88\x929'
  $'x-g\xef\xbc\x99-\xef\xbc\x99'
  $'x-g9\t-9' $'x-g9-\t9' 'x-g9- 9'
  $'x-g9\xe2\x80\x8b-9'
  $'x-g9\x01-9'
  $'x-g9\xcc\x81-9'
  $'x-g\xcc\x819-9' $'x-g\xcc\x829-9' $'x-g\xcc\xa79-9' $'x-g\xcc\x849-9'
  $'x-\xc7\xb59-9' $'x-\xc4\x9f9-9'
  $'x-g9\xe2\x83\x9d-9' $'x-g9\xe0\xa4\x83-9'
  $'x-g9\xc2\xad9'
  $'x-g\xc2\xad9-9' $'x-g9\xc2\xad-9' $'x-g9-\xc2\xad9'
  $'x-g9 \xe2\x80\x8b -9' $'x-g9 \xcc\x81 -9' $'x-g9- \xe2\x80\x8b 9'
  'g9-9'
)
n=0
for v in "${ids[@]}"; do
  n=$((n+1))
  for field in owner blocked next; do
    before="$(lines)"
    if "$DWS" open "id$n-$field" --title t "--$field" "$v" >"$root/out" 2>&1; then
      bad "open refuses $field $(printf %q "$v")" "accepted"
    elif [[ "$(lines)" != "$before" ]]; then
      bad "open refuses $field $(printf %q "$v")" "appended anyway"
    elif ! grep -qi 'instance id' "$root/out"; then
      bad "open refuses $field $(printf %q "$v")" "refused for another reason: $(cat "$root/out")"
    else
      ok "open refuses $field $(printf %q "$v")"
    fi
    before="$(lines)"
    if "$DWS" set base "--$field" "$v" >"$root/out" 2>&1; then
      bad "set refuses $field $(printf %q "$v")" "accepted"
    elif [[ "$(lines)" != "$before" ]]; then
      bad "set refuses $field $(printf %q "$v")" "appended anyway"
    elif ! grep -qi 'instance id' "$root/out"; then
      bad "set refuses $field $(printf %q "$v")" "refused for another reason: $(cat "$root/out")"
    else
      ok "set refuses $field $(printf %q "$v")"
    fi
  done
done
"$DWS" set base --next "review g9 then 9 items" >"$root/out" 2>&1 \
  && ok "set accepts 'review g9 then 9 items'" || bad "set accepts 'review g9 then 9 items'" "$(cat "$root/out")"
"$DWS" set base --blocked "team-a/architect" >"$root/out" 2>&1 \
  && ok "set accepts blocked on a role" || bad "set accepts blocked on a role" "$(cat "$root/out")"
"$DWS" set base --owner "team-a/revisión" >"$root/out" 2>&1 \
  && ok "set accepts an accented role" || bad "set accepts an accented role" "$(cat "$root/out")"
owner="$("$DWS" show --json | python3 -c 'import json,sys; print([r["owner"] for r in json.load(sys.stdin)["rows"] if r["slug"]=="base"][0])' 2>/dev/null || true)"
[[ "$owner" == "team-a/revisión" ]] && ok "the accent survives normalization" || bad "the accent survives normalization" "$owner"
# Stored text round-trips (two-form rule, re-ruled 2026-10-02): the stored
# value is NFKC with whitespace collapsed and Cc stripped, nothing else; the
# match key strips marks and maps dashes, and is never stored.
nfkc() { python3 -c 'import sys,unicodedata; print(unicodedata.normalize("NFKC", sys.argv[1]))' "$1"; }
for v in \
  $'team-a/revisi\xc3\xb3n' $'team-a/revisio\xcc\x81n' \
  $'team-a/\xe0\xa4\xb9\xe0\xa4\xbf\xe0\xa4\x82\xe0\xa4\xa6\xe0\xa5\x80' \
  $'team-a/\xe0\xb8\x94\xe0\xb8\xb4\xe0\xb8\x99' \
  $'team-a/\xe2\x9d\xa4\xef\xb8\x8f' \
  $'team-a/\xf0\x9f\x91\xa9\xe2\x80\x8d\xf0\x9f\x92\xbb' \
  $'team-a/architect \xe2\x80\x93 reviews' \
  'review g9 then 9 items'; do
  want="$(nfkc "$v")"
  if "$DWS" set base --next "$v" >"$root/out" 2>&1; then
    got="$("$DWS" show --json | python3 -c 'import json,sys; print([r["next"] for r in json.load(sys.stdin)["rows"] if r["slug"]=="base"][0])' 2>/dev/null || true)"
    [[ "$got" == "$want" ]] && ok "next $(printf %q "$v") is accepted and stored as its NFKC form" \
      || bad "next $(printf %q "$v") is accepted and stored as its NFKC form" "got $(printf %q "$got")"
  else
    bad "next $(printf %q "$v") is accepted and stored as its NFKC form" "refused: $(cat "$root/out")"
  fi
done
# Pinned by design: the leading boundary means a glued id is not refused.
"$DWS" set base --next "xg9-9" >"$root/out" 2>&1 \
  && ok "glued xg9-9 is accepted, by design (the leading boundary)" \
  || bad "glued xg9-9 is accepted, by design (the leading boundary)" "$(cat "$root/out")"
"$DWS" set base --owner "team-a/architect" >/dev/null 2>&1 || true
"$DWS" set base --owner "x-g9-9 (acting)" >"$root/out" 2>&1 || true
grep -q 'g9-9' "$root/out" && ok "the refusal names the token" || bad "the refusal names the token" "$(cat "$root/out")"

# --- stages and stale moves (section 3) ------------------------------------
"$DWS" stage base defined --from idea >/dev/null 2>&1 \
  && ok "a move with the current from is applied" || bad "a move with the current from is applied"
before="$(lines)"
if "$DWS" stage base designed --from idea >"$root/out" 2>&1; then
  bad "a stale from is refused" "accepted"
else
  [[ "$(lines)" == "$before" ]] && ok "a stale from is refused, nothing appended" || bad "a stale from is refused" "appended anyway"
fi
if "$DWS" stage base shipped --from defined >/dev/null 2>&1; then
  bad "an unknown stage is refused"
else
  ok "an unknown stage is refused"
fi
"$DWS" stage base building --from defined >/dev/null 2>&1 \
  && ok "a move may skip stages" || bad "a move may skip stages"
"$DWS" park base --from building >/dev/null 2>&1 \
  && ok "park records a move to parked" || bad "park records a move to parked"
stage="$("$DWS" show --json | python3 -c 'import json,sys; print([r["stage"] for r in json.load(sys.stdin)["rows"] if r["slug"]=="base"][0])' 2>/dev/null || true)"
[[ "$stage" == parked ]] && ok "show folds base to parked" || bad "show folds base to parked" "$stage"
from="$(python3 - "$LEDGER" <<'PY' 2>/dev/null || true
import json, sys
ev = [json.loads(l) for l in open(sys.argv[1]) if l.strip()]
print(",".join(e["from"] for e in ev if e.get("event") in ("stage", "park") and e.get("ws") == "base"))
PY
)"
[[ "$from" == "idea,defined,building" ]] && ok "each move records the stage it came from" || bad "each move records the stage it came from" "$from"

# --- link, and duplicate open ---------------------------------------------
"$DWS" link base ArcavenAE/director#186 aae-orc-abcd >/dev/null 2>&1 \
  && ok "link adds links" || bad "link adds links"
links="$("$DWS" show --json | python3 -c 'import json,sys; print(" ".join([r["links"] for r in json.load(sys.stdin)["rows"] if r["slug"]=="base"][0]))' 2>/dev/null || true)"
[[ "$links" == "ArcavenAE/director#186 aae-orc-abcd" ]] && ok "show carries the links" || bad "show carries the links" "$links"
"$DWS" link base --remove aae-orc-abcd >/dev/null 2>&1 && ok "link --remove unlinks" || bad "link --remove unlinks"
if "$DWS" open base --title again >/dev/null 2>&1; then bad "open refuses an existing slug"; else ok "open refuses an existing slug"; fi
if "$DWS" set nosuch --next x >/dev/null 2>&1; then bad "set refuses an unknown row"; else ok "set refuses an unknown row"; fi

# --- writer allowlist over --actor (section 6) -----------------------------
"$DWS" open act --title "actor row" >/dev/null 2>&1 || true
"$DWS" stage act building --from idea >/dev/null 2>&1 || true
if "$DWS" --actor beadle set act --next x >/dev/null 2>&1; then bad "an unlisted actor is refused"; else ok "an unlisted actor is refused"; fi
if "$DWS" --actor refresh set act --next x >/dev/null 2>&1; then bad "refresh may not set"; else ok "refresh may not set"; fi
if "$DWS" --actor refresh stage act parked --from building >/dev/null 2>&1; then bad "refresh may not park"; else ok "refresh may not park"; fi
"$DWS" --actor refresh stage act "in review" --from building >/dev/null 2>&1 \
  && ok "refresh may move building to in review" || bad "refresh may move building to in review"

# --- concurrency: 2 x 200 appends -> 400 whole lines (section 6) ----------
"$DWS" open race --title "race row" >/dev/null 2>&1 || true
before="$(lines)"
writer() { for i in $(seq 1 200); do "$DWS" set race --next "w$1 $i" >/dev/null || echo "miss $1 $i" >>"$root/miss"; done; }
writer a & writer b &
wait
added=$(( $(lines) - before ))
whole="$(python3 - "$LEDGER" <<'PY' 2>/dev/null || true
import json, sys
n = 0
for l in open(sys.argv[1]):
    try:
        e = json.loads(l)
    except ValueError:
        continue
    if e.get("ws") == "race" and e.get("event") == "set":
        n += 1
print(n)
PY
)"
[[ ! -s "$root/miss" && "$added" == 400 && "$whole" == 400 ]] \
  && ok "two writers of 200 each give 400 parseable lines" \
  || bad "two writers of 200 each give 400 parseable lines" "added $added, parseable $whole, misses $(wc -l <"$root/miss" 2>/dev/null || echo 0)"

# --- torn last line: skipped, counted, never repaired ----------------------
printf '{"event":"set","ws":"race","next":"torn' >>"$LEDGER"
"$DWS" show --json >"$root/show.json" 2>"$root/show.err" \
  && ok "show reads past a torn last line" || bad "show reads past a torn last line" "$(cat "$root/show.err")"
torn="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["torn"])' "$root/show.json" 2>/dev/null || true)"
[[ "$torn" == 1 ]] && ok "the torn line is counted" || bad "the torn line is counted" "$torn"
"$DWS" set race --next "after torn" >/dev/null 2>&1 && ok "a write after a torn line succeeds" || bad "a write after a torn line succeeds"
grep -q '"next":"torn$' "$LEDGER" && ok "the torn line is left as it was" || bad "the torn line is left as it was"
next="$("$DWS" show --json | python3 -c 'import json,sys; print([r["next"] for r in json.load(sys.stdin)["rows"] if r["slug"]=="race"][0])' 2>/dev/null || true)"
[[ "$next" == "after torn" ]] && ok "the write after a torn line folds" || bad "the write after a torn line folds" "$next"

# --- lock timeout fails loudly ---------------------------------------------
python3 - "$DIRECTOR_STATE/workstreams.lock" <<'PY' &
import fcntl, sys, time
f = open(sys.argv[1], "a")
fcntl.flock(f, fcntl.LOCK_EX)
time.sleep(8)
PY
holder=$!
sleep 1
before="$(lines)"
start=$SECONDS
if "$DWS" set race --next "locked out" >"$root/out" 2>&1; then
  bad "a held lock times out loudly" "accepted"
else
  took=$((SECONDS - start))
  [[ "$(lines)" == "$before" && $took -le 7 ]] && grep -qi lock "$root/out" \
    && ok "a held lock times out loudly, nothing appended" \
    || bad "a held lock times out loudly" "took ${took}s: $(cat "$root/out")"
fi
wait "$holder" 2>/dev/null || true

# --- sort (section 3, test 2): 12 rows, stage order, oldest first, parked last
export DIRECTOR_STATE="$root/sort"
mkdir -p "$DIRECTOR_STATE"
python3 - "$DIRECTOR_STATE/workstreams.jsonl" <<'PY'
import json, sys
rows = [
    ("p1", "parked", "2026-09-01T00:00:00Z"),
    ("a2", "accepted", "2026-09-20T00:00:00Z"),
    ("b1", "building", "2026-09-28T00:00:00Z"),
    ("i1", "idea", "2026-09-30T00:00:00Z"),
    ("r1", "in review", "2026-09-29T00:00:00Z"),
    ("b2", "building", "2026-09-25T00:00:00Z"),
    ("d1", "defined", "2026-09-27T00:00:00Z"),
    ("m1", "merged", "2026-09-26T00:00:00Z"),
    ("g1", "designed", "2026-09-24T00:00:00Z"),
    ("e1", "released", "2026-09-23T00:00:00Z"),
    ("y1", "deployed", "2026-09-22T00:00:00Z"),
    ("i2", "idea", "2026-09-10T00:00:00Z"),
]
with open(sys.argv[1], "w") as f:
    for slug, stage, ts in rows:
        # An idea row's last move is its open; any other row's is its stage move.
        opened = ts if stage == "idea" else "2026-09-01T00:00:00Z"
        f.write(json.dumps({"ts": opened, "actor": "director", "event": "open", "ws": slug, "title": slug}) + "\n")
        if stage != "idea":
            ev = "park" if stage == "parked" else "stage"
            f.write(json.dumps({"ts": ts, "actor": "director", "event": ev, "ws": slug, "from": "idea", "to": stage}) + "\n")
PY
order="$("$DWS" show --json | python3 -c 'import json,sys; print(" ".join(r["slug"] for r in json.load(sys.stdin)["rows"]))' 2>/dev/null || true)"
want="i2 i1 d1 g1 b2 b1 r1 m1 e1 y1 a2 p1"
[[ "$order" == "$want" ]] && ok "12 rows sort by stage, oldest first, parked last" || bad "12 rows sort by stage, oldest first, parked last" "got $order"
"$DWS" show >"$root/text" 2>&1 && head -1 "$root/text" | grep -q . && ok "show prints a text table" || bad "show prints a text table"

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
