# finding-010: a send with an explicit workspace is accepted for a mailbox nobody reads, and an unknown workspace is accepted too

- **Date:** 2026-09-27
- **Session:** arcaven-builder-g5-0, placing the 2026-09-27 harvest of another team's seats
- **Subject:** director-mcp send resolution (R-92), the explicit `workspace` argument
- **Confidence:** two live instances from seat harvests; the mechanism read in `probe/nats-phase-0/director-mcp/bus.go` at 02a3188

## 0. The sentence

**R-92 refuses a send nobody would consume only when it resolves the
workspace itself. An explicit workspace skips that check, so a wrong one
builds a subject nobody filters, and the send still returns "accepted".**

## 1. What was observed

- A supervisor seat sent nine messages over about three hours to the
  director's address with the `workspace` argument set to a workspace the
  director was not live in. Every send returned "accepted for delivery". The
  director read none of them. The seat found the gap through `list_roster`,
  not through any send result.
- A seat sent an empty message to itself with a workspace value that named no
  workspace at all. It was accepted, not refused.

## 2. Mechanism

`subjectWorkspace` (bus.go, around line 507) uses an explicit hint verbatim
and resolves from live presence only when the hint is empty. The comment says
why: "so a cold mailbox with no live presence can still be addressed". The
only check on a hint is `validToken`, which asks whether the value is a legal
subject token, not whether any session lives there. The AGENT_INBOX stream
captures `agent.*.*.*.inbox`, so the message is stored, but no durable filters
that subject, so no session ever reads it.

So the explicit argument trades R-92's liveness check for the ability to
reach a cold mailbox, and a typo or a stale workspace pays for that trade in
silence. The sender cannot tell the two apart.

## 3. What this does not establish

Whether the explicit form should refuse, warn, or stay as it is is a design
call. Reaching a cold mailbox is a real need (a seat that is down but will
return). One shape that keeps both: accept the explicit send, and say in the
result that no live session reads that subject, the same fact R-92 already
computes on the resolved path.

## Related

- R-08 (accepted is not delivered or read), R-92 (refuse a send nobody would consume)
- director#121 (broadcast, the same "accepted for no reader" class on another path)
- Loss-reduction item 6 (a refused send leaves an audit record): does not cover this, since nothing is refused
