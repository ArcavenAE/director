#!/usr/bin/env bash
# Prove dws refresh keeps its W2 contract (sim/design/board-workstream-ledger.md
# section 5 and section 11 item 3), with gh and bd stubbed: last moved follows
# the linked sources; building moves to in review when every linked PR has
# left draft, and in review to merged when every one has merged, as actor
# refresh; PRs that disagree give a hint and no move; a release tag gives a
# hint and no move; a consumed fact is never re-applied, so a revert holds; a
# row with no PR never moves; a PR closed unmerged is left out and shown; a
# link checked inside the skip window is not fetched again.
#
# Usage: skills/director/scripts/verify-dws-refresh.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DWS="${DWS:-$here/dws}"
pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
export DIRECTOR_STATE="$root/state"
mkdir -p "$DIRECTOR_STATE" "$root/bin" "$root/fix"
export FIX="$root/fix" CALLS="$root/calls"
: >"$CALLS"

# Stubs. gh pr view N -R o/r, gh issue view N -R o/r, gh api
# repos/o/r/releases/latest, gh api repos/o/r/compare/A...B and bd show ID read
# fixtures; every call is logged so a skipped fetch can be seen. A repo with no
# latest fixture answers releases/latest with a 404, as GitHub does for a repo
# with no stable release; a fixture holding ERR500 answers with a server error.
cat >"$root/bin/gh" <<'SH'
#!/usr/bin/env bash
echo "gh $*" >>"$CALLS"
key() { echo "$1" | tr '/#' '__'; }
case "$1 $2" in
  "pr view")      f="$FIX/pr-$(key "$5")-$3.json" ;;
  "issue view")   f="$FIX/issue-$(key "$5")-$3.json" ;;
  "api repos/"*/releases/latest)
    f="$FIX/latest-$(key "${2#repos/}" | sed 's/_releases_latest$//')"
    [[ -f "$f" ]] || { echo "gh: Not Found (HTTP 404)" >&2; exit 1; }
    grep -q ERR500 "$f" && { echo "gh: Server Error (HTTP 500)" >&2; exit 1; }
    cat "$f"; exit 0 ;;
  "api "*)        f="$FIX/compare-$(key "$2" | tr '.' '_')" ;;
  *) echo "stub gh: unexpected $*" >&2; exit 2 ;;
esac
[[ -f "$f" ]] || { echo "stub gh: no fixture $f" >&2; exit 1; }
cat "$f"
SH
cat >"$root/bin/bd" <<'SH'
#!/usr/bin/env bash
echo "bd $*" >>"$CALLS"
f="$FIX/bd-$2.json"
[[ -f "$f" ]] || { echo "stub bd: no fixture $f" >&2; exit 1; }
cat "$f"
SH
chmod +x "$root/bin/gh" "$root/bin/bd"
export PATH="$root/bin:$PATH"

pr() { # repo num state isDraft updatedAt mergedAt mergeCommit lastCommitDate reviewDate
  # The last commit defaults to updatedAt; pass it to model an update that is
  # not a commit, review, ready or merge (a comment).
  local merged=null commit=null committed="${8:-$5}" reviews="[]"
  [[ -n "${6:-}" ]] && merged="\"$6\""
  [[ -n "${7:-}" ]] && commit="{\"oid\":\"$7\"}"
  [[ -n "${9:-}" ]] && reviews="[{\"submittedAt\":\"$9\"}]"
  printf '{"state":"%s","isDraft":%s,"reviewDecision":"","updatedAt":"%s","mergedAt":%s,"mergeCommit":%s,"commits":[{"committedDate":"%s"}],"reviews":%s}\n' \
    "$3" "$4" "$5" "$merged" "$commit" "$committed" "$reviews" >"$FIX/pr-$(echo "$1" | tr '/#' '__')-$2.json"
}
row() { python3 -c 'import json,sys; print(json.dumps([r for r in json.load(sys.stdin)["rows"] if r["slug"]==sys.argv[1]][0]))' "$1"; }
field() { "$DWS" show --json | row "$1" | python3 -c 'import json,sys; v=json.load(sys.stdin).get(sys.argv[1]); print(" | ".join(v) if isinstance(v,list) else v)' "$2" 2>/dev/null || true; }
LEDGER="$DIRECTOR_STATE/workstreams.jsonl"
fresh() { export DWS_REFRESH_SKIP_SECONDS=0; }
fresh
# The clock (DWS_NOW): rows open before the fixtures' times, and refresh runs
# after them, so last moved is decided by the sources, never by the open.
export DWS_TEST_CLOCK=1
export DWS_NOW=2030-12-31T00:00:00Z
dws_open() { DWS_NOW=2030-09-01T00:00:00Z "$DWS" open "$@"; }

