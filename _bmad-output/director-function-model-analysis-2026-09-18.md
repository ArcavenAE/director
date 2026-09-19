# Director as a function, not a single seat: analysis

Status: ANALYSIS, held for the operator. It widens the seat question and corrects an
assumption before any exclusivity mechanism is built; it does not resolve every question, per
the operator's instruction. Date: 2026-09-18. Redaction held: no origin organization, its
short environment tokens, its infrastructure repository, or the deployed hub hostname. Feeds
the director requirements register.

Scope, from the operator: the SEAT model only, decoupled from identity. ID-A (identity at
spawn) and writer-side deregister are greenlit and building now; they are identity and
liveness, not the seat model, and this analysis leaves them alone.

## The assumption mistake, named plainly

The party carried a fenced single-holder seat lease forward as decided (item 2 of the crossing
build). The design brief it rests on states the premise outright: SEAT-A, "exactly one session
holds it at a time." That premise drove straight to a technical mechanism (create-only KV,
fencing token, fail-closed) before the use cases were understood. The mistake is not the
mechanism; the mechanism is sound engineering. The mistake is that "exactly one holder" was
assumed, not derived, and it conflates the director FUNCTION with a singular identity. The
lease answered "how do we stop two directors" before anyone established that two directors is a
conflict to prevent rather than a valid case to support. The fence was built on an unexamined
premise, and the operator is right to pull it back.

I own this correction: the party (me) drove to the immediate technical answer and did not first
ask whether single-holder was even the right model. That is the assumption mistake to fix.

## What survives the correction, and what needs rework

Survives unchanged, do not re-litigate:

- ID-A (identity at spawn) and writer-side deregister: identity and liveness, greenlit.
- Authority-never-in-content (INJ-A..C): director-ness is a property established out of band,
  never asserted by a string. This is the FOUNDATION of the function model, and the reframe
  strengthens it rather than weakening it.
- Continuous-custody-succession (CUST-A..H): custody externalized continuously, holder-
  agnostic. It fits a multi-holder function better than it fit the single seat, because it
  never wrote custody AT a handoff to begin with.

Needs rework:

- The seat as a single-holder lease AS THE MODEL for director. The lease is demoted from "the
  director definition" to "a per-capability tool," below.

## The reframe: director is a function that participants hold

