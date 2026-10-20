#!/usr/bin/env bash
# Prove `introspect` (the director introspector, a script and not a seat) against the party outcome
# of 2026-10-20 (introspector party 5ptdd): pull-only, at most 7 lines, each count anchored, no false
# zero (a source that cannot see prints a GAP line), one record file with fixed fields and modes
# 700/600, nothing quoted from a source, one band per line. Values the outcome leaves open are
# provisional here, not ruled: the denial band (2000 results, 2x the weekday mean), the disk band
# (10 percent) and the sell-by date 2026-11-17.
#
# Every run is under `env -i` with a scratch HOME, DIRECTOR_STATE and TMPDIR, no MARVEL_SOCKET, and
# fixture copies of git, marvel, go and director-mcp first on PATH. The real ~/.director and
# ~/.marvel are never read or written. Written for bash 3.2 and bash 5 (no associative arrays).
#
# Usage: skills/director/scripts/verify-introspect.sh   Exit nonzero on any miss.
set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
S="${INTROSPECT_BIN:-$here/introspect}"
repo="$(cd "$here/../../.." && pwd)"
pass=0; fail=0; skip=0
ok()   { echo "PASS $1"; pass=$((pass+1)); }
bad()  { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }
skp()  { echo "SKIP $1"; skip=$((skip+1)); }
has()  { grep -q -- "$1" "$2"; }

[[ -f "$S" ]] || { bad "introspect exists at $S"; echo "$pass passed, $fail failed"; exit 1; }

# A short scratch path (macOS socket limit) that exists on macOS and Linux; a failed mktemp stops the run,
# so no path ever collapses to "/".
root="$(mktemp -d "${INTROSPECT_TMP:-/tmp}/ins.XXXXXX")" || { echo "FAIL scratch root: mktemp failed"; exit 1; }
[[ -n "$root" && -d "$root" ]] || { echo "FAIL scratch root: no directory"; exit 1; }
root="$(cd "$root" && pwd -P)"
modeof() { stat -c %a "$1" 2>/dev/null || stat -f %Lp "$1"; }
trap 'rm -rf "$root"' EXIT
mkdir -p "$root/home" "$root/t" "$root/bin" "$root/proj/-work-aae-orc" "$root/proj/-work-client-x" "$root/state"
PYDIR="$(dirname "$(command -v python3)")"
BASEPATH="/usr/bin:/bin:$PYDIR"
FIXPATH="$root/bin:$BASEPATH"
NOW="2026-10-20T18:44:00Z"
state="$root/state"
conf="$root/c.conf"

genv() { env -i HOME="$root/home" PATH="$BASEPATH" LANG=en_US.UTF-8 GIT_CONFIG_NOSYSTEM=1 "$@"; }
run()  { env -i HOME="$root/home" PATH="$FIXPATH" LANG=en_US.UTF-8 TMPDIR="$root/t" DIRECTOR_STATE="$state" INTROSPECT_NOW="${NOW_OVERRIDE:-$NOW}" python3 -I "$S" "$@"; }

