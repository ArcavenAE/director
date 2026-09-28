# finding-012: a result addressed to a seat that no longer exists has no route back to the seat that asked for it

- **Date:** 2026-09-27
- **Session:** arcaven-builder-g5-0, placing the 2026-09-27 seat harvest
- **Subject:** director-mcp addressing across a seat restart
- **Confidence:** one live instance from a seat harvest; mechanism inferred from the addressing model, not tested on a scratch broker

## 0. The sentence

**Mail follows an agent id, not a request, so a reply to an id whose session
has ended waits on a subject nobody will read again, and the seat that
originated the request has no way to learn the result exists.**

## 1. What was observed

A research supervisor asked for work that was dispatched to an author seat.
The author seat was replaced by a successor with a new id. The result was sent
to the old id. The requester learned of it only because a supervisor's
restart welcome happened to mention it. Nothing on the bus routed the result
to the requester, or to the successor.

## 2. Mechanism, as far as it is read

An agent address resolves to one inbox subject per id. A successor with a new
id reads a new subject. Nothing in the envelope names the original requester,
so even a reader of the dead inbox could not forward by rule. This is the
agent-address sibling of director#85 (role mail sits on a role subject no
session reads), and of the stale-id cause recorded as orc finding-169 cause 4.

The envelope declares `expires_at` (probe/nats-phase-0/director-mcp/envelope.go; optional in docs/shim-reference.md) and the ledger writes it, but `git grep expires_at` on director main finds no reader, so an ask to a dead holder has no expiry to trip either.

## 3. What this does not establish

Whether the send was accepted or refused at the time. If the dead seat's
presence had expired, R-92 would refuse the send; if the record was still
live, it would be accepted. The harvest does not say which. Either way the
requester was never told.

## Related

- director#85 (role:// mail accepted and never read)
- Loss-reduction items 3 and 4 (an unread-age reader, and a sender that holds an ask until it is read): the instruments that would show this ask as unread
