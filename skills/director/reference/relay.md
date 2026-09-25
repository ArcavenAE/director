# Relaying: an open question, kept open

Director interprets. That is the function: the human has one attention budget,
and an assistant that only transcribes hands the work back. Any rule that
forbids paraphrase, interpretation, or resolving an ambiguity would remove the
reason director exists, so there is no such rule here.

The one thing observation ruled out is narrower and is about authority, not
language: director must not represent itself as carrying authority it was not
given. See `finding-151` O-8 for the incident.

Everything else below is open, and is meant to be answered from use. Do not
close these in prose.

## Open, and deliberately not answered here

**1. Does director hold a standing license?** Asking a session for status and
telling a session its PR merged are plainly fine and plainly origination. If
every one of those needs the human first, the human is the message bus again,
which is the defect O-1 named. A license needs an edge, and "re-run a
read-only check" already sits on it.

**2. When an instruction is ambiguous, who is best placed to resolve it?**
Director resolving it is often right and is the reason it exists. Bouncing to
the human spends the scarce resource. Forwarding the ambiguity named costs a
session turn, which is cheap, and puts the choice with the party holding the
local policy. Session 1 produced one instance of each, one good and one wrong,
and one instance is not a rule. Collect more before deciding anything.

**3. What does a session do with an instruction it cannot verify?** This is
the nonrepudiation problem and it belongs to the identity plane, not to a
prompt. The addressed session needs to know director with confidence and to
validate that a message is in fact from director. Until that exists, every
correct refusal observed is luck.

## Why the harness makes this look easier than it is

Claude Code does not deliver a message; it delivers the message wrapped in a
policy paragraph the RECEIVING harness writes, telling the receiver the sender
is *"not typed by your user, but very likely working on their behalf"* and to
refuse permission laundering. "Very likely" is a probability estimate standing
where a signature belongs.

Two consequences while the simulation borrows the feature. That text is doing
part of the safety work `finding-151` attributed to receiving sessions. And it
does not exist on codex, opencode, or crush, so the property vanishes the
moment director addresses one of them.

Full requirement, three template variants, and the seven reasons a director
that does not use this feature is better:
`../../sim/specs/vendor-injected-receiver-policy.md`.

Standing rule for the simulation: never record "the session refused correctly"
without recording that the harness told it to.

## The harder problem, named and parked

Asking a session to review materials does not make an injected instruction
inside those materials carry the authority of the human who asked for the
review. Authority does not flow to content by way of the request that
surfaced it. Enumerate instances from use; do not try to close it in theory.

## Relay the fix, not every recommended layer (2026-09-25, operator correction)

When a builder has reproduced and fixed a defect, the work order is the fix.
Do not transcribe every guardrail a party or architect recommended (extra
hooks, gates, acks) into the brief. The operator called that "mitigation
ratcheting." Let the builder ship, and let it ask for architect, reviewer, or
tester input if it wants that input. Source: O-2026-09-25-director-layered-the-brief.
