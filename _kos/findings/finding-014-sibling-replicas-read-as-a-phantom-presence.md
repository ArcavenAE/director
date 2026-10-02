# finding-014: two replicas of one role read as a killed seat and its successor, because a roster row carries no replica or generation context

- **Date:** 2026-10-02
- **Session:** errand-supervisor-g4-2 on mokuzai, errand harvest
- **Subject:** how roster rows identify a seat
- **Confidence:** one instance, investigated read-only on the host; filed as #164 and resolved there as not a bug

## 0. The sentence

**A roster row names an agent id and an instance, but not that the id is one of
N replicas of a role, so a reader who knows one seat of that role was replaced
can take the other, healthy replica for the dead one.**

## 1. What happened

On 2026-09-30 a supervisor seat was killed and respawned. Its successor took the
next free index. A second supervisor replica of the same role, alive the whole
time and idle since 2026-09-25, kept its row with a fresh heartbeat. The global
roster showed both rows, and the live replica was read as the killed seat still
heartbeating. #164 was filed as a phantom-presence bug.

Investigated read-only: the pid in the row was the live replica's director-mcp
shim, its parent the replica's harness session, both carrying that replica's
agent id. The killed seat had no row and no process, so for the seat actually
killed, presence ended correctly. I then compounded the error in a relay by
calling the live replica a "duplicate", until the team manifest showed
`replicas: 2` for the role.

## 2. Why it was easy to get wrong

- The agent id encodes a generation and an index (`-g4-1`), but nothing says
  whether a different index is a sibling replica or a later instance of the
  same slot.
- The row carries no role replica count, no spawn time and no "replaces" link.
- An idle replica and a dead one look alike from the bus. Only the host can
  tell them apart.

## 3. What would prevent it

A roster row could carry the replica count of its role and its own spawn
time, both of which marvel knows at cast. With those, "two rows of a
two-replica role" reads as normal at a glance. This is a suggestion, not a
design; it touches the presence schema.
