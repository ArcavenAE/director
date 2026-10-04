# What "handoff complete" means at a shift change, and whether a rotated-out seat stays on as support

Status: idea (operator ruling on the B2 question, 2026-10-04). No design and no commitment. Subject: how a seat's shift change is judged finished, which is director's concern because director routes to and reports on the successor.

## The ruling, verbatim

Operator, 2026-10-04: "we should do more research on how this handoff/shiftchange should be completed, possibly put elegable originals into a secondary guarentee/support role ready for questions for some period of time (if they are being rotated out for a non-critical or non-fault reason)"

## What is settled for now

A successor is considered live when its catch-up is complete (ruled, B2-R2). The design for what catch-up consists of is director#223: inherited mail, a catching-up presence, and an expiry notice for asks that die unread.

## The question

1. **What should "handoff complete" mean?** Today it is the successor finishing its catch-up. Whether that is enough is open: the successor can finish reading and still hold nothing of what its predecessor knew that never reached a handoff file.
2. **Should a predecessor rotated out for a non-fault reason stay on?** The ruling suggests a secondary support role for a period: an eligible original seat kept ready for questions from its successor, then released. What makes an original eligible, how long the period lasts, and what it may do (answer, or also act) are all unknown.

## Why it is only an idea

The operator asked for more research, not a design. The evidence so far is two failures at shift change:
- director#220: a respawned supervisor reports live before it has read its predecessor's replies.
- director#222: unread mail to a live session that never polls expires and reads as silence.

Neither says what a support seat would be, and a support seat is a new role with its own address and cost, so this needs the operator's input before it needs a design.

## Pointers

- director#220, director#222, director#223
- R-178 (a respawned supervisor reads its predecessor's replies before reporting live)
- finding-015 (a respawned seat replays its inbox and the successor cannot tell handled mail from new)