- "Director" names a FUNCTION: a bundle of capabilities on the bus (coordinate sessions, hold
  the human's intent, route asks, carry the authority to direct).
- A participant HOLDS the function through a grant established out of band (the
  authority-never-in-content principle), not by occupying an address and not by a string
  asserting it.
- MORE THAN ONE participant may hold the function at once. Multi-holding is the default;
  exclusivity is a per-capability policy, not the model.
- Address (where to reach a participant), function or role held (what it may do), and durable
  identity (who it is across restarts) stay three separate things (ID-D). The single seat
  fused the function with one identity and one address; the reframe keeps them apart.

This is not a new invention. It is session-identity Thread 2 Option C, "authority on the
envelope, not the seat; occupying the address confers nothing," which the party wrongly
deprioritized as "the strongest option and the furthest." The operator's push promotes Option C
from furthest to the model, and extends it: the capability a participant holds can be the full
director function or a restricted subset.

## Grounding in the five use cases

Each case is one the single-holder seat cannot express and the function model handles.

1. **Two directors online at once, as a valid case.** Concrete shapes: the human's laptop
   director session AND a mobile remote-control director acting for the same human (the
   vision's Mode B keeps the director reachable from a phone while it keeps operating); a
   director and an incoming successor overlapping during a graceful handoff; a per-workspace or
   per-team director when several teams run at once. The single-holder lease REFUSES or queues
   the second (SEAT-A, "a second claimant is refused or queued"). The function model: both hold
   the director function; a per-capability exclusivity policy applies only to the specific
   capabilities that need one live writer.
2. **Two human directors.** Two humans each directing (collaboration; the vision's Mode D
   cross-person shape). A seat keyed to one `holder_agent_id` cannot represent two principals.
   The function model: each human's director session holds the function, scoped to that human
   as the principal behind it; authority is per-principal, verified per message.
3. **Shift-change deeper than acquire-not-inherit.** The terminal-marker handoff assumes one
   holder passing to one successor across a vacancy or a knife-edge cutover. The function model
   allows OVERLAP: the successor holds the function alongside the predecessor, drains the
   in-flight, and the predecessor releases, with no vacancy and no single-holder race.
   Continuous-custody makes this safe because custody is externalized, so a co-holder or a
   successor reads the same custody store; nothing is lost at the handoff because nothing is
   written at the handoff.
4. **Name versus function.** Director is FUNCTIONALITY, not identity. The lease keyed to
   `holder_agent_id` conflates them. Correct model: director is a capability grant; the
   participant's identity (its ID-A name) is separate from the function it holds; the function
   is verified per message from the grant (the envelope authority block now, a signed grant
   later), never from occupying a key. This is Option C, made the model.
5. **Lesser shapes: restricted director-like roles.** User, analyst, auditor, assistant, and
   others, participating on the bus with REDUCED authority. This is a role and capability
   TAXONOMY, not a single seat: a set of capabilities; roles are named bundles; a restricted
   role holds a SUBSET of the director's capabilities. A participant holds a role through a
   grant; multiple participants may hold the same role; authority is verified per message
   against the capabilities the role carries. The single seat cannot express this; the
   capability taxonomy subsumes the lesser roles cleanly and is extensible.

## Where mutual exclusion (a lease) still belongs

The fenced lease is wrong as THE model and right as a PRIMITIVE. Some individual capabilities
are genuinely exclusive: a coordination lock where exactly one holder must act at a moment (who
writes the canonical board, who holds the commit pen for a specific resource, who is the single
authority for a specific decision at a specific time). Those are per-capability leases on that
one capability, not on "being the director." So the good engineering (fencing, fail-closed,
the shim-driven renewal of SEAT-D) survives as a tool applied per capability, chosen by policy,
where one live writer is required. It stops being the definition of director. This keeps the
mechanism available exactly where it earns its place, without the single-seat premise.

## The model, stated

- **Capabilities:** the atomic grants on the bus (direct, route-asks, hold-intent, write-board,
  audit, analyze, assist, observe-read-only, and so on).
- **Roles:** named bundles of capabilities. Director is the fullest bundle; analyst, auditor,
  assistant, and user are restricted bundles (subsets). The set of roles is a taxonomy,
  extensible, and it should be reconciled with the wardrobe role library and the nine fleet
  functions rather than invented fresh.
- **Participants:** sessions (with ID-A identities) that HOLD one or more roles through grants
  established out of band.
- **Multi-holding:** the default. Two participants may hold the same role. Exclusivity is a
  per-capability policy (a lease), applied only where one live writer is required.
- **Authority:** verified per message from the grant (authority-never-in-content), never from
  occupying an address and never asserted by content. The envelope carries the role or
  capability being asserted; the receiver verifies it against the grant.
- **Custody:** externalized continuously (continuous-custody-succession), holder-agnostic, so
  overlap, co-holding, and ungraceful loss all read the same durable state.

## R-94, sharpened by a live test (finding-179, aae-orc#361)

skippy's P2.2 test made the authority axis concrete: R-94 is not enforced at the broker. Broker
grants are per-TEAM (one broker user per applied team), so inside a team, role is not a security
boundary. A worker, with just the team password and no shim, read the supervisor's global inbox
and published to the director's inbox. This confirms, rather than contradicts, the grant spec:
`sender.role` is a label trusted by the boundary, not a broker-enforced attestation, and the
finding is the concrete instance of that stated limit.

Two distinctions the finding forces into the model, and my read on each:

- **Holding a global address is not the same as participating.** R-94, "a worker never holds a
  global address," governs being an ADDRESSABLE global-tier role (an inbox others send to, a
  global presence row), not whether a session may SEND. In the function model, PARTICIPATE
  (send and receive) is the base capability; holding the director or supervisor ROLE (a global
  address, direction authority) is the authority-axis capability a worker does not hold. So a
  worker is not a global-tier role, and R-94 should say that precisely: it excludes holding a
  global ADDRESS, not the act of sending.

- **A worker publishing to `global.director.inbox` is not the intended path (my read), with one
  operator intent flag.** R-86 makes the global tier the director-supervisor channel; workers
  live on the local tier. The intended upward path is worker to supervisor (local), supervisor
  to director (global), so a worker on the global tier at all is outside the two-tier model, and
  the finding's direct worker-to-director publish is the leakage the missing enforcement allowed,
  not a supported report path. The one thing I flag rather than decide: whether the operator
  wants a direct worker-to-director ESCALATION escape hatch (bypassing the supervisor for an
  urgent case). That is an operator intent call, not a mechanism one, and I frame it against
  skippy's recommended shape (#355 harvest) so the choice is not open-ended:

  - **Default: two-tier.** Worker to supervisor (local), supervisor to director (global). This is
    R-86's model and stays the norm.
  - **The escape hatch: keep the direct worker-to-director path OPEN, but rare and audited, for
    the dead-supervisor escalation-of-last-resort case only.** This is skippy's recommendation:
    not a routine second path, an emergency valve for when the supervisor is not live to relay.

  So the operator's call is keep-open-but-constrained versus close, not keep-versus-close in the
  abstract. If kept open, "rare and audited" is not free; it has two concrete requirements the
  operator should price into the decision:

  - **An audit trail.** A direct worker-to-director publish is logged as an escalation event
    (who, when, why-bypassed), so a bypass is visible and reviewable rather than silent. Without
    it, "rare" is unmeasurable and the path degrades into an ordinary second channel.
  - **A supervisor-liveness precondition.** The bypass is legitimate only when the supervisor is
    not live. The R-92 liveness check already exists at the global tier (a global address with no
    live presence row refuses before publish); the same liveness signal is the natural gate, so a
    direct-to-director escalation is admitted only when the worker's supervisor has no live
    presence row, and refused otherwise. That turns "bypass the supervisor" from a worker's
    discretion into a mechanically-checkable last resort.

  If closed instead, a worker with an urgent case and a dead supervisor has no upward path until
  succession restores the supervisor, which is the cost of closing that the operator weighs
  against the leakage risk of keeping it open. Either way the enforcement axis (subscribe-narrow,
  per-role users) below is unchanged; the escape hatch is a PUBLISH-side policy, and PUBLISH stays
  open in the physical-access phase regardless, so today the hatch is trusted-not-enforced like
  the rest of R-94, and its audit-trail and liveness-gate become enforceable at the same tier-2
  step that makes role a broker boundary.

- **Subscription breadth is the read-leakage vector, and it should narrow.** The worker read the
  supervisor's inbox by subscribing `global.<cluster>.>`, the whole cluster subtree. A session
  should subscribe to its OWN inbox subject only (the shim's `inboxSubject()` already computes
  it: `global.director.inbox` or `global.<cluster>.supervisor.inbox`). The wildcard is the read
  leak; narrow the broker grant to the role's own inbox for subscribe. Publishing to another
  role's inbox stays open (send is like mail, you can send to anyone; you read only your own
  box), which is what keeps legitimate upward and lateral reporting working.