# ---------------------------------------------------------------- fixture generator
cat >"$root/mkfix.py" <<'PY'
import json, sys, argparse
ap = argparse.ArgumentParser()
ap.add_argument("path"); ap.add_argument("--agent"); ap.add_argument("--day", default="2026-10-20")
ap.add_argument("--ok", type=int, default=0); ap.add_argument("--den", type=int, default=0)
ap.add_argument("--mid", type=int, default=0); ap.add_argument("--err", type=int, default=0)
ap.add_argument("--plainerr", type=int, default=0); ap.add_argument("--secret", action="store_true")
ap.add_argument("--sends", type=int, default=0); ap.add_argument("--to", default="global://mokuzai/supervisor")
ap.add_argument("--sendtool", default="mcp__director__send_message"); ap.add_argument("--badline", action="store_true")
ap.add_argument("--lag", type=int, default=0); ap.add_argument("--fakelag", type=int, default=0)
ap.add_argument("--secrets", action="store_true")
a = ap.parse_args()
DEN = "Permission for this action was denied by the Claude Code auto mode classifier"
n = [0]
def ts(): n[0] += 1; return "%sT10:%02d:%02d.000Z" % (a.day, (n[0] // 60) % 60, n[0] % 60)
def pair(f, name, content, is_error=False, inp=None):
    i = "t%d" % n[0]
    f.write(json.dumps({"timestamp": ts(), "message": {"content": [{"type": "tool_use", "id": i, "name": name, "input": inp or {}}]}}) + "\n")
    r = {"type": "tool_result", "tool_use_id": i, "content": content}
    if is_error: r["is_error"] = True
    f.write(json.dumps({"timestamp": ts(), "message": {"content": [r]}}) + "\n")
with open(a.path, "w") as f:
    if a.agent:
        f.write(json.dumps({"type": "agent-name", "agentName": a.agent, "timestamp": ts()}) + "\n")
    for _ in range(a.ok): pair(f, "Bash", "fine")
    for _ in range(a.den): pair(f, "Bash", DEN + ". Reason: [x] CANARY-QQ-9731")
    for _ in range(a.mid): pair(f, "Bash", "the log said: " + DEN)
    for _ in range(a.err): pair(f, "mcp__director__wait_for_message", "nats: consumer not found", True)
    for _ in range(a.plainerr): pair(f, "mcp__director__inbox_summary", "note: consumer not found was seen last week")
    for _ in range(a.sends): pair(f, a.sendtool, "accepted", False, {"to": a.to, "text": "see /Users/someone/work/x"})
    if a.badline:
        f.write('{"timestamp": "%sT10:30:00.000Z", "message": {"content": [{"type": "tool_use", "id": "z", "name": "mcp__director__send_message", "input": {"to": "glo\n' % a.day)
    def wait(sent, data):
        body = {"count": 1, "messages": [{"message": {"sent_at": sent, "content": {"type": "text", "data": data}}, "tier": "local"}]}
        pair(f, "mcp__director__wait_for_message", json.dumps(body))
    for _ in range(a.lag): wait("%sT07:00:00Z" % a.day, "plain")
    # a content field that carries text shaped like an envelope: it must never be read as one
    fake = '{"sent_at": "%sT01:00:00Z"}' % a.day
    for _ in range(a.fakelag): wait("%sT09:59:59Z" % a.day, 'x "sent_at": "%sT01:00:00Z" ' % a.day + fake.replace('"', '\\"'))
    if a.secrets:
        pair(f, "Bash", "o " + "ghp_" + "Zq7" * 12)
        pair(f, "Bash", "o " + "SA" + "B2" * 28)
        pair(f, "Bash", "o -----BEGIN " + "RSA PRIVATE" + " KEY-----")
    if a.secret:
        key = "AKIA" + "ABCDEFGH" + "IJKLMNOP"
        pair(f, "Bash", "output " + key)
PY
mk() { python3 "$root/mkfix.py" "$@"; }

# git fixtures: a checkout whose origin/main is ahead of main by commits that touch code, and by a
# docs-only and a test-only commit that must not count
gcommit() { genv git -C "$1" -c user.name=t -c user.email=t@t.invalid commit -q --allow-empty-message -m "$2"; }
mkco() { # $1 dir, $2 code commits ahead
  d="$1"; mkdir -p "$d/cmd/director-mcp" "$d/docs"; genv git -C "$d" init -q -b main
  echo a >"$d/cmd/director-mcp/a.go"; genv git -C "$d" add -A; gcommit "$d" c1
  genv git -C "$d" rev-parse HEAD >"$d.c1"
  genv git -C "$d" checkout -q -b up
  i=0; while [[ $i -lt $2 ]]; do echo "$i" >>"$d/cmd/director-mcp/a.go"; genv git -C "$d" add -A; gcommit "$d" "code$i"; i=$((i+1)); done
  echo t >"$d/cmd/director-mcp/a_test.go"; genv git -C "$d" add -A; gcommit "$d" testonly
  echo t >"$d/docs/d.md"; genv git -C "$d" add -A; gcommit "$d" docsonly
  genv git -C "$d" update-ref refs/remotes/origin/main "$(genv git -C "$d" rev-parse up)"
  genv git -C "$d" checkout -q main
  touch "$d/.git/FETCH_HEAD"
}
mkco "$root/co-director" 3
mkco "$root/co-marvel" 2
C1="$(cat "$root/co-director.c1")"
M1="$(cat "$root/co-marvel.c1")"
cat >"$root/bin/marvel" <<SH
#!/bin/sh
echo "marvel version $M1"
SH
cat >"$root/bin/go" <<SH
#!/bin/sh
printf '%s: go1.24\n\tbuild\tvcs.revision=$C1\n' "\$3"
SH
chmod +x "$root/bin/marvel" "$root/bin/go"
echo binary >"$root/bin/director-mcp-bin"
cat >"$root/bin/director-mcp" <<'SH'
#!/bin/sh
# fixture director-mcp: `asks --json` prints rows; ASKS_MODE=absent mimics a build without the subcommand
d="$(dirname "$0")"
if [ "$1" = asks ] && [ "$(cat "$d/asks.mode" 2>/dev/null || echo ok)" = ok ]; then
  cat "$d/asks.json"; exit 0
fi
echo "unknown command" >&2; exit 2
SH
chmod +x "$root/bin/director-mcp"
mkdir -p "$root/pages" "$root/ledgers"
utc() { python3 -c 'import os,sys,calendar,time;t=calendar.timegm(time.strptime(sys.argv[2],"%Y-%m-%dT%H:%M"));os.utime(sys.argv[1],(t,t))' "$1" "$2"; }
echo page >"$root/pages/stalled-streams.md"; utc "$root/pages/stalled-streams.md" 2026-10-15T22:40
echo '{}' >"$root/ledgers/workstreams.jsonl"; utc "$root/ledgers/workstreams.jsonl" 2026-10-16T09:00
cat >"$root/bin/asks.json" <<'J'
{"generated_at":"2026-10-20T18:44:00Z","rows":[
 {"ask":"01M5AAAAAAAAAAAAAAAAAAAAAA","state":"sent","sent_at":"2026-10-20T17:00:00Z","owner":{"role":"builder"}},
 {"ask":"01M5BBBBBBBBBBBBBBBBBBBBBB","state":"acked","sent_at":"2026-10-20T10:00:00Z","owner":{"role":"builder"}},
 {"ask":"01M5CCCCCCCCCCCCCCCCCCCCCC","state":"sent","sent_at":"2026-10-20T18:40:00Z","owner":{"role":"builder"}}]}
J

mkdir -p "$state/introspector"; chmod 700 "$state/introspector"
writeconf() { # extra lines on stdin
  {
    echo "projects_dir=$root/proj"
    echo "project_include=aae-orc"
    echo "exclude_agent_names=research-supervisor,introspector,retrospector"
    echo "checkout.director=$root/co-director"
    echo "checkout.marvel=$root/co-marvel"
    echo "install.director-mcp=$root/bin/director-mcp-bin"
    echo "director_mcp=$root/bin/director-mcp"
    echo "stalled_page_dir=$root/pages"
    echo "ledgers=$root/ledgers/workstreams.jsonl"
    echo "hub_volume=$root"
    echo "state_dir=$state"
    echo "host=kinu"
    echo "hosts=kinu,mokuzai,corporate"
    echo "roles=operator,builder,director"
    echo "band.sell_by=2026-11-17"
    echo "band.disk_free_pct=0"
    cat
  } >"$conf"
}
echo ok >"$root/bin/asks.mode"

# a director session with a real denial, plus a research transcript that quotes one (P7)
mk "$root/proj/-work-aae-orc/s1.jsonl" --agent director --ok 40 --den 1 --mid 1 --err 2 --plainerr 1 --secret
mk "$root/proj/-work-aae-orc/s2.jsonl" --agent research-supervisor --ok 5 --den 1
mk "$root/proj/-work-client-x/s3.jsonl" --agent director --ok 5 --den 4
writeconf </dev/null

# ================================================================ usage
run --bogus >/dev/null 2>"$root/err"; rc=$?
[[ $rc -eq 2 ]] && ok "an unknown argument exits 2, a usage error" || bad "an unknown argument exits 2" "rc=$rc"
run --help >"$root/o" 2>&1; rc=$?
[[ $rc -eq 0 ]] && grep -q -e '--since' "$root/o" && ok "--help exits 0 and names --since" || bad "--help exits 0" "rc=$rc"
run --since nonsense --config "$conf" >/dev/null 2>&1; rc=$?
[[ $rc -eq 2 ]] && ok "an unreadable --since is a usage error" || bad "a bad --since exits 2" "rc=$rc"

# ================================================================ the block
run --since today --config "$conf" --no-append >"$root/o" 2>"$root/err"; rc=$?
[[ $rc -eq 0 ]] && ok "a reading exits 0" || bad "a reading exits 0" "rc=$rc $(head -3 "$root/err")"
hdr="$(sed -n 1p "$root/o")"
case "$hdr" in "introspect kinu since 00:00Z rev "*) ok "the header names host, window and rev";; *) bad "header form" "$hdr";; esac
case "$hdr" in *"not covered: mokuzai, corporate"*) ok "the header names the hosts it does not cover";; *) bad "hosts not covered" "$hdr";; esac
case "$hdr" in *"excluded 1/2"*) ok "the header counts excluded transcripts (research out; a non-included project is not read)";; *) bad "excluded count" "$hdr";; esac
case "$hdr" in *"provisional until 2026-11-17"*) ok "the header says the bands are provisional before the sell-by date";; *) bad "provisional marker" "$hdr";; esac
case "$hdr" in *"behind origin/main"*"fetched "*) ok "the header says how far the script is behind origin/main and when it was fetched";; *) bad "behind/fetched" "$hdr";; esac
body="$root/body"; sed 1d "$root/o" >"$body"
[[ "$(grep -c . "$body")" -le 7 ]] && ok "at most 7 lines after the header, footers included" || bad "at most 7 lines" "$(grep -c . "$body")"
badline="$(grep -v -E '^(denials|bus-errors|not-installed|disk|stale-page|asks|weekly|GAP [a-z-]+|SECRET-SHAPE |REJECT |\\+[0-9]+ more)' "$body" || true)"
[[ -z "$badline" ]] && ok "every line is a known id or a GAP line" || bad "closed output" "$badline"
[[ "$(grep -c -E '\?|should|propose|recommend|rule' "$root/o" || true)" == 0 ]] && ok "P5 nothing in the output asks for an answer" || bad "P5 the output asks for an answer" "$(grep -n -E '\?|should|propose|recommend|rule' "$root/o")"
[[ "$(grep -E '^[a-zA-Z-]+ ' "$body" | grep -c -v -E '^GAP|SECRET-SHAPE|REJECT')" -ge 1 ]] && ok "a shown line carries an owner" || bad "owner on lines"
grep -E '^(denials|bus-errors|not-installed|disk|stale-page|asks|weekly)' "$body" | grep -q -v '| owner: ' && bad "each fact line names an owner by role" || ok "each fact line names an owner by role"

