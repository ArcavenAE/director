# Party log: director NATS attachment topology (2026-09-17)

Subject: where the director attaches to the NATS fabric, given the design doc
`sim/design/global-nats-leaf-attach.md` (d41a292) shows a direct attachment to
the global tier while the common configuration is one director plus one local
marvel plus no global tier. Redaction held throughout: the durable global tier
is "the shared infrastructure NATS."

## Cast

Assembled for the commission. Each ran as a real agent that web-searched and
verified against the NATS documentation before positioning, favoring faster
models per persona.

- Nyx: NATS internals (client connection model, leaf nodes, gateways,
  superclusters, multiple connections, JetStream across the fabric). The
  load-bearing seat.
- Winston: systems architecture.
- Per: platform and operations engineering.
- Dr. Quinn: root-cause and failure analysis.
- Mara: site reliability and availability.

Five seats, not the full ten allowed; the topic converged and did not need more
voices to reach a defensible answer.

## Process

Round 1 was independent research: each seat self-informed from the NATS
documentation and returned verified facts plus an opening position. I ran the
clash and convergence as the orchestrator across the returns, since the
technical ground truth (Nyx) settled the one real disagreement rather than
leaving it to negotiation. Web verification was the rule, not the exception:
every load-bearing claim in the recommendation carries a documentation source.

## Where the room agreed at once

Four of five opened on option 4, local-only-with-leaf-bridge, and the fifth
(Nyx) confirmed it on internals grounds. The reasoning was the same from three
different directions:

- Architecture (Winston): the client attaches where the peer it must not lose is
  closest; the director's supervisor sits on the local broker; the leaf carries
  global reach for free, so a second connection earns nothing.
- Availability (Mara): a home-internet blip is the most likely outage; option 4
  keeps no wide-area hop in the director-to-local-supervisor path, and the
  adaptivity lives in the local broker's leaf config, not the director's code.
- Operations (Per): a local nats patch bounces one connection into RECONNECTING
  with a bounded in-order buffer and automatic resubscribe; set MaxReconnects to
  unlimited so the client waits out a patch. Global-only routes local
  coordination through the tier's uptime, which is the operator's first failure
  framing made structural.

Global-only was disqualified by every seat: nothing to dial in the modal
no-global case, and the wrong link in the global-present case. Migrate was
rejected by every seat: a client cannot move while attached, so every move is a
disconnect and a full resubscribe, with a cutover window that drops core-NATS
messages and a flap risk on a local restart.

## The one real clash: tandem

The disagreement was whether tandem (two simultaneous connections) is a
reserved-for-need fallback or ruled out.

- Winston reserved it for an explicit service-level need: independent reach to
  remote clusters when the local leaf is partitioned.
- Dr. Quinn ruled it out on risk: any subject overlap between two live
  connections is a duplicate-delivery pattern in NATS's own leaf and cluster
  issue history, so it needs a dedup cache and a presence-owner rule to be
  correct in the steady state, not only under fault.
- Per and Mara: it buys nothing the leaf does not already provide, at the cost
  of a second failing connection and a rule for which connection is
  authoritative when they disagree.

Nyx settled it with the internals. Core NATS delivers one copy per active
subscription and has no client-identity concept that would collapse two
subscriptions into one delivery. So two connections subscribing the same subject
(which a leaf link will bridge) receive two deliveries on every publish,
continuously, not only during a fault. The only native fix is a shared queue
group both connections opt into, which adds its own bookkeeping. That makes
tandem's cost structural, not a fault-window tax, and turns Winston's reservation
into an explicit, scoped exception for later rather than a standing default. The
room accepted this: tandem is inadvisable as a default; if a future requirement
ever needs independent remote reach during a local-leaf partition, open a
bounded second connection whose subject interest never overlaps the local one,
and tear it down after.

## Convergence

Unanimous: option 4, local-only-with-leaf-bridge, as the single default for both
the no-global and the global-present configurations. The director attaches to
the broker on its own host and nothing else; the leaf carries global and
cross-cluster traffic; the director's connection code is identical across every
deployment shape. The full recommendation, with the failure block diagram,
sequence views, transaction views, the client configuration, and the design-doc
revisions it implies, is in `recommendation.md`.

## The four questions, answered

1. Tandem local and global at once? No, not as a default. Structural duplicate
   delivery; reserve a scoped, bounded second connection for a future explicit
   need only.
2. Migrate the attachment point? No. A move is a disconnect and full
   resubscribe with a lossy cutover window; the leaf already gives what migrate
   chases.
3. What does connecting to the global tier buy, against the two failure
   framings? Directly attaching the director to the tier buys nothing the leaf
   does not already carry, and it puts the wide-area link in the path to the
   director's own local cluster, which is the first failure framing. Option 4
   keeps global reach (through the leaf) while removing the wide-area link from
   the local path. The second framing (a local restart cutting remote reach) is
   handled by unlimited client reconnect plus the leaf's own independent
   reconnect; the local restart bounces one connection cleanly rather than
   stranding the director.
4. Can a client be at two points at once? A client attaches to one server at a
   time (server-pool failover at connect and reconnect). "Two points" is only
   two separate connections (tandem, with the duplicate cost above) or
   server-side fabric routing from one connection (which is option 4). The
   client cannot move while staying attached.

## Sources cited by the panel

NATS documentation on leaf nodes, resilient clients and reconnection, connection
lifecycle, request-reply resilience, publish-subscribe, the leafnode protocol,
JetStream on leaf nodes, JetStream consumers, gateways and superclusters, and
the nats.go package reference. The nats-server issue and discussion history on
duplicate delivery across leaf and cluster paths (issues 3191, 1722, 2694, 5473,
discussion 4823) grounded the tandem ruling. Every load-bearing fact in the
recommendation is anchored to one of these.
