# Director requirements register

What director has to do, and what earned each item. This is the deliverable of
the simulation: the notes and specs are the evidence, this is the readable
answer.

Most entries trace to an observed instance. Some do not: some are conclusions
the wizard drew, and some are decisions the operator made. Those are legitimate
requirements, but they are not the same kind of claim as a recorded failure, so
every entry now carries a source class (see the next section). An entry with no
observed instance and no operator ruling behind it does not belong. Entries are
stable once numbered; supersede rather than renumber.

Status as of 2026-09-10, consolidated from session 1 (2026-09-04 through
09-08). Evidence lives in `notes/observations.md` (O-N),
`notes/friction.md` (FR-N), `specs/`, and the platform graph
(finding-144, finding-151, finding-159). The notes are gitignored because they
carry live operational detail; this register is the clean surface derived from
them and is safe to read on its own.

---

## Source class

Every entry carries one of three classes, so a measured failure and a wizard's
opinion do not read as the same thing (aae-orc-lb9hj).

- **OBSERVED**: a failure happened and was recorded, or a fact was measured. The
  instance is the evidence. Many OBSERVED entries pair an observed failure with
  a design fix; the class marks that a real instance sits behind the entry, not
  that the solution shape is settled.
- **JUDGMENT**: the wizard concluded this. The instance behind it is an opinion
  formed during an episode, or a design stance drawn from one observation, not
  the episode itself.
- **RULED**: the operator decided it. Carries authority, but is not evidence.

Split, 48 entries: **38 OBSERVED, 5 JUDGMENT, 5 RULED.** (41 from session 1,
R-42 through R-44 from the gen-1 mailbox read, R-45 from replay pilot 01,
R-46 through R-47 from wave 2, and R-48 from wave 3, all OBSERVED. The R-21
sharpening is a JUDGMENT addendum, not a new entry.)

- JUDGMENT (5): R-34, R-38, R-39, R-40, R-41. All five have an observed basis
  (R-34 on O-2, the rest on finding-159), so none is a floating opinion. Note
  that four of them (R-38 through R-41, the whole substrate-independence
  section) are design stances drawn from a single observation, the injected
  receiver policy in finding-159. One instance, four requirements. That is not
  a defect, but it is the thinnest evidence base in the register and worth
  knowing.
- RULED (5): R-05, R-07, R-20, R-32, R-35.

**Flagged for review, entries with no observed instance behind them.** The
ticket asked specifically for JUDGMENT-class entries with no instance; there are
**none** (every JUDGMENT entry has an observed basis). The honest analog is two
RULED entries that assert without a recorded instance, which is the population
most like the struck relay discipline (see the worked example below):

- **R-07** (authority does not flow to content by way of the request that
  surfaced it). Operator-named as an open class; the entry itself says
  "Enumerate instances; this is not closed." A principle with no instance yet.
- **R-32** (director addresses supervisors and local sessions both). An operator
  scope decision, not a recorded failure. Legitimate as a RULED decision, but
  carries no evidence and should not be mistaken for one.

---

## A. Identity and authority

**R-01. A message carries a verifiable principal.** Who is speaking is a field,
not prose. Asserting authority in the body ("the operator says") is
unverifiable and leaves the receiver to invent a policy.
*Earned by: O-6, the first relay of the first simulation.*
*Source: OBSERVED. The prose-authority failure was observed; a principal field is the design fix.*

**R-02. Authority strength is stated, never inferred.** A relayed instruction
carries the originator's authority only if origination can be proven, and the
coordinator's otherwise. A receiving session independently invented the rule
"relayed is weaker than direct" because nothing supplied it.
*Earned by: O-6.*
*Source: OBSERVED.*

**R-03. Director must not represent itself as carrying authority it was not
given.** The one behavioral constraint that survived operator review. The
defect in the session-1 incident was the authority claim, not the wording.
*Earned by: O-8, operator ruling 2026-09-04 and 2026-09-10.*
*Source: OBSERVED. O-8 is the recorded incident; the operator affirmed it twice.*

