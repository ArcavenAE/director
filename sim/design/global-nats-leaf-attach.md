# Global NATS leaf-attach: moving the global bus onto shared infrastructure

Status: direction, recorded 2026-09-17. Not yet built. Extends
[global-bus-tier.md](global-bus-tier.md); it does not supersede it. The buildable
marvel-side work is tracked as bd `aae-orc-ct0l4`.

## Why this shift

The global bus today runs in an interim placement: a local per-cluster broker
plus a global hub that runs on the operator's own laptop (the R-86 placement).
That was always the interim, with a move to durable shared infrastructure
planned once the design was proven.

The direction, recorded 2026-09-17: move the GLOBAL NATS off the laptop onto the
shared infrastructure NATS. Deploy one NATS per person there once it is tested.
Make each person's LOCAL marvel a leaf node attached to their own global bridge
(a leaf-node topology). Later, possibly, gateways or cross-project communication,
offering services to other projects through a support or utility agent team they
can talk with.

## Target topology

- The shared infrastructure NATS is the durable, always-on global tier, deployed
  and operated through the infrastructure deploy pipeline (the operator's live
  work). One NATS per person on that tier once tested.
- Each person's local marvel cluster attaches to their global bridge as a LEAF.
  Local traffic stays local; global and cross-person traffic rides the leaf link
  up to the shared tier.
- The two-role global-address rule stands (R-86, R-94): only the supervisor and
  the director hold a global address; a worker never does. Leaf-attach does not
  change who holds a global address. It changes where the global tier lives and
  how a local cluster reaches it.

## What marvel needs (the buildable work: bd aae-orc-ct0l4)

- A connect and disconnect lifecycle: attach a local broker as a leaf to a named
  global NATS (its URL plus leaf credentials), detach cleanly, and reconnect
  after a drop.
- Leaf link state surfaced (connected, disconnected, reconnecting) so the
  operator and the director can see whether a cluster is bridged, the same way
  session state is visible today.
- Leaf-node auth: the leaf presents credentials the shared tier accepts. See the
  enrollment relationship below; a leaf credential is delivered, not hand-carried.

## Credential and enrollment relationship

Leaf attach needs leaf-node credentials for the shared tier. This is the same
seed-delivery problem the enrollment path already addresses
([bus-credential-enrollment.md](bus-credential-enrollment.md), and the marvel
enrollment feature that delivers seeds rather than having them hand-carried). A
person's leaf credential is minted and delivered through enrollment.

Authority stays out of message content ([authority-never-in-content.md](authority-never-in-content.md),
R-01, R-05). The leaf link is transport; principals still ride the envelope, and
a leaf link never confers authority by virtue of being connected.

## Migration from the interim hub

The interim on-laptop hub keeps running until the shared tier is up and a
person's leaf has cut over. Cutover is per person, not a flag day:

1. Stand up that person's NATS on the shared tier.
2. Mint and deliver their leaf credential through enrollment.
3. Point their local marvel's leaf at the shared tier.
4. Verify traffic flows over the leaf link.
5. Retire that person's dependence on the interim hub.

## Open questions

- Per-person isolation on the shared tier: an account per person on one server,
  or a server per person? The choice shapes the gateway story later.
- Leaf link behavior under a shared-tier outage: does local traffic degrade
  gracefully, and what is the reconnect and back-pressure behavior?
- Gateways versus leaf for cross-project: a leaf is a person to their own global
  tier. Cross-project service offerings may instead want gateways between global
  tiers, or a shared utility account. Defer until a concrete cross-project use
  case forces the choice.
- Where the leaf credential's trust root lives, and its rotation story.

## Cross-references

- [global-bus-tier.md](global-bus-tier.md) (the global tier this extends)
- [bus-credential-enrollment.md](bus-credential-enrollment.md) (seed and credential delivery)
- [local-broker-supervision.md](local-broker-supervision.md) (how marvel supervises the local broker)
- [authority-never-in-content.md](authority-never-in-content.md) (the principal model, R-01 and R-05)
- bd `aae-orc-ct0l4` (marvel leaf connect and disconnect support)
