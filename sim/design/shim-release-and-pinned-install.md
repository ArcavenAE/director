# director-mcp: a reproducible release and a pinned install

Status: design for review, 2026-10-02. Owner: the architect role. Tracks
director#68. Builds on `sim/design/launcher-distribution.md` (approved
2026-09-27), which already installs the launcher from a commit. Design only;
builders follow review.

## 1. Why

A seat runs whatever director-mcp binary it was started with, and today
nothing says which build that is or whether it matches a commit. During the
cross-host return-path outage (finding-006) the running shim was a dirty,
unpinned build whose wire behavior matched no commit, and other seats ran
different builds from different paths. The channel cue sharpened this: a cue
seat needs a shim that has the cue, and in the C-0 trial seats spawned
without it because the shared checkout had not moved after the merge
(finding-017, "merge is not deploy"). A shim built in a git worktree also
stamps the wrong revision (finding-018), so even the rev in presence can
mislead.

The fix has three parts: a release built and stamped in CI, an install that
pins a host to one release by version, and a version a seat can be asked for
without touching the bus.

## 2. Today (checked at main c0038b1)

- No `.github/workflows/` in this repository: no CI build, no release.
- `director-install` installs one component, `cast-launch`, from a commit
  (bare mirror, `git archive`, verify, atomic symlink, `--rollback`,
  `--status`). Adding director-mcp as a second component is filed
  (aae-orc-x07y8) and not built.
