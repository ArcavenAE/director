#!/usr/bin/env bash
# Prove draft-sweep prints the right board lines, skips what the brief says to
# skip, turns every unreadable input into a counted GAP instead of an empty
# board, exits 0, and never calls a gh verb that changes anything.
# Network-free: gh is a stub that serves canned JSON, logs every call, and
# misbehaves on request (STUB_MODE).
#
# Usage: scripts/draft-sweep/verify-draft-sweep.sh     Exit nonzero on any miss.
# DRAFT_SWEEP_BIN points the suite at another build of the sweep, which is how
# the negative control runs it against an older version.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SWEEP="${DRAFT_SWEEP_BIN:-$here/draft-sweep}"
[[ -x "$SWEEP" ]] || {
  echo "draft-sweep not executable at $SWEEP"
  exit 2
}

pass=0
fail=0
ok() {
  echo "PASS $1"
  pass=$((pass + 1))
}
bad() {
  echo "FAIL $1${2:+ -- $2}"
  fail=$((fail + 1))
}

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
mkdir -p "$root/bin"

# 2026-09-28T12:00:00Z. Draft ages below are measured from it.
NOW=1790596800

# The stub. Author "bot" always fails its searches. STUB_MODE:
#   normal      canned results; searches also write noise to stderr, which
#               must not reach the payload
#   badsearch   the draft search exits 0 with a body that is not JSON
#   badview     pr view of repo-a#1 exits 0 with a body that is not JSON
#   noreviews   pr view of repo-a#1 is JSON but has no reviews field
#   cap         run with DRAFT_SWEEP_LIMIT equal to the draft result count
cat >"$root/bin/gh" <<STUB
#!/usr/bin/env bash
echo "\$*" >>"$root/calls.log"
args="\$*"
mode="\${STUB_MODE:-normal}"
case "\$args" in
*"search prs"*"--author bot"*)
  echo "HTTP 502: search unavailable" >&2
  exit 1 ;;
*"search prs"*"--draft"*)
  echo "warning: a notice on stderr" >&2
  if [[ "\$mode" == badsearch ]]; then echo "<html>rate limited</html>"; exit 0; fi
  cat <<'JSON'
[
 {"repository":{"nameWithOwner":"org/repo-a"},"number":1,"createdAt":"2026-09-26T00:00:00Z","url":"u/1"},
 {"repository":{"nameWithOwner":"org/repo-a"},"number":2,"createdAt":"2026-09-28T06:00:00Z","url":"u/2"},
 {"repository":{"nameWithOwner":"org/repo-out"},"number":3,"createdAt":"2026-02-21T00:00:00Z","url":"u/3"},
 {"repository":{"nameWithOwner":"org/repo-b"},"number":4,"createdAt":"2026-09-26T00:00:00Z","url":"u/4"},
 {"repository":{"nameWithOwner":"org/repo-b"},"number":5,"createdAt":"2026-09-26T00:00:00Z","url":"u/5"},
 {"repository":{"nameWithOwner":"org/repo-a"},"number":6,"createdAt":"2026-09-26T00:00:00Z","url":"u/6"}
]
JSON
  ;;
*"search prs"*"--merged-at"*)
  echo "warning: a notice on stderr" >&2
  cat <<'JSON'
[
 {"repository":{"nameWithOwner":"org/repo-a"},"number":10,"url":"u/10"},
 {"repository":{"nameWithOwner":"org/repo-a"},"number":11,"url":"u/11"},
 {"repository":{"nameWithOwner":"org/repo-out"},"number":12,"url":"u/12"}
]
JSON
  ;;
"pr view 1 "*)
  case "\$mode" in
  badview) echo "Something went wrong" ;;
  noreviews) echo '{"baseRefName":"main"}' ;;
  *) echo '{"reviews":[],"baseRefName":"main"}' ;;
  esac ;;
"pr view 3 "*) echo '{"reviews":[],"baseRefName":"main"}' ;;
"pr view 4 "*) echo '{"reviews":[{"state":"COMMENTED"}],"baseRefName":"main"}' ;;
"pr view 5 "*) echo '{"reviews":[],"baseRefName":"feat/base"}' ;;
"pr view 6 "*) echo '{"reviews":[{"state":"CHANGES_REQUESTED"}],"baseRefName":"main"}' ;;
"pr view 10 "*) echo '{"reviews":[],"mergedAt":"2026-09-27T01:00:00Z"}' ;;
"pr view 11 "*) echo '{"reviews":[{"state":"APPROVED"}],"mergedAt":"2026-09-27T02:00:00Z"}' ;;
"pr view 12 "*) echo '{"reviews":[{"state":"APPROVED"}],"mergedAt":"2026-09-27T03:00:00Z"}' ;;
"pr view "*) echo "unexpected pr view: \$args" >&2; exit 1 ;;
"pr list"*"--head feat/base"*) echo '[{"isDraft":true}]' ;;
"pr list"*) echo '[]' ;;
*) echo "unexpected gh call: \$args" >&2; exit 1 ;;
esac
STUB
chmod +x "$root/bin/gh"