# --- building -> in review when every linked PR has left draft -------------
dws_open ready1 --title "ready" --stage building --link pr:o/r#1 >/dev/null 2>&1 || true
pr o/r 1 OPEN false 2030-10-02T10:00:00Z
"$DWS" refresh ready1 >"$root/out" 2>&1 || bad "refresh runs" "$(cat "$root/out")"
[[ "$(field ready1 stage)" == "in review" ]] && ok "a PR out of draft moves building to in review" || bad "a PR out of draft moves building to in review" "$(field ready1 stage)"
actor="$(python3 - "$LEDGER" <<'PY' 2>/dev/null || true
import json, sys
ev = [json.loads(l) for l in open(sys.argv[1]) if l.strip()]
print(",".join(e["actor"] for e in ev if e.get("ws") == "ready1" and e.get("event") == "stage"))
PY
)"
[[ "$actor" == refresh ]] && ok "the move is appended as actor refresh" || bad "the move is appended as actor refresh" "$actor"
[[ "$(field ready1 last_moved)" == "$DWS_NOW" ]] \
  && ok "a stage move dates last moved by the refresh that saw it" || bad "a stage move dates last moved by the refresh that saw it" "$(field ready1 last_moved)"

# --- in review -> merged when every linked PR has merged -------------------
dws_open merge1 --title "merge" --stage "in review" --link pr:o/r#2 >/dev/null 2>&1 || true
pr o/r 2 MERGED false 2030-10-02T11:00:00Z 2030-10-02T11:00:00Z aaa111
"$DWS" refresh merge1 >/dev/null 2>&1 || true
[[ "$(field merge1 stage)" == merged ]] && ok "a merged PR moves in review to merged" || bad "a merged PR moves in review to merged" "$(field merge1 stage)"

# --- a release tag gives a hint, never a move -----------------------------
dws_open rel1 --title "release" --stage merged --link pr:o/rel#3 >/dev/null 2>&1 || true
pr o/rel 3 MERGED false 2030-10-01T09:00:00Z 2030-10-01T09:00:00Z bbb222
echo v1.2.0 >"$FIX/latest-o_rel"
echo ahead >"$FIX/compare-repos_o_rel_compare_bbb222___v1_2_0"
"$DWS" refresh rel1 >/dev/null 2>&1 || true
[[ "$(field rel1 stage)" == merged ]] && ok "a release tag moves nothing" || bad "a release tag moves nothing" "$(field rel1 stage)"
field rel1 hints | grep -q 'v1.2.0' && ok "a release tag that contains the merge gives a hint" || bad "a release tag that contains the merge gives a hint" "$(field rel1 hints)"
grep -q 'api repos/o/rel/releases/latest' "$CALLS" && ! grep -q 'release list' "$CALLS" \
  && ok "the latest stable release is read with releases/latest" || bad "the latest stable release is read with releases/latest" "$(grep -E 'release' "$CALLS")"

# --- no stable release (a 404) is no hint and no failure -------------------
dws_open rel3 --title "no stable release" --stage "in review" --link pr:o/norel#13 >/dev/null 2>&1 || true
pr o/norel 13 MERGED false 2030-10-01T10:00:00Z 2030-10-01T10:00:00Z fff666
"$DWS" refresh rel3 >"$root/out" 2>&1 || true
[[ "$(field rel3 stage)" == merged ]] && ok "a repo with no stable release still moves to merged" || bad "a repo with no stable release still moves to merged" "$(field rel3 stage)"
field rel3 hints | grep -qi 'release' && bad "a 404 on releases/latest is no hint" "$(field rel3 hints)" || ok "a 404 on releases/latest is no hint"

