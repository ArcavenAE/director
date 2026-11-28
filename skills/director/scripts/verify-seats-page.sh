#!/usr/bin/env bash
# Prove `seats-page`, the seats-and-hosts page (I-876, panel 1 with panel 2's quota half):
#   - it reads, for each cluster `marvel config list` names, exactly these and nothing else:
#       marvel --cluster C get sessions --columns team,role,name,state,health,last-active,account,7d
#       marvel --cluster C get vitals -o json
#       marvel --cluster C get account-readings -o json
#   - it writes one local, static HTML page: first a strip with one host block per cluster (host vitals and,
#     per account and window, the percent used and the time to reset), then each cluster's sessions;
#   - a read that fails shows as "GAP <read>: <error>" in that cluster's block, and no cluster is left out;
#   - marvel's words are shown verbatim, escaped; the page has no button, link, form or script, and no red;
#   - the page's own words never include stuck, busy, free or idle (ops-dashboard.md section 2);
#   - dash-withhold.txt (private names, whole words, any case, as dash-writer and lane-ledger read it): a value
#     from marvel or the cluster list that holds a name reads "withheld". A list that is absent, unreadable or
#     wider than 0600 fails closed: no session rows, no reporting seat, no unexpected sessions line and none of
#     marvel's error text are drawn; a failed read still shows as GAP with a fixed reason word.
# The page's shape the checks rely on: <section class="host" data-cluster="C">, then
# <section class="sessions" data-cluster="C">, with tables of <tr> rows.
# Every run is under `env -i` with a scratch HOME and DIRECTOR_STATE, a fixture marvel named by DASH_MARVEL and
# first on PATH, and no MARVEL_SOCKET. No live daemon is ever read. Written for bash 3.2 and bash 5.
#
# Usage: skills/director/scripts/verify-seats-page.sh   Exit nonzero on any miss. SEATS_PAGE names another script.
set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/../../.." && pwd)"
SRC="${SEATS_PAGE:-$here/seats-page}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(if [[ -n "${SEATS_TMP:-}" ]]; then mktemp -d "$SEATS_TMP/seats.XXXXXX"; else mktemp -d; fi)" || { echo "FAIL scratch root: mktemp failed"; exit 1; }
[[ -n "$root" && -d "$root" ]] || { echo "FAIL scratch root: no directory"; exit 1; }
root="$(cd "$root" && pwd -P)"
trap 'rm -rf "$root"' EXIT
mkdir -p "$root/home" "$root/bin" "$root/s" "$root/tables" "$root/state"
state="$root/state"
tables="$root/tables"
page="$state/seats.html"
NOW="2026-11-28T18:00:00Z"

# The script runs from a scratch bin beside dash-writer, the installed layout.
[[ -f "$SRC" ]] && cp "$SRC" "$root/s/seats-page"
cp "$here/dash-writer" "$root/s/dash-writer"

# python3 is linked into the scratch bin: its own directory is never added, since it can hold a real marvel.
ln -s "$(python3 -c 'import sys;print(sys.executable)')" "$root/bin/python3" || { echo "FAIL cannot link python3 into the scratch bin"; exit 1; }
STUB="$root/bin/marvel"
RUN_PATH="$root/bin:/usr/bin:/bin"
RUN_MARVEL="$STUB"
run() { env -i HOME="$root/home" PATH="$RUN_PATH" LANG=en_US.UTF-8 DIRECTOR_STATE="$state" \
        DWS_TEST_CLOCK=1 DWS_NOW="$NOW" DASH_MARVEL="$RUN_MARVEL" DASH_MARVEL_TIMEOUT="${MARVEL_TIMEOUT:-20}" \
        TABLES="$tables" python3 -I "$root/s/seats-page" "$@" >"$root/out.txt" 2>"$root/err.txt"; rc=$?; }

cat >"$root/bin/marvel" <<'SH'
#!/bin/sh
# fixture marvel: logs every call; answers only the four read-only calls the page may make
echo "$*" >>"$TABLES/calls"
if [ "$*" = "config list" ]; then
  [ -f "$TABLES/config-list" ] || { echo "Error: CANARYCONFIG cannot be read" >&2; exit 1; }
  cat "$TABLES/config-list"; exit 0
