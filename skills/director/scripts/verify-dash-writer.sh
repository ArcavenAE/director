#!/usr/bin/env bash
# Prove `dash-writer` against sim/design/ops-dashboard.md sections 3 and 4 and tests 2 to 4 (the writer's
# half of 9), as the operator approved them on 2026-10-20 with dash_meta.checks added:
#   - it writes only the approved fields, in only dash_panels, dash_daily and dash_meta, in one batch of
#     at most 50 writes; `next`, `repos` and anything else never reach a document;
#   - title, id, health, state and last_active checks, each failure counted by field and reason and never
#     by value; a placeholder is a fixed word;
#   - the sessions table is parsed by fixed offsets from its header and refuses on a moved layout, a row
#     that cannot be cut cleanly alone, and an unreached cluster as one line;
#   - a panel over 256 KiB is written with no rows and a refusal;
#   - the three checks, the 30-minute floor, the 14-day delete, and read-only behaviour.
# The Desk's own write rule (test 8) and the page (tests 1, 5 to 7) live in the Desk artifact, not here.
# Clusters come from the `clusters` keys of the marvel config under the scratch HOME (key names only; every
# value in the fixture carries a canary that must never appear), and Desk areas from the `areas` the Desk
# snapshot records; neither has a file of its own.
# Every run is under `env -i` with a scratch HOME and DIRECTOR_STATE, a fixture marvel on PATH, no
# MARVEL_SOCKET. Written for bash 3.2 and bash 5.
#
# Usage: skills/director/scripts/verify-dash-writer.sh   Exit nonzero on any miss.
set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
W="${DASH_WRITER:-$here/dash-writer}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }
skp()  { echo "SKIP $1"; }
[[ -f "$W" ]] || { echo "FAIL dash-writer exists at $W"; echo "0 passed, 1 failed"; exit 1; }

root="$(mktemp -d "${DASH_TMP:-/tmp}/dsh.XXXXXX")" || { echo "FAIL scratch root: mktemp failed"; exit 1; }
[[ -n "$root" && -d "$root" ]] || { echo "FAIL scratch root: no directory"; exit 1; }
root="$(cd "$root" && pwd -P)"
trap 'rm -rf "$root"' EXIT
mkdir -p "$root/home" "$root/bin" "$root/tables" "$root/state"
PYDIR="$(dirname "$(command -v python3)")"
state="$root/state"
NOW="2026-10-20T18:00:00Z"
command -v yq >/dev/null 2>&1 || { echo "FAIL yq (the mikefarah build) is required: dash-writer reads the marvel config with it"; exit 1; }
YQDIR="$(dirname "$(command -v yq)")"
run() { env -i HOME="$root/home" PATH="$root/bin:/usr/bin:/bin:$PYDIR:$YQDIR" LANG=en_US.UTF-8 DIRECTOR_STATE="$state" \
        DWS_TEST_CLOCK=1 DWS_NOW="${NOW_OVERRIDE:-$NOW}" DASH_MARVEL="$root/bin/marvel" TABLES="$root/tables" python3 -I "$W" "$@"; }

cat >"$root/bin/marvel" <<'SH'
#!/bin/sh
# fixture marvel: `marvel --cluster C get sessions --columns ...` prints $TABLES/C, or fails when there is none
f="$TABLES/$2"
[ -f "$f" ] || { echo "no route to $2" >&2; exit 1; }
cat "$f"
SH
chmod +x "$root/bin/marvel"

# a sessions table the way marvel's tabwriter lays it out: padding 2, cells aligned to the header
mktable() { python3 - "$@" <<'PY'
import sys
head = ["TEAM", "ROLE", "AGENT NAME", "STATE", "HEALTH", "LAST-ACTIVE"]
rows = [r.split("|") for r in sys.stdin.read().splitlines() if r] if False else []
PY
}
python3 - "$root/tables" <<'PY'
import os, sys
d = sys.argv[1]
def table(head, rows):
    w = [max(len(r[i]) for r in [head] + rows) for i in range(len(head))]
    out = ["  ".join(c.ljust(w[i]) for i, c in enumerate(r)).rstrip() for r in [head] + rows]
    return "\n".join(out) + "\n"
