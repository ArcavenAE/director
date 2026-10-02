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

# Stubs. gh pr view N -R o/r, gh issue view N -R o/r, gh release list -R o/r,
# gh api repos/o/r/compare/A...B and bd show ID read fixtures; every call is
# logged so a skipped fetch can be seen.
cat >"$root/bin/gh" <<'SH'
#!/usr/bin/env bash
echo "gh $*" >>"$CALLS"
key() { echo "$1" | tr '/#' '__'; }
case "$1 $2" in
  "pr view")      f="$FIX/pr-$(key "$5")-$3.json" ;;
  "issue view")   f="$FIX/issue-$(key "$5")-$3.json" ;;
  "release list") f="$FIX/release-$(key "$4").json" ;;
  "api "*)        f="$FIX/compare-$(key "$2" | tr '.' '_')" ;;
  *) echo "stub gh: unexpected $*" >&2; exit 2 ;;
esac
[[ -f "$f" ]] || { [[ "$1 $2" == "release list" ]] && { echo '[]'; exit 0; }; echo "stub gh: no fixture $f" >&2; exit 1; }
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

pr() { # repo num state isDraft updatedAt mergedAt mergeCommit
  local merged=null commit=null
  [[ -n "${6:-}" ]] && merged="\"$6\""
  [[ -n "${7:-}" ]] && commit="{\"oid\":\"$7\"}"
  printf '{"state":"%s","isDraft":%s,"reviewDecision":"","updatedAt":"%s","mergedAt":%s,"mergeCommit":%s}\n' \
    "$3" "$4" "$5" "$merged" "$commit" >"$FIX/pr-$(echo "$1" | tr '/#' '__')-$2.json"
}
row() { python3 -c 'import json,sys; print(json.dumps([r for r in json.load(sys.stdin)["rows"] if r["slug"]==sys.argv[1]][0]))' "$1"; }
field() { "$DWS" show --json | row "$1" | python3 -c 'import json,sys; v=json.load(sys.stdin).get(sys.argv[1]); print(" | ".join(v) if isinstance(v,list) else v)' "$2" 2>/dev/null || true; }
LEDGER="$DIRECTOR_STATE/workstreams.jsonl"
fresh() { export DWS_REFRESH_SKIP_SECONDS=0; }
fresh

# --- building -> in review when every linked PR has left draft -------------
"$DWS" open ready1 --title "ready" --stage building --link pr:o/r#1 >/dev/null 2>&1 || true
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
[[ "$(field ready1 last_moved)" == "2030-10-02T10:00:00Z" ]] \
  && ok "last moved follows the PR's update" || bad "last moved follows the PR's update" "$(field ready1 last_moved)"

# --- in review -> merged when every linked PR has merged -------------------
"$DWS" open merge1 --title "merge" --stage "in review" --link pr:o/r#2 >/dev/null 2>&1 || true
pr o/r 2 MERGED false 2030-10-02T11:00:00Z 2030-10-02T11:00:00Z aaa111
"$DWS" refresh merge1 >/dev/null 2>&1 || true
[[ "$(field merge1 stage)" == merged ]] && ok "a merged PR moves in review to merged" || bad "a merged PR moves in review to merged" "$(field merge1 stage)"

# --- a release tag gives a hint, never a move -----------------------------
"$DWS" open rel1 --title "release" --stage merged --link pr:o/rel#3 >/dev/null 2>&1 || true
pr o/rel 3 MERGED false 2030-10-01T09:00:00Z 2030-10-01T09:00:00Z bbb222
echo '[{"tagName":"v1.2.0","publishedAt":"2030-10-02T09:00:00Z"}]' >"$FIX/release-o_rel.json"
echo ahead >"$FIX/compare-repos_o_rel_compare_bbb222___v1_2_0"
"$DWS" refresh rel1 >/dev/null 2>&1 || true
[[ "$(field rel1 stage)" == merged ]] && ok "a release tag moves nothing" || bad "a release tag moves nothing" "$(field rel1 stage)"
field rel1 hints | grep -q 'v1.2.0' && ok "a release tag that contains the merge gives a hint" || bad "a release tag that contains the merge gives a hint" "$(field rel1 hints)"

# --- the skip window -------------------------------------------------------
export DWS_REFRESH_SKIP_SECONDS=600
"$DWS" open skip1 --title "skip" --stage building --link pr:o/r#4 >/dev/null 2>&1 || true
pr o/r 4 OPEN true 2030-10-02T12:00:00Z
five_ago="$(python3 -c 'import datetime as d; print((d.datetime.now(d.timezone.utc)-d.timedelta(minutes=5)).strftime("%Y-%m-%dT%H:%M:%SZ"))')"
printf '{"ts":"%s","actor":"refresh","event":"observe","ws":"skip1","link":"pr:o/r#4","seen":{"kind":"pr","state":"draft"},"moved_at":"2030-10-02T12:00:00Z","checked_at":"%s"}\n' "$five_ago" "$five_ago" >>"$LEDGER"
: >"$CALLS"
"$DWS" refresh skip1 >/dev/null 2>&1 || true
grep -q 'pr view 4 ' "$CALLS" && bad "a link checked 5 minutes ago is not fetched" "$(cat "$CALLS")" || ok "a link checked 5 minutes ago is not fetched"
fresh
"$DWS" refresh skip1 >/dev/null 2>&1 || true
grep -q 'pr view 4 ' "$CALLS" && ok "outside the window the same link is fetched" || bad "outside the window the same link is fetched" "$(cat "$CALLS")"

