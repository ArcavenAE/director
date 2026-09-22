# finding-005: cross-host director mail fails on the consumer side, not the wire

Date: 2026-09-21. Subject: director (cross-host inbound mail). Status:
diagnosis complete; fix in flight (l15b5 + 7gnvo). Supersedes the working
hypothesis that a duplicate `global://kinu/director` holder was intercepting.

## Symptom (O-23)

Replies sent by mokuzai (skippy) seats to `global://director` never reach the
kinu director inbox. The bus accepts them ("accepted for delivery"), so from
the sender's side and from the director's send-side the channel looks healthy.
The director simply never receives, so every cross-host reply is silently
lost and the operator's assistive agent is blind to remote status.

## What was ruled out

Issue aae-orc#391 surfaced a duplicate holder of `global://kinu/director` on
mokuzai (a builder carrying a director role claim). The hypothesis was that
the duplicate was splitting or intercepting director mail. The operator
disabled it and we ran a clean return-path test:

- errand seat replied to `global://director`; the message landed on
  `GLOBAL_TO_DIRECTOR` at seq 74 (confirmed on the stream).
- the kinu director inbox drained nothing, twice.

Disabling the duplicate did NOT fix it. Negative result, recorded rather than
harmonized: the duplicate director was a real addressing defect but not the
cause of O-23.

## Cause

Consumer-side. The director inbox consumer binds the LOCAL tier
(`AGENT_INBOX`), not the global stream `GLOBAL_TO_DIRECTOR` that cross-host
replies to `global://director` are published onto. Mail addressed to the
global director is written where the director is not reading. This is why
send reports success (the publish onto the global stream succeeds) while
delivery never happens (nothing consumes it into the director inbox).

## Operating condition while it holds

- "Accepted is not delivered or read" (R-08) is not a caveat here, it is the
  whole failure: treat a cross-host `send_message` "accepted" as unheard.
- Collect cross-host status from GitHub (PR/issue comments), the authoritative
  channel, until the fix lands. Every mokuzai supervisor has been told this.
- The fix is l15b5 + 7gnvo (bind/consume the global stream into the director
  inbox). When they merge, re-run the round-trip test (a mokuzai seat replies
  to `global://director`; confirm it drains into the kinu inbox) before
  trusting the bus again.

## What it promotes

Two director requirements harvested from this: R-104 (cross-host per-seat
addressing) and R-105 (per-message delivery/read receipts). See
`sim/requirements.md`.

## Cross-refs

- `sim/notes/observations.md` O-23 (and O-27, the addressing-confusion
  cousin, retracted the same day)
- aae-orc#391 (the duplicate-director surface that led here)
- R-08 (accepted != read), R-103 (fleet-state model), R-97 (per-seat address)
- bd/fix tickets: l15b5, 7gnvo
