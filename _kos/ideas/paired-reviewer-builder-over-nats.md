# A reviewer and a builder paired on one PR, coordinating over NATS directly

- **Status:** idea (pre-hypothesis, no commitment). No ticket and no frontier
  node yet.
- **Date:** 2026-10-22
- **Subject:** director. The pairing is a routing choice about who talks to
  whom on the bus during a PR loop. marvel, which runs the two seats, and the
  supervisor, which casts them, are objects here, not co-owners.
- **Related:** aae-orc-8yag2 (the retrospector as a scheduled headless role);
  director message 01M5JYJK3X (a list of headless candidates, in flight);
  director message 01M5JYKF4Q (a scheduler evaluation); ADR-010 (headless
  roles use completion semantics); companion marvel idea, link to follow.

## The operator's words

> "kos idea marvel, director perhaps there is some way to leverage
> supervisor-as-loadbalancer to also route and decide when some agents should
> spin up as headless, and when they should be transitioned into
> standing/hot-standby agents to reduce the churn, maybe pair a reviewer and
> builder on a single PR to loop fast, to coordinate over nats directly?"

## The idea, in one sentence

For the life of one PR, let its builder and its reviewer address each other on
the bus directly, so a review round and its fix round pass seat to seat
instead of through a supervisor relay.

This file covers the pairing half. The other half, the supervisor deciding by
load whether a seat runs headless, stands hot, or stands, is the companion
marvel idea.

## What it skips

- **The supervisor relay hop.** Today a review result goes reviewer to
  supervisor to builder, and the fix round goes back the same way. Each hop
  waits on a seat that is doing other work.
- **The 30-minute stable-head hold.** As the loop runs now, a review waits for
  the PR head to sit unchanged for 30 minutes before it starts. A paired
  reviewer would learn of a new head from the builder's own message and could
  start at once.

## Risks

- **Review independence.** A reviewer that loops fast with one builder may
  start to share its framing. The pairing should not let the builder steer
  what the reviewer checks, and the reviewer should still read the diff, not
  the builder's account of it.
- **Run conditions.** A mutant list or repro command sent seat to seat skips
  the relay that today makes sure the four run conditions travel with it. The
  direct channel needs the same rule, enforced at the sender.
- **Who still gates ready and merge.** The pairing changes who carries
  messages, not who decides. Marking a draft ready and merging stay where they
  are now, and the supervisor still needs to see the loop's outcome, if not
  each round.

## Open questions

- What starts a pairing and what ends it: a cast-time field, a supervisor
  message, or the PR's own state?
- Does the supervisor get a copy of every round, a summary at the end, or only
  the stage changes (draft, ready)?
- What replaces the stable-head hold as the signal that a head is ready to
  review, and how does a reviewer tell a finished push from one in progress?
- How is a pairing that stalls (one seat gone quiet) noticed, and by whom?
