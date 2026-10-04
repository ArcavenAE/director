# What "handoff complete" means at a shift change, and whether a rotated-out seat stays on as support

Status: idea (operator ruling on the B2 question, 2026-10-04). No design and no commitment. Subject: how a seat's shift change is judged finished, which is director's concern because director routes to and reports on the successor.

## The ruling, verbatim

Operator, 2026-10-04, answering the B2 question. The text is quoted exactly, typos included, in a code block so nothing is reflowed (583 bytes):

```
R1 predecessor lineage comes from the launcher's environment, R2 it's not live until it's considered to have taken over for it's predecisor, the handoff is compelete; We don't have the details on that exactly, for now it's when the catch up is complete, but we should do more research on how this handoff/shiftchange should be completed, possibly put elegable originals into a secondary guarentee/support role ready for questions for some period of time (if they are being rotated out for a non-critical or non-fault reason) B2-R3 yes, definitely 72h and fix the stale 24h referneces
```

It carries three rulings:
- **R1:** a successor's predecessor lineage comes from the launcher's environment.
- **R2:** a successor is not live until it is considered to have taken over, the handoff being complete. The details are not known; for now that is when its catch-up is complete, and the handoff and shift change deserve more research, including a possible secondary support role for eligible originals rotated out for a non-fault reason.
- **B2-R3:** 72 hours, and fix the stale 24 hour references. This is outside this idea.

## What is settled for now

A successor is considered live when its catch-up is complete (ruled, B2-R2: "for now it's when the catch up is complete"). The design for what catch-up consists of is director#223: inherited mail, a catching-up presence, and an expiry notice for asks that die unread.

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
