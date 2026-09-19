# The minimal physical-access grant for the director function

Status: SPEC, held for the operator and for marvel-builder's new-code-or-covered call.
Crossing-critical. Date: 2026-09-18. Redaction held: no origin organization, its short
environment tokens, its infrastructure repository, or the deployed hub hostname. Feeds the
director requirements register.

## The question

What establishes "this session holds the director function" out of band in the lightweight
no-crypto phase, and how does a participant verify it per-message within our single trust
boundary? And, for marvel-builder: is the crossing's grant item new code or already covered?

## The answer: it is already coded, as `sender.role` backed by ID-A

The grant is the envelope's `Sender.Role`, set from the launcher's `DIRECTOR_GLOBAL_ROLE`,
backed by `Sender.AgentID` (the ID-A identity). Grounded in
`probe/nats-phase-0/director-mcp`:

- `envelope.go:13` Sender already carries `AgentID` (the ID-A identity), `Role` (the function
  held, json `role`), `Principal` (RESERVED, null in Phase 0, the crypto-phase slot), and
  `Session`.
- `global.go` `loadGlobalConfig` reads `DIRECTOR_GLOBAL_ROLE` into `Role`, validates it as a
  closed set {supervisor, director}, reject-not-rewrite (R-76/R-94), and uses it to derive the
  self-address (`global://director`), the inbox, the stream, and the presence key.
- `global.go` `globalDurable(agentID, instance)` is per-SESSION, so two sessions never bind one
  durable and race each other's mail (R-50).

So every envelope already carries who is speaking (`agent_id`, ID-A) and what function it holds
(`role`), and reserves the crypto upgrade slot (`principal`). The grant plumbing exists.

## Out of band, and why it is a valid grant in this phase

The grant is out of band because the LAUNCHER sets `DIRECTOR_GLOBAL_ROLE`, not the message
content (the authority-never-in-content principle). In the physical-access phase the launcher
is the minting authority and the human owns the launcher on every host, so a launcher-set role
is the human's own authorization. This is the "there are no peers" doctrine: the receiver's
question is "did the human's director authorize this," and a launcher-set role answers it.
`--strict-mcp-config` keeps a well-behaved session from overriding the baked env; a session
that controls its own env to forge a role is out of scope while physical access is the trust
boundary, the same scope decision ID-A already made.

## Per-message verification within the single trust boundary

A receiver reads `sender.role` off the envelope and trusts it, because only the human's
launcher sets `DIRECTOR_GLOBAL_ROLE`, every participant on the bus is the human's (there are no
peers), and `presence.director.<instance>` shows a live holder. This is trust-the-boundary
verification, proportionate, the same rigor as ID-A (a launcher-set label, not a crypto
attestation). The honest limit: `sender.role` is a label, not an attestation; a session
forging its own env is out of scope now. The upgrade path already has its slot,
`sender.principal` (RESERVED null), which becomes the signed grant (A2A DID plus JWS) when the
trust boundary leaves the host, so the label is not a dead end.

## Is the crossing's grant item new code or already covered? ALREADY COVERED.

`DIRECTOR_GLOBAL_ROLE=director` (launcher-set) backed by ID-A (`DIRECTOR_AGENT_ID`) already
establishes and stamps the director function on every envelope. marvel-builder does not need to
build a grant mechanism for the physical-access phase. No KV grant record, no lease, and no
signed grant is needed now.

## So the crossing enable, precisely, now is

ID-A (a distinct `DIRECTOR_AGENT_ID`) + `DIRECTOR_GLOBAL_ROLE=director` (the function grant,
launcher-set, already coded and already stamped on the envelope as `sender.role`) +
`DIRECTOR_GLOBAL_DOMAIN` + `DIRECTOR_CLUSTER` (the D1 enablement switch, already coded) +
writer-side deregister. The single-seat lease acquisition is REMOVED and NOT replaced by new
grant code.

## What the function-model reframe leaves open (not crossing-blocking)

The single-holder assumption was never in the grant. It lives at the address and inbox layer,
and it only bites when a SECOND director-holder actually comes online, which the crossing's
first cut (one director) does not require. So none of these block the crossing:

- **Co-holder inbox semantics (wants an operator ruling).** Two director-holders each bind
  their own per-session durable on the same `global.director.inbox` subject, so with distinct
  durables each currently receives ALL director mail (fan-out, not a race, because the durables
  are distinct). Is that the wanted multi-holder semantics (both holders see everything and
  coordinate), or should co-holders share via a queue-group (one delivery, load-shared)?
  Fan-out versus share is an operator ruling; it is not a grant or a crypto question, and it is
  moot until a second holder exists.
- **Per-holder addressing.** The self-address is per-role (`global://director`), so two holders
  share one send-address. If addressing one specific holder is wanted, the address needs the
  instance (the presence key already carries it). A small refinement, deferred until per-holder
  addressing is needed.
- **The lesser roles.** `sender.role` is a closed set {supervisor, director} at the global tier
  today. Extending it to analyst, auditor, assistant, and user is a taxonomy extension of that
  closed set plus each role's subject and authority scoping. That is the function-block-diagram
  and taxonomy work (the next step), not the crossing.

## Candidate requirement

The physical-access grant for a bus function is the launcher-set `sender.role`, backed by the
ID-A `sender.agent_id`, verified per-message by trust-the-boundary (there are no peers);
`sender.principal` is the RESERVED crypto-upgrade slot. Multi-holding is not constrained by the
grant; co-holder delivery semantics (fan-out versus queue-group) is a per-function policy, and
for the director function it wants an operator ruling.
