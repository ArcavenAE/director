# wirerust#407 revision — SENT

Pushed as `3643b0a9` on 2026-09-08 and replied on the PR
(`Zious11/wirerust#407`, comment 5592979100). 14 of 14 checks pass,
including the Signing Workflow Injection Guard, which now covers
`sync-upstream.yml` for the first time.

Kept for the record of what the item was and how it was closed.

## What shipped

Blocking items 1-4 from the 2026-09-06 review, the duplication item 5,
and the four minors.

1. `depends_on :macos` on all five formulae, plus removal of the stable
   formula's redundant `version` line (the addition below).
2. Unknown-channel tags no longer advertise a `brew install` line; the
   default case arm leaves `FORMULA` empty and the title falls back to
   the bare project name.
3. `sync-upstream.yml` env-binds every context in its `run:` bodies, and
   `check-signing-workflow-injection.sh` self-discovers workflows meeting
   its structural criteria rather than reading a two-file list.
   `sign-and-publish.yml` and `backfill-release.yml` stay required, so
   their zero-in-scope sentinel is unchanged.
4. All 7 dead spec references repointed to a commit-pinned permalink.
5. `scripts/sign-and-notarize.sh` and `scripts/update-homebrew-formula.sh`.
   Workflows lose 437 lines and gain 107; the scripts add 266.

## The addition I folded in

The stable formula template declared `version "VERSION_PLACEHOLDER"`
alongside a `.../download/TAG_PLACEHOLDER/...` URL. Stable tags are
`v<version>`, so brew scans the same value and `brew audit --strict`
rejects the duplicate. Same defect fixed downstream in
ArcavenAE/homebrew-tap#5 and in the jira-cli template at
ArcavenAE/jira-cli#7.

## Two decisions worth remembering

**Subcommands, not one entry point.** The signing script takes
`import-certs` / `sign-binaries` / `package` / `notarize` / `verify` /
`checksums` so each phase stays its own workflow step and its secrets
stay scoped to that step. A composite action, which is what the review
suggested, would put the notarization credentials in scope for signing
and the signing identity in scope for notarization. Verified the
refactor changed nothing: identical step sequence, and per-step `env:`
keys identical apart from one added `COMMIT_SHA`.

**`egress-policy: block` declined with a reason, not skipped.** Block
mode grades against a per-repo allowlist and these jobs have only run in
audit mode, so there is no detections history to derive one from.
Offered as a follow-up once runs produce the endpoint list.
