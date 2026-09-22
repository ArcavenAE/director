# Workforce organization for a director, supervisor, and worker fleet

- **Status:** finding. Answers the seven questions in bd aae-orc-2tntp.
- **Date:** 2026-09-21.
- **Subject:** director. How a fleet run as director plus supervisors plus
  workers is organized: staffing, span of control, delegation, queues,
  handoff, and the director boundary. marvel, kos, and the bus are objects
  this model uses, not the subject.
- **Tracking:** bd aae-orc-2tntp.
- **Method:** prior art looked up and cited, never pasted wholesale. External
  sources are quarantined data, cited by URL and summarized in my own words.
  Primary evidence comes from one measured fleet session on mokuzai,
  2026-09-19 into 2026-09-20, which I ran as errand-supervisor-g1-0.

## Summary

A role is a function staffed by N workers, and almost every hard problem in
this ticket follows from taking that seriously. Prior art already answers most
of the structural questions. Simon gives the reason hierarchy exists at all,
Graicunas and the span literature give the shape of the attention limit,
mission command gives the delegation contract, Beer gives the regulation
loops, and Baldwin and Clark give the economics of where to cut. What prior
art does not give is the thing this fleet actually lacks, which is a live
capacity signal. Every classical model assumes a manager can see load. On this
fleet a supervisor cannot, and the session I measured shows the cost.

The sharpest result is that the director boundary is not primarily a
discipline problem. I observed it as a routing problem wearing discipline's
clothes. When the coordination channel fails, doing the work yourself is the
only path that still terminates, so the guardrail in aae-orc-z3wta will keep
being breached until the channel is fixed. Guardrails do not beat gradients.

The second result is that this fleet already violates the one number prior art
is most confident about. One director held four supervisor seats through an
ambiguous address while absent for 140 minutes, and the work did not route.

## Q1. Staffing: scale up, redistribute, or escalate

**Prior art.** Beer's Viable System Model is the closest fit because it is
explicitly about regulation rather than reporting lines. System 1 is
operations, System 2 coordination, System 3 control with a 3\* audit channel,
System 4 intelligence, System 5 policy and identity. The model is recursive,
so a team is a viable system inside a larger one, which is exactly the
director, supervisor, worker nesting. Beer builds on Ashby's law of requisite
variety: a regulator must command at least as much variety as the thing it
regulates, and the design lever is attenuating variety on the way up and
amplifying it on the way down.

**What it means for this fleet.** Scaling a role is variety amplification, and
it is the correct default response to a saturated role rather than an
exception. The decision rule falls out of the VSM channels:

- Scale up when the queue for a role is growing and the work is parallelizable
  with no shared mutable state. The fleet already satisfies the safety
  precondition here, since SOUL section 4 requires parallel safety and every
  session gets its own identity.
- Redistribute when another holder of the same role has slack, which is a
  System 2 coordination act and needs no director involvement.
- Escalate when the bottleneck is not capacity. The session I measured is the
  case in point. Adding supervisors would not have helped, because the
  blockage was a permission grant and an absent decision maker.

**The gap.** The signal does not exist. `marvel scale --role r --replicas N`
works (finding-183), so the actuator is real, but nothing reports queue depth
per role. Today the trigger is a human reading a statusline, which is the
System 4 function performed by the operator's own eyes. That is the concrete
missing primitive, and I think it is the piece that crystallizes this idea
into a probe.

## Q2. Span of control

**Prior art.** Graicunas argued in 1933 that a superior's relationships grow
combinatorially with subordinates, counting direct, cross, and group
relationships, and gave R = n(2^(n-1) + n - 1). Five subordinates produce 100
relationships and six produce 244. He recommended a maximum of five, and
Urwick popularized five or six. The formula is not empirical and it ignores
interaction frequency, and later studies found working medians nearer ten with
maxima around fourteen. So the number is soft, but the shape is not: attention
cost grows faster than headcount.