# --- a failing release check is a hint; the merged fact still applies -----
dws_open rel2 --title "release check fails" --stage "in review" --link pr:o/rel2#11 >/dev/null 2>&1 || true
pr o/rel2 11 MERGED false 2030-10-01T09:30:00Z 2030-10-01T09:30:00Z eee555
echo v2.0.0 >"$FIX/latest-o_rel2"
"$DWS" refresh rel2 >"$root/out" 2>&1 || true
[[ "$(field rel2 stage)" == merged ]] && ok "a failing release check does not block the merged move" || bad "a failing release check does not block the merged move" "$(field rel2 stage): $(cat "$root/out")"
field rel2 hints | grep -qi 'release check' && ok "a failing release check becomes a hint" || bad "a failing release check becomes a hint" "$(field rel2 hints)"
dws_open rel4 --title "releases/latest fails" --stage "in review" --link pr:o/rel4#14 >/dev/null 2>&1 || true
pr o/rel4 14 MERGED false 2030-10-01T11:00:00Z 2030-10-01T11:00:00Z ggg777
echo ERR500 >"$FIX/latest-o_rel4"
"$DWS" refresh rel4 >/dev/null 2>&1 || true
[[ "$(field rel4 stage)" == merged ]] && field rel4 hints | grep -qi 'release check' \
  && ok "a server error on releases/latest is a hint, not a 404" || bad "a server error on releases/latest is a hint, not a 404" "$(field rel4 stage): $(field rel4 hints)"

# --- the skip window -------------------------------------------------------
export DWS_REFRESH_SKIP_SECONDS=600
dws_open skip1 --title "skip" --stage building --link pr:o/r#4 >/dev/null 2>&1 || true
pr o/r 4 OPEN true 2030-10-02T12:00:00Z
five_ago=2030-12-30T23:55:00Z
printf '{"ts":"%s","actor":"refresh","event":"observe","ws":"skip1","link":"pr:o/r#4","seen":{"kind":"pr","state":"draft"},"moved_at":"2030-10-02T12:00:00Z","checked_at":"%s"}\n' "$five_ago" "$five_ago" >>"$LEDGER"
: >"$CALLS"
"$DWS" refresh skip1 >/dev/null 2>&1 || true
grep -q 'pr view 4 ' "$CALLS" && bad "a link checked 5 minutes ago is not fetched" "$(cat "$CALLS")" || ok "a link checked 5 minutes ago is not fetched"
fresh
"$DWS" refresh skip1 >/dev/null 2>&1 || true
grep -q 'pr view 4 ' "$CALLS" && ok "outside the window the same link is fetched" || bad "outside the window the same link is fetched" "$(cat "$CALLS")"

# --- two PRs that disagree: a hint, no move --------------------------------
dws_open mix1 --title "mixed" --stage building --link pr:o/r#5 pr:o/r#6 >/dev/null 2>&1 || true
pr o/r 5 MERGED false 2030-10-02T13:00:00Z 2030-10-02T13:00:00Z ccc333
pr o/r 6 OPEN true 2030-10-02T13:00:00Z
"$DWS" refresh mix1 >/dev/null 2>&1 || true
[[ "$(field mix1 stage)" == building ]] && ok "PRs that disagree leave the stage" || bad "PRs that disagree leave the stage" "$(field mix1 stage)"
field mix1 hints | grep -qi 'disagree' && ok "PRs that disagree give a hint" || bad "PRs that disagree give a hint" "$(field mix1 hints)"

# --- a revert holds: no re-applied fact, a comment is not a new fact -------
dws_open rev1 --title "revert" --stage building --link pr:o/r#7 >/dev/null 2>&1 || true
pr o/r 7 MERGED false 2030-10-02T14:00:00Z 2030-10-02T14:00:00Z ddd444
"$DWS" refresh rev1 >/dev/null 2>&1 || true
if [[ "$(field rev1 stage)" == merged ]]; then ok "every PR merged carries building through to merged"; reverted=1; else bad "every PR merged carries building through to merged" "$(field rev1 stage)"; reverted=0; fi
DWS_NOW=2030-10-02T14:30:00Z "$DWS" stage rev1 building --from merged >/dev/null 2>&1 || bad "director moves the row back"
"$DWS" refresh rev1 >/dev/null 2>&1 || true
[[ $reverted == 1 && "$(field rev1 stage)" == building ]] && ok "the next refresh does not re-advance a reverted row" || bad "the next refresh does not re-advance a reverted row" "$(field rev1 stage)"
moved_before="$(field rev1 last_moved)"
pr o/r 7 MERGED false 2030-10-02T15:30:00Z 2030-10-02T14:00:00Z ddd444 2030-10-02T14:00:00Z
"$DWS" refresh rev1 >/dev/null 2>&1 || true
[[ $reverted == 1 && "$(field rev1 stage)" == building ]] && ok "a comment after the revert does not re-advance it" || bad "a comment after the revert does not re-advance it" "$(field rev1 stage)"
[[ -n "$moved_before" && "$(field rev1 last_moved)" == "$moved_before" ]] \
  && ok "a comment does not move last moved (design section 5 table)" || bad "a comment does not move last moved (design section 5 table)" "$moved_before -> $(field rev1 last_moved)"