fi
[ "$1" = --cluster ] || { echo "UNEXPECTED $*" >>"$TABLES/calls"; exit 9; }
c="$2"; shift 2
case "$*" in
  "get sessions --columns team,role,name,state,health,last-active,account,7d") k=sessions ;;
  "get vitals -o json") k=vitals ;;
  "get account-readings -o json") k=readings ;;
  *) echo "UNEXPECTED $c $*" >>"$TABLES/calls"; exit 9 ;;
esac
f="$TABLES/$c.$k"
[ -f "$f.sleep" ] && exec sleep "$(cat "$f.sleep")"
[ -f "$f.err" ] && { cat "$f.err" >&2; exit 1; }
[ -f "$f" ] || { echo "Error: no fixture for $c $k" >&2; exit 1; }
cat "$f"
SH
chmod +x "$root/bin/marvel"
for dir in $(printf '%s' "$RUN_PATH" | tr ':' ' '); do
  [[ ! -e "$dir/marvel" || "$dir/marvel" == "$STUB" ]] || { echo "FAIL a marvel other than the stub is on the PATH a run uses: $dir/marvel"; exit 1; }
done
[[ "$(PATH="$RUN_PATH" command -v marvel)" == "$STUB" ]] || { echo "FAIL marvel does not resolve to the stub on the PATH a run uses"; exit 1; }
grep -q 'fixture marvel' "$RUN_MARVEL" || { echo "FAIL DASH_MARVEL is not the stub"; exit 1; }

python3 - "$tables" <<'PY'
import json, os, sys
d = sys.argv[1]
def put(name, text):
    with open(os.path.join(d, name), "w", encoding="utf-8") as f:
        f.write(text)
HEAD = ["TEAM", "ROLE", "AGENT NAME", "STATE", "HEALTH", "LAST-ACTIVE", "ACCOUNT", "7D%"]
def table(rows, head=HEAD):
    w = [max(len(r[i]) for r in [head] + rows) for i in range(len(head))]
    return "".join("".join(c.ljust(w[i] + 2) for i, c in enumerate(r)).rstrip() + "\n" for r in [head] + rows)
put("config-list", "     NAME     ADDRESS                  IDENTITY\n"
    "*    alpha    /CANARYSOCK/alpha.sock   CANARYKEY\n"
    + "".join(f"     {n:<8} ssh://CANARYHOST-{n}     -\n" for n in ("beta", "gamma", "delta", "epsilon")))

def vit(thresholds, enabled=True, sampled=True):
    return json.dumps({"enabled": enabled, "sampled": sampled, "at": "2026-11-28T17:59:30.600763Z", "thresholds": thresholds,
                       "top_processes": [{"pid": 4242, "name": "CANARYPROC", "seat": "aae/x"}],
                       "orphans": [{"pid": 4343, "name": "CANARYORPHAN"}]})
SWAP = {"name": "swap used", "line": "over 50% of total", "state": "over", "detail": "6.4 GiB used; 48331 swap-ins"}
DISK = {"name": "disk free", "line": "no line", "state": "none", "detail": "/w: 12.0 GiB free <90% used>"}
def reading(account, window, used, resets, at, session):
    return {"cluster": "alpha", "account": account, "window": window, "used_percent": used, "resets_at": resets, "at": at, "session": session}