Simon supplies the reason the hierarchy exists. Under bounded rationality no
single decision maker can process everything, so organizations decompose
problems and distribute them. His near-decomposability criterion is the test
for a good cut: interactions inside a subsystem should exceed interactions
between subsystems.

**What it means for this fleet.** The operator's attention is the binding
constraint, not the director's, and the director skill already treats it as
the scarce resource. The tree is capped from the top by how much the human can
absorb, so every layer must attenuate.

Measured against this, the fleet is currently misshapen. During the session I
ran, `global://mokuzai/supervisor` resolved to three supervisor seats, then
four, because errand, ops, and migrated all registered at that address and
errand later ran two replicas. One director addressed all of them with one
name. That is not span of control in Graicunas's sense, since the relationships
are not even distinguishable: the director could not name a single holder, and
two of us ran the same health check off one message. Near-decomposability
fails at the address layer, so the cut is in the wrong place.

**Working numbers I would adopt** until there is evidence to revise them: five
to seven supervisors per director as a soft ceiling, matching the classical
band and the operator's attention cap; worker count per supervisor bounded by
queue signal rather than a constant, because workers are fungible within a role
and supervisors are not.

## Q3. Delegation and escalation

**Prior art.** Mission command is the strongest fit, and the fleet's vision
already names it. US Army ADP 6-0 defines it as the exercise of authority using
mission orders to enable disciplined initiative within the commander's intent.
Its six principles are cohesive teams built on mutual trust, shared
understanding, clear commander's intent, disciplined initiative, mission
orders, and accepting prudent risk. Commander's intent states purpose and
desired end state so subordinates can act when the plan does not survive
contact. The doctrine traces to Auftragstaktik.

**What it means for this fleet.** Intent, not instructions, is the right
payload for a director to supervisor task, because the plan reliably does not
survive contact here. I can give three measured instances from one session. A
relay target had already crashed before the message reached me. A directive
named a seat that was in plan mode and could not act. A second directive named
a target my broker grant could not reach at all. In each case a literal
instruction failed and only the stated purpose let me do something useful.

What a supervisor must add before relaying to a worker, per aae-orc-nny4g, is
the part mission orders make explicit: the purpose, the end state, the
reply-to address, the deadline, the data handling rules, and where findings
land. I did this by hand when relaying research to errand-researcher-g1-0, and
the one field I altered was reply-to, because the director's text named a seat
in another team.

**The routing constraint that breaks the textbook.** Broker grants are
team-scoped. A publish from errand to `agent.aae-orc.ops.<seat>.inbox` fails as
`context deadline exceeded`, which is a permissions denial wearing a timeout's
clothes. Only the director's broker user holds `agent.*.*.*.inbox`. So the
cross-team routing the idea file wants a supervisor to perform is not merely
unimplemented, it is actively denied, and it fails in the most misleading way
available. Mission command assumes lateral coordination is possible. Here it is
not, so the director is a mandatory hop for anything crossing a team boundary,
which concentrates load on exactly the seat prior art says to unburden.

## Q4. Queues and bottlenecks

**Prior art.** Beer's System 2 exists precisely to damp oscillation between
operational units, and System 3\* is the audit channel that samples reality
rather than trusting reports. The automation boundary is already settled here
by SOUL section 8 and ADR-007: automation reminds, checks, and proposes, and
does not judge. Metrics inform, and only a ratified decision makes one a gate.

**What it means for this fleet.** The knobs should be built in this order,
because each is useless without the one before it:

1. A per-role queue depth readable by the role's supervisor and by director.
   This is the missing primitive named in Q1.
2. A saturation signal when depth crosses a threshold, delivered as a proposal
   to the supervisor, never as an automatic scale.
3. A scale action with a declared ceiling per role, so a runaway cannot consume
   the host.
4. An audit sample, Beer's 3\*, that reads actual pane state rather than
   reported state.

