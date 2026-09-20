# Live fleet knowledge, and roles as staffed functions not single agents

- **Status:** idea (pre-hypothesis), operator-raised 2026-09-20.
- **Subject:** director. How director holds a live, non-fixed picture of the
  fleet's teams and staffs roles; supervisors' share of that picture; and
  controllable queue/bottleneck management. marvel, kos, and the bus are
  objects it uses, not the subject.
- **Tracking:** none yet; relates aae-orc-nny4g (work-management model),
  aae-orc-z3wta (director role), aae-orc-q9mtd (multi-holder role
  addressing), finding-183 (scale + shift-work probe), aae-orc-ebc32
  (presence/bus features).

## The framing

Team constituency has no fixed shape. There is no single manifest that stays
true, so knowledge of the fleet's teams (their purposes, capabilities, and
roles) has to be LIVE rather than baked:

- **Director** holds the live picture: which teams exist, what each is for,
  what capabilities and roles it has, and where work is queued or stuck.
- **Supervisors** know their own team's shape, and know enough about OTHER
  teams to route, escalate, and hand off across team boundaries.
- Some flexible but controllable method of understanding and managing QUEUES
  and BOTTLENECKED work is available at both tiers: work that is piling up, or
  blocked on one role, is visible and can be acted on (staff the role up,
  redistribute, escalate), not just observed.

## The load-bearing point: a role is a function, not a person

A role is a function that can be staffed by more than one worker. The operator's
illustration: we do not build roads with one person to work the asphalt,
baseball does not field one outfielder because it is a single role, and
companies do not put all the accounting or all the programming on one staff
member's shoulders. A role names the work; capacity is how many workers hold
it, and that number moves with the queue.

Consequences that fall out of this and connect to open work:

- **Addressing a role may mean addressing N holders**, not one. This is the
  multi-holder half of q9mtd (a shift-change overlaps two supervisors; a role
  can have many replicas). Addressing has to distinguish "the role" (all
  holders, a scoped broadcast) from "one holder" (pick-one, the NATS
  queue-group shape) from a specific named holder.
- **Scaling is the normal response to a bottleneck**, not an exception.
  finding-183 already showed `marvel scale --role r --replicas N` works and is
  the clean-seat escape; the missing half is the live queue/capacity picture
  that tells director and the supervisor WHEN to scale, and the controllable
  method to do it deliberately.
- **The live picture is a director surface**, distinct from marvel's applied
  manifest (point-in-time desired state). marvel says what was declared;
  director needs what is true now, including queue depth and where work is
  stuck. Presence (ebc32) is one substrate for the liveness half; the
  purpose/capability/role catalog is the other half and has no home yet.

## Open questions

- What is the minimum live team catalog director keeps, and how is it kept
  current without becoming a second manifest that drifts?
- How much of other teams' shape should a supervisor see, and how does it
  learn it (a director push, a shared read of presence, a query)?
- What is the queue/bottleneck primitive: a per-role work queue director and
  supervisors can read depth from, an escalation signal when a role saturates,
  or both? Ties to the work-management model in nny4g.
- Role addressing across N holders: scoped broadcast vs pick-one (queue group)
  vs named holder. Needs the deeper NATS-features look flagged in q9mtd.
- The controllable knobs: who may scale a role, on what signal, with what
  ceiling. This is a values/responsibility question (z3wta): director proposes,
  the supervisor decides, marvel executes, and none of it is lockstep
  (SOUL section 8).

## Why it is an idea and not yet a probe

It crosses director, marvel (scale, the applied manifest), kos (the team
catalog), and the bus (presence, queue groups). It crystallizes into a probe
when one piece is forced: most likely the queue/bottleneck primitive, when a
real team first saturates a role and director has to decide to scale from a
live signal rather than a human reading a statusline.