# alpha: every read answers
put("alpha.sessions", table([
    ["arcaven", "builder", "alpha-builder-g1-0", "running", "alive (unwatched)", "6m*", "acct-one", "60%"],
    ["arcaven", "reviewer", "alpha-reviewer-g1-0", "failed", "unhealthy", "-", "acct-one", "60%?"],
    ["probe", "account-probe", "<b>bold</b>&amp", "succeeded (exit 0)", "unknown (parked: usage limit)", "2h5m", "-", "-"],
    ["zzpriv-team", "reviewer", "alpha-ZZPRIV-g1-0", "running", "alive", "1m", "zzpriv acct", "7%"],
    ["arcaven", "builder", "alpha-zzprivate-g1-0", "running", "alive", "3m", "acct-one", "60%"],
]))
put("alpha.vitals", vit([SWAP, DISK, {"name": "process RSS", "line": "over 20 GiB", "state": "under", "detail": "zzpriv (pid 7) 0.9 GiB"}]))
put("alpha.readings", json.dumps([
    reading("acct-long /CANARY-less/one", "seven_day", 42.5, "2026-11-29T23:00:00Z", "2026-11-28T17:00:00.988606Z", "aae/newer"),
    reading("acct-long /CANARY-less/one", "seven_day", 10, "2026-11-29T23:00:00Z", "2026-11-27T10:00:00Z", "aae/older"),
    reading("acct-long /CANARY-less/one", "five_hour", 7, "2026-11-28T18:47:00Z", "2026-11-28T17:30:00Z", "aae/five"),
    reading("acct-two <x>", "seven_day", 99, "2026-11-28T12:00:00Z", "2026-11-28T11:00:00Z", "aae/past"),
    reading("acct-three", "seven_day", 3, "2026-11-28T21:05:00Z", "2026-11-28T11:00:00Z", "aae/hours"),
    reading("acct zzpriv", "seven_day", 55, "2026-11-29T23:00:00Z", "2026-11-28T11:00:00Z", "aae/acct"),
    reading("acct-four", "seven_day", 12, "2026-11-29T23:00:00Z", "2026-11-28T11:00:00Z", "aae/zzpriv-seat"),
]))
# beta: every read fails, each with its own words; the usage text that follows an error is not an error
for k in ("sessions", "vitals", "readings"):
    put(f"beta.{k}.err", f"Error: dial beta for {k}: connect: connection refused <&>\nUsage:\n  marvel get CANARYUSAGE\n")
# gamma: every read answers with something that is not what was asked
put("gamma.sessions", "WORKSPACE  SOMETHING-ELSE\nx          y\n")
put("gamma.vitals", "not json at all\n")
put("gamma.readings", "{}\n")
# delta: one row cannot be cut, vitals not sampled, no readings
t = table([["arcaven", "builder", "delta-builder-g1-0", "running", "alive", "1m", "acct-d", "5%"]])
put("delta.sessions", t + "short row\n")
put("delta.vitals", vit([], sampled=False))
put("delta.readings", "[]\n")
# epsilon: the sessions read never returns; the other two answer
put("epsilon.sessions.sleep", "5")
put("epsilon.vitals", vit([SWAP]))
put("epsilon.readings", "[]\n")
PY

# q <class> <cluster>: the lines of that section, one per table row (cells joined with "|") or text block.
# q all - : every line of the page. q order - : the sections in page order as "class:cluster".
cat >"$root/q.py" <<'PY'
import sys
from html.parser import HTMLParser
class P(HTMLParser):
    def __init__(self):
        super().__init__()
        self.lines, self.order, self.stack, self.buf, self.cells = [], [], [], [], None
        self.skip = False
    def key(self):
        return self.stack[-1] if self.stack else ""
    def flush(self):
        t = "".join(self.buf).strip()
        self.buf = []
        if t:
            self.lines.append((self.key(), t))
    def handle_starttag(self, tag, attrs):
        a = dict(attrs)
        if tag in ("style", "title"):
            self.skip = True
        if tag == "section":
            self.flush()
            k = f"{a.get('class', '')}:{a.get('data-cluster', '')}"
            self.stack.append(k); self.order.append(k)
        elif tag == "tr":
            self.flush(); self.cells = []
        elif tag in ("td", "th"):
            self.buf = []
        elif tag in ("p", "li", "h1", "h2", "h3", "caption", "div", "table", "ul"):
            self.flush()
    def handle_endtag(self, tag):
        if tag in ("style", "title"):
            self.skip = False
        if tag in ("td", "th") and self.cells is not None:
            self.cells.append("".join(self.buf).strip()); self.buf = []
        elif tag == "tr" and self.cells is not None:
            self.lines.append((self.key(), "|".join(self.cells))); self.cells = None; self.buf = []
        elif tag == "section":
            self.flush(); self.stack and self.stack.pop()
        elif tag in ("p", "li", "h1", "h2", "h3", "caption", "div"):
            self.flush()
    def handle_data(self, data):
        if not self.skip:
            self.buf.append(data)
path, cls, cluster = sys.argv[1:4]
p = P()
try:
    p.feed(open(path, encoding="utf-8").read()); p.flush()
except OSError:
    sys.exit(0)
if cls == "order":
    print("\n".join(p.order))
else:
    for k, line in p.lines:
        if cls == "all" or k == f"{cls}:{cluster}":
            print(line)
