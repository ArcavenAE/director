# finding-005: cross-host director mail fails on the consumer side, not the wire

> SUPERSEDED 2026-09-22 by aae-orc-m517d (the migrated team's four-candidate
> investigation) and by the kinu re-investigation opened this date. Do NOT
> trust the "consumer-side, diagnosis complete" conclusion below. Two defects
> in it, both confirmed:
>
> 1. The root cause was asserted on a SINGLE uncontrolled test ("the kinu
>    director inbox drained nothing, twice") that diagnosed THROUGH the channel
>    under test and never verified the director consumer was in a
>    `wait_for_message` loop at send time. Given O-28 (an idle seat does not
>    consume the bus) and finding-186 (marvel capture reads stale), "drained
>    nothing" cannot distinguish consumer-binds-wrong-stream from
>    nobody-was-listening. The finding carried no caveat to this effect.
> 2. It cites "the fix is l15b5 + 7gnvo." l15b5 fixed the `agent://` R-92
>    workspace-subject defect (director#24), a DIFFERENT path; `global://`
>    carries no workspace token (bus.go:392), so l15b5 does not touch the
>    return path at all.
>
> The live investigation treats the cause as OPEN with four candidates and a
> stated diagnostic discipline (pin shim build per seat; diagnose via harness
> or direct hub read, never through the channel under test; read the SENDING
> seats' transcripts for the refusal text). See aae-orc-m517d, aae-orc-7xrdo
> (presence-resolver silently skips the director row → false refusal),
> aae-orc-nzh7c (the director-silence split). The text below is retained
> verbatim as the record of a premature diagnosis, not as guidance.

Date: 2026-09-21. Subject: director (cross-host inbound mail). Status:
SUPERSEDED (see banner above). Originally filed as: diagnosis complete; fix in
flight (l15b5 + 7gnvo). Supersedes the working hypothesis that a duplicate
`global://kinu/director` holder was intercepting.

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