H = ["TEAM", "ROLE", "AGENT NAME", "STATE", "HEALTH", "LAST-ACTIVE"]
rows = [
    ["arcaven", "builder", "arcaven-builder-g5-0", "running", "healthy", "3m*"],
    ["arcaven", "supervisor", "arcaven-supervisor-g5-0", "running", "healthy (parked: /Users/a/secret)", "12m"],
    ["arcaven", "analyst", "arcaven-analyst-g5-1", "running", "healthy (stalled) (parked: a (b) c)", "1h"],
    ["arcaven", "worker", "arcaven-worker-g5-2", "crashed (exit 3)", "unknown", "-"],
    ["arcaven", "worker", "arcaven-worker-g5-3", "failed (budget exceeded)", "unhealthy", "2d"],
    ["arcaven", "worker", "arcaven-worker-g5-4", "running (exit 3)", "healthy", "1m"],
    ["arcaven", "worker", "arcaven-worker-g5-5", "running", "healthy (parked: x) (stalled)", "1m"],
    ["arcaven", "worker", "arcaven-worker-g5-6", "running", "healthy", ("1m " * 14).strip()],
    ["arcaven", "worker", "bad_name", "running", "healthy", "1m"],
    ["arcaven", "worker", "arcaven-worker-g5-7", "running", "healthy", "5 fortnights ago"],
    ["arcaven", "worker", "arcaven-worker-g5-8", "running", "healthy", "2h5m"],
    ["arcaven", "worker", "arcaven-worker-g5-9", "running", "healthy", "stale 5m*"],
    ["arcaven", "worker", "arcaven-worker-g5-10", "running", "healthy", "stale soon"],
    ["arcaven", "worker", "arcaven-bartholomew-g5-9", "running", "healthy", "1m"],
]
open(os.path.join(d, "alpha"), "w").write(table(H, rows))
open(os.path.join(d, "good"), "w").write(table(H, rows[:1]))
# a renamed column, a missing column, an extra column
open(os.path.join(d, "renamed"), "w").write(table(["TEAM", "ROLE", "NAME", "STATE", "HEALTH", "LAST-ACTIVE"], [r for r in rows[:1]]))
open(os.path.join(d, "missing"), "w").write(table(H[:5], [r[:5] for r in rows[:1]]))
open(os.path.join(d, "extra"), "w").write(table(H + ["CTX%"], [r + ["9"] for r in rows[:1]]))
# rows that cannot be cut: a cell that runs into the next column, a tab, a short row
t = table(H, [rows[0], rows[0][:2] + ["arcaven-builder-g5-1"] + rows[0][3:], rows[0][:2] + ["arcaven-builder-g5-2"] + rows[0][3:]]).split("\n")
t[2] = t[2].replace("arcaven-builder-g5-1  running", "arcaven-builder-g5-1 running", 1)
t[3] = t[3].replace("running", "run\tning", 1)
t = [x for x in t if x != ""] + ["arcaven  builder  arcaven-builder-g5-9"]
open(os.path.join(d, "cuts"), "w").write("\n".join(x for x in t if x is not None) + "\n")
PY

seed() { # python source reading $state/ledger.jsonl lines from stdin
  python3 -c '
import json, sys
out = open(sys.argv[1], "w")
for l in sys.stdin:
    l = l.strip()
    if l: out.write(json.dumps(json.loads(l)) + "\n")
' "$state/workstreams.jsonl"
}
openev() { # slug stage title blocked links-json ts
  python3 -c '
import json, sys
slug, stage, title, blocked, links, ts = sys.argv[1:7]
print(json.dumps({"ts": ts, "actor": "director", "tag": "judgment", "event": "open", "ws": slug, "title": title, "stage": stage,
  "owner": "arcaven-builder", "next": "CANARY-NEXT-8841", "blocked": blocked, "repos": ["CANARY-REPO-9902/x"], "links": json.loads(links)}))
' "$@"
}
HEX30="$(python3 -c 'print("a1b2c3d4"*4)')"
TOK="ghp_""$(python3 -c 'print("Z"*30)')"
{
  openev linked-one "in review" "a plain title" "" '["pr:o/r#1"]' 2026-10-18T06:00:00Z
  openev t-path "building" "see ~/work/x for it" "" '["pr:o/r#2"]' 2026-10-19T06:00:00Z
  openev t-users "building" "fix /Users/a/b today" "" '["pr:o/r#3"]' 2026-10-19T07:00:00Z
  openev t-token "building" "token $TOK here" "" '["pr:o/r#4"]' 2026-10-19T08:00:00Z
  openev t-hex "building" "digest $HEX30 here" "" '["pr:o/r#5"]' 2026-10-19T09:00:00Z
  openev t-url "building" "see https://example.test/a" "" '["pr:o/r#6"]' 2026-10-19T10:00:00Z
  openev t-name "building" "ask Bartholomew about it" "" '["pr:o/r#7"]' 2026-10-19T11:00:00Z
  openev t-long "building" "$(python3 -c 'print("x"*121)')" "" '["pr:o/r#8"]' 2026-10-19T12:00:00Z
  openev t-ok120 "building" "$(python3 -c 'print("y "*60)' | sed 's/ $//' | cut -c1-120)" "" '["pr:o/r#9"]' 2026-10-19T13:00:00Z
  openev bad_slug "building" "underscore id" "" '["pr:o/r#10"]' 2026-10-19T14:00:00Z
  openev bartholomew-plan "building" "a plain title" "" '["pr:o/r#12"]' 2026-10-19T15:00:00Z
  openev waits "defined" "waits on the operator" "operator" '["ask:c1"]' 2026-10-17T06:00:00Z
  openev idea-a "idea" "an idea" "" '[]' 2026-10-05T06:00:00Z
  openev idea-b "idea" "another idea" "" '[]' 2026-10-09T06:00:00Z
  openev done-a "merged" "merged one" "" '["pr:o/r#11"]' 2026-10-10T06:00:00Z
  printf '{"ts":"2026-10-20T10:00:00Z","actor":"refresh","tag":"mirror","event":"refresh-run","at":"2026-10-20T10:00:00Z","rows":14,"links":3,"warnings":0}\n'
} >"$root/ledger.lines"
seed <"$root/ledger.lines"
echo '{"read_at":"2026-10-20T10:00:00Z","cards":1,"open":["c1"],"by_status":{"open":1},"ids":{"c1":"open"},"areas":["Ops","Research"]}' >"$state/desk-snapshot.json"
printf 'linked-one\tOps\nwaits\tResearch\nidea-a\tNowhere\n' >"$state/dash-area-map.tsv"
printf 'Bartholomew\n' >"$state/dash-withhold.txt"; chmod 600 "$state/dash-withhold.txt"