So the enforcement is asymmetric and proportionate, and it has two tiers (marvel-builder's code
read, `declared.go:206-218`: the broker grant unit is the TEAM, one user per team shared by
every role). Tier 1, greenlit and held for the operator's go: narrow the shared team user's
SUBSCRIBE from the cluster-global wildcard to the role's own inbox, which reduces the team user's
global read to just that inbox (a blast-radius reduction). It does not separate a worker from the
supervisor within the team, because they share the team user, and it does not touch PUBLISH.
Tier 2, deferred: per-role broker users, which is the only thing that makes R-94 ENFORCED rather
than advisory inside a team; it returns when a role needs enforcement or the trust boundary
leaves the host. PUBLISH stays open in both tiers now (send is not the boundary; a worker or raw
publish to the director inbox is caught receiver-side by envelope validation and
authority-never-in-content). The R-94 requirement gains: trusted-not-enforced in the
physical-access phase, with subscribe-narrowing as the interim blast-radius reduction and
per-role users as the deferred enforcement.

## Prior work this builds on (not started cold)

- The functional breakdown: the nine fleet functions in
  `sim/design/marvel-twin-manifest-and-cutover.md` (director, supervisor, the builder
  instances, reviewer, and the rest) are the existing functional decomposition; the role
  taxonomy should extend that, not replace it. The requirements register (`sim/requirements.md`)
  is the other half of the "FBD and other questions" prior work, carrying the open questions
  per requirement.
