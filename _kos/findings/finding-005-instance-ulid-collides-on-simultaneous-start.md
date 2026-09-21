# finding-005: the instance ULID is not a session discriminator; two processes started together mint the same one 23.5% of the time

- **Date:** 2026-09-21
- **Session:** migrated-marvel-builder-g1-1, mokuzai, building the aae-orc-2vwae round-trip harness
- **Subject:** director session identity (R-49, R-50, R-06), so this belongs in director's own graph beside finding-003
- **Confidence:** measured. 200 simultaneous process pairs against the shim's own ULID library version, plus 160 staggered pairs as the control, plus occurrences inside the harness before I went looking

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
(`oklog/ulid/v2@v2.1.2/ulid.go:135-136`):

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

## 3b. Two consequences that change what it costs

**An orderly exit evicts a LIVE session's row, not only the overwritten one.**
`deletePresence` removes `presenceKey(instance)` (`global.go:363-365`), and the
global key carries no agent id. So if A and B share an instance and B won the
write, A exiting cleanly deletes the row B is living in, and the SURVIVING
session goes dark. It is bounded: the 30s heartbeat rewrites the row against a
90s TTL, so B reappears within a beat. But for up to 30 seconds a live
supervisor is absent from the global roster and from the R-92 liveness check,
and there is no log line on either side saying so. A sender in that window gets
an R-92 refusal for a session that is running perfectly. The mechanism is not
theoretical: check 10 of `verify-roundtrip-receipts.sh` exercises orderly-exit
deletion and measures it clearing the row in under a second.

**The R-49 collision detector is silenced by exactly the case it was built
for.** `checkCollision` (`bus.go:725`) warns only when it finds a row whose
`instance` differs from its own:

    if inst, _ := rec["instance"].(string); inst != "" && inst != b.instance {

Two sessions sharing both agent id and instance write ONE local presence key, so
the loop sees a single row, its own, `inst == b.instance`, and it returns empty.
The detector added in response to finding-003 goes quiet in precisely the
compound case that makes finding-003 worse.

That reframes the defect. It does not only remove a roster DISCRIMINATOR, which
is passive; it disables an active runtime GUARD. Four protections keyed on one
value fail together: the local durable collides, the global durable collides,
both presence rows collapse, and the warning that would have reported it is
silent.

None of this widens the fix. The one line in section 5 closes all four, because
all four depend on the same value being unique.

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

Layer 2 filed as ArcavenAE/director#62 (2026-09-21), on the supervisor's
ruling: a GitHub issue rather than a bd slot, because bd would mean committing
to close it on a timeframe and whether the director plane takes the change is
not this team's to commit.

## 6. Where it was found

The harness that surfaced it asserts the property so a regression is caught:
check 0 of `probe/nats-global-tier/verify-roundtrip-receipts.sh` fails the run
if the two sessions report one instance.

That check went on to correct this finding, which is the reason this section
is longer than "it was found by a script."

I first recorded the harness occurrences as unforced, meaning two sessions that
were meant to start apart and collided anyway. That was wrong, and the error
was mine rather than the generator's. The script appeared to stagger its two
shim launches by 300ms, but a FIFO opened for reading blocks until a writer
appears, so each backgrounded shim parked on its stdin redirect and never
reached exec. A single later `exec` opened both write ends at once and released
both processes in the same instant. The sleep between the launches did nothing,
and the two "staggered" starts were simultaneous starts.

So those occurrences corroborate the mechanism in section 2 rather than
extending it: they are two more simultaneous pairs, not evidence that separated
starts collide. The stagger now sits on the fd opens, which are the real start
barrier, and the call site says why.

A second cause sat behind the first. With the stagger corrected the collision
still returned about one run in four, because the FIRST exec of a freshly built
binary is cold and can take longer to reach package init than the stagger
itself, letting the second process, now running a hot binary the first paged
in, catch up. Paging the binary in before either real start fixes that: 0
collisions in 10 runs with the warm-up, 1 in 4 without. I established this the
expensive way, by deleting the warm-up on the reasoning that the corrected fd
barrier had made it redundant and watching the collision come straight back.
Two independent causes, both of which had to be removed.

Two controls were run afterwards to pin the boundary, 80 pairs each: starts
300ms apart with `ulid.Make()` called immediately, and starts 300ms apart with
the `Make()` calls forced to land together. Neither collided. Separation of the
package inits is what avoids the collision; when `Make()` is called does not
matter. That strengthens section 2 rather than qualifying it, and it is the
reason no change is needed to the fix in section 5 or to director#62.

The wider point is the one worth keeping: for about an hour I had evidence of
two processes with different pids, different agent ids and different brokers
minting one ULID 300ms apart, which the library cannot do, and I was close to
reporting the defect as worse than measured. The reading that resolved it was
not of the library but of my own rig. A harness that contradicts a well-founded
model is the harness's bug until proven otherwise, and once one cause is found
that is not evidence it was the only one.
