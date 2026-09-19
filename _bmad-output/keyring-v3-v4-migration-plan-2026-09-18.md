# keyring v3 to v4 migration: design plan for stave, bloomctl, sidestep

Status: PLAN ONLY. No source changes and no migration PRs were made in producing this.
Date: 2026-09-18. Author: Michael Pursifull.

bd tickets: stave `aae-orc-qpa49`, bloomctl `aae-orc-vch2t`, sidestep `aae-orc-zb5ar`
(all P3 open, mutually associated via relates_to). Adjacent: `aae-orc-ydto` (stave
named-profiles, P1 in_progress) touches the same stave auth file.

## The premise correction that reframes the whole task

The three tickets describe the work as a feature rename: "keyring v4 removed the
`apple-native` and `linux-native` feature names, find v4's replacement names." That
premise is half right and the remedy it implies is wrong. keyring v4 did not rename
those features. It SPLIT THE CRATE:

- The library and API moved into a new crate, `keyring-core` (at v1.0.0).
- Each platform backend became its own `*-keyring-store` crate (e.g.
  `apple-native-keyring-store`, `zbus-secret-service-keyring-store`).
- The `keyring` crate itself is now, in the maintainer's own words, "just a sample
  app." Its default `v1` feature re-exposes a v1-compatible `Entry` façade over
  `keyring-core`, so a caller that only uses the simple surface keeps working.

The maintainer is explicit on the v4.0.0 release: apps should either enable the `v1`
feature for the old façade, or depend on `keyring-core` plus the store crates directly.
There is NO 1:1 feature name to substitute. Grounded against the tagged source
(v3.6.2 and v4.2.0 Cargo.toml, keyring-core lib.rs and error.rs; sources listed at the
end). This corrects the ticket's plan-phase step 1; the ticket notes now carry the
correction.

## The consequence: our surface is fully covered by the v1 façade

All three CLIs consume keyring identically and use only the simple surface. Grounded by
grep across all three (2026-09-18):

- `keyring::Entry::new(SERVICE, user)` (stave:552/646/658, sidestep:178/187/198,
  bloomctl:244/269/289)
- `entry.get_password()` (read)
- `entry.set_password(secret)` (stave:649, bloomctl:272, sidestep:190)
- `entry.delete_credential()` (stave:660, bloomctl:291, sidestep:200)
- one error arm: `Err(keyring::Error::NoEntry) => Ok(false)` (stave:662, sidestep:202,
  bloomctl:293)

All four methods are present and unchanged in the v4 `v1` façade. `NoEntry` is unchanged
in v4. Two things that WOULD have broken, both checked and both absent:

- No use of any method the v4 façade dropped (`new_with_target`, `new_with_credential`,
  `get_attributes`, `update_attributes`, `get_credential`, `set_secret`, `get_secret`):
  grep found none in any of the three.
- No exhaustive `match` on `keyring::Error` (v4 added variants and changed the
  `Ambiguous` payload, so an exhaustive match would break): each repo matches the single
  `NoEntry` variant with a fallback arm, which the new v4 variants fall into safely.

So for all three, the migration is a Cargo edit with NO auth-module code change expected.
That is the reference finding to capture and replay.

## Decision 1 (the one to ratify): shared reference-first, not three independent

The three declarations and the four call sites are identical in shape. This is one
migration performed three times, not three migrations. Ratify the tickets' own
shared-outcome protocol, made explicit:

  Work ONE repo first as the reference. Capture the v4 crate-split model, the "drop the
  feature list, use `keyring = \"4\"` default v1" remedy, and the "no API change for our
  surface" result in a kos finding. The other two replay the finding; they do not
  rediscover.

Reference-repo pick: **sidestep**. Simplest consumer (single token, no profile or MCP
richness to hide a surprise), its blocking dependency `aae-orc-6ryug` (the develop CI
fix) is already CLOSED so develop is green, and it is the one unambiguously-gitflow repo.
stave is worked LAST because of the ydto interaction (Decision 3).

