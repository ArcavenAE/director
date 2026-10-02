# finding-018: a shim built in a git worktree stamps the revision of whatever repository encloses it

- **Date:** 2026-10-02
- **Session:** the arcaven builder seat, placing the 2026-10-02 team harvest
- **Subject:** the build stamp that presence `rev` reports (LR-3)
- **Confidence:** reproduced once and explained from the Go toolchain's own source (go1.26.5)

## 0. The sentence

**Go treats `.git` as a VCS root only when it is a directory, and a linked
git worktree's `.git` is a file, so a director-mcp built in a worktree takes
its `vcs.revision` from the nearest enclosing repository, and presence `rev`
then reports that revision as the shim's.**

## 1. What was observed

The shim was built for C-0 from a worktree of this repo checked out at main
d3e736f, inside the orchestrator's working tree. `go version -m` on the
binary showed `vcs.revision=42e1c00`, the orchestrator's HEAD, with
`vcs.modified=true`. The same commit built from a plain clone stamped
`d3e736f` with `vcs.modified=false`, and that binary was the one installed.

## 2. Mechanism

`src/cmd/go/internal/vcs/vcs.go` declares Git's root as
`{filename: ".git", isDir: true}`. Walking up from the module, the build
skips the worktree's `.git` file and stops at the first `.git` directory
above it. Under the orchestrator layout, where worktrees of subrepos sit
inside the orchestrator's tree, that directory is the orchestrator's. Outside
any repository the stamp would be absent and `rev` would read `"unknown"`,
which is at least honest. Inside another repository it is a confident wrong
answer, and `+dirty` follows that repository's state, not the shim's.

## 3. Why it matters

LR-3 put `rev` in presence so a stale seat could be told from a current one
(docs/shim-reference.md). A worktree-built shim defeats that: the roster
shows a real-looking revision that belongs to a different repository.
finding-006 was resolved by reading this stamp, and question-director-seat-
startup names stale builds as a rough spot. Both depend on the stamp being
the shim's own.

## 4. What this does not establish

Whether any running seat carries a worktree-built shim today. Remedies,
none chosen: build from a plain clone (what was done here); pass
`-ldflags` with the revision `git rev-parse` gives in the worktree; or have
the shim refuse to report a `rev` whose repository is not this one.

## Related

- finding-006 (resolved by a clean rebuild, read from this stamp);
  finding-017 (the launcher's revision is not visible either);
  director#126 (unread age and shim revision in presence).