setclusters() { # names...: a marvel config whose cluster entries hold values that must never be read
  mkdir -p "$root/home/.marvel"
  { echo "# comment"; echo "default_cluster: CANARY-TOP-5520"; echo "token: CANARY-TOKEN-8801"; echo "clusters:"
    for n in "$@"; do echo "  $n:   # CANARY-KEYLINE-6633"; echo "    url: https://CANARY-URL-4410.example.test"; echo "    credentials:"; echo "      secret: CANARY-SECRET-3392"; echo "    nested:"; echo "      deep: CANARY-DEEP-7714"; done
    echo "other:"; echo "  notacluster: CANARY-OTHER-6620"; } >"$root/home/.marvel/config.yaml"
}
setclusters alpha
pyc() { python3 -c "$1" "$root/out.json"; }
runout() { run "$@" >"$root/out.json" 2>"$root/err"; }

# ---------------------------------------------------------------- usage
run bogus >/dev/null 2>&1; rc=$?; [[ $rc -eq 2 ]] && ok "an unknown subcommand exits 2" || bad "unknown subcommand exits 2" "rc=$rc"
run --help >"$root/o" 2>&1; rc=$?; [[ $rc -eq 0 ]] && grep -q 'dash_panels' "$root/o" && ok "--help exits 0 and names the collections" || bad "--help"

# ---------------------------------------------------------------- the batch
runout run; rc=$?
[[ $rc -eq 0 ]] && ok "a run exits 0" || bad "a run exits 0" "rc=$rc $(head -3 "$root/err")"
pyc '
import json, sys
d = json.load(open(sys.argv[1]))
w = d["writes"]
assert len(w) <= 50, len(w)
cols = {x["collection"] for x in w}
assert cols == {"dash_panels", "dash_daily", "dash_meta"}, cols
ids = sorted(x["doc_id"] for x in w if x["collection"] == "dash_panels")
assert ids == ["in-flight", "sessions", "waiting", "where-work-sits"], ids
for x in w:
    if x["action"] == "delete":
        assert set(x) == {"action", "collection", "doc_id"}, x
        continue
    assert x["action"] == "set" and set(x) == {"action", "collection", "doc_id", "data"}, x
    k = set(x["data"])
    if x["collection"] == "dash_panels": assert k == {"panel", "as_of", "writer", "rows", "counts", "refusal"}, k
    if x["collection"] == "dash_daily": assert k == {"panel", "day", "counts"}, k
    if x["collection"] == "dash_meta": assert k == {"last_run", "runs", "reader", "checks"} and x["doc_id"] == "writer", k
' && ok "only dash_panels, dash_daily and dash_meta; at most 50 writes; each document holds exactly the approved fields" || bad "batch shape"
pyc '
import json, sys
d = json.load(open(sys.argv[1]))
LEDGER = {"id", "stage", "age_h", "area", "title"}
SESS = {"id", "stage", "health", "last_active", "cluster"}
for x in d["writes"]:
    if x["collection"] != "dash_panels": continue
    for r in x["data"]["rows"]:
        want = SESS if x["doc_id"] == "sessions" else LEDGER
        assert set(r) == want, (x["doc_id"], set(r))
' && ok "a row holds only the approved fields (ledger rows: id, stage, age_h, area, title; session rows: id, stage, health, last_active, cluster)" || bad "row fields"
grep -q 'CANARY' "$root/out.json" && bad "next and repos never reach a document" || ok "next and repos never reach a document (canaries absent)"

# ---------------------------------------------------------------- titles and ids
pyc '
import json, sys
d = json.load(open(sys.argv[1]))
rows = {r["id"]: r for x in d["writes"] if x["collection"] == "dash_panels" and x["doc_id"] == "in-flight" for r in x["data"]["rows"]}
exp = {"t-path": "(title withheld: path)", "t-users": "(title withheld: path)", "t-token": "(title withheld: token)", "t-hex": "(title withheld: token)",
       "t-url": "(title withheld: url)", "t-name": "(title withheld: withheld-name)", "t-long": "(title withheld: length)"}
for k, v in exp.items():
    assert rows[k]["title"] == v, (k, rows[k]["title"])
assert rows["linked-one"]["title"] == "a plain title"
assert len(rows["t-ok120"]["title"]) <= 120 and not rows["t-ok120"]["title"].startswith("(title withheld"), rows["t-ok120"]["title"]
assert "bad_slug" not in rows
' && ok "a title with ~/, /Users/, a token, a hex run, a URL, a withheld name or over 120 characters is a fixed placeholder; a 120-character title passes" || bad "title checks"
pyc '
import json, sys
d = json.load(open(sys.argv[1]))
ref = [x["data"]["refusal"] for x in d["writes"] if x["collection"] == "dash_panels" and x["doc_id"] == "in-flight"][0]
assert ref.get("id:charset") == 1, ref
assert ref.get("title:path") == 2 and ref.get("title:token") == 2 and ref.get("title:url") == 1 and ref.get("title:withheld-name") == 1 and ref.get("title:length") == 1, ref
' && ok "each failure is counted by field and reason in the panel's refusal; an id with an underscore writes no row" || bad "refusal counts" "$(python3 -c 'import json;d=json.load(open("'"$root"'/out.json"));print([x["data"]["refusal"] for x in d["writes"] if x["doc_id"]=="in-flight"])')"
for v in "$TOK" "$HEX30" "example.test" "Bartholomew" "/Users/a/b"; do
  grep -q -F -- "$v" "$root/out.json" && bad "the refusal and the page never repeat a failed value" "$v appears"