# every fact line carries an id field: the natural one where it has one, `-` where it has none
grep -E '^(denials|bus-errors|not-installed|disk|stale-page|asks|weekly) ' "$root/o" | grep -v -q -E ' \| id: [^ |]+ \| remedy: [a-z-]+ \| owner: [a-z]+$' && bad "every fact line carries id, remedy and owner fields" || ok "every fact line carries id, remedy and owner fields"
grep '^not-installed ' "$body" | grep -q '| id: [0-9a-f]\{7,40\} |' && ok "not-installed carries a revision as its id" || bad "not-installed id" "$(grep '^not-installed' "$body")"
grep '^bus-errors ' "$body" | grep -q '| id: - |' && ok "a line with no natural id prints - as its id" || bad "bus-errors id -" "$(grep '^bus-errors' "$body")"

# P7 and anchoring: the director session has 1 real denial and 1 mid-text; the research transcript's is not counted
grep -q '^GAP denials .*under 2000' "$body" && ok "denials with fewer than 2000 results is a GAP, never a zero" || bad "denials GAP below 2000 results" "$(grep denials "$body")"
run --since today --config "$conf" --no-append --json >"$root/j" 2>/dev/null
python3 - "$root/j" <<'PY' && ok "P7 --json: 1 denial counted for the director, 1 research denial set aside, the mid-text one never counted" || bad "P7 counts" "$(head -c 600 "$root/j")"
import json, sys
d = json.load(open(sys.argv[1]))
c = d["counts"]
assert c["denials"] == 1 and c["denials_research"] == 1, c
assert c["denials_substring"] == 2, c
assert c["bus_errors"] == 2, c
assert c["results"] == 40 + 1 + 1 + 2 + 1 + 1, c
PY
grep -q '^bus-errors  *2 ' "$body" && ok "bus errors count only is_error director results with the phrase (2, not the plain mention)" || bad "bus-errors 2" "$(grep bus "$body")"
grep -q 'CANARY' "$root/o" && bad "nothing is quoted from a source" || ok "nothing is quoted from a source (the canary in a denial never appears)"
grep -q '^SECRET-SHAPE aws-key 1 files kinu$' "$body" && ok "a secret shape prints as a shape, a file count and a host only" || bad "SECRET-SHAPE line" "$(grep SECRET "$body")"
grep -q 'ABCDEFGH' "$root/o" && bad "no value or prefix of a secret" || ok "no value or prefix of a secret in the output"

# bus errors GAP when no director tool call
rm -f "$root/proj/-work-aae-orc/s1.jsonl"
run --since today --config "$conf" --no-append >"$root/o" 2>/dev/null
grep -q '^GAP bus-errors' "$root/o" && ok "no director tool call that day is a GAP for bus-errors, not 0" || bad "bus-errors GAP" "$(grep bus "$root/o")"
mk "$root/proj/-work-aae-orc/s1.jsonl" --agent director --ok 40 --den 1 --mid 1 --err 2 --plainerr 1 --secret

# not-installed
grep -q '^not-installed ' "$body" && ok "not-installed shows when something is behind on a code path" || bad "not-installed shows" "$(grep -c . "$body")"
grep '^not-installed ' "$body" | grep -q 'director-mcp .* 3 behind' && ok "director-mcp is 3 behind (the test-only and docs-only commits do not count)" || bad "director-mcp 3 behind" "$(grep '^not-installed' "$body")"
grep '^not-installed ' "$body" | grep -q 'marvel .* 2 behind' && ok "marvel is 2 behind" || bad "marvel 2 behind" "$(grep '^not-installed' "$body")"
utc "$root/co-director/.git/FETCH_HEAD" 2026-10-17T00:00
run --since today --config "$conf" --no-append >"$root/o" 2>/dev/null
grep -q '^GAP not-installed' "$root/o" && ok "origin/main not fetched within 24h is a GAP" || bad "stale fetch GAP" "$(grep -i install "$root/o")"
touch "$root/co-director/.git/FETCH_HEAD"

