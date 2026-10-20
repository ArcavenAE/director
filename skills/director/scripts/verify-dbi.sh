#!/usr/bin/env bash
# Prove dbi (S1 of sim/design/stalled-big-ideas.md): the scan that lists stalled
# big ideas. Cases 1 to 7g of section 6: a stalled idea and its reasons, open and
# closed pointers, successors and cycles, parked and resolved ideas, the quiet
# threshold, never-read roots, an unreachable bd named instead of read as "no
# pointer", read-by-ref from the default branch, no fetch, no prompt, and the
# pool deadline. The fixtures are built with git fast-import, which writes
# unsigned commit objects into scratch repos under a temp directory that is never
# pushed; whether the signing rules cover such objects is an open question for the
# operator. bd and gh are stubs first on PATH.
#
# Usage: skills/director/scripts/verify-dbi.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DBI="${DBI:-$here/dbi}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
export PYTHONDONTWRITEBYTECODE=1 GIT_TERMINAL_PROMPT=0
export DIRECTOR_STATE="$root/state"
export DBI_TEST_CLOCK=1 DBI_NOW=2026-10-20T12:00:00Z
NOW=$(date -j -u -f %Y-%m-%dT%H:%M:%SZ 2026-10-20T12:00:00Z +%s 2>/dev/null || date -u -d 2026-10-20T12:00:00Z +%s)
DAY=86400
mkdir -p "$DIRECTOR_STATE" "$root/orc" "$root/remotes" "$root/bin"
export PATH="$root/bin:$PATH"

# --- stubs ------------------------------------------------------------------
cat >"$root/bin/bd" <<'S'
#!/usr/bin/env bash
[[ -e "$DBI_FIX/bd.down" ]] && { echo "bd: server unreachable" >&2; exit 1; }
[[ "$1 $2" == "sql --json" ]] || { echo "stub bd: unexpected $*" >&2; exit 2; }
cat "$DBI_FIX/bd.json"
S
cat >"$root/bin/gh" <<'S'
#!/usr/bin/env bash
[[ -e "$DBI_FIX/gh.down" ]] && { echo "gh: no network" >&2; exit 1; }
# gh pr view N --repo OWNER/REPO --json state  |  gh issue view ...
n="$3"; repo="$5"
st="$(awk -v k="$repo#$n" '$1==k{print $2}' "$DBI_FIX/gh.tsv")"
[[ -n "$st" ]] || { echo "not found" >&2; exit 1; }
echo "{\"state\":\"$st\"}"
S
chmod +x "$root/bin/bd" "$root/bin/gh"
export DBI_FIX="$root/fix"; mkdir -p "$DBI_FIX"
echo '[]' >"$DBI_FIX/bd.json"; : >"$DBI_FIX/gh.tsv"

# --- fixture builder: bare remote by fast-import, clone as the checkout ------------
cat >"$root/fx.py" <<'P'
import subprocess, sys
def sh(*a, **k): return subprocess.run(a, check=True, capture_output=True, text=True, **k)
def init(bare):
    sh("git", "init", "-q", "--bare", "-b", "main", bare)
def commit(bare, epoch, files):
    have = subprocess.run(["git", "-C", bare, "rev-parse", "-q", "--verify", "refs/heads/main"], capture_output=True).returncode == 0
    out = f"commit refs/heads/main\ncommitter T <t@example.invalid> {epoch} +0000\ndata 3\nfx\n"
    if have: out += "from refs/heads/main^0\n"
    for path, body in files:
        b = body.encode()
        if body == "\0delete": out += f"D {path}\n"; continue
        out += f"M 644 inline {path}\ndata {len(b)}\n" + body + "\n"
    subprocess.run(["git", "-C", bare, "fast-import", "--quiet"], input=out.encode(), check=True)
if __name__ == "__main__":
    cmd, bare = sys.argv[1], sys.argv[2]
    if cmd == "init": init(bare)
    else: commit(bare, sys.argv[3], [tuple(a.split("=", 1)) for a in sys.argv[4:]])