dws_open com1 --title "commit moves" --stage building --link pr:o/r#12 >/dev/null 2>&1 || true
pr o/r 12 OPEN true 2030-10-03T08:00:00Z "" "" 2030-10-03T08:00:00Z
"$DWS" refresh com1 >/dev/null 2>&1 || true
pr o/r 12 OPEN true 2030-10-03T09:00:00Z "" "" 2030-10-03T09:00:00Z
"$DWS" refresh com1 >/dev/null 2>&1 || true
[[ "$(field com1 last_moved)" == "2030-10-03T09:00:00Z" ]] && ok "a new commit moves last moved" || bad "a new commit moves last moved" "$(field com1 last_moved)"

# --- review, ready and merge each move last moved; the future is clamped ---
dws_open rvw1 --title "review moves" --stage "in review" --link pr:o/r#15 >/dev/null 2>&1 || true
pr o/r 15 OPEN false 2030-10-04T10:00:00Z "" "" 2030-10-04T08:00:00Z 2030-10-04T10:00:00Z
"$DWS" refresh rvw1 >/dev/null 2>&1 || true
[[ "$(field rvw1 last_moved)" == "2030-10-04T10:00:00Z" ]] && ok "a review moves last moved" || bad "a review moves last moved" "$(field rvw1 last_moved)"
dws_open mrg1 --title "merge moves" --stage merged --link pr:o/r#16 >/dev/null 2>&1 || true
pr o/r 16 MERGED false 2030-10-04T11:00:00Z 2030-10-04T11:00:00Z hhh888 2030-10-04T08:00:00Z
"$DWS" refresh mrg1 >/dev/null 2>&1 || true
[[ "$(field mrg1 last_moved)" == "2030-10-04T11:00:00Z" ]] && ok "a merge moves last moved" || bad "a merge moves last moved" "$(field mrg1 last_moved)"
dws_open rdy1 --title "ready moves" --stage "in review" --link pr:o/r#17 >/dev/null 2>&1 || true
pr o/r 17 OPEN true 2030-10-04T08:00:00Z "" "" 2030-10-04T08:00:00Z
DWS_NOW=2030-10-05T00:00:00Z "$DWS" refresh rdy1 >/dev/null 2>&1 || true
pr o/r 17 OPEN false 2030-10-04T08:00:00Z "" "" 2030-10-04T08:00:00Z
DWS_NOW=2030-10-06T00:00:00Z "$DWS" refresh rdy1 >/dev/null 2>&1 || true
[[ "$(field rdy1 last_moved)" == "2030-10-06T00:00:00Z" ]] && ok "leaving draft moves last moved, dated by the refresh that saw it" || bad "leaving draft moves last moved, dated by the refresh that saw it" "$(field rdy1 last_moved)"
dws_open fut1 --title "future commit" --stage building --link pr:o/r#18 >/dev/null 2>&1 || true
pr o/r 18 OPEN true 2099-01-01T00:00:00Z "" "" 2099-01-01T00:00:00Z
"$DWS" refresh fut1 >/dev/null 2>&1 || true
[[ "$(field fut1 last_moved)" == "$DWS_NOW" ]] && ok "a source time in the future is clamped to now" || bad "a source time in the future is clamped to now" "$(field fut1 last_moved)"

