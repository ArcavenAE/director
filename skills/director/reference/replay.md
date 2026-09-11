# Replay: mining requirements from the session corpus

Most of the evidence director needs is retrospective and already on disk. The
session inventory reads hundreds of sessions across four harnesses. Replaying a
recorded session and asking what director would have needed at each decision
point is the cheapest requirements source available, and it reaches failure
classes a live simulation cannot easily produce (a credential expiring under a
long-idle session, R-45, is the worked example: you cannot conveniently stage
that live).

Method is counterfactual replay, after the CHI 2026 hybrid Wizard-of-Oz work
(arXiv 2510.06872).

## Procedure

1. Inventory wide: `DSI_DAYS=90 DIRECTOR_STATE=/tmp/replay-inv scripts/dsi`.
   Do not replay the whole corpus.
2. Stratify by the failure classes the register already names, and pick a few
   per class. Selector heuristics that worked on pilot 01:
   - died holding an ask: awaiting AND (dead process OR no address) AND age>12h
   - blocked at publish: tail matches denied/refused/permission/publish/yubikey
   - artifact promised never written: tail matches draft-at/parked-at/scratchpad/not-filed
   - no acknowledgement: tail matches sendmessage/cross-session/unresponsive/no-reply
   - handoff into unattended channel: tail matches handoff/told-the-session/left-for-you
3. For each chosen session, read the transcript tail and identify the decision
   points. Ask what director would have needed there. Map each need to an
   existing R number or propose a new one.
4. Tag source class strictly. OBSERVED only if the transcript shows the failure
   happening. Replay is still you reading, so default to JUDGMENT unless the
   transcript shows it.
5. Working notes go to `sim/notes/replay-pilot-NN.md` (gitignored, may name
   sessions). The clean proposed-changes section, written by shape with no
   client tokens, is what gets integrated into `sim/requirements.md`.

## The caveat that costs a pass if you skip it

**Never trust the `awaiting` flag; confirm the ask against the transcript.**
The ask-extractor false-fires on confirmation sentences ("Confirmed, both
gates approved", "Item 2 done"). On pilot 01, 3 of 14 sampled sessions were
clean completions misflagged as holding asks, and the entire blocked-at-publish
pool was keyword false positives on incidental "refused"/"denied" prose. The
flag is a candidate selector, not evidence. Every counted instance must be
confirmed against the transcript tail before it enters a count or the register.

## What a narrow pass yields

Mostly CONFIRMS of existing requirements with fresh independent instances,
which is itself a signal that the named classes are real rather than one-time.
New requirements come from the classes the hand simulation never reached. Pilot
01: 14 sessions, 1 new (R-45), 1 sharpening (R-21), 4 existing confirmed.