- Session-identity Thread 2 Option C (`session-identity-and-succession-options.md`): authority
  on the envelope, occupying the address confers nothing. Promoted here from furthest to the
  model.
- `authority-never-in-content.md` (INJ-A..C) and the A2A model: director-ness is out of band;
  the capability grant is the out-of-band property.
- `continuous-custody-succession.md` (CUST-A..H): holder-agnostic custody, which the function
  model needs and the single seat did not require.
- The diagram form exists as the NATS topology party's failure block diagram plus sequence and
  transaction views (`_bmad-output/nats-topology-party-2026-09-17/`); a function block diagram
  of director-as-a-function should be built in that same shape over the nine functions.

## What this feeds into director requirements

- RETRACT the single-holder premise as the model: SEAT-A ("exactly one session holds it") is
  withdrawn as the director definition. Reclassify the fenced lease (SEAT-B/D/E/F/G) as a
  per-capability exclusivity tool, available where a capability needs one live writer.
- ADOPT the capability and role taxonomy as the director model: director is a role (a
  capability bundle) held by participants, multi-holding by default; lesser roles are
  restricted bundles; authority is per message from a grant (Thread 2 Option C,
  authority-never-in-content).
- KEEP unchanged and greenlit: ID-A, writer-side deregister, authority-never-in-content,
  continuous-custody.

## Open questions surfaced (the operator said we need not resolve all)

- Capability granularity: the atomic capability set, and how fine. Too fine is unusable; too
  coarse loses the lesser roles.
- Which capabilities genuinely need per-capability exclusivity (a lease), and which are safely
  multi-held.
- How a grant is minted and represented in the physical-access phase (a launcher-minted role
  grant) versus the crypto phase (a signed capability grant, A2A).
- Two human directors: is authority partitioned by workspace, team, or principal, or is there a
  shared authority both hold, and how do their directions reconcile if they conflict.
- The role taxonomy's initial membership (director, supervisor, analyst, auditor, assistant,
  user, observer, and so on) and its alignment with the wardrobe role library and the nine
  fleet functions.
- Relationship to ID-A and the cluster label: the participant's address (ID-A) and the role it
  holds are separate; confirm the grant references the participant identity, not the cluster
  label.
- Does this change the crossing enable? The crossing needs a participant to HOLD the director
  function on the global tier, which is a grant, not a single-seat acquisition. The party's
  minimal crossing unblock becomes ID-A plus hold-the-director-function-via-grant plus
  writer-side deregister; the single-seat lease acquisition leaves the critical path. This is
  the one place the reframe touches the crossing directly and is worth confirming early.

## What I am not doing, per the operator

Not designing the full capability taxonomy and not resolving every question. Not building any
single-seat mechanism. The single deliverable is the widened analysis and the corrected
assumption, so the seat model is re-derived as a function and capability model before any
exclusivity mechanism is built.
