---
title: "Recommended director changes for supervisor development (candidate register entries)"
author: Michael Pursifull
date: 2026-09-23
bibliography: references.bib
csl: chicago-author-date.csl
---

# Recommended director changes for supervisor development

RESEARCH ONLY. This document proposes candidate register entries and coaching
patterns for director, drawn from the supervisor-development synthesis
(`synthesis-supervisor-development.md`). Nothing here edits the live
`sim/requirements.md`. Every item below is a candidate for operator and
director analysis, marked as such; a candidate becomes a real R-number only by
the operator's decision, with a source class and an earned-by line held to the
register's own admission test (an entry with no observed instance and no
operator ruling behind it does not belong).

## Why this exists

The register already records what the supervisor does wrong; it does not yet
record what director must do to develop a supervisor that does it right. The
synthesis maps tested human-organization levers onto those recorded failures.
This document turns the mapping into the register's own shape, so the operator
can accept, reject, or reshape each lever as a discrete entry rather than as a
paragraph of intent. The candidates cluster under the three surfaces the study
was asked to strengthen: status solicitation, escalation channels, and
decision-rights support. A fourth short cluster proposes coaching patterns,
because developing the supervisor is itself a director function (the synthesis
Q1 finding) and the register has no home for it today.

The numbering starts at R-111 because R-110 is the current tail; a candidate's
number is provisional and is assigned for real only at acceptance. Where a
candidate refines an existing entry, it cross-references by number rather than
restating.

---

## A. Status solicitation (candidates)