PY
q() { python3 -I "$root/q.py" "$page" "$1" "$2"; }
whole() { [[ -s "$page" ]] && grep -q '</html>' "$page"; }
# has <label> <class> <cluster> <fixed string>...: one line of the section holds every string
has() { local label="$1" c="$2" n="$3" got s; shift 3; got="$(q "$c" "$n")"
  for s in "$@"; do got="$(printf '%s\n' "$got" | grep -F -- "$s")"; done
  [[ -n "$got" ]] && ok "$label" || bad "$label" "no line in $c:$n holds: $*"; }
# line <label> <class> <cluster> <whole line>
line() { q "$2" "$3" | grep -Fxq -- "$4" && ok "$1" || bad "$1" "no line in $2:$3 equals: $4"; }
# lacks <label> <ERE>: the page is whole and no line of its source matches
lacks() { if ! whole; then bad "$1" "no whole page was written"; elif grep -Eiq -- "$2" "$page"; then bad "$1" "found: $(grep -Eio -- "$2" "$page" | head -3 | tr '\n' ' ')"; else ok "$1"; fi; }

# ownwords <label>: the whole page, less the two fixture strings of marvel's that hold such a word, says none of them
ownwords() { if ! whole; then bad "$1" "no whole page was written"; return; fi
  got="$(python3 -I - "$page" <<'PY'
import html, re, sys
s = html.unescape(open(sys.argv[1], encoding="utf-8").read())
for marvels in ("/w: 12.0 GiB free <90% used>", "disk free"):
    s = s.replace(marvels, "")
print(" ".join(re.findall(r"(?<![A-Za-z])(?:stuck|busy|free|idle)(?![A-Za-z])", s, re.I)))
PY
)" || { bad "$1" "the page could not be read"; return; }
  [[ -z "$got" ]] && ok "$1" || bad "$1" "found: $got"; }

# ------------------------------------------------------------------------------ the full run, five clusters
putlist() { rm -rf "$state/dash-withhold.txt"; printf '# private names\nzzpriv\n' >"$state/dash-withhold.txt"; chmod 600 "$state/dash-withhold.txt"; }
putlist
: >"$tables/calls"
MARVEL_TIMEOUT=1 run
{ [[ $rc -eq 0 ]] && whole; } && ok "a run exits 0 and writes the page in DIRECTOR_STATE" || bad "a run exits 0 and writes the page in DIRECTOR_STATE" "rc=$rc $(head -3 "$root/err.txt")"
grep -Eq 'clusters: 5 named, 1 read in full; 9 GAP' "$root/out.txt" && ok "the one line counts the clusters named, read in full, and the gaps" || bad "the one line counts the clusters named, read in full, and the gaps" "$(cat "$root/out.txt")"
grep -Fq "$page" "$root/out.txt" && ok "the one line names the page it wrote" || bad "the one line names the page it wrote" "$(cat "$root/out.txt")"
mode="$(python3 -c 'import os,sys,stat;print(oct(stat.S_IMODE(os.stat(sys.argv[1]).st_mode)))' "$page" 2>/dev/null)"
[[ "$mode" == "0o600" ]] && ok "the page is mode 600" || bad "the page is mode 600" "got ${mode:-no file}"
[[ "$(ls -A "$state" | LC_ALL=C sort | tr '\n' ' ')" == "dash-withhold.txt seats.html " ]] && ok "the run adds only the page to the state directory" || bad "the run adds only the page to the state directory" "$(ls -A "$state" | tr '\n' ' ')"
{ whole && [[ ! -e "$root/s/__pycache__" ]]; } && ok "the run writes no bytecode beside the scripts" || bad "the run writes no bytecode beside the scripts"

want="$( { echo "config list"; for c in alpha beta gamma delta epsilon; do
  echo "--cluster $c get sessions --columns team,role,name,state,health,last-active,account,7d"
  echo "--cluster $c get vitals -o json"; echo "--cluster $c get account-readings -o json"; done; } | LC_ALL=C sort)"
got="$(LC_ALL=C sort "$tables/calls")"
[[ "$got" == "$want" ]] && ok "marvel is called for config list and the three reads of each cluster, once each, and nothing else" || bad "marvel is called for config list and the three reads of each cluster, once each, and nothing else" "$(printf '%s' "$got" | tr '\n' ';' | head -c 600)"

