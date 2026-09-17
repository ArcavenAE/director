# Session identity and succession: solution options

**Date:** 2026-09-17
**Status:** options surfaced, nothing decided. This doc pressure-tests
solution shapes for the operator to choose among; it does not rule.
**Grounding:** the two live instances of 2026-09-17 (finding-003, the
session-identity collision; finding-166 instance (c), stale presence on exit),
plus R-01 (a message carries a verifiable principal), R-05 (nonrepudiation),
and R-06 (a restart changes identity and address; key on durable identity).

## The organizing doctrine: there are no peers

Every option below resolves against one frame. A human directs. The director is
the human's assistive agent across many sessions, harnesses, and hosts. The
director addresses sessions and supervisors; they do not form a flat peer mesh.
So a receiver's question is never "is this a peer I trust," it is "did the
human's director authorize this." That single reframe decides most of the
tradeoffs: identity, seat authority, and takeover defense all reduce to a grant
that is minted and delegated, never self-declared and never inferred from the
channel.

The two instances are one class seen twice. Identity is under-specified: it is
self-asserted and the address equals the OS user, so any session in the project
dir is `agent://ops/michael`. Liveness is under-specified: there is no
deregister on exit and no read-time cutoff, so a dead seat lingers. The cheapest
fixes close each half now; the durable answer is a minting authority plus a
signed principal plus receipt-based liveness plus a generation-bound handoff.

## Thread 1: a distinct stable identity at spawn

Today identity derives from the OS user through a project-scope MCP config, and
the only roster discriminators are the instance ULID and the pid (finding-003).

- **Option A: launcher-assigned name (ID-A).** The launcher assigns a distinct
  `DIRECTOR_AGENT_ID` per session and passes `--strict-mcp-config` so the baked
  project-scope config cannot override it. `probe/nats-phase-0/id-a-demo/director-session.sh`
  already does this. Tradeoff: it closes the collision tonight, but a name is a
  label, not an attestation (ID-E). It holds only while the launcher is the one
  true minter and the human owns the launcher.
- **Option B: name bound to a durable session identity (R-06).** Key identity on
  the session's durable id, the ULID that was in fact the only thing telling the
  two sessions apart, so a restart keeps identity distinct from address.
  Tradeoff: senders address a name, so this needs a published mapping from
  durable id to a current address, and the roster has to carry it.
- **Option C: a DID bound to the name (ID-C).** The name carries a key, so the
  identity is verifiable, not just declared. Tradeoff: heavier, needs a trust
  root, and is the place where R-01 and R-05 actually land. A deferred
  trajectory, not a tonight fix.
- **Option D: hybrid.** ID-A now, ID-C when the nonrepudiation plane exists. The
  launcher-assigned name is the interim; the DID binding is the destination.

## Thread 2: the human-director seat and its authority

- **Option A: singleton by convention.** Exactly one session holds
  `agent://ops/<human>`; others take a role-suffixed address. Tradeoff:
  convention only. The collision I recorded is precisely this singleton going
  unenforced.
- **Option B: the seat as a claimed lease.** The seat is a KV lease one session
  holds; a second claimant is refused or queued. Tradeoff: it needs a liveness
  story (Thread 5) so a dead holder's lease frees, and it carries the operator
  risk Reyes named: a stuck lease that locks the human out of their own seat is
  worse than a brief double-address.
- **Option C: authority on the envelope, not the seat (R-01).** The seat is just
  an address; the authority to act as director is a principal claim the bus
  verifies, so occupying the address confers nothing. Tradeoff: the enforcement
  plane is unbuilt. It is the strongest option and the furthest.

Doctrine cut: authority flows human to director to addressed sessions. The
seat's authority is delegated from the human, so the real question is how that
delegation is represented: a name (A), a lease (B), or a signed grant (C).

## Thread 3: shift-change custody handoff without losing the seat

marvel owns the handoff schema (vision row 15) and the departing agent owns the
content; the generation-bound handoff slot is `aae-orc-c3be5`. The director seat
is the special case where the address is the human's, so the aim is to
specialize the marvel mechanism, not invent a second one.

- **Option A: two-phase custody transfer.** The departing director writes the
  handoff content and marks the slot terminal; the successor claims the seat
  only after the terminal marker, so the address is never held by two live
  sessions. Tradeoff: a gap where no one holds the seat and the human is briefly
  unaddressed, unless an overlap is allowed.