**Proposed R-111 (candidate) · director solicits status on a fixed beat that
tightens with worker readiness, and treats a missed beat as a signal, not as
default-fine.** A supervisor should push status upward on a predictable cadence
against the declared plan [@prince2_exception], with the cadence tightening for
new or low-readiness workers [@hersey1969management]. Director's role is to set
and hold that beat per team and to treat a skipped beat as an unknown state to
resolve, never as evidence of health. This is the solicitation counterpart to
R-107 (a starved consumer is loud, never "silence, not failure") and R-89 (a
silently denied wake): the poll that returns nothing must distinguish
no-message from no-consumer, and director must not read a quiet beat as a green
one.
*Would be earned by: the R-107 starvation instance (finding-007 / director#66)
and the R-89 wake denial, read from the solicitation side.*
*Candidate source class: OBSERVED basis (R-107, R-89); the cadence rule is
JUDGMENT drawn from workplace practice.*

**Proposed R-112 (candidate) · director never accepts a bare status color; a
status claim is bound to a receiver-produced, provenance-stamped
liveness-plus-progress signal.** The failure to design against is watermelon
reporting, green outside and red inside, caused by a culture where amber draws
blame rather than help [@cultivated_watermelon]. The fix is to anchor status to
observable artifacts and a monotonically advancing work counter that cannot be
hand-adjusted, produced by the receiver after processing, not inferred from
plumbing. This makes R-14 (liveness is end-to-end), R-16 (status is not
liveness), and R-93 (attachment is asserted, never inferred from a live pane)
into a director-behavior contract: director asks for the evidence, not the
mood, and stamps every status with when and by whom it was checked so it can
tell NOT-CHECKED from CHECKED-AND-FINE.
*Would be earned by: R-14, R-16, R-93 (the nine-sessions-running-on-a-bare-broker
instance); O-14 (the board is an authored backdrop that decays).*
*Candidate source class: OBSERVED basis; the no-bare-color rule is JUDGMENT.*

**Proposed R-113 (candidate) · the first status event director requires of a
new task is a confirm-back, not a progress report.** The subordinate restates
what it thinks it was asked and how it will approach it, before compute is
spent [@call1998rehearsals]. This catches a misread task cheaply and is the
reporting analog of checking intent before acting. It bears directly on O-8
(the supervisor resolved an ambiguous "proceed" against a policy boundary with
no human in between): a required confirm-back would have surfaced the
production boundary as a question before any action.
*Would be earned by: O-8 (ambiguity resolved as authority); the workplace
confirm-back practice.*
*Candidate source class: OBSERVED basis (O-8); the confirm-back requirement is
JUDGMENT.*

---

## B. Escalation channels (candidates)

**Proposed R-114 (candidate) · every supervisor decision class carries an
explicit tolerance band with a consult-or-inform flag; the supervisor decides
inside the band and escalates only on a forecast breach.** Manage by exception:
the band is the permissible deviation before escalation [@prince2_exception],
and RACI's consulted-versus-informed line is the escalate-versus-notify line
[@wikipedia_ram]. This is the director-facing form of R-110 (a REQUEST carries
a reply_by and director bounds-and-escalates rather than polling forever) read
as the supervisor's own contract, and it unifies the escalation trigger with
the delegation grant (they are two readings of one number).
*Would be earned by: R-110 (bound-and-escalate, the operator's "wait forever on
replies that will never come" correction), generalized from director to
supervisor.*
*Candidate source class: OBSERVED basis (R-110); the per-class band is JUDGMENT
drawn from the exception model.*

**Proposed R-115 (candidate) · a denied route, a denied wake, or an
unreachable recipient fails loud as a named refusal, never as a timeout or a
silent success.** R-109 recorded a cross-team authorization denial arriving as
"context deadline exceeded," an authorization denial wearing a timeout's
clothes, and R-09 recorded five sends returning success with none delivered.
Director must surface these as loud, named refusals so the supervisor and the
human are not fed false state. Because the global tier routes cross-team and
cross-cluster relay through the director seat by topology (R-109), director is
the reliable escalation path by construction, and this candidate makes that
path's failures observable rather than inferred days later (R-105).
*Would be earned by: R-109 (three deadline-exceeded reproductions), R-09 (the
strongest silent-drop instance in the register), R-105.*
*Candidate source class: OBSERVED (R-109, R-09).*

**Proposed R-116 (candidate) · trouble classes are pre-classified by impact,
each attached to a who-to-wake and a how-often-to-report rule, with an
acknowledgement-timeout auto-escalation that ends at the human.** Incident
practice ties a graded severity, written down in advance, to a fixed escalation
path and cadence, and auto-advances on an ack timeout [@pagerduty_severity].
The went-dark case (R-89's silently denied wake, R-107's starved consumer) is
exactly the ack-timeout case: no acknowledgement within N escalates up the
chain. Escalate-to-human is a first-class terminal outcome (the arcaven-filer
wedge, where a permission dialog only a human can clear was the right route),
and before waking a wedged seat director clears its own pending decision first,
so a wake never submits a stale or control-bypass draft (O-29 danger).
*Would be earned by: R-89, R-107 (went-dark), the arcaven-filer wedge, O-29
(the composer-wedge danger).*
*Candidate source class: OBSERVED basis; the pre-classification scheme is
JUDGMENT drawn from incident practice.*

---

## C. Decision-rights support (candidates)

**Proposed R-117 (candidate) · delegated work is expressed as a mission order
plus a decision-rights table: per decision class, the accountable owner and
whether the worker may decide, must consult, or only informs.** Mission orders
carry result and purpose, not procedure, and the table allocates rights per
decision with exactly one accountable owner [@wikipedia_ram]. The table is
machine-representable and is the same artifact as the escalation policy in R-114
(the tolerance band and the delegation grant are two readings of one number).
This directly answers O-8: the supervisor lacked a commander's-intent statement
telling it where the boundary ran, so it filled the gap with its own board.
Authority delegates; responsibility does not, and the small set of non-delegable
decisions (production protection, the O-8 case) is named up front.
*Would be earned by: O-8 (deciding without the right or the intent statement);
R-70 (a delegated decision needs a bounded role to be delegated into).*
*Candidate source class: OBSERVED basis (O-8, O-22/R-70); the table shape is
JUDGMENT.*

**Proposed R-118 (candidate) · the supervisor is biased toward its own
role-verbs and does not absorb a worker's work; it sizes each worker's band to
demonstrated readiness and widens it as trust accrues.** R-102 already states
the own-verbs bias for director ("where the director holds direct capability
for a director-level action, do it; reserve relay for work that must run in
another session"); this candidate carries the same rule to the supervisor and
adds the readiness sizing from situational leadership
[@hersey1969management; @tannenbaum1958leadership]. Widening the band as a
worker earns it is how capability is built, not only how it is contained
[@spreitzer1995empowerment].
*Would be earned by: R-102 (own-verbs versus relay, bounded by z3wta),
generalized to the supervisor; the readiness axis.*
*Candidate source class: JUDGMENT on the R-102 observed basis.*

**Proposed R-119 (candidate) · partial compliance is preserved as a
first-class reportable outcome, and every delegated decision has a reliable
enactment path.** R-25 recorded a session that did four of five delegated
things, refused the fifth with a reason, verified it read-only, and reported the
residual as zero; a boolean ack would have destroyed all of that. Director must
carry partial outcomes, never coerce them to done-or-not. And O-29 recorded
subordinate decisions stranded in composers with no safe way to enact them: a
delegated decision with no enactment path is not delegated, it is stalled, so
director must provide or verify the enactment path before treating a decision
as delegated. R-21 adds that a parked ask's wake condition is not always a human
decision, so the enactment path must handle a capability or environment becoming
available, not only a human answer.
*Would be earned by: R-25 (partial compliance), O-29 (stranded decisions), R-21
(wake condition is not always human).*
*Candidate source class: OBSERVED (R-25, O-29); the sharpening is JUDGMENT
(R-21).*

---

## D. Coaching patterns (candidates, not register entries)

These are proposed director behaviors for developing a supervisor over logged,
reviewed episodes (synthesis Q1). They are coaching patterns rather than
requirements, so they are offered for the director skill's standing and harvest
modes, not as R-numbers.

- **Develop through the parallel channel, not the ask graph.** Director coaches
  the supervisor on a structure that parallels the task-routing chain
  [@tc7-22-7-2025, para 2-47], so development is an organizational function and
  not folded into ad hoc routing (R-33: the human is not the message bus).
- **Correct in SBI shape.** Director's correction of a supervisor names the run,
  the observed action, and the downstream effect [@ccl_sbi], which is close to a
  good bug report and avoids the global "you are unreliable" judgment; it also
  respects R-35 (output is ranked and terse).
- **Separate the identity verdict from the competency correction.** A weak
  supervisory episode is corrected as a trainable competency (the DO), not read
  as a character verdict (the BE) [@adp6-22-2019, para 1-84 and 1-85].
- **Debrief every standing session as development, not only as harvest.** The
  supervisor is learned by doing it under review [@hill2003becoming]; the
  director harvest mode is the natural place to add a supervisory-episode
  retrospective.

---

## Cross-referenced existing entries

- **R-08** (acknowledgement comes from the receiver, never the send call): the
  root of R-112's receiver-produced status and R-115's loud-delivery contract.
- **R-89** (the out-of-band wake channel can be silently denied): R-111 and
  R-116 (the went-dark ack-timeout case).
- **R-107** (a starved consumer is loud, never "silence, not failure"; the
  written form of finding-007 / director#66): R-111, R-112.
- **R-109** (the global tier routes cross-team relay through director; a denied
  publish fails loud): R-115.
- **R-110** (a REQUEST carries reply_by; bound-and-escalate, never poll
  forever): R-114.
- **R-102** (own-verbs versus relay, bounded by z3wta): R-118.
- **R-25** (partial compliance is a first-class reportable outcome): R-119.

## Carried flags

- The spot-report versus periodic-report taxonomy belongs to FM 6-99, which was
  NOT retrieved; R-111's cadence rests on the exception model and ADP 6-0 para
  1-59, not on that taxonomy.
- The one-on-one engagement multipliers are UNCONFIRMED (secondary aggregators);
  R-111 rests on the cadence finding, which is supported, not on the multipliers.
- Two ADP chapter-2 passages the synthesis cites (the ADP 6-0 back-brief list in
  "Command Presence" and the ADP 6-22 candor sentence in "Personal Courage") are
  cited by section name only, because the paragraph numbers did not survive text
  extraction; add the exact numbers from the published PDF before any of these
  candidates is filed as a real entry.
- finding-007 (director#66) has no standalone file yet, so R-107 is treated as
  its written form.
