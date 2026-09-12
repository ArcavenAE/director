# Director design brief 3: continuous custody and succession

Task: develop the succession option-set. The load-bearing insight from the
room is that shiftchange as a ritual assumes the departing session is present
to hand off. The ungraceful path (context exhausted, crash, accidental close)
skips the ritual, so custody cannot be written AT handoff. It has to be
externalized continuously, so an involuntary loss loses nothing.

This is not a new idea bolted on. It is R-22 (a dead session's asks survive it)
and R-42 (the store survives a restart with the queue intact) doing exactly
what they were written for, generalized from a single ask to the seat and to
worker roles.

## 1. Mechanism

### 1.1 Where custody lives, and why in two places

Custody is event-sourced across two durable stores on the bus:

- **AGENT_AUDIT stream (the journal, source of truth).** Every custody
  transition is one appended event: ask-opened, ask-parked (with its wake
  condition), ask-answered, artifact-filed, owner-reassigned. Append-only,
  ordered, survives a process crash. This is the record that cannot lie about
  the past, because nothing overwrites it.
- **CUSTODY KV bucket (the current view, a projection).** A materialized view
  keyed by ask id, holding only the asks that are still live (open or parked),
  each carrying {ask_id, raiser, owner, state, wake_condition, artifact_refs,
  last_update, owner_generation}. The KV is rebuildable by replaying the
  journal; it exists so a successor reads current custody in one pass instead
  of folding the whole stream.

The board (R-34) is the human-facing rendering of the KV current view plus the
roster. R-34 says the board needs no transport and its only missing piece was
somewhere durable to keep the ask. The CUSTODY KV is that somewhere.

### 1.2 Write-through, not flush-at-handoff

The discipline that makes involuntary loss survivable: director writes the
custody record BEFORE it acts on or continues past an ask. Accepting an ask
includes journaling it. Changing an ask's state includes writing the
transition. Learning a worker filed an artifact includes recording the
pointer. None of this waits for a handoff, a sweep, or a session close, because
those are exactly the moments an ungraceful exit never reaches.

### 1.3 Graceful shiftchange

When a seat holder rotates on purpose, it writes a handoff note: commander's
intent, current focus, what it was mid-thought on. The note is an orientation
convenience layered on top of durable state. It is NOT the source of truth. A
successor's authority to act comes from acquiring the seat lease (brief 2), and
its custody comes from reading the CUSTODY store. The note only helps a human
or a successor orient faster.

Marvel prior art: marvel owns the handoff schema, the departing agent owns the
content. Director keeps that split but does not require marvel. Without marvel,
director writes its own note into the CUSTODY store under a schema director
defines; the departing session supplies the content.

### 1.4 The ungraceful path (the one that matters)

A session dies with no note written. The seat lease TTL expires (brief 2 owns
the seat mechanics; the presence KV TTL is the vacancy alarm). A successor
acquires the vacant lease and reads the CUSTODY current view: the open and
parked asks, their wake conditions, their artifact pointers, their owners.
Nothing was waiting on a note, so nothing is lost. The successor inherits live
custody, minus only the orientation prose.

Re-seat in the physical-access phase: the human re-designates a session as the
seat, or a fresh session claims the vacant lease and the human confirms. There
is no election algorithm, because there is one operator in the room. The TTL
tells the human (or a later supervisor) that the seat went silent; the human
decides who sits.

R-26 (a handoff into an unattended channel is not a handoff) reappears here as
a rule about succession: acquiring the seat is not inheriting custody unless
the successor actually reads the CUSTODY store. The store is the attended
channel; a seat holder that never reads it has received nothing.

### 1.5 Worker-role succession, not just the seat

A dead worker also holds asks: questions it raised for the human, and asks
assigned to it. R-22 says these survive the session. So worker asks live in the
CUSTODY store too, not only in the worker's context. To make them reassignable,
an ask separates two fields:

- **raiser**: who first surfaced the ask (immutable).
- **owner**: who is currently working it (mutable; changes on reassignment).

When a worker dies, its owned open asks are not done. They are stranded (the
R-24 distinction: blocked or stranded is distinct from failed and from done)
and re-attachable: director re-routes them to a live worker or holds them for
the human, changing owner while preserving raiser and the ask body.

### 1.6 Lifecycle and cleanup (the R-44 guardrail)

Custody garbage collection is state-aware. Answered asks age out of the KV
current view (their history stays in the journal under the journal's own
retention). Open and parked asks are NEVER collected because their holder died.
This is the direct warning R-44 draws from gen-1's CleanupOrphaned, which
removed dead agents' mailboxes wholesale and so destroyed exactly the
undelivered asks R-22 protects. Do not copy that GC.

## 2. Failure modes and adversarial check

- **Gap window at crash (durable write lags real state).** Between an action
  and its write-through landing, a crash loses the delta. Mitigation: order
  writes as record-before-act, so the durable state is a superset of reality.
  The only surviving inconsistency is an ask shown open that was in fact just
  answered, which biases the failure toward re-asking a settled item
  (recoverable, mildly annoying) and away from losing an unanswered one (the
  original finding-151 defect). Fail toward remembering, never toward
  forgetting.
- **Double-processing after reassignment.** A reassigned ask is worked by its
  new owner while a revived zombie worker also works it. Mitigation: an ask
  carries an owner_generation that increments on reassignment (the same
  fencing idea as the seat lease). A result must cite the current
  owner_generation; a stale-generation result is journaled but is not the
  authoritative completion. Combine with R-47: confirm the artifact against the
  system of record rather than trusting either worker's self-report, so a
  double-file is detected.
- **Wake condition known only to the dead session.** A park whose wake
  condition lived in the worker's head cannot survive it. Mitigation: R-21
  already requires ask state to be a field. Sharpen it: a park record MUST
  carry a wake condition that a successor can evaluate (a machine predicate, or
  a human-readable condition addressed to the human). A park with no
  externalized wake condition is a custody defect, because it cannot survive
  its holder. This aligns with R-21's replay-01 sharpening (a wake condition is
  not always a human decision; it can be a capability or environment becoming
  available).
