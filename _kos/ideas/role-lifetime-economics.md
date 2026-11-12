# Role lifetime: when a long-lived seat pays, and when an ephemeral one does

- **Status:** idea (pre-hypothesis, no commitment). Operator, 2026-11-12.
- **Subject:** director (which way director sends a piece of work: to a
  standing seat, or to a seat started for it). Seat lifecycle is an OBJECT from
  marvel; the lifetime a role declares is an OBJECT from the role library.
- **Related:** `one-shot-dispatch-to-naked-seats` (this graph); marvel idea
  `seat-lifetime-cost-model`; role-library idea `role-lifetime-field`.

## The observation

The operator, verbatim: "we should also study which roles merit long term use,
and what the benefit/cost is to using more one off, ad hoc, and ephemeral
agents for certain roles. this is a complex topic as cache may save many
tokens for repeat action, under the right circumstances. but larger contexts
and long sessions or cache idling out are also costly".

## What one measurement already says

From the kinu director session's own transcript, 24 hours to 2026-11-12: 367
calls, 99.4M cache-read tokens, 0.56M cache-write tokens, under 0.01M uncached
input. Reads were about 99 percent of the input tokens. For this seat, the
cache was not missing; the cost was the size of the context it re-read on every
call. A long session pays most of its cost in reading, and that grows with the
context. An ephemeral seat pays a cold write of a small context once.

## The trade, in the terms director would use

- **Standing seat:** cheap per call while the cache is warm and the context is
  small. It gets dearer as the context grows, and an idle gap past the cache
  lifetime re-writes the whole context at the write rate. It holds state the
  next task can use (a review thread, a party).
- **Ephemeral seat:** pays a cold start every run (system prompt, tools, the
  task), with no idle cost and no growth. It holds no state, so the task must
  carry what it needs.
- A rough rule to test: a task with little shared state that recurs less often
  than the cache lifetime is cheaper ephemeral. A burst of related tasks with
  shared state is cheaper on a standing seat, until its context passes some
  size.

## Open questions

1. Per role, how often does its work recur, how much state carries between
   tasks, and how large does its context grow? T8's counters (director#586) and
   the transcripts can answer the first and the third.
2. Which roles today are standing out of habit: reviewers between reviews,
   supervisors of idle teams, the director session itself?
3. Price multipliers (cache write against read, the 5-minute and 1-hour
   lifetimes) are to be read from the provider's current pricing before any
   number is used; none is assumed here.
