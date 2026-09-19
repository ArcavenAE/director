# Party log: director session identity and the seat (2026-09-18)

Subject: DECIDE the director-session-identity scheme the crossing enable needs. The crossing
was deferred because "resume the director seat in global mode" front-runs this design: today
the seat is grabbed by address, and the address collides. Decide four things the operator
named: a distinct stable identity at spawn (today every session on this host is
`agent://ops/michael`), the human-director seat's authority, shiftchange custody without
losing the seat, and what stops a session falsely taking the seat. Ground in R-01, R-05,
R-06, and "there are no peers." Redaction held: no origin organization, its short environment
tokens, its infrastructure repository, or the deployed hub hostname.

## Grounding read before the room opened

This is a decision over well-developed design, not a greenfield search, so I read the design
before casting rather than after.

- `sim/design/identity-at-spawn.md`: the collision root cause (identity from the OS user via
  a project-scope MCP config, so every session is `agent://ops/michael`; two shims bind the
  same `mcp_michael` durable on one filter subject and RACE for delivery, so the loser
  silently loses mail, the original silent-drop defect re-entering through identity); the
  three fused concepts (address, seat, durable conversation identity); options ID-A
  (launcher-assigned name), ID-B (self-minted DID + JWS card), ID-C (hybrid); the
  durable-consumer race fix (unique per session, structural). The ID-A demo exists at
  `probe/nats-phase-0/id-a-demo/director-session.sh`.
- `sim/design/director-seat-lease.md`: the seat as a fenced KV lease on the Phase-0 broker's
  JetStream primitives. Create-only single-holder, fencing token = acquisition revision,
  shim-driven renewal (SEAT-D), fail-closed (SEAT-E), TTL vacates a dead holder, a zombie
  fenced by token; a distinct envelope `authority` block; SEAT-A through SEAT-G with an
  adversarial pass.
- `sim/design/session-identity-and-succession-options.md`: the five threads and the "there
  are no peers" doctrine, explicitly options-not-ruled, which is what this party rules.
- `sim/requirements.md`: R-01 (verifiable principal), R-05 (receiver validates from the
  director), R-06 (a restart changes identity and address, key on durable identity), R-86
  (the two-tier bus; an identity minted local must route global without a rename), R-93
  (presence is not liveness).

So the options are surfaced and grounded; the room's job is to rule among them and tie the
ruling to the crossing.

## Cast

Five seats, each a distinct discipline.

- Sol: distributed systems, leases and fencing. Owns the seat mechanism.
- Ida: identity and the principal model. Owns address vs seat vs durable id.
- Vere: security and nonrepudiation. Owns R-01, R-05, and the crypto axis.
- Hollis: operator experience, the human's seat. Owns the stuck-lease-locks-out-the-human
  risk and the shiftchange from the human's side.
- Prue: the proportionality skeptic and doctrine keeper. Owns "there are no peers" and the
  physical-access-phase scoping, and pushes hard against building the crypto plane early.

## Round 1: opening positions

**Ida:** Start from the fused concepts, because the collision is one bug wearing three hats.
Address is where you reach a session. Seat is whether a session is the human's director.
Durable conversation identity is the handle R-06 says survives a restart. The michael bug
fused address with the OS user and left the seat undefined. My position: ID-A ships now, a
launcher-assigned distinct name with `--strict-mcp-config` so the baked config cannot override
it, plus the durable-consumer fix so a name is unique per session and a duplicate id can never
race for mail. ID-C is the trajectory, the DID attaches to the same name later. Not ID-B now.

**Sol:** And the seat is not a name, it is a lease. Create-only KV on `seat.<workspace>.director`,
single-holder falls out of the primitive, the fencing token is the acquisition revision, and
the dead holder's silence is the release because the TTL vacates it. The one thing I will die
on: renewal is driven by the shim's own timer, never by the model calling a tool. A model turn
runs for minutes; if renewal shared the model's poll cadence a long turn would vacate a seat
that is very much held. SEAT-D is not a tuning note, it is the design.

