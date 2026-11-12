# One-shot dispatch: director sends deterministic work to scripts and small seats, not to its own context

- **Status:** idea (pre-hypothesis, no commitment). Operator, 2026-11-12.
- **Subject:** director (what the director session does itself, and what it
  hands off). The seat launch is an OBJECT from marvel; the role text is an
  OBJECT from the role library. Filed in the director graph per the subject test.
- **Related:** marvel idea `naked-seat-launch`; role-library idea
  `naked-role-shape`; the cue plan T1 to T13 (aae-orc-tk8ge and siblings, party
  record `_bmad-output/party-mode/4ptdd-director-cue-2026-11-12/`); aae-orc
  SOUL.md section 9.

## The observation

The operator, verbatim: "we could also save tokens if we created "naked
agents" that do not include the vast majority of instructions, context or
tools, and reduce the context use for one shot processes for specialized
actions, in addition to leveraging deterministic scripts and tools to reduce
token useage/output".

Measured on kinu, 2026-11-12, from the Claude Code session transcripts: in 24 hours the
aae-orc project made 1550 model calls reading 387M cached input tokens, about
250k per call. The standing director session made 367 of those calls and read
99.4M, about 270k per call. Cost is calls times context. The cue plan lowers
calls. This idea lowers the context each piece of work is done in.

## What the director session does today that a script or small seat could do

- Publish and update the director pages (the Desk, the Shelf, the Work Ledger
  and others). Their content is rendered by scripts already (`dws page`,
  `board-html`, `shelf-row`); the session only makes the publish and database
  calls, inside its full context.
- Read Desk answers and page picks and relay them, which O-2113 and O-2114 show
  is missed when it waits on the session.
- Check a PR's state, run merge-guard, rerun a failed CI job.
- Parse card files and write Desk rows.

## The shape

Director keeps judgment: what to route, what to ask the operator, what to
rule. Everything else is a script on a timer (no model), or a one-shot naked
seat that marvel starts with the role text and a short tool list, runs once,
and lets exit. Director reads the one-line result instead of doing the work in
a 270k context.

## Limits already known

- Artifact pages belong to the claude.ai account that published them. A seat on
  another account can write a page only after it is shared with that account as
  an editor. Bedrock and API-key seats have no Artifact tool at all.
- UNVERIFIED: whether the Artifact tools work in a headless `claude -p` run.
- The director state (`~/.director/state`) is on kinu, so rendering stays on
  kinu until that state is reachable from other hosts.

## Open questions

1. Which of the tasks above costs the most turns today? T8's counters
   (director#586) and the transcripts can say.
2. Is the one-shot seat a director verb (director asks marvel to run it) or a
   supervisor's job (director routes it to a team)? The director role says
   director scales roles and keeps the record (R-102, R-131), which reads as the
   former.
