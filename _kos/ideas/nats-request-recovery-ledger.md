# nats-request recovery ledger: a durable memory of outstanding instructions

- **Status:** idea (pre-hypothesis, no commitment). A core expansion of
  director's bus durability work.
- **Date:** 2026-09-17
- **Subject:** director. The ledger is director's own durable record of what
  it has asked for and whether it has been done. marvel (which runs the
  receiving agents) and NATS (which carries the messages) are objects the
  ledger reasons about, not co-owners.
- **Related:** the tmux harness-state watchdog idea in the marvel graph
  (`marvel/_kos/ideas/tmux-harness-state-watchdog.md`): the watchdog notices
  that a receiver cannot act (frozen on a permission prompt, crashed, logged
  out); the ledger remembers what that receiver was supposed to do, so the
  instruction survives the stuck agent. director bus durability work:
  finding-001 (the global hub and its `GLOBAL_TO_*` JetStream streams and
  presence bucket), `docs/architecture.md`, and the two-tier NATS design.

## The problem, in one sentence

NATS can guarantee a message reaches a shim; it cannot guarantee the agent
behind that shim acted on it, and director cannot remember across a
context compaction or a session change what it is still waiting on.

## Why the bus's own durability does not cover this

JetStream already stores a message on `GLOBAL_TO_<seat>` and delivers it
when a down leaf re-links (finding-001 section 6.1). That durability is real
and load-bearing, and it is not the gap. The gap is the distance between
three different events that the transport collapses into one:

1. **Delivered:** the shim pulled the message off the stream and acked it at
   the transport layer. JetStream now considers the message handled and may
   drop it.
2. **Acted:** the agent behind the shim actually started the instructed
   work. This can fail to happen even after a clean delivery, because the
   agent is frozen on a permission prompt, has crashed, or is sitting at a
   login prompt (finding-041, the daemon-side cause of exactly this).
3. **Done:** the work finished and produced its result.

Once a message is acked off the stream at step 1, the bus has no memory that
steps 2 and 3 never happened. The instruction is gone from the transport and
was never carried out. There is also a second party the bus does not model:
director itself. If director's context has compacted, director has gone
offline, or director has changed sessions since it issued the instruction,
director no longer knows the instruction is outstanding, even if the agent
did act.

The ledger is the durable record that spans both gaps: the receiver's
act/done state that the transport ack hides, and director's own memory of
what it asked for that its conversation context does not keep.

## What the ledger is

A local, durable record on the director side, one row per issued
instruction, carrying a lifecycle state:

| State | Meaning |
|---|---|
| **issued** | director wrote the instruction to the bus |
| **delivered** | the transport confirms a shim pulled it |
| **acted** | the receiving agent reported it started the work |
| **done** | the work finished, with its result or a pointer to it |

`issued` and `delivered` are observable from the bus and its acks today.
`acted` and `done` are the states the bus does not model and the ledger
adds; they arrive as explicit acknowledgements from the receiving agent, not
as transport events. An instruction that reaches `delivered` and never
advances is the exact failure this ledger exists to make visible: the
message was carried, and the work was not done.

## The three properties it needs

- **A redelivery and replay path.** From the ledger, director can re-issue
  an instruction that stalled at `delivered` or `acted`, to the same seat or
  a reassigned one. The ledger is the source the replay reads from, so replay
  survives a director restart.
- **Idempotency, so replay is safe.** Each instruction carries a stable id
  that rides the envelope, and the receiving agent treats a repeat of an id
  it has already acted on as a no-op that re-reports its existing state
  rather than doing the work twice. Without this, the recovery path is more
  dangerous than the failure it recovers from.
- **An ownership model, so a fresh director can pick up the thread.** The
  ledger records which director session issued each instruction and its
  current state, so a new director session (after a compaction, a restart, or
  a handoff) reads the ledger and sees the outstanding set without needing
  the prior session's conversation context. Outstanding work is a property of
  the ledger, not of any one director's memory.

## When it earns its place

The two scenarios that motivate it, both observed in the fleet:

- **The receiver cannot act.** A supervisor cast on a seat is frozen on a
  permission prompt, has crashed, or is logged out (finding-041). The
  instruction was delivered and acked off the stream; nothing did it; nothing
  remembers it should have been done. The watchdog can surface that the
  receiver is stuck; the ledger is what still holds the instruction so it can
  be retried or reassigned once the receiver is unstuck.
- **Director loses the thread.** director's context compacts, director goes
  offline, or director changes sessions between issuing an instruction and
  its completion. The conversation memory that held "I am waiting on X" is
  gone; the ledger is the record that is not.

## Relationship to the watchdog and the bus, kept honest

The watchdog (marvel graph) and this ledger (director graph) answer
different questions and neither subsumes the other. The watchdog answers "can
this receiver act right now?" by classifying the receiver's on-screen state.
The ledger answers "what did director ask for, and is it done?" by tracking
the instruction lifecycle. They compose: a `delivered`-and-stuck ledger row
plus a `logged-out` watchdog verdict on the same seat is a precise picture,
retry the instruction after the seat is re-authenticated, do not retry into a
seat that will only refuse again. The bus stays the transport; the ledger
does not replace JetStream durability, it records the semantic layer above
the transport ack that the bus does not model.

## Open questions

- Where does the ledger live, and in what store? A director-local durable
  file, or a JetStream stream/KV bucket on the same bus (which folds the
  ledger's own durability into the infrastructure director already runs)?
- What obligates a receiving agent to send `acted` and `done`
  acknowledgements? Is that a protocol requirement on the envelope, or an
  advisory the ledger degrades without?
- How does an instruction id relate to the A2A envelope director is adopting
  for the bus (per the vision's director section)? Reuse the envelope's
  message id, or a separate instruction id that can span multiple messages?
- What is the retention and closure policy? When is a `done` row archived,
  and how long does an unacted `delivered` row stay live before it escalates
  to the operator?
- The auth boundary (SOUL section 3, ADR-009): the ledger records
  instructions and their states, never credentials. A retry must not tempt
  any credential-handling path; it re-sends an instruction, nothing more.

## Crystallization signal

Already close: two observed fleet failures this would have caught (a stuck
receiver whose instruction was lost, and a director that lost its own thread
across a session change). It crystallizes into a frontier question plus a
probe brief when the bus envelope work (finding-001 and the A2A adoption)
settles enough that the instruction id and the ack messages have a shape to
attach to.