# disk
writeconf <<<"band.disk_free_pct=101"
run --since today --config "$conf" --no-append >"$root/o" 2>/dev/null
grep -q '^disk  *[0-9]*% free' "$root/o" && ok "disk shows when free space is under the band" || bad "disk shows" "$(grep disk "$root/o")"
writeconf <<<"band.disk_free_pct=0"
run --since today --config "$conf" --no-append >"$root/o" 2>/dev/null
grep -q '^disk' "$root/o" && bad "disk hidden when above the band" || ok "disk hidden when above the band"
writeconf <<<"hub_volume=$root/does-not-exist"
run --since today --config "$conf" --no-append >"$root/o" 2>/dev/null
grep -q '^GAP disk' "$root/o" && ok "an unreadable volume is a GAP" || bad "disk GAP" "$(grep -i disk "$root/o")"
writeconf </dev/null

# stale-page and asks
run --since today --config "$conf" --no-append >"$root/o" 2>/dev/null
grep '^stale-page ' "$root/o" | grep -q 'written 10-15 22:40' && ok "stale-page names when the page was written" || bad "stale-page" "$(grep -i stale "$root/o")"
grep '^stale-page ' "$root/o" | grep -q '[0-9][0-9]* director commits since; ledger written 4d ago' && ok "stale-page counts commits since the page and the ledger age in days" || bad "stale-page commits and ledger age" "$(grep '^stale-page' "$root/o")"
grep -q '^asks  *1 ' "$root/o" && ok "asks counts REQUESTs unacknowledged past 15 minutes (1 of 2 sent)" || bad "asks 1" "$(grep ask "$root/o")"
echo absent >"$root/bin/asks.mode"
run --since today --config "$conf" --no-append >"$root/o" 2>/dev/null
grep -q '^GAP asks' "$root/o" && ok "a director-mcp with no asks subcommand is a GAP" || bad "asks GAP" "$(grep -i ask "$root/o")"
echo ok >"$root/bin/asks.mode"
mkdir -p "$root/emptypages"; writeconf <<<"stalled_page_dir=$root/emptypages"
run --since today --config "$conf" --no-append >"$root/o" 2>/dev/null
grep -q '^GAP stale-page' "$root/o" && ok "an empty page directory is a GAP" || bad "stale-page GAP" "$(grep -i stale "$root/o")"
writeconf </dev/null

# weekly appears only on --window week
run --since today --config "$conf" --no-append >"$root/o" 2>/dev/null
grep -q '^weekly' "$root/o" && bad "weekly only at harvest" || ok "no weekly line on a sweep run"
mk "$root/proj/-work-aae-orc/s4.jsonl" --agent director --ok 3 --sends 2 --to global://mokuzai/supervisor
run --since today --window week --config "$conf" --no-append >"$root/o" 2>/dev/null
grep -q '^weekly ' "$root/o" && ok "the weekly line appears with --window week" || bad "weekly line" "$(tail -3 "$root/o")"
grep '^weekly ' "$root/o" | grep -q 'host-local paths 2' && ok "weekly counts host-local paths in sends as a number" || bad "host-local count" "$(grep '^weekly' "$root/o")"
# the host-local-path count: send_message inputs only, "another host" from the `to` address only
hl() { run --since today --window week --config "$conf" --no-append "$@" >"$root/o" 2>/dev/null; grep '^weekly ' "$root/o" | sed -n 's/.*host-local paths \([^;]*\);.*/\1/p'; }
hlcase() { # name, expected, mk args...
  nm="$1"; want="$2"; shift 2
  mk "$root/proj/-work-aae-orc/s4.jsonl" --agent director --ok 3 "$@"
  got="$(hl)"; [[ "$got" == "$want" ]] && ok "host-local paths: $nm ($want)" || bad "host-local paths: $nm" "want $want got $got"
}
hlcase "a send to a cluster that is another host counts" 2 --sends 2 --to global://mokuzai/supervisor
hlcase "a send to this host's own cluster does not" 0 --sends 2 --to global://kinu/supervisor
hlcase "a send to an agent address does not" 0 --sends 2 --to agent://arcaven/arcaven-supervisor-g5-0
hlcase "global://director counts only when director is off this host (here it is not)" 0 --sends 2 --to global://director
hlcase "a tool that merely starts with send does not" 0 --sends 2 --sendtool mcp__director__send_digest
hlcase "a broadcast input does not" 0 --sends 2 --sendtool mcp__director__broadcast
writeconf <<<"director_host=mokuzai"
hlcase "global://director counts when director_host is another host" 2 --sends 2 --to global://director
writeconf </dev/null
cat >"$root/mkres.py" <<'PY'
import json, sys
p, day = sys.argv[1:3]
with open(p, "w") as f:
    f.write(json.dumps({"timestamp": day + "T10:00:01.000Z", "message": {"content": [{"type": "tool_use", "id": "r1", "name": "mcp__director__wait_for_message", "input": {}}]}}) + "\n")
    f.write(json.dumps({"timestamp": day + "T10:00:02.000Z", "message": {"content": [{"type": "tool_result", "tool_use_id": "r1", "content": json.dumps({"messages": [{"message": {"sent_at": day + "T10:00:00Z", "content": {"data": "see /Users/someone/work/x"}}}]})}]}}) + "\n")
PY
python3 "$root/mkres.py" "$root/proj/-work-aae-orc/s4.jsonl" 2026-10-20
got="$(hl)"; [[ "$got" == "0" ]] && ok "host-local paths: a path inside a delivered message is never matched (0)" || bad "delivered content matched" "got $got"
mk "$root/proj/-work-aae-orc/s5.jsonl" --agent research-supervisor --ok 3 --sends 2 --to global://mokuzai/supervisor
hlcase "a dropped (research) transcript's sends are not counted" 0
rm -f "$root/proj/-work-aae-orc/s5.jsonl"
mk "$root/proj/-work-aae-orc/s4.jsonl" --agent director --ok 3 --sends 1 --to global://mokuzai/supervisor --badline
got="$(hl)"; [[ "$got" == "GAP"* ]] && ok "an unreadable send line makes the host-local field a GAP, never a count" || bad "unreadable shape GAP" "got $got"
run --since today --window week --config "$conf" --json >"$root/j" 2>/dev/null
python3 - "$state/introspector/records.jsonl" <<'PY' && ok "and the weekly record is a gap with no n" || bad "weekly gap record"
import json, sys
r = [json.loads(l) for l in open(sys.argv[1])]
w = [x for x in r if x["line"] == "weekly"][-1]
assert w["state"] == "gap" and "n" not in w, w
PY
rm -f "$root/proj/-work-aae-orc/s4.jsonl"
[[ "$(sed 1d "$root/o" | grep -c .)" -le 7 ]] && ok "the weekly run is still at most 7 lines, footers included" || bad "weekly at most 7 lines" "$(cat "$root/o")"