# run MODE [VAR=value ...]: run the sweep under the stub; sets $out and $rc.
out=""
rc=0
run() {
  local mode="$1"
  shift
  set +e
  out="$(env PATH="$root/bin:$PATH" STUB_MODE="$mode" DRAFT_SWEEP_NOW="$NOW" \
    DRAFT_SWEEP_CONFIG="$root/none.conf" DRAFT_SWEEP_OWNER=org \
    DRAFT_SWEEP_REPOS="repo-a repo-b" DRAFT_SWEEP_AUTHORS="dev bot" "$@" "$SWEEP" 2>"$root/stderr")"
  rc=$?
  set -e
}
has() { grep -qF -- "$1" <<<"$out"; }
# expect_line NAME TEXT: TEXT must appear. expect_none NAME TEXT: it must not.
expect_line() { if has "$2"; then ok "$1"; else bad "$1" "$(head -n 4 <<<"$out")"; fi; }
expect_none() { if has "$2"; then bad "$1"; else ok "$1"; fi; }
expect_rc0() { if [[ "$rc" == 0 ]]; then ok "$1: exits 0"; else bad "$1: exits 0" "rc=$rc"; fi; }

# --- normal: every line type and every skip rule -------------------------------
run normal
expect_rc0 normal
expect_line "stale unreviewed in-scope draft is listed" \
  "- DRAFT org/repo-a#1 by dev, 60h old, no verdict review: u/1"
expect_none "draft under the age floor is skipped" "repo-a#2"
expect_none "out-of-scope repo is skipped" "repo-out#3"
expect_line "draft with only a comment review is still listed" \
  "- DRAFT org/repo-b#4 by dev, 60h old, no verdict review: u/4"
expect_none "draft with a CHANGES_REQUESTED review is skipped" "repo-a#6"
expect_none "draft stacked on an open draft is skipped" "repo-b#5"
expect_line "zero-review merge is listed" \
  "- MERGED-NO-REVIEW org/repo-a#10 by dev, merged 2026-09-27T01:00:00Z: u/10"
expect_none "reviewed merge is skipped" "repo-a#11"
expect_none "out-of-scope merge is skipped" "repo-out#12"
if [[ "$(grep -c -- '- GAP draft-sweep: .* author bot failed: HTTP 502' <<<"$out")" == 2 ]]; then
  ok "both failed searches print a GAP line"
else
  bad "both failed searches print a GAP line" "$out"
fi
expect_line "stderr noise on a good search does not break it; counts match" \
  "2 stale draft(s) over 24h, 1 merged with no review since 2026-09-26, 2 gap(s)"

# Read-only: nothing but search, pr view and pr list was ever called.
if grep -vE '^(search prs|pr view|pr list) ' "$root/calls.log" >"$root/other"; then
  bad "only read verbs are called" "$(cat "$root/other")"
else
  ok "only read verbs are called"
fi

# --- a payload that is not JSON, or lacks a field, is a counted GAP --------------
run badsearch
expect_rc0 badsearch
expect_line "search exiting 0 with a non-JSON body is a GAP" \
  "- GAP draft-sweep: draft search for author dev returned a payload that is not JSON: <html>rate limited</html>"
expect_line "that GAP is counted" "0 stale draft(s) over 24h, 1 merged with no review since 2026-09-26, 3 gap(s)"

run badview
expect_rc0 badview
expect_line "pr view exiting 0 with a non-JSON body is a GAP" \
  "- GAP draft-sweep: read org/repo-a#1 returned a payload that is not JSON: Something went wrong"
expect_none "the unreadable draft is not listed as reviewed or unreviewed" "- DRAFT org/repo-a#1"
expect_line "that GAP is counted" "1 stale draft(s) over 24h, 1 merged with no review since 2026-09-26, 3 gap(s)"

run noreviews
expect_rc0 noreviews
expect_line "a payload missing its reviews field is a GAP" \
  "- GAP draft-sweep: read org/repo-a#1: unexpected payload shape:"

# --- settings: the result cap, and missing owner or scope -----------------------
run cap DRAFT_SWEEP_LIMIT=6
expect_line "a search that fills its result cap is a GAP" \
  "- GAP draft-sweep: draft search for author dev hit the 6 result cap; results may be missing"

run normal DRAFT_SWEEP_OWNER=
expect_rc0 "no owner"
expect_line "no owner set is a GAP, nothing swept" "- GAP draft-sweep: no owner set"
expect_none "no owner set runs no search" "- DRAFT"

run normal DRAFT_SWEEP_REPOS=
expect_rc0 "no scope"
expect_line "no repo scope set is a GAP, nothing swept" "- GAP draft-sweep: no repo scope set"

# --- the config file supplies what the environment does not ----------------------
printf 'owner=org\nrepos=all\n# comment\n' >"$root/sweep.conf"
run normal DRAFT_SWEEP_OWNER= DRAFT_SWEEP_REPOS= DRAFT_SWEEP_CONFIG="$root/sweep.conf"
expect_line "config file supplies owner and scope; repos=all widens it" "- DRAFT org/repo-out#3"

echo "$pass passed, $fail failed"
((fail == 0))