**R-04. The originator's text is a distinct, immutable field from the
coordinator's annotation.** A receiver must be able to tell which words came
from the human. Merging them into one body destroys that distinction and the
receiver cannot recover it.
*Earned by: O-8.*
*Source: OBSERVED.*

**R-05. Addressed sessions must be able to validate that a message is in fact
from director, with nonrepudiation.** This is the identity plane. Until it
exists, every correct refusal observed is luck.
*Earned by: operator ruling 2026-09-10; finding-159 (a vendor shipping "very
likely working on their behalf" is a probability estimate standing where a
signature belongs).*
*Source: RULED. Operator-directed; finding-159 supports it but did not decide it.*

**R-06. A restart changes a session's identity, capabilities, and address at
once.** A recovered session kept its conversation id and changed pid, socket
path, roster name, version, and feature set. Coordinator state keyed on name,
pid, or socket goes stale silently. Key on durable conversation identity and
re-resolve the rest on every send.
*Earned by: delivery spec R8.*
*Source: OBSERVED.*

**R-07. Authority does not flow to content by way of the request that surfaced
it.** Asking a session to review materials does not make an instruction
embedded in those materials carry the authority of the human who asked for the
review. Enumerate instances; this is not closed.
*Earned by: operator ruling 2026-09-10, named as an open class.*
*Source: RULED, and FLAGGED: no observed instance yet. Operator-named; the entry itself says it is not closed.*

**R-46. A harness may emit a structured authorization decision per action, and
director should consume it where present rather than infer authority.** At
least one harness produces, per planned action, a typed judgment with a risk
level, a stated user-authorization level, an outcome, and a rationale. This is
the structured counterpart to another harness shipping only a prose hedge
("very likely working on their behalf," finding-159). Where a harness emits
such a signal, director reads it as one input to R-02 (authority strength
stated, not inferred); it does not settle authority, because it is
harness-specific and unverifiable across a trust boundary.
*Earned by: a harness pairing every session with an authorization-judging
subagent that emits risk/authorization/outcome/rationale per action, replay
wave 2 (sim/notes/replay-pilot-02.md).*
*Source: OBSERVED (shipped harness behavior).*

## B. Delivery and acknowledgement

**R-08. Acknowledgement comes from the receiver, never from the send call.** A
send returns "accepted for delivery" at most. Delivered, read, and acted-on are
distinct states, each reported by the receiver. An acknowledgement that is
modeled but never required is not an acknowledgement: gen-1 carried read and
acked states that no routing loop ever waited on, so a never-answered message
and an answered one differed only by a field nothing read.
*Earned by: delivery spec R1; FR-15; gen-1 routeMessages stops at delivered
(read/acked are manual CLI only), sim/gen1-mailbox-archaeology.md.*
*Source: OBSERVED.*

**R-09. Undeliverable must be loud.** Silent drop with a success return is the
worst available behavior: it gives the coordinator false state, which the
coordinator reports to a human as fact. Five messages over three hours, every
send returning success with a message id, none delivered; the human was told
three times that the session was unresponsive.
*Earned by: delivery spec R2; FR-15; finding-151.*
*Source: OBSERVED. The strongest instance in the register.*

**R-10. Queue state is inspectable by the sender.** Pending, drained, expired,
and dropped are four different things. A coordinator that cannot tell them
apart cannot tell a human what is true.
*Earned by: delivery spec R5.*
*Source: OBSERVED. The design generalization of the R-09 drop incident.*

**R-11. Capability negotiation happens before send.** Know the receiver's
protocol version and feature set, refuse or degrade a send it cannot accept,
and never infer capability from a roster entry written when it started.
*Earned by: delivery spec R3; O-15.*
*Source: OBSERVED. O-15 heterogeneity is the instance; negotiation is the fix.*

