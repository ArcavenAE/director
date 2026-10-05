# director-mcp: a reproducible release and a pinned install

Status: design for review, 2026-10-02. Owner: the architect role. Tracks
director#68. Builds on `sim/design/launcher-distribution.md` (approved
2026-09-27), which already installs the launcher from a commit. Design only;
builders follow review.

## 1. Why

A seat runs whatever director-mcp binary it was started with, and today
nothing says which build that is or whether it matches a commit. During the
cross-host return-path outage (finding-027) the running shim was a dirty,
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

One build recipe, `scripts/build-director-mcp <sha>`, used by CI and by the
installer's from-source path:

```sh
VERSION="0.1.0-alpha.$(TZ=UTC git show -s --format=%cd --date=format-local:%Y%m%d.%H%M%S "$SHA").${SHA:0:7}"
GOTOOLCHAIN=go1.26.5 CGO_ENABLED=0 go build -trimpath -buildvcs=false \
  -ldflags "-s -w -buildid= -X main.version=$VERSION -X main.revision=$SHA -X main.channel=$CHANNEL" \
  -o director-mcp .
```

- **`-buildvcs=false`.** A build from a git checkout embeds VCS settings and a
  build from `git archive` of the same commit does not, so without this flag
  CI (a checkout) and `--ref` (an archive) give different bytes. It also stops
  the walk up to an enclosing repository (finding-018) for every recipe build.
- **The toolchain is pinned in the recipe.** `go 1.26.5` in `go.mod` is a
  minimum: under `GOTOOLCHAIN=auto` a host with a newer Go builds with it.
  The recipe sets `GOTOOLCHAIN=go1.26.5`, and `go.mod` gains a matching
  `toolchain go1.26.5` line so `setup-go` in CI reads the same version.
  Raising it is a one-line change in both places, in one PR.
- **VERSION comes from the commit, not a clock.** The date and time are the
  commit's committer timestamp in UTC, so CI and `--ref` derive the same
  string for one commit. A stable tag `director-mcp-v<semver>` sets
  `VERSION=<semver>` instead. This departs from kos and marvel, which stamp
  CI's clock; the departure is what makes `--ref` match a release.
- `main.revision` is the commit being built, never a directory walk.
  `shimRevision()` prefers it when set and falls back to the VCS stamp only
  for an unstamped build.
- `-trimpath`, an empty build id and `CGO_ENABLED=0` remove the remaining
  path and host differences. Test 3 checks the whole recipe.

**aae-orc-ldteb is partly closed.** Every recipe build (CI, `--ref`) is
stamped from the commit. An ad hoc `go build` in a worktree, outside the
recipe, still stamps the enclosing repository: finding-018's own case. That
remainder is marked by #126 slice M's reader until ldteb's own fix lands, and
the PR uses `Refs`, with `REMAINING: ad hoc local builds` recorded on the
ticket.

### R3. Release, following kos and marvel

A workflow in this repository, on push to main when
`probe/nats-phase-0/director-mcp/` changes:
- builds R2 for darwin/arm64, darwin/amd64, linux/amd64 and linux/arm64;
- publishes a prerelease tagged `director-mcp-alpha-<date>-<time>-<sha7>`
  (prefixed, since the repository may release other things later), with the
  four binaries and a `SHA256SUMS`;
- attests each binary (`actions/attest`, as kos and marvel do) and publishes the attestation bundle as a release asset
  (`director-mcp-<os>-<arch>.sigstore.json`).

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

- `--release` downloads the asset for this host, its bundle and
  `SHA256SUMS`, then runs three checks in order. The first that fails aborts
  the install with a message naming the check, and the symlink is not
  touched:
  1. **Checksum:** the asset matches its `SHA256SUMS` line.
  2. **Attestation:** `gh attestation verify <asset> --repo ArcavenAE/director
     --signer-workflow ArcavenAE/director/.github/workflows/release-director-mcp.yml
     --bundle <asset>.sigstore.json`. The flags refuse an attestation from
     another repository or workflow. Passing the downloaded bundle means the
     check does not depend on the attestation API, but `gh` still needs a
     trust root. A host where it cannot verify refuses the install and names
     `--ref` as the path that builds from source instead. There is no fleet
     precedent for this step: kos and marvel produce attestations and nothing
     in the fleet verifies one yet, so V4 is its first consumer.
  3. **First exec:** `director-mcp --version`, run as in section 4 item 3
     (stdin from `/dev/null`, a 5-second timeout, an unreachable
     `NATS_URL`), must name the release's commit.
- `--ref` builds R2 from `git archive` of that commit in the bare mirror, so
  a host with no release yet, or an operator testing a commit, gets the same
  stamped binary.