# --- two PRs that disagree: a hint, no move --------------------------------
"$DWS" open mix1 --title "mixed" --stage building --link pr:o/r#5 pr:o/r#6 >/dev/null 2>&1 || true
pr o/r 5 MERGED false 2030-10-02T13:00:00Z 2030-10-02T13:00:00Z ccc333
pr o/r 6 OPEN true 2030-10-02T13:00:00Z
"$DWS" refresh mix1 >/dev/null 2>&1 || true
[[ "$(field mix1 stage)" == building ]] && ok "PRs that disagree leave the stage" || bad "PRs that disagree leave the stage" "$(field mix1 stage)"
field mix1 hints | grep -qi 'disagree' && ok "PRs that disagree give a hint" || bad "PRs that disagree give a hint" "$(field mix1 hints)"

# --- a revert holds: no re-applied fact, a comment is not a new fact -------
"$DWS" open rev1 --title "revert" --stage building --link pr:o/r#7 >/dev/null 2>&1 || true
pr o/r 7 MERGED false 2030-10-02T14:00:00Z 2030-10-02T14:00:00Z ddd444
"$DWS" refresh rev1 >/dev/null 2>&1 || true
if [[ "$(field rev1 stage)" == merged ]]; then ok "every PR merged carries building through to merged"; reverted=1; else bad "every PR merged carries building through to merged" "$(field rev1 stage)"; reverted=0; fi
"$DWS" stage rev1 building --from merged >/dev/null 2>&1 || bad "director moves the row back"
"$DWS" refresh rev1 >/dev/null 2>&1 || true
[[ $reverted == 1 && "$(field rev1 stage)" == building ]] && ok "the next refresh does not re-advance a reverted row" || bad "the next refresh does not re-advance a reverted row" "$(field rev1 stage)"
pr o/r 7 MERGED false 2030-10-02T15:30:00Z 2030-10-02T14:00:00Z ddd444
"$DWS" refresh rev1 >/dev/null 2>&1 || true
[[ $reverted == 1 && "$(field rev1 stage)" == building ]] && ok "a comment after the revert does not re-advance it" || bad "a comment after the revert does not re-advance it" "$(field rev1 stage)"
[[ "$(field rev1 last_moved)" == "2030-10-02T15:30:00Z" ]] \
  && ok "the comment still moves last moved" || bad "the comment still moves last moved" "$(field rev1 last_moved)"

# --- rows with no PR never move --------------------------------------------
"$DWS" open iss1 --title "issue only" --stage building --link issue:o/r#8 >/dev/null 2>&1 || true
echo '{"state":"CLOSED","updatedAt":"2030-10-02T16:00:00Z"}' >"$FIX/issue-o_r-8.json"
"$DWS" open bd1 --title "bd only" --stage "in review" --link bd:aae-orc-zz99 >/dev/null 2>&1 || true
echo '[{"id":"aae-orc-zz99","status":"closed","updated_at":"2030-10-02T16:30:00Z"}]' >"$FIX/bd-aae-orc-zz99.json"
"$DWS" refresh iss1 bd1 >/dev/null 2>&1 || true
[[ "$(field iss1 stage)" == building ]] && ok "an issue-only row never moves" || bad "an issue-only row never moves" "$(field iss1 stage)"
[[ "$(field bd1 stage)" == "in review" ]] && ok "a bd-only row never moves" || bad "a bd-only row never moves" "$(field bd1 stage)"
[[ "$(field iss1 last_moved)" == "2030-10-02T16:00:00Z" ]] \
  && ok "an issue update moves last moved" || bad "an issue update moves last moved" "$(field iss1 last_moved)"
grep -q 'bd show aae-orc-zz99' "$CALLS" && ok "a bd link is read with bd show" || bad "a bd link is read with bd show" "$(cat "$CALLS")"

# --- a PR closed unmerged is left out of the agreement and shown -----------
"$DWS" open cls1 --title "closed" --stage building --link pr:o/r#9 pr:o/r#10 >/dev/null 2>&1 || true
pr o/r 9 OPEN false 2030-10-02T17:00:00Z
pr o/r 10 CLOSED false 2030-10-02T17:00:00Z
"$DWS" refresh cls1 >/dev/null 2>&1 || true
[[ "$(field cls1 stage)" == "in review" ]] && ok "a closed-unmerged PR does not block the move" || bad "a closed-unmerged PR does not block the move" "$(field cls1 stage)"
field cls1 hints | grep -q 'PR o/r#10 closed unmerged' && ok "the row shows PR #n closed unmerged" || bad "the row shows PR #n closed unmerged" "$(field cls1 hints)"

# --- an unreadable link is a warning, not a failure, and moves nothing -----
"$DWS" open err1 --title "unreadable" --stage building --link pr:o/r#99 >/dev/null 2>&1 || true
if "$DWS" refresh err1 >"$root/out" 2>&1; then
  [[ "$(field err1 stage)" == building ]] && grep -qi 'pr:o/r#99' "$root/out" \
    && ok "an unreadable link is reported and moves nothing" || bad "an unreadable link is reported and moves nothing" "$(cat "$root/out")"
else
  bad "an unreadable link is reported and moves nothing" "refresh exited nonzero: $(cat "$root/out")"
fi

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
