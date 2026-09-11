# Director requirements register

What director has to do, and what earned each item. This is the deliverable of
the simulation: the notes and specs are the evidence, this is the readable
answer.

Every entry traces to an observed instance. Nothing here is deduced from first
principles, and an entry with no instance behind it does not belong. Entries
are stable once numbered; supersede rather than renumber.

Status as of 2026-09-10, consolidated from session 1 (2026-09-04 through
09-08). Evidence lives in `notes/observations.md` (O-N),
`notes/friction.md` (FR-N), `specs/`, and the platform graph
(finding-144, finding-151, finding-159). The notes are gitignored because they
carry live operational detail; this register is the clean surface derived from
them and is safe to read on its own.

---

## A. Identity and authority

**R-01. A message carries a verifiable principal.** Who is speaking is a field,
not prose. Asserting authority in the body ("the operator says") is
unverifiable and leaves the receiver to invent a policy.
*Earned by: O-6, the first relay of the first simulation.*

**R-02. Authority strength is stated, never inferred.** A relayed instruction
carries the originator's authority only if origination can be proven, and the
coordinator's otherwise. A receiving session independently invented the rule
"relayed is weaker than direct" because nothing supplied it.
*Earned by: O-6.*

**R-03. Director must not represent itself as carrying authority it was not
given.** The one behavioral constraint that survived operator review. The
defect in the session-1 incident was the authority claim, not the wording.
*Earned by: O-8, operator ruling 2026-09-04 and 2026-09-10.*

**R-04. The originator's text is a distinct, immutable field from the
coordinator's annotation.** A receiver must be able to tell which words came
from the human. Merging them into one body destroys that distinction and the
receiver cannot recover it.
*Earned by: O-8.*

**R-05. Addressed sessions must be able to validate that a message is in fact
from director, with nonrepudiation.** This is the identity plane. Until it
exists, every correct refusal observed is luck.
*Earned by: operator ruling 2026-09-10; finding-159 (a vendor shipping "very
likely working on their behalf" is a probability estimate standing where a
signature belongs).*

**R-06. A restart changes a session's identity, capabilities, and address at
once.** A recovered session kept its conversation id and changed pid, socket
path, roster name, version, and feature set. Coordinator state keyed on name,
pid, or socket goes stale silently. Key on durable conversation identity and
re-resolve the rest on every send.
*Earned by: delivery spec R8.*

**R-07. Authority does not flow to content by way of the request that surfaced
it.** Asking a session to review materials does not make an instruction
embedded in those materials carry the authority of the human who asked for the
review. Enumerate instances; this is not closed.
*Earned by: operator ruling 2026-09-10, named as an open class.*

## B. Delivery and acknowledgement

**R-08. Acknowledgement comes from the receiver, never from the send call.** A
send returns "accepted for delivery" at most. Delivered, read, and acted-on are
distinct states, each reported by the receiver.
*Earned by: delivery spec R1; FR-15.*

**R-09. Undeliverable must be loud.** Silent drop with a success return is the
worst available behavior: it gives the coordinator false state, which the
coordinator reports to a human as fact. Five messages over three hours, every
send returning success with a message id, none delivered; the human was told
three times that the session was unresponsive.
*Earned by: delivery spec R2; FR-15; finding-151.*

**R-10. Queue state is inspectable by the sender.** Pending, drained, expired,
and dropped are four different things. A coordinator that cannot tell them
apart cannot tell a human what is true.
*Earned by: delivery spec R5.*

**R-11. Capability negotiation happens before send.** Know the receiver's
protocol version and feature set, refuse or degrade a send it cannot accept,
and never infer capability from a roster entry written when it started.
*Earned by: delivery spec R3; O-15.*

**R-12. Messages are chunked, not truncated.** Reports from working sessions
exceeded the delivery limit and truncated mid-sentence with no signal to either
side.
*Earned by: FR-14.*

**R-13. Duplicate delivery is detectable.** A completion arrived twice and the
second copy was indistinguishable from news.
*Earned by: FR-13. Envelope field: `duplicate_of`, `in_reply_to`.*

## C. Presence and liveness

**R-14. Liveness is end-to-end, produced by the receiver after processing, not
inferred from plumbing.** A process running, holding its socket, and reporting
idle can be accepting and discarding messages. Every observable short of the
recipient's own record said the session was healthy.
*Earned by: delivery spec R7; finding-144.*

**R-15. Presence distinguishes present-and-receptive from
present-and-unreachable.** Observed states on one harness: busy, idle, shell,
none, and absent-from-roster. A session in a subshell is present and cannot
receive.
*Earned by: O-3.*

**R-16. Status is not liveness.** A roster status is a label the harness last
wrote, not evidence the session is alive or reachable now.
*Earned by: FR-3.*

**R-17. One terminal signal distinguishes finished from died.** No harness
measured supplies this. A clean completion and a crash both read as "the
process is gone."
*Earned by: FR-5; finding-144; finding-159.*

**R-18. Reachability is a roster field, distinct from status,** carrying
last-successful-delivery and last-acknowledgement per session, so
unreachable-but-alive is visible without running an experiment.
*Earned by: delivery spec R4.*

**R-19. A heartbeat reports aliveness between messages.**
*Earned by: finding-144.*

## D. Custody of outputs and asks

**R-20. Custody is director's concern, though not exclusively.** Director
augments the human with tracking of, and communication with, the agents that
supervise agent teams; its relationship to outputs follows from that function.
It does not own outputs generally, any more than stdio is exclusive to one part
of a Unix system.
*Earned by: operator ruling 2026-09-10, answering the open question in
finding-151.*