- **Unbounded growth.** Every ask and transition appended forever. Mitigation:
  the KV current view holds only open and parked asks, so its size tracks live
  work, not history. The journal is bounded by a retention policy. The bound
  never deletes an open or parked ask (R-44); it ages answered history.
- **Successor reads a stale view.** If the KV projection lags the journal at
  the moment of acquisition, the successor could miss the newest ask.
  Mitigation: on acquiring the seat, fold the journal forward from the KV's
  last-applied sequence before acting, so acquisition includes a catch-up read,
  not a bare KV snapshot.

## 3. Candidate requirements

Provisional tags, not R-numbers. Numbers are assigned at harvest (the register
currently ends at R-48).

- **CUST-A. Custody is externalized continuously (write-through), never flushed
  at handoff, because an involuntary exit skips the handoff.** Source: JUDGMENT.
  Earned by: the design reasoning over R-22's OBSERVED evidence (five sessions
  died holding unanswered asks with no chance to flush); the conclusion
  "therefore continuous, not handoff-time" is the judgment, the loss is
  observed.
- **CUST-B. A handoff note is an orientation convenience layered on durable
  state, never the source of truth for a successor's custody or authority.**
  Source: JUDGMENT. Earned by: the ungraceful-path analysis; a source-of-truth
  note would lose everything exactly when the note is skipped.
- **CUST-C. An ask separates raiser (immutable) from owner (mutable); a dead
  owner's open asks are reassignable, not done.** Source: JUDGMENT. Earned by:
  generalizing R-22 and R-24 from the seat to worker roles.
- **CUST-D. A parked ask's wake condition must be externalized so a successor
  can evaluate it; a wake condition held only in the dead session's context is
  a custody defect.** Source: JUDGMENT. Earned by: sharpening R-21 for the
  succession case.
- **CUST-E. Custody write ordering is fail-safe (record before act), so a crash
  gap biases toward re-asking a settled item rather than losing an unanswered
  one.** Source: JUDGMENT. Earned by: the gap-window adversarial pass against
  the finding-151 defect.
- **CUST-F. Custody garbage collection is state-aware: answered asks age out;
  open and parked asks are never collected because their holder died.** Source:
  OBSERVED (shipped code). Earned by: gen-1 CleanupOrphaned deleting dead
  agents' mailboxes wholesale, the same grounding as R-44.
- **CUST-G. A completion for an ask carries the current owner_generation; a
  stale-generation (zombie) result is journaled but not authoritative, and
  artifacts are confirmed against the system of record.** Source: JUDGMENT.
  Earned by: the double-processing adversarial pass; mirrors the seat fencing
  token and R-47.
- **CUST-H. Custody is event-sourced: the audit stream is the durable journal,
  the KV current view is a rebuildable projection holding only live asks; a
  successor folds the journal forward from the projection's last sequence
  before acting.** Source: JUDGMENT. Earned by: the stale-view adversarial pass
  and the NATS KV-plus-stream primitives; grounded in R-42.

## 4. Open questions

- The boundary between director's CUSTODY store and marvel's handoff artifact.
  Does director's store subsume marvel's, or complement it, when both are
  present? Marvel owns the shift schema; director must degrade without marvel.
- Write-through cost at high ask volume. Every transition is a KV write plus a
  journal append. Does volume force batching, and does batching reopen the gap
  window CUST-E closes? Name the tension; do not resolve it here.
- Who evaluates a machine-evaluable wake condition after the raiser dies. The
  seat holder on a poll, or a separate watcher subscribed to the condition?
- Journal retention. R-44 forbids deleting open or parked asks; answered-and-
  aged history still needs a bound, and the bound interacts with any future
  audit or compliance need.
- Whether a worker's own raised asks and asks assigned to it need different
  survival treatment, or the raiser/owner split covers both.
- Cross-host succession (future, past the physical-access phase): custody in a
  KV on one broker, a successor on another host. How does custody replicate?
  Deferred; the physical-access phase is single-broker.
