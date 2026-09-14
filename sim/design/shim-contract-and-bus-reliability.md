# Design brief 5: shim contract and bus-reliability lessons (builder harvest)

Captured 2026-09-13 by builder, at the director's request. Several of these
lessons lived only in bus INFORMs and would be lost if a session ended, so this
is their durable, published home. It gathers the shim, bus, and identity-
contract lessons from the director#3/#10 identity work, the director#12 empty-
body guard, the director#13 canonical-emitter refresh, and the b69n schema-first
contract work. It is the single referenceable path for the cross-cutting comms-
reliability synthesis. Candidate register additions are flagged inline; the
empty-body incident's fuller operational log lives in the local (unpublished)
sim/notes and is summarized here where it is load-bearing.

Register-grade throughout: each lesson is a way an "accepted for delivery" ack,
or a contract, can be true on its face and hollow underneath. That gap is the
class director exists to catch.

## 1. A fixed source is not a fixed running binary

A stale MCP subprocess serves its old build until an operator restarts it, so
"bug fixed in source" and "fleet healthy" are different states separated by a
per-session restart the session cannot perform on itself. The empty-body
regression is the proof: director#12 corrected the source while the planner and
builder shims, started before the rebuild, kept publishing empty bodies;
sessions that had restarted (marvel-builder, architect) sent full bodies.

Elevation for the synthesis: deploy and restart discipline is part of the
contract, not an operational afterthought. A reliability claim about the bus is
a claim about the running binaries, not the merged source. A fleet health check
that reads only merge state will call a half-broken fleet healthy.

## 2. The audit-stream diagnostic method (reusable silent-loss probe)

The AGENT_AUDIT JetStream stream mirrors every envelope at publish, so it
decides send-side loss against delivery-side loss in one pass. The method,
reusable for any "did the body actually go out" question:

```
nats stream get AGENT_AUDIT <seq> --json
# then base64-decode the .data field and measure content.data length
```

Scan by `sender.agent_id`, read `content.data` length per seq, and the boundary
between full-body and empty-body sends dates the regression to a rebuild window.
This localized the empty-body incident to specific shim instances (planner and
builder empty at seqs 135/136/141; marvel-builder and architect full). Keep the
audit mirror as a ratified invariant precisely because it makes this decidable.
It is the load-bearing diagnostic, not a convenience.

## 3. Emit-path policy is not schema validation (R-87)

The canonical envelope JSON Schema makes `content.data` optional: a signal
carries no body, a pointer carries refs rather than data. So a schema validator
(`envelope.Validate()`) cannot catch an empty body on a body-bearing type
(text, task, result). The non-empty-body requirement (R-87) must live on the
emit path, before any ack, in every producer. Folding it into schema validation
would silently drop the guard the moment a producer adopts canonical validation.
Documented as a caveat in marvel #249's README (commit 19efa39) and enforced as
an emit-path policy layer in director#12 and director#13.

## 4. Validate on emit, stay lenient on receive (migration discipline)

When a contract migrates across a fleet of independently-restarted shims, a
producer that has adopted validation must not reject or poison traffic still in
flight from un-migrated producers. The discipline: validate the envelope on
EMIT only; keep RECEIVE lenient. A migrated shim then never fails a peer's
older-but-valid frame, and the fleet converges without a flag day. Applied in
director#13 (emit-only validation in the publish path; receive unchanged). This
is the safe-migration counterpart to lesson 1: because shims restart at
different times, the contract must tolerate a mixed fleet on purpose.

## 5. Import, do not vendor; the extraction trigger is a toolchain need

Architect ruling (relayed via marvel-builder, 2026-09-13), superseding the
earlier vendor answer and the third-consumer framing: the shim IMPORTS marvel's
Go package (`github.com/arcavenae/marvel/contracts/go/envelope`) rather than
vendoring a copy. beadle keeps a sha-pinned schema copy plus a drift guard
because Rust cannot import a Go package; that is a language boundary, not a
policy difference. The trigger for extracting a standalone shared module is a
fleet-wide TOOLCHAIN need, not merely a third consumer arriving. A pseudo-
version import against a branch commit builds and stays small when the imported
package pulls few dependencies (only the validator came along here). Residue:
director#13 pins a pseudo-version against the #249 branch commit and repins to
the release once #249 merges.

## 6. Reject, never rewrite, for namespace tokens

director#3 root cause: a `sanitize()` step rewrote identity tokens to a safe
charset, and that rewrite HID a collision. `ops.planner` and `ops_planner`
sanitized to the same presence key while addressing distinct inboxes, so a
rewrite silently merged two identities. The fix validates and REJECTS an
out-of-class token at both boundaries (at spawn and at the send boundary in
`resolveSubject`), against the closed identity-token class R-76
(`[A-Za-z0-9_-]`). The principle: for anything that names a principal, a rewrite
that "helps" by coercing an invalid token into a valid one destroys the
distinction the token exists to carry. Reject and make the caller fix it. This
is R-02/R-03 in the token layer: identity is stated exactly or refused, never
silently transformed into something adjacent.

## 7. Authority is none/null for seatless self-authored sends

A phase-0 shim speaking on its own behalf, with no relayed seat, emits
`authority { "strength": "none", "seat": null }` for send and for broadcast.
The shim never upgrades `none` to `relayed` until the identity-lane seat model
exists (architect rider, 2026-09-13). This keeps R-02/R-03 honest at the wire:
the envelope claims no authority it did not earn, and a later reader cannot
mistake a self-authored frame for a delegated one. Emitting the block explicitly
rather than omitting it is deliberate; absence would be ambiguous, while
`none`/`null` is a stated fact.

## 8. Codegen drops unknown claims from open objects

Both typify (Rust) and go-jsonschema (Go) render an open object
(`additionalProperties: true`) as a CLOSED struct and silently drop any field
not named in the schema. For a reserved or extensible field that carries claims
not yet modeled, that is data loss at deserialization. Represent open fields as
`serde_json::Value` (Rust) or `interface{}` (Go) so unknown claims survive a
round trip. In the envelope: `principal` is open, so it is `Value`/`interface{}`;
`seat` is schema-closed and stays typed, because the `epoch` inside it is a
fencing token read for enforcement (R-55/R-69), and a typed read is correct
there. typify also replaces only named `$defs`, not inline subschemas, so the
beadle codegen post-processes the generated source to rewrite `principal` to an
optional Value and delete the generated struct. The divergence is intentional
and flagged to marvel-builder.

## Pointers to what is committed

- director#10 (branch `fix/director-3-identity-token-class`, commit 22aa4d8):
  identity validate-reject (lesson 6) plus shim credential options.
- director#12 (branch `fix/reject-empty-content-body`): the R-87 emit-path
  empty-body guard (lesson 3).
- director#13 (branch `feat/shim-canonical-envelope`, stacked on director#3):
  canonical emitter, emit-only validation (lesson 4), authority none/null
  (lesson 7), marvel pseudo-version import (lesson 5, repin pending #249).
- beadle#62 (branch `feat/b69n-envelope-rust`): Rust envelope types and
  validator, principal-as-Value post-processing (lesson 8).
- marvel #248 (frozen canonical schema), #249 (Go reference package and the
  R-87 README caveat).