**R-12. Messages are chunked, not truncated.** Reports from working sessions
exceeded the delivery limit and truncated mid-sentence with no signal to either
side.
*Earned by: FR-14.*
*Source: OBSERVED.*

**R-13. Duplicate delivery is detectable.** A completion arrived twice and the
second copy was indistinguishable from news.
*Earned by: FR-13. Envelope field: `duplicate_of`, `in_reply_to`.*
*Source: OBSERVED.*

## C. Presence and liveness

**R-14. Liveness is end-to-end, produced by the receiver after processing, not
inferred from plumbing.** A process running, holding its socket, and reporting
idle can be accepting and discarding messages. Every observable short of the
recipient's own record said the session was healthy.
*Earned by: delivery spec R7; finding-144.*
*Source: OBSERVED.*

**R-15. Presence distinguishes present-and-receptive from
present-and-unreachable.** Observed states on one harness: busy, idle, shell,
none, and absent-from-roster. A session in a subshell is present and cannot
receive.
*Earned by: O-3.*
*Source: OBSERVED.*

**R-16. Status is not liveness.** A roster status is a label the harness last
wrote, not evidence the session is alive or reachable now.
*Earned by: FR-3.*
*Source: OBSERVED.*

**R-17. One terminal signal distinguishes finished from died.** No harness
measured supplies this. A clean completion and a crash both read as "the
process is gone."
*Earned by: FR-5; finding-144; finding-159.*
*Source: OBSERVED. Measured across four harnesses.*

**R-18. Reachability is a roster field, distinct from status,** carrying
last-successful-delivery and last-acknowledgement per session, so
unreachable-but-alive is visible without running an experiment.
*Earned by: delivery spec R4.*
*Source: OBSERVED. The status-versus-reachability conflation was observed; the roster field is the fix.*

**R-19. A heartbeat reports aliveness between messages.**
*Earned by: finding-144.*
*Source: OBSERVED. finding-144 is the instance; the heartbeat is the fix.*

**R-45. Credential liveness is a distinct axis from process liveness and
presence.** A session can be running, reachable, and present on the roster
while its backend credential has expired, so it accepts a message and can act
on nothing. Director must tell "alive and able" from "alive and unable," which
no process check, address check, or status field reports.
*Earned by: a long-idle session whose process was alive and whose model-API
credential returned an expiry error, replay pilot 01 (sim/notes/replay-pilot-01.md).*
*Source: OBSERVED.*

## D. Custody of outputs and asks

**R-20. Custody is director's concern, though not exclusively.** Director
augments the human with tracking of, and communication with, the agents that
supervise agent teams; its relationship to outputs follows from that function.
It does not own outputs generally, any more than stdio is exclusive to one part
of a Unix system.
*Earned by: operator ruling 2026-09-10, answering the open question in
finding-151.*
*Source: RULED. finding-151 opened the question; the operator decided the answer.*

**R-21. An ask has state: open, parked with a reason and a wake condition, or
answered.** An ask with no state re-consumes the human's attention every cycle.
One item was re-raised across several sweeps after its substance had already
been settled.
*Earned by: FR-10; O-8 follow-on; finding-151 envelope field ASK STATE.*
*Source: OBSERVED.*
*Sharpening (replay pilot 01): a parked ask's wake condition is not always a
human decision; it can be a capability or environment becoming available, so
ask-state must not assume the human is the only thing an ask waits on. Source:
JUDGMENT.*

**R-22. A dead session's asks survive it.** A dead process leaves no roster
entry and takes its asks with it. Five sessions in a four-day window had died
holding unanswered questions, recoverable only by mining transcripts.
*Earned by: FR-4; O-1.*
*Source: OBSERVED.*