# denials band: 2400 results, 20 denials is 8.3 per 1000; baseline seeded at about 2.0 per 1000
mkdir -p "$root/proj/-work-aae-orc"
mk "$root/proj/-work-aae-orc/big.jsonl" --agent director --ok 2380 --den 20
python3 - "$state/introspector/records.jsonl" <<'PY'
import json, sys
rows = []
for day in ("2026-10-13", "2026-10-14", "2026-10-15", "2026-10-16", "2026-10-19"):
    rows.append({"v": 1, "rev": "0000000", "host": "kinu", "from": day + "T00:00Z", "to": day + "T23:59Z", "line": "denials",
                 "state": "count", "n": 4, "d": 2000, "band": "none", "remedy": "none", "owner": "operator", "ids": []})
open(sys.argv[1], "w").write("".join(json.dumps(r) + "\n" for r in rows))
PY
chmod 600 "$state/introspector/records.jsonl"
run --since today --config "$conf" --no-append >"$root/o" 2>/dev/null
grep '^denials ' "$root/o" | grep -q 'of 2' && ok "denials shows at 2000 results or more and 2x the weekday mean" || bad "denials band shows" "$(grep -i denial "$root/o")"
grep '^denials ' "$root/o" | grep -q '(provisional)' && ok "the denial band prints as provisional before the sell-by date" || bad "denials provisional" "$(grep '^denials' "$root/o")"
grep '^denials ' "$root/o" | grep -q 'remedy: operator-grant | owner: operator$' && ok "a denial line carries one remedy field and one owner by role, never \"X vs Y\"" || bad "denial remedy" "$(grep '^denials' "$root/o")"
NOW_OVERRIDE="2026-11-18T18:44:00Z" run --since 2026-10-20 --config "$conf" --no-append >"$root/o" 2>/dev/null
grep -q '^denials ' "$root/o" && ok "control: the denials line still shows in that run" || bad "control: denials shows past sell-by" "$(grep -i denial "$root/o")"
grep '^denials ' "$root/o" | grep -q provisional && bad "past sell-by the marker goes" || ok "past the sell-by date the provisional marker is gone"
sed -n 1p "$root/o" | grep -q 'past sell-by' && ok "the header says the bands are past their sell-by date" || bad "header past sell-by" "$(sed -n 1p "$root/o")"
rm -f "$root/proj/-work-aae-orc/big.jsonl"
mk "$root/proj/-work-aae-orc/big.jsonl" --agent director --ok 2380 --den 2
run --since today --config "$conf" --no-append >"$root/o" 2>/dev/null
grep -q '^denials' "$root/o" && bad "denials hidden under 2x the mean" || ok "denials hidden when the rate is under 2x the weekday mean"
rm -f "$root/proj/-work-aae-orc/big.jsonl"
# a high rate on a thin day is a GAP even with a baseline on file: the sample is too small to read
mk "$root/proj/-work-aae-orc/thin.jsonl" --agent director --ok 100 --den 50
run --since today --config "$conf" --no-append >"$root/o" 2>/dev/null
grep -q '^GAP denials .*under 2000' "$root/o" && ! grep -q '^denials ' "$root/o" && ok "a thin day with a high rate is a GAP, never a denials line" || bad "thin day" "$(grep -i denial "$root/o")"
rm -f "$root/proj/-work-aae-orc/thin.jsonl"

# ================================================================ records (P2, schema, modes)
rm -f "$state/introspector/records.jsonl"
run --since today --config "$conf" >"$root/o" 2>"$root/err"; rc=$?
rec="$state/introspector/records.jsonl"
[[ $rc -eq 0 && -s "$rec" ]] && ok "a run appends typed records to records.jsonl" || bad "records appended" "rc=$rc $(head -2 "$root/err")"
[[ "$(modeof "$state/introspector")" == 700 && "$(modeof "$rec")" == 600 ]] && ok "P2 the directory is 700 and the file 600" || bad "modes" "$(modeof "$state/introspector") $(modeof "$rec")"
genv git -C "$state/introspector" rev-parse >/dev/null 2>&1 && bad "P2 the record directory is outside any checkout" || ok "P2 the record directory is outside any checkout"
python3 - "$rec" <<'PY' && ok "every record matches the typed schema (v, rev, host, from, to, line, state; a gap has no n)" || bad "record schema"
import json, re, sys
LINES = {"denials","bus-errors","not-installed","disk","stale-page","asks","weekly","SECRET-SHAPE","REJECT"}
ALLOWED = {"v","rev","host","from","to","line","state","n","d","band","cat","tool","remedy","owner","ids","sess"}
for l in open(sys.argv[1]):
    r = json.loads(l)
    assert set(r) <= ALLOWED, r
    assert r["v"] == 1 and re.fullmatch(r"[0-9a-f]{7,40}", r["rev"]), r
    assert r["line"] in LINES and r["state"] in ("count", "gap"), r
    assert re.fullmatch(r"\d{4}-\d\d-\d\dT\d\d:\d\dZ", r["from"]) and re.fullmatch(r"\d{4}-\d\d-\d\dT\d\d:\d\dZ", r["to"]), r
    if r["state"] == "gap": assert "n" not in r, r
    for v in r.values():
        assert not (isinstance(v, str) and ("/" in v or "~" in v) and not re.search(r"ArcavenAE/\w", v)), r
    assert len(r.get("ids", [])) <= 3, r
