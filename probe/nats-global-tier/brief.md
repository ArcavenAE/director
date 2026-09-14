# Probe brief: the global bus tier (R-86)

Status: brief, 2026-09-14. Design artifact: `sim/design/global-bus-tier.md`.
Commissioned by the director seat under the operator's forward program
(connect a local director to marvel on another host). Subject: director owns
the global tier's definition; marvel owns the managed-NATS mechanics (R-85),
cross-linked from `marvel/_kos/ideas/identity-at-launch-and-managed-nats.md`.

## The gap

A local director cannot see or reach a remote supervisor. Each host runs its
own local broker with no wire between them; the phase-0 shim knows one broker
URL and one workspace. R-86 ratifies the shape of the answer (one local NATS
per marvel cluster for team traffic, events, and heartbeats; one global NATS
for the director-supervisor channel across hosts) and leaves the how open.
This probe answers the how. The topology is not re-litigated here.

## Question

How is the global tier stood up, partitioned, secured, and operated so that a
director on one host reaches a supervisor on another, with the credential
bound to the subjects it may use (R-77), a loud failure on an unauthorized or
undeliverable subject (R-09, R-92), and no consumer credential crossing the
principal boundary (SOUL section 3)?

## Hypothesis

Leaf nodes are enough: each cluster's local broker connects up to one global
broker under a cluster-scoped credential, the hub binds that credential to the
cluster's subject prefix, JetStream stays per domain (one local domain per
cluster, one hub domain for the global channel), and a supervisor session
keeps a single connection to its local broker while its global traffic rides
the leaf link. No gateway or supercluster, no shared cluster, no second
connection in the shim.

## Sub-questions (each gets a recommendation in the design artifact)

1. Mechanism: leaf nodes, gateways, or accounts on one shared cluster.
2. Partition: what crosses the global bus and what stays local; the subject
   grammar for both.
3. Placement and operation: host, TLS, discovery. Options for the operator to
   ratify; the probe does not pick the host.
4. Credential-to-subject binding across hosts: how director#4's authorization
   block extends to the leaf link and the hub.
5. The multi-user boundary: shared transport, separate identity; who may
   write which subjects across the principal boundary.
6. Envelope and direction anchors: where A2A v1.0, NATS-as-transport, SLIM
   (watched), and Matrix (under examination) bear; none reopened.
7. marvel's role: the local broker and the hub as supervised workloads, the
   config and credential seams, bring-up.

## Method

Design first, with the NATS documentation for versions 2.10 through 2.12
verified per claim, then the smallest test that can fail: three throwaway
brokers on one host (a hub and two leaves, each with its own JetStream
domain), two shims dual-cast as a director and a supervisor on different
leaves, and the checks below. The live phase-0 broker on :4222 is not
touched, the same isolation director#4's verify-auth.sh used. The second
stage moves one leaf to another host, the case the twin work is staging
(brief 7 stage 4).

## Success signal (the R-86 MVP)

1. A director REQUEST published on leaf A reaches a supervisor consuming on
   leaf B through the hub, and the supervisor's AGREE comes back with
   `in_reply_to` set; both messages are in the hub's global stream.
2. A worker credential on leaf B publishing a global subject is refused by
   its local broker (loud, before the leaf link), and leaf B's cluster
   credential publishing another cluster's prefix is refused by the hub.
3. A send to a cluster with no live global presence refuses before publish
   (R-92 zero match) and writes nothing to the stream.
4. With the hub down, local traffic on both leaves continues and a global
   send refuses loudly within one presence TTL; with the hub back, the
   stream and the global presence bucket are intact and the link resumes
   without a shim restart.
5. Nothing in the run holds a credential that is bearer authority at a third
   party (ADR-009); the only secrets are hub-minted bus credentials.

## Timebox

Design and plan: this sitting. MVP on one host: one sitting after the
tickets are taken. Cross-host stage: gated on the operator's placement
ruling and skippy's leaf on his host.

## Deliverables

- This brief.
- `sim/design/global-bus-tier.md` (the design, recommendations per
  sub-question, candidate requirements marked for the operator).
- bd tickets for the near-term build, flat with depends-on edges, labeled
  aae-orc, director, marvel, source:session; ids recorded in the design
  artifact's closing section.

## Closing note (2026-09-14)

Design landed as `sim/design/global-bus-tier.md`; the hub is running on kinu
(4242 client, 7442 leaf, 8242 loopback monitor, domain `global`) and
`verify-global.sh` passes 8 of 8. Tickets, flat with depends-on edges, labels
aae-orc, director, marvel, source:session:

- aae-orc-gvf6k (P1): shim global mode (marvel-builder, after director#27).
- aae-orc-bxg5f (P1): relaunch the phase-0 broker as the kinu leaf, coordinated.
- aae-orc-av2v1 (P1, blocked by gvf6k and bxg5f): cross-host stage, mokuzai
  joins, the director reaches skippy's supervisor.
- aae-orc-qu88n (P2): the hub as a service on kinu (launchd, provisioning).
- aae-orc-e9g8i (P2): marvel bus section on Cluster and conf rendering.
- aae-orc-z37ux (P2): marvel validates Cluster.Name as a subject token.
- aae-orc-xy1dh (P3, blocked by e9g8i): marvel supervises the local broker as
  a non-agent workload; leaf-link state on the events ring.
- aae-orc-b0fzk (P3, blocked by qu88n): cloud phase, TLS, CA, DNS, placement.

Candidate requirements R-94 and R-95 (design section 10) and the interim LAN
posture (section 9) are the operator's to ratify.
