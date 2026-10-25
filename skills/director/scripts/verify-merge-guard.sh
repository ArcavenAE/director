#!/usr/bin/env bash
# Prove merge-guard fails closed (R-152) and enforces operator merge exclusions
# (R-153): a PR in an excluded repo carrying an excluded label never proceeds,
# and a label set or an exclusion record that cannot be read stops. A read that errors, times out, comes
# back empty or unparseable, or reports UNKNOWN stops the merge run; only a
# CLEAN, open, non-draft PR with passing checks, no stacked child, and a
# non-author approval at the current head, given after that head reached
# GitHub, and no standing change request on any commit, proceeds.
# gh is a stub that replays fixtures, so nothing reaches GitHub. Exclusion
# records are read from rulings.jsonl (MERGE_GUARD_RULINGS), one JSON object per
# line; only kind "exclusion" lines are read here, and the fixture's own file
# stands in for the operator's.
#
# Usage: skills/director/scripts/verify-merge-guard.sh   Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GUARD="${MERGE_GUARD:-$here/merge-guard}"
[[ -x "$GUARD" ]] || { echo "merge-guard not executable at $GUARD"; exit 2; }

pass=0; fail=0
ok()  { echo "PASS $1"; pass=$((pass+1)); }
bad() { echo "FAIL $1${2:+ -- $2}"; fail=$((fail+1)); }

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
mkdir -p "$root/bin"

# The gh stub. `gh pr view` replays view.<call>.json (or view.json) with exit
# code view.rc; `gh pr list` replays list.json with list.rc. Stderr text comes
# from <verb>.err. `gh api` (the check suites at the head) replays suites.json
# with suites.rc; each call also appends its arguments to <verb>.args. `gh repo view` (the default branch) replays repo.json with repo.rc. Each call is counted, so a retry is observable.
cat > "$root/bin/gh" <<'STUB'
#!/usr/bin/env bash
fx="$GH_FIXTURE"
case "$1 $2" in
  "pr view") verb=view ;;
  "pr list") verb=list ;;
  "repo view") verb=repo ;;
  api\ *) verb=suites ;;
  *) echo "gh stub: unexpected call: $*" >&2; exit 97 ;;
esac
n=$(( $(cat "$fx/$verb.calls" 2>/dev/null || echo 0) + 1 ))
echo "$n" > "$fx/$verb.calls"
echo "$*" >> "$fx/$verb.args" # every call's arguments, so a test can say what was asked
[[ -f "$fx/$verb.err" ]] && cat "$fx/$verb.err" >&2
if [[ -f "$fx/$verb.$n.json" ]]; then cat "$fx/$verb.$n.json"
elif [[ -f "$fx/$verb.json" ]]; then cat "$fx/$verb.json"; fi
exit "$(cat "$fx/$verb.rc" 2>/dev/null || echo 0)"
STUB
chmod +x "$root/bin/gh"