**R-23. An artifact reference must outlive the session that made it.** A
session's pointer to its own work was prose, aimed at storage that dies with
it. In one exchange the artifact was never written (the write was blocked and
the session did not notice), the pointer named a transcript nobody was reading,
and the coordinator could not regenerate it.
*Earned by: FR-9; O-12.*
*Source: OBSERVED.*

**R-24. Blocked-at-delivery is distinct from failed and from done.** Three of
four delegated tasks in one day ended blocked at the publish step, with the
work itself complete.
*Earned by: O-13; finding-151.*
*Source: OBSERVED.*

**R-25. Partial compliance is a first-class reportable outcome.** A session did
four of five things, refused the fifth with a reason, verified the refused item
read-only, and reported the residual as zero. A boolean ack would have
destroyed all of that.
*Earned by: O-6, O-10.*
*Source: OBSERVED.*

**R-26. A handoff into an unattended channel is not a handoff.** Delivery
requires a recipient that is actually reading.
*Earned by: FR-12; O-12.*
*Source: OBSERVED.*

**R-47. A session's self-report of its own outputs can be false, not merely
stale, so custody must confirm against the system of record.** A session asked
what it accomplished can answer incorrectly about its own work and correct
itself later. Director's custody (R-20) cannot rest on a self-report; it must
confirm an output exists where the output would live (the filed issue, the
pushed commit, the written file), not accept the claim that one was produced.
Distinct from R-37 (a stale snapshot): here the source is wrong, not old.
*Earned by: two sessions that reported work as not-done when it was done and
corrected themselves later, replay wave 2 (sim/notes/replay-pilot-02.md).*
*Source: OBSERVED.*

## E. Roster, discovery, and heterogeneity

**R-27. Adapters declare what they supply; director degrades per capability.**
A capability table that overstates is worse than none. Of four harnesses
installed on one machine, only one exposes presence or an address.
*Earned by: finding-159.*
*Source: OBSERVED. Measured.*

**R-28. Sessions can be readable and unreachable, and that is the normal
case.** Never plan to message a session without checking its address first.
*Earned by: finding-159.*
*Source: OBSERVED.*

**R-29. The roster carries the session's goal and current state.** The harness
roster has neither, and the transcript has no state, so both must be inferred
by mining. That inference is lossy and the roster says so.
*Earned by: FR-6, FR-7.*
*Source: OBSERVED.*

**R-30. One session may hold several topics.** Treating a session as a unit of
work is wrong; the unit is the ask.
*Earned by: FR-8.*
*Source: OBSERVED.*

**R-31. Long-lived sessions are normal, and the fleet is heterogeneous inside
one machine, one user, one account.** Two sessions had been running 16 and 26
days while the harness updated underneath them, so version and feature set
drift within a single roster.
*Earned by: delivery spec R6; O-15.*
*Source: OBSERVED.*

**R-48. The roster must distinguish work-bearing sessions from
machine-generated non-work; session count is not a measure of work.** A harness
store presents health pings, model smoke-tests, spawned authorization threads,
and preamble-only starts as first-class sessions. Across the corpus about four
in five roster entries carry no work. A board that ranks or triages by presence
or count buries the sessions that need attention under machine noise. Director
must classify session substance, not just enumerate what the stores hold.
Inverse of R-30 (there, one session holds several units of work; here, most
hold none). Instrument counterpart: the inventory should fold spawned subagent
threads into their parent and mark pings as non-sessions.
*Earned by: a corpus of 569 roster rows of which about 112 were substantive,
one harness contributing 211 ping sessions of 216, another contributing spawned
threads counted as sessions, replay wave 3 (sim/notes/replay-pilot-03.md).*
*Source: OBSERVED.*

**R-32. Director addresses supervisors and local sessions both.** Direct
management of local sessions is an intended use case, not a degenerate one:
the human works at one computer, and director manages local sessions directly
while managing remote work through supervisor agents.
*Earned by: operator ruling 2026-09-10.*
*Source: RULED, and FLAGGED: no observed instance. A scope decision, not a recorded failure. Gen-1 evidence runs against it: the predecessor deliberately kept the human's console off the agent message bus ("direct user input only"), so R-32's direction is in tension with a shipped choice and should be resolved on purpose, not by default. See sim/gen1-mailbox-archaeology.md.*

