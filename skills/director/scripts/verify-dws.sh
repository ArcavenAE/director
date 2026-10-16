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

# --- K8: every new line carries a tag that dws sets itself ------------------
# mirror (a cited outside fact: refresh), proposal (needs a confirm: defined,
# never appended yet) and judgment (director's own call). Old and hand-written
# lines fold as untagged, a value of their own; the fold never refuses them.
export DIRECTOR_STATE="$root/tags"
mkdir -p "$DIRECTOR_STATE"
LEDGER="$DIRECTOR_STATE/workstreams.jsonl"
"$DWS" open t1 --title T1 >/dev/null 2>&1
"$DWS" set t1 --next x >/dev/null 2>&1
"$DWS" stage t1 designed --from idea >/dev/null 2>&1
"$DWS" link t1 pr:ArcavenAE/director#1 >/dev/null 2>&1
"$DWS" park t1 --from designed >/dev/null 2>&1
tags_of() { python3 -c 'import json,sys
for l in open(sys.argv[1]):
    e = json.loads(l)
    print(e["event"], e.get("actor"), e.get("tag"))' "$LEDGER"; }
tags_of >"$root/tags.out" 2>&1 || true
if [[ "$(wc -l <"$root/tags.out" | tr -d ' ')" == 5 ]] && ! grep -v ' director judgment$' "$root/tags.out" | grep -q .; then
  ok "open, set, stage, link and park by director are tagged judgment"
else bad "open, set, stage, link and park by director are tagged judgment" "$(tr '\n' ';' <"$root/tags.out")"; fi

"$DWS" open t2 --title T2 --stage building >/dev/null 2>&1
"$DWS" --actor refresh stage t2 "in review" --from building >/dev/null 2>&1
last="$(tags_of | tail -1)"
[[ "$last" == "stage refresh mirror" ]] && ok "a refresh stage move is tagged mirror" || bad "a refresh stage move is tagged mirror" "$last"

# A caller cannot choose the tag: dws has no flag for it.
if "$DWS" set t1 --next y --tag mirror >/dev/null 2>&1; then bad "dws takes no --tag flag"; else ok "dws takes no --tag flag"; fi

# proposal is defined and refused on append; an unknown value is refused too.
python3 - "$DWS" <<'PY' >"$root/prop.out" 2>&1 && ok "the proposal tag is defined and refused on append" || bad "the proposal tag is defined and refused on append" "$(cat "$root/prop.out")"
import importlib.machinery, importlib.util, sys
loader = importlib.machinery.SourceFileLoader("dws_mod", sys.argv[1])
spec = importlib.util.spec_from_loader("dws_mod", loader)
m = importlib.util.module_from_spec(spec)
loader.exec_module(m)
assert set(m.TAGS) == {"mirror", "proposal", "judgment"}, m.TAGS
for bad_tag in ("proposal", "bogus", None):
    try:
        m.check_tag(bad_tag)
    except m.Refused:
        continue
    raise SystemExit("check_tag accepted %r" % (bad_tag,))
m.check_tag("mirror"); m.check_tag("judgment")
PY

# Hand-written lines: one with no tag, one with a value that is not a tag. Both
# fold as untagged, are never repaired, and nothing is refused.
printf '%s\n' '{"ts":"2026-10-16T00:00:00Z","actor":"director","event":"set","ws":"t1","next":"h1"}' \
  '{"ts":"2026-10-16T00:00:01Z","actor":"director","event":"set","ws":"t1","next":"h2","tag":"bogus"}' \
  '{"ts":"2026-10-16T00:00:02Z","actor":"director","event":"set","ws":"ghost","next":"g"}' \
  '{"ts":"2026-10-16T00:00:03Z","actor":"director","event":"open","ws":"t2","title":"T2 again"}' >>"$LEDGER"
# The text after the untagged lines are in, so the checks below test those lines.
appended_text="$(cat "$LEDGER")"
"$DWS" show --json >"$root/tags.json" 2>"$root/tags.err" && rc=0 || rc=$?
# The ledger-wide count is taken over every event line in the fold: a line for a slug
# with no row (ghost) and a hand-written re-open that resets a row's own count are both
# in it. A per-row count is the row's own events since it was last opened.
python3 - "$root/tags.json" <<'PY' >"$root/tags.chk" 2>&1 && ok "show --json counts events by tag per row, and ledger-wide over every event line" || bad "show --json counts events by tag per row, and ledger-wide over every event line" "$(cat "$root/tags.chk")"
import json, sys
d = json.load(open(sys.argv[1]))
rows = {r["slug"]: r for r in d["rows"]}
assert rows["t1"]["tags"] == {"judgment": 5, "untagged": 2}, rows["t1"]["tags"]
assert rows["t2"]["tags"] == {"untagged": 1}, rows["t2"]["tags"]
assert d["tags"] == {"judgment": 6, "mirror": 1, "untagged": 4}, d["tags"]
PY
[[ "$rc" == 0 ]] && ok "the fold does not refuse an untagged history" || bad "the fold does not refuse an untagged history" "rc $rc"
[[ "$(cat "$LEDGER")" == "$appended_text" ]] && ok "reading never repairs an untagged line" || bad "reading never repairs an untagged line"
"$DWS" set t1 --next after >/dev/null 2>&1 || true
[[ "$(head -n "$(printf '%s\n' "$appended_text" | wc -l | tr -d ' ')" "$LEDGER")" == "$appended_text" ]] \
  && ok "a later write leaves the untagged lines as they were" || bad "a later write leaves the untagged lines as they were"

# A tag the caller puts in an event never reaches the line: stamp sets it from the actor.
PYTHONDONTWRITEBYTECODE=1 python3 - "$DWS" <<'PY' >"$root/stamp.out" 2>&1 && ok "stamp drops a caller tag and sets the actor's" || bad "stamp drops a caller tag and sets the actor's" "$(cat "$root/stamp.out")"
import importlib.machinery, importlib.util, sys
loader = importlib.machinery.SourceFileLoader("dws_mod2", sys.argv[1])
spec = importlib.util.spec_from_loader("dws_mod2", loader)
m = importlib.util.module_from_spec(spec)
loader.exec_module(m)
e = m.stamp("director", {"event": "set", "ws": "x", "tag": "mirror"})
assert e["tag"] == "judgment", e
r = m.stamp("refresh", {"event": "observe", "ws": "x", "tag": "judgment"})
assert r["tag"] == "mirror", r
assert list(e)[:3] == ["ts", "actor", "tag"], list(e)
PY

# --- K9: rulings.jsonl kinds ruling, grant and term (exclusion stays the operator's)
export DIRECTOR_STATE="$root/rec"
mkdir -p "$DIRECTOR_STATE"
RUL="$DIRECTOR_STATE/rulings.jsonl"
unset MERGE_GUARD_RULINGS
rec() { "$DWS" record "$@"; }
rl() { if [[ -f "$RUL" ]]; then wc -l <"$RUL" | tr -d ' '; else echo 0; fi; }
jf() { python3 -c 'import json,sys
rows=[json.loads(l) for l in open(sys.argv[1]) if l.strip()]
r=rows[int(sys.argv[2])]
v=r
for k in sys.argv[3].split("."):
    v=v.get(k, "__absent__") if isinstance(v, dict) else "__absent__"
print(json.dumps(v, ensure_ascii=False) if not isinstance(v, str) else v)' "$RUL" "$1" "$2" 2>/dev/null || echo "__error__"; }

# A missing file is refused with the guard's wording; nothing is created.
if rec ruling --subject s --text t --by operator --ruled-at 2026-10-16 --source relay-1 >"$root/out" 2>&1; then
  bad "record refuses a missing rulings file" "accepted"
elif [[ -e "$RUL" || -e "$DIRECTOR_STATE/rulings.lock" ]]; then
  bad "record refuses a missing rulings file" "created $(ls "$DIRECTOR_STATE")"
elif grep -q "no rulings file at $RUL" "$root/out" && grep -q "does not create the file" "$root/out"; then
  ok "record refuses a missing rulings file with the guard's wording and creates nothing"
else bad "record refuses a missing rulings file" "$(cat "$root/out")"; fi

: >"$RUL"
verbatim=$'  the operator said:  "ship it"\tnot\u00a0before  Friday  '
rec ruling --subject merge-order --text "$verbatim" --by operator --ruled-at 2026-10-16 --source relay-1 --card card-7 >"$root/out" 2>&1 \
  && ok "record writes a ruling" || bad "record writes a ruling" "$(cat "$root/out")"
[[ "$(rl)" == 1 ]] && ok "a ruling appends one line" || bad "a ruling appends one line" "$(rl)"
chk="$(python3 - "$RUL" "$verbatim" <<'PY' 2>&1
import json, re, sys
e = json.loads(open(sys.argv[1]).readline())
need = ["kind", "id", "ts", "actor", "tag", "source", "subject", "text", "by", "ruled_at"]
miss = [k for k in need if k not in e]
assert not miss, miss
assert e["kind"] == "ruling" and e["actor"] == "director" and e["tag"] == "mirror", e
assert re.fullmatch(r"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ", e["ts"]), e["ts"]
assert e["id"] == "ruling-" + e["ts"], e["id"]
assert e["source"] == "relay-1" and e["by"] == "operator" and e["ruled_at"] == "2026-10-16" and e["card"] == "card-7", e
assert e["text"] == sys.argv[2], repr(e["text"])
print("ok")
PY
)" || true
[[ "$chk" == ok ]] && ok "the ruling carries every required field and its text verbatim" || bad "the ruling carries every required field and its text verbatim" "$chk"

# grant: a draft is a proposal, a confirmed grant is a mirror; to is a role, never an instance id.
rec grant --subject reviewer-allow --scope "gh pr review" --to builder --state draft --text "may review" --source card-9 >/dev/null 2>&1 || true
[[ "$(jf 1 tag)" == proposal && "$(jf 1 state)" == draft ]] && ok "a draft grant is tagged proposal" || bad "a draft grant is tagged proposal" "$(tail -1 "$RUL")"
rec grant --subject reviewer-allow --scope "gh pr review" --to builder --state confirmed --text "may review" --source card-9 --replaces "$(jf 1 id)" >/dev/null 2>&1 || true
[[ "$(jf 2 tag)" == mirror && "$(jf 2 replaces)" == "$(jf 1 id)" ]] && ok "a confirmed grant is tagged mirror and carries replaces" || bad "a confirmed grant is tagged mirror and carries replaces" "$(tail -1 "$RUL")"
n="$(rl)"
if rec grant --subject s --scope x --to builder-g5-0 --state draft --text t --source c >"$root/out" 2>&1; then
  bad "a grant to an instance id is refused" "accepted"
elif grep -qi 'instance id' "$root/out" && [[ "$(rl)" == "$n" ]]; then ok "a grant to an instance id is refused, nothing appended"
else bad "a grant to an instance id is refused" "$(cat "$root/out")"; fi
if rec grant --subject s --scope x --to builder --state maybe --text t --source c >/dev/null 2>&1; then
  bad "a grant state other than draft or confirmed is refused" "accepted"
else ok "a grant state other than draft or confirmed is refused"; fi

rec term --role builder --subject lane-c --text "hold the director lane" --set-by supervisor --source relay-2 >/dev/null 2>&1 || true
[[ "$(jf 3 kind)" == term && "$(jf 3 set_by)" == supervisor && "$(jf 3 tag)" == mirror ]] && ok "record writes a term" || bad "record writes a term" "$(tail -1 "$RUL")"

# exclusion is the operator's; a missing required field, a repeated id and a non-director actor are refused.
n="$(rl)"
if rec exclusion --repo org/repo --label acu.x >"$root/out" 2>&1; then bad "record refuses the exclusion kind" "accepted"
elif grep -q 'R-153' "$root/out" && [[ "$(rl)" == "$n" ]]; then ok "record refuses the exclusion kind and names R-153"
else bad "record refuses the exclusion kind" "$(cat "$root/out")"; fi
if rec ruling --subject s --text t --by operator --ruled-at 2026-10-16 >/dev/null 2>&1; then bad "a record with no source is refused" "accepted"
elif [[ "$(rl)" == "$n" ]]; then ok "a record with no source is refused"; else bad "a record with no source is refused" "appended"; fi
if rec ruling --subject s --text t --by operator --ruled-at yesterday --source c >/dev/null 2>&1; then bad "a ruled-at that is not a date is refused" "accepted"
else ok "a ruled-at that is not a date is refused"; fi
rec ruling --id fixed-1 --subject s --text t --by operator --ruled-at 2026-10-16 --source c >/dev/null 2>&1 \
  && ok "an explicit id is accepted" || bad "an explicit id is accepted"
n="$(rl)"
if rec ruling --id fixed-1 --subject s --text t --by operator --ruled-at 2026-10-16 --source c >"$root/out" 2>&1; then bad "a repeated id is refused" "accepted"
elif [[ "$(rl)" == "$n" ]] && grep -qi 'already' "$root/out"; then ok "a repeated id is refused, nothing appended"
else bad "a repeated id is refused" "$(cat "$root/out")"; fi
if "$DWS" --actor refresh record ruling --subject s --text t --by operator --ruled-at 2026-10-16 --source c >/dev/null 2>&1; then bad "refresh may not write a record" "accepted"
else ok "refresh may not write a record"; fi

# The fold: replaces is an explicit pointer. A missing id or another kind is counted and reported, never guessed.
printf '%s\n' '{"kind":"exclusion","repo":"org/repo","label":"acu.*"}' >>"$RUL"
rec term --role builder --subject lane-c --text "lane c is done" --set-by supervisor --source relay-3 --replaces no-such-id >/dev/null 2>&1 || true
rec term --role builder --subject lane-c --text "x" --set-by supervisor --source relay-3 --replaces fixed-1 >/dev/null 2>&1 || true
"$DWS" records --json >"$root/rec.json" 2>"$root/rec.err" && rc=0 || rc=$?
chk="$(python3 - "$root/rec.json" "$RUL" <<'PY' 2>&1
import json, sys
d = json.load(open(sys.argv[1]))
grants = [r for r in d["records"] if r["kind"] == "grant"]
assert grants[0]["replaced_by"] == [grants[1]["id"]], grants[0]
assert grants[1]["replaced_by"] == [], grants[1]
bad = {u["replaces"]: u["why"] for u in d["unresolved_replaces"]}
assert bad.get("no-such-id") == "missing", d["unresolved_replaces"]
assert bad.get("fixed-1") == "other kind", d["unresolved_replaces"]
assert d["counts"]["exclusion"] == 1 and d["counts"]["ruling"] >= 2 and d["counts"]["term"] >= 1, d["counts"]
assert d["torn"] == 0
assert open(sys.argv[2]).read().count('{"kind":"exclusion","repo":"org/repo","label":"acu.*"}\n') == 1
print("ok")
PY
)" || true
[[ "$rc" == 0 && "$chk" == ok ]] && ok "records marks a replaced target, counts a missing or other-kind pointer, keeps an exclusion line as written" || bad "records marks a replaced target, counts a missing or other-kind pointer, keeps an exclusion line as written" "rc $rc: $chk"