done; ok "no failed value appears anywhere in the output"
pyc '
import json, sys
d = json.load(open(sys.argv[1]))
rows = {r["id"]: r for x in d["writes"] if x["doc_id"] == "in-flight" and x["collection"] == "dash_panels" for r in x["data"]["rows"]}
assert rows["linked-one"]["area"] == "Ops" and rows["t-path"]["area"] == "unlabelled", rows["linked-one"]
w = {r["id"]: r for x in d["writes"] if x["doc_id"] == "waiting" and x["collection"] == "dash_panels" for r in x["data"]["rows"]}
assert list(w) == ["waits"] and w["waits"]["area"] == "Research", w
' && ok "area comes from the operator's map and a label on the Desk list; anything else is unlabelled; waiting lists the operator-blocked row" || bad "area and waiting"
pyc '
import json, sys
d = json.load(open(sys.argv[1]))
c = {x["doc_id"]: x["data"]["counts"] for x in d["writes"] if x["collection"] == "dash_panels"}["where-work-sits"]
assert c["idea"] == 2 and c["merged"] == 1 and c["ideas_oldest_opened"] == "2026-10-05", c
f = [x["data"]["rows"] for x in d["writes"] if x["collection"] == "dash_panels" and x["doc_id"] == "in-flight"][0]
assert not any(r["id"] in ("idea-a", "idea-b", "done-a") for r in f), "ideas and merged are not in flight"
' && ok "ideas and merged rows are counts only, never listed in flight" || bad "ideas and merged"

# ---------------------------------------------------------------- sessions
pyc '
import json, sys
d = json.load(open(sys.argv[1]))
rows = {r["id"]: r for x in d["writes"] if x["doc_id"] == "sessions" and x["collection"] == "dash_panels" for r in x["data"]["rows"]}
assert rows["arcaven-builder-g5-0"] == {"id": "arcaven-builder-g5-0", "stage": "running", "health": "healthy", "last_active": "3m*", "cluster": "alpha"}, rows["arcaven-builder-g5-0"]
assert rows["arcaven-supervisor-g5-0"]["health"] == "healthy (parked)", rows["arcaven-supervisor-g5-0"]
assert rows["arcaven-analyst-g5-1"]["health"] == "healthy (stalled) (parked)", rows["arcaven-analyst-g5-1"]
assert rows["arcaven-worker-g5-2"]["stage"] == "crashed (exit 3)", rows["arcaven-worker-g5-2"]
assert rows["arcaven-worker-g5-3"]["stage"] == "failed (terminal)", rows["arcaven-worker-g5-3"]
assert "arcaven-worker-g5-4" not in rows, "running (exit 3) fails the grammar and drops the row"
assert rows["arcaven-worker-g5-5"]["health"] == "unreadable", "wrong order is unreadable"
assert rows["arcaven-worker-g5-6"]["last_active"] == "unreadable", "over 40 characters is unreadable"
assert "bad_name" not in rows
assert rows["arcaven-worker-g5-7"]["last_active"] == "unreadable", "5 fortnights ago is not a word of the grammar"
assert rows["arcaven-worker-g5-8"]["last_active"] == "2h5m" and rows["arcaven-worker-g5-9"]["last_active"] == "stale 5m*", (rows["arcaven-worker-g5-8"], rows["arcaven-worker-g5-9"])
assert rows["arcaven-worker-g5-10"]["last_active"] == "unreadable", "stale takes an age word or nothing"
assert "arcaven-bartholomew-g5-9" not in rows, "a withheld name in a session name drops the row"
ref = [x["data"]["refusal"] for x in d["writes"] if x["doc_id"] == "sessions" and x["collection"] == "dash_panels"][0]
assert ref.get("health:grammar") == 1 and ref.get("last_active:unreadable") == 3 and ref.get("stage:grammar") == 1 and ref.get("id:charset") == 1, ref
' && ok "state and health are matched to marvel's grammar: parked reasons dropped, exit codes as printed, failed tagged terminal, wrong order and long cells unreadable, a bad state drops the row" || bad "session grammar"
grep -q 'secret\|budget exceeded' "$root/out.json" && bad "free text after a session cell never lands" || ok "free text inside a session cell never lands"

setclusters alpha renamed; runout run
pyc '
import json, sys
d = json.load(open(sys.argv[1]))
p = [x["data"] for x in d["writes"] if x["doc_id"] == "sessions" and x["collection"] == "dash_panels"][0]
assert not [r for r in p["rows"] if r["cluster"] == "renamed"], "no rows from a renamed column"
assert p["refusal"].get("table:header") == 1, p["refusal"]
' && ok "a renamed column draws no rows from that cluster and sets refusal" || bad "renamed column"
for c in missing extra; do
  setclusters good $c; runout run
  pyc 'import json,sys
d=json.load(open(sys.argv[1])); p=[x["data"] for x in d["writes"] if x["doc_id"]=="sessions" and x["collection"]=="dash_panels"][0]
assert p["refusal"].get("table:header")==1 and all(r["cluster"]=="good" for r in p["rows"]), p["refusal"]' \
   && ok "a $c column draws no rows from that cluster and sets refusal" || bad "$c column"