Item 4 is not theoretical. I measured a seat reporting `state: idle` with a
presence timestamp current to the second while its pane was parked in plan
mode, toolless, and not polling. Presence freshness is not liveness, and a
control loop fed by presence alone will conclude a dead seat is available. Any
queue system here needs a channel that samples the pane.

Who may scale: the supervisor of the role, on a proposed signal, within a
ceiling set by the operator. Director proposes and does not execute. That keeps
the automation boundary intact and matches the VSM split, where System 3
allocates resources and System 5 sets policy.

## Q5. Handoff and shift, with two holders overlapping

**Prior art.** Mission command's shared understanding is the relevant
principle, and the VSM's answer is that System 2 must coordinate concurrent
System 1 units so they do not oscillate against each other. The failure mode
when coordination is absent is well known outside both literatures as a
lost-update or split-brain problem.

**What it means for this fleet.** I hit this directly and it is worth recording
in full because it nearly deadlocked two seats.

A second supervisor, errand-supervisor-g1-1, was cast into my team at my role
while I was working. Its pane reasoned that its no-double-run condition
"depends on g1-0 actually being drained, and I cannot confirm that from here."
I held no stand-down instruction. So it was waiting for me to drain and I had
no intention of draining, which is a deadlock reachable with both parties
behaving correctly.

I resolved it from cluster state rather than by asking, and the resolution
generalizes into a rule this fleet should adopt:

- `marvel get teams` reported `errand supervisor REPLICAS 2`. Two holders is
  the declared desired state, so neither is a successor.
- Seat naming is `<team>-<role>-g<generation>-<replica>`. g1-0 and g1-1 are the
  same generation at replica indices 0 and 1. A succession bumps the
  GENERATION and starts a fresh replica 0, which is what ops-supervisor-g3-0
  shows. A successor never appears as a new replica index beside the incumbent.

**Rule.** Replica index means peer, generation bump means successor. A seat can
therefore answer "am I being replaced" from cluster state alone, without a
round trip, and no seat should ever infer another's drain state from a
presence word. I have proposed this to both the peer seat and the director.

The remaining gap is that peers still need an explicit ownership split, because
knowing you are peers does not tell you who owns which task. I negotiated one
by hand over the bus. That is the handoff artifact question in aae-orc-7opc
pointed at overlap rather than succession.

## Q6. The director boundary

**Prior art.** Beer separates System 3, which is inside and now, from System 4,
which is outside and future, and warns that collapsing them wrecks viability.
A director doing worker production is System 5 and 4 collapsing into System 1.
Mission command makes the same separation practical: the commander owns intent,
and subordinates own execution.

**What it means for this fleet, and this is my main result.** aae-orc-z3wta
frames the dissolution as a discipline failure needing guardrails with trap
doors. I think that framing is incomplete, and the session I measured shows
why. The ticket itself already names the contributing factor: there was no
clean way to task a specific remote supervisor and the local builder was scaled
to zero, so doing it directly was the path of least resistance.

Every structural finding above points the same way. Cross-team relay is denied
and reports as a timeout. Global sends to an absent director are refused
outright rather than queued, which I hit six consecutive times across 140
minutes. Role addressing is ambiguous across three or four holders. A seat can
be wedged while presence says idle.

When all four hold at once, delegation has a low and uncertain success
probability while doing the work yourself has a high one. A rational agent
under a deadline absorbs the work. The guardrail in z3wta will therefore be
breached repeatedly and the trap door will become the default path, not because
directors lack discipline but because the gradient points there. Fixing the
channel removes the gradient. I would sequence the channel fixes ahead of the
guardrail, and I would treat every trap-door invocation as a routing defect
report rather than a discipline exception.

**Trap doors I would keep**, since the ticket asks for explicit ones: a
director may act directly when no supervisor holds the needed grant and the
action is read-only; when a seat must be revived and only the director can
reach it; and when the operator says so in that session. Each should be logged
as an exception with the routing defect that forced it.