# --- rows with no PR never move --------------------------------------------
dws_open iss1 --title "issue only" --stage building --link issue:o/r#8 >/dev/null 2>&1 || true
echo '{"state":"CLOSED","updatedAt":"2030-10-02T16:00:00Z"}' >"$FIX/issue-o_r-8.json"
dws_open bd1 --title "bd only" --stage "in review" --link bd:aae-orc-zz99 >/dev/null 2>&1 || true
echo '[{"id":"aae-orc-zz99","status":"closed","updated_at":"2030-10-02T16:30:00Z"}]' >"$FIX/bd-aae-orc-zz99.json"
"$DWS" refresh iss1 bd1 >/dev/null 2>&1 || true
[[ "$(field iss1 stage)" == building ]] && ok "an issue-only row never moves" || bad "an issue-only row never moves" "$(field iss1 stage)"
[[ "$(field bd1 stage)" == "in review" ]] && ok "a bd-only row never moves" || bad "a bd-only row never moves" "$(field bd1 stage)"
[[ "$(field iss1 last_moved)" == "2030-10-02T16:00:00Z" ]] \
  && ok "an issue update moves last moved" || bad "an issue update moves last moved" "$(field iss1 last_moved)"
grep -q 'bd show aae-orc-zz99' "$CALLS" && ok "a bd link is read with bd show" || bad "a bd link is read with bd show" "$(cat "$CALLS")"

# --- a PR closed unmerged is left out of the agreement and shown -----------
dws_open cls1 --title "closed" --stage building --link pr:o/r#9 pr:o/r#10 >/dev/null 2>&1 || true
pr o/r 9 OPEN false 2030-10-02T17:00:00Z
pr o/r 10 CLOSED false 2030-10-02T17:00:00Z
"$DWS" refresh cls1 >/dev/null 2>&1 || true
[[ "$(field cls1 stage)" == "in review" ]] && ok "a closed-unmerged PR does not block the move" || bad "a closed-unmerged PR does not block the move" "$(field cls1 stage)"
field cls1 hints | grep -q 'PR o/r#10 closed unmerged' && ok "the row shows PR #n closed unmerged" || bad "the row shows PR #n closed unmerged" "$(field cls1 hints)"

# --- an unreadable link is a warning, not a failure, and moves nothing -----
dws_open err1 --title "unreadable" --stage building --link pr:o/r#99 >/dev/null 2>&1 || true
if "$DWS" refresh err1 >"$root/out" 2>&1; then
  [[ "$(field err1 stage)" == building ]] && grep -qi 'pr:o/r#99' "$root/out" \
    && ok "an unreadable link is reported and moves nothing" || bad "an unreadable link is reported and moves nothing" "$(cat "$root/out")"
else
  bad "an unreadable link is reported and moves nothing" "refresh exited nonzero: $(cat "$root/out")"
fi

# --- links refresh does not read are said, and an id is never an option ---
dws_open unr1 --title "unread links" --stage building --link ask:01ABC ArcavenAE/director#1 bd:--db=/x >/dev/null 2>&1 || true
: >"$CALLS"
"$DWS" refresh unr1 >"$root/out" 2>&1 || true
grep -q 'ask:01ABC' "$root/out" && grep -q 'ArcavenAE/director#1' "$root/out" && grep -q 'bd:--db=/x' "$root/out" \
  && ok "links refresh does not read are named in its output" || bad "links refresh does not read are named in its output" "$(cat "$root/out")"
grep -q -- '--db' "$CALLS" && bad "a bd id that looks like an option is never passed to bd" "$(cat "$CALLS")" || ok "a bd id that looks like an option is never passed to bd"

# --- refresh with no slugs covers every row; director cannot be impersonated
: >"$CALLS"
"$DWS" refresh >/dev/null 2>&1 || true
grep -q 'pr view 1 ' "$CALLS" && grep -q 'issue view 8 ' "$CALLS" && ok "refresh with no slug reads every row" || bad "refresh with no slug reads every row" "$(cat "$CALLS")"
bad_actor="$(python3 - "$LEDGER" <<'PY' 2>/dev/null || true
import json, sys
n = seen = 0
for l in open(sys.argv[1]):
    try:
        e = json.loads(l)
    except ValueError:
        continue
    if e.get("event") == "observe" and e.get("actor") == "refresh" and e.get("ws") != "skip1":
        seen += 1
    if e.get("event") in ("observe",) and e.get("actor") != "refresh":
        n += 1
    if e.get("event") == "stage" and e.get("actor") == "refresh" and (e.get("from"), e.get("to")) not in (("building", "in review"), ("in review", "merged")):
        n += 1
print(n if seen else "no refresh observe events")
PY
)"
[[ "$bad_actor" == 0 ]] && ok "refresh appends only observe and the two factual moves, as refresh" || bad "refresh appends only observe and the two factual moves, as refresh" "$bad_actor"

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