order="$(q order - | tr '\n' ' ')"
[[ "$order" == "host:alpha host:beta host:gamma host:delta host:epsilon sessions:alpha sessions:beta sessions:gamma sessions:delta sessions:epsilon " ]] \
  && ok "every named cluster has a host block and then a sessions block, in marvel's order, the strip first" || bad "every named cluster has a host block and then a sessions block, in marvel's order, the strip first" "$order"
has "the page says when it was read" all - "$NOW"
has "the page says it does not refresh itself" all - "does not refresh itself"
has "the sessions are headed Sessions, as dash panel 4 is" all - "Sessions"

ownwords "the page's own words, every GAP sentence among them, never say stuck, busy, free or idle"

# sessions, verbatim
line "the sessions table keeps marvel's column names" sessions alpha 'TEAM|ROLE|AGENT NAME|STATE|HEALTH|LAST-ACTIVE|ACCOUNT|7D%'
line "a session row is marvel's cells word for word" sessions alpha 'arcaven|builder|alpha-builder-g1-0|running|alive (unwatched)|6m*|acct-one|60%'
line "a stale quota mark and a dash stay as marvel printed them" sessions alpha 'arcaven|reviewer|alpha-reviewer-g1-0|failed|unhealthy|-|acct-one|60%?'
line "a parked reason and an exit code stay, and markup in a cell is text" sessions alpha 'probe|account-probe|<b>bold</b>&amp|succeeded (exit 0)|unknown (parked: usage limit)|2h5m|-|-'
lacks "markup in a cell is escaped in the source" '<b>bold'

# host vitals, verbatim
has "a vitals threshold shows its name, state, detail and line" host alpha "swap used" "over" "6.4 GiB used; 48331 swap-ins" "over 50% of total"
has "a vitals detail with markup characters is shown as text" host alpha "disk free" "/w: 12.0 GiB free <90% used>" "none" "no line"
has "the host block says when marvel sampled" host alpha "2026-11-28T17:59:30"
lacks "process and orphan lists are not on the page" 'CANARYPROC|CANARYORPHAN'

# quota per account and window
has "the latest reading of an account window gives its percent and time to reset" host alpha "acct-long /CANARY-less/one" "seven_day" "42.5%" "+1d5h" "2026-11-29T23:00:00Z" "2026-11-28T17:00:00" "aae/newer"
{ whole && q host alpha | grep -Fq 'aae/newer' && ! q host alpha | grep -Fq 'aae/older'; } && ok "an older reading of the same account window is not shown" || bad "an older reading of the same account window is not shown"
has "each window of an account has its own row, minutes to reset" host alpha "acct-long /CANARY-less/one" "five_hour" "7%" "+47m"
has "a reset that has passed reads now, and a second account is its own row" host alpha "acct-two <x>" "99%" "now"
has "hours and minutes to reset" host alpha "acct-three" "3%" "+3h5m"
has "no readings is said, not left blank" host delta "no readings"

# gaps
for r in sessions vitals readings; do
  c=host; label="$r"; [[ $r == sessions ]] && c=sessions; [[ $r == readings ]] && label=account-readings
  has "a failed $label read shows as GAP with marvel's error" "$c" beta "GAP $label:" "Error: dial beta for $r: connect: connection refused <&>"
done
lacks "the usage text after an error is not shown" 'CANARYUSAGE'
has "a table with another header is a GAP that shows what came" sessions gamma "GAP sessions:" "WORKSPACE  SOMETHING-ELSE"
{ whole && q sessions gamma | grep -Fq 'GAP' && ! q sessions gamma | grep -Fxq 'x|y'; } && ok "a table with another header draws no rows" || bad "a table with another header draws no rows"
has "vitals that are not JSON are a GAP" host gamma "GAP vitals:" "JSON"
has "readings that are not a list are a GAP" host gamma "GAP account-readings:"
has "a row that cannot be cut cleanly is counted as a GAP" sessions delta "GAP sessions:" "1 row"
line "the rows that can be cut are still shown" sessions delta 'arcaven|builder|delta-builder-g1-0|running|alive|1m|acct-d|5%'
has "vitals marvel has not sampled are a GAP in marvel's word" host delta "GAP vitals:" "sampled false"
has "a read that does not return is a GAP that says it timed out" sessions epsilon "GAP sessions:" "timed out after 1"
has "the other reads of that cluster are still shown" host epsilon "swap used" "6.4 GiB used"

