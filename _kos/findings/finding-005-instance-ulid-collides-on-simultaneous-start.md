# finding-005: the instance ULID is not a session discriminator; two processes started together mint the same one 23.5% of the time

- **Date:** 2026-09-21
- **Session:** migrated-marvel-builder-g1-1, mokuzai, building the aae-orc-2vwae round-trip harness
- **Subject:** director session identity (R-49, R-50, R-06), so this belongs in director's own graph beside finding-003
- **Confidence:** measured. 200 simultaneous process pairs against the shim's own ULID library version, plus two unforced occurrences inside the harness before I went looking

## 1. What was observed

While building `probe/nats-global-tier/verify-roundtrip-receipts.sh`, which starts two
shims at once, the two sessions twice reported the SAME instance:

    director verify-director-g1-0 (01M330H2FXY7SMA14RV1BK4JPE)
    supervisor verify-supervisor-g1-0 (01M330H2FXY7SMA14RV1BK4JPE)

I first assumed a scrape race in my own script and checked, because an
identical ULID from two processes should be impossible. The scrape was correct
and the files genuinely held the same value.

Measured directly, with a one-line Go program importing the same library
version the shim pins (`github.com/oklog/ulid/v2 v2.1.2`), launching two
processes simultaneously 200 times:

    simultaneous pairs: 200   pairs that collided: 47

23.5%. This is not a birthday-paradox tail; it is a systematic property of how
the default entropy is seeded.

## 2. Mechanism

`ulid.Make()` uses the package-level default entropy
(`oklog/ulid/v2@v2.1.2/ulid.go:135`):

    var defaultEntropy = func() io.Reader {
        rng := rand.New(rand.NewSource(time.Now().UnixNano()))
        ...

That is `math/rand`, seeded from the wall clock at package init. Two processes
that initialise in the same clock tick draw the same seed, and if they also
land in the same millisecond, `Make()` returns byte-identical ULIDs. The shim
calls it once per session at `probe/nats-phase-0/director-mcp/bus.go:134`.

The condition is not exotic. It is what a launcher does: marvel casts a whole
team in one go, and `marvel shift` rotates a generation together.

## 3. Why this matters, and it is sharper than it first looks

**It removes the discriminator finding-003 relies on.** finding-003 records two
different-purpose sessions colliding on one `agent_id`, and says "the only
roster discriminators are the instance ULID and the pid." One of those two is
now measured unreliable under exactly the condition a launcher creates.

**It can compound with finding-003 into mail theft.** finding-003 section 4
states that "R-50's per-session durable name makes even a misconfigured
duplicate id unable to steal another session's mail." The durable is
`mcp_<agentID>_<instance>` (`bus.go:141`) and its global twin is
`mcp_global_<agentID>_<instance>` (`global.go:165`). If `agent_id` collides
(finding-003's case) AND the instance collides (this one), the durable name
collides too and two shims race one consumer. R-50's protection is conditional
on the instance being unique, and it is not.

**It can silently under-count the global roster on its own**, with no
`agent_id` collision needed. The global presence key is
`presence.<cluster>.<role>.<instance>` (`global.go:137-147`) and carries NO
agent id. Two supervisors on one cluster, cast together, that draw the same
instance write the same key: one row, last writer wins. The overwritten
supervisor is then absent from the global roster and invisible to the R-92
liveness check, while running perfectly well. The local key
(`presence.<team>.<agentID>.<instance>`) includes the agent id, so the local
tier is exposed only to same-agent restarts.

## 4. Honest limits

I did not catch a collision on the live mokuzai cluster. The four live global
supervisor rows on 2026-09-21 carry distinct instances, so this is not offered
as the explanation for anything currently observed there, and specifically not
for the four-presence-rows-against-three-consumers discrepancy I saw the same
day, which I could not resolve from a cluster credential and which remains
open. What is established is the generator's behaviour under simultaneous
start, and two unforced occurrences in a rig that starts two shims together.

The rate is host-dependent: it turns on clock granularity and process start
skew, so 23.5% is this host today, not a constant.

## 5. Fix

One line, in the shim rather than in the library: seed from `crypto/rand`
instead of the clock, which is what `ulid` documents for exactly this case.

    entropy := ulid.Monotonic(crypto_rand.Reader, 0)
    instance := ulid.MustNew(ulid.Now(), entropy).String()

That keeps the sortable-by-time property the instance is wanted for and
removes the shared-seed failure. It does not need a manifest surface, a
protocol change or a restart of anything, and it is independent of the R-84
declared-name path (`aae-orc-8gp9f`), which addresses a different problem: that
a shift SHOULD sometimes preserve identity. This finding is about two sessions
that should never have shared one and did.

Not self-ratified. I am an instrument builder on aae-orc-2vwae and filed this
under the layer-1 rule in `task-workflow.md`; whether it earns a GitHub issue
or a bd slot is the supervisor's call.

## 6. Where it was found

The harness that surfaced it asserts the property so a regression is caught:
check 0 of `probe/nats-global-tier/verify-roundtrip-receipts.sh` fails the run
if the two sessions report one instance. That script staggers its two starts by
300ms so the round-trip gate measures the round trip rather than re-measuring
this defect on every run, and says so at the call site.
