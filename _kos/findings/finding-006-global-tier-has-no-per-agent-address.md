# finding-006: the global tier has no per-agent address, so every inbound message to a cluster is a fan-out and "the receipt" is ambiguous by construction

- **Date:** 2026-09-21
- **Session:** migrated-marvel-builder-g1-1, mokuzai, pricing the zero-fan-out options for aae-orc-2vwae
- **Subject:** director global-tier addressing (R-86, R-94, R-95), so this belongs in director's graph
- **Confidence:** measured for the live shape; the mechanism read directly in `global.go` and in the rendered `authorization.conf` on this host

## 1. The property

At the global tier a session is addressable only as `global://<cluster>/<role>`
or `global://director`. There is no per-agent global address. The two legal role
words are hard-coded (`global.go:36-38`) and an unknown one is refused before the
shim dials anything, verified on this host:

    DIRECTOR_GLOBAL_ROLE=harness  ->
    director-mcp: DIRECTOR_GLOBAL_ROLE "harness" is not a global role;
    exactly two exist, supervisor and director (R-94)

Every supervisor session on a cluster therefore holds the SAME inbound address.
Each builds its own durable on `GLOBAL_TO_<cluster>` with the identical
`FilterSubject global.<cluster>.supervisor.inbox` (`global.go:254`) under
`DeliverAllPolicy` (`global.go:256`), and `fetchOne` (`global.go:315`) applies no
recipient filter: it unmarshals whatever it pulled and hands it to the model.

So one message to `global://mokuzai/supervisor` is delivered to every live
supervisor on mokuzai, and replayed in full to every supervisor session that
starts inside the stream's 24h max age. Measured on mokuzai 2026-09-21: four live
supervisors across three teams (errand x2, reviewer, migrated) all holding that
one address.

## 2. Why it is a defect and not just a shape

aae-orc-2vwae asks for a REQUEST answered by "a receipt written by the
recipient". With a fan-out address there is no *the* recipient. A single REQUEST
can return up to four AGREEs from four different `agent_id`s, all correlating to
the same `in_reply_to`, all equally valid. A sender cannot address one of them,
and cannot tell from the address how many answers to expect.

This is independent of any instrumentation. It is a property every user of this
bus already has, including the director seat dispatching work to a named cluster.

It also makes a test REQUEST a non-private act, which is the operational
consequence: there is no way to exercise the inbound path against a cluster
without putting the message into every working agent's inbox on it.

## 3. The two-sided lock, which is what closed every zero-fan-out option

Pricing the alternatives for aae-orc-2vwae surfaced that the constraint is
enforced in two independent places, and this is worth stating because each one
alone looks like it could be worked around:

1. **The shim** refuses any role word but `supervisor` and `director`, at start
   and again in `parseGlobalAddress`, citing R-94. So a harness cannot borrow an
   unused role such as `global.mokuzai.harness.inbox`.
2. **The credential** refuses it anyway. In marvel's rendered
   `authorization.conf` on this host, every team user's global publish allow is
   exactly `global.director.inbox` plus `$JS.global.API.>`. Not one team user can
   publish to `global.<cluster>.>`; that subject appears only in the SUBSCRIBE
   list. Count of team users granted publish on a cluster inbox: zero.

Consequence worth naming: **a session on a cluster cannot send to a supervisor on
its own cluster at the global tier.** Only the far side's leaf, which is granted
`global.*.supervisor.inbox` at the hub, can publish into a cluster inbox. The
upward direction (`global.director.inbox`) is open to everyone, which is R-95's
asymmetry working as designed.

That also means a live cross-host round trip against mokuzai's supervisor cannot
be initiated from mokuzai at all. It has to be driven by the director seat.

## 4. What would actually fix it

Not a harness change; an addressing change. The minimum is a per-agent or
per-session global address so a reply has one destination, which is the global
twin of what R-84 (`aae-orc-8gp9f`) is already asking for at the local tier. A
weaker interim that costs nothing at the broker: have `fetchOne` drop, or at
least mark, an envelope whose `recipient.address` names an agent this session is
not, so a fan-out delivery is visible to the model rather than silent. That is a
filter, not a boundary, exactly as `aae-orc-6vy9x` says of its own narrowing.

Related and distinct: `aae-orc-6vy9x` is about a session being able to SUBSCRIBE
to an inbox it does not hold. This is about sessions that legitimately hold the
same inbox because the model gives them no other name.

## 5. Honest limits

I did not publish anything to a live cluster inbox to demonstrate the fan-out; I
could not have, per section 3 item 2, and I was directed not to. The fan-out is
established from the consumer construction in source plus the four live
same-address presence rows, not from an observed multi-delivery. The isolated rig
in `probe/nats-global-tier/verify-roundtrip-receipts.sh` exercises the one-to-one
case only, so it does not reproduce this and is not evidence either way.

Separately unresolved and not explained by this: `GLOBAL_TO_mokuzai` reported
`consumer_count` 3 against four live supervisor presence rows on 2026-09-21. A
cluster credential cannot enumerate consumers (`CONSUMER.NAMES` is outside the
leaf's allow-list and returns "no responders", which means denied, not absent),
so this needs someone with a hub credential. Carried as an open lead.