# no verbs, no red, nothing external
lacks "the page has no button, link, form, input or script" '<(button|a[[:space:]]|form|input|select|textarea|script|iframe)|[[:space:]]on(click|load|change)='
lacks "the page fetches nothing and does not reload itself" 'https?://|[[:space:]](src|href)=|@import|url\(|http-equiv="refresh"'
lacks "the page has no inline style" '[[:space:]]style='
if whole && python3 -I - "$page" <<'PY'
import re, sys
s = open(sys.argv[1], encoding="utf-8").read()
css = "".join(re.findall(r"<style[^>]*>(.*?)</style>", s, re.S))
if re.search(r"<[^>]*\s(bg)?color\s*=", s, re.I) or re.search(r"rgb|hsl|hwb|lab\(|lch\(|color\(|\b(red|darkred|crimson|maroon|firebrick|tomato|orangered|indianred|salmon|coral|brown|pink|magenta|orange)\b", css, re.I):
    sys.exit(1)
for h in re.findall(r"#([0-9A-Fa-f]{3,8})\b", css):
    if len(h) in (3, 4):
        h = "".join(c * 2 for c in h)
    if len(h) not in (6, 8) or not (h[0:2].lower() == h[2:4].lower() == h[4:6].lower()):
        sys.exit(1)
PY
then ok "the page sets no colour but grey: no red"; else bad "the page sets no colour but grey: no red"; fi
lacks "no other column of config list reaches the page" 'CANARYSOCK|CANARYKEY|CANARYHOST'

# ------------------------------------------------------------------------------ the private-name list, present
line "a team and seat name that hold a listed name read withheld, as does any such cell" sessions alpha 'withheld|reviewer|withheld|running|alive|1m|withheld|7%'
line "a name that only starts like a listed one is drawn: whole words only" sessions alpha 'arcaven|builder|alpha-zzprivate-g1-0|running|alive|3m|acct-one|60%'
has "a vitals detail holding a listed name reads withheld" host alpha "process RSS" "withheld"
has "an account holding a listed name reads withheld and keeps its quota" host alpha "withheld" "55%" "+1d5h" "aae/acct"
has "a reporting seat holding a listed name reads withheld" host alpha "acct-four" "12%" "withheld"
lacks "no listed name is anywhere in the page source" 'zzpriv([^a]|$)'

# ------------------------------------------------------------------------------ the private-name list, not usable
NAMES='arcaven|account-probe|reviewer|builder|alpha-[a-z]+-g1|<b>bold|&lt;b&gt;bold|aae/|zzpriv-(team|seat)'
MARVELS='dial beta|connection refused|CANARYUSAGE|no fixture for'
rm -f "$state/dash-withhold.txt"
MARVEL_TIMEOUT=1 run
has "with no list the sessions block says it is withheld, and why" sessions alpha "Sessions withheld" "dash-withhold.txt absent"
lacks "with no list no session, team, role or seat name is drawn, the reporting seat included" "$NAMES"
has "with no list the quota is still drawn, its reporting seat withheld" host alpha "acct-long /CANARY-less/one" "seven_day" "42.5%" "+1d5h" "withheld"
lacks "with no list an unexpected sessions line is not quoted" 'SOMETHING-ELSE'
has "with no list that cluster still shows its sessions GAP" sessions gamma "GAP sessions:"
has "with no list a failed sessions read is a GAP with the fixed word failed" sessions beta "GAP sessions:" "failed"
has "with no list a failed vitals read is a GAP with the fixed word failed" host beta "GAP vitals:" "failed"
has "with no list a failed account-readings read is a GAP with the fixed word failed" host beta "GAP account-readings:" "failed"
has "with no list a read that does not return still says it timed out" sessions epsilon "GAP sessions:" "timed out after 1"
lacks "with no list none of marvel's error text is drawn" "$MARVELS"
{ [[ $rc -eq 0 ]] && grep -Fq 'sessions withheld: absent' "$root/out.txt"; } && ok "the one line says the sessions are withheld, exit 0" || bad "the one line says the sessions are withheld, exit 0" "rc=$rc $(cat "$root/out.txt")"
ownwords "the withheld page's own words never say them either"
putlist; chmod 644 "$state/dash-withhold.txt"; MARVEL_TIMEOUT=1 run
has "a list wider than 0600 withholds the sessions" sessions alpha "Sessions withheld" "dash-withhold.txt mode"
lacks "a list wider than 0600 draws no name" "$NAMES"
lacks "a list wider than 0600 draws none of marvel's error text" "$MARVELS"
rm -f "$state/dash-withhold.txt"; mkdir "$state/dash-withhold.txt"; MARVEL_TIMEOUT=1 run
has "an unreadable list withholds the sessions" sessions alpha "Sessions withheld" "dash-withhold.txt unreadable"
lacks "an unreadable list draws no name" "$NAMES"
lacks "an unreadable list draws none of marvel's error text" "$MARVELS"
rmdir "$state/dash-withhold.txt"; putlist
printf '     NAME     ADDRESS\n*    zzpriv   x\n' >"$tables/config-list"
echo "Error: no session zzpriv-1 here" >"$tables/zzpriv.sessions.err"; cp "$tables/delta.vitals" "$tables/zzpriv.vitals"; cp "$tables/delta.readings" "$tables/zzpriv.readings"
run
has "a cluster named with a listed name is still drawn, as withheld" host withheld "withheld"
has "an error holding a listed name is still a GAP, its text withheld" sessions withheld "GAP sessions:" "withheld"
lacks "neither the cluster name nor the error carries the listed name" 'zzpriv'

