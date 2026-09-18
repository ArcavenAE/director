# Emergency-shift verdict channel

**Date:** 2026-09-17
**Status:** options surfaced, nothing decided. This specifies the director-layer
half of the emergency-shift path and leaves the choices to the operator.
**Grounding:** R-01 (a message carries a verifiable principal), R-05
(nonrepudiation), and the "there are no peers" doctrine. Reconciled with
`sim/design/session-identity-and-succession-options.md` and
`sim/design/authority-never-in-content.md`. The marvel-side half is the
auto-shift trigger model (marvel PR #297, recommendation F) and the bd task
`aae-orc-tf5hy`.

## The problem, and why this half is director's

The auto-shift design (defect 7) calls for an emergency shift when an agent is
misbehaving: giving poor or bad results, showing suspect performance signals, or
running away (output tokens skyrocket and it loops). It splits in two.

- marvel-side (stays marvel): a LOCAL runaway detector in the health loop
  (output-token rate and repetition, needs no external principal and can act on
  its own observation), a graceful interrupt-first primitive, and the one
  shift-initiation seam. These are `aae-orc-3zxrv`, `aae-orc-mya1a`,
  `aae-orc-vb2tv`.
- director-side (this doc): the INBOUND verdict, where one principal asserts that
  another agent is unfit and recommends an emergency shift. This assertion is
  authority-bearing, so it is a director-layer concern, not a marvel primitive.

The reason it cannot be a marvel primitive is the doctrine. "There are no peers":
authority flows from the human, through the director, to the sessions and
supervisors it addresses; agents are not a flat mesh. A raw "kill agent X"
callable exposed to peers would be a flat-peer kill primitive, which is exactly
the false-takeover surface the session-identity doc's thread 4 warns against. A
verdict is instead an authority-bearing claim that the director layer carries and
marvel adjudicates. The receiver's question is never "is this a peer I trust," it
is "did the human's director authorize this agent to pass this judgment."

## What a verdict is

A verdict is an authority-bearing assertion by one principal about another
agent's fitness, carrying a recommendation, not a command. Shape:

- subject: the agent judged, named by its stable spawn identity (R-49/R-50), not
  by a colliding OS-user address (finding-003).
- issuer principal: who is asserting, as a verifiable principal (R-01).
- class: the claim, from a closed set (poor-results, suspect-signals,
  runaway-observed), so the receiver can reason about it.
- evidence: pointers, not prose (a transcript range, a metric, an event id).
- recommendation: the requested response (interrupt, emergency-shift, observe).
- reply_by and signature.

It is advisory. marvel adjudicates it against its own bar; a verdict is one input
to marvel's emergency decision, never a direct kill. This is the safety property:
no single external assertion, however authorized, reaches through to end an agent
without marvel's own interrupt-first, corroboration-aware mechanism.

## Authority: who may issue an act-worthy verdict (R-05 territory)

Not every agent may pass a killing judgment on any other. The verdict's weight is
its issuer's granted authority, minted and delegated, never self-declared. Three
options, surfaced not decided.

- Option A: supervisor-scoped. Only a supervisor role may issue an act-worthy
  verdict, and only about agents in the team it supervises; peers may REPORT
  (advisory, low weight) but not act-verdict. Tradeoff: matches the vision's
  supervisor-as-agent shape, but concentrates a dangerous authority in one role
  whose own compromise is then high-value.
- Option B: escalation-computed. Any agent may report; the weight is computed
  from the issuer's authority and corroboration, and only a sufficient weight is
  act-worthy. Tradeoff: flexible and defense-in-depth, but the weight function is
  a new policy surface to get right, and a subtle bug in it is a subtle kill
  policy.
- Option C: human-director only, until the plane matures. Only the human's
  director may issue an act-worthy verdict; everything else is observe-only.
  Tradeoff: safest and simplest, but it does not deliver the peer-reported case
  the operator asked for until later.

Across all three, the human's director may always issue a verdict, and the local
marvel detector needs no verdict at all (it observes directly).

## The envelope (R-01) and why verification is load-bearing

The verdict rides an authority-carrying envelope with `sender.principal` and a
signature, the same envelope the director protocol plans for every message. The
director verifies the principal before the verdict travels further, and marvel
verifies again before it acts. Without verification a forged verdict is a remote
kill primitive: a single crafted message ends another agent. So the verdict
channel cannot exist ahead of verifiable principals; it is one of the first
things the identity plane has to earn, because its failure mode is destructive,
not merely confusing.

This is the same R-01 the session-identity doc's thread 4 names, applied to a
verdict: identity is a launcher-minted, ideally signed principal (ID-A now,
ID-C/DID later), never an OS-user label. A verdict from `agent://ops/michael`
means nothing if every session in the project dir is michael (finding-003).

## Nonrepudiation (R-05)

Every verdict is signed and logged, so a wrongful emergency shift is attributable
and undeniable. Nonrepudiation is the deferred axis in the identity work, but for
an authority to end an agent it is load-bearing: if marvel kills on a verdict,
the record must prove who ordered it. The log is the accountability floor, and it
pairs with `authority-never-in-content.md`: the authority to issue a verdict
lives in the envelope and the issuer's minted grant, never asserted inside the
message body.

## Gating and the defined fallback

The channel depends on the identity plane (verifiable principals). Until that
exists it degrades to a defined state, never a silent accept-anything:

- observe-only: verdicts are logged, none act. Safe first state, and it lets the
  log accumulate before any teeth.
- human-director-only: the one principal trusted out of band (the operator's own
  seat) may act; all others observe. This is Option C as an interim.
- disabled: the channel is off; only marvel's local detector can emergency-shift.

The runaway case is why this matters even before the full plane: a runaway agent
is often still heartbeating (alive but looping), so roster liveness (R-93) does
not catch it, and the local marvel detector plus the human-director verdict cover
it without waiting for the peer-reported case.

## The seam to marvel

The contract at the boundary, so neither side reaches into the other:

- director hands marvel a VERIFIED verdict object {subject stable-id, class,
  evidence, issuer principal, recommendation, signature}, delivered to marvel's
  inbound emergency-shift input (the new daemon method in `aae-orc-tf5hy`).
- marvel adjudicates with its own higher bar: a verdict plus a local
  corroborating signal, or a sufficiently authoritative issuer, before it acts;
  it always tries the graceful interrupt first (`aae-orc-mya1a`) and only then
  the shift. marvel never exposes a raw kill to peers; the only thing crossing
  the seam is a verdict marvel judges.
- recovery-with-guard is succession content: the successor recovers from session
  remnants through the #295 reconcile path, and the handoff marks the triggering
  prompt poisoned so it is not replayed (`aae-orc-abrii`). The verdict channel
  does not carry the prompt; it names the subject and the class.

This keeps the split clean: director owns who-may-judge and the verified
envelope; marvel owns detect, interrupt, and shift; wardrobe/#295 owns the
recovery content.

## Transport

The verdict is a message type on the director bus (the authority-carrying
envelope, A2A-shaped per the platform direction, riding the shared
infrastructure NATS). It is not a new side channel; it is a message class beside
the director protocol's other types, carrying the R-01 principal fields the
envelope already plans. A runaway detector's own local finding is not a bus
message at all; it is marvel-internal. Only the external, principal-bearing
verdict rides the bus.

## Adversarial pass

- Forged verdict (a remote kill). Defended by R-01 verification at both hops,
  authority-scoping (only a sufficiently authorized issuer is act-worthy), and
  marvel's independent corroboration bar. A verdict alone from a low-authority
  issuer does not kill.
- Verdict flood (denial of service, or manufacturing corroboration). Rate-limit
  verdicts per issuer; a burst of verdicts is itself a suspect signal, not
  license to act faster. This mirrors the aggregation hazard on the marvel side
  (do not let correlated pressure burst the shared resource).
- Compromised supervisor (Option A's concentration risk). The nonrepudiation log
  makes its verdicts attributable after the fact, and marvel's interrupt-first
  makes a wrongful act recoverable rather than fatal in the common case.
- Self-verdict or mutual-kill loops. A verdict names a subject other than its
  issuer; two agents issuing act-worthy verdicts about each other is a policy
  case the weight function (Option B) or the scoping (Option A) must refuse.

## Open questions for the operator

- Which authority model (A, B, or C), and the interim state until the identity
  plane matures.
- Whether a supervisor's verdict is advisory to marvel (marvel adjudicates,
  recommended here to keep marvel the sole executor) or authoritative direction
  that marvel executes through its safety mechanism. This is the one place this
  doc's advisory stance sits in tension with the vision's supervisor-as-agent
  ("the supervisor decides, marvel executes"); I recommend that a DESTRUCTIVE act
  still passes marvel's interrupt-first and corroboration bar even when the
  issuer is an authoritative supervisor, the same way a human's destructive
  command goes through marvel's mechanism rather than around it.
- The corroboration bar marvel applies, and whether the human-director alone can
  clear it without a local corroborating signal.