- Either way the binary goes to `~/.director/lib/director-mcp/<sha>/` with a
  `STAMP` (sha, version, channel, features, source, install time, taken from
  the guarded first exec), and
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
   and the shim's installed `STAMP`, and the spawn line carries `launcher
   <sha> shim <version>`. It does not execute the shim to learn its version.
   A shim from before #75 ignores unknown flags and would start a live shim,
   and one from before V1 exits nonzero. A seat's first act echoes that line,
   so a reader of the spawn log knows which builds each seat started with.
3. **The cue is gated on the shim's features.** With `DIRECTOR_CUE=1`,
   cast-launch checks that the shim's `STAMP` lists `cue`. When the shim has
   no `STAMP` (a `DIRECTOR_MCP` override, or a host not yet on R4), it runs
   `--version` guarded: stdin from `/dev/null`, a 5-second timeout, and
   `NATS_URL` set to an unreachable address, so even a pre-#75 shim cannot
   join the bus. A nonzero exit, a timeout or no `cue` in the output means
   `version unknown, cue unavailable`. The spawn line then says `cue:
   unavailable (shim <version or unknown> has no cue)` and the seat starts
   without the channel flag. It is never silently off, which is the C-0
   failure.

## 5. Parts, in order

| part | what | depends on |
|---|---|---|
| V1 | `--version` / `version` and `--version --json`, no config read, no connect; `main.version`, `main.revision`, `main.channel`; `shimRevision()` prefers `main.revision` | none |
| V2 | the R2 build recipe as a script (`scripts/build-director-mcp`), the `toolchain` line in `go.mod`, plus the reproducibility test | V1 |
| V3 | release workflow (R3) | V2 |
| V4 | `director-install director-mcp` (`--release`, `--ref`, `--rollback`, `--status` row and WARNs); `director-mcp-seat` execs the installed path (aae-orc-x07y8) | V2 |
| V5 | cast-launch spawn line names launcher and shim from their STAMPs; the cue feature gate with the guarded fallback (aae-orc-wnuzs) | V1, V4 |
| V6 | `unread --by-rev` grouping | director#126 slice M |

## 6. Tests (red first)

1. **V1:** `director-mcp --version` with no config and no broker reachable
   prints the line and exits 0, and opens no network connection (run under a
   sandbox that refuses connects); an unknown argument is still refused;
   `--version --json` parses.
2. **V1 rev:** a binary built with `-X main.revision=abc1234` inside a git
   worktree reports `abc1234` in `--version` and in presence; an unstamped
   build falls back to the VCS stamp.
3. **V2:** in an unrelated git repository (so a walk-up would find the wrong
   root), with a Go newer than 1.26.5 on `PATH`, build one commit twice
   through the script: once from a `git clone` checkout and once from `git
   archive`. The two binaries are byte-identical, `go version -m` reports
   go1.26.5 and no `vcs.*` lines (build info never records the
   `-buildvcs` flag itself, so the test asserts its effect), and `--version` names that commit with a
   VERSION built from the commit timestamp. Red on content: the red run puts
   today's plain `go build -o director-mcp .` in the script, so the
   comparison fails on the bytes, the toolchain and the revision, not on a
   missing file.
4. **V3:** in a dry run of the workflow (act or a fork), the prerelease has
   four binaries and `SHA256SUMS`, and each binary's `--version` names the
   pushed commit.
5. **V4:** `--release` with a tampered asset fails the checksum; an asset
   whose bundle was signed by another repository or workflow, and an asset
   with no bundle, each fail the attestation step; a host where `gh` cannot
   verify refuses and names `--ref`. In every failing case the install aborts
   with the check named and the symlink is unchanged; `--ref` produces the same `--version` line as the
   release of that commit; `--rollback` restores the previous directory;
   `--status` WARNs on an MCP config naming a checkout path, and exits 0;
   on macOS the installed binary has no `com.apple.quarantine` attribute and
   runs.
6. **V5:** with `DIRECTOR_CUE=1` and a shim whose `STAMP` lacks `cue`, the
   spawn line carries `cue: unavailable` and the harness starts without the
   channel flag; with a cue-capable `STAMP`, the flag is passed and the line
   names both builds, and the shim was not executed. With no `STAMP` and a
   pre-V1 shim (one that ignores `--version` and serves, and one that exits
   nonzero), the guarded fallback reports `version unknown, cue unavailable`,
   returns within the timeout, and no presence row or consumer appears on a
   scratch broker.

## 7. Rulings needed (operator, via director)

| # | question | default |
|---|---|---|
| 1 | Release as GitHub prereleases with checksums and attestation, not the Homebrew tap, for seat hosts (R3, R4) | yes |
| 2 | macOS signing and notarization left out of slice 1 (R3) | yes |
| 3 | Upgrades stay per host at the operator's word; no automatic upgrade (R5) | yes |