# ------------------------------------------------------------------------------ the page's own words, one cluster
printf '     NAME     ADDRESS\n*    delta    x\n' >"$tables/config-list"
run
{ [[ $rc -eq 0 ]] && q order - | grep -qx 'host:delta' && ! q order - | grep -q ':alpha'; } && ok "a second run replaces the page with the clusters named now" || bad "a second run replaces the page with the clusters named now" "rc=$rc"
ownwords "the one-cluster page's own words never say them either"

# ------------------------------------------------------------------------------ the cluster list cannot be read
rm "$tables/config-list"
run
{ [[ $rc -eq 0 ]] && q all - | grep -Fq 'GAP cluster list: failed'; } && ok "an unreadable cluster list is a GAP on the page, exit 0" || bad "an unreadable cluster list is a GAP on the page, exit 0" "rc=$rc"
{ whole && [[ -z "$(q order - | grep -E '^(host|sessions):')" ]]; } && ok "with no cluster list no cluster block is drawn" || bad "with no cluster list no cluster block is drawn"
lacks "the config command's own message is not shown" 'CANARYCONFIG'
ownwords "the unreadable-list page's own words never say them either"
RUN_MARVEL="$root/bin/no-such-marvel"; run; RUN_MARVEL="$STUB"
{ [[ $rc -eq 0 ]] && q all - | grep -Fq 'GAP cluster list: unavailable'; } && ok "a missing marvel is a GAP that says unavailable" || bad "a missing marvel is a GAP that says unavailable" "rc=$rc"
ownwords "the missing-marvel page's own words never say them either"

# ------------------------------------------------------------------------------ the command line
printf '     NAME     ADDRESS\n*    delta    x\n' >"$tables/config-list"
rm -f "$page"
run --out "$root/elsewhere.html"
{ [[ $rc -eq 0 && -s "$root/elsewhere.html" && ! -e "$page" ]]; } && ok "--out writes the page there and only there" || bad "--out writes the page there and only there" "rc=$rc"
run --out "$root/no-such-dir/p.html"
{ [[ $rc -eq 1 ]] && grep -q 'seats-page:' "$root/err.txt"; } && ok "a page that cannot be written is exit 1 with a message" || bad "a page that cannot be written is exit 1 with a message" "rc=$rc"
run --nonsense
{ [[ $rc -eq 2 ]] && grep -qi 'usage' "$root/err.txt"; } && ok "an unknown argument is a usage error, exit 2" || bad "an unknown argument is a usage error, exit 2" "rc=$rc"
: >"$tables/calls"
run --help
{ [[ $rc -eq 0 ]] && grep -q 'read only' "$root/out.txt" && [[ ! -s "$tables/calls" ]]; } && ok "--help describes the reads and calls nothing" || bad "--help describes the reads and calls nothing" "rc=$rc"

# ------------------------------------------------------------------------------ installed
grep -Eq '^install_one .*skills/director/scripts/seats-page"' "$repo/install.sh" && ok "install.sh installs seats-page beside dash-writer" || bad "install.sh installs seats-page beside dash-writer"

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
