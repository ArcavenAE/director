# GitHub merge events onto the bus: stop seats polling gh for "has it merged yet"

- **Status:** idea (pre-hypothesis, no commitment). A design question, not a
  build.
- **Date:** 2026-09-28
- **Subject:** director. The question is whether the bus should carry an
  external event feed, and who may publish onto it. GitHub is the object
  the feed reads.
- **Related:** `nats-request-recovery-ledger.md` (a seat's durable memory of
  what it is waiting on; a merge is one such wait); finding-001 (the
  `GLOBAL_TO_*` streams, which already replay to a leaf that re-links).

## The problem, in one sentence

A seat holding a trigger ("re-run the report once PR N merges") learns about
the merge only by polling `gh`, so it either polls often and spends the
shared GitHub rate budget, or polls rarely and acts late.

## What was observed

A test-architect seat on a client team held re-run triggers for three pull
requests. It had no subscription to merge events, so each trigger was a
`gh pr view` loop. The fleet acts as one GitHub identity, and every poll
draws on that identity's one budget: 5,000 core requests an hour, shared by
every seat (aae-orc finding-200). A poll that times out
also reads as "not merged yet", which is the quiet partial answer aae-orc
finding-197 records.

## The shape it might take

1. **One watcher, many listeners.** A single process watches the repositories
   the fleet cares about, through a webhook or one conditional-request poll
   loop, and publishes `pr.merged` (repo, number, merge sha, time) on a bus
   subject. Seats subscribe; nobody else polls.
2. **Durable, so a late seat still hears it.** Publish onto a stream a
   re-linking leaf replays (the finding-001 pattern), not a fan-out with no
   replay. Otherwise a seat that restarts during the merge misses it and
   falls back to polling.
3. **The watcher's authority is narrow.** It reads GitHub and writes one
   subject family. It is not a seat, and it holds no merge rights.

## Open questions

A. Webhook or poll? A webhook needs an endpoint GitHub can reach, which a
laptop cluster behind NAT does not have. A single ETag-conditional poll does
not count against the rate limit when the answer is "not modified". It may
be the honest first step.

B. Where does the watcher run: in director, as a marvel-supervised workload,
or as its own small service? The independence rule (no component
conscripts another) argues against making it a director dependency.

C. Which events beyond merge are worth it? Review submitted at the head,
draft to ready, and check-suite concluded are the ones seats poll for
today. Each one added is one more thing the watcher must get right.

D. Is this a subscription the recipient declares (intent: "tell me when
repo#N merges") or a firehose every seat filters? A declared interest also
gives the recovery ledger something to close.
