# Delivering a fleet-universal behavioral nudge when skills and rules are not distributed

- **Status:** idea (pre-hypothesis), operator-raised 2026-09-20.
- **Subject:** director. How a behavioral nudge (or any cross-fleet
  convention) reaches every agent regardless of harness, host, user, and
  working directory. The harnesses, sideshow, and the bus are objects it
  would use, not the subject.
- **Tracking:** none yet. Prompted by the "try first" nudge (item 6,
  ratified as a nudge not a hard rule): before reaching for the inject
  doorbell, try recovering the director channel, another agent, a file, or
  a bd ticket. Relates aae-orc-nny4g (work-management model), the director
  skill's vendor-injected-receiver-policy spec, aae-orc-z3wta (director
  role).

## The problem the operator named

The obvious homes for a behavioral nudge are a Claude Code skill or a
`.claude/rules/` entry. Neither is fleet-universal:

- **Scoped, not distributed.** A skill is per-user/per-host install; a rule
  is per-directory (loaded only for sessions whose cwd is under that repo).
  Neither travels to another host, another user, another checkout, or
  another harness. The fleet's existing behavior-trigger rules
  (tooling-friction, agent-tools, upstream-claim-gate) all live in one
  repo's `.claude/rules/` and only govern sessions running there. That is
  accepted for repo-local discipline; it fails for a nudge that must reach
  every agent the moment it is about to inject.
- **Harness-uneven.** Even the one channel that looks universal, the
  harness wrapping inbound messages in a policy paragraph, is Claude-Code
  only. codex, opencode, and crush inject nothing (director skill,
  vendor-injected-receiver-policy). Leaning on it is the "borrowed safety"
  the director skill already warns is an artifact of the substrate, not a
  property of the fleet.

So the nudge has no universal home today. Writing the local rule would
cover only this repo's Claude sessions on this host, a partial stopgap
consistent with the other behavior-trigger rules but not the thing the
operator is asking for.

## Candidate universal channels (none proven)

- **The director envelope.** Director owns the message envelope and it is
  the one channel every coordinating agent already consumes cross-harness
  and cross-host. A nudge could ride the envelope (a standing advisory
  field, or director prepending it to relayed tasking) so it reaches the
  receiver regardless of harness. This makes it a director feature, not a
  per-repo file, and it degrades to nothing for an agent not on the bus.
- **A bus-level convention** carried in presence or a well-known subject
  every seat reads at join.
- **sideshow-distributed pack.** Real provenance, but still a per-repo
  install, so not universal on its own; it distributes the file, it does
  not guarantee load.
- **Marvel spawn-time environment.** Marvel constructs every session's
  environment (the one built enforcement locus); a nudge could be a stamped
  advisory. Universal across marvel-managed seats, absent for anything not
  spawned by marvel.

## Open questions

- Is "a nudge that reaches every agent" even director's job, or is it a
  platform-composition question (marvel stamps + director envelope +
  sideshow pack, each covering a slice)? The subject test points at
  director for the envelope-carried case and at the composition otherwise.
- What is the minimal universal carrier: envelope field, presence
  advisory, or spawn-time stamp? Rank by coverage (which agents it reaches)
  and by cost (who has to change).
- Does a nudge carried by director conflict with SOUL 8 / the automation
  boundary (advisory, confirm-or-override, never a gate)? A nudge is
  advisory by construction, so it should sit inside the boundary, but state
  it so it is not later hardened into a required check.
- How does this relate to the receiver-policy the harness injects: does
  director supply the harness-even floor that the Claude-Code-only policy
  paragraph currently fakes?

## Why an idea and not a probe

It crosses director (envelope), marvel (spawn env), sideshow (packs), and
the bus (presence), and it reframes a ratified nudge (item 6) from "write a
rule" into "find the universal carrier." It crystallizes into a probe when
the first nudge or convention actually has to reach a non-Claude,
off-this-host seat and the local rule is demonstrably not enough.