- **Option B: generation-bound address.** The seat address carries a generation;
  senders address the current generation and a handoff bumps it. Tradeoff:
  senders must discover the current generation (the roster has to publish it),
  and a message in flight to the old generation after cutover is stranded, which
  wants the reconcile-from-records fallback.
- **Option C: overlap window.** The successor comes up on a provisional address,
  drains the predecessor's in-flight, then assumes the seat. Tradeoff: two
  identities are live at once by design, so they must be distinct even during
  overlap, which requires Thread 1 to be solved first.

The two instances bound this thread from both ends. Instance 1 is an accidental
live second holder; Instance 2 is a dead holder that lingers. A managed handoff
has to prevent the live collision and drop the dead holder promptly, so Thread 3
cannot be designed apart from Thread 5.

## Thread 4: malicious false-takeover and nonrepudiation

The threat is Instance 1 done on purpose: a session sets
`DIRECTOR_AGENT_ID=michael` and speaks as the director. Today nothing stops it,
because identity is a self-asserted label.

- **Option A: a minting authority.** Only the launcher or marvel mints
  identities; a session cannot pick its own name. This closes the accidental
  case and the casual malicious one. Tradeoff: the trust root is the launcher;
  on a single shared host that is the human's own boundary, and a determined
  local actor who controls the launcher env is out of scope for it.
- **Option B: a signed envelope (R-01).** Every message carries a
  `sender.principal` with a signature that the bus and receivers verify. A
  forger without the key cannot speak as the director. Tradeoff: key custody.
  The SOUL custody boundary points at a minimal per-session spawn token as the
  grant (this is vision M1's minimal principal), not a durable secret held by
  the agent.
- **Option C: a nonrepudiation log (R-05).** Messages are signed and logged so
  that after the fact you can prove who said what. It does not prevent a
  takeover; it makes one attributable and undeniable. Tradeoff: log integrity
  and key binding, and it is the deferred axis.

Doctrine cut: because there are no peers, a signed grant from the minting
authority answers the receiver's only real question; a self-asserted name never
does.

## Thread 5: roster liveness

Instance 2: a quit session read `present` for the full 90s TTL with a frozen
`ts`, because the shim did not deregister and the reader applied no cutoff.

- **Option A: writer-side graceful deregister (BEAT-C).** Delete presence on an
  orderly exit; a crash falls to TTL. Tracked by `aae-orc-kz4t5` and
  `aae-orc-lebdu`. Tradeoff: SIGKILL still lingers one TTL window.
- **Option B: reader-side liveness cutoff (BEAT-G).** `list_roster` marks or
  drops any entry older than a staleness bound, covering the ungraceful case
  within the observation window. This is the new gap this run earned. Tradeoff:
  it picks a bound, and a slow-but-alive shim near the bound can flap.
- **Option C: receipt-based liveness (R-88).** Consumers take liveness from a
  recent, advancing heartbeat rather than from list membership. This is the
  strongest signal, since an advancing `ts` is exactly what distinguished live
  from dead in Instance 2. Tradeoff: every consumer must implement the check,
  and presence membership becomes advisory.

These layer rather than compete. A is the cheapest and already ticketed, B is
the reader-side gap this run surfaced, and C is the doctrine that presence
implies possibly-stale while liveness is a receipt. Liveness meets identity at
the handoff: a returning session must find the dead predecessor already dropped
(A or B) so it does not collide, and must be distinguishable during any overlap
(Thread 1).

## Cross-thread synthesis

Two axes, one cheapest-now and one durable, run through all five threads.

- **Closes tonight:** ID-A launcher-assigned name (Thread 1 A) plus writer-side
  deregister (Thread 5 A). Together they make the two recorded instances
  impossible in the graceful case at low cost.
- **The durable answer:** a minting authority (Thread 4 A) issuing a signed
  principal (Thread 4 B / R-01), receipt-based liveness (Thread 5 C / R-88), and
  a generation-bound handoff specialized from marvel's slot (Thread 3 B, over
  `aae-orc-c3be5`). Nonrepudiation (R-05) is the deferred axis that makes a
  takeover attributable.

The sequencing question I did not resolve: whether the minimal per-session spawn
token (vision M1) lands with the tonight fix or after a dedicated identity
study. That is an operator call, and it is the one place where the cheap path
and the durable path share a first step.