PY
grep -q CANARY "$rec" && bad "records hold no quoted text" || ok "records hold fixed fields only (no canary)"
before="$(wc -l <"$rec")"
run --since today --config "$conf" --no-append >/dev/null 2>&1
[[ "$(wc -l <"$rec")" == "$before" ]] && ok "--no-append writes nothing" || bad "--no-append writes nothing"
python3 - "$rec" <<'PY'
import json, sys
rows = [json.loads(l) for l in open(sys.argv[1])]
rows.append({"v": 1, "rev": "0000000", "host": "kinu", "from": "2025-01-01T00:00Z", "to": "2025-01-01T23:59Z", "line": "disk", "state": "count", "n": 9, "d": 100, "band": "none", "remedy": "none", "owner": "operator", "ids": []})
open(sys.argv[1], "w").write("".join(json.dumps(r) + "\n" for r in rows))
PY
run --since today --config "$conf" >/dev/null 2>&1
grep -q '2025-01-01' "$rec" && bad "records older than 400 days are pruned" || ok "records older than 400 days are pruned on append"
[[ "$(modeof "$rec")" == 600 ]] && ok "the atomic rewrite keeps mode 600" || bad "mode after rewrite"
chmod 755 "$state/introspector"
run --since today --config "$conf" >"$root/o" 2>/dev/null; rc=$?
grep -q '^GAP records write' "$root/o" && ok "a directory wider than 700 refuses the append and prints a GAP line" || bad "wide dir GAP" "$(grep -i records "$root/o")"
grep -q '^bus-errors\|^GAP bus-errors' "$root/o" && ok "the counts still print when the append fails" || bad "counts still print"
chmod 700 "$state/introspector"
chmod 644 "$rec"
run --since today --config "$conf" >"$root/o" 2>/dev/null
grep -q '^GAP records write' "$root/o" && ok "a file wider than 600 refuses the append" || bad "wide file GAP"
chmod 600 "$rec"
writeconf <<<"record_dir=$root/co-director/rec"
run --since today --config "$conf" >/dev/null 2>"$root/err"; rc=$?
[[ $rc -eq 2 ]] && grep -q 'work tree' "$root/err" && ok "it refuses to start when its write path is inside a git work tree" || bad "refuse inside a work tree" "rc=$rc $(cat "$root/err")"
[[ ! -e "$root/co-director/rec" ]] && ok "and it wrote nothing there" || bad "wrote inside a work tree"
writeconf </dev/null

# ================================================================ round 1 of the review
# (1) every line after the header, footers included, is at most 7; the lowest-priority ones fold into "+N more"
mk "$root/proj/-work-aae-orc/sec.jsonl" --agent director --ok 3 --secrets
echo ok >"$root/bin/asks.mode"
run --since today --window week --config "$conf" --no-append >"$root/o" 2>/dev/null
[[ "$(sed 1d "$root/o" | grep -c .)" -eq 7 ]] && ok "with every band and four secret shapes the output is exactly 7 lines after the header" || bad "7 lines" "$(sed 1d "$root/o" | grep -c .)"
sed 1d "$root/o" | tail -1 | grep -q -E '^\+[0-9]+ more$' && ok "the last line folds the lowest-priority lines into one +N more" || bad "+N more" "$(tail -2 "$root/o")"
run --since today --window week --config "$conf" --no-append --json >"$root/j" 2>/dev/null
python3 - "$root/j" "$root/o" <<'PY' && ok "the +N more count is the number of lines folded; --json carries the same capped lines" || bad "fold count"
import json, sys
d = json.load(open(sys.argv[1])); L = d["lines"]
assert len(L) == 7 and L[-1].startswith("+"), L
n = int(L[-1].split()[0][1:])
assert n >= 2, L
assert L[:-1] == open(sys.argv[2]).read().splitlines()[1:7], (L, "text and json agree")
PY
rm -f "$root/proj/-work-aae-orc/sec.jsonl"

# (2) a source set that is all unreadable is a GAP and records no zero
grep -v -e '^checkout.director=' -e '^install.director-mcp=' "$conf" >"$root/c2.conf"; echo "marvel_cmd=$root/bin/no-such-marvel" >>"$root/c2.conf"
rm -f "$state/introspector/records.jsonl"
run --since today --config "$root/c2.conf" >"$root/o" 2>"$root/err"
grep -q '^GAP not-installed last never' "$root/o" && ! grep -q '^not-installed ' "$root/o" && ok "every not-installed source unreadable prints GAP not-installed last <time>, not a hidden line" || bad "all-unreadable GAP" "$(grep -i install "$root/o")"
python3 - "$state/introspector/records.jsonl" <<'PY' && ok "and the record is a gap with no n, never a count of 0" || bad "all-unreadable record"
import json, sys
r = [json.loads(l) for l in open(sys.argv[1])]
r = [x for x in r if x["line"] == "not-installed"]
assert r and all(x["state"] == "gap" and "n" not in x for x in r), r
PY

# (3) the work-tree guard fails closed
mkdir -p "$root/pybin"; ln -sf "$(command -v python3)" "$root/pybin/python3"
nogit() { env -i HOME="$root/home" PATH="$root/pybin" LANG=en_US.UTF-8 TMPDIR="$root/t" DIRECTOR_STATE="$state" INTROSPECT_NOW="$NOW" python3 -I "$S" "$@"; }
writeconf <<<"record_dir=$root/co-director/rec"
nogit --since today --config "$conf" >"$root/o" 2>"$root/err"; rc=$?
[[ $rc -eq 2 && ! -e "$root/co-director/rec" ]] && grep -q 'path is inside a git work tree; refusing' "$root/err" && ok "with no git on PATH a record path inside a checkout is refused (exit 2) and nothing is written" || bad "no-git inside checkout" "rc=$rc $(head -2 "$root/err")"
writeconf <<<"record_dir=$root/clear/rec"
nogit --since today --config "$conf" >"$root/o" 2>"$root/err"; rc=$?
[[ $rc -eq 2 && ! -e "$root/clear" ]] && grep -q 'did not answer' "$root/err" && ok "with no git to ask, even a path with no .git above it is refused: exit 2, nothing written" || bad "no-git unknown" "rc=$rc $(head -2 "$root/err")"
nogit --since today --config "$conf" --no-append >/dev/null 2>&1; rc=$?
[[ $rc -eq 0 ]] && ok "--no-append needs no git answer and still reads" || bad "no-append without git" "rc=$rc"
mkdir -p "$root/wt/sub"; echo "gitdir: /nowhere" >"$root/wt/.git"
writeconf <<<"record_dir=$root/wt/sub/rec"
nogit --since today --config "$conf" >/dev/null 2>"$root/err"; rc=$?
[[ $rc -eq 2 && ! -e "$root/wt/sub/rec" ]] && grep -q 'path is inside a git work tree; refusing' "$root/err" && ok "a .git file up the tree is found without git" || bad ".git file" "rc=$rc"
writeconf </dev/null

