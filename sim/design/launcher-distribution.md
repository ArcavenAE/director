# Director launcher distribution

Status: approved design, 2026-09-27. Owner: arcaven-architect-g5-0. Operator
ruling 2026-09-27, relayed by director: yes to L1 (the design), L2 (a seat
edits the two operator-local wrappers on kinu) and L3 (bootstrap mokuzai now).
The build goes through arcaven-supervisor under the standard workflow.

## Recommendation

A merge becomes live with one command per host, run from anywhere, with no
working tree involved:

```sh
~/.director/bin/director-install cast-launch --ref origin/main
```

1. **Versioned installs with an atomic switch.** The installer:
   - fetches into a bare mirror at `~/.director/src/director.git`;
   - writes `git archive <ref> sim/twin` into
     `~/.director/lib/cast-launch/<sha>/`, with a `STAMP` file recording the
     sha, ref, source URL and install time;
   - runs `verify-cast-launch.sh` against that directory;
   - only if the verify passes, switches the symlink
     `~/.director/bin/cast-launch` to the new directory (`ln -sfn` to a temp
     name, then `mv -f`, so the switch is atomic).

   The previous version directory stays for rollback. Nothing runs from a
   working tree, and a dirty checkout cannot leak in, because `git archive`
   reads the commit, not the files.
2. **Wrappers exec the installed copy.** `cast-seat.sh:78` and
   `cast-dtu-seat.sh:61` change to
   `exec "${DIRECTOR_CAST_LAUNCH:-$HOME/.director/bin/cast-launch}"`. The
   other wrappers already reach cast-launch through these two (cast-aae,
   cast-infra, and the cast-dtu-* family all `exec` one of them).
   `DIRECTOR_CAST_LAUNCH` is the deliberate way to test a checkout. If the
   launcher is missing, the wrapper fails loudly and names the install
   command. It never falls back to the checkout path silently, because a
   silent fallback is how staleness hides.
3. **Upgrade.** Run the one command above after a merge that touches
   `sim/twin/`. The next spawn picks it up. Running seats need no roll: the
   launcher runs only at spawn and then `exec`s the harness, so no seat keeps
   it resident. A seat that needs the new launch behavior gets it at its next
   restart or shift. Forcing that is the operator's call per seat, not the
   installer's job. Rollback is `director-install cast-launch --rollback`,
   which switches the symlink back to the previous directory.
4. **Doctor, diagnostic only.** `director-install --status` prints, for each
   installed component:
   - the installed sha and its age;
   - how many commits on `origin/main` touching `sim/twin/` the installed sha
     is behind (this count is the one that matters; total commits behind is
     noise);
   - a WARN for any wrapper under `~/.marvel/manifests` that still names a
     checkout path.

   It always exits 0 (SOUL §8, `diagnostic-not-gate.md`). `aq hygiene` can
   call it when present, but director does not depend on aq. The one
   pass/fail step in this whole design is the verify run before the switch,
   and that is a structural check on the artifact being installed.
5. **mokuzai.**
   - Now (optional, harmless): bootstrap the same mirror and install the
     launcher and the pinned wardrobe clone.
   - Later: moving skippy's manifests from inline role text to cast-launch is
     launch-parity phase 2's decision (aae-orc#421 names phase 2 as owner of
     that fix), so it waits for that ruling. The install makes the move a
     manifest edit when it comes.
6. **Packaging.** install.sh is enough while there are two hosts, both run by
   the operator. Revisit at the first-user milestone, or when a third host or
   a non-operator user appears:
   - a brew formula for the code (launcher, shim, board);
   - sideshow for role content, which is wardrobe's shape.

## Premises checked (one command each)

