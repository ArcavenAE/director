# Design brief: the director seat as a fenced KV lease

Option-set 2 from the identity roundtable. The human-director seat is a
capability, not a name and not an address. Exactly one session holds it at a
time. This brief develops the mechanism on the NATS JetStream primitives the
Phase 0 probe broker already runs, grounds the envelope field, runs an
adversarial pass, and proposes candidate requirements.

## 1. The mechanism

### 1.1 The seat is a lease, held in a KV key

Reuse a JetStream KV bucket (the probe already runs `AGENT_STATE` for presence;
the seat can share it or take a sibling bucket `AGENT_SEAT`). One key names the
seat:

```
seat.{workspace}.director        e.g. seat.aae-orc.director
```

The value is a small record:

```json
{
  "seat": "director",
  "workspace": "aae-orc",
  "holder_agent_id": "cc-planner",
  "holder_session": "uuid-of-the-current-session",
  "fencing_token": 41,
  "acquired_at": "2026-09-12T14:20:00Z",
  "renewed_at": "2026-09-12T14:22:30Z"
}
```

`holder_agent_id` is the address of the session currently holding the seat. It
is recorded for observability, it is not the authority. The authority is the
`fencing_token`.

### 1.2 Acquire: create-only, so exactly one winner

Acquisition is a KV Create (create-only put) on the seat key. NATS KV Create
maps to a JetStream publish with expected-last-subject-sequence 0, which the
server accepts only if the key does not currently exist. Under a concurrent
race, exactly one create commits and every other caller gets a wrong-sequence
error and does not hold the seat. Single-holder falls out of the primitive; no
external lock is needed.

### 1.3 The fencing token is the acquisition revision

Every KV write returns a strictly increasing revision (the JetStream stream
sequence). Read the revision returned by the winning Create and store it in the
value as `fencing_token`. It is monotonic and server-issued, so there is no
separate counter to keep and no client clock involved.

The token changes only on ACQUISITION, never on a heartbeat. A successor that
acquires a vacated seat writes a strictly higher token (the new create lands at
a higher stream sequence than any prior write to that key). So the token
answers one question: is this the current grant, or a superseded one.

### 1.4 Heartbeat renewal refreshes the lease

The holder renews by a compare-and-set Update on the seat key: expected
revision = the revision it last saw, new value = the same record with
`renewed_at` bumped. A successful CAS refreshes the bucket-TTL age of the key
(each write resets the entry's age under a bucket MaxAge). The stored
`fencing_token` is PRESERVED across renewals; only `renewed_at` moves.

Renewal failure is definitive: a CAS conflict (someone else wrote the key) or a
missing key means the holder has lost ownership. On renewal failure the holder
must stop acting as the seat at once (see 2.2, fail closed).

### 1.5 TTL expiry vacates the seat with no cooperation

The seat key carries a bucket TTL. If the holder dies and stops renewing, the
key ages out and is deleted. The seat is then vacant and the next Create
succeeds. The dead holder does nothing; its silence is the release. This is the
answer to "what happens when the old session is unexpectedly unavailable": the
lease expires on its own.

### 1.6 The zombie is fenced by token, not by cooperation

A revived holder that went dark for longer than the TTL still believes it holds
the seat with `fencing_token: 41`. But the key expired and a successor
re-created it with `fencing_token: 42` (strictly higher). Two independent
defenses catch the zombie:

- If it tries to RENEW, its CAS on the old revision fails; it learns it lost the
  seat and self-demotes.
- If it EMITS a seat-authority message carrying token 41, a receiver that reads
  the current seat record sees token 42 and rejects 41 as stale.

## 2. Where the token rides, and how a receiver validates it

### 2.1 The envelope field

Seat authority is not identity and not content, so it belongs in neither
`sender.principal` (RESERVED for the identity plane) nor `content`. I propose a
distinct top-level `authority` block, present only when the sender is asserting
seat authority:

```jsonc
"authority": {
  "seat":          "director",              // the role being asserted
  "seat_key":      "seat.aae-orc.director", // which KV key proves it
  "fencing_token": 41                       // the acquisition revision held
}
```

This satisfies R-01 (who-speaks-with-what-authority is a field, not prose) and
R-02 (authority strength is stated, never inferred). It keeps three things
apart that the michael collision had fused: `sender.agent_id` (the routing
address), `sender.principal` (identity, RESERVED), and `authority.seat` (the
grant). The envelope doc's `role://{team}/{role}` address resolves to whoever
currently holds the matching seat key, so the seat lease can BE the
role-binding resolution the envelope doc left open for the no-marvel case
(section 4, "roster file + role-binding resolution without marvel").

### 2.2 Receiver validation

On an inbound envelope carrying `authority.seat`, the receiver:

1. Reads the current value of `authority.seat_key` from the KV (one KV get).
2. Accepts the seat claim iff the key exists (seat not vacant) and
   `message.authority.fencing_token == value.fencing_token`.
3. Rejects otherwise: a strictly lower token is a superseded holder (fenced); a
   missing key is a vacant seat (no one holds this authority right now).

This is the receiver-side validation R-05 asks for, with one honest limit
stated in 3 below.

## 3. What this proves, and what it does not (R-05, R-45, physical access)

The fencing token proves RECENCY of a grant: the sender holds the current seat
lease. It does NOT prove identity. A session that learned the current token
could forge a seat-authority message, because nothing here is signed. So the
check is a partial R-05 (validate a message is from the current director): it
validates currency of the grant, not nonrepudiation of the sender.

That is proportionate while physical access to the human is the trust boundary,
the operator's scoping for this phase. Every session is the human's own, on the
human's machine; the risk the token addresses is an accident (a stale or zombie
holder), not an adversary who has learned a token. When the trust boundary
leaves the host, the crypto axis closes the gap: A2A v1.0 keeps identity
out-of-band (a W3C DID the session owns, a JWS-signed AgentCard) and the seat
message would then be signed by the holder's key, so a learned token alone
cannot forge it. Name the axis now so the token does not become a dead end.

