# finding-023: a cluster's global supervisor address delivers every supervisor's traffic to every supervisor, and two seats measured what that costs to read

- **Date:** 2026-10-04
- **Session:** a builder seat placing the ops team's 2026-10-04 harvest report (item 5), beside a second measurement from my own team's supervisor seat. Neither measurement is mine; I recorded both as reported and checked the mechanism against finding-006.
- **Subject:** director global-tier addressing and delivery, so this belongs in director's graph.
- **Confidence:** measured, twice, on one cluster during one pause. Open question, not a decision.

## 0. The sentence

**A message to `global://<cluster>/supervisor` is delivered to every supervisor seat on that cluster, and the `FOR` line that says who it is for is a convention the receiver reads after delivery. So each supervisor reads, and pays for, every other supervisor's global traffic.**

## 1. The mechanism, already recorded

finding-006 established the shape: the global tier has no per-agent address, so every inbound message to a cluster is a fan-out to every seat that holds the cluster's supervisor inbox. This finding adds what that costs a receiver, measured by two seats on the same cluster across the same pause.

## 2. The two measurements

**The ops team's report.** Draining global sequences 395 to 680 meant reading about 285 messages, at about 90k characters per batch of 50. Six of them were for the ops team. Most of the rest were another team's review-gate traffic.

**My own team's supervisor seat.** During the same pause its global inbox held 300 messages, from 2026-10-02T04:37Z to 2026-10-03T19:11Z.

- 273 of the 300 (91%) were addressed to one other team's supervisor or its reviewer.
- About 20 were relevant to my team.
- One `wait_for_message` call at `max` 50 returned about 90k characters, too large for a single tool result.
- The six batches came to 552 KB.

Both seats report the same shape: well over nine messages in ten were for someone else, and a full drain reads hundreds of kilobytes to find a few dozen relevant lines. The cost falls hardest on a seat returning from a pause, which is exactly when it most needs a quick read of what it missed.

## 3. The open question

Two shapes have been named. Neither is decided here.

- **Team-addressed global subjects.** `global://<cluster>/<team>/supervisor`, so a message reaches one team's supervisor and no other. This is an addressing change at the broker, the global twin of the per-agent address finding-006 section 4 asks for.
- **Filter by `FOR` before delivery.** Keep the address, and have the shim drop or fold an envelope whose `FOR` names a different team before it reaches the model. That is cheaper, and it is a filter, not a boundary: the seat still holds the inbox, and the message is still on its stream. finding-006 section 4 already names the weaker form of this, marking an envelope addressed to someone else.

What any answer must keep: a broadcast meant for every supervisor still has to reach every supervisor.

## 4. Limits

- Both measurements are from one cluster during one pause. I have no measurement from a cluster with one supervisor seat, where the fan-out costs nothing.
- The relevance counts ("6 for ops", "about 20 relevant") are each seat's own judgement, not a field in the envelopes.
- The 90k-character batch size comes from both reports and was not re-measured by me.

## Addendum 2026-10-04: a second team's view, and what the tools cap

**Observed by the errand team's report.** On the same cluster, that team's supervisor drained 182 held messages on 2026-10-02 and 321 on 2026-10-04, and none of them was addressed to that seat. Every drain at `max` 50 overflowed the tool-result limit, at 86k to 109k characters per batch. This is a third seat reporting the same fact as section 2: on a shared global supervisor address, most of what a supervisor reads is someone else's.

**What the tools bound, checked at director origin/main 97dfcf9.** A `wait_for_message` batch is capped at 50 messages (`maxDrain`, `probe/nats-phase-0/director-mcp/drain.go:39`); the comment there sizes the worst case "at a few MiB". So at the batch limit the size is bounded by envelope count, not by the caller's tool-result limit, and three seats have now hit that limit at 50. `inbox_summary` reads 200 waiting messages by default (`summaryDefault`, `drain.go:42`), up to 500 when asked (`summaryMax`, `drain.go:43`, clamped at `tools.go:331-336`), and marks its result `partial` when it read fewer than the consumer reports waiting (`tools.go:349`). The errand team's report describes this as a cap at 200; it is the default, and a caller can raise it.

**Judgment, not observed design.** The errand team runs its supervisor role with two replicas. Its report says every ask addressed `FOR` that team's supervisor reached both seats, and the two seats split the work by PROPOSE three times (local seqs 305, 722 and 1203). That is the same fan-out one level down, inside a team: the address names a role, and a role with two replicas has two holders. The report's view is that a seat-level or role-level singleton claim would settle it. I record that as the reporting team's judgment; I did not observe the split.
