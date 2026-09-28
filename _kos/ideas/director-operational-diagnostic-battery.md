# An operational diagnostic battery for director: intended, working, broken

- **Status:** idea (pre-hypothesis, no commitment). Operator ask, relayed via
  director to arcaven-supervisor, 2026-09-27.
- **Subject:** director (reach, addressing, presence, replay, grants and the
  launcher are director's). marvel is an object here; its half of the
  battery is filed in its own graph.
- **Sibling:** `marvel::marvel-operational-diagnostic-battery` (the same
  battery for spawn, restart, shifts, inject, reexec and bus auth). One ask,
  two subjects, filed once in each graph per the subject test.
- **Related:** `nats-presence-harness-status-binding.md` (a logged-out
  harness that still looks present), `nats-request-recovery-ledger.md` (did
  the agent act), R-08 (accepted is not delivered or read), R-92 (a global
  send to an absent seat is refused, not queued).

## The operator's words

> "we should have operational tests, diagnostics, that verify designed,
> intended functionality is working, because the design is complex, and
> instances wont know what is intended vs what is working vs what is broken,
> and it's very challenging without a battery of positive and negative tests
> to know when something is broken or not working correctly"

## Operator ruling (RULED 2026-09-27)

Relayed by arcaven-supervisor, 2026-09-27T18:34:43Z:

> "the operator ruled on the diagnostics battery: YES to the shape, and YES
> to the default that a shim-only refusal is not enough and the broker must
> refuse before a third cluster joins."

- The shape above (named behavior with an anchor, a positive case, a
  negative case, and PASS, FAIL or NOT-APPLICABLE with evidence) is
  accepted.
- A negative case that only the shim refuses does not count as the control
  holding. The broker must refuse too, and that has to be in place before a
  third cluster joins.

## The observation

A runnable battery that any instance (a seat, a supervisor, director, the
operator) can call to learn three things: what is intended, what is working,
and what is broken. Each check:

- names the designed behavior it verifies, with a design-doc or R-number
  anchor;
- has a positive case: the thing works;
- has a negative case: the control refuses what it should refuse (for
  example a publish outside a host's allow list, a send to a cold mailbox, a
  merge without approval at the current head);
- reports PASS, FAIL or NOT-APPLICABLE, with the evidence.

Director's areas: reach and addressing on both tiers, presence, durable
replay, grants, and the launcher.

## Evidence that it is needed (2026-09-27)

- The peer-routing ruling went live, but no kinu supervisor held a global
  address, and finding that out took a manual survey.
- A supervisor sat on "Login expired" and swallowed six messages while every
  send returned accepted. R-08 already says accepted is not delivered; nothing
  ran that would have shown it.

## Prior art to build on, not duplicate

- `sim/twin/verify-cast-launch.sh`: 26 broker-free checks that the launcher
  hands the global-tier levers to the right roles and clears them for the
  rest (26 passed on 5c5faa3; re-signed as 07a2681; map in tag resign-2026-09-27).
- The director-mcp `--preflight` (`probe/nats-phase-0/director-mcp/bus.go`):
  broker reachable and provisioned before a seat starts.
- The hub acceptance steps in `sim/design/global-bus-tier.md`.
- The orc's warn-only hygiene check for shared-checkout drift (aae-orc#376).
- marvel's `keys doctor` (a permission audit of `~/.marvel`), on the marvel
  side.

These are single-purpose and run at one moment (a launch, a pre-flight, an
acceptance pass). The battery is the standing, callable union, with the
negative cases most of them lack.

## Tensions

- **Diagnostic, not gate.** The battery informs; it does not gate CI
  (`.claude/rules/diagnostic-not-gate.md`, ADR-007). A structural check (a
  config that cannot parse, a grant that names no subject) may gate; a
  behavioral probe of a live fleet only reports.
- **Negative cases touch real controls.** A refused publish or a cold-mailbox
  send has to be provably harmless on a live bus, or run against a scratch
  one.
- **NOT-APPLICABLE must be honest.** A check that cannot run (no global
  tier on this cluster) says so, rather than passing.
- **Anchors drift.** A check tied to an R-number goes stale when the
  requirement changes; the anchor is how that is noticed.