Liveness axes stay distinct (extends R-45): a session can be process-alive,
credential-alive, and NOT hold the seat. Seat-holding is a third liveness axis
(grant liveness), separate from process liveness and credential liveness.
R-06 is satisfied by decoupling: a restart changes the address, but the seat is
re-acquired (new token) rather than inherited by name, so seat authority never
rides on an address that a restart invalidates.

## 4. Adversarial and failure pass

- **Receiver does not check the token.** Then seat authority is unenforced and a
  stale or forged token is accepted. Director cannot force a foreign receiver to
  check; the check has to be part of director's portable receiver-side policy
  (R-38, R-39), and any harness that ignores it is the "borrowed safety"
  problem restated. Honest limit: the seat protects the honest and the compliant,
  not the non-compliant. Acceptable in the physical-access phase.
- **Split-brain during a network blip.** The holder cannot reach the broker to
  renew. Its CAS fails or times out. Per 1.4 it must fail closed: stop emitting
  seat authority at once. Meanwhile the TTL expires and a successor may acquire.
  When the blip heals the old holder is a fenced zombie. Cost: a brief seat
  vacancy during the blip. That is the correct trade, a vacancy beats two seats.
- **Two sessions race to acquire.** KV Create is atomic (expected-sequence 0).
  Exactly one wins; the loser errors and does not hold the seat. Single-holder
  holds under concurrent acquire.
- **Clock and TTL skew.** TTL is enforced by the broker clock, not client clocks,
  so client skew cannot cause premature expiry or two holders. The renewal
  interval must be safely under the TTL (renew at roughly TTL/3) to tolerate one
  missed beat.
- **Long model turn reads as false vacancy (the important one).** If renewal
  depended on the MODEL calling a tool, a long turn (a model turn can run for
  minutes) would miss the TTL window and vacate a seat that is very much held.
  The receive-is-a-poll finding (159/160) says the model must poll to RECEIVE;
  liveness renewal must NOT share that cadence. Renewal has to be driven by the
  always-running shim on its own timer, independent of the model's turn. This is
  a real design constraint, not a tuning note, and it is the highest-value item
  in this brief.

## 5. Candidate requirements (provisional tags, source-classed)

- **SEAT-A.** The human-director seat is a capability held as a lease, not an
  identity and not an address; a successor ACQUIRES it, it is never inherited by
  name. Source: JUDGMENT (design conclusion), grounded in the OBSERVED michael
  collision that fused identity and address.
- **SEAT-B.** Seat holding is proven by a monotonic fencing token issued at
  acquisition; a receiver validates a seat-authority message by comparing its
  token to the current seat record and rejects a stale token. Source: JUDGMENT.
  Earned by: the zombie and split-brain analysis; NATS KV revision as the token.
- **SEAT-C.** The fencing token proves RECENCY of a grant, not identity; it is
  proportionate while physical access is the trust boundary and does not satisfy
  R-05 nonrepudiation, which needs the crypto axis (A2A DID plus JWS-signed
  AgentCard) when the boundary leaves the host. Source: RULED (operator scoped
  security as premature now) plus JUDGMENT.
- **SEAT-D.** Liveness renewal (presence and seat) must be driven by the
  always-running shim on its own timer, never by the model calling a tool,
  because model-turn latency is unbounded and would otherwise read as false
  vacancy. Source: JUDGMENT, derived from R-19 and the receive-is-a-poll finding.
- **SEAT-E.** On renewal failure (CAS conflict or broker unreachable) a holder
  must fail closed: stop emitting seat authority immediately, accepting a brief
  vacancy over split-brain. Source: JUDGMENT.
- **SEAT-F.** Seat authority rides the envelope as a distinct `authority` block
  (seat role, seat key, fencing token), never in the message body, distinct from
  sender.principal (identity) and content. Source: JUDGMENT, grounds R-01 and
  R-02 for the seat case; A2A keeps authority out of the payload.
- **SEAT-G.** Seat liveness is a third liveness axis (grant liveness), distinct
  from process liveness and credential liveness (R-45). Source: JUDGMENT.

## 6. Open questions

- One seat per workspace, or one per team (`seat.{ws}.director` vs
  `seat.{ws}.{team}.director`)? Several teams may each want a director seat.
- Does a session hold the seat on behalf of the human, or can the human occupy
  it directly? Working assumption: the seat is held by the human's assistive
  session (director), the human is the principal behind it.
- Unify the seat lease with `role://` resolution? The seat key looks exactly
  like the resolution target for `role://{ws}/director`; making them the same
  mechanism removes the envelope doc's separate "roster file without marvel"
  path. Worth confirming.
- Seat TTL value: what balances fast failover against tolerating slow renewals
  and brief blips? Ties to SEAT-D (shim-driven renewal) and to the presence TTL.
- Receiver enforcement across non-compliant harnesses: how much does unenforced
  seat authority actually matter in the physical-access phase, and does that
  change the priority of the crypto axis.