done
setclusters cuts; runout run
pyc '
import json, sys
d = json.load(open(sys.argv[1]))
p = [x["data"] for x in d["writes"] if x["doc_id"] == "sessions" and x["collection"] == "dash_panels"][0]
ids = [r["id"] for r in p["rows"]]
assert ids == ["arcaven-builder-g5-0"], ids
assert p["refusal"].get("row:unreadable") == 3, p["refusal"]
' && ok "a cell that runs into its neighbour, a row with a tab and a short row are each refused alone; the clean row is drawn and no value shifts" || bad "unreadable rows" "$(cat "$root/out.json" | head -c 400)"
setclusters good nowhere; runout run
pyc '
import json, sys
d = json.load(open(sys.argv[1]))
p = [x["data"] for x in d["writes"] if x["doc_id"] == "sessions" and x["collection"] == "dash_panels"][0]
assert p["counts"]["not_reached"] == ["nowhere: not reached at 2026-10-20T18:00:00Z"], p["counts"]
assert all(r["cluster"] == "good" for r in p["rows"])
' && ok "a cluster that does not answer is one line, not reached at T, and no rows" || bad "not reached"
setclusters 'bad/name'; runout run
pyc 'import json,sys
d=json.load(open(sys.argv[1])); p=[x["data"] for x in d["writes"] if x["doc_id"]=="sessions" and x["collection"]=="dash_panels"][0]
assert p["rows"]==[] and p["refusal"].get("cluster:charset")==1, p' && ok "a cluster name that is not on the list's grammar writes nothing" || bad "cluster charset"
rm -f "$root/home/.marvel/config.yaml"; runout run
pyc 'import json,sys
d=json.load(open(sys.argv[1])); p=[x["data"] for x in d["writes"] if x["doc_id"]=="sessions" and x["collection"]=="dash_panels"][0]
assert p["rows"]==[] and p["refusal"].get("clusters:unread")==1, p' && ok "with no cluster list the sessions panel is refused, never written empty as if read" || bad "no cluster list"
setclusters alpha

# the config reader: key names only
setclusters alpha beta; runout run
grep -q 'CANARY' "$root/out.json" "$root/err" && bad "no config value reaches the output" "$(grep -o 'CANARY[A-Z0-9-]*' "$root/out.json" "$root/err" | head -3)" || ok "no value under a cluster key, and no other config value, reaches the output or stderr"
pyc 'import json,sys
d=json.load(open(sys.argv[1])); p=[x["data"] for x in d["writes"] if x["doc_id"]=="sessions" and x["collection"]=="dash_panels"][0]
assert sorted({r["cluster"] for r in p["rows"]})==["alpha"] and p["counts"]["not_reached"]==["beta: not reached at 2026-10-20T18:00:00Z"], (p["counts"], p["rows"][:1])' \
  && ok "the clusters are the key names under clusters: in the marvel config, in order, and nothing else in the file is one" || bad "config key names"
printf 'clusters: {alpha: {url: CANARY-FLOW-1}}\n' >"$root/home/.marvel/config.yaml"; runout run
pyc 'import json,sys
d=json.load(open(sys.argv[1])); p=[x["data"] for x in d["writes"] if x["doc_id"]=="sessions" and x["collection"]=="dash_panels"][0]
assert sorted({r["cluster"] for r in p["rows"]})==["alpha"], p["rows"][:1]'
[[ $? -eq 0 ]] && ! grep -q CANARY "$root/out.json" "$root/err" && ok "a flow-style clusters mapping is read by the real parser: its key is used and no value is echoed" || bad "flow-style clusters"
printf 'clusters:\n  alpha:\n    url: x\n  beta: "first line\n  CANARYLEAK7: rest"\n' >"$root/home/.marvel/config.yaml"; runout run
! grep -q CANARYLEAK7 "$root/out.json" "$root/err" && grep -q 'beta: not reached' "$root/out.json" && ok "a quoted value that spans lines is not read as a key: only the real keys appear" || bad "quoted continuation" "$(grep -o 'CANARYLEAK7[^"]*' "$root/out.json" | head -1)"
printf 'clusters:\n  alpha: [unclosed CANARY-BAD-4471\n  beta: {\n' >"$root/home/.marvel/config.yaml"; runout run
pyc 'import json,sys
d=json.load(open(sys.argv[1])); p=[x["data"] for x in d["writes"] if x["doc_id"]=="sessions" and x["collection"]=="dash_panels"][0]
assert p["rows"]==[] and p["refusal"].get("clusters:unread")==1, p'
[[ $? -eq 0 ]] && ! grep -q CANARY "$root/out.json" "$root/err" && ok "a config that does not parse refuses the sessions panel and prints nothing from the file" || bad "invalid yaml"
printf 'clusters: {}\n  alpha:\n    url: CANARY-FLOW-2\n' >"$root/home/.marvel/config.yaml"; runout run
pyc 'import json,sys
d=json.load(open(sys.argv[1])); p=[x["data"] for x in d["writes"] if x["doc_id"]=="sessions" and x["collection"]=="dash_panels"][0]
assert p["rows"]==[] and p["refusal"].get("clusters:unread")==1, p' && ok "a clusters key with an inline value takes no keys from the lines under it" || bad "inline clusters value"
printf 'default_cluster: x\n' >"$root/home/.marvel/config.yaml"; runout run
pyc 'import json,sys
d=json.load(open(sys.argv[1])); p=[x["data"] for x in d["writes"] if x["doc_id"]=="sessions" and x["collection"]=="dash_panels"][0]
assert p["rows"]==[] and p["refusal"].get("clusters:unread")==1, p' && ok "a config with no clusters key refuses the sessions panel" || bad "no clusters key"
setclusters alpha