- `director-mcp` accepts no arguments (serve), `--preflight`, or `unread
  [--json]`, and refuses anything else (director#75). There is no
  `--version`; a version probe today means starting a shim, which connects to
  the bus and can collide on identity.
- Presence carries `rev` from the binary's VCS stamp (director#137). A
  worktree build stamps the enclosing repository's revision (aae-orc-ldteb).

**Precedent, kos and marvel:** CI on every push to main builds alpha
binaries with the version stamped by `-ldflags` (`0.1.0-alpha.<date>.<time>.<sha7>`,
tag `alpha-<date>-<time>-<sha7>`), publishes a prerelease, and attests it; a
version tag cuts stable. This design follows that shape for director-mcp.

## 3. Design

### R1. A version that touches nothing

`director-mcp --version` (and `version`) prints one line and exits 0 without
reading config or connecting:

```
director-mcp 0.1.0-alpha.20261002.120501.c0038b1 rev c0038b1 channel alpha features cue,unread
```

`--version --json` prints the same fields as an object. `features` lists the
capabilities a launcher may need to check (section 4). The parser keeps
refusing every other unknown argument.

### R2. A stamped, reproducible build

One build recipe, used by CI and by the installer's from-source path:

```sh
CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -buildid= -X main.version=$VERSION -X main.revision=$SHA -X main.channel=$CHANNEL" \
  -o director-mcp .
```

- `main.revision` comes from the commit being built (`git rev-parse` in CI,
  the archived commit in the installer), never from walking up the
  directory tree. `shimRevision()` prefers `main.revision` when set and
  falls back to the VCS stamp only for an unstamped local build. This closes
  aae-orc-ldteb for every released or installed binary; the worktree case
  remains only for ad hoc local builds, which slice M of #126 marks.
- `-trimpath`, an empty build id, `CGO_ENABLED=0`, and the toolchain pinned
  by `go.mod` make two builds of one commit byte-identical. A test checks it.

### R3. Release, following kos and marvel

A workflow in this repository, on push to main when
`probe/nats-phase-0/director-mcp/` changes:
- builds R2 for darwin/arm64, darwin/amd64, linux/amd64 and linux/arm64;
- publishes a prerelease tagged `director-mcp-alpha-<date>-<time>-<sha7>`
  (prefixed, since the repository may release other things later), with the
  four binaries and a `SHA256SUMS`;
- attests the artifacts (`actions/attest`, as kos and marvel do).

A tag `director-mcp-v<semver>` cuts a stable release the same way. macOS
signing and notarization are left out of slice 1. The installer downloads
with `gh release download` from a shell, not a browser; V4's test checks that
the installed binary carries no quarantine attribute and runs. If it does
not, or a seat host ever needs a browser download, marvel's notarize job is
the model.

### R4. A pinned install, one path per host

`director-install` gains the `director-mcp` component (aae-orc-x07y8), with
the launcher's shape:

```sh
director-install director-mcp --release latest-alpha     # or a tag
director-install director-mcp --ref <sha>                # build from the mirror
director-install director-mcp --rollback
```

- `--release` downloads the asset for this host, checks it against
  `SHA256SUMS`, verifies the attestation (`gh attestation verify`), and runs
  `director-mcp --version` to confirm the stamped revision equals the
  release's commit.
- `--ref` builds R2 from `git archive` of that commit in the bare mirror, so
  a host with no release yet, or an operator testing a commit, gets the same
  stamped binary.
- Either way the binary goes to `~/.director/lib/director-mcp/<sha>/` with a
  `STAMP` (sha, version, channel, source, install time), and
  `~/.director/bin/director-mcp` switches to it atomically. The previous
  directory stays for rollback.
- `director-mcp-seat`, the wrapper seats start, execs
  `${DIRECTOR_MCP:-$HOME/.director/bin/director-mcp}` and never a checkout
  path. `DIRECTOR_MCP` is the deliberate way to test another build.

**One known path per host:** `director-install --status` adds the
director-mcp row (installed version and sha, its age, commits on main under
the shim's directory since then) and a WARN for any MCP config, wrapper or
manifest that names a director-mcp binary outside `~/.director/bin`. It
exits 0 (diagnostic, ADR-007).

**Why not the Homebrew tap,** which kos and marvel use: a tap upgrades every
host to the newest release at `brew upgrade`, while seats need a host pinned
to a chosen version with a rollback, and director's components already live
under `~/.director` with that shape. A tap formula can be added later for
operators outside the fleet; it is not the seat path.

### R5. Skew made visible

- Every seat's presence already carries `rev`; with R2 it is trustworthy for
  installed binaries.
- `director-install --status` prints the installed sha; `director-mcp unread`
  (slice M) shows each live seat's rev, so a seat running a build other than
  the host's installed one stands out. A `--by-rev` grouping is a small
  addition to slice M's report and is listed as a part here.
- Upgrade is per host, at the operator's word: `director-install director-mcp
  --release latest-alpha`. Running seats keep their binary until their next
  restart or shift; the rev column shows which have not picked it up.

## 4. Pairing: launcher revision at cast (aae-orc-wnuzs) and merge-is-not-deploy (finding-017)

Together these decide how a cue seat gets a known shim build:

1. **The launcher is installed from a commit** (launcher-distribution) and
   **the shim from a release or commit** (R4). Neither runs from a working
   tree, so a merge changes nothing until an install, and the install is one
   visible command per host. That is finding-017's gap closed by design: a
   merge produces a release; an install deploys it.
2. **The spawn line names both** (wnuzs): cast-launch reads its own `STAMP`
   and the shim's `director-mcp --version`, and the spawn line carries
   `launcher <sha> shim <version>`. A seat's first act echoes that line, so a
   reader of the spawn log knows which builds each seat started with.
3. **The cue is gated on the shim's features.** With `DIRECTOR_CUE=1`,
   cast-launch checks that `--version` lists `cue`. If it does not, the spawn
   line says `cue: unavailable (shim <version> has no cue)` and the seat
   starts without the channel flag. It is never silently off, which is the C-0
   failure.

## 5. Parts, in order

| part | what | depends on |
|---|---|---|
| V1 | `--version` / `version` and `--version --json`, no config read, no connect; `main.version`, `main.revision`, `main.channel`; `shimRevision()` prefers `main.revision` | none |
| V2 | the R2 build recipe as a script (`scripts/build-director-mcp`), plus the reproducibility test | V1 |
| V3 | release workflow (R3) | V2 |
| V4 | `director-install director-mcp` (`--release`, `--ref`, `--rollback`, `--status` row and WARNs); `director-mcp-seat` execs the installed path (aae-orc-x07y8) | V2 |
| V5 | cast-launch spawn line names launcher and shim; the cue feature gate (aae-orc-wnuzs) | V1, V4 |
| V6 | `unread --by-rev` grouping | director#126 slice M |

## 6. Tests (red first)

1. **V1:** `director-mcp --version` with no config and no broker reachable
   prints the line and exits 0, and opens no network connection (run under a
   sandbox that refuses connects); an unknown argument is still refused;
   `--version --json` parses.
2. **V1 rev:** a binary built with `-X main.revision=abc1234` inside a git
   worktree reports `abc1234` in `--version` and in presence; an unstamped
   build falls back to the VCS stamp.
3. **V2:** two builds of one commit in two clean directories produce
   byte-identical binaries for one target.
4. **V3:** in a dry run of the workflow (act or a fork), the prerelease has
   four binaries and `SHA256SUMS`, and each binary's `--version` names the
   pushed commit.
5. **V4:** `--release` with a tampered asset fails the checksum and does not
   switch the symlink; `--ref` produces the same `--version` line as the
   release of that commit; `--rollback` restores the previous directory;
   `--status` WARNs on an MCP config naming a checkout path, and exits 0;
   on macOS the installed binary has no `com.apple.quarantine` attribute and
   runs.
6. **V5:** with `DIRECTOR_CUE=1` and a shim whose features lack `cue`, the
   spawn line carries `cue: unavailable` and the harness starts without the
   channel flag; with a cue-capable shim, the flag is passed and the line
   names both builds.

## 7. Rulings needed (operator, via director)

| # | question | default |
|---|---|---|
| 1 | Release as GitHub prereleases with checksums and attestation, not the Homebrew tap, for seat hosts (R3, R4) | yes |
| 2 | macOS signing and notarization left out of slice 1 (R3) | yes |
| 3 | Upgrades stay per host at the operator's word; no automatic upgrade (R5) | yes |
