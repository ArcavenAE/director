# Director session identity and the seat: decision

Status: PARTLY SUPERSEDED (2026-09-18, same day). The operator pulled the seat model (the
fenced single-holder KV lease, "the seat" thread here) back for deeper analysis as a premise to
challenge, not implement: it drove to a technical single-holder answer without grounding in the
use cases, conflating the director FUNCTION with a singular identity. That thread is superseded
by `_bmad-output/director-function-model-analysis-2026-09-18.md` (director as a function that
participants may hold, potentially more than one; the lease demoted to a per-capability
exclusivity tool). The identity-at-spawn (ID-A) and writer-side deregister (BEAT-C) parts of
this recommendation STAND and are greenlit to build; they are identity and liveness, not the
seat model. Read the two documents together: this one for ID-A and liveness, the analysis for
the seat and role model.

Original status: DECIDED by a design-party roundtable, held for the operator. Feeds the director
requirements register. Date: 2026-09-18. Party record in `party-log.md`. Redaction held: no
origin organization, its short environment tokens, its infrastructure repository, or the
deployed hub hostname is named.

## Why this party ran

The crossing enable (resuming the director seat in global mode and naming the cluster) was
deferred because it front-runs the open director-session-identity design: "resume the seat in
global mode" is today an ad hoc address grab, on an identity that collides. The operator
routed the replan to a roundtable to decide the scheme the crossing needs, so the crossing
re-clears with a design rather than an ad hoc label. Grounded in R-01 (a message carries a
verifiable principal), R-05 (a receiver validates a message is from the director), R-06 (a
restart changes identity and address; key on durable identity), and the doctrine "there are
no peers": a human directs, the director is the human's assistive agent across sessions, so a
receiver's question is never "is this a peer I trust," it is "did the human's director
authorize this."

## The decision in one paragraph

Ship the physical-access-phase scheme now, name the crypto trajectory. Identity at spawn is
ID-C hybrid: ID-A ships (the launcher assigns a distinct `DIRECTOR_AGENT_ID`, passes
`--strict-mcp-config` so the baked project-scope config cannot override it, and the durable
consumer name is unique per session so a duplicate id can never silently race for mail), with
the self-minted DID plus JWS-signed AgentCard (ID-B) named as the deferred axis that returns
when the trust boundary leaves the host. The human-director seat is a fenced KV lease
(SEAT-A through SEAT-G): a create-only single-holder on `seat.<workspace>.director`, a fencing
token equal to the acquisition revision, renewal driven by the always-running shim on its own
timer (SEAT-D, the load-bearing constraint), fail-closed on renewal failure (SEAT-E), a TTL
that vacates a dead holder with no cooperation, and a zombie fenced by token. Seat authority
rides a distinct envelope `authority` block, never the content, distinct from
`sender.principal`. Shiftchange is a seat re-acquisition (acquire, never inherit by name;
SEAT-A), specializing marvel's handoff slot (`aae-orc-c3be5`) with a two-phase terminal
marker so the address is never held by two live sessions. False-seat prevention rests on the
launcher as the minting authority now (a session cannot pick its own name) plus the fencing
token for recency; the signed principal (R-01) and the nonrepudiation log (R-05) are the
deferred crypto axis, proportionate to defer while physical access to the human is the trust
boundary. Liveness ships writer-side deregister now (BEAT-C), adds the reader-side cutoff this
run earned (BEAT-G), and names receipt-based liveness (R-88) as the doctrine; all three serve
the ratified R-93 (presence is not a liveness signal), they layer, they do not compete.

## The label and identity approach the crossing needs (the synthesis)

Two distinct identities, and the whole point is not to fuse them:

- The CLUSTER label (decided by the 2026-09-18 cluster-identity party): the routable NATS
  token, operator-chosen, charset `[A-Za-z0-9_-]`, unique per tier. It names the cluster on
  the tier.
- The SESSION and SEAT identity (this party): the director's session address is a
  spawn-assigned agent id (ID-A), distinct from the OS user and from the cluster label; the
  director seat is a lease keyed `seat.<workspace>.director`, resolved to whoever holds it.

So the crossing enable is two things kept separate: name the cluster label (the cluster-
identity party settled this), AND resume the director seat as a lease acquisition in global
mode with a spawn-assigned id, not an address grab. The crossing front-ran this design because
"resume the seat in global mode" was an address grab on the colliding `agent://ops/michael`
identity; with the lease, resuming the seat is a create-only acquisition that is single-holder
and fenced, which is exactly the design the crossing was missing.

## What the crossing needs minimally to re-clear (the unblock)

Three items, and this trio is the whole unblock:

1. **ID-A at spawn.** The director session comes up with a distinct `DIRECTOR_AGENT_ID` (not
   the OS user), `--strict-mcp-config`, and a per-session-unique durable consumer. The demo
   already exists: `probe/nats-phase-0/id-a-demo/director-session.sh`.
2. **The seat as a lease.** Resuming the director seat in global mode is a create-only
   acquisition on `seat.<workspace>.director` with a fencing token, not an address grab.
   Single-holder falls out of the KV Create primitive; no external lock.