P
fx() { python3 "$root/fx.py" "$@"; }
# newrepo NAME  ->  $root/remotes/NAME.git and, once seeded, $root/orc/NAME
newrepo() { fx init "$root/remotes/$1.git"; }
checkout() { git clone -q "$root/remotes/$1.git" "$root/orc/$1" 2>/dev/null; }
idea() { printf '# %s\n\n%s\n' "$1" "${2:-A design with nothing that points forward.}"; }

cfg() {  # roots file
  printf '%s\n' "$root/orc/*/_kos/ideas/*.md" "$root/orc/*/sim/design/*.md" >"$DIRECTOR_STATE/idea-roots.conf"
}
cfg
run() { "$DBI" --json --threshold-days 14 "$@" || echo "dbi exited $?" >&2; }
q() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
rows() { q "sorted((r['path'], r['reason']) for r in d['rows'])"; }

# --- the threshold has no default (design section 7) -----------------------------------
newrepo none; fx commit "$root/remotes/none.git" $((NOW-20*DAY)) "_kos/ideas/a.md=$(idea A)"; checkout none
if "$DBI" --json >"$root/o" 2>"$root/e"; then bad "no threshold, no run"; else grep -q 'section 7' "$root/e" && ok "no threshold, no run (names section 7)" || bad "no threshold, no run (names section 7)" "$(cat "$root/e")"; fi
echo '{"stalled_idea_days": 14}' >"$DIRECTOR_STATE/thresholds.json"
"$DBI" --json >"$root/o" 2>"$root/e" && ok "the threshold may come from thresholds.json" || bad "the threshold may come from thresholds.json" "$(cat "$root/e")"
rm -f "$DIRECTOR_STATE/thresholds.json"
rm -rf "$root/orc/none" "$root/remotes/none.git"

# --- 1, 5: stalled with no pointer; under the threshold is not listed ---------------------
newrepo r1
fx commit "$root/remotes/r1.git" $((NOW-20*DAY)) "_kos/ideas/stale.md=$(idea Stale)" "_kos/ideas/recent.md=$(idea Recent)"
fx commit "$root/remotes/r1.git" $((NOW-10*DAY)) "_kos/ideas/recent.md=$(idea Recent 'touched lately')"
checkout r1
run >"$root/j" 2>"$root/e" || bad "dbi runs" "$(cat "$root/e")"
[[ "$(rows <"$root/j")" == "[('r1/_kos/ideas/stale.md', 'no pointer')]" ]] && ok "1: an idea with no pointer, 20 days quiet, is listed as no pointer" || bad "1: no pointer" "$(rows <"$root/j")"
[[ "$(q "[r['quiet_days'] for r in d['rows']]" <"$root/j")" == "[20]" ]] && ok "quiet days count from the last commit on the default branch" || bad "quiet days" "$(q "d['rows']" <"$root/j")"
[[ "$(q "set(d['rows'][0])>={'path','title','quiet_days','reason','pointers','as_of','remote'}" <"$root/j")" == "True" ]] && ok "a row has the section 5 fields" || bad "row fields"
[[ "$(q "d['rows'][0]['title']" <"$root/j")" == "Stale" ]] && ok "the title is the first heading" || bad "title"

# --- 2: pointers -------------------------------------------------------------------------
newrepo r2
fx commit "$root/remotes/r2.git" $((NOW-30*DAY)) \
  "_kos/ideas/open-bd.md=$(idea OpenBd 'Tracked in aae-orc-abcd1.')" \
  "_kos/ideas/closed-bd.md=$(idea ClosedBd 'Tracked in aae-orc-zzzz9.')" \
  "_kos/ideas/open-pr.md=$(idea OpenPr 'See o/r#7.')" \
  "_kos/ideas/merged-pr.md=$(idea MergedPr 'See o/r#8.')" \
  "_kos/ideas/cited.md=$(idea Cited 'No pointer in the text.')"