This is the cross-cutting decision the operator ratifies: shared reference-first (one
finding, two replays) versus three independent migrations. My recommendation is
reference-first; the grounding makes the three provably identical.

## Decision 2 (recommended, low-stakes): the v1 façade path, not keyring-core

Two migration shapes exist:

- **Path A, the v1 façade (RECOMMENDED).** `keyring = "4"` with its default `v1`
  feature. Keeps the exact API we use, auto-registers the default store on first
  `Entry::new` (no boilerplate), zero auth-module change. Matches the tickets' framing:
  not urgent, dependency-currency, not a rewrite.
- **Path B, keyring-core + explicit store crates.** The maintainer's "recommended for
  apps wanting control" path: depend on `keyring-core` plus `apple-native-keyring-store`
  etc., and call `keyring_core::set_default_store(...)` at startup. More code, more
  control (per-entry credentials, non-default stores). We need none of that today.

Recommend Path A for all three. Revisit Path B only if a CLI ever needs per-entry
credentials or a non-default store. I am recommending, not leaving this open; flag it
only if the operator wants Path B for a forward reason I do not see.

## The per-repo mechanical steps (identical; driven by the reference finding)

1. Workspace-root Cargo.toml: replace
   `keyring = { version = "3", features = ["apple-native", "linux-native"] }`
   with `keyring = "4"` (default `v1` feature). Drop the explicit feature list; the `v1`
   default supplies macOS Keychain via `apple-native-keyring-store/keychain`. The
   `crates/<name>-sdk/Cargo.toml` `keyring.workspace = true` line needs NO edit.
   (stave root:49, bloomctl root:45, sidestep root:45.)
2. Regenerate the lockfile: `cargo update -p keyring` (pulls keyring 4.x + keyring-core
   1.x + the store crates).
3. Expect NO auth-module change. If clippy/tests surface a delta, apply it against the
   v1 façade; do not reach for keyring-core (that is Path B).
4. `ax lint <repo>` (clippy -D warnings) and `ax test <repo>` (workspace tests) green.
5. Local macOS Keychain round-trip verification (next section).
6. Dispose of the Renovate bare-bump PR: comment linking this work, then close it or let
   the migration branch supersede it. The bare bump renames nothing and cannot merge as
   filed. PRs: stave#16, bloomctl#20, sidestep#18.

## Verification path, and why it does NOT touch the live tenant

Constraint from the task: these are write-guarded prod-touching CLIs; seed/prod writes
stay operator-run; the verification plan must not assume the agent can exercise the live
tenant.

It does not need to. The keyring migration touches the LOCAL macOS Keychain custody path
(SOUL ADR-009 custody boundary, audience-not-format), not the vendor prod API. What
proves v4 works is a local Keychain round-trip, which is safe to run and exercises
exactly the changed code:

- CAN run (agent or builder), all local: `cargo build`, clippy, workspace tests, and a
  local Keychain round-trip under a throwaway service name
  (`service = <repo>-keyring-migration-test`): store a random secret, read it back,
  assert equal, `delete_credential`, assert `NoEntry`.
- STAYS operator-run and OUT OF SCOPE for this migration: any write against the live
  vendor tenant (Wiz GraphQL, iru/Kandji, StepSecurity). The migration does not touch
  that path; its write-guard and operator-run posture are unchanged.

### The cross-major compatibility check the docs could not answer

Primary sources confirm the `v1` façade keeps the API identical. They do NOT state that
a Keychain item WRITTEN by v3's `apple-native` is read back byte-identically by v4's
`apple-native-keyring-store` (same service/account attribute mapping). The project has
cared about cross-version credential compatibility historically, but that is not a 3->4
guarantee. So add one verification step and do not assert compatibility as fact:

- With the current v3 binary, store a real (throwaway) entry via the CLI's own login.
- Rebuild on v4, read it back via the CLI's own status/read path.
- If it reads: the upgrade is transparent to existing users. Record that in the finding.
- If it does NOT: users re-authenticate once after upgrade. That is acceptable for a
  credential CLI, but it must be DOCUMENTED (a one-line upgrade note), not discovered.

