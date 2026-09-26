#!/usr/bin/env bash
# Prove draft-sweep prints the right board lines, skips what the brief says to
# skip, reports a failed search as a GAP instead of an empty board, exits 0,
# and never calls a gh verb that changes anything. Network-free: gh is a stub
# that serves canned JSON and logs every call.
#
# Usage: scripts/draft-sweep/verify-draft-sweep.sh     Exit nonzero on any miss.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SWEEP="$here/draft-sweep"
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

# NOW is 2026-09-28T12:00:00Z. Draft ages below are measured from it.
NOW=1790596800

# The stub: author arcaven gets canned results; author arcavenai's searches
# fail, which the sweep must report as GAP lines.
cat >"$root/bin/gh" <<STUB
#!/usr/bin/env bash
echo "\$*" >>"$root/calls.log"
args="\$*"
case "\$args" in
*"search prs"*"--author arcavenai"*)
	echo "HTTP 502: search unavailable" >&2
	exit 1 ;;
*"search prs"*"--draft"*)
	cat <<'JSON'
[
 {"repository":{"nameWithOwner":"1898andCo/Vantage"},"number":1,"createdAt":"2026-09-26T00:00:00Z","url":"u/1"},
 {"repository":{"nameWithOwner":"1898andCo/Vantage"},"number":2,"createdAt":"2026-09-28T06:00:00Z","url":"u/2"},
 {"repository":{"nameWithOwner":"1898andCo/poller-bear"},"number":3,"createdAt":"2026-02-21T00:00:00Z","url":"u/3"},
 {"repository":{"nameWithOwner":"1898andCo/i-orc"},"number":4,"createdAt":"2026-09-26T00:00:00Z","url":"u/4"},
 {"repository":{"nameWithOwner":"1898andCo/i-orc"},"number":5,"createdAt":"2026-09-26T00:00:00Z","url":"u/5"},
 {"repository":{"nameWithOwner":"1898andCo/Vantage"},"number":6,"createdAt":"2026-09-26T00:00:00Z","url":"u/6"}
]
JSON
	;;
*"search prs"*"--merged-at"*)
	cat <<'JSON'
[
 {"repository":{"nameWithOwner":"1898andCo/Vantage"},"number":10,"url":"u/10"},
 {"repository":{"nameWithOwner":"1898andCo/Vantage"},"number":11,"url":"u/11"},
 {"repository":{"nameWithOwner":"1898andCo/axiathon"},"number":12,"url":"u/12"}
]
JSON
	;;
"pr view 1 "*) echo '{"reviews":[],"baseRefName":"main"}' ;;
"pr view 4 "*) echo '{"reviews":[{"state":"COMMENTED"}],"baseRefName":"main"}' ;;
"pr view 6 "*) echo '{"reviews":[{"state":"CHANGES_REQUESTED"}],"baseRefName":"main"}' ;;
"pr view 5 "*) echo '{"reviews":[],"baseRefName":"feat/base"}' ;;
"pr view 10 "*) echo '{"reviews":[],"mergedAt":"2026-09-27T01:00:00Z"}' ;;
"pr view 11 "*) echo '{"reviews":[{"state":"APPROVED"}],"mergedAt":"2026-09-27T02:00:00Z"}' ;;
"pr view "*) echo "unexpected pr view: \$args" >&2; exit 1 ;;
"pr list"*"--head feat/base"*) echo '[{"isDraft":true}]' ;;
"pr list"*) echo '[]' ;;
*) echo "unexpected gh call: \$args" >&2; exit 1 ;;
esac
STUB
chmod +x "$root/bin/gh"

set +e
out="$(PATH="$root/bin:$PATH" DRAFT_SWEEP_NOW="$NOW" "$SWEEP" 2>"$root/stderr")"
rc=$?
set -e

has() { grep -qF -- "$1" <<<"$out"; }
# expect_line NAME TEXT: TEXT must appear. expect_none NAME TEXT: it must not.
expect_line() { if has "$2"; then ok "$1"; else bad "$1" "$(head -n 3 <<<"$out")"; fi; }
expect_none() { if has "$2"; then bad "$1"; else ok "$1"; fi; }

if [[ "$rc" == 0 ]]; then ok "exits 0"; else bad "exits 0" "rc=$rc"; fi
expect_line "stale unreviewed in-scope draft is listed" \
  "- DRAFT 1898andCo/Vantage#1 by arcaven, 60h old, no verdict review: u/1"
expect_none "draft under the age floor is skipped" "Vantage#2"
expect_none "out-of-scope repo is skipped" "poller-bear#3"
expect_line "draft with only a comment review is still listed" \
  "- DRAFT 1898andCo/i-orc#4 by arcaven, 60h old, no verdict review: u/4"
expect_none "draft with a CHANGES_REQUESTED review is skipped" "Vantage#6"
expect_none "draft stacked on an open draft is skipped" "i-orc#5"
expect_line "zero-review merge is listed" \
  "- MERGED-NO-REVIEW 1898andCo/Vantage#10 by arcaven, merged 2026-09-27T01:00:00Z: u/10"
expect_none "reviewed merge is skipped" "Vantage#11"
expect_none "out-of-scope merge is skipped" "axiathon#12"
if [[ "$(grep -c -- '- GAP draft-sweep: .* author arcavenai failed: HTTP 502' <<<"$out")" == 2 ]]; then
  ok "both failed arcavenai searches print a GAP line"
else
  bad "both failed arcavenai searches print a GAP line" "$out"
fi
expect_line "summary line counts match" \
  "2 stale draft(s) over 24h, 1 merged with no review since 2026-09-26, 2 gap(s)"

# Read-only: nothing but search, pr view and pr list was ever called.
if grep -vE '^(search prs|pr view|pr list) ' "$root/calls.log" >"$root/other"; then
  bad "only read verbs are called" "$(cat "$root/other")"
else
  ok "only read verbs are called"
fi

# Scope override: DRAFT_SWEEP_REPOS=all lets the out-of-scope draft through.
out_all="$(PATH="$root/bin:$PATH" DRAFT_SWEEP_NOW="$NOW" DRAFT_SWEEP_REPOS=all "$SWEEP" 2>/dev/null || true)"
if grep -qF "poller-bear#3" <<<"$out_all"; then
  ok "DRAFT_SWEEP_REPOS=all widens the scope"
else
  bad "DRAFT_SWEEP_REPOS=all widens the scope"
fi

echo "$pass passed, $fail failed"
((fail == 0))