# view JSON: state, isDraft, mergeStateStatus, then checks as a JSON array.
# The head commit is abc1234def5678, committed at HEAD_AT. By default one
# non-author (reviewer) approved it after that; REVIEWS overrides the list.
HEAD_AT="2026-09-28T10:00:00Z"
review() { # login state submittedAt [commit-oid]
  printf '{"author":{"login":"%s"},"state":"%s","submittedAt":"%s","commit":{"oid":"%s"}}' \
    "$1" "$2" "$3" "${4:-abc1234def5678}"
}
GOOD_REVIEW="$(review reviewer APPROVED 2026-09-28T11:00:00Z)"
view() { # state draft merge-state [rollup-json]; LABELS is the label names, comma separated
  local labels="" l list="${LABELS-}"
  for l in ${list//,/ }; do labels="${labels:+$labels,}{\"name\":\"$l\"}"; done
  printf '{"state":"%s","isDraft":%s,"mergeStateStatus":"%s","baseRefName":"%s","headRefName":"feat/x","headRefOid":"abc1234def5678","statusCheckRollup":%s,"author":{"login":"builder"},"commits":[{"oid":"0000000old","committedDate":"2026-09-27T09:00:00Z"},{"oid":"abc1234def5678","committedDate":"%s"}],"reviews":[%s],"labels":[%s]}\n' \
    "$1" "$2" "$3" "${BASE-main}" "${4:-[]}" "$HEAD_AT" "${REVIEWS-$GOOD_REVIEW}" "$labels"
}
SUCCESS_RUN='{"__typename":"CheckRun","name":"ci","status":"COMPLETED","conclusion":"SUCCESS"}'
SKIPPED_RUN='{"__typename":"CheckRun","name":"harden","status":"COMPLETED","conclusion":"SKIPPED"}'
FAILED_RUN='{"__typename":"CheckRun","name":"ci","status":"COMPLETED","conclusion":"FAILURE"}'
PENDING_RUN='{"__typename":"CheckRun","name":"ci","status":"IN_PROGRESS","conclusion":null}'
GREEN_STATUS='{"__typename":"StatusContext","context":"legacy","state":"SUCCESS"}'

n=0
# By default GitHub created the head's first check suite 5s after the commit.
suites() { # created_at... (none: no suite)
  local list="" t
  for t in "$@"; do list="${list:+$list,}{\"created_at\":\"$t\"}"; done
  printf '{"total_count":%d,"check_suites":[%s]}\n' "$#" "$list"
}
fixture() {
  n=$((n+1)); fx="$root/fx$n"; mkdir -p "$fx"; echo '[]' > "$fx/list.json"
  suites 2026-09-28T10:00:05Z > "$fx/suites.json"
  echo '{"defaultBranchRef":{"name":"main"}}' > "$fx/repo.json"
  : > "$fx/rulings.jsonl" # a present, empty rulings file: no exclusions declared
  mkdir -p "$fx/state"; : > "$fx/state/workstreams.jsonl" # a present, empty ledger: no row waits on a decision
}
guard() {
  env PATH="$root/bin:/usr/bin:/bin" GH_FIXTURE="$fx" \
      MERGE_GUARD_RETRIES="${RETRIES:-2}" MERGE_GUARD_DELAY=0 \
      MERGE_GUARD_RULINGS="${RULINGS:-$fx/rulings.jsonl}" \
      DIRECTOR_STATE="${STATE_DIR:-$fx/state}" DWS_TEST_CLOCK=1 DWS_NO_RERENDER=1 PYTHONDONTWRITEBYTECODE=1 \
      "$GUARD" "$@" >"$fx/out" 2>&1
}
expect_proceed() { # name
  if guard ${FLAGS-} org/repo 7; then
    grep -q "^PROCEED org/repo#7 @abc1234def5678" "$fx/out" && ok "$1" || bad "$1" "$(cat "$fx/out")"
  else
    bad "$1" "stopped: $(cat "$fx/out")"
  fi
}
# A PR whose only obstacle is an open stacked child gets PROCEED-KEEP-BRANCH: exit 0,
# the children named, and the merge command without --delete-branch. Its first word
# is not PROCEED, so a caller that matches the plain PROCEED line never takes it for
# permission to delete the branch.
expect_keep() { # name, child numbers the line must name (e.g. "#9")
  if guard ${FLAGS-} org/repo 7; then
    grep -q "^PROCEED-KEEP-BRANCH org/repo#7 @abc1234def5678" "$fx/out" \
      && grep -q -- "$2" "$fx/out" && grep -q -- "WITHOUT --delete-branch" "$fx/out" \
      && ! grep -q "^PROCEED org/repo" "$fx/out" && ok "$1" || bad "$1" "$(cat "$fx/out")"
  else
    bad "$1" "stopped: $(cat "$fx/out")"
  fi
}
expect_stop() { # name, text the STOP line must carry
  if guard ${FLAGS-} org/repo 7; then
    bad "$1" "proceeded: $(cat "$fx/out")"
  else
    local rc=$?
    grep -q "^STOP org/repo#7" "$fx/out" && grep -q -- "$2" "$fx/out" && [[ $rc -eq 1 ]] \
      && ok "$1" || bad "$1" "rc=$rc $(cat "$fx/out")"
  fi
}

# --- the one path that proceeds -------------------------------------------------
fixture; view OPEN false CLEAN "[$SUCCESS_RUN,$SKIPPED_RUN,$GREEN_STATUS]" > "$fx/view.json"
expect_proceed "CLEAN, open, not draft, checks passing, no child: PROCEED with the head sha"

fixture; view OPEN false CLEAN > "$fx/view.json"
expect_proceed "CLEAN with no checks reported: PROCEED (CLEAN already covers required checks)"

# --- UNKNOWN fails closed ---------------------------------------------------------
fixture; view OPEN false UNKNOWN > "$fx/view.json"
expect_stop "UNKNOWN that never resolves: STOP" "UNKNOWN"
[[ "$(cat "$fx/view.calls")" == 3 ]] && ok "UNKNOWN is re-read before stopping (1 + 2 retries)" \
  || bad "UNKNOWN re-read count" "calls=$(cat "$fx/view.calls")"

fixture; view OPEN false UNKNOWN > "$fx/view.1.json"; view OPEN false CLEAN > "$fx/view.2.json"
expect_proceed "UNKNOWN that resolves to CLEAN on a re-read: PROCEED"

fixture; view OPEN false UNKNOWN > "$fx/view.json"
RETRIES=0 expect_stop "UNKNOWN with retries off: STOP at once" "UNKNOWN"

# --- every other merge state stops ----------------------------------------------
for st in BLOCKED DIRTY BEHIND UNSTABLE HAS_HOOKS DRAFT; do
  fixture; view OPEN false "$st" > "$fx/view.json"
  expect_stop "merge state $st: STOP" "$st"
done
fixture; view OPEN false "" > "$fx/view.json"
expect_stop "an empty merge state: STOP" "merge state"

# --- a read that fails or returns nothing stops -----------------------------------
fixture; echo 1 > "$fx/view.rc"; echo "Post https://api.github.com/graphql: i/o timeout" > "$fx/view.err"
expect_stop "gh pr view times out: STOP with its output" "i/o timeout"
fixture; : > "$fx/view.json"
expect_stop "gh pr view returns nothing: STOP" "nothing"
fixture; echo "<html>502</html>" > "$fx/view.json"
expect_stop "gh pr view returns something that is not JSON: STOP" "not JSON"
fixture; echo '{"state":"OPEN","mergeStateStatus":"CLEAN","baseRefName":"main","headRefName":"feat/x","headRefOid":"abc1234def5678","statusCheckRollup":[],"labels":[]}' > "$fx/view.json"
expect_stop "a view with no isDraft field: STOP" "isDraft"

# --- terminal and draft states stop (R-157, R-148) --------------------------------
fixture; view MERGED false UNKNOWN > "$fx/view.json"
expect_stop "an already merged PR: STOP, terminal state read first" "MERGED"
fixture; view CLOSED false UNKNOWN > "$fx/view.json"
expect_stop "a closed PR: STOP" "CLOSED"
fixture; view OPEN true CLEAN > "$fx/view.json"
expect_stop "a draft: STOP" "draft"

# --- checks -----------------------------------------------------------------------
fixture; view OPEN false CLEAN "[$SUCCESS_RUN,$FAILED_RUN]" > "$fx/view.json"
expect_stop "a failed check: STOP naming it" "ci.*FAILURE"
fixture; view OPEN false CLEAN "[$PENDING_RUN]" > "$fx/view.json"
expect_stop "a check still running: STOP" "IN_PROGRESS"
fixture; view OPEN false CLEAN "null" > "$fx/view.json"
expect_stop "a check rollup that is not a list: STOP" "check"

# --- stacked children (the R-152 specimen) ----------------------------------------
fixture; view OPEN false CLEAN > "$fx/view.json"; echo 1 > "$fx/list.rc"
echo "Post https://api.github.com/graphql: i/o timeout" > "$fx/list.err"
expect_stop "the stacked-child read times out: STOP, never read as no children" "i/o timeout"
fixture; view OPEN false CLEAN > "$fx/view.json"; : > "$fx/list.json"
expect_stop "the stacked-child read returns nothing: STOP" "nothing"
fixture; view OPEN false CLEAN > "$fx/view.json"; echo '[{"number":9}]' > "$fx/list.json"
expect_keep "an open PR based on this one's head: PROCEED-KEEP-BRANCH naming it" "#9"
fixture; view OPEN false CLEAN > "$fx/view.json"; echo '[{"number":9},{"number":12}]' > "$fx/list.json"
expect_keep "two open children: PROCEED-KEEP-BRANCH naming both" "#9 #12"
fixture; view OPEN false CLEAN > "$fx/view.json"; echo '{"number":9}' > "$fx/list.json"
expect_stop "a child read that is not a list: STOP" "not a list"
# A child read the guard cannot read as a list of PRs with numbers stops, and never
# ends in a verdict (review of #311 at 2d4a4c7). Each shape below was exit 0 at that head.
for shape in '[{}]' '[{"number":9},7]' '[5]' '["x"]' '[null]' '[{"number":null}]' '[{"number":"9"}]' '[{"number":9},{}]' '[[9]]'; do
  fixture; view OPEN false CLEAN > "$fx/view.json"; printf '%s\n' "$shape" > "$fx/list.json"
  expect_stop "a child read of $shape: STOP, not a verdict" "stacked-child read"
done
# A child never softens any other stop: every other check still runs first.
fixture; view OPEN true CLEAN > "$fx/view.json"; echo '[{"number":9}]' > "$fx/list.json"
expect_stop "a draft base with a child: STOP" "draft"
fixture; view OPEN false BLOCKED > "$fx/view.json"; echo '[{"number":9}]' > "$fx/list.json"
expect_stop "a BLOCKED base with a child: STOP" "BLOCKED"
fixture; view OPEN false CLEAN "[$FAILED_RUN]" > "$fx/view.json"; echo '[{"number":9}]' > "$fx/list.json"
expect_stop "a failed check on a base with a child: STOP" "FAILURE"
fixture; REVIEWS="" view OPEN false CLEAN > "$fx/view.json"; echo '[{"number":9}]' > "$fx/list.json"
expect_stop "an unapproved base with a child: STOP" "no approval"
fixture; REVIEWS="$GOOD_REVIEW,$(review other CHANGES_REQUESTED 2026-09-28T12:00:00Z)" view OPEN false CLEAN > "$fx/view.json"; echo '[{"number":9}]' > "$fx/list.json"
expect_stop "a standing change request on a base with a child: STOP" "other"

# --- approval at the current head (director#159) -----------------------------------
fixture; REVIEWS="$(review reviewer APPROVED 2026-09-28T09:30:00Z)" view OPEN false CLEAN > "$fx/view.json"
expect_stop "an approval submitted before the head commit: STOP (the #159 specimen)" "before the head reached GitHub"
fixture; REVIEWS="$(review reviewer APPROVED 2026-09-28T11:00:00Z 0000000old)" view OPEN false CLEAN > "$fx/view.json"
expect_stop "an approval of an earlier commit: STOP" "no approval"
fixture; REVIEWS="" view OPEN false CLEAN > "$fx/view.json"
expect_stop "no reviews at all: STOP" "no approval"
fixture; REVIEWS="$(review builder APPROVED 2026-09-28T11:00:00Z)" view OPEN false CLEAN > "$fx/view.json"
expect_stop "an approval only by the PR's author: STOP" "no approval"
fixture; REVIEWS="$(review reviewer COMMENTED 2026-09-28T11:00:00Z)" view OPEN false CLEAN > "$fx/view.json"
expect_stop "a comment is not an approval: STOP" "no approval"
fixture; REVIEWS="$GOOD_REVIEW,$(review other CHANGES_REQUESTED 2026-09-28T12:00:00Z)" view OPEN false CLEAN > "$fx/view.json"
expect_stop "an approval beside a later change request at the head: STOP naming it" "other"
fixture; REVIEWS="$(review reviewer CHANGES_REQUESTED 2026-09-28T10:30:00Z),$GOOD_REVIEW" view OPEN false CLEAN > "$fx/view.json"
expect_proceed "a reviewer who requested changes and then approved at the head: PROCEED"
fixture; REVIEWS="$(review reviewer APPROVED 2026-09-28T09:30:00Z),$GOOD_REVIEW" view OPEN false CLEAN > "$fx/view.json"
expect_proceed "a stale approval followed by a fresh one at the head: PROCEED"
fixture; REVIEWS="$(review other CHANGES_REQUESTED 2026-09-28T09:00:00Z 0000000old),$GOOD_REVIEW" view OPEN false CLEAN > "$fx/view.json"
expect_stop "a change request on an earlier commit still stands beside a fresh approval: STOP naming it" "other at 0000000"
fixture; REVIEWS="$(review other CHANGES_REQUESTED 2026-09-28T09:00:00Z 0000000old),$(review other DISMISSED 2026-09-28T09:10:00Z 0000000old),$GOOD_REVIEW" view OPEN false CLEAN > "$fx/view.json"
expect_proceed "a dismissed change request on an earlier commit: PROCEED"

# --- the push anchor: a commit made before the approval and pushed after it --------
fixture; REVIEWS="$(review reviewer APPROVED 2026-09-28T10:30:00Z)" view OPEN false CLEAN > "$fx/view.json"
suites 2026-09-28T11:00:00Z > "$fx/suites.json"
expect_stop "an approval after the commit date but before the push: STOP (re-pinned, director#149)" "first check suite"
fixture; view OPEN false CLEAN > "$fx/view.json"; suites 2026-09-28T10:20:00Z 2026-09-28T10:00:05Z > "$fx/suites.json"
expect_proceed "the earliest of several check suites is the anchor: PROCEED"
fixture; view OPEN false CLEAN > "$fx/view.json"; suites > "$fx/suites.json"
expect_proceed "no check suite at the head: PROCEED on the commit date"
grep -q "push time is unknown" "$fx/out" && ok "with no check suite, PROCEED says the push time is unknown" \
  || bad "no-suite PROCEED line" "$(cat "$fx/out")"
fixture; view OPEN false CLEAN > "$fx/view.json"; echo 1 > "$fx/suites.rc"
echo "Get https://api.github.com/repos/org/repo: i/o timeout" > "$fx/suites.err"
expect_stop "the check-suite read times out: STOP" "i/o timeout"
fixture; view OPEN false CLEAN > "$fx/view.json"; echo '{"message":"Not Found"}' > "$fx/suites.json"
expect_stop "a check-suite read with no list: STOP" "check_suites"
fixture; view OPEN false CLEAN > "$fx/view.json"
echo '{"total_count":101,"check_suites":[{"created_at":"2026-09-28T10:00:05Z"}]}' > "$fx/suites.json"
expect_stop "a paged check-suite read: STOP" "paged"
fixture; echo '{"state":"OPEN","isDraft":false,"mergeStateStatus":"CLEAN","baseRefName":"main","headRefName":"feat/x","headRefOid":"abc1234def5678","statusCheckRollup":[],"author":{"login":"builder"},"commits":[],"reviews":[],"labels":[]}' > "$fx/view.json"
expect_stop "the head commit is not in the view's commit list: STOP" "head commit"
fixture; echo '{"state":"OPEN","isDraft":false,"mergeStateStatus":"CLEAN","baseRefName":"main","headRefName":"feat/x","headRefOid":"abc1234def5678","statusCheckRollup":[],"author":{"login":"builder"},"commits":[{"oid":"abc1234def5678","committedDate":"2026-09-28T10:00:00Z"}],"labels":[]}' > "$fx/view.json"
expect_stop "a view with no reviews field: STOP" "reviews"

# --- operator merge exclusions (R-153) ---------------------------------------------
excl() { printf '{"kind":"exclusion","repo":"%s","label":"%s"}\n' "$1" "$2"; }

fixture; LABELS="acu.update" view OPEN false CLEAN > "$fx/view.json"; excl org/repo "acu.update" > "$fx/rulings.jsonl"
expect_stop "an excluded PR (repo and label match): STOP naming the exclusion" "excluded"
grep -q "acu.update" "$fx/out" && ok "the exclusion STOP names the label" || bad "exclusion STOP names the label" "$(cat "$fx/out")"

fixture; LABELS="bug,acu.update,docs" view OPEN false CLEAN > "$fx/view.json"; excl org/repo "acu.update" > "$fx/rulings.jsonl"
expect_stop "an excluded label among several labels: STOP" "excluded"

fixture; LABELS="ACU.Update" view OPEN false CLEAN > "$fx/view.json"; excl ORG/Repo "acu.update" > "$fx/rulings.jsonl"
expect_stop "repo and label compare without case, as GitHub does: STOP" "excluded"

fixture; LABELS="acu.deps" view OPEN false CLEAN > "$fx/view.json"; excl org/repo 'acu.*' > "$fx/rulings.jsonl"
expect_stop "a label pattern (acu.*) matches: STOP" "excluded"

fixture; LABELS="acu.update" view OPEN true UNKNOWN > "$fx/view.json"; excl org/repo "acu.update" > "$fx/rulings.jsonl"
expect_stop "an excluded draft with an unresolved merge state: STOP on the exclusion, before the other checks" "excluded"
[[ "$(cat "$fx/view.calls")" == 1 ]] && ok "the exclusion is decided on the first read, with no UNKNOWN re-reads" \
  || bad "exclusion before the other checks" "calls=$(cat "$fx/view.calls")"

fixture; LABELS="acu.update" view MERGED false UNKNOWN > "$fx/view.json"; excl org/repo "acu.update" > "$fx/rulings.jsonl"
expect_stop "an excluded PR that is also merged: STOP, never PROCEED" "STOP"

# The reads-fine path, as hard as the refusals: nothing here may stop.
fixture; LABELS="acu.update" view OPEN false CLEAN > "$fx/view.json"; excl other/repo "acu.update" > "$fx/rulings.jsonl"
expect_proceed "an exclusion for another repo does not touch this PR: PROCEED"

fixture; LABELS="bug" view OPEN false CLEAN > "$fx/view.json"; excl org/repo "acu.update" > "$fx/rulings.jsonl"
expect_proceed "an exclusion for another label does not touch this PR: PROCEED"

fixture; LABELS="" view OPEN false CLEAN > "$fx/view.json"; excl org/repo "acu.update" > "$fx/rulings.jsonl"
expect_proceed "a PR with no labels in an excluded repo: PROCEED"

fixture; LABELS="acu" view OPEN false CLEAN > "$fx/view.json"; excl org/repo "acu.update" > "$fx/rulings.jsonl"
expect_proceed "a label that only starts like the excluded one is not a match: PROCEED"

fixture; LABELS="acu.update" view OPEN false CLEAN > "$fx/view.json"; excl org/repo-two "acu.update" > "$fx/rulings.jsonl"
expect_proceed "a repo that only starts like the excluded one is not a match: PROCEED"

fixture; LABELS="acu.update" view OPEN false CLEAN > "$fx/view.json"; excl org/rep "acu.update" > "$fx/rulings.jsonl"
expect_proceed "an excluded repo name that is a prefix of this repo is not a match: PROCEED"

fixture; LABELS="acu.update" view OPEN false CLEAN > "$fx/view.json"; excl org/repo "acu" > "$fx/rulings.jsonl"
expect_proceed "an excluded label that is a prefix of this label is not a match: PROCEED"

# Anchored at both ends: extra text before the excluded value is not a match.
fixture; LABELS="acu.update" view OPEN false CLEAN > "$fx/view.json"; excl org/repo "acu.update" > "$fx/rulings.jsonl"
if guard xorg/repo 7; then
  grep -q "^PROCEED xorg/repo#7 @" "$fx/out" && ok "a repo with extra text before the excluded one is not a match: PROCEED" \
    || bad "xorg/repo against org/repo" "$(cat "$fx/out")"
else bad "xorg/repo against org/repo" "stopped: $(cat "$fx/out")"; fi

fixture; LABELS="notacu.update" view OPEN false CLEAN > "$fx/view.json"; excl org/repo "acu.update" > "$fx/rulings.jsonl"
expect_proceed "a label with extra text before the excluded one is not a match (notacu.update, acu.update): PROCEED"

fixture; LABELS="acuprod" view OPEN false CLEAN > "$fx/view.json"; excl org/repo "acu.*" > "$fx/rulings.jsonl"
expect_proceed "a dot in an exclusion is a literal dot, not any character (acuprod, acu.*): PROCEED"

fixture; LABELS="acu.update,bug" view OPEN false CLEAN > "$fx/view.json"
{ echo; printf '{"kind":"ruling","text":"unrelated"}\n'; printf '{"kind":"grant","state":"draft"}\n'; excl other/repo "acu.update"; excl org/repo "release"; echo; } > "$fx/rulings.jsonl"
expect_proceed "other record kinds, blank lines and non-matching exclusions are read and ignored: PROCEED"
grep -q "2 exclusion record(s), none matching" "$fx/out" && ok "the PROCEED line says how many exclusions were read and that none matched" \
  || bad "PROCEED reports the exclusions read" "$(cat "$fx/out")"

fixture; LABELS="bug" view OPEN false CLEAN > "$fx/view.json"
expect_proceed "a present rulings file with no exclusions: PROCEED as before"
grep -q "no exclusion records" "$fx/out" && ok "with no exclusions the PROCEED line says there are no exclusion records" \
  || bad "PROCEED says no records" "$(cat "$fx/out")"

# A missing rulings file refuses: the guard cannot tell "none declared" from
# "the record is gone", so it does not guess.
fixture; LABELS="bug" view OPEN false CLEAN > "$fx/view.json"; rm -f "$fx/rulings.jsonl"
expect_stop "a missing rulings file: STOP, a missing record refuses" "rulings"
[[ ! -f "$fx/view.calls" ]] && ok "the missing-record STOP comes before any gh call" \
  || bad "missing record before the view" "view.calls=$(cat "$fx/view.calls")"
grep -q "The operator creates the file" "$fx/out" && grep -q "a seat reports this STOP and does not create the file" "$fx/out" \
  && ok "the missing-record STOP says the operator creates the file and a seat reports it" \
  || bad "missing-record STOP says what to do" "$(cat "$fx/out")"

fixture; view OPEN false CLEAN > "$fx/view.json"; : > "$fx/rulings.jsonl"
expect_proceed "an empty rulings file and a PR with neither labels nor exclusions: PROCEED"

# An unreadable label set stops.
fixture; echo '{"state":"OPEN","isDraft":false,"mergeStateStatus":"CLEAN","baseRefName":"main","headRefName":"feat/x","headRefOid":"abc1234def5678","statusCheckRollup":[],"author":{"login":"builder"},"commits":[{"oid":"abc1234def5678","committedDate":"2026-09-28T10:00:00Z"}],"reviews":[]}' > "$fx/view.json"
expect_stop "a view with no labels field: STOP, cannot tell what the PR carries" "labels"
fixture; echo '{"state":"OPEN","isDraft":false,"mergeStateStatus":"CLEAN","baseRefName":"main","headRefName":"feat/x","headRefOid":"abc1234def5678","statusCheckRollup":[],"author":{"login":"builder"},"commits":[],"reviews":[],"labels":null}' > "$fx/view.json"
expect_stop "labels that are not a list: STOP" "labels"
fixture; echo '{"state":"OPEN","isDraft":false,"mergeStateStatus":"CLEAN","baseRefName":"main","headRefName":"feat/x","headRefOid":"abc1234def5678","statusCheckRollup":[],"author":{"login":"builder"},"commits":[],"reviews":[],"labels":[{"id":"x"}]}' > "$fx/view.json"
expect_stop "a label with no name: STOP" "label"
fixture; echo '{"state":"OPEN","isDraft":false,"mergeStateStatus":"CLEAN","baseRefName":"main","headRefName":"feat/x","headRefOid":"abc1234def5678","statusCheckRollup":[],"author":{"login":"builder"},"commits":[],"reviews":[],"labels":[]}' > "$fx/view.json"
excl org/repo "acu.update" > "$fx/rulings.jsonl"
expect_stop "labels read as an empty list but the view is otherwise unreadable: STOP, never a pass" "head commit"

# An unreadable exclusion record stops, and stops before gh is asked anything.
fixture; LABELS="bug" view OPEN false CLEAN > "$fx/view.json"; { excl org/repo x; echo '{"kind":"exclusion","repo":"org/repo"'; } > "$fx/rulings.jsonl"
expect_stop "a torn exclusion line: STOP, a record that might be an exclusion cannot be skipped" "rulings"
[[ ! -f "$fx/view.calls" ]] && ok "the records are read before the PR view (gh was never called)" \
  || bad "records before the view" "view.calls=$(cat "$fx/view.calls")"
fixture; LABELS="bug" view OPEN false CLEAN > "$fx/view.json"; echo 'not json at all' > "$fx/rulings.jsonl"
expect_stop "a rulings line that is not JSON: STOP" "rulings"
fixture; LABELS="bug" view OPEN false CLEAN > "$fx/view.json"; echo '["exclusion","org/repo"]' > "$fx/rulings.jsonl"
expect_stop "a rulings line that is JSON but not an object: STOP" "rulings"
fixture; LABELS="bug" view OPEN false CLEAN > "$fx/view.json"; echo '{"kind":"exclusion","label":"acu.update"}' > "$fx/rulings.jsonl"
expect_stop "an exclusion with no repo: STOP, it cannot be matched" "repo"
fixture; LABELS="bug" view OPEN false CLEAN > "$fx/view.json"; echo '{"kind":"exclusion","repo":"org/repo","label":""}' > "$fx/rulings.jsonl"
expect_stop "an exclusion with an empty label: STOP, an empty label would match nothing or everything" "label"
fixture; LABELS="bug" view OPEN false CLEAN > "$fx/view.json"; echo '{"kind":"exclusion","repo":42,"label":"x"}' > "$fx/rulings.jsonl"
expect_stop "an exclusion whose repo is not a string: STOP" "repo"
fixture; LABELS="bug" view OPEN false CLEAN > "$fx/view.json"; rm -f "$fx/rulings.jsonl"; mkdir "$fx/rulings.jsonl"
expect_stop "a rulings path that is a directory: STOP" "rulings"
if [[ "$(id -u)" != 0 ]]; then
  fixture; LABELS="bug" view OPEN false CLEAN > "$fx/view.json"; excl org/repo x > "$fx/rulings.jsonl"; chmod 000 "$fx/rulings.jsonl"
  expect_stop "a rulings file that exists and cannot be read: STOP" "rulings"
  chmod 644 "$fx/rulings.jsonl"
else
  echo "SKIP an unreadable rulings file (running as root)"
fi

# K9: the four kinds are ruling, grant, exclusion and term. A line with no kind,
# a kind that is not a string, or a kind outside the four STOPs, since it might be
# a record the guard should have read. A term line is ignored like ruling and grant.
fixture; LABELS="bug" view OPEN false CLEAN > "$fx/view.json"; printf '{"kind":"term","role":"builder","subject":"s","text":"t","set_by":"supervisor"}\n' > "$fx/rulings.jsonl"
expect_proceed "a term record is read and ignored: PROCEED"
fixture; LABELS="bug" view OPEN false CLEAN > "$fx/view.json"; echo '{"repo":"org/repo","label":"acu.update"}' > "$fx/rulings.jsonl"
expect_stop "a rulings line with no kind: STOP" "kind"
fixture; LABELS="bug" view OPEN false CLEAN > "$fx/view.json"; echo '{"kind":"exclusions","repo":"org/repo","label":"acu.update"}' > "$fx/rulings.jsonl"
expect_stop "a rulings line with a kind outside the four: STOP" "kind"
fixture; LABELS="bug" view OPEN false CLEAN > "$fx/view.json"; echo '{"kind":42}' > "$fx/rulings.jsonl"
expect_stop "a rulings line whose kind is not a string: STOP" "kind"
fixture; LABELS="bug" view OPEN false CLEAN > "$fx/view.json"; echo '{"kind":null,"repo":"org/repo","label":"acu.update"}' > "$fx/rulings.jsonl"
expect_stop "a rulings line whose kind is null: STOP" "kind"

# --- the base must be the default branch ---------------------------------------------
# Two stacked PRs were once squash-merged into their parents' branches because their bases were never
# retargeted. The guard reads the default branch with the repo named, and stops unless the PR's base is it,
# or the caller names that exact base with --allow-base.
fixture; BASE=feat/parent view OPEN false CLEAN > "$fx/view.json"
expect_stop "a PR based on a branch that is not the default: STOP, naming both" "base is feat/parent, not the default branch main"
fixture; BASE=feat/parent view OPEN false CLEAN > "$fx/view.json"
expect_stop "the refusal says how to allow it" "--allow-base feat/parent"
fixture; view OPEN false CLEAN > "$fx/view.json"
expect_proceed "a PR based on the default branch: PROCEED"
grep -q "based on main" "$fx/out" && ok "the PROCEED line names the base" || bad "PROCEED names the base" "$(cat "$fx/out")"
fixture; BASE=feat/parent view OPEN false CLEAN > "$fx/view.json"; FLAGS="--allow-base feat/parent" expect_proceed "--allow-base with the matching name: PROCEED"
fixture; BASE=feat/parent view OPEN false CLEAN > "$fx/view.json"; FLAGS="--allow-base feat/other" expect_stop "--allow-base with a different name: still STOP" "base is feat/parent, not the default branch main"
fixture; BASE=feat/parent view OPEN false CLEAN > "$fx/view.json"; FLAGS="--allow-base main" expect_stop "--allow-base naming the default does not allow another base" "base is feat/parent"
fixture; BASE=feat/parent view OPEN false BLOCKED > "$fx/view.json"; FLAGS="--allow-base feat/parent" expect_stop "an allowed base does not waive the other checks" "BLOCKED"
fixture; view OPEN false CLEAN > "$fx/view.json"; echo 'no such repo' > "$fx/repo.err"; echo 1 > "$fx/repo.rc"
expect_stop "the default branch cannot be read: STOP" "default branch"
fixture; view OPEN false CLEAN > "$fx/view.json"; echo '{"defaultBranchRef":null}' > "$fx/repo.json"
expect_stop "a repo view with no default branch: STOP" "default branch"
fixture; view OPEN false CLEAN > "$fx/view.json"; : > "$fx/repo.json"
expect_stop "an empty repo read: STOP" "default branch"
fixture; echo '{"state":"OPEN","isDraft":false,"mergeStateStatus":"CLEAN","headRefName":"feat/x","headRefOid":"abc1234def5678","statusCheckRollup":[],"labels":[]}' > "$fx/view.json"
expect_stop "a view with no baseRefName: STOP" "base branch"
fixture; view OPEN false CLEAN > "$fx/view.json"
guard ${FLAGS-} org/repo 7 || true; [[ "$(cat "$fx/repo.calls")" == 1 ]] && ok "the default branch is read once, with the repo named" || bad "repo read count" "$(cat "$fx/repo.calls" 2>/dev/null)"
# The base check comes before the merge-state retries: a PR on the wrong base that is also UNKNOWN stops on the
# base at once, with one view read and no re-reads (a guard that retried first would spend the retries, and
# report the merge state, for a PR it must refuse anyway).
fixture; BASE=feat/parent view OPEN false UNKNOWN > "$fx/view.json"
expect_stop "a wrong base with merge state UNKNOWN: STOP naming the base, not the merge state" "base is feat/parent, not the default branch main"
[[ "$(cat "$fx/view.calls")" == 1 ]] && ok "the base is decided on the first read, with no UNKNOWN re-reads" \
  || bad "base before the retries" "view.calls=$(cat "$fx/view.calls")"
# The default branch is asked of the repo the PR is in, by name, never of whatever the working directory is.
fixture; view OPEN false CLEAN > "$fx/view.json"; guard org/repo 7 || true
grep -qx 'repo view org/repo --json defaultBranchRef' "$fx/repo.args" && ok "the default branch is asked of the named repo: gh repo view org/repo --json defaultBranchRef" \
  || bad "repo named on the default-branch read" "$(cat "$fx/repo.args" 2>/dev/null)"
fixture; rc=0; guard --allow-base >/dev/null 2>&1 || rc=$?; [[ $rc -eq 2 ]] && ok "--allow-base with no value is a usage error" || bad "allow-base no value" "rc=$rc"
fixture; rc=0; guard --allow-base a --allow-base b org/repo 7 >/dev/null 2>&1 || rc=$?; [[ $rc -eq 2 ]] && ok "--allow-base twice is a usage error" || bad "allow-base twice" "rc=$rc"

# --- seat decisions (seat-decide-window, D2) ---------------------------------------
# A row that links the PR and waits on a decision that is not confirmed stops the merge, before gh is asked anything.
DWS_BIN="$here/dws"
dwsrun() { DIRECTOR_STATE="$fx/state" DWS_TEST_CLOCK=1 DWS_NO_RERENDER=1 DWS_NOW=2026-10-20T12:00:00Z PYTHONDONTWRITEBYTECODE=1 "$DWS_BIN" "$@" >/dev/null 2>"$fx/dws.err" || { bad "dws $*" "$(cat "$fx/dws.err")"; }; }
decision_block() { printf 'seat-decision@1\nid: team-a/builder-20261020-1\nseat: team-a/builder\nquestion: Which way?\noptions: a) one; b) two\nchosen: b) two because it is cheaper\ntest: 1 ok 2 ok 3 ok 4 ok 5 ok 6 ok 7 ok 8 ok\nestimate: 1\nundo: revert it\nchanges: pr:org/repo#7\n' > "$fx/blk"; }
waiting_pr() { # a row that links org/repo#7 and waits on the decision
  decision_block; dwsrun open w1 --title "Row" --owner team-a/builder
  dwsrun decision add --from "$fx/blk"; dwsrun link w1 pr:org/repo#7; dwsrun set w1 --waits decision:team-a/builder-20261020-1
}
fixture; view OPEN false CLEAN > "$fx/view.json"; waiting_pr
expect_stop "a PR that waits on an unconfirmed decision: STOP" "waits on seat decision team-a/builder-20261020-1, not confirmed (seat-decide-window)"
[[ ! -e "$fx/view.calls" ]] && ok "the decision read comes before any gh call" || bad "the decision read comes before any gh call" "view.calls=$(cat "$fx/view.calls")"
fixture; view OPEN false CLEAN > "$fx/view.json"
OK8="1 ok 2 ok 3 ok 4 ok 5 ok 6 ok 7 ok 8 ok"
dwsrun open p1 --title "Path row" --owner team-a/builder; dwsrun open p2 --title "Other path" --owner team-a/builder
printf '{"read_at":"2026-10-20T11:00:00Z","open":["card-1"]}' > "$fx/state/desk-snapshot.json"
dwsrun link p1 pr:org/repo#7
dwsrun decision paths team-a/architect-20261020-5 --card card-1 --evidence e --why-waiting w --row-a p1 --estimate-a 1 --test-a "$OK8" --row-b p2 --estimate-b 1 --test-b "$OK8"
expect_stop "a PR on a paths-open row: STOP" "waits on seat decision team-a/architect-20261020-5, not confirmed"
fixture; view OPEN false CLEAN > "$fx/view.json"; waiting_pr
dwsrun decision override team-a/builder-20261020-1 --words no --ref relay:R-1 --instead other
expect_stop "a PR that waits on an overridden decision: STOP" "not confirmed"
fixture; view OPEN false CLEAN > "$fx/view.json"; waiting_pr
dwsrun decision confirm team-a/builder-20261020-1 --words yes --ref relay:R-2
expect_proceed "once the decision is confirmed the PR proceeds"
fixture; view OPEN false CLEAN > "$fx/view.json"; decision_block; dwsrun open w1 --title Row --owner team-a/builder; dwsrun decision add --from "$fx/blk"; dwsrun link w1 pr:org/repo#9; dwsrun set w1 --waits decision:team-a/builder-20261020-1
expect_proceed "a PR the ledger does not link is not stopped by this read"
fixture; view OPEN false CLEAN > "$fx/view.json"; rm -f "$fx/state/workstreams.jsonl"
expect_stop "a missing ledger STOPs" "no workstream ledger"
fixture; view OPEN false CLEAN > "$fx/view.json"; chmod 000 "$fx/state/workstreams.jsonl"
if [[ -r "$fx/state/workstreams.jsonl" ]]; then ok "(skipped: the ledger stays readable to this user)"; else expect_stop "an unreadable ledger STOPs" "ledger read failed"; fi
chmod 600 "$fx/state/workstreams.jsonl" 2>/dev/null || true
fixture; view OPEN false CLEAN > "$fx/view.json"
MERGE_GUARD_DWS="$root/no-such-dws" expect_stop "a dws that cannot be run STOPs" "dws not found"

# --- usage --------------------------------------------------------------------------
fixture
if guard only-one-arg; then bad "usage error proceeded"
else rc=$?; [[ $rc -eq 2 ]] && ok "a usage error exits 2" || bad "usage rc" "rc=$rc"; fi
# The usage text is the header's verdict list: it must show every verdict, whole.
grep -q "usage error" "$fx/out" && grep -q "PROCEED-KEEP-BRANCH" "$fx/out" && grep -q "STOP <repo>" "$fx/out" \
  && grep -q "line never takes it for leave to delete" "$fx/out" \
  && ok "the usage text shows PROCEED, PROCEED-KEEP-BRANCH, STOP and usage error whole" \
  || bad "usage text" "$(cat "$fx/out")"

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