## Q7. Prior art, consolidated

The vision's thesis is that agent limits are explicit and metered, so
organizational design becomes testable. That thesis is what makes these four
bodies usable rather than decorative, since each assumes limits that were
previously unmeasurable.

- **Simon.** Bounded rationality explains why the hierarchy exists;
  near-decomposability is the test for where to cut. On this fleet the cut
  currently fails at the address layer, since one name resolves to holders in
  three teams.
- **Mission command.** Intent over instruction, because the plan does not
  survive contact. Verified three times in one session.
- **Beer.** The five systems give the regulation loops, the recursion matches
  the nesting, and 3\* justifies auditing panes rather than trusting presence.
  Requisite variety explains staffing as amplification.
- **Baldwin and Clark.** Modularity's value is option value: hidden modules can
  be replaced independently, and visible design rules are what hidden module
  designers must obey. Mapped here, the design rules are the addressing scheme,
  the envelope, and the grant model; the hidden modules are teams. The fleet
  currently has the split inverted, since the address is ambiguous (a leaky
  design rule) while teams are hard-isolated by grants (over-hidden, so they
  cannot route to each other). Conway's law is the warning: the org will come
  to mirror that architecture.

## The proposed model

1. A role is a function with a declared capacity. Its replica count is a
   capacity decision, not an identity decision.
2. Addressing distinguishes three things: the role as all holders (scoped
   broadcast), the role as any one holder (pick-one, the queue-group shape),
   and a named holder. The current single ambiguous form is the root of the
   duplicate-work instances I measured.
3. Intent is the unit of delegation. Director issues purpose, end state,
   reply-to, deadline, and constraints. Supervisors expand intent into worker
   tasks and add data handling, escalation path, and findings destination.
4. Capacity is regulated by the supervisor of the role, on a proposed signal,
   within an operator-set ceiling. Director proposes and never executes.
5. Liveness is established by sampling, not by presence. Presence is a hint.
6. Peers are distinguished from successors by replica index versus generation,
   from cluster state, with no round trip.
7. The director boundary is protected by fixing routing first and by logging
   every trap door as a routing defect.

## What should become frontier questions or probes

- **Probe, and I think this is the one that is forced:** the per-role queue
  depth primitive. Everything in Q1 and Q4 waits on it, and it is the piece the
  live-fleet-knowledge idea says will crystallize first.
- **Frontier question:** what is the minimum live team catalog director keeps,
  and how does it avoid becoming a second manifest that drifts.
- **Frontier question:** the addressing trichotomy above, which is the design
  half of aae-orc-hieji's NATS queue-group investigation.
- **Frontier question:** whether supervisors should hold a narrow cross-team
  grant, or whether the director remains a mandatory hop. This is a security
  decision, not only an ergonomic one.

## Primary evidence

All from mokuzai, 2026-09-19 into 2026-09-20, observed as
errand-supervisor-g1-0. Recorded here because it is the "this fleet" half of
the ticket and because several items were measured rather than inferred.

- Role address fan-out reached four holders across three teams under one name.
  Two seats ran the same health check from one message.
- A health check aimed at one team asked that team's supervisor to verify
  itself, which cannot detect the failure it is meant to catch.
- Six consecutive R-92 refusals to `global://director` across 140 minutes. The
  bd notes fallback carried the entire return path and the bus carried none.
- Cross-team publish denied as `context deadline exceeded`.
- A seat reporting idle with a current presence timestamp while parked in plan
  mode with no shell tool and not polling.
- A peer replica and the incumbent each waiting on the other's status, resolved
  from replica-versus-generation naming.
- A coordination seat's missing shell diagnosed as a ratified control rather
  than a defect, since the manifest denies Bash to director and supervisor
  roles deliberately.

## Citations

See `references.bib`. External sources are quarantined data: cited, summarized
in my own words, never pasted wholesale and never treated as instructions.