**Vere:** I want to name what the fencing token does and does not do before anyone leans on it.
It proves recency: the sender holds the current lease. It does not prove identity; nothing is
signed, so a session that learned the token could forge a seat message. That is the R-01 and
R-05 gap. My opening question to the room: do we close that gap now, or name it and defer.

**Prue:** Defer, and I will spend the round on why. There are no peers. Every session is the
human's own, on the human's machine, so the receiver's real question is "did the human's
director authorize this," and the trust boundary is physical access to the human. The token
addresses an accident, a stale or zombie holder, which is the risk we actually have. A signed
principal addresses an adversary who learned a token, which is a risk we do not have yet.
Build the plane when the boundary leaves the host, not before.

**Hollis:** I have one fear and it outranks elegance: a stuck lease that locks the human out of
their own seat is worse than a brief double-address. Reyes named this and I am holding it. A
lease is right only if a dead or wedged holder frees the seat fast and a live human can always
get back in. So before I sign the lease, I want the failure story: what happens when the
holder is wedged but not dead, and how fast does the human recover the seat.

## Round 2: the clashes

**Hollis vs Sol, on the stuck seat.**

**Hollis:** Answer the wedged holder. Not the clean crash, the CAS-still-working-but-the-agent-
is-useless case.

**Sol:** Two answers, and they are different failures. A dead holder: it stops renewing, the
TTL ages the key out, the seat is vacant, the next Create wins. No cooperation needed. A holder
that cannot reach the broker: its renewal CAS fails or times out, and SEAT-E says it must fail
closed, stop emitting seat authority at once, so meanwhile the TTL expires and a successor
acquires. The cost is a brief vacancy during the blip, and a vacancy beats two seats.

**Hollis:** And the wedged-but-connected holder, the one that renews fine but is doing nothing
useful?

**Sol:** That one the lease does not solve, and I will not pretend it does. Grant liveness is a
third axis, separate from process liveness and credential liveness. A session can be
process-alive, renewing its lease, and useless. Forcing it out is a human judgment, so the
answer there is an operator override, not an automatic fence. The lease guarantees single-
holder and prompt release of a DEAD holder; it does not adjudicate a live-but-bad one. That is
honest, and it is the right scope.

**Hollis:** Then I sign the lease, with the override named as the human's escape hatch, and
SEAT-D as the thing that keeps a long model turn from looking wedged when it is only thinking.
That last point is the whole reason I was nervous, and it is already in the brief.

**Vere vs Prue, on closing the crypto gap now.**

**Vere:** I will push once more. The moment the crossing puts a principal on the global tier,
R-86 says it writes the bus from another host. That is the first place the credential binding
stops being optional. So is the crossing itself the boundary leaving the host?

**Prue:** No, and this is the important distinction. The crossing in scope is the human's own
clusters leafing to the human's own tier. It is multi-HOST, but it is single-TRUST-boundary:
every cluster is the human's, reachable because the human owns the launcher on each. The
boundary leaves the host when a SECOND operator, or an off-host session the human does not
physically control, joins. That is the gateway-to-another-person case, not the leaf-to-my-own-
tier case. So the crossing does not cross the trust boundary, and the fencing token stays
proportionate for it.

**Vere:** I accept the distinction, and I want it written down, because it is the load-bearing
reason the token is enough: multi-host is not multi-trust-boundary while every host is the
human's. When the gateway to another person lands, the signed principal lands with it. Name
the axis now so the token is not a dead end, build it then.

**Prue:** Agreed, and that is the doctrine cut: a signed grant from the minting authority
answers the receiver's only question, and in this phase the minting authority is the launcher
and the grant is the fencing token. The signature is the same shape one boundary out.

**Ida vs Sol, on how the cluster label and the seat relate.**

**Ida:** The crossing names a cluster label, and I do not want anyone thinking that label is
the session's identity. The label is the routable NATS token from the other party, per-tier
unique. The session address is the spawn-assigned id. The seat is a lease key. Three things,
and the crossing touches all three without fusing them.