This is the one place the migration could surprise a user, and it is cheap to settle.

## macOS scope limit (stated plainly)

Validation is macOS-only. The fleet is macOS; the round-trip and the cross-major check
run against the macOS Keychain backend. Linux is COMPILE-verified only (the crate
resolves and builds), not round-trip-verified here.

One Linux caveat worth recording, not chasing: v4's `v1` default uses
`zbus-secret-service-keyring-store` (the D-Bus desktop secret service) on Linux. Our v3
`linux-native` used `linux-keyutils` (the kernel session keyring), a DIFFERENT backend
with different persistence semantics. Under Path A the Linux backend changes from
keyutils to secret-service. This is moot for a macOS fleet and Linux-compile-only
validation. If keyutils parity on Linux is ever required, that is the `cli` feature or a
keyring-core + `linux-keyutils-keyring-store` dependency, and a separate Linux-host task.

## Decision 3 / coordination flag: stave follows ydto, does not race it

`aae-orc-ydto` (P1, in_progress) is "stave: named profiles ... per-profile keyring." It
adds per-profile keyring call sites (`keyring_client_secret_user(profile)`) in the same
`stave/crates/stave-sdk/src/auth.rs` this migration edits. The migration is a Cargo edit
with no expected code change, so the collision is small, but it is a collision. Sequence:
land ydto first (it is P1 and in flight), then apply the stave keyring bump on top. Under
Path A the bump inherits ydto's new call sites with no extra work (they use the same
`Entry` surface). This is why stave is worked LAST of the three. Flag for the operator:
the stave keyring bump should follow ydto, not race it.

## Branch targets (verified, correcting the tickets)

The tickets say "branch from and PR into develop" for all three. Verified 2026-09-18
against each repo's remotes:

- sidestep: has `origin/develop`. Gitflow. PR into develop. (Ticket correct.)
- stave: `origin/main` only, no develop. PR into main. (Ticket wrong; corrected in notes.)
- bloomctl: `origin/main` only, no develop. PR into main. (Ticket wrong; corrected in notes.)

## Sequencing summary

1. sidestep first (reference): PR into develop. Capture the finding.
2. bloomctl second (replay): PR into main.
3. stave last (replay), after ydto lands: PR into main.

Each: Cargo edit + lockfile + build/clippy/test + local Keychain round-trip + cross-major
read-back check + Renovate PR disposition. Each closes its bd ticket with the finding
cross-linked.

## Sources (primary, byte-verified)

- keyring v4.0.0 release notes ("just a sample app," use keyring-core):
  github.com/open-source-cooperative/keyring-rs/releases/tag/v4.0.0
- v4.2.0 Cargo.toml (`default = ["v1"]`, the store-crate dependencies):
  .../keyring-rs/blob/v4.2.0/Cargo.toml
- v3.6.2 Cargo.toml (`apple-native`, `linux-native`, no default feature):
  .../keyring-rs/blob/v3.6.2/Cargo.toml
- v4 src/v1.rs (the façade Entry: new / set_password / get_password / delete_credential;
  auto set_default_store; NoDefaultStore): .../keyring-rs/blob/v4.2.0/src/v1.rs
- keyring-core lib.rs and error.rs (new_with_modifiers, Arc credential, new Error
  variants, PlatformError alias): github.com/open-source-cooperative/keyring-core
- Latest versions: keyring 4.2.0 (2026-08-29), keyring-core 1.0.0 (2026-04-21),
  keyring 3.6.3 (last of v3, 2025-07-27).
- Repo moved hwchen/keyring-rs -> open-source-cooperative/keyring-rs (old URL redirects).

There is no official "3 to 4 upgrade" document; the guidance above is distilled from the
release notes, README History, and crate module docs. This plan IS that missing document
for our three CLIs.
