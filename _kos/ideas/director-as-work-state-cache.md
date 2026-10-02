# director as a cache of work state: dispatched, status, open questions

- **Status:** idea (pre-hypothesis, no commitment). No ticket and no frontier
  node yet.
- **Date:** 2026-10-01
- **Subject:** director. The cache is director's own record of the fleet's
  work. marvel, the bus and the seats are the sources it reads, not
  co-owners.
- **Related:** director#168 (draft), the board's current-state panel over an
  item ledger; the nats-request recovery ledger idea
  (`_kos/ideas/nats-request-recovery-ledger.md`); the operational diagnostic
  battery idea (`_kos/ideas/director-operational-diagnostic-battery.md`).

## The operator's words

> "director at it's core, director must be very good at being a cache, work
> dispatched, status, open questions, and it should refresh that status, keep
> the work streams organized and efficiently retrieve and update as it relays,
> articulates intent, assists in delegating and tracking efficiently"

## The idea, in one sentence

Treat director's core job as keeping a cache of the fleet's work state, so
that every relay reads from it and writes back to it, and judge director by
the things a cache is judged by: whether an entry is fresh, whether it was
read before it was served, and whether a stale entry gets refreshed from its
source.

## What the cache holds

Three kinds of entry, taken from the operator's words:

- **Work dispatched:** what was asked, of which role, under which ask id, and
  in which work stream.
- **Status:** where each dispatched item stands now, and when that was last
  confirmed.
- **Open questions:** what is waiting on the operator or on another seat, and
  since when.

Each entry has a source it can be refreshed from: the bus for arrivals, the
roster for reach, the PR for review state, the ratified plan for order.

## Evidence from 2026-10-01

The ids below are entries in director's observation log
(`sim/notes/observations.md`), an operator-local file that is not tracked in
this repository. Each line restates what the entry says, scrubbed to
mechanism.

- **No write on arrival (O-31j, O-31m, O-31o).** Three of three
  trial-intervention GATEs that day were forwarded 35 to 40 minutes late. In
  each case director read the bus only when the operator asked for status;
  nothing on director's side fires when such a message arrives. In cache
  terms, new entries land in the store but are not read until a sweep.
- **No refresh owner for an approved item (O-31k).** Two approved draft PRs
  sat because a bus message does not wake an idle seat. Director woke both
  seats by hand. The entry "approved, waiting to go ready" existed, and
  nothing owned moving it.
- **Served without reading (O-31l).** Director relayed asks that reordered a
  ratified plan without first reading the plan's current step, and withdrew
  them by CANCEL after the operator objected. The entry recommends: before
  relaying a step of a tracked plan, read that plan's current step and
  dependencies.
- **A stale belief served instead of the source (O-31q).** Director told the
  operator the wrong supervisor lacked cross-cluster reach; the roster showed
  otherwise. Director had been routing all supervisor traffic to the other
  cluster through itself by habit, adding 35 to 40 minutes per hop. The entry
  recommends: verify reach from the roster per seat, never assume it.

## How this relates to director#168

director#168 designs an item ledger and a current-state panel for the board.
That ledger is a candidate store for this cache. This idea is about the
behaviors around the store: write an entry when its message arrives, read the
entry before relaying, and refresh a belief from its source before serving
it.

## Open questions

- What should trigger a refresh: arrival of a message, a timer, or the next
  relay that touches the entry?
- Which source is authoritative for each field, and what does director do
  when two sources disagree?
- What must director never cache, because it is a judgment rather than a
  fact (SOUL section 8)?
- How would a stale entry be detected and shown, and is that a diagnostic
  rather than a gate (aae-orc's `diagnostic-not-gate` rule)?