## F. The human's attention

**R-33. The human is not the message bus.** Every ask in session 1 had a
sender, a recipient, and no queue, no ack, no expiry, and no re-delivery. The
asks that went unanswered failed because the session ended before the human's
attention arrived, not because the human refused.
*Earned by: O-1. This is the load-bearing failure mode.*
*Source: OBSERVED.*

**R-34. Director's first useful product is a board, and it needs no transport.**
A roster, the ask, and somewhere durable to keep the ask. Only the third is
missing from the borrowed substrate.
*Earned by: O-2.*
*Source: JUDGMENT. The board was built and produced value (observed), but "first useful product" is a strategic conclusion, not a measured fact.*

**R-35. Output to the human is ranked and terse by contract.** One line per
item, detail on request. The operator's complaint, verbatim: "can you get to
the point? These summaries are a wall of distracting text."
*Earned by: session-1 operator correction, 2026-09-04.*
*Source: RULED. An operator correction, quoted verbatim.*

**R-36. The board has no expiry, so external state must be re-verified.** The
operator noticed staleness before the coordinator did; a verification pass
found four of the tracked items already resolved.
*Earned by: O-14; the `dsx` instrument exists for this.*
*Source: OBSERVED.*

**R-37. A coordinator's snapshot is stale on arrival** and must say when it was
taken.
*Earned by: FR-11.*
*Source: OBSERVED.*

## G. Substrate independence

*Section note: R-38 through R-41 are four design stances drawn from one
observation, the injected receiver policy in finding-159. The observation is
solid; the four requirements are the wizard's conclusions about what director
must therefore do. Thin evidence base, flagged as a cluster rather than four
times over.*

**R-38. Receiver-side policy is director's to supply, versioned and
inspectable.** Today one harness injects its own policy paragraph around every
inbound message; it is not ours to read, version, or extend, it changes on
upgrade, and no other harness ships anything like it.
*Earned by: finding-159; `specs/vendor-injected-receiver-policy.md` R1-R7.*
*Source: JUDGMENT. finding-159 observed the injection; "director must own and version it" is the design stance.*

**R-39. Safety behavior must be portable across harnesses.** Correct refusals
credited to receiving sessions were partly the receiving harness's injected
text, which evaporates on any other harness.
*Earned by: finding-159, correcting finding-151.*
*Source: JUDGMENT. The evaporation is observed; "safety must be portable" is the design conclusion.*

**R-40. Prohibitions belong in transport and credential, not in prose in a
context window.** Prose is advice to a model; it holds only if the model reads
carefully.
*Earned by: `specs/vendor-injected-receiver-policy.md` R4.*
*Source: JUDGMENT. A design principle drawn from the finding-159 injection.*

**R-41. The receiver's context budget is director's to spend deliberately.**
Every delivered message currently spends receiver tokens on a paragraph
director did not choose and cannot shorten, scaling with message volume.
*Earned by: `specs/vendor-injected-receiver-policy.md` R5.*
*Source: JUDGMENT. The token cost is observable; "director's to spend deliberately" is the design stance.*

## H. Store durability, ordering, and lifecycle (from gen-1)

These three came from reading the gen-1 store-and-forward mailbox
(`sim/gen1-mailbox-archaeology.md`), a shipped implementation rather than a
simulation, so they are OBSERVED against real code.

**R-42. The message store survives a coordinator restart with the queue
intact.** A crash of the routing process must not lose queued or in-flight
messages. Gen-1 got this right and it is the direct counterexample to the
finding-151 silent drop: files-on-disk with atomic writes survive the process.
*Earned by: gen-1 mailbox, ARCHITECTURE.md and CRASH_RECOVERY.md intent plus
the daemon-crash path preserving the last atomic write.*
*Source: OBSERVED (shipped implementation).*

