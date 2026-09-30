#!/usr/bin/env bash
# Prove merge-guard fails closed (R-152): a read that errors, times out, comes
# back empty or unparseable, or reports UNKNOWN stops the merge run; only a
# CLEAN, open, non-draft PR with passing checks, no stacked child, and a
# non-author approval at the current head, given after that head reached
# GitHub, and no standing change request on any commit, proceeds.
# gh is a stub that replays fixtures, so nothing reaches GitHub.
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
# with suites.rc. Each call is counted, so a retry is observable.
cat > "$root/bin/gh" <<'STUB'
#!/usr/bin/env bash
fx="$GH_FIXTURE"
case "$1 $2" in
  "pr view") verb=view ;;
  "pr list") verb=list ;;
  api\ *) verb=suites ;;
  *) echo "gh stub: unexpected call: $*" >&2; exit 97 ;;
esac
n=$(( $(cat "$fx/$verb.calls" 2>/dev/null || echo 0) + 1 ))
echo "$n" > "$fx/$verb.calls"
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
view() { # state draft merge-state [rollup-json]
  printf '{"state":"%s","isDraft":%s,"mergeStateStatus":"%s","headRefName":"feat/x","headRefOid":"abc1234def5678","statusCheckRollup":%s,"author":{"login":"builder"},"commits":[{"oid":"0000000old","committedDate":"2026-09-27T09:00:00Z"},{"oid":"abc1234def5678","committedDate":"%s"}],"reviews":[%s]}\n' \
    "$1" "$2" "$3" "${4:-[]}" "$HEAD_AT" "${REVIEWS-$GOOD_REVIEW}"
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
}
guard() {
  env PATH="$root/bin:/usr/bin:/bin" GH_FIXTURE="$fx" \
      MERGE_GUARD_RETRIES="${RETRIES:-2}" MERGE_GUARD_DELAY=0 \
      "$GUARD" "$@" >"$fx/out" 2>&1
}
expect_proceed() { # name
  if guard org/repo 7; then
    grep -q "^PROCEED org/repo#7 @abc1234def5678" "$fx/out" && ok "$1" || bad "$1" "$(cat "$fx/out")"
  else
    bad "$1" "stopped: $(cat "$fx/out")"
  fi
}
expect_stop() { # name, text the STOP line must carry
  if guard org/repo 7; then
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
fixture; echo '{"state":"OPEN","mergeStateStatus":"CLEAN","headRefName":"feat/x","headRefOid":"abc1234def5678","statusCheckRollup":[]}' > "$fx/view.json"
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
expect_stop "an open PR based on this one's head: STOP naming it" "#9"

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
fixture; echo '{"state":"OPEN","isDraft":false,"mergeStateStatus":"CLEAN","headRefName":"feat/x","headRefOid":"abc1234def5678","statusCheckRollup":[],"author":{"login":"builder"},"commits":[],"reviews":[]}' > "$fx/view.json"
expect_stop "the head commit is not in the view's commit list: STOP" "head commit"
fixture; echo '{"state":"OPEN","isDraft":false,"mergeStateStatus":"CLEAN","headRefName":"feat/x","headRefOid":"abc1234def5678","statusCheckRollup":[],"author":{"login":"builder"},"commits":[{"oid":"abc1234def5678","committedDate":"2026-09-28T10:00:00Z"}]}' > "$fx/view.json"
expect_stop "a view with no reviews field: STOP" "reviews"

# --- usage --------------------------------------------------------------------------
fixture
if guard only-one-arg; then bad "usage error proceeded"
else rc=$?; [[ $rc -eq 2 ]] && ok "a usage error exits 2" || bad "usage rc" "rc=$rc"; fi

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