**R-21. An ask has state: open, parked with a reason and a wake condition, or
answered.** An ask with no state re-consumes the human's attention every cycle.
One item was re-raised across several sweeps after its substance had already
been settled.
*Earned by: FR-10; O-8 follow-on; finding-151 envelope field ASK STATE.*

**R-22. A dead session's asks survive it.** A dead process leaves no roster
entry and takes its asks with it. Five sessions in a four-day window had died
holding unanswered questions, recoverable only by mining transcripts.
*Earned by: FR-4; O-1.*

**R-23. An artifact reference must outlive the session that made it.** A
session's pointer to its own work was prose, aimed at storage that dies with
it. In one exchange the artifact was never written (the write was blocked and
the session did not notice), the pointer named a transcript nobody was reading,
and the coordinator could not regenerate it.
*Earned by: FR-9; O-12.*

**R-24. Blocked-at-delivery is distinct from failed and from done.** Three of
four delegated tasks in one day ended blocked at the publish step, with the
work itself complete.
*Earned by: O-13; finding-151.*

**R-25. Partial compliance is a first-class reportable outcome.** A session did
four of five things, refused the fifth with a reason, verified the refused item
read-only, and reported the residual as zero. A boolean ack would have
destroyed all of that.
*Earned by: O-6, O-10.*

**R-26. A handoff into an unattended channel is not a handoff.** Delivery
requires a recipient that is actually reading.
*Earned by: FR-12; O-12.*

## E. Roster, discovery, and heterogeneity

**R-27. Adapters declare what they supply; director degrades per capability.**
A capability table that overstates is worse than none. Of four harnesses
installed on one machine, only one exposes presence or an address.
*Earned by: finding-159.*

**R-28. Sessions can be readable and unreachable, and that is the normal
case.** Never plan to message a session without checking its address first.
*Earned by: finding-159.*

**R-29. The roster carries the session's goal and current state.** The harness
roster has neither, and the transcript has no state, so both must be inferred
by mining. That inference is lossy and the roster says so.
*Earned by: FR-6, FR-7.*

**R-30. One session may hold several topics.** Treating a session as a unit of
work is wrong; the unit is the ask.
*Earned by: FR-8.*

**R-31. Long-lived sessions are normal, and the fleet is heterogeneous inside
one machine, one user, one account.** Two sessions had been running 16 and 26
days while the harness updated underneath them, so version and feature set
drift within a single roster.
*Earned by: delivery spec R6; O-15.*

**R-32. Director addresses supervisors and local sessions both.** Direct
management of local sessions is an intended use case, not a degenerate one:
the human works at one computer, and director manages local sessions directly
while managing remote work through supervisor agents.
*Earned by: operator ruling 2026-09-10.*

## F. The human's attention

**R-33. The human is not the message bus.** Every ask in session 1 had a
sender, a recipient, and no queue, no ack, no expiry, and no re-delivery. The
asks that went unanswered failed because the session ended before the human's
attention arrived, not because the human refused.
*Earned by: O-1. This is the load-bearing failure mode.*

**R-34. Director's first useful product is a board, and it needs no transport.**
A roster, the ask, and somewhere durable to keep the ask. Only the third is
missing from the borrowed substrate.
*Earned by: O-2.*

**R-35. Output to the human is ranked and terse by contract.** One line per
item, detail on request. The operator's complaint, verbatim: "can you get to
the point? These summaries are a wall of distracting text."
*Earned by: session-1 operator correction, 2026-09-04.*

**R-36. The board has no expiry, so external state must be re-verified.** The
operator noticed staleness before the coordinator did; a verification pass
found four of the tracked items already resolved.
*Earned by: O-14; the `dsx` instrument exists for this.*

**R-37. A coordinator's snapshot is stale on arrival** and must say when it was
taken.
*Earned by: FR-11.*

## G. Substrate independence

**R-38. Receiver-side policy is director's to supply, versioned and
inspectable.** Today one harness injects its own policy paragraph around every
inbound message; it is not ours to read, version, or extend, it changes on
upgrade, and no other harness ships anything like it.
*Earned by: finding-159; `specs/vendor-injected-receiver-policy.md` R1-R7.*

**R-39. Safety behavior must be portable across harnesses.** Correct refusals
credited to receiving sessions were partly the receiving harness's injected
text, which evaporates on any other harness.
*Earned by: finding-159, correcting finding-151.*

**R-40. Prohibitions belong in transport and credential, not in prose in a
context window.** Prose is advice to a model; it holds only if the model reads
carefully.
*Earned by: `specs/vendor-injected-receiver-policy.md` R4.*

**R-41. The receiver's context budget is director's to spend deliberately.**
Every delivered message currently spends receiver tokens on a paragraph
director did not choose and cannot shorten, scaling with message volume.
*Earned by: `specs/vendor-injected-receiver-policy.md` R5.*

---

## Not requirements: what the capture channel accumulated anyway

Observations O-16 through O-21 are about work hygiene in general (premature
published diagnoses, the value of a control run, claim verification, an
environment rolling underneath a repo). They are real and several became
platform findings, but they are not director requirements. They landed in the
director notes because the capture channel had no admission criterion and the
session had drifted off the role.

That is itself a requirement for how the simulation is run, not for the
software: a capture channel with no admission test collects whatever the
session happens to be learning about anything. Fixed by the harvest mode and
the admission test in the skill.

## Open, and deliberately not answered

- Whether director holds a standing license to originate anything that asks a
  session to act.
- Who is best placed to resolve an ambiguous instruction: director, the human,
  or the addressed session. One good instance and one bad instance so far, and
  one of each is not a rule.
- R-07's class: instructions embedded in reviewed material.
- What the envelope's evidence-standard and deviation-license fields should
  contain. Both were earned by O-9 and neither has a shape yet.
