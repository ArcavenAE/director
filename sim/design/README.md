# Design briefs (candidates, not specs)

These four briefs came out of the 2026-09-12 identity roundtable, after a live
incident: several Claude Code sessions on one host loaded the same local-scope
MCP config and all registered as `agent://ops/operator` (the OS user), so two
real sessions collided on one address and one presence key.

They are candidate designs for further development and probes, not firm specs.
`sim/specs/` is for requirements firm enough to constrain the software; these
are a level below that. Each brief develops a mechanism, runs an adversarial
pass, and proposes candidate requirements (provisional tags), which are
harvested into `../requirements.md` section I (R-49 onward) with their source
classes. Where a brief's conclusion is JUDGMENT rather than OBSERVED, it wants
a probe before it hardens.

- `identity-at-spawn.md` (ID-A..E): who assigns a session its address, and when
  cryptographic identity (A2A DID plus JWS-signed AgentCard) becomes necessary.
- `director-seat-lease.md` (SEAT-A..G): the human-director seat as a fenced KV
  lease on the NATS primitives the Phase 0 probe already runs.
- `continuous-custody-succession.md` (CUST-A..H): custody externalized
  continuously so an involuntary exit loses nothing; graceful and ungraceful
  succession converge on the same durable state.
- `authority-never-in-content.md` (INJ-A..C): why authority rides an
  out-of-band channel and never message or document content, and the R-07
  graduation.
- `shim-timer-heartbeat.md` (BEAT-A..F): the R-56 build plan; one shim timer
  serving presence and seat, observed state separated from declared state,
  the shutdown edge, failure accounting, and a watcher for shimless sessions.
- `marvel-twin-manifest-and-cutover.md`: the operator's stand-up-alongside
  program; the target manifest mapping the nine fleet functions to marvel roles
  and wardrobe casts, the FORWARD identity block (R-84), and the cutover
  criteria as checks (R-49/R-84, R-08/R-09, R-56, R-42, R-60/R-61) plus the
  cross-host trial stage (R-86, R-77).
- `global-bus-tier.md` (brief 8): the R-86 global tier as built, leaf nodes
  with one hub domain, the subject partition, the per-cluster NKey binding
  (R-77) proven with two scratch leaves, the interim kinu placement and its
  LAN posture, marvel's build items (R-85), and the candidate requirements
  R-94 and R-95. Probe brief and artifacts in `probe/nats-global-tier/`.
- `bus-credential-enrollment.md` (brief 9, approved 2026-09-14): marvel and
  marvel keys as the enrollment and distribution plane for the global bus
  credential; the flow, the transient `Credential` resource, the custody
  argument, the multi-user boundary, and the seams for marvel-builder.
- `local-broker-supervision.md` (brief 10, shape for review 2026-09-15): the
  local nats-server as marvel's first supervised non-agent workload, ruled
  as a daemon-owned child process; the `bus` section on Cluster, the
  rendered conf and authorization file, the R-93 spawn hold, leaf state on
  the events ring, S6's seed path, and the ordered gap list to a functional
  cross-host service. Candidate R-96.
- `leaf-fabric-one-address-space.md` (brief 11, candidate for review
  2026-09-24): one fleet address per seat over the leaf fabric; mail stored on
  the recipient's cluster; per-cluster outbox sourced by the remote inbox so a
  link outage delays and loses nothing; hierarchy by per-seat credential; no
  silent expiry; seat-keyed presence; a notifier for idle seats; a flag-day
  cutover on the ruled `agent.<cluster>.` root, with rollback, and a
  two-cluster probe (finding-008). Amends R-94, R-95, R-50, and R-109 (ruled
  2026-09-24). Candidate FAB-A to FAB-G.
- `asks-die-unread.md` (design for review 2026-10-04): one class, three
  paths by which an ask dies unread (expiry, mail orphaned on a dead
  instance id, a successor reporting live before reading); inherited mail
  from `DIRECTOR_PREDECESSOR`, a `catching-up` presence state, and FAB-C's
  expiry notice on today's stream until brief 11's cutover. Issues #220,
  #222; R-130, R-143, R-144, R-178.
- `ask-ledger.md` (design for review 2026-10-06): one row per REQUEST,
  derived read-only from bus traffic, with states from existing
  performatives plus an optional status message, an unacked alarm, a
  blocked-on chain, and a per-role rollup that marvel's QUEUE column and
  the board's W5 read. Says how it differs from #168. Issue #246; R-08,
  R-105, R-110, R-115, R-117, R-183.
- `global-per-seat-inbox.md` (design for review 2026-10-07): the operator's
  ruling B on two supervisor-role seats sharing one global address, delivered
  on brief 11's fabric. A seat's fabric role is its cast role, a role address
  is a one-taker queue, the legacy cluster-wide supervisor alias refuses when
  ambiguous, the shim change against `global.go`, proposed R-94 text, and the
  cutover with a named legacy reader and the forwarding seat's end condition.
  Candidate GSI-A to GSI-C.
- `director-functions-redesign.md` (design for review 2026-10-09): the
  operations coordination plan's part 2. An inventory of 19 director
  functions, four designs that keep all of them, and the panel's
  recommendation D (C's stores and reconciler on the existing `dws` fold,
  A's presentation check, B's mirror-or-proposal tags and UNREAD rule, the
  Desk as the store of asks). The decisions for the operator, with votes
  and dissent; R-153 has no enforcing code today; 20 candidate tickets, not
  filed. Candidate DFR-A to DFR-C.