checkout r2
cat >"$DBI_FIX/bd.json" <<J
[{"id":"aae-orc-abcd1","status":"open","description":""},
 {"id":"aae-orc-zzzz9","status":"closed","description":""},
 {"id":"aae-orc-cite1","status":"open","description":"Follow-up to _kos/ideas/cited.md"}]
J
printf 'o/r#7 OPEN\no/r#8 MERGED\n' >"$DBI_FIX/gh.tsv"
run >"$root/j" 2>/dev/null
r2="$(q "sorted((r['path'], r['reason']) for r in d['rows'] if r['path'].startswith('r2/'))" <"$root/j")"
[[ "$r2" == "[('r2/_kos/ideas/closed-bd.md', 'pointers all closed'), ('r2/_kos/ideas/merged-pr.md', 'pointers all closed')]" ]] && ok "2: open bd, open PR and a ticket citing the path keep an idea off the list; closed ones list it as pointers all closed" || bad "2: pointers" "$r2"
echo '[]' >"$DBI_FIX/bd.json"; : >"$DBI_FIX/gh.tsv"

# --- 3, 7a, 7c: successors and cycles -------------------------------------------------------
newrepo r3
fx commit "$root/remotes/r3.git" $((NOW-30*DAY)) \
  "_kos/ideas/old.md=$(idea Old 'Superseded by succ-stalled.md.')" \
  "_kos/ideas/succ-stalled.md=$(idea SuccStalled 'Nothing forward.')" \
  "_kos/ideas/old2.md=$(idea Old2 'Became succ-live.md.')" \
  "_kos/ideas/succ-live.md=$(idea SuccLive 'Tracked in aae-orc-abcd1.')" \
  "_kos/ideas/ca.md=$(idea CA 'Superseded by cb.md.')" \
  "_kos/ideas/cb.md=$(idea CB 'Superseded by ca.md.')" \
  "_kos/ideas/ta.md=$(idea TA 'Superseded by tb.md.')" \
  "_kos/ideas/tb.md=$(idea TB 'Superseded by tc.md.')" \
  "_kos/ideas/tc.md=$(idea TC 'Superseded by ta.md.')" \
  "_kos/ideas/wa.md=$(idea WA 'Superseded by wb.md.')" \
  "_kos/ideas/wb.md=$(idea WB 'Superseded by wa.md. Became wc.md.')" \
  "_kos/ideas/wc.md=$(idea WC 'Tracked in aae-orc-abcd1.')"
checkout r3
echo '[{"id":"aae-orc-abcd1","status":"open","description":""}]' >"$DBI_FIX/bd.json"
run >"$root/j" 2>/dev/null
r3="$(q "sorted((r['path'][3:], r['reason']) for r in d['rows'] if r['path'].startswith('r3/'))" <"$root/j")"
[[ "$r3" == "[('_kos/ideas/ca.md', 'successor cycle'), ('_kos/ideas/cb.md', 'successor cycle'), ('_kos/ideas/old.md', 'successor stalled'), ('_kos/ideas/succ-stalled.md', 'no pointer'), ('_kos/ideas/ta.md', 'successor cycle'), ('_kos/ideas/tb.md', 'successor cycle'), ('_kos/ideas/tc.md', 'successor cycle')]" ]] \
  && ok "3, 7a, 7c: a stalled successor, a live successor, two- and three-member cycles, and a cycle with a way out" || bad "3, 7a, 7c" "$r3"

# --- 4, 6: resolved, parked, never-read roots ---------------------------------------------------
newrepo r4
fx commit "$root/remotes/r4.git" $((NOW-30*DAY)) \
  "_kos/ideas/grave.md=$(printf '# Grave\n\nstatus: graveyard\n')" \
  "_kos/ideas/parked.md=$(idea Parked)" \
  "_kos/ideas/live.md=$(idea Live)"