3. **Writer-side deregister (BEAT-C).** A prior seat holder's presence does not linger and
   collide; an orderly exit deletes presence, a crash falls to TTL.

Everything else (the DID, the signed principal, the nonrepudiation log, receipt-based
liveness, and the generation-bound-handoff hardening) is the durable trajectory: deferred, and
named so the interim is not a dead end.

## The five threads, decided

| Thread | Ships now (physical-access phase) | Deferred axis (named, not built) |
|---|---|---|
| 1. Identity at spawn | ID-A: launcher-assigned `DIRECTOR_AGENT_ID`, `--strict-mcp-config`, per-session-unique durable. ID-C is the trajectory. | ID-B: self-minted DID + JWS-signed AgentCard, when the boundary leaves the host. |
| 2. Seat authority | The fenced KV lease (SEAT-A/B/D/E/F/G): create-only single-holder, fencing token, shim-driven renewal, fail-closed, envelope `authority` block. | Authority on the envelope as a signed grant (Thread 2 C); the crypto completion of SEAT-C. |
| 3. Shiftchange custody | Acquire-not-inherit (SEAT-A) plus a two-phase terminal marker, specializing marvel's handoff slot `aae-orc-c3be5`; no live double-hold. | A bounded overlap window once Thread 1's durable identity lets two ids be distinct during overlap. |
| 4. False-seat prevention | Launcher as minting authority (a session cannot pick its own name) plus the fencing token for recency. | Signed principal (R-01) and nonrepudiation log (R-05), the crypto axis, when the boundary leaves the host. |
| 5. Roster liveness | Writer-side deregister (BEAT-C, ticketed) plus reader-side cutoff (BEAT-G, the reader gap this run earned), serving R-93. | Receipt-based liveness (R-88) as the doctrine: liveness is a recent advancing heartbeat, presence membership is advisory. |

## The load-bearing constraint (do not miss)

SEAT-D: liveness renewal, for both presence and the seat, must be driven by the always-running
shim on its own timer, never by the model calling a tool. A model turn can run for minutes; if
renewal shared the model's poll cadence (the receive-is-a-poll finding), a long turn would miss
the TTL window and vacate a seat that is very much held. This is the single highest-value item
in the seat design. The crossing's seat-resume depends on it, or the seat flaps under load.

## Why the fencing token is proportionate now, and where it stops

The fencing token proves RECENCY of a grant (the sender holds the current seat lease), not
identity. A session that learned the current token could forge a seat-authority message,
because nothing here is signed. That is proportionate while physical access to the human is the
trust boundary: every session is the human's own, on the human's machine, so the risk the token
addresses is an accident (a stale or zombie holder), not an adversary who learned a token. When
the trust boundary leaves the host, the crypto axis closes the gap: A2A v1.0 keeps identity
out-of-band (a DID the session owns, a JWS-signed AgentCard), and the seat message is then
signed by the holder's key, so a learned token alone cannot forge it. The axis is named now so
the token does not become a dead end.

## The one operator call the party surfaces and does not settle

Whether the minimal per-session spawn token (the vision's M1 minimal principal) lands WITH the
ID-A fix now or after a dedicated identity study. The party's lean: land ID-A, the lease, and
writer-side deregister now (they unblock the crossing and make the two recorded collisions
impossible in the graceful case), and take the spawn token with the dedicated M1 identity-lane
study, because it is the one place the cheap path and the durable path share a first step, and
binding it early risks the identity-lane sequencing the vision already sets. This is the
operator's call per the cross-thread synthesis, not the party's.

## Open questions carried, not resolved

- One seat per workspace or one per team (`seat.{ws}.director` vs `seat.{ws}.{team}.director`);
  several teams may each want a director seat.
- Does the human occupy the seat directly, or only through the director session (working
  assumption: through the session, the human is the principal behind it).
- Unify the seat lease with `role://` resolution: the seat key looks exactly like the
  resolution target for `role://{ws}/director`, so one mechanism may remove the envelope doc's
  separate roster-file-without-marvel path.
- The seat TTL value that balances fast failover against tolerating slow renewals and brief
  blips; ties to SEAT-D and the presence TTL.

## What this feeds into director requirements

The existing candidate tags stand and this decision adopts them: ID-A through ID-E
(`identity-at-spawn.md`), SEAT-A through SEAT-G (`director-seat-lease.md`), and the ratified
R-93 (presence is not liveness). This party mints no new R-numbers; it decides which candidates
ship in the physical-access phase (ID-A, SEAT-A/B/D/E/F/G, BEAT-C, BEAT-G) and which are the
named deferred axis (ID-B, the crypto completion of SEAT-C, R-88, the R-05 nonrepudiation log),
and it ties the scheme to the crossing so the crossing re-clears with a design. The address,
the seat, and the durable conversation identity stay three separate concepts (ID-D); the
michael collision fused address with the OS user and left the seat undefined, and keeping them
apart is the design.
