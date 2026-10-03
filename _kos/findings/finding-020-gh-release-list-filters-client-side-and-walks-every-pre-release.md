# finding-020: `gh release list --exclude-pre-releases` filters on the client and walks every release, so a repo with only pre-releases costs minutes; `releases/latest` is one request

- **Date:** 2026-10-03
- **Session:** the arcaven builder seat, placing the 2026-10-03 team harvest. The timing is the reviewer's (director#199 review 5396018550); the fix and its stub case are mine (#202).
- **Subject:** the release hint in `dws refresh` (director#186 W2, `release_containing` in `skills/director/scripts/dws`)
- **Confidence:** measured once, read-only, against a real repository; the replacement call was exercised live (about 2 s) and by a stub for the 404 case

## 0. The sentence

**`gh release list --exclude-drafts --exclude-pre-releases --limit 1` pages through releases until one passes the filter, so on a repo with no stable release it reads all of them: 148 s against ArcavenAE/marvel (about four pages of alpha releases, no stable one), against 1 s unfiltered. `gh api repos/{repo}/releases/latest --jq .tag_name` is one request, GitHub defines it as the most recent non-draft, non-prerelease release, and it answers 404 when there is none.**

## 1. Why it mattered

refresh runs the release check for every merged PR link outside its skip window, and merged PRs are polled indefinitely. With a handful of merged marvel PRs on the board, one refresh would have taken many minutes.

## 2. Ruled out, and what replaced it

- **Ruled out:** the filtered `gh release list` form as the way to find the latest stable release. Both forms return the right answer (`[]` against the newest alpha tag); only the filtered one is slow on a repo whose releases are all pre-releases.
- **Replacement:** `gh api repos/{repo}/releases/latest --jq .tag_name`. A 404 maps to "no release" (None), not to the release-check-failed hint; any other error still raises to that hint. A stub case pins the 404 path (director#202, commits d67136c and 7506e7b).

## 3. Edges

- informs: the W2 design, `sim/design/board-workstream-ledger.md` section 5 (the release hint is a hint only; refresh never moves a row to released)