# Two records of one kind in one second: the default id takes -2 so it stays unique.
n="$(rl)"
for _ in 1 2; do DWS_TEST_CLOCK=1 DWS_NOW=2030-01-01T00:00:00Z rec ruling --subject same --text t --by operator --ruled-at 2030-01-01 --source c >/dev/null 2>&1 || true; done
if [[ "$(jf $n id)" == "ruling-2030-01-01T00:00:00Z" && "$(jf $((n+1)) id)" == "ruling-2030-01-01T00:00:00Z-2" ]]; then ok "a default id repeated in one second takes -2"
else bad "a default id repeated in one second takes -2" "$(jf $n id) $(jf $((n+1)) id)"; fi

# Own lock file beside rulings.jsonl; the workstream lock is not used; parallel writes all land whole.
[[ -e "$DIRECTOR_STATE/rulings.lock" && ! -e "$DIRECTOR_STATE/workstreams.lock" ]] && ok "record locks its own file beside rulings.jsonl" || bad "record locks its own file beside rulings.jsonl" "$(ls "$DIRECTOR_STATE")"
n="$(rl)"
for i in 1 2 3 4 5 6 7 8; do rec ruling --id par-$i --subject p --text "t$i" --by operator --ruled-at 2026-10-16 --source c >/dev/null 2>&1 & done
wait
if [[ "$(( $(rl) - n ))" == 8 ]] && python3 -c 'import json,sys; [json.loads(l) for l in open(sys.argv[1])]' "$RUL"; then ok "eight parallel records all land as whole lines"
else bad "eight parallel records all land as whole lines" "$(( $(rl) - n )) new"; fi

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