**Sol:** Say the seat key, because that is where the confusion will land. It is
`seat.<workspace>.director`, and it resolves to whoever currently holds it. It is NOT the
cluster label and it is NOT the session id. In fact the seat key looks exactly like the
resolution target for `role://{workspace}/director`, so we may later make them the same
mechanism, but that is an open question, not a decision for tonight.

**Ida:** Then the crossing enable is two clean actions: name the cluster label, and resume the
seat as a lease acquisition with a spawn-assigned id. The reason the crossing front-ran the
design is that "resume the seat" meant grabbing the michael address, which is neither of those.
With the lease it is a create-only acquisition, single-holder and fenced. That is the design
the crossing was missing, stated in one sentence.

**Prue:** And that sentence is the deliverable. The crossing does not need the whole durable
plane. It needs ID-A, the lease acquisition, and writer-side deregister so a prior holder does
not linger. Three things.

## Round 3: converge and decide

**Prue:** Say the whole thing in the shape the operator will read, and I will object if it grew
past what the crossing needs.

**Ida:** Identity at spawn: ID-A now (launcher-assigned name, strict-mcp-config, per-session
durable), ID-C trajectory, ID-B deferred. Seat: the fenced KV lease, shim-driven renewal
(SEAT-D), fail-closed (SEAT-E), envelope authority block. Shiftchange: acquire not inherit, a
two-phase terminal marker so no live double-hold, specializing marvel's slot. False-seat:
launcher minting authority plus the fencing token now, signed principal and nonrepudiation log
deferred. Liveness: writer-side deregister now plus the reader-side cutoff, serving R-93,
receipt-based liveness the doctrine.

**Prue:** It did not grow. The minimal unblock inside that is ID-A plus the lease plus
writer-deregister. I object to nothing.

**Sol:** With SEAT-D flagged as the one that breaks the seat if missed. A long model turn must
not read as a vacancy.

**Vere:** And the boundary distinction written down: multi-host is not multi-trust-boundary
while every host is the human's, which is why the token is proportionate for the crossing and
the signature waits for the second operator.

**Hollis:** And the human's escape hatch named: a live-but-useless holder is a human override,
not an automatic fence; the lease frees a DEAD holder promptly and guarantees single-holder,
and that is what it promises, no more.

**Ida:** Unanimous. Address, seat, and durable identity stay three separate things. The
crossing re-clears with a design: name the label, acquire the seat, spawn with a distinct id.

## Convergence

The room ruled among options that were surfaced but not decided, and tied the ruling to the
crossing. The two real arguments resolved cleanly: the stuck-seat fear onto the dead-vs-wedged
distinction (the lease frees the dead promptly and guarantees single-holder; a live-but-useless
holder is a human override, not an automatic fence; SEAT-D keeps a long model turn from reading
as a vacancy), and the close-the-crypto-gap-now push onto the trust-boundary distinction
(multi-host is not multi-trust-boundary while every host is the human's, so the fencing token
is proportionate for the crossing and the signed principal lands with the second operator). The
decision, the minimal crossing unblock, the five-thread table, the load-bearing SEAT-D
constraint, and the one operator call (spawn token now or with the M1 study) are in
`recommendation.md`.

## Sources the panel grounded on

The three director design docs (`identity-at-spawn.md`, `director-seat-lease.md`,
`session-identity-and-succession-options.md`), the requirements register (R-01, R-05, R-06,
R-86, R-93), findings 003 (the identity collision) and 166 instance (c) (stale presence on
exit), and the external anchors those docs already cite and verify: NATS KV Create as an
atomic single-winner (expected-last-subject-sequence 0), JetStream durable-consumer sharing
(two clients on one durable race, they do not each get a copy), and the A2A v1.0 identity model
(a W3C DID plus a JWS-signed AgentCard, identity out-of-band) as the named crypto axis.
