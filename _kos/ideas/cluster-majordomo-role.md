# The cluster majordomo, not a "cluster supervisor"

Status: idea (operator ruling, 2026-09-21). Subject: fleet role taxonomy
(director + marvel addressing). Cross-refs: B14 (orc), aae-orc-q9mtd,
aae-orc-8hgw7, ArcavenAE/aae-orc#391.

## The ruling

There is no "cluster supervisor" role. Operator, 2026-09-21:

- **Supervisor is a TEAM role.** Every team has a supervisor. Typically at
  most two are live at once, and the second exists only during a shift change.
  A supervisor is concerned with the WORK.
- What issue #391 measured as five or six co-holders of
  `global://mokuzai/supervisor` is a category error, not a supervisor
  population. It is the several team supervisors on one cluster all resolving
  to a single cluster-scoped address. Team supervisors should be addressed
  per team, not aggregated into a cluster address.
- A cluster-level coordinating role can exist, but it is a DIFFERENT role: the
  **cluster majordomo** (a supervisor-of-supervisors). It is concerned with
  the CLUSTER, not the work: cluster health, capacity, seat lifecycle, shift
  orchestration across teams, and the cluster's standing with the director.
  The operator had not conceived a "cluster supervisor"; that concern belongs
  to the majordomo, named here for the first time.

## What this changes

- **Addressing.** `global://{cluster}/supervisor` as a role address that all
  team supervisors co-hold is the wrong abstraction. Team supervisors are
  reachable per team (per-workspace/team, honoring the 1-to-2 shift-change
  count). A cluster address, if one exists, addresses the majordomo, and there
  is exactly one majordomo per cluster (with the same shift-change caveat).
- **The co-holder question (aae-orc-8hgw7) is reframed, not just widened.**
  8hgw7 asked how to deliver to N co-holders of a cluster-supervisor address.
  Under this ruling that address should not have N team-supervisor co-holders
  at all. The real delivery questions become: per-team supervisor addressing
  (at most two holders, a shift-change pair), and single-holder majordomo
  addressing. The "fan-out vs queue-group at six holders" dilemma dissolves.
- **The ambiguous-global-role-address ticket (aae-orc-q9mtd)** is the place
  the addressing fix lands: three teams sharing one global supervisor address
  is the bug; the fix is per-team supervisor addressing plus a distinct
  majordomo address.

## Open, for later elaboration

- Is the majordomo a marvel-managed seat, a director function, or a hat a
  team supervisor can wear? (Leaning: a distinct seat, since its concern is
  the cluster, which no single team owns.)
- Its authority relative to the director (global tier) and to team
  supervisors: it coordinates the cluster for the director, it does not
  direct the work of a team over that team's supervisor.
- Role home is wardrobe (per B14); this idea only names the role and its
  concern, it does not define the card.