# the area reader: the Desk snapshot's areas
runout run
grep -q '"area": "Ops"' "$root/out.json" && ok "an area on the Desk snapshot labels a workstream the area map points at" || bad "snapshot area used"
python3 - "$state/desk-snapshot.json" <<'P'
import json, sys
p = sys.argv[1]; s = json.load(open(p)); s["areas"] = ["Research"]; json.dump(s, open(p, "w"))
P
runout run
{ grep -q '"area": "Ops"' "$root/out.json" && bad "an area absent from the snapshot is not used" || ok "an area the snapshot does not list reads unlabelled, even when the map names it"; }
python3 - "$state/desk-snapshot.json" <<'P'
import json, sys
p = sys.argv[1]; s = json.load(open(p)); del s["areas"]; json.dump(s, open(p, "w"))
P
runout run
{ grep -q '"area": "Ops"\|"area": "Research"' "$root/out.json" && bad "no areas key labels nothing" || ok "a snapshot with no areas key labels nothing"; }
python3 - "$state/desk-snapshot.json" <<'P'
import json, sys
p = sys.argv[1]; s = json.load(open(p)); s["areas"] = ["Ops", "Research"]; json.dump(s, open(p, "w"))
P

# ---------------------------------------------------------------- the checks (the writer's half of test 9)
checks() { runout run; python3 -c '
import json, sys
d = json.load(open(sys.argv[1]))
m = [x["data"] for x in d["writes"] if x["collection"] == "dash_meta"][0]
print(" ".join("%s=%s" % (c["name"], "yes" if c["holds"] else "no") for c in m["checks"]))' "$root/out.json"; }
[[ "$(checks)" == "refresh-ran-today=yes desk-snapshot-exists=yes rows-past-idea-linked=yes" ]] && ok "all three checks hold on the base fixture" || bad "base checks" "$(checks)"
python3 - "$state/workstreams.jsonl" <<'PY'
import json, sys
lines = [l for l in open(sys.argv[1]) if '"refresh-run"' not in l]
open(sys.argv[1], "w").write("".join(lines))
PY
printf '{"ts":"2026-10-19T17:00:00Z","actor":"refresh","tag":"mirror","event":"refresh-run","at":"2026-10-19T17:00:00Z","rows":1,"links":0,"warnings":0}\n' >>"$state/workstreams.jsonl"
[[ "$(checks)" == *"refresh-ran-today=no"* ]] && ok "a newest refresh-run 25 hours old fails" || bad "25h old"
python3 - "$state/workstreams.jsonl" <<'PY'
import sys
l = [x for x in open(sys.argv[1]) if '"refresh-run"' not in x]
open(sys.argv[1], "w").write("".join(l) + '{"ts":"2026-10-19T19:00:00Z","actor":"refresh","tag":"mirror","event":"refresh-run","at":"2026-10-19T19:00:00Z","rows":1,"links":0,"warnings":0}\n')
PY
[[ "$(checks)" == *"refresh-ran-today=yes"* ]] && ok "23 hours old holds" || bad "23h old"
python3 - "$state/workstreams.jsonl" <<'PY'
import sys
l = [x for x in open(sys.argv[1]) if '"refresh-run"' not in x]
open(sys.argv[1], "w").write("".join(l) + '{"ts":"2026-10-20T19:00:00Z","actor":"refresh","tag":"mirror","event":"refresh-run","at":"2026-10-20T19:00:00Z","rows":1,"links":0,"warnings":0}\n')
PY
[[ "$(checks)" == *"refresh-ran-today=no"* ]] && ok "a run ahead of the clock fails" || bad "future run"
python3 - "$state/workstreams.jsonl" <<'PY'
import sys
l = [x for x in open(sys.argv[1]) if '"refresh-run"' not in x]
open(sys.argv[1], "w").write("".join(l))
PY
[[ "$(checks)" == *"refresh-ran-today=no"* ]] && ok "no run on record fails (never)" || bad "no run on record"
printf '{"ts":"2026-10-20T10:00:00Z","actor":"refresh","tag":"mirror","event":"refresh-run","at":"2026-10-20T10:00:00Z","rows":1,"links":0,"warnings":0}\n' >>"$state/workstreams.jsonl"
mv "$state/desk-snapshot.json" "$root/snap.keep"
[[ "$(checks)" == *"desk-snapshot-exists=no"* ]] && ok "a missing snapshot fails" || bad "missing snapshot"
echo '{not json' >"$state/desk-snapshot.json"
[[ "$(checks)" == *"desk-snapshot-exists=no"* ]] && ok "a snapshot that does not parse fails" || bad "unparseable snapshot"
mv "$root/snap.keep" "$state/desk-snapshot.json"
runout run
pyc 'import json,sys
d=json.load(open(sys.argv[1])); m=[x["data"] for x in d["writes"] if x["collection"]=="dash_meta"][0]
o={c["name"]:c["observed"] for c in m["checks"]}
assert o["desk-snapshot-exists"]=="exists" and o["rows-past-idea-linked"]=="0 rows past idea with no link" and o["refresh-ran-today"]=="2026-10-20T10:00:00Z", o' \
  && ok "observed is text the page may show: the refresh time, exists or missing, and a count of unlinked rows (no titles)" || bad "observed text"
printf '%s\n' "$(openev nolink-defined defined "a defined row" "" '[]' 2026-10-19T06:00:00Z)" >>"$state/workstreams.jsonl"
[[ "$(checks)" == *"rows-past-idea-linked=no"* ]] && ok "one defined row with no link fails the third check" || bad "defined no link"
sed '$d' "$state/workstreams.jsonl" >"$root/l2"; cp "$root/l2" "$state/workstreams.jsonl"
printf '%s\n' "$(openev nolink-parked parked "a parked row" "" '[]' 2026-10-19T06:00:00Z)" >>"$state/workstreams.jsonl"
[[ "$(checks)" == *"rows-past-idea-linked=no"* ]] && ok "one parked row with no link fails it too" || bad "parked no link"
sed '$d' "$state/workstreams.jsonl" >"$root/l2"; cp "$root/l2" "$state/workstreams.jsonl"
[[ "$(checks)" == *"rows-past-idea-linked=yes"* ]] && ok "idea rows with no link hold (the base fixture has two)" || bad "idea no link holds"

# ---------------------------------------------------------------- the meta, the daily docs, the floor, reading only
runout run
pyc 'import json,sys
d=json.load(open(sys.argv[1])); w=d["writes"]
m=[x for x in w if x["collection"]=="dash_meta"][0]["data"]
assert m["last_run"]=="2026-10-20T18:00:00Z" and m["runs"]==1 and isinstance(m["reader"],str), m
day=[x for x in w if x["collection"]=="dash_daily" and x["action"]=="set"]
assert sorted(x["doc_id"] for x in day)==["in-flight-2026-10-20","sessions-2026-10-20","waiting-2026-10-20","where-work-sits-2026-10-20"], [x["doc_id"] for x in day]
dele=sorted(x["doc_id"] for x in w if x["action"]=="delete")
assert dele==["in-flight-2026-10-06","sessions-2026-10-06","waiting-2026-10-06","where-work-sits-2026-10-06"], dele
assert all(set(x["data"])=={"panel","day","counts"} for x in day)' && ok "dash_meta carries last_run, runs and reader; daily counts are set for today and the day 14 days back is deleted" || bad "meta and daily"
before="$(cat "$state/workstreams.jsonl" "$state/desk-snapshot.json" | shasum -a 256)"
runout run
after="$(cat "$state/workstreams.jsonl" "$state/desk-snapshot.json" | shasum -a 256)"
[[ "$before" == "$after" ]] && ok "a run changes neither the ledger nor the snapshot" || bad "read only"
[[ ! -e "$state/dash-writer.json" ]] && ok "a run writes no state of its own (only ack does)" || bad "run writes state"
# every daily doc past 14 days is deleted, not only the one exactly 14 back; a batch never passes 50 writes
python3 - "$state/dash-writer.json" <<'PY'
import json, sys
json.dump({"last_run": "2026-10-20T10:00:00Z", "runs": 3, "days": ["2026-10-02", "2026-10-04", "2026-10-10", "2026-10-19"]}, open(sys.argv[1], "w"))
PY
runout run
pyc 'import json,sys
d=json.load(open(sys.argv[1])); dele=sorted(x["doc_id"] for x in d["writes"] if x["action"]=="delete")
want=sorted("%s-2026-10-%s" % (p, dd) for p in ("waiting","where-work-sits","in-flight","sessions") for dd in ("02","04","06"))
assert dele==want, dele' && ok "daily docs older than 14 days are all deleted (days on record plus the one 14 back), the newer ones kept" || bad "stale daily delete"
python3 - "$state/dash-writer.json" <<'PY'
import json, sys, datetime
d0 = datetime.date(2026, 9, 20)
days = [(d0 + datetime.timedelta(days=i)).isoformat() for i in range(17)] + ["2026-10-19"]
json.dump({"last_run": "2026-10-20T10:00:00Z", "runs": 3, "days": days}, open(sys.argv[1], "w"))
PY
runout run
pyc 'import json,sys
d=json.load(open(sys.argv[1])); w=d["writes"]; dele=sorted(x["doc_id"] for x in w if x["action"]=="delete")
assert "refused" not in d and len(w)==49, (len(w), d.get("refused"))
assert len(dele)==40, dele
assert not [x for x in dele if x.endswith("-2026-09-30") or x.endswith("-2026-10-06")], "the oldest ten days go first, the rest wait"' && ok "a long backlog is cut to the oldest ten days: 49 writes, never over 50, never refused" || bad "backlog cap"
run ack >/dev/null 2>&1
python3 -c 'import json,sys;d=json.load(open(sys.argv[1]));assert d["days"]==["2026-09-30","2026-10-01","2026-10-02","2026-10-03","2026-10-04","2026-10-05","2026-10-06","2026-10-19","2026-10-20"], d["days"]' "$state/dash-writer.json" && ok "ack drops from its record only the days it was told to delete, and adds today" || bad "ack days" "$(cat "$state/dash-writer.json")"
rm -f "$state/dash-writer.json"
run ack >/dev/null 2>&1
[[ "$(python3 -c 'import json,sys;d=json.load(open(sys.argv[1]));print(d["runs"],d["last_run"])' "$state/dash-writer.json")" == "1 2026-10-20T18:00:00Z" ]] && ok "ack records the run and its count" || bad "ack"
NOW_OVERRIDE="2026-10-20T18:20:00Z" run run >"$root/out.json" 2>/dev/null
pyc 'import json,sys;d=json.load(open(sys.argv[1]));assert d["writes"]==[] and "30-minute" in d["skipped"], d' && ok "inside the 30-minute floor it writes nothing and says why" || bad "floor"
NOW_OVERRIDE="2026-10-20T18:20:00Z" run run --force >"$root/out.json" 2>/dev/null
pyc 'import json,sys;d=json.load(open(sys.argv[1]));assert d["writes"]' && ok "--force runs inside the floor" || bad "--force"
NOW_OVERRIDE="2026-10-20T18:31:00Z" run run >"$root/out.json" 2>/dev/null
pyc 'import json,sys;d=json.load(open(sys.argv[1]));m=[x["data"] for x in d["writes"] if x["collection"]=="dash_meta"][0];assert m["runs"]==2 and m["last_run"]=="2026-10-20T18:31:00Z", m' && ok "after the floor it runs again and the count moves to 2" || bad "after the floor"
rm -f "$state/dash-writer.json"

# ---------------------------------------------------------------- the withhold file and the size cap
# a withhold file that cannot be trusted refuses every panel that carries a title, an id or a session name
refused3() { # $1 reason: the three filtered panels hold no rows and one withhold-file refusal; where-work-sits still draws
  pyc 'import json,sys
d=json.load(open(sys.argv[1])); P={x["doc_id"]:x["data"] for x in d["writes"] if x["collection"]=="dash_panels"}
for n in ("waiting","in-flight","sessions"):
    assert P[n]["rows"]==[] and P[n]["refusal"].get("withhold-file:'"$1"'")==1 and P[n]["counts"]=={}, (n, P[n]["rows"][:1], P[n]["refusal"], P[n]["counts"])
assert P["where-work-sits"]["counts"]["building"]>0, P["where-work-sits"]["counts"]'
}
chmod 644 "$state/dash-withhold.txt"; runout run
refused3 mode && ok "a withhold file wider than 0600 refuses waiting, in-flight and sessions: no rows, no counts, one withhold-file refusal" || bad "withhold mode"
grep -qi bartholomew "$root/out.json" && bad "no withheld name in ids or agent names under a wide file" || ok "no withheld name appears anywhere (ids, titles, agent names) under a wide file"
chmod 600 "$state/dash-withhold.txt"; runout run
grep -qi bartholomew "$root/out.json" && bad "control withhold" || ok "control: with a 0600 file the withheld name is refused id by id and never printed"
chmod 000 "$state/dash-withhold.txt"
if [[ -r "$state/dash-withhold.txt" ]]; then skp "an unreadable withhold file: this user can read a mode 000 file"
else runout run; refused3 unreadable && ok "a withhold file present but unreadable (mode 000) refuses the panels it filters, never reads as no list" || bad "withhold mode 000"; fi
chmod 600 "$state/dash-withhold.txt"
mv "$state/dash-withhold.txt" "$root/w.keep"; mkdir "$state/dash-withhold.txt"; runout run
refused3 unreadable && ok "a directory in place of the withhold file refuses the panels it filters" || bad "withhold directory"
rmdir "$state/dash-withhold.txt"
printf '\xff\xfe Bartholomew\n' >"$state/dash-withhold.txt"; chmod 600 "$state/dash-withhold.txt"; runout run; rc=$?
[[ $rc -eq 0 ]] && ! grep -q Traceback "$root/err" && refused3 unreadable && ok "a withhold file that is not UTF-8 refuses the panels it filters, exit 0, no traceback" || bad "withhold not utf-8" "rc=$rc $(head -2 "$root/err")"
rm -f "$state/dash-withhold.txt"; runout run
pyc 'import json,sys
d=json.load(open(sys.argv[1])); p=[x["data"] for x in d["writes"] if x["doc_id"]=="in-flight" and x["collection"]=="dash_panels"][0]
assert p["rows"] and not [k for k in p["refusal"] if k.startswith("withhold-file")], p["refusal"]' && ok "control: an absent withhold file is no list, not a refusal" || bad "absent withhold file"
mv "$root/w.keep" "$state/dash-withhold.txt"; chmod 600 "$state/dash-withhold.txt"
python3 - "$state/workstreams.jsonl" <<'PY'
import json, sys
with open(sys.argv[1], "a") as f:
    for i in range(2600):
        f.write(json.dumps({"ts": "2026-10-19T06:00:00Z", "actor": "director", "tag": "judgment", "event": "open", "ws": "bulk-%d" % i,
                            "title": "t" * 119, "stage": "building", "owner": "o", "links": ["pr:o/r#%d" % i]}) + "\n")
PY
runout run
pyc 'import json,sys
d=json.load(open(sys.argv[1])); p=[x for x in d["writes"] if x["collection"]=="dash_panels" and x["doc_id"]=="in-flight"][0]["data"]
assert p["rows"]==[] and p["refusal"].get("panel:over-256KiB")==1, (len(p["rows"]), p["refusal"])
assert len(json.dumps(d).encode()) > 0' && ok "a panel over 256 KiB is written with no rows and a refusal, not cut short" || bad "size cap"

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