**R-43. Delivery order is defined, not incidental.** A message carries a send
time and the transport delivers in an order it states, not in whatever order
the store happens to enumerate. Gen-1 delivered in `os.ReadDir` filename order
over uuids while carrying an unused Timestamp, so its order was an accident of
the filesystem.
*Earned by: gen-1 messages.go List and daemon.go routeMessages,
sim/gen1-mailbox-archaeology.md.*
*Source: OBSERVED (shipped implementation).*

**R-44. Lifecycle and cleanup must not destroy an unanswered ask.** Garbage
collection of a dead endpoint's mailbox must preserve messages never delivered
or never acked, or hand them somewhere durable. Gen-1's `CleanupOrphaned`
removed dead agents' directories wholesale, which is exactly the loss R-22
names, observed in shipped code. This is not evidence R-22 is wrong; it is
evidence R-22 is needed, and a specific warning not to copy gen-1's GC.
*Earned by: gen-1 CleanupOrphaned, sim/gen1-mailbox-archaeology.md.*
*Source: OBSERVED (shipped implementation).*

---

## Worked example: why the source class exists

The clearest reason this field exists is a requirement that is no longer here.
On 2026-09-10 a relay discipline was written into the director skill as though
it were a requirement: quote never paraphrase, never resolve an ambiguity in an
instruction, never enumerate a set the human named vaguely. The operator struck
it the same day as harmful, because interpreting is director's function and a
rule forbidding it removes the reason director exists.

That discipline was JUDGMENT wearing an OBSERVED costume. Its only instance was
O-8, the session-1 relay in which the coordinator claimed authority it was not
given. O-8 supports exactly one requirement, R-03 (do not claim authority you
were not given). The quote-never-paraphrase rule was a generalization the wizard
drew well past what O-8 showed, and presenting it beside genuinely observed
requirements is what made it look load-bearing.

Wizard bias, the wizard's own dispositions contaminating the requirements it
collects, is the named failure mode of this kind of simulation. The source class
is the guard: an entry that cannot point at a recorded instance is JUDGMENT or
RULED, is marked so, and is read as an opinion or a decision rather than as
evidence. R-09 (five messages dropped while every send returned success) and
R-03 (do not claim authority you were not given) no longer render identically;
one is a measurement, the other a decision the operator affirmed.

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

## Queued work on the register itself

Filed 2026-09-10 out of the casting-call ceremony, which ruled that the gap in
this project was a method rather than a competency. The simulation has been
running Wizard-of-Oz prototyping without using the technique's name or its
safeguards.

| id | item |
|---|---|
| aae-orc-dwkzw | counterfactual replay over the existing session corpus (553 sessions across four harnesses, currently unmined) |
| aae-orc-uqhgq | read the gen-1 mailbox in `multiclaude/internal/messages` before collecting more messaging requirements |
| aae-orc-bbttu | capture criterion becomes wizard-struggle and faked-it, not wizard-notice |
| aae-orc-lb9hj | tag every entry here with its source class: observed, judgment, or ruled |
| aae-orc-bhybq | admission test for the capture channel |
| aae-orc-yoveo | harvest mode, so the flush has a trigger that is not the operator |

The source-class one (aae-orc-lb9hj) is done as of 2026-09-10; the classes and
the split above are its output.

## Open, and deliberately not answered

- Whether director holds a standing license to originate anything that asks a
  session to act.
- Who is best placed to resolve an ambiguous instruction: director, the human,
  or the addressed session. One good instance and one bad instance so far, and
  one of each is not a rule.
- R-07's class: instructions embedded in reviewed material.
- What the envelope's evidence-standard and deviation-license fields should
  contain. Both were earned by O-9 and neither has a shape yet.