# (4) drain lag reads envelope fields only; secret shapes are counts only; a store or spill path is never opened
mk "$root/proj/-work-aae-orc/lag.jsonl" --agent director --ok 1 --lag 2
run --since today --config "$conf" --no-append --json >"$root/j" 2>/dev/null
python3 -c 'import json,sys; assert json.load(open(sys.argv[1]))["counts"]["lag_over_1h"]==2' "$root/j" && ok "control: two results whose envelope sent_at is over an hour old count as drain lag 2" || bad "lag control"
mk "$root/proj/-work-aae-orc/lag.jsonl" --agent director --ok 1 --fakelag 3
run --since today --config "$conf" --no-append --json >"$root/j" 2>/dev/null
python3 -c 'import json,sys; assert json.load(open(sys.argv[1]))["counts"]["lag_over_1h"]==0' "$root/j" && ok "a sent_at planted in a content field never changes drain lag" || bad "fake sent_at in content" "$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["counts"]["lag_over_1h"])' "$root/j")"
rm -f "$root/proj/-work-aae-orc/lag.jsonl"
mk "$root/proj/-work-aae-orc/sec.jsonl" --agent director --ok 3 --secrets
rm -f "$state/introspector/records.jsonl"
run --since today --config "$conf" >"$root/o" 2>"$root/err"
python3 - "$state/introspector/records.jsonl" <<'PY' && ok "each planted shape (aws key, token, seed, private key) is one count record with its shape name" || bad "shape records"
import json, sys
r = [json.loads(l) for l in open(sys.argv[1])]
cats = sorted(x["cat"] for x in r if x["line"] == "SECRET-SHAPE")
assert cats == ["aws-key", "gh-token", "nkey-seed", "private-key"] and all(x["n"] == 1 for x in r if x["line"] == "SECRET-SHAPE"), cats
PY
sed 1d "$root/o" | grep -c . | grep -qx 7 && grep -q '^+[0-9]* more$' "$root/o" && ok "the footers fold into +N more before any fact line does" || bad "footers fold first" "$(sed 1d "$root/o")"
grep -q -e ABCDEFGH -e Zq7Zq7 -e B2B2B2 -e 'PRIVATE KEY' "$root/o" "$root/err" "$state/introspector/records.jsonl" && bad "no byte of a planted secret in stdout, stderr or records" "$(grep -l -e ABCDEFGH -e Zq7Zq7 -e B2B2B2 "$root/o" "$root/err" "$state/introspector/records.jsonl")" || ok "no byte of a planted secret reaches stdout, stderr or records"
rm -f "$root/proj/-work-aae-orc/sec.jsonl"
run --since today --config "$conf" --no-append --json >"$root/j0" 2>/dev/null
mkdir -p "$root/proj/-work-aae-orc/nats-global/store" "$root/proj/-work-aae-orc/spill"
mk "$root/proj/-work-aae-orc/nats-global/store/blk.jsonl" --agent director --ok 50 --den 9 --err 4 --lag 3 --secrets
mk "$root/proj/-work-aae-orc/spill/s-1.jsonl" --agent director --ok 50 --den 9 --err 4 --lag 3 --secrets
run --since today --config "$conf" --no-append --json >"$root/j1" 2>/dev/null
cmp -s "$root/j0" "$root/j1" && ok "files under a store or spill path are never read: the counts and lines are identical with them planted" || bad "store or spill read" "$(diff "$root/j0" "$root/j1" | head -4)"
rm -rf "$root/proj/-work-aae-orc/nats-global" "$root/proj/-work-aae-orc/spill"

# (5) last_harvest unset: seven days and a GAP, not today
run --since last-harvest --config "$conf" --no-append >"$root/o" 2>/dev/null
grep -q '^GAP last-harvest last never' "$root/o" && sed -n 1p "$root/o" | grep -q 'since 2026-10-13T00:00Z' && ok "--since last-harvest with no value counts from 7 days back and prints a GAP" || bad "last-harvest fallback" "$(sed -n 1p "$root/o"; grep GAP "$root/o")"
writeconf <<<"last_harvest=2026-10-18T08:00:00Z"
run --since last-harvest --config "$conf" --no-append >"$root/o" 2>/dev/null
sed -n 1p "$root/o" | grep -q 'since 2026-10-18T08:00Z' && ! grep -q '^GAP last-harvest' "$root/o" && ok "a set last_harvest is used, with no GAP" || bad "last-harvest set" "$(sed -n 1p "$root/o")"
writeconf </dev/null
grep -q 'last_harvest=' "$repo/skills/director/SKILL.md" && ok "harvest step 6 writes last_harvest after the run" || bad "SKILL.md last_harvest line"

# (6) "+N research": only the anchored denial count, from research-supervisor and retrospector transcripts;
# an introspector transcript is never counted; an unreadable dropped file is a GAP, never +0
run --since today --config "$conf" --no-append --json >"$root/j0" 2>/dev/null
mk "$root/proj/-work-aae-orc/i1.jsonl" --agent introspector --ok 5 --den 3 --err 2 --secrets
run --since today --config "$conf" --no-append --json >"$root/j1" 2>/dev/null
python3 - "$root/j0" "$root/j1" <<'PY' && ok "an introspector transcript holding real denials adds nothing: not to the count, not to +N research, no secret shape" || bad "introspector transcript counted"
import json, sys
a = json.load(open(sys.argv[1])); b = json.load(open(sys.argv[2]))
for k in ("denials", "denials_research", "bus_errors", "results"):
    assert a["counts"][k] == b["counts"][k], (k, a["counts"][k], b["counts"][k])
assert not [l for l in b["lines"] if l.startswith("SECRET-SHAPE") and "private-key" in l], b["lines"]
PY
rm -f "$root/proj/-work-aae-orc/i1.jsonl"
mk "$root/proj/-work-aae-orc/rt1.jsonl" --agent retrospector --ok 5 --den 2 --err 3 --secrets --sends 2 --to global://mokuzai/supervisor
run --since today --config "$conf" --no-append --json >"$root/j1" 2>/dev/null
python3 - "$root/j0" "$root/j1" <<'PY' && ok "a retrospector transcript adds only its denials to +N research: nothing else it holds is read into any count" || bad "retrospector count"
import json, sys
a = json.load(open(sys.argv[1]))["counts"]; b = json.load(open(sys.argv[2]))["counts"]
assert b["denials_research"] == a["denials_research"] + 2, (a, b)
for k in ("denials", "bus_errors", "results", "director_calls", "host_local_paths"):
    assert a[k] == b[k], (k, a[k], b[k])