| Claim | Command | Result |
|---|---|---|
| kinu seats exec the checkout | `grep -n cast-launch ~/.marvel/manifests/*.sh` | holds: `cast-seat.sh:78`, `cast-dtu-seat.sh:61`; the others exec those two |
| `install.sh --copy` can upgrade | read `install_one` in `install.sh` | **false**: it refuses to replace a non-symlink, so a second `--copy` fails. That is why a copy install needs versioned dirs plus a symlink |
| cast-launch is location-independent | `grep -n SCRIPT_DIR cast-launch.sh` | **false**: `MARVEL_OVERLAY_ROOT` defaults to `$SCRIPT_DIR/overlays`, so an installed copy would look for overlays inside the install tree. No overlays exist on kinu today (`ls` of both candidate paths), so moving the default costs no migration |
| verify can test an installed copy | `verify-cast-launch.sh:20` | **false as written**: `LAUNCH="$here/cast-launch.sh"` is fixed; it needs a `CAST_LAUNCH` override |
| overwriting a running script is safe | scratch test: rewrite a sleeping bash script in place | **false**: the running process executed the NEW text. An in-place `cp` during a spawn can run a mix of two versions, and the atomic symlink switch avoids that |
| upgrades are frequent enough to need one command | `git rev-list --count HEAD -- sim/twin/cast-launch.sh` | 9 commits, 3 of them 2026-09-25 to 26 |
| mokuzai uses no cast-launch | `ssh mokuzai ...` | **unverified**: connection refused. Taken from the brief and aae-orc#421 |
| a doctor surface exists | `git ls-files \| grep doctor` in director | none; `tools/aq-hygiene.py` exists in the orc |

## Changes for the builder

- **director `install.sh`**
  - Add the `cast-launch` component: mirror, archive, verify, atomic switch,
    `--ref`, `--rollback`, `--status`.
  - Keep the existing skill and board installs as they are.
  - Install itself as `~/.director/bin/director-install` so a host can
    upgrade with no checkout.
- **`sim/twin/cast-launch.sh`**
  - The overlay default becomes `${DIRECTOR_HOME:-$HOME/.director}/overlays`.
    Overlays are state, not code.
- **`sim/twin/verify-cast-launch.sh`**
  - `LAUNCH="${CAST_LAUNCH:-$here/cast-launch.sh}"`.
- **Operator-local wrappers (`~/.marvel/manifests/cast-seat.sh`,
  `cast-dtu-seat.sh`)**
  - Apply the exec change from item 2. These files are in no repository, so
    the operator applies it or approves a seat doing it.
  - They are a second distribution gap of the same kind: each host's copies
    diverge. Whether they move into a repo belongs to launch-parity phase 2.
- **Proving tests**
  - Install at ref A, then at ref B, and check that the symlink points at B
    and A's directory remains.
  - `--rollback` returns to A.
  - A ref whose verify fails leaves the symlink unchanged.
  - A dirty working tree does not change the installed bytes.
  - `--status` reports 0 behind at `origin/main`, and N behind after N commits
    to `sim/twin/`.

## Follow-ons, not in scope

- **`director-mcp` (the shim).** It has the same problem, and it is
  hand-installed today with `.prev-<date>-<sha>` backups in
  `~/.director/bin`. Those backups are an informal version of item 1. Fold
  the shim into the same installer as a second component once the launcher
  path is proven.
- **Auto-install on merge** (a post-merge hook or a timer). Not recommended
  now: a bad merge would reach every host with nobody deciding it (ADR-007).
  With `--status` showing the lag, a person runs the one command.

## Rulings (operator, 2026-09-27)

- **L1:** the design is approved as stated.
- **L2:** a seat applies the wrapper edit on kinu.
- **L3:** bootstrap mokuzai now; move its manifests after launch-parity
  phase 2.

## Tickets (flat, with edges)

1. `aae-orc-v0mu6`: the director-install cast-launch component and its tests.
   Blocked by `aae-orc-6dkwn`, since the verify step needs the override.
2. `aae-orc-6dkwn`: the cast-launch overlay default plus the verify override.
3. `aae-orc-e9zo5`: the kinu wrapper exec change. Blocked by `aae-orc-v0mu6`.
4. `aae-orc-yu5bv`: the mokuzai bootstrap. Blocked by `aae-orc-v0mu6`.
5. `aae-orc-x07y8`: the shim as a second installer component. Blocked by
   `aae-orc-v0mu6`; later.
