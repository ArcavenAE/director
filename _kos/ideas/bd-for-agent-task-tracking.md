# director uses bd directly, with opinions, for agent task tracking

- **Status:** idea (pre-hypothesis, no commitment). No ticket, probe or
  frontier node yet.
- **Date:** 2026-11-02
- **Subject:** director. The question is how director records and finds the
  work it relays; bd is the store it would use, not a co-owner.
- **Asked by:** the operator, via director.
- **Related, not merged:** `_kos/ideas/director-as-work-state-cache.md` (what
  the cache holds and how it is judged) and `_kos/ideas/task-judge.md` (who
  assesses a task before dispatch and at handoff). This idea is about where the
  per-item record lives and who reads it.

## The operator's words (the part this idea covers)

> "integrating bd more directly in director, more opinionated around using bd
> for agent task tracking, not just project-level stuff"

The same message also asks about a local cache to marvel with global
read-write and regular sync. That is a separate idea and is not covered here.

## The idea, in one sentence

Let director write to and read from bd as the record of agent work, with a
fixed convention for what becomes an item, so that a task a seat was asked to
do is as visible as a project ticket.

## What changes, as a sketch

- **An ask that closes an analysis opens one bd item per resulting piece.**
  The analysis ends in a list of pieces; each piece becomes its own flat item
  at that moment, not a paragraph in a reply.
- **Routes and claims live in bd.** Who a piece was routed to, and who holds
  it, are fields on the item. A seat's claim is what the record shows, not what
  a message once said.
- **The sweep reads bd.** A periodic pass lists items that are unclaimed or
  unacked past an expiry, and surfaces them as a proposal. It reads and
  proposes; it does not reassign.

## Why it might matter

A plan held only in a session's context, with no record per item, sat unrouted
for most of a day: nothing was late because nothing was written down to be
late. A per-item record turns that silence into a row a sweep can see.

## What this does not settle

Open for a probe or a design, none decided here:

- which items are agent tasks and which stay project tickets, and how the two
  are told apart;
- the fields a route or a claim needs, and what an expiry is measured from;
- whether director writes bd itself or asks a seat to;
- how this sits beside the cache idea's store, and whether bd is the cache or
  its source.