PY
rm -f "$root/proj/-work-aae-orc/rt1.jsonl"
mk "$root/proj/-work-aae-orc/rs9.jsonl" --agent research-supervisor --ok 2 --den 1
mkdir -p "$root/proj/-work-aae-orc/rs9/subagents"
mk "$root/proj/-work-aae-orc/rs9/subagents/a.jsonl" --den 4
chmod 000 "$root/proj/-work-aae-orc/rs9/subagents/a.jsonl"
if [[ -r "$root/proj/-work-aae-orc/rs9/subagents/a.jsonl" ]]; then
  skp "an unreadable dropped file: this user can read a mode 000 file"
else
  run --since today --config "$conf" --no-append --json >"$root/j1" 2>/dev/null
  python3 - "$root/j1" <<'PY' && ok "an unreadable dropped file is a GAP in the header and a count of files, never a quiet +0" || bad "unreadable dropped file"
import json, sys
d = json.load(open(sys.argv[1]))
assert d["counts"]["research_unreadable"] == 1, d["counts"]
assert "GAP research-transcripts 1 unreadable" in d["header"], d["header"]
PY
fi
chmod 600 "$root/proj/-work-aae-orc/rs9/subagents/a.jsonl"; rm -rf "$root/proj/-work-aae-orc/rs9" "$root/proj/-work-aae-orc/rs9.jsonl"

# ================================================================ properties of the script itself
[[ "$(grep -c -E 'ArtifactData|broadcast|marvel inject|gh (api|pr|issue)|sh\(\[[^]]*send' "$S")" == 0 ]] && ok "P1 the script names no Desk, bus send, inject or gh call (the name send_message is read from transcripts, never run)" || bad "P1" "$(grep -n -E 'ArtifactData|broadcast|marvel inject|gh (api|pr|issue)|sh\(\[[^]]*send' "$S")"
python3 - "$S" <<'PY' && ok "the script is Python 3 standard library only" || bad "stdlib only"
import ast, sys, sys as _s
mods = set()
for n in ast.walk(ast.parse(open(sys.argv[1]).read())):
    if isinstance(n, ast.Import): mods |= {a.name.split(".")[0] for a in n.names}
    if isinstance(n, ast.ImportFrom): mods.add((n.module or "").split(".")[0])
bad = mods - set(_s.stdlib_module_names)
assert not bad, bad
PY
[[ -z "$(grep -l -r introspect "$root/home" 2>/dev/null || true)" ]] && ok "P6 the run installs no trigger of its own (nothing written under HOME)" || bad "P6 trigger" "$(grep -l -r introspect "$root/home" 2>/dev/null)"
[[ ! -e "$root/home/.marvel" && ! -e "$root/home/.director" ]] && ok "the run never touched the scratch HOME's marvel or director dirs" || bad "touched home dirs"

if command -v sandbox-exec >/dev/null 2>&1; then
  prof="$root/p.sb"
  cat >"$prof" <<EOF
(version 1)
(allow default)
(deny file-write*)
(allow file-write* (subpath "$root") (subpath "/dev"))
(deny network*)
(allow network* (remote ip "localhost:*"))
EOF
  if env -i PATH=/usr/bin:/bin sandbox-exec -f "$prof" /bin/echo ok >/dev/null 2>"$root/sberr"; then
    sbrun() { env -i HOME="$root/home" PATH="$FIXPATH" LANG=en_US.UTF-8 TMPDIR="$root/t" DIRECTOR_STATE="$state" INTROSPECT_NOW="$NOW" sandbox-exec -f "$prof" python3 -I "$S" "$@"; }
    sbrun --since today --config "$conf" >"$root/o" 2>"$root/err"; rc=$?
    [[ $rc -eq 0 ]] && ok "P3 a full run under a write-deny, network-deny sandbox exits 0" || bad "P3 sandboxed run" "rc=$rc $(head -2 "$root/err")"
    # the control target is outside scratch and outside any home (a Desk card on the conflicting rule is pending)
    cpath="/tmp/ins-control-$$"
    ctl="$(env -i HOME="$root/home" PATH=/usr/bin:/bin sandbox-exec -f "$prof" /bin/sh -c "echo x >\"$cpath\"" 2>&1)"; crc=$?
    [[ $crc -ne 0 && "$ctl" == *"Operation not permitted"* && ! -e "$cpath" ]] && ok "P3 control: a write outside scratch is refused by the profile (Operation not permitted)" || bad "P3 control write" "rc=$crc $ctl"
    rm -f "$cpath"
    in="$(env -i HOME="$root/home" PATH=/usr/bin:/bin sandbox-exec -f "$prof" /bin/sh -c "echo y >\"$root/t/inside\"" 2>&1)"
    [[ -s "$root/t/inside" ]] && ok "P3 control: a write inside scratch succeeds under the same profile" || bad "P3 inside write" "$in"
  else
    skp "P3 sandbox-exec could not apply a profile here: $(head -1 "$root/sberr")"
  fi
else
  skp "P3 sandbox-exec is not installed"
fi

# ================================================================ wiring (SKILL.md, install.sh)
sk="$repo/skills/director/SKILL.md"
grep -q 'bin/introspect --since today' "$sk" && ok "the sweep runs introspect --since today" || bad "sweep wiring"
grep -q 'bin/introspect --since last-harvest --window week' "$sk" && ok "harvest step 6 runs introspect --since last-harvest --window week" || bad "harvest wiring"
python3 - "$sk" <<'PY' && ok "the sweep call sits after step 3 and before step 4; the harvest call inside step 6" || bad "wiring order"
import re, sys
s = open(sys.argv[1]).read()
a = s.index("introspect --since today"); b = s.index("4. Present, in this shape")
assert s.index("draft-sweep") < a < b
h = s.index("introspect --since last-harvest --window week")
assert s.index("6. For an episode that produced a correction") < h < s.index("Do not renumber existing requirements")
PY
grep -q '^Introspect:' "$sk" && ok "the reply shape has an Introspect block" || bad "Introspect block in the shape"
grep -q 'never an R-entry\|never an R-entry, a rule' "$sk" && ok "the skill says a count is cited under harvest step 6 only and never becomes an R-entry" || bad "M1 sentence"
grep -q 'introspect' "$repo/install.sh" && ok "install.sh installs introspect into the bin home" || bad "install line"
[[ -z "$(git -C "$repo" grep -l introspect HEAD -- sim/requirements.md 2>/dev/null || true)" ]] && ok "M1 no count or script is cited in sim/requirements.md" || bad "M1 requirements"

echo "$pass passed, $fail failed, $skip skipped"
[[ $fail -eq 0 ]]