checkout r4
printf '%s  2026-10-01  operator\n' "r4/_kos/ideas/parked.md" >"$DIRECTOR_STATE/ideas-parked.txt"
newrepo run-x; newrepo r6
fx commit "$root/remotes/r6.git" $((NOW-30*DAY)) "_kos/ideas/x.md=$(idea X)"
checkout r6
mkdir -p "$root/orc/run/x/_kos/ideas" "$root/orc/r6-wt-a/_kos/ideas"
cp -R "$root/orc/r6/.git" "$root/orc/r6-wt-a/.git"; echo "$(idea W)" >"$root/orc/r6-wt-a/_kos/ideas/w.md"
cp -R "$root/orc/r6/.git" "$root/orc/run/x/.git"; echo "$(idea R)" >"$root/orc/run/x/_kos/ideas/r.md"
mkdir -p "$root/unenc/r6-wt-b/_kos/ideas"; cp -R "$root/orc/r6/.git" "$root/unenc/r6-wt-b/.git"; echo "$(idea U)" >"$root/unenc/r6-wt-b/_kos/ideas/u.md"
git init -q "$root/orc"
cat >>"$DIRECTOR_STATE/idea-roots.conf" <<C
$root/orc/run/*/_kos/ideas/*.md
$root/orc/*-wt-*/_kos/ideas/*.md
$root/orc/run/x/_kos/ideas/*.md
$root/orc/r6-wt-a/_kos/ideas/*.md
$root/unenc/r6-wt-b/_kos/ideas/*.md
C
run >"$root/j" 2>"$root/e"
r4="$(q "sorted(r['path'] for r in d['rows'] if r['path'].startswith('r4/'))" <"$root/j")"
[[ "$r4" == "['r4/_kos/ideas/live.md']" ]] && ok "4: a graveyard status and a parked path are not listed" || bad "4" "$r4"
[[ "$(q "any(r['path'].startswith(('run/','x/','r6-wt-a','r6-wt-b')) for r in d['rows'])" <"$root/j")" == "False" ]] && ok "6: run/ and a worktree directory are never read" || bad "6: never read"
[[ "$(q "len(d['skipped'])>=2 and any('run' in x['path'] for x in d['skipped']) and any('-wt-' in x['path'] for x in d['skipped'])" <"$root/j")" == "True" ]] && ok "6: a root that names run/ or a worktree directory is still excluded, and the skips are logged" || bad "6: skips logged" "$(q "d['skipped']" <"$root/j")"
cfg

# --- 7b: read by ref, from the default branch -----------------------------------------------------
newrepo r7
fx commit "$root/remotes/r7.git" $((NOW-30*DAY)) "_kos/ideas/on-main.md=$(idea OnMain)" "_kos/ideas/deleted-in-wt.md=$(idea DeletedInWt)"
checkout r7
echo "$(idea Draft)" >"$root/orc/r7/_kos/ideas/draft.md"; rm "$root/orc/r7/_kos/ideas/deleted-in-wt.md"
run >"$root/j" 2>/dev/null
r7="$(q "sorted(r['path'] for r in d['rows'] if r['path'].startswith('r7/'))" <"$root/j")"
[[ "$r7" == "['r7/_kos/ideas/deleted-in-wt.md', 'r7/_kos/ideas/on-main.md']" ]] && ok "7b: a file only in the working tree is not read; one deleted there is" || bad "7b" "$r7"
rm -f "$root/orc/r7/_kos/ideas/draft.md"

# --- 7: bd unreachable --------------------------------------------------------------------------------
touch "$DBI_FIX/bd.down"
run >"$root/j" 2>/dev/null
[[ "$(q "'bd' in d['unread']" <"$root/j")" == "True" ]] && ok "7: an unreachable bd is named in the output" || bad "7: unread bd" "$(q "d['unread']" <"$root/j")"
[[ "$(q "any(r['path'].startswith('r1/') for r in d['rows'])" <"$root/j")" == "False" ]] && ok "7: an idea that would be stalled is not listed while bd is unread" || bad "7: not listed while bd unread"
[[ "$(q "d['unverified']['count']>=1 and d['unverified']['kinds'].get('bd',0)>=1" <"$root/j")" == "True" ]] && ok "7: it is counted as unverified, kind bd" || bad "7: unverified count" "$(q "d['unverified']" <"$root/j")"
rm -f "$DBI_FIX/bd.down"

# --- 7: gh unreachable: the ideas that cite a PR are unverified, not read as no pointer ----------------------
printf 'o/r#7 OPEN\no/r#8 MERGED\n' >"$DBI_FIX/gh.tsv"; touch "$DBI_FIX/gh.down"
run >"$root/j" 2>/dev/null
[[ "$(q "'gh' in d['unread'] and d['unverified']['kinds'].get('gh',0)==2" <"$root/j")" == "True" ]] && ok "7: an unreachable gh is named and the two ideas that cite a PR are unverified" || bad "7: gh down" "$(q "[d['unread'], d['unverified']]" <"$root/j")"
[[ "$(q "any('open-pr' in r['path'] or 'merged-pr' in r['path'] for r in d['rows'])" <"$root/j")" == "False" ]] && ok "7: they are not listed while gh is unread" || bad "7: not listed while gh unread"
rm -f "$DBI_FIX/gh.down"; : >"$DBI_FIX/gh.tsv"

# --- 7d, 7g: no fetch, tags, stale and unreachable ---------------------------------------------------------
newrepo r8
fx commit "$root/remotes/r8.git" $((NOW-40*DAY)) "_kos/ideas/old.md=$(idea Old8)"
checkout r8
fx commit "$root/remotes/r8.git" $((NOW-1*DAY)) "_kos/ideas/new.md=$(idea New8)"
snap() { (cd "$root/orc/r8" && { git for-each-ref; cat .git/FETCH_HEAD 2>/dev/null || true; find .git/logs -type f -exec cat {} + 2>/dev/null || true; } | shasum -a 256); }
b="$(snap)"; run >"$root/j" 2>/dev/null; a="$(snap)"
[[ "$b" == "$a" ]] && ok "7d: the scan never fetches (refs, FETCH_HEAD and reflogs unchanged)" || bad "7d: no fetch"
[[ "$(q "[(r['remote'], r['as_of']) for r in d['rows'] if r['path']=='r8/_kos/ideas/old.md']" <"$root/j")" == "[('stale', '2026-09-10')]" ]] && ok "7g: a stale repo's row carries remote stale and as_of" || bad "7g: stale tag" "$(q "d['rows']" <"$root/j")"
[[ "$(q "[(r['remote'], r['as_of']) for r in d['rows'] if r['path']=='r1/_kos/ideas/stale.md']" <"$root/j")" == "[('current', None)]" ]] && ok "7g: a current repo's row carries remote current and as_of null" || bad "7g: current tag"
mv "$root/remotes/r8.git" "$root/remotes/r8.gone"
run >"$root/j" 2>/dev/null
[[ "$(q "[r['remote'] for r in d['rows'] if r['path']=='r8/_kos/ideas/old.md']" <"$root/j")" == "['unreachable']" ]] && ok "7d, 7g: an unreachable remote with a local ref is judged and tagged unreachable" || bad "7d: unreachable tag" "$(q "d['rows']" <"$root/j")"
mv "$root/remotes/r8.gone" "$root/remotes/r8.git"
git -C "$root/orc/r8" update-ref -d refs/remotes/origin/main; mv "$root/remotes/r8.git" "$root/remotes/r8.gone"
run >"$root/j" 2>/dev/null
[[ "$(q "any(r['path'].startswith('r8/') for r in d['rows']) or ('remote unreachable' not in d['unverified']['kinds'])" <"$root/j")" == "False" ]] && ok "7d: a repo with no local default-branch ref is unread and its ideas are unverified (remote unreachable)" || bad "7d: unread repo" "$(q "[d['rows'], d['unverified']]" <"$root/j")"
mv "$root/remotes/r8.gone" "$root/remotes/r8.git"; git -C "$root/orc/r8" fetch -q origin 2>/dev/null || true

# --- 7e, 7f: no prompt, the pool deadline binds ----------------------------------------------------------------
mkdir -p "$root/hang"
cat >"$root/hang/ssh" <<'S'
#!/usr/bin/env bash
echo $$ >>"$HANG_PIDS"
sleep 300 &
echo $! >>"$HANG_PIDS"
wait
S
chmod +x "$root/hang/ssh"
export HANG_PIDS="$root/hang/pids"; : >"$HANG_PIDS"
for i in $(seq 1 12); do
  d="$root/hangorc/h$i"; mkdir -p "$d/_kos/ideas"; git init -q "$d"
  git -C "$d" remote add origin "ssh://hang.invalid/h$i.git"
  mkdir -p "$d/.git/refs/remotes/origin"
done
printf '%s\n' "$root/hangorc/*/_kos/ideas/*.md" >"$DIRECTOR_STATE/idea-roots.conf"
start=$(date +%s)
GIT_SSH="$root/hang/ssh" GIT_SSH_COMMAND= DBI_CALL_TIMEOUT=2 DBI_REMOTE_DEADLINE=3 run >"$root/j" 2>"$root/e" || true
took=$(( $(date +%s) - start ))
[[ "$took" -le 5 ]] && ok "7f: the remote step returns within the deadline plus one second ($took s)" || bad "7f: deadline" "took $took s"
sleep 1
alive=0; while read -r p; do kill -0 "$p" 2>/dev/null && alive=$((alive+1)); done <"$HANG_PIDS"
[[ "$alive" == 0 ]] && ok "7f: no fake ssh or child is alive afterwards" || { bad "7f: stray processes" "$alive alive"; while read -r p; do kill "$p" 2>/dev/null || true; done <"$HANG_PIDS"; }
[[ "$(wc -l <"$HANG_PIDS" | tr -d ' ')" -gt 0 ]] && ok "7f: the fake ssh really ran" || bad "7f: the fake ssh really ran"
grep -q 'BatchMode=yes' "$here/dbi" && ok "7e: the ssh command line carries BatchMode=yes" || bad "7e: BatchMode=yes"
grep -q 'start_new_session=True' "$here/dbi" && ok "7e: each call runs in its own session" || bad "7e: own session"


# --- review 5580238780: a missing roots file is loud, never a clean scan --------------------------
cfg
mv "$DIRECTOR_STATE/idea-roots.conf" "$root/roots.saved"
"$DBI" --json --threshold-days 14 >"$root/o" 2>"$root/e" && rc=0 || rc=$?
[[ "$rc" == 2 ]] && grep -q 'idea-roots.conf' "$root/e" && ! grep -q '"rows"' "$root/o" && ok "a missing roots file exits 2 naming the file and prints no report" || bad "a missing roots file is loud" "rc=$rc $(cat "$root/e")"
: >"$DIRECTOR_STATE/idea-roots.conf"
"$DBI" --json --threshold-days 14 >"$root/o" 2>"$root/e" && rc=0 || rc=$?
[[ "$rc" == 2 ]] && ! grep -q '"rows"' "$root/o" && ok "a roots file with no root exits 2 too" || bad "an empty roots file is loud" "rc=$rc"
mv "$root/roots.saved" "$DIRECTOR_STATE/idea-roots.conf"
"$DBI" --json --threshold-days -3 >"$root/o" 2>/dev/null && bad "a negative threshold is refused" || ok "a negative threshold is refused"

# --- a git read that fails is unverified, never "no pointer" --------------------------------------------
mkdir -p "$root/gitfail"; REALGIT="$(command -v git)"
cat >"$root/gitfail/git" <<S
#!/usr/bin/env bash
a=("\$@"); i=0; [[ "\${a[0]}" == "-C" ]] && i=2
if [[ -n "\${FAKE_GIT_FAIL:-}" && "\${a[\$i]}" == "\$FAKE_GIT_FAIL" && ( -z "\${FAKE_GIT_FAIL_ARG:-}" || "\$*" == *"\$FAKE_GIT_FAIL_ARG" ) ]]; then echo "fatal: fake failure of \$FAKE_GIT_FAIL" >&2; exit 128; fi
exec "$REALGIT" "\$@"
S
chmod +x "$root/gitfail/git"
newrepo g1
fx commit "$root/remotes/g1.git" $((NOW-30*DAY)) "_kos/ideas/g.md=$(idea G)"
checkout g1
for sub in ls-tree show log; do
  PATH="$root/gitfail:$PATH" FAKE_GIT_FAIL=$sub run >"$root/j" 2>/dev/null
  [[ "$(q "any(r['path'].startswith('g1/') for r in d['rows'])" <"$root/j")" == "False" ]] && ok "git $sub failing: the idea is not listed as no pointer" || bad "git $sub failing: not listed" "$(q "d['rows']" <"$root/j")"
  [[ "$(q "d['unverified']['kinds'].get('git read',0)>=1 and 'git read' in d['unread']" <"$root/j")" == "True" ]] && ok "git $sub failing: counted unverified, kind git read, and named in unread" || bad "git $sub failing: unverified" "$(q "[d['unverified'],d['unread']]" <"$root/j")"
done
# review 5580296034: one failed read makes every idea of that repo unverified
newrepo gb
fx commit "$root/remotes/gb.git" $((NOW-30*DAY)) "_kos/ideas/a.md=$(idea A 'Superseded by b.md.')" "_kos/ideas/b.md=$(idea B)" "_kos/ideas/c.md=$(idea C)"
checkout gb
printf '%s\n' "$root/orc/gb/_kos/ideas/*.md" >"$DIRECTOR_STATE/idea-roots.conf"
PATH="$root/gitfail:$PATH" FAKE_GIT_FAIL=show FAKE_GIT_FAIL_ARG=ideas/b.md run >"$root/j" 2>/dev/null
[[ "$(q "any(r['path'].startswith('gb/') for r in d['rows'])" <"$root/j")" == "False" ]] && ok "show failing for b.md only: no idea of that repo is listed (a is not no pointer)" || bad "gb: listed" "$(q "d['rows']" <"$root/j")"
[[ "$(q "d['unverified']['kinds'].get('git read',0)==3" <"$root/j")" == "True" ]] && ok "show failing for b.md only: all three ideas of the repo are counted unverified, git read" || bad "gb: unverified" "$(q "d['unverified']" <"$root/j")"
cfg
grep -q 'GIT_NO_LAZY_FETCH' "$here/dbi" && ok "git reads set GIT_NO_LAZY_FETCH" || bad "git reads set GIT_NO_LAZY_FETCH"

# --- review item 3: exclusion by realpath, and only below the glob's fixed prefix --------------------------
mkdir -p "$root/orc/run/lk"; newrepo lk; fx commit "$root/remotes/lk.git" $((NOW-30*DAY)) "_kos/ideas/lk.md=$(idea LK)"
git clone -q "$root/remotes/lk.git" "$root/orc/run/lk/blue" 2>/dev/null
ln -s "$root/orc/run/lk/blue" "$root/orc/linked-blue"
mkdir -p "$root/legit/run"; git clone -q "$root/remotes/lk.git" "$root/legit/run/lg" 2>/dev/null
printf '%s\n' "$root/orc/*/_kos/ideas/*.md" "$root/legit/run/*/_kos/ideas/*.md" >"$DIRECTOR_STATE/idea-roots.conf"
run >"$root/j" 2>/dev/null
[[ "$(q "any(r['path'].startswith('linked-blue/') for r in d['rows'])" <"$root/j")" == "False" ]] && ok "a symlink into run/ is never read" || bad "symlink into run/ read"
[[ "$(q "any('linked-blue' in x['path'] for x in d['skipped'])" <"$root/j")" == "True" ]] && ok "the symlinked root's skip is logged" || bad "symlink skip logged" "$(q "d['skipped']" <"$root/j")"
[[ "$(q "any(r['path'].startswith('lg/') for r in d['rows'])" <"$root/j")" == "True" ]] && ok "a checkout under a parent named run, named by the root's fixed prefix, is read" || bad "legit run parent" "$(q "[d['rows'],d['skipped']]" <"$root/j")"
cfg

echo "dbi: $pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
